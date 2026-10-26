using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Php;

/// <summary>Owns the PHP catalog, its per-minor mutation lanes, and its read and action errors.</summary>
public sealed class PhpService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly SharedRead<PhpCatalog> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private readonly OperationErrors catalogErrors = new();
    private PhpCatalog? current;

    public PhpService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<PhpCatalog>(dispatcher);
        catalogErrors.PropertyChanged += (_, _) => Changed?.Invoke();
        runner.RunningChanged += RunnerChanged;
    }
    public PhpCatalog? Current => current;
    public string? RefreshError => catalogErrors.Refresh;
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null) ?? catalogErrors.Action;
    public string? Error => ActionError ?? RefreshError;
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string minor) => runner.IsRunning(Key(minor));
    public bool IsChecking => runner.IsRunning("php:catalog");

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.PhpVersionsAsync, Apply, error => catalogErrors.SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> InstallAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.InstallPhpAsync(minor, cancellation), token);

    public Task<OperationOutcome> CancelAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.CancelPhpInstallAsync(minor, cancellation), token);

    public Task<OperationOutcome> DismissAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.DismissPhpJobAsync(minor, cancellation), token);

    public Task<OperationOutcome> UninstallAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.UninstallPhpAsync(minor, cancellation), token);

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) =>
        MutateCatalogAsync(cancellation => client.CheckPhpReleasesAsync(cancellation), token);

    public Task<string> ReadLogAsync(string minor, CancellationToken token = default) => client.PhpInstallLogAsync(minor, token);

    public void DismissError()
    {
        catalogErrors.ClearAction();
        catalogErrors.ClearRefresh();
        foreach (var error in resourceErrors.Values)
        {
            error.ClearAction();
            error.ClearRefresh();
        }
    }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunnerChanged;
        await request.DisposeAsync();
    }

    private async Task<OperationOutcome> MutateAsync(string minor, Func<CancellationToken, Task<PhpCatalog>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(Key(minor), ErrorsFor(minor), async token =>
        {
            request.Invalidate();
            try
            {
                await mutation(token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        await FollowUpAsync(outcome, applicationToken);
        return outcome;
    }

    private async Task<OperationOutcome> MutateCatalogAsync(Func<CancellationToken, Task<PhpCatalog>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync("php:catalog", catalogErrors, async token =>
        {
            request.Invalidate();
            try
            {
                await mutation(token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        await FollowUpAsync(outcome, applicationToken);
        return outcome;
    }

    private async Task FollowUpAsync(OperationOutcome outcome, CancellationToken token)
    {
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync(token);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
    }

    private void Apply(PhpCatalog value)
    {
        if (current is null || !Same(current, value))
        {
            current = value;
            Changed?.Invoke();
        }
        catalogErrors.ClearRefresh();
    }

    private OperationErrors ErrorsFor(string minor)
    {
        if (resourceErrors.TryGetValue(minor, out var error))
        {
            return error;
        }
        error = new OperationErrors();
        error.PropertyChanged += (_, _) => Changed?.Invoke();
        resourceErrors.Add(minor, error);
        return error;
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key == "php:catalog" || key.StartsWith("php:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private static string Key(string minor) => "php:" + minor;

    private static bool Same(PhpCatalog left, PhpCatalog right) =>
        left.CheckedAt == right.CheckedAt && left.CheckError == right.CheckError && left.Versions.Count == right.Versions.Count &&
        left.Versions.Zip(right.Versions).All(pair => pair.First.Minor == pair.Second.Minor && pair.First.Latest == pair.Second.Latest &&
            pair.First.Installed == pair.Second.Installed && pair.First.IsDefault == pair.Second.IsDefault && pair.First.Status == pair.Second.Status &&
            pair.First.Update == pair.Second.Update && pair.First.Size == pair.Second.Size && pair.First.Prerelease == pair.Second.Prerelease &&
            pair.First.Yanked == pair.Second.Yanked && pair.First.Xdebug == pair.Second.Xdebug && SameJob(pair.First.Job, pair.Second.Job));

    private static bool SameJob(ComponentJob? left, ComponentJob? right) =>
        (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            ({ } a, { } b) => a.Action == b.Action && a.State == b.State && a.Phase == b.Phase && a.Progress == b.Progress && a.Error == b.Error && a.StartedAt == b.StartedAt,
        };
}