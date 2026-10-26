namespace Flock.Tray.ViewModels;

/// <summary>Owns one cancellable activation per selected settings pane.</summary>
public sealed class SettingsNavigationViewModel : IAsyncDisposable
{
    private readonly object gate = new();
    private readonly Func<SettingsPane, CancellationToken, Task> activate;
    private CancellationTokenSource? currentCancellation;
    private Task? currentTask;
    private SettingsPane? selected;

    public SettingsNavigationViewModel(Func<SettingsPane, CancellationToken, Task> activate)
    {
        this.activate = activate;
    }

    public SettingsPane? Selected => selected;

    public Task ActivateAsync(SettingsPane pane)
    {
        lock (gate)
        {
            if (selected == pane)
            {
                return currentTask ?? Task.CompletedTask;
            }
            currentCancellation?.Cancel();
            var old = currentTask;
            var cancellation = new CancellationTokenSource();
            currentCancellation = cancellation;
            selected = pane;
            currentTask = ActivateCoreAsync(old, pane, cancellation);
            return currentTask;
        }
    }

    private async Task ActivateCoreAsync(Task? old, SettingsPane pane, CancellationTokenSource cancellation)
    {
        if (old is not null && !old.IsCompleted)
        {
            try
            {
                await old;
            }
            catch (OperationCanceledException)
            {
            }
        }
        try
        {
            await activate(pane, cancellation.Token);
        }
        catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
        {
        }
        finally
        {
            lock (gate)
            {
                if (ReferenceEquals(currentCancellation, cancellation))
                {
                    currentCancellation = null;
                }
            }
            cancellation.Dispose();
        }
    }

    public async ValueTask DisposeAsync()
    {
        Task? task;
        lock (gate)
        {
            currentCancellation?.Cancel();
            task = currentTask;
        }
        if (task is not null)
        {
            await task;
        }
    }
}

/// <summary>Window-level settings state shared by the retained navigation surface.</summary>
public sealed class SettingsWindowViewModel : IAsyncDisposable
{
    public SettingsWindowViewModel(Func<SettingsPane, CancellationToken, Task> activate)
    {
        Navigation = new SettingsNavigationViewModel(activate);
    }

    public SettingsNavigationViewModel Navigation { get; }

    public ValueTask DisposeAsync() => Navigation.DisposeAsync();
}