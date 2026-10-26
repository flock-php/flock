using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Configuration;

/// <summary>Owns configuration reads and named settings mutations.</summary>
public sealed class ConfigurationService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly OperationErrors errors = new();
    private readonly SharedRead<FlockConfig> request;
    private FlockConfig? current;

    public ConfigurationService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<FlockConfig>(dispatcher);
        errors.PropertyChanged += (_, _) => Changed?.Invoke();
    }

    public FlockConfig? Current => current;
    public string? ActionError => errors.Action;
    public string? RefreshError => errors.Refresh;
    public string? Error => errors.DisplayedMessage;
    public bool IsRunning => runner.IsRunning("settings");
    public Func<Task>? OnChange { get; set; }

    public event Action? Changed;

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.ConfigAsync, Apply, PublishRefreshFailure, observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> SetDefaultPhpAsync(string version, CancellationToken applicationToken = default) =>
        UpdateAsync(new SettingsChange(DefaultPhp: version), applicationToken);

    public Task<OperationOutcome> SetDefaultNodeAsync(string major, CancellationToken applicationToken = default) =>
        UpdateAsync(new SettingsChange(DefaultNode: major), applicationToken);

    public Task<OperationOutcome> SetChannelAsync(string channel, CancellationToken applicationToken = default) =>
        UpdateAsync(new SettingsChange(Channel: channel), applicationToken);

    public async Task<OperationOutcome> UpdateAsync(SettingsChange change, CancellationToken applicationToken = default)
    {
        var outcome = await runner.RunMutationAsync("settings", errors, async token =>
        {
            request.Invalidate();
            try
            {
                await client.UpdateSettingsAsync(change, token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync(applicationToken);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    public void DismissError()
    {
        errors.ClearAction();
        errors.ClearRefresh();
    }

    private void Apply(FlockConfig value)
    {
        if (current is not null && Same(current, value))
        {
            errors.ClearRefresh();
            return;
        }
        current = value;
        errors.ClearRefresh();
        Changed?.Invoke();
    }

    private void PublishRefreshFailure(Exception error)
    {
        errors.SetRefresh(error);
    }

    private static bool Same(FlockConfig left, FlockConfig right) =>
        left.Tld == right.Tld && left.DefaultPhp == right.DefaultPhp && left.DefaultNode == right.DefaultNode &&
        left.Xdebug == right.Xdebug && left.Dumps == right.Dumps && left.Channel == right.Channel && left.Root == right.Root &&
        left.DumpsInPage == right.DumpsInPage && left.CaptureOff.SequenceEqual(right.CaptureOff) &&
        left.Ports.Http == right.Ports.Http && left.Ports.Https == right.Ports.Https && left.Ports.Dns == right.Ports.Dns &&
        left.Ports.Smtp == right.Ports.Smtp && left.Ports.Postgres == right.Ports.Postgres && left.Ports.Valkey == right.Ports.Valkey &&
        left.Ports.Dumps == right.Ports.Dumps && left.Ports.Api == right.Ports.Api &&
        left.Parked.SequenceEqual(right.Parked) && left.Node.SequenceEqual(right.Node) &&
        left.Php.Select(p => (p.Version, p.FullVersion, p.Ini, p.XdebugAvailable)).SequenceEqual(
            right.Php.Select(p => (p.Version, p.FullVersion, p.Ini, p.XdebugAvailable)));

    public ValueTask DisposeAsync() => request.DisposeAsync();
}
