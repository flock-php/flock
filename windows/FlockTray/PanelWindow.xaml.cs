using System.ComponentModel;
using Windows.Foundation;
using Windows.Graphics;
using Windows.System;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using DispatcherQueue = Microsoft.UI.Dispatching.DispatcherQueue;
namespace Flock.Tray;

/// <summary>
///     The flyout shown from the notification area icon (the macOS app's menu bar panel): status,
///     services, sites, toggles and window shortcuts.
/// </summary>
internal sealed partial class PanelWindow : Window
{
    private const int Width = 360;
    private readonly DispatcherQueue dispatcher;
    private readonly IntPtr hwnd;

    private readonly RuntimeService model;
    private readonly PanelViewModel viewModel;
    private Native.POINT anchor;
    private bool fitPending;
    private DateTime hiddenAt;
    private bool loaded;
    private bool releasing;

    public PanelWindow(PanelViewModel viewModel)
    {
        this.viewModel = viewModel;
        model = viewModel.Model;
        InitializeComponent();
        Root.DataContext = viewModel;
        dispatcher = DispatcherQueue.GetForCurrentThread() ?? throw new InvalidOperationException("The panel dispatcher is unavailable.");
        hwnd = Win32Interop.GetWindowFromWindowId(AppWindow.Id);
        Title = App.Current.Options.Identity.DisplayName;
        SystemBackdrop = new DesktopAcrylicBackdrop();
        if (App.Current.Options.UseSolidBackground)
        {
            Scroller.Background = Ui.Brush("SolidBackgroundFillColorBaseBrush");
        }

        var presenter = OverlappedPresenter.Create();
        presenter.SetBorderAndTitleBar(true, false);
        presenter.IsResizable = false;
        presenter.IsMaximizable = false;
        presenter.IsMinimizable = false;
        presenter.IsAlwaysOnTop = true;
        AppWindow.SetPresenter(presenter);
        AppWindow.IsShownInSwitchers = false;

        Root.Loaded += RootLoaded;
        Scroller.ActualThemeChanged += ThemeChanged;
        Ui.Shortcut(Scroller, VirtualKey.Escape, VirtualKeyModifiers.None, Hide);
        Ui.SettingsShortcut(Scroller);
        Activated += ActivatedChanged;
        viewModel.PropertyChanged += ViewModelChanged;
        AppWindow.Closing += ClosingWindow;
    }

    public void Release()
    {
        if (releasing)
        {
            return;
        }
        releasing = true;
        Root.Loaded -= RootLoaded;
        Scroller.ActualThemeChanged -= ThemeChanged;
        Activated -= ActivatedChanged;
        AppWindow.Closing -= ClosingWindow;
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.Dispose();
    }

    public void Toggle()
    {
        // Clicking the icon while the panel is open first deactivates (and hides) the panel;
        // that click must not open it again.
        if (AppWindow.IsVisible || (DateTime.UtcNow - hiddenAt).TotalMilliseconds < 300)
        {
            Hide();
            return;
        }
        ShowNearTray();
    }

    /// <summary>Shows the panel next to the tray icon, which is under the cursor (an icon click or its menu).</summary>
    public void ShowNearTray()
    {
        anchor = Native.CursorPosition();
        UpdateTrayIcon();
        Fit();
        AppWindow.Show();
        Activate();
        Native.SetForegroundWindow(hwnd);
        _ = model.RefreshAsync();
        _ = model.RefreshSetupAsync();
    }

    public void Hide()
    {
        if (AppWindow.IsVisible)
        {
            AppWindow.Hide();
            hiddenAt = DateTime.UtcNow;
        }
    }

    private void RootLoaded(object sender, RoutedEventArgs args)
    {
        loaded = true;
        if (AppWindow.IsVisible)
        {
            Fit();
        }
    }

    private void ThemeChanged(FrameworkElement sender, object args)
    {
        UpdateTrayIcon();
        ScheduleFit();
    }

