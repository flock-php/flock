namespace Flock.Tray.Infrastructure;

/// <summary>Shares equivalent reads while allowing a newer generation to supersede an older one.</summary>
public sealed class SharedRead<T>(IUiDispatcher? dispatcher = null) : IAsyncDisposable
{
    private readonly Lock gate = new();
    private readonly HashSet<Task> retired = [];
    private Task? current;
    private CancellationTokenSource? currentCancellation;
    private long generation;

    public async ValueTask DisposeAsync()
    {
        Task[] tasks;
        lock (gate)
        {
            currentCancellation?.Cancel();
            tasks = [.. retired];
        }
        if (tasks.Length > 0)
        {
            await Task.WhenAll(tasks);
        }
    }

    public Task JoinReadAsync(
        Func<CancellationToken, Task<T>> load,
        Action<T> publish,
        Action<Exception> publishFailure,
        CancellationToken observerToken,
        CancellationToken applicationToken = default,
        CancellationToken lifetimeToken = default)
    {
        ArgumentNullException.ThrowIfNull(load);
        ArgumentNullException.ThrowIfNull(publish);
        ArgumentNullException.ThrowIfNull(publishFailure);
        Task task;
        lock (gate)
        {
            if (current is null || current.IsCompleted)
            {
                task = StartLocked(load, publish, publishFailure, applicationToken, lifetimeToken);
            }
            else
            {
                task = current;
            }
        }
        return task.WaitAsync(observerToken);
    }

    public Task RunReadAsync(
        Func<CancellationToken, Task<T>> load,
        Action<T> publish,
        Action<Exception> publishFailure,
        CancellationToken applicationToken = default,
        CancellationToken lifetimeToken = default)
    {
        lock (gate)
        {
            currentCancellation?.Cancel();
            return StartLocked(load, publish, publishFailure, applicationToken, lifetimeToken);
        }
    }

    public void Invalidate()
    {
        lock (gate)
        {
            generation++;
            currentCancellation?.Cancel();
            current = null;
        }
    }

    public void StartRead(Func<CancellationToken, Task<T>> load, Action<T> publish,
        Action<Exception> publishFailure, CancellationToken applicationToken = default, CancellationToken lifetimeToken = default)
    {
        _ = RunReadAsync(load, publish, publishFailure, applicationToken, lifetimeToken);
    }

    private Task StartLocked(Func<CancellationToken, Task<T>> load, Action<T> publish, Action<Exception> publishFailure,
        CancellationToken applicationToken, CancellationToken lifetimeToken)
    {
        var request = CancellationTokenSource.CreateLinkedTokenSource(applicationToken, lifetimeToken);
        currentCancellation = request;
        var identity = ++generation;
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        current = completion.Task;
        retired.Add(completion.Task);
        _ = RunAsync(request, identity, load, publish, publishFailure, completion);
        return completion.Task;
    }

    private async Task RunAsync(CancellationTokenSource request, long identity, Func<CancellationToken, Task<T>> load,
        Action<T> publish, Action<Exception> publishFailure, TaskCompletionSource completion)
    {
        try
        {
            T value;
            try
            {
                value = await load(request.Token);
            }
            catch (Exception error) when (error is not OperationCanceledException)
            {
                if (IsCurrent(identity, request))
                {
                    await PublishAsync(() =>
                    {
                        if (IsCurrent(identity, request))
                        {
                            publishFailure(error);
                        }
                    });
                }
                return;
            }

            if (IsCurrent(identity, request))
            {
                await PublishAsync(() =>
                {
                    if (IsCurrent(identity, request))
                    {
                        publish(value);
                    }
                });
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error)
        {
            completion.TrySetException(error);
        }
        finally
        {
            lock (gate)
            {
                retired.Remove(completion.Task);
                if (ReferenceEquals(currentCancellation, request))
                {
                    currentCancellation = null;
                }
            }
            request.Dispose();
            completion.TrySetResult();
        }
    }

    private bool IsCurrent(long identity, CancellationTokenSource request)
    {
        lock (gate)
        {
            return identity == generation && ReferenceEquals(currentCancellation, request) && !request.IsCancellationRequested;
        }
    }

    private Task PublishAsync(Action action)
    {
        if (dispatcher is not null)
        {
            return dispatcher.InvokeAsync(action, CancellationToken.None);
        }
        action();
        return Task.CompletedTask;
    }
}