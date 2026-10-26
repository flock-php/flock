using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Services;

/// <summary>Owns PostgreSQL reads and the single lane for every PostgreSQL mutation.</summary>
public sealed class PostgresService : IAsyncDisposable
{
    private const string Key = "postgres";
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly SharedRead<PostgresVersions> request;
    private readonly OperationErrors errors = new();
    private PostgresVersions? current;
    private bool hasSnapshot;

    public PostgresService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<PostgresVersions>(dispatcher);
        errors.PropertyChanged += (_, _) => Changed?.Invoke();
        runner.RunningChanged += RunnerChanged;
    }

    public PostgresVersions? Current => current;
    public bool HasSnapshot => hasSnapshot;
    public string? RefreshError => errors.Refresh;
    public string? ActionError => errors.Action;
    public string? Error => errors.DisplayedMessage;
    public bool IsRunning => runner.IsRunning(Key);
    public bool IsChecking => runner.IsRunning(Key);
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.PostgresVersionsAsync, Apply, errors.SetRefresh, observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> InstallAsync(string major, CancellationToken token = default) =>
        MutateAsync(cancellation => client.InstallPostgresAsync(major, cancellation), token);

    public Task<OperationOutcome> StartAsync(CancellationToken token = default) =>
        MutateAsync(cancellation => client.StartServiceAsync("postgres", cancellation), token);

    public Task<OperationOutcome> StopAsync(CancellationToken token = default) =>
        MutateAsync(cancellation => client.StopServiceAsync("postgres", cancellation), token);

    public Task<OperationOutcome> SetPortAsync(int port, CancellationToken token = default) =>
        MutateAsync(cancellation => client.SetServicePortAsync("postgres", port, cancellation), token);

    /// <summary>Sets the PostgreSQL port first, then installs the first major in the same mutation lane.</summary>
    public Task<OperationOutcome> InstallFirstAsync(string major, int? port, int currentPort, CancellationToken token = default) =>
        MutateAsync(async cancellation =>
        {
            if (port is { } chosen && chosen != currentPort)
            {
                _ = await client.SetServicePortAsync("postgres", chosen, cancellation);
            }
            _ = await client.InstallPostgresAsync(major, cancellation);
        }, token);

    public Task<OperationOutcome> UpdateAsync(string major, CancellationToken token = default) =>
        MutateAsync(cancellation => client.UpdatePostgresAsync(major, cancellation), token);

    public Task<OperationOutcome> ActivateAsync(string major, CancellationToken token = default) =>
        MutateAsync(cancellation => client.ActivatePostgresAsync(major, cancellation), token);

    public Task<OperationOutcome> UninstallAsync(string major, bool deleteData, CancellationToken token = default) =>
        MutateAsync(cancellation => client.UninstallPostgresAsync(major, deleteData, cancellation), token);

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) =>
        MutateAsync(cancellation => client.CheckPostgresReleasesAsync(cancellation), token);

    public void DismissError()
    {
        errors.ClearAction();
        errors.ClearRefresh();
    }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunnerChanged;
        await request.DisposeAsync();
    }

    private async Task<OperationOutcome> MutateAsync(Func<CancellationToken, Task> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(Key, errors, async token =>
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
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await request.RunReadAsync(client.PostgresVersionsAsync, Apply, errors.SetRefresh, applicationToken);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    private void Apply(PostgresVersions value)
    {
        if (current is null || !Same(current, value))
        {
            current = value;
            Changed?.Invoke();
        }
        errors.ClearRefresh();
        if (!hasSnapshot)
        {
            hasSnapshot = true;
            Changed?.Invoke();
        }
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key == Key)
        {
            Changed?.Invoke();
        }
    }

    private static bool Same(PostgresVersions left, PostgresVersions right) =>
        left.Active == right.Active && left.Running == right.Running && left.Enabled == right.Enabled && left.Port == right.Port &&
        left.CheckedAt == right.CheckedAt && left.CheckError == right.CheckError && left.Majors.Count == right.Majors.Count &&
        left.Majors.Zip(right.Majors).All(pair =>
            pair.First.Major == pair.Second.Major && pair.First.Installed == pair.Second.Installed && pair.First.Latest == pair.Second.Latest &&
            pair.First.Update == pair.Second.Update && pair.First.Prerelease == pair.Second.Prerelease && pair.First.Yanked == pair.Second.Yanked &&
            pair.First.Active == pair.Second.Active && pair.First.HasData == pair.Second.HasData && pair.First.DataSize == pair.Second.DataSize &&
            pair.First.Eol == pair.Second.Eol && pair.First.Busy == pair.Second.Busy);
}