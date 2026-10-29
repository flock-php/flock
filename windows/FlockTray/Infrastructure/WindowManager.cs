using Flock.Tray.ViewModels;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
namespace Flock.Tray.Infrastructure;

public interface IWindowRoot : IAsyncDisposable
{
    Window Window { get; }
    event Action<WindowClosingEventArgs> Closing;
    event Action Closed;
    void Activate();
    void Close();
}

public sealed class WindowClosingEventArgs
{
    public bool Cancel { get; set; }
}

public interface IWindowFactory
{
    IWindowRoot Create(WindowKind kind);
}

/// <summary>Creates each concrete root in an explicit scope and keeps UI construction in one switch.</summary>
public sealed class WindowFactory(IServiceScopeFactory scopes, RuntimeService model, DeepLinkCoordinator deepLinks) : IWindowFactory
{
    public IWindowRoot Create(WindowKind kind)
    {
        var scope = scopes.CreateAsyncScope();
        try
        {
            Window window = kind switch
            {
                WindowKind.Mail => new MailWindow(scope.ServiceProvider.GetRequiredService<MailViewModel>()),
                WindowKind.Dumps => new DumpsWindow(scope.ServiceProvider.GetRequiredService<DumpsViewModel>()),
                WindowKind.Logs => new LogsWindow(scope.ServiceProvider.GetRequiredService<LogsViewModel>()),
                WindowKind.Setup => new SetupWindow(model),
                WindowKind.Settings => new SettingsWindow(model, deepLinks),
                _ => throw new ArgumentOutOfRangeException(nameof(kind))
            };
            return new WindowRoot(window, scope);
        }
        catch
        {
            scope.DisposeAsync().AsTask().GetAwaiter().GetResult();
            throw;
        }
    }

    private sealed class WindowRoot : IWindowRoot
    {
        private readonly AsyncServiceScope scope;

        public WindowRoot(Window window, AsyncServiceScope scope)
        {
            Window = window;
            this.scope = scope;
            window.AppWindow.Closing += WindowClosing;
            window.Closed += WindowClosed;
        }

        public Window Window { get; }

        public event Action<WindowClosingEventArgs>? Closing;
        public event Action? Closed;

        public void Activate()
        {
            Window.Activate();
        }

        public void Close()
        {
            Window.Close();
        }

        public async ValueTask DisposeAsync()
        {
            if (Window is FlockWindow flockWindow)
            {
                await flockWindow.ReleaseAsync();
            }
            await scope.DisposeAsync();
        }

        private void WindowClosing(AppWindow sender, AppWindowClosingEventArgs args)
        {
            var closing = new WindowClosingEventArgs();
            Closing?.Invoke(closing);
            args.Cancel = closing.Cancel;
        }

        private void WindowClosed(object sender, WindowEventArgs args)
        {
            Closed?.Invoke();
        }
    }
}

/// <summary>Owns one root and scope per window kind and joins release before native destruction.</summary>
public sealed class WindowManager(IWindowFactory factory, IUiDispatcher dispatcher)
{
    private readonly Dictionary<WindowKind, Entry> entries = [];

    private readonly object gate = new();

    public Window Open(WindowKind kind)
    {
        lock (gate)
        {
            if (entries.TryGetValue(kind, out var existing))
            {
                if (existing.CloseTask is null)
                {
                    existing.Root.Activate();
                    return existing.Root.Window;
                }
                if (!existing.CloseTask.IsCompletedSuccessfully)
                {
                    throw new InvalidOperationException($"The {kind} window is still closing.");
                }
                entries.Remove(kind);
            }
            var entry = new Entry(factory.Create(kind));
            entries.Add(kind, entry);
            entry.Root.Closing += args =>
            {
                if (!entry.AllowClose)
                {
                    args.Cancel = true;
                    _ = ObserveCloseAsync(kind, entry);
                }
            };
            entry.Root.Closed += () => RemoveClosed(kind, entry);
            entry.Root.Activate();
            return entry.Root.Window;
        }
    }

    /// <summary>Opens a kind after an older instance finishes its asynchronous release.</summary>
    public async Task<Window> OpenAsync(WindowKind kind)
    {
        Task? closing;
        lock (gate)
        {
            closing = entries.TryGetValue(kind, out var entry) ? entry.CloseTask : null;
        }
        if (closing is not null)
        {
            await closing;
        }
        return Open(kind);
    }

    private async Task ObserveCloseAsync(WindowKind kind, Entry entry)
    {
        try
        {
            await CloseAsync(kind, entry);
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    public T? Get<T>(WindowKind kind) where T : Window
    {
        return entries.TryGetValue(kind, out var entry) ? entry.Root.Window as T : null;
    }

    public Task CloseAsync(WindowKind kind)
    {
        lock (gate)
        {
            return entries.TryGetValue(kind, out var entry) ? CloseAsync(kind, entry) : Task.CompletedTask;
        }
    }

    private Task CloseAsync(WindowKind kind, Entry entry)
    {
        if (entry.CloseTask is not null)
        {
            return entry.CloseTask;
        }
        entry.CloseTask = ReleaseAndCloseAsync(kind, entry);
        return entry.CloseTask;
    }

    private async Task ReleaseAndCloseAsync(WindowKind kind, Entry entry)
    {
        try
        {
            await entry.Root.DisposeAsync();
        }
        finally
        {
            entry.AllowClose = true;
            await dispatcher.InvokeAsync(entry.Root.Close, CancellationToken.None);
        }
    }

    private void RemoveClosed(WindowKind kind, Entry entry)
    {
        lock (gate)
        {
            if (entries.GetValueOrDefault(kind) == entry)
            {
                entries.Remove(kind);
            }
        }
    }

    public async Task CloseAllAsync()
    {
        KeyValuePair<WindowKind, Entry>[] current;
        lock (gate)
        {
            current = [.. entries];
        }
        await Task.WhenAll(current.Select(pair => CloseAsync(pair.Key, pair.Value)));
    }

    private sealed class Entry(IWindowRoot root)
    {
        public IWindowRoot Root { get; } = root;
        public Task? CloseTask { get; set; }
        public bool AllowClose { get; set; }
    }
}