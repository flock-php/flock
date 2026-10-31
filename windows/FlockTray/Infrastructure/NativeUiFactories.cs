using Flock.Tray;
using Flock.Tray.ViewModels;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;

namespace Flock.Tray.Infrastructure;

/// <summary>Creates the one native tray resource owned by the application runtime.</summary>
public interface ITrayIconFactory
{
    Task<ITrayIcon> CreateAsync(string iconPath, string tooltip);
}

/// <summary>Application-owned tray icon surface used by the WinUI entry point.</summary>
public interface ITrayIcon : IDisposable
{
    event Action? Clicked;
    event Action? MenuRequested;
    event Action? ShowRequested;
    event Action<string>? LinkReceived;
    event Action? ThemeChanged;

    void Update(string iconPath, string tooltip);
    int ShowMenu(IReadOnlyList<string?> items, int defaultItem = -1);
    event Action<string>? Failure;
}

public sealed class TrayIconFactory(ITrayNativeApi native, IUiDispatcher dispatcher, ILogger<TrayService> logger,
    string windowClass = Native.TrayWindowClass) : ITrayIconFactory
{
    public async Task<ITrayIcon> CreateAsync(string iconPath, string tooltip)
    {
        var tray = new TrayService(native, dispatcher, logger, windowClass);
        try
        {
            await tray.StartAsync(iconPath, tooltip);
            return tray;
        }
        catch
        {
            tray.Dispose();
            throw;
        }
    }
}

/// <summary>Creates the panel window without letting App construct a native UI root.</summary>
public interface IPanelWindowFactory
{
    IPanelWindow Create();
}

public interface IPanelWindow
{
    void Toggle();
    void ShowNearTray();
    void Hide();
    void Close();
}

public sealed class PanelWindowFactory(IServiceScopeFactory scopes) : IPanelWindowFactory
{
    public IPanelWindow Create()
    {
        var scope = scopes.CreateScope();
        try
        {
            return new PanelWindowRoot(new PanelWindow(scope.ServiceProvider.GetRequiredService<PanelViewModel>()), scope);
        }
        catch
        {
            scope.Dispose();
            throw;
        }
    }

    private sealed class PanelWindowRoot(PanelWindow window, IServiceScope scope) : IPanelWindow
    {
        public void Toggle() => window.Toggle();

        public void ShowNearTray() => window.ShowNearTray();

        public void Hide() => window.Hide();

        public void Close()
        {
            window.Release();
            window.Close();
            scope.Dispose();
        }
    }
}
