using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Services;

/// <summary>Owns the service snapshot and per-service start, stop, and port mutations.</summary>
public sealed class ServicesService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly SharedRead<List<Service>> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private List<Service> current = [];
    private bool hasSnapshot;

    public ServicesService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<List<Service>>(dispatcher);
        runner.RunningChanged += RunnerChanged;
    }

    public IReadOnlyList<Service> Current => current;
    public bool HasSnapshot => hasSnapshot;
    public string? RefreshError => resourceErrors.Values.Select(error => error.Refresh).FirstOrDefault(error => error is not null);
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null);
    public string? Error => ActionError ?? RefreshError;
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string id) => runner.IsRunning(Key(id));

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.ServicesAsync, Apply, error => ErrorsFor("services").SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> StartAsync(string id, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.StartServiceAsync(id, cancellation), token);

    public Task<OperationOutcome> StopAsync(string id, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.StopServiceAsync(id, cancellation), token);

    public Task<OperationOutcome> SetPortAsync(string id, int port, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.SetServicePortAsync(id, port, cancellation), token);

    public void DismissError()
    {
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

    private async Task<OperationOutcome> MutateAsync(string id, Func<CancellationToken, Task<List<Service>>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(Key(id), ErrorsFor(id), async token =>
        {
            request.Invalidate();
            try
            {
                _ = await mutation(token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await request.RunReadAsync(client.ServicesAsync, Apply, error => ErrorsFor("services").SetRefresh(error), applicationToken);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    private void Apply(List<Service> services)
    {
        if (!Same(current, services))
        {
            current = services;
            Changed?.Invoke();
        }
        if (!hasSnapshot)
        {
            hasSnapshot = true;
            Changed?.Invoke();
        }
        ErrorsFor("services").ClearRefresh();
    }

    private OperationErrors ErrorsFor(string id)
    {
        if (resourceErrors.TryGetValue(id, out var error))
        {
            return error;
        }
        error = new OperationErrors();
        error.PropertyChanged += (_, _) => Changed?.Invoke();
        resourceErrors.Add(id, error);
        return error;
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key.StartsWith("service:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private static string Key(string id) => "service:" + id;

    private static bool Same(IReadOnlyList<Service> left, IReadOnlyList<Service> right) =>
        left.Count == right.Count && left.Zip(right).All(pair =>
            pair.First.Id == pair.Second.Id && pair.First.Name == pair.Second.Name && pair.First.Category == pair.Second.Category &&
            pair.First.Description == pair.Second.Description && pair.First.Available == pair.Second.Available && pair.First.Unavailable == pair.Second.Unavailable &&
            pair.First.Version == pair.Second.Version && pair.First.Port == pair.Second.Port && pair.First.Installed == pair.Second.Installed &&
            pair.First.Enabled == pair.Second.Enabled && pair.First.Running == pair.Second.Running && pair.First.Stoppable == pair.Second.Stoppable &&
            pair.First.Env == pair.Second.Env && pair.First.Log == pair.Second.Log && pair.First.Url == pair.Second.Url && pair.First.Problem == pair.Second.Problem);
}