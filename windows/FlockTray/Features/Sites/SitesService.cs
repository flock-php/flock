using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Sites;

/// <summary>Owns site mutations and extension reads; accepted status GETs remain the site snapshot owner.</summary>
public sealed class SitesService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly Dictionary<string, OperationErrors> errors = [];
    private readonly SharedRead<ExtensionReport> extensionRequest;
    private readonly object extensionGate = new();
    private string? extensionSite;
    private ExtensionReport? extensionReport;
    private string? extensionError;

    public SitesService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        extensionRequest = new SharedRead<ExtensionReport>(dispatcher);
        runner.RunningChanged += RunningChanged;
    }

    /// <summary>Refreshes the shared status snapshot after a site action, including an uncertain failure.</summary>
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string site) => runner.IsRunning(Key(site));

    public string? Error => errors.Values.Select(value => value.DisplayedMessage).FirstOrDefault(value => value is not null);
    public string? ActionError => errors.Values.Select(value => value.Action).FirstOrDefault(value => value is not null);

    public string? ErrorFor(string resource) => errors.TryGetValue(resource, out var value) ? value.DisplayedMessage : null;

    public event Action? Changed;

    public Task<OperationOutcome> LinkAsync(string path, CancellationToken token = default) =>
        MutateAsync("link:" + path, path, (_, cancellationToken) => client.LinkSiteAsync(path, cancellationToken: cancellationToken), token);

    public Task<OperationOutcome> UnlinkAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UnlinkSiteAsync(site, cancellationToken), token);

    public Task<OperationOutcome> UpdateAsync(string site, SiteChange change, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UpdateSiteAsync(site, change, cancellationToken), token);

    public Task<OperationOutcome> RestartAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RestartSiteAsync(site, cancellationToken), token);

    public Task<OperationOutcome> AddDomainAsync(string site, string domain, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.AddDomainAsync(site, domain, cancellationToken), token);

    public Task<OperationOutcome> RemoveDomainAsync(string site, string domain, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RemoveDomainAsync(site, domain, cancellationToken), token);

    public Task<OperationOutcome> PinAsync(string site, string tool, string version, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.PinAsync(site, tool, version, cancellationToken), token);

    public Task<OperationOutcome> UnpinAsync(string site, string tool, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UnpinAsync(site, tool, cancellationToken), token);

    public Task<OperationOutcome> AddBoostGuidelineAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.AddBoostGuidelineAsync(site, cancellationToken), token);

    public Task<OperationOutcome> StartShareAsync(string site, IReadOnlyList<string> allow, bool debug, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.StartShareAsync(site, allow, debug, cancellationToken), token);

    public Task<OperationOutcome> RebuildShareAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RebuildShareAsync(site, cancellationToken), token);

    public Task<OperationOutcome> StopShareAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.StopShareAsync(site, cancellationToken), token);

    /// <summary>Reads one selected site's extensions. A new site retires the prior read; equivalent reads join.</summary>
    public async Task<(ExtensionReport? Report, string? Error)> SiteExtensionsAsync(string site, CancellationToken token = default)
    {
        Task read;
        lock (extensionGate)
        {
            if (extensionSite == site)
            {
                read = extensionRequest.JoinReadAsync(clientToken => client.SiteExtensionsAsync(site, clientToken),
                    value => PublishExtension(site, value, null), error => PublishExtension(site, null, error.Message), token);
            }
            else
            {
                extensionSite = site;
                extensionReport = null;
                extensionError = null;
                extensionRequest.Invalidate();
                read = extensionRequest.RunReadAsync(clientToken => client.SiteExtensionsAsync(site, clientToken),
                    value => PublishExtension(site, value, null), error => PublishExtension(site, null, error.Message), token);
            }
        }
        await read;
        lock (extensionGate)
        {
            return extensionSite == site ? (extensionReport, extensionError) : (null, null);
        }
    }

    /// <summary>Requires an extension, then reloads its report through the same selected-report owner.</summary>
    public async Task<(ExtensionReport? Report, string? Error)> RequireExtensionAsync(string site, string name, CancellationToken token = default)
    {
        var outcome = await MutateAsync(Key(site), site, async (_, cancellationToken) =>
        {
            extensionRequest.Invalidate();
            await client.RequireExtensionAsync(site, name, cancellationToken);
        }, token);
        if (outcome is OperationOutcome.Rejected or OperationOutcome.Canceled)
        {
            return (null, outcome == OperationOutcome.Canceled ? "Extension request canceled." : null);
        }
        return await SiteExtensionsAsync(site, token);
    }

    public void DismissError(string resource)
    {
        if (errors.TryGetValue(resource, out var value))
        {
            value.ClearAction();
            value.ClearRefresh();
        }
    }

    public void DismissError()
    {
        foreach (var value in errors.Values)
        {
            value.ClearAction();
            value.ClearRefresh();
        }
    }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunningChanged;
        await extensionRequest.DisposeAsync();
    }

    private async Task<OperationOutcome> MutateAsync(string key, string resource, Func<FlockClient, CancellationToken, Task> mutation,
        CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(key, ErrorsFor(resource), token => mutation(client, token), applicationToken);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    private OperationErrors ErrorsFor(string resource)
    {
        if (errors.TryGetValue(resource, out var value))
        {
            return value;
        }
        value = new OperationErrors();
        value.PropertyChanged += (_, _) => Changed?.Invoke();
        errors.Add(resource, value);
        return value;
    }

    private void RunningChanged(string key, bool running)
    {
        if (key.StartsWith("site:", StringComparison.Ordinal) || key.StartsWith("link:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private void PublishExtension(string site, ExtensionReport? report, string? error)
    {
        lock (extensionGate)
        {
            if (extensionSite == site)
            {
                extensionReport = report;
                extensionError = error;
            }
        }
    }

    private static string Key(string site) => "site:" + site;
}
