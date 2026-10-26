using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Mcp;

/// <summary>Owns MCP reads and one mutation lane per client.</summary>
public sealed class McpService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly OperationErrors errors = new();
    private readonly Dictionary<string, OperationErrors> clientErrors = [];
    private readonly SharedRead<McpState> request;
    private McpState? current;

    public McpService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        request = new SharedRead<McpState>(dispatcher);
        errors.PropertyChanged += (_, _) => Changed?.Invoke();
        runner.RunningChanged += RunnerChanged;
    }

    public McpState? Current => current;
    public string? ActionError => clientErrors.Values.Select(value => value.Action).FirstOrDefault(value => value is not null);
    public string? RefreshError => errors.Refresh;
    public string? Error => ActionError ?? RefreshError;
    public event Action? Changed;

    public bool IsRunning(string id) => runner.IsRunning("mcp:" + id);

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.McpAsync, Apply, error => errors.SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> RegisterAsync(string id, CancellationToken applicationToken = default) => MutateAsync(id, add: true, applicationToken);

    public Task<OperationOutcome> UnregisterAsync(string id, CancellationToken applicationToken = default) => MutateAsync(id, add: false, applicationToken);

    public void DismissError()
    {
        foreach (var value in clientErrors.Values)
        {
            value.ClearAction();
        }
        errors.ClearRefresh();
    }

    private async Task<OperationOutcome> MutateAsync(string id, bool add, CancellationToken applicationToken)
    {
        var key = "mcp:" + id;
        var outcome = await runner.RunMutationAsync(key, ErrorsFor(id), async token =>
        {
            request.Invalidate();
            try
            {
                // The mutation response is deliberately ignored. GET is the only publication owner.
                if (add)
                {
                    await client.RegisterMcpAsync(id, token);
                }
                else
                {
                    await client.UnregisterMcpAsync(id, token);
                }
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync(applicationToken);
        }
        return outcome;
    }

    private void Apply(McpState value)
    {
        if (current is null || !Same(current, value))
        {
            current = value;
            Changed?.Invoke();
        }
        errors.ClearRefresh();
    }

    private static bool Same(McpState left, McpState right) =>
        left.Command == right.Command && left.Snippet == right.Snippet && left.Args.SequenceEqual(right.Args) &&
        left.Clients.Count == right.Clients.Count && left.Clients.Zip(right.Clients).All(pair =>
            pair.First.Id == pair.Second.Id && pair.First.Name == pair.Second.Name && pair.First.State == pair.Second.State &&
            pair.First.Config == pair.Second.Config && pair.First.Problem == pair.Second.Problem);

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunnerChanged;
        await request.DisposeAsync();
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key.StartsWith("mcp:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private OperationErrors ErrorsFor(string id)
    {
        if (clientErrors.TryGetValue(id, out var value))
        {
            return value;
        }
        value = new OperationErrors();
        value.PropertyChanged += (_, _) => Changed?.Invoke();
        clientErrors.Add(id, value);
        return value;
    }
}