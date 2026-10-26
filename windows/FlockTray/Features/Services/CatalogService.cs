using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Services;

/// <summary>Owns catalog reads and independent mutation lanes for each catalog service.</summary>
public sealed class CatalogService : IAsyncDisposable
{
    private const string CheckKey = "catalog:check";
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly SharedRead<ServiceCatalog> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private ServiceCatalog? current;
    private bool hasSnapshot;

    public CatalogService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<ServiceCatalog>(dispatcher);
        runner.RunningChanged += RunnerChanged;
    }

    public ServiceCatalog? Current => current;
    public bool HasSnapshot => hasSnapshot;
    public string? RefreshError => ErrorsFor("catalog").Refresh;
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null);
    public string? Error => ActionError ?? RefreshError;
    public bool IsRunning(string id) => runner.IsRunning(Key(id));
    public bool IsChecking => runner.IsRunning(CheckKey);
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.CatalogAsync, Apply, error => ErrorsFor("catalog").SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> InstallAsync(string id, string? line = null, int? port = null, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.InstallCatalogServiceAsync(id, line, port, cancellation), token);

    public Task<OperationOutcome> CancelAsync(string id, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.CancelCatalogJobAsync(id, cancellation), token);

    public Task<OperationOutcome> DismissAsync(string id, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.DismissCatalogJobAsync(id, cancellation), token);

    public Task<OperationOutcome> ActivateAsync(string id, string line, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.ActivateCatalogServiceAsync(id, line, cancellation), token);

    public Task<OperationOutcome> UninstallAsync(string id, string? line, bool deleteData, CancellationToken token = default) =>
        MutateAsync(id, cancellation => client.UninstallCatalogServiceAsync(id, line, deleteData, cancellation), token);

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) =>
        MutateAsync(CheckKey, cancellation => client.CheckCatalogAsync(cancellation), token);

    public Task<string> ReadLogAsync(string id, CancellationToken token = default) => client.CatalogLogAsync(id, token);

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

    private async Task<OperationOutcome> MutateAsync(string id, Func<CancellationToken, Task<ServiceCatalog>> mutation, CancellationToken applicationToken)
    {
        var lane = id == CheckKey ? CheckKey : Key(id);
        var outcome = await runner.RunMutationAsync(lane, ErrorsFor(id), async token =>
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
            await request.RunReadAsync(client.CatalogAsync, Apply, error => ErrorsFor("catalog").SetRefresh(error), applicationToken);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    private void Apply(ServiceCatalog value)
    {
        if (current is null || !Same(current, value))
        {
            current = value;
            Changed?.Invoke();
        }
        ErrorsFor("catalog").ClearRefresh();
        if (!hasSnapshot)
        {
            hasSnapshot = true;
            Changed?.Invoke();
        }
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
        if (key == CheckKey || key.StartsWith("catalog:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private static string Key(string id) => "catalog:" + id;

    private static bool Same(ServiceCatalog left, ServiceCatalog right) =>
        left.CheckedAt == right.CheckedAt && left.CheckError == right.CheckError && left.Services.Count == right.Services.Count &&
        left.Services.Zip(right.Services).All(pair =>
            pair.First.Id == pair.Second.Id && pair.First.Name == pair.Second.Name && pair.First.Category == pair.Second.Category &&
            pair.First.Description == pair.Second.Description && pair.First.Available == pair.Second.Available && pair.First.Unavailable == pair.Second.Unavailable &&
            pair.First.Estimate == pair.Second.Estimate && pair.First.Installed == pair.Second.Installed && pair.First.Line == pair.Second.Line &&
            pair.First.Latest == pair.Second.Latest && pair.First.Update == pair.Second.Update && pair.First.Yanked == pair.Second.Yanked &&
            pair.First.Port == pair.Second.Port && pair.First.DefaultPort == pair.Second.DefaultPort &&
            pair.First.Lines.Count == pair.Second.Lines.Count && pair.First.Lines.Zip(pair.Second.Lines).All(lines =>
                lines.First.Id == lines.Second.Id && lines.First.Label == lines.Second.Label && lines.First.Latest == lines.Second.Latest &&
                lines.First.Installed == lines.Second.Installed && lines.First.Version == lines.Second.Version && lines.First.Active == lines.Second.Active &&
                lines.First.Update == lines.Second.Update && lines.First.Yanked == lines.Second.Yanked && lines.First.HasData == lines.Second.HasData) &&
            SameJob(pair.First.Job, pair.Second.Job));

    private static bool SameJob(CatalogJob? left, CatalogJob? right) =>
        (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            ({ } a, { } b) => a.Action == b.Action && a.State == b.State && a.Phase == b.Phase && a.Progress == b.Progress &&
                a.Error == b.Error && a.StartedAt == b.StartedAt && a.Version == b.Version && a.Line == b.Line,
        };
}