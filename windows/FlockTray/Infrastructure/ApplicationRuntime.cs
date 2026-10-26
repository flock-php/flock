namespace Flock.Tray.Infrastructure;

public interface IShutdownRequest
{
    void RequestQuit();
}

/// <summary>Owns admission and the single application shutdown completion.</summary>
public sealed class ApplicationRuntime : IShutdownRequest
{
    private readonly object gate = new();
    private readonly Action stopHost;
    private Task? quit;
    private TaskCompletionSource? quitCompletion;
    private bool accepting = true;
    private bool stopSignaled;

    public ApplicationRuntime(Action stopHost) => this.stopHost = stopHost;

    public bool IsAccepting
    {
        get
        {
            lock (gate)
            {
                return accepting;
            }
        }
    }

    /// <summary>Closes new work admission and signals host stopping without waiting.</summary>
    public void RequestQuit()
    {
        var signal = false;
        lock (gate)
        {
            accepting = false;
            if (!stopSignaled)
            {
                stopSignaled = true;
                signal = true;
            }
        }
        if (signal)
        {
            stopHost();
        }
    }

    /// <summary>Returns one shared completion task for the owner that must join shutdown.</summary>
    public Task QuitAsync(Func<Task> cleanup)
    {
        TaskCompletionSource? start = null;
        lock (gate)
        {
            if (quit is null)
            {
                quitCompletion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                quit = quitCompletion.Task;
                start = quitCompletion;
            }
        }
        if (start is not null)
        {
            _ = CompleteQuitAsync(start, cleanup);
        }
        return quit;
    }

    private static async Task CompleteQuitAsync(TaskCompletionSource completion, Func<Task> cleanup)
    {
        try
        {
            await cleanup();
            completion.TrySetResult();
        }
        catch (Exception error)
        {
            completion.TrySetException(error);
        }
    }
}