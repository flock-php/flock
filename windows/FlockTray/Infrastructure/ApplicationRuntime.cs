namespace Flock.Tray.Infrastructure;

/// <summary>Owns work admission and the host stopping signal.</summary>
public sealed class ApplicationRuntime(Action stopHost)
{
    private readonly Lock gate = new();
    private bool accepting = true;
    private bool stopSignaled;

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
}