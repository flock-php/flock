using Microsoft.UI.Dispatching;

namespace Flock.Tray.Infrastructure;

/// <summary>Runs UI publication and native cleanup on the WinUI dispatcher.</summary>
public interface IUiDispatcher
{
    Task InvokeAsync(Action callback, CancellationToken cancellationToken = default);
    Task InvokeAsync(Func<Task> callback, CancellationToken cancellationToken = default);
}

/// <summary>WinUI dispatcher adapter used by application services and window release.</summary>
public sealed class UiDispatcher(DispatcherQueue queue) : IUiDispatcher
{
    public Task InvokeAsync(Action callback, CancellationToken cancellationToken = default) => Enqueue(() =>
    {
        callback();
        return Task.CompletedTask;
    }, cancellationToken);

    public Task InvokeAsync(Func<Task> callback, CancellationToken cancellationToken = default) => Enqueue(callback, cancellationToken);

    private Task Enqueue(Func<Task> callback, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (queue.HasThreadAccess)
        {
            return callback();
        }
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!queue.TryEnqueue(async () =>
        {
            try
            {
                await callback();
                completion.TrySetResult();
            }
            catch (Exception e)
            {
                completion.TrySetException(e);
            }
        }))
        {
            completion.TrySetException(new InvalidOperationException("The UI dispatcher is unavailable."));
        }
        return completion.Task;
    }
}