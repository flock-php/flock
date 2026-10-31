using Flock.Kit;
using Microsoft.Extensions.Hosting;

namespace Flock.Tray.Infrastructure;

/// <summary>Owns the daemon status loop and stops it with the host token.</summary>
public sealed class StatusPollingService(FlockClient client, Action<Exception>? fatal = null, IUiDispatcher? dispatcher = null) : BackgroundService, IAsyncDisposable
{
    private readonly SharedRead<Status> request = new(dispatcher);
    private readonly object gate = new();
    private int activeMutations;

    public Action<Status?>? StatusPublished { get; set; }

    public string? LastError { get; private set; }

    /// <summary>Retires an in-flight status read and blocks new reads while a daemon mutation runs.</summary>
    public void BeginMutation()
    {
        lock (gate)
        {
            activeMutations++;
        }
        request.Invalidate();
    }

    public void EndMutation()
    {
        lock (gate)
        {
            activeMutations = Math.Max(0, activeMutations - 1);
        }
    }

    public async Task RefreshAsync(CancellationToken cancellationToken = default)
    {
        lock (gate)
        {
            if (activeMutations > 0)
            {
                return;
            }
        }

        LastError = null;
        await request.RunReadAsync(
            client.StatusAsync,
            status => StatusPublished?.Invoke(status),
            error =>
            {
                LastError = error.Message;
                StatusPublished?.Invoke(null);
            },
            applicationToken: cancellationToken);
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await RefreshAsync(stoppingToken);
                await Task.Delay(TimeSpan.FromSeconds(2), stoppingToken);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                return;
            }
            catch (Exception error)
            {
                fatal?.Invoke(error);
                return;
            }
        }
    }

    public async ValueTask DisposeAsync()
    {
        await request.DisposeAsync();
        Dispose();
    }
}