    private void ActivatedChanged(object sender, WindowActivatedEventArgs args)
    {
        if (!App.Current.Options.KeepPanelOpen && args.WindowActivationState == WindowActivationState.Deactivated)
        {
            Hide();
        }
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (AppWindow.IsVisible)
        {
            ScheduleFit();
        }
    }

    private void ClosingWindow(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (!releasing)
        {
            args.Cancel = true;
            Hide();
        }
    }

    private void ScheduleFit()
    {
        if (fitPending || !AppWindow.IsVisible)
        {
            return;
        }
        fitPending = true;
        dispatcher.TryEnqueue(() =>
        {
            fitPending = false;
            if (AppWindow.IsVisible)
            {
                Fit();
            }
        });
    }

    /// <summary>Sizes the panel to its content and puts it next to the taskbar, near the cursor.</summary>
    private void Fit()
    {
        if (!loaded)
        {
            return;
        }
        var cursor = anchor;
        var display = DisplayArea.GetFromPoint(new PointInt32(cursor.X, cursor.Y), DisplayAreaFallback.Nearest);
        var area = display.WorkArea;
        var outer = display.OuterBounds;
        var scale = Math.Max(1, Native.GetDpiForWindow(hwnd) / 96.0);

        Root.Measure(new Size(Width, double.PositiveInfinity));
        // Before the window has been shown once, layout may not be measurable yet.
        var height = Root.DesiredSize.Height > 100 ? Root.DesiredSize.Height + 2 : 560;
        // The size set below includes the window border, which the content doesn't get.
        var w = (int)(Width * scale) + AppWindow.Size.Width - AppWindow.ClientSize.Width;
        var frame = AppWindow.Size.Height - AppWindow.ClientSize.Height;
        var max = (int)(area.Height * 0.85);
        var needed = (int)Math.Ceiling(height * scale) + frame;
        var h = Math.Min(needed, max);
        // Only scroll when the content doesn't fit (rounding left a pixel or two to scroll before).
        Scroller.VerticalScrollBarVisibility = needed > max ? ScrollBarVisibility.Auto : ScrollBarVisibility.Disabled;
        var gap = (int)(12 * scale);

        int x, y;
        if (area.X > outer.X || area.X + area.Width < outer.X + outer.Width)
        {
            // Taskbar on the left or right.
            x = area.X > outer.X ? area.X + gap : area.X + area.Width - w - gap;
            y = Math.Clamp(cursor.Y - h / 2, area.Y + gap, area.Y + area.Height - h - gap);
        }
        else
        {
            x = Math.Clamp(cursor.X - w / 2, area.X + gap, area.X + area.Width - w - gap);
            y = area.Y > outer.Y ? area.Y + gap : area.Y + area.Height - h - gap;
        }
        AppWindow.MoveAndResize(new RectInt32(x, y, w, h));
    }

    private void UpdateTrayIcon()
    {
        var file = App.TrayIconPath(model, Scroller.ActualTheme == ElementTheme.Light, ".png");
        TrayIcon.Source = new BitmapImage(new Uri(file));
    }

    private void DismissError(InfoBar sender, object args)
    {
        viewModel.DismissError();
    }

    private void OpenSite(object sender, RoutedEventArgs args)
    {
        if (sender is Button { DataContext: PanelSiteRow { Url: { Length: > 0 } url } })
        {
            viewModel.OpenSite(url);
        }
    }

    private void OpenWorktree(object sender, RoutedEventArgs args)
    {
        if (sender is Button { DataContext: PanelWorktreeRow { Url: { Length: > 0 } url } })
        {
            viewModel.OpenSite(url);
        }
    }

    private void ToggleWorktrees(object sender, RoutedEventArgs args)
    {
        if (sender is Button { DataContext: PanelSiteRow row })
        {
            viewModel.ToggleWorktrees(row.Name);
        }
    }
}