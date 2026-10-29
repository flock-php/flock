namespace Flock.Tray.Infrastructure;

/// <summary>Owns accepted install work until it completes or the application shuts down.</summary>
internal sealed class AcceptedInstallTracker(Action<Exception> log)
{
    private readonly CancellationTokenSource stopping = new();
    private Task installs = Task.CompletedTask;

    public void Start(Func<CancellationToken, Task> install)
    {
        installs = Task.WhenAll(installs, ObserveAsync(install));
    }

    public async Task StopAsync()
    {
        stopping.Cancel();
        await installs;
    }

    private async Task ObserveAsync(Func<CancellationToken, Task> install)
    {
        try
        {
            await install(stopping.Token);
        }
        catch (OperationCanceledException) when (stopping.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            log(error);
        }
    }
}
