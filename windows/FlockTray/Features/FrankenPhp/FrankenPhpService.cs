using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.FrankenPhp;

/// <summary>Owns FrankenPHP independently from PHP-CGI, including unsupported-daemon handling.</summary>
public sealed class FrankenPhpService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly SharedRead<FrankenPhpState?> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private readonly OperationErrors catalogErrors = new();
    private FrankenPhpState? current;

    public FrankenPhpService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<FrankenPhpState?>(dispatcher);
        catalogErrors.PropertyChanged += (_, _) => Changed?.Invoke();
        runner.RunningChanged += RunnerChanged;
    }
    public FrankenPhpState? Current => current;
    public string? RefreshError => catalogErrors.Refresh;
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null) ?? catalogErrors.Action;
    public string? Error => ActionError ?? RefreshError;
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string minor) => runner.IsRunning(Key(minor));
    public bool IsChecking => runner.IsRunning("frankenphp:catalog");

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(ReadAsync, Apply, error => catalogErrors.SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> InstallAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.InstallFrankenPhpAsync(minor, cancellation), token);

    public Task<OperationOutcome> CancelAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.CancelFrankenPhpJobAsync(minor, cancellation), token);

    public Task<OperationOutcome> DismissAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.DismissFrankenPhpJobAsync(minor, cancellation), token);

    public Task<OperationOutcome> UninstallAsync(string minor, CancellationToken token = default) =>
        MutateAsync(minor, cancellation => client.UninstallFrankenPhpAsync(minor, cancellation), token);

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) =>
        MutateCatalogAsync(cancellation => client.CheckFrankenPhpAsync(cancellation), token);

    public Task<string> ReadLogAsync(string minor, CancellationToken token = default) => client.FrankenPhpLogAsync(minor, token);

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

    private async Task<FrankenPhpState?> ReadAsync(CancellationToken token)
    {
        try
        {
            return await client.FrankenPhpAsync(token);
        }
        catch (FlockServerException error) when (error.StatusCode is 404 or 501)
        {
            return null;
        }
    }

    private async Task<OperationOutcome> MutateAsync(string minor, Func<CancellationToken, Task<FrankenPhpState>> mutation, CancellationToken applicationToken)
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

    private async Task<OperationOutcome> MutateCatalogAsync(Func<CancellationToken, Task<FrankenPhpState>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync("frankenphp:catalog", catalogErrors, async token =>
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

    private void Apply(FrankenPhpState? value)
    {
        if (!Same(current, value))
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
        if (key == "frankenphp:catalog" || key.StartsWith("frankenphp:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private static string Key(string minor) => "frankenphp:" + minor;

    private static bool Same(FrankenPhpState? left, FrankenPhpState? right) =>
        (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            ({ } a, { } b) => a.Available == b.Available && a.CheckedAt == b.CheckedAt && a.CheckError == b.CheckError && a.Builds.Count == b.Builds.Count &&
                a.Builds.Zip(b.Builds).All(pair => pair.First.Php == pair.Second.Php && pair.First.Installed == pair.Second.Installed && pair.First.PhpVersion == pair.Second.PhpVersion &&
                    pair.First.Latest == pair.Second.Latest && pair.First.Update == pair.Second.Update && pair.First.Size == pair.Second.Size && pair.First.Prerelease == pair.Second.Prerelease &&
                    pair.First.Yanked == pair.Second.Yanked && SameJob(pair.First.Job, pair.Second.Job)),
        };

    private static bool SameJob(ComponentJob? left, ComponentJob? right) =>
        (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            ({ } a, { } b) => a.Action == b.Action && a.State == b.State && a.Phase == b.Phase && a.Progress == b.Progress && a.Error == b.Error && a.StartedAt == b.StartedAt,
        };
}