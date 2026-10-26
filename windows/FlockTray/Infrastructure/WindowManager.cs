using Flock.Tray;
using Flock.Tray.ViewModels;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.UI.Xaml;

namespace Flock.Tray.Infrastructure;

public interface IWindowRoot : IAsyncDisposable
{
    Window Window { get; }
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
                _ => throw new ArgumentOutOfRangeException(nameof(kind)),
            };
            return new WindowRoot(window, scope);
        }
        catch
        {
            scope.DisposeAsync().AsTask().GetAwaiter().GetResult();
            throw;
        }
    }

    private sealed class WindowRoot(Window window, AsyncServiceScope scope) : IWindowRoot
    {
        public Window Window => window;

        public async ValueTask DisposeAsync()
        {
            if (window is FlockWindow flockWindow)
            {
                await flockWindow.ReleaseAsync();
            }
            await scope.DisposeAsync();
        }
    }
}


/// <summary>Owns one root and scope per window kind and joins release before native destruction.</summary>
public sealed class WindowManager(IWindowFactory factory, IUiDispatcher dispatcher)
{
    private sealed class Entry(IWindowRoot root)
    {
        public IWindowRoot Root { get; } = root;
        public Task? CloseTask { get; set; }
        public bool AllowClose { get; set; }
    }

    private readonly object gate = new();
    private readonly Dictionary<WindowKind, Entry> entries = [];

    public Window Open(WindowKind kind)
    {
        lock (gate)
        {
            if (entries.TryGetValue(kind, out var existing))
            {
                existing.Root.Window.Activate();
                return existing.Root.Window;
            }
            var entry = new Entry(factory.Create(kind));
            entries.Add(kind, entry);
            entry.Root.Window.AppWindow.Closing += (appWindow, args) =>
            {
                if (!entry.AllowClose)
                {
                    args.Cancel = true;
                    _ = CloseAsync(kind, entry);
                }
            };
            entry.Root.Window.Closed += (_, _) => RemoveClosed(kind, entry);
            entry.Root.Window.Activate();
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

    public T? Get<T>(WindowKind kind) where T : Window =>
        entries.TryGetValue(kind, out var entry) ? entry.Root.Window as T : null;

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
            await dispatcher.InvokeAsync(() => entry.Root.Window.Close(), CancellationToken.None);
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
}