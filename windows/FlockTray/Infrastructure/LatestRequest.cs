namespace Flock.Tray.Infrastructure;

/// <summary>Shares equivalent reads while allowing a newer generation to supersede an older one.</summary>
public sealed class SharedRead<T> : IAsyncDisposable
{
    private readonly object gate = new();
    private readonly IUiDispatcher? dispatcher;
    private readonly HashSet<Task> retired = [];
    private CancellationTokenSource? currentCancellation;
    private Task? current;
    private long generation;

    public SharedRead(IUiDispatcher? dispatcher = null)
    {
        this.dispatcher = dispatcher;
    }

    public Task JoinReadAsync(Func<CancellationToken, Task<T>> load, Action<T> publish,
        Action<Exception> publishFailure, CancellationToken observerToken, CancellationToken applicationToken = default,
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

    public Task RunReadAsync(Func<CancellationToken, Task<T>> load, Action<T> publish,
        Action<Exception> publishFailure, CancellationToken applicationToken = default, CancellationToken lifetimeToken = default)
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
        Action<Exception> publishFailure, CancellationToken applicationToken = default, CancellationToken lifetimeToken = default) =>
        _ = RunReadAsync(load, publish, publishFailure, applicationToken, lifetimeToken);

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
}



/// <summary>Retires stale reads and prevents their results from publishing after a newer generation.</summary>
public sealed class LatestRequest : IAsyncDisposable
{
    private readonly object gate = new();
    private readonly HashSet<Task> retired = [];
    private CancellationTokenSource? current;
    private long generation;

    public void Invalidate()
    {
        lock (gate)
        {
            generation++;
            current?.Cancel();
        }
    }

    public async Task RunReadAsync<T>(Func<CancellationToken, Task<T>> read, Action<T> publish, CancellationToken applicationToken,
        CancellationToken lifetimeToken = default, Action<Exception>? publishFailure = null)
    {
        CancellationTokenSource request;
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        long identity;
        lock (gate)
        {
            generation++;
            current?.Cancel();
            request = CancellationTokenSource.CreateLinkedTokenSource(applicationToken, lifetimeToken);
            current = request;
            retired.Add(completion.Task);
            identity = generation;
        }
        try
        {
            T result;
            try
            {
                result = await read(request.Token);
            }
            catch (Exception error) when (error is not OperationCanceledException)
            {
                var currentRequest = false;
                lock (gate)
                {
                    currentRequest = identity == generation && !request.IsCancellationRequested && ReferenceEquals(current, request);
                }
                if (currentRequest && publishFailure is not null)
                {
                    publishFailure(error);
                    return;
                }
                throw;
            }
            var publishResult = false;
            lock (gate)
            {
                publishResult = identity == generation && !request.IsCancellationRequested && ReferenceEquals(current, request);
            }
            if (publishResult)
            {
                publish(result);
            }
        }
        finally
        {
            lock (gate)
            {
                current = ReferenceEquals(current, request) ? null : current;
                retired.Remove(completion.Task);
            }
            request.Dispose();
            completion.TrySetResult();
        }
    }

    public async ValueTask DisposeAsync()
    {
        Task[] tasks;
        lock (gate)
        {
            current?.Cancel();
            tasks = [.. retired];
        }
        await Task.WhenAll(tasks);
        current?.Dispose();
    }
}