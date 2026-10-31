namespace Flock.Tray.Infrastructure;

public enum OperationOutcome { Succeeded, Failed, Rejected, Canceled }

/// <summary>Admits keyed mutations once, observes their faults, and releases every key in a finally block.</summary>
public sealed class OperationRunner
{
    private readonly object gate = new();
    private readonly Dictionary<string, Task> running = [];

    public event Action<string, bool>? RunningChanged;

    public bool IsRunning(string key)
    {
        lock (gate)
        {
            return running.ContainsKey(key);
        }
    }

    /// <summary>Runs one mutation. Duplicate callers are rejected before their delegates run.</summary>
    public async Task<OperationOutcome> RunMutationAsync(
        string key,
        OperationErrors errors,
        Func<CancellationToken, Task> operation,
        CancellationToken applicationToken)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        lock (gate)
        {
            if (running.ContainsKey(key))
            {
                return OperationOutcome.Rejected;
            }
            running.Add(key, completion.Task);
        }
        try
        {
            RunningChanged?.Invoke(key, true);
            await operation(applicationToken);
            errors.ClearAction();
            return OperationOutcome.Succeeded;
        }
        catch (OperationCanceledException) when (applicationToken.IsCancellationRequested)
        {
            return OperationOutcome.Canceled;
        }
        catch (Exception error)
        {
            errors.SetAction(error);
            return OperationOutcome.Failed;
        }
        finally
        {
            lock (gate)
            {
                running.Remove(key);
            }
            completion.TrySetResult();
            RunningChanged?.Invoke(key, false);
        }
    }

    /// <summary>Waits for all accepted mutations without admitting a new operation.</summary>
    public async Task DrainAsync(CancellationToken cancellationToken = default)
    {
        while (true)
        {
            Task[] current;
            lock (gate)
            {
                current = [.. running.Values];
            }
            if (current.Length == 0)
            {
                return;
            }
            await Task.WhenAll(current).WaitAsync(cancellationToken);
        }
    }
}