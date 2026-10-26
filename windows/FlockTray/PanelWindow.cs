using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using System.ComponentModel;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.Foundation;
using Windows.Graphics;

namespace Flock.Tray;

/// <summary>
/// The flyout shown from the notification area icon (the macOS app's menu bar panel): status,
/// services, sites, toggles and window shortcuts.
/// </summary>
internal sealed partial class PanelWindow : Window
{
    private const int Width = 360;

    private readonly RuntimeService model;
    private readonly PanelViewModel viewModel;
    private readonly IntPtr hwnd;
    private readonly ScrollViewer scroller = new() { VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
    private readonly StackPanel root = new() { Spacing = 12, Padding = new Thickness(16) };
    private readonly ContentControl headerSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly ContentControl updateSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly ContentControl errorSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly ContentControl contentSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly ContentControl footerSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly StackPanel serviceRows = Ui.Column(5);
    private readonly StackPanel siteRows = Ui.Column(2);
    private readonly StackPanel togglesRows = Ui.Column(2);
    private readonly ScrollViewer sitesScroller = new() { MaxHeight = 240, VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
    private readonly StackPanel statusContent = Ui.Column(12);
    private readonly ContentControl warningsSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly StackPanel servicesSection = Ui.Column(5);
    private readonly StackPanel sitesSection = Ui.Column(4);
    private readonly ContentControl sitesEmptySlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly ContentControl shortcutsSlot = new() { HorizontalContentAlignment = HorizontalAlignment.Stretch };
    private readonly KeyedRowsBinding<string, PanelServiceRow> serviceBinding;
    private readonly KeyedRowsBinding<string, PanelSiteRow> siteBinding;
    private ToggleSwitch? xdebugToggle;
    private ToggleSwitch? captureToggle;
    private DateTime hiddenAt;
    private Native.POINT anchor;
    private bool dirty = true;
    private Task? refreshTask;

    public PanelWindow(PanelViewModel viewModel)
    {
        this.viewModel = viewModel;
        model = viewModel.Model;
        serviceBinding = new KeyedRowsBinding<string, PanelServiceRow>(serviceRows, row => row.Id,
            row => new ServiceRowControl(row), (_, element) => ((ServiceRowControl)element).Dispose());
        siteBinding = new KeyedRowsBinding<string, PanelSiteRow>(siteRows, row => row.Domain,
            row => new SiteRowControl(row, model), (_, element) => ((SiteRowControl)element).Dispose());
        sitesScroller.Content = siteRows;
        servicesSection.Children.Add(SectionTitle("Services"));
        servicesSection.Children.Add(serviceRows);
        sitesSection.Children.Add(SectionTitle("Sites"));
        sitesSection.Children.Add(sitesEmptySlot);
        sitesSection.Children.Add(sitesScroller);
        statusContent.Children.Add(warningsSlot);
        statusContent.Children.Add(Divider());
        statusContent.Children.Add(servicesSection);
        statusContent.Children.Add(Divider());
        statusContent.Children.Add(sitesSection);
        statusContent.Children.Add(Divider());
        statusContent.Children.Add(togglesRows);
        statusContent.Children.Add(Divider());
        statusContent.Children.Add(shortcutsSlot);
        hwnd = Microsoft.UI.Win32Interop.GetWindowFromWindowId(AppWindow.Id);
        Title = App.Current.Options.Identity.DisplayName;
        SystemBackdrop = new DesktopAcrylicBackdrop();
        if (App.Current.Options.UseSolidBackground)
        {
            scroller.Background = Ui.Brush("SolidBackgroundFillColorBaseBrush");
        }

        var presenter = OverlappedPresenter.Create();
        presenter.SetBorderAndTitleBar(hasBorder: true, hasTitleBar: false);
        presenter.IsResizable = false;
        presenter.IsMaximizable = false;
        presenter.IsMinimizable = false;
        presenter.IsAlwaysOnTop = true;
        AppWindow.SetPresenter(presenter);
        AppWindow.IsShownInSwitchers = false;

        root.Children.Add(headerSlot);
        root.Children.Add(updateSlot);
        root.Children.Add(errorSlot);
        root.Children.Add(contentSlot);
        root.Children.Add(Divider());
        root.Children.Add(footerSlot);
        Content = scroller;
        scroller.Content = root;
        root.Loaded += (_, _) =>
        {
            if (AppWindow.IsVisible)
            {
                Fit();
            }
        };
        Ui.Shortcut(scroller, Windows.System.VirtualKey.Escape, Windows.System.VirtualKeyModifiers.None, Hide);
        Ui.SettingsShortcut(scroller);
        Activated += (_, e) =>
        {
            if (!App.Current.Options.KeepPanelOpen && e.WindowActivationState == WindowActivationState.Deactivated)
            {
                Hide();
            }
        };
        scroller.ActualThemeChanged += (_, _) => dirty = true;
        viewModel.PropertyChanged += ViewModelChanged;
        Closed += (_, _) => viewModel.PropertyChanged -= ViewModelChanged;
    }

    public void Release()
    {
        serviceBinding.Release();
        siteBinding.Release();
        viewModel.Dispose();
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        dirty = true;
        if (AppWindow.IsVisible)
        {
            Render();
            Fit();
        }
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
        if (dirty)
        {
            Render();
        }
        Fit();
        AppWindow.Show();
        Activate();
        Native.SetForegroundWindow(hwnd);
        refreshTask = model.RefreshAsync();
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

    /// <summary>Sizes the panel to its content and puts it next to the taskbar, near the cursor.</summary>
    private void Fit()
    {
        var cursor = anchor;
        var display = DisplayArea.GetFromPoint(new PointInt32(cursor.X, cursor.Y), DisplayAreaFallback.Nearest);
        var area = display.WorkArea;
        var outer = display.OuterBounds;
        var scale = Math.Max(1, Native.GetDpiForWindow(hwnd) / 96.0);

        if (scroller.Content is UIElement content)
        {
            content.Measure(new Size(Width, double.PositiveInfinity));
        }
        // Before the window has been shown once, layout may not be measurable yet.
        var height = scroller.Content is UIElement measured && measured.DesiredSize.Height > 100 ? measured.DesiredSize.Height + 2 : 560;
        // The size set below includes the window border, which the content doesn't get.
        var w = (int)(Width * scale) + AppWindow.Size.Width - AppWindow.ClientSize.Width;
        var frame = AppWindow.Size.Height - AppWindow.ClientSize.Height;
        var max = (int)(area.Height * 0.85);
        var needed = (int)Math.Ceiling(height * scale) + frame;
        var h = Math.Min(needed, max);
        // Only scroll when the content doesn't fit (rounding left a pixel or two to scroll before).
        scroller.VerticalScrollBarVisibility = needed > max ? ScrollBarVisibility.Auto : ScrollBarVisibility.Disabled;
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

    private void Render()
    {
        dirty = false;
        headerSlot.Content = Header();
        updateSlot.Content = model.UpdateService.Available is { } update ? UpdateRow(update) : null;
        if (viewModel.ActionError is { } failure)
        {
            // Shown whether or not the daemon runs (a failed restart, a toggle the daemon refused).
            var banner = Ui.ErrorBar(viewModel.DismissError);
            banner.Margin = new Thickness(0);
            Ui.ShowError(banner, failure);
            errorSlot.Content = banner;
        }
        else
        {
            errorSlot.Content = null;
        }

        // Before the setup is done, a daemon that answers is the setup's (it may still fail).
        if (model.Status is { } status && !model.NeedsSetup)
        {
            warningsSlot.Content = status.Warnings.Count > 0 ? Warnings(status.Warnings) : null;
            Services();
            Sites();
            Toggles(status);
            shortcutsSlot.Content = Shortcuts(status);
            contentSlot.Content = statusContent;
        }
        else
        {
            serviceBinding.Synchronize([]);
            siteBinding.Synchronize([]);
            warningsSlot.Content = null;
            sitesEmptySlot.Content = null;
            sitesScroller.Visibility = Visibility.Collapsed;
            shortcutsSlot.Content = null;
            contentSlot.Content = NotRunning();
        }

        footerSlot.Content = Footer();
    }

    private Grid Header()
    {
        // The tray icon, colored for the panel's own theme (which can differ from the taskbar's).
        var file = App.TrayIconPath(model, scroller.ActualTheme == ElementTheme.Light, ".png");
        var icon = new Image
        {
            Source = new Microsoft.UI.Xaml.Media.Imaging.BitmapImage(new Uri(file)),
            Width = 26,
            Height = 26,
        };
        var title = Ui.Column(0, Ui.Text(App.Current.Options.Identity.DisplayName, "BodyStrongTextBlockStyle"), Ui.Caption(model.Headline));
        var leading = Ui.Row(10, icon, title);
        return model.IsRestarting ? Ui.Spread(leading, new ProgressRing { Width = 16, Height = 16 }) : Ui.Spread(leading);
    }

    /// <summary>A newer version of the app; Update installs it (Settings → General shows the progress).</summary>
    private Grid UpdateRow(UpdateFeed.Item update)
    {
        var updater = model.UpdateService;
        var idle = !updater.IsBusy;
        var text = Ui.Caption(idle ? $"Flock {update.Version} is available" : updater.Summary);
        var button = Ui.Button("Update", () => App.Current.InstallUpdate(), accent: true);
        button.IsEnabled = idle;
        return Ui.Spread(Ui.Row(6, Ui.Icon(Ui.GlyphDownload, 14, Ui.Accent), text), button);
    }

    private static StackPanel Warnings(List<string> warnings)
    {
        var panel = Ui.Column(4);
        foreach (var text in warnings.Select(warning => Ui.Caption(warning, Ui.Caution)))
        {
            panel.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), text));
            text.MaxWidth = Width - 54;
        }
        return panel;
    }

    private StackPanel Services()
    {
        serviceBinding.Synchronize(viewModel.Services);
        return servicesSection;
    }

    private StackPanel Sites()
    {
        if (viewModel.Sites.Count == 0)
        {
            siteBinding.Synchronize([]);
            sitesEmptySlot.Content = Ui.Caption("No sites yet — run `flock link` or `flock park`, or add a site in Settings.");
            sitesScroller.Visibility = Visibility.Collapsed;
        }
        else
        {
            sitesEmptySlot.Content = null;
            sitesScroller.Visibility = Visibility.Visible;
            siteBinding.Synchronize(viewModel.Sites);
        }
        return sitesSection;
    }

    private StackPanel Toggles(Status status)
    {
        if (togglesRows.Children.Count == 0)
        {
            var xdebugRow = Toggle(Ui.GlyphBug, "Xdebug", status.Xdebug, on => model.SetXdebugAsync(on));
            var captureRow = Toggle(Ui.GlyphDownload, "Capture dumps", status.Dumps, on => viewModel.SetDumpsAsync(on), !viewModel.IsCaptureBusy);
            xdebugToggle = xdebugRow.Children.OfType<ToggleSwitch>().Single();
            captureToggle = captureRow.Children.OfType<ToggleSwitch>().Single();
            togglesRows.Children.Add(xdebugRow);
            togglesRows.Children.Add(captureRow);
        }
        xdebugToggle!.IsOn = status.Xdebug;
        captureToggle!.IsOn = status.Dumps;
        captureToggle.IsEnabled = !viewModel.IsCaptureBusy;
        return togglesRows;
    }

    private static Grid Toggle(string glyph, string label, bool isOn, Func<bool, Task> change, bool enabled = true)
    {
        var toggle = new ToggleSwitch { IsOn = isOn, IsEnabled = enabled, OnContent = "", OffContent = "", MinWidth = 0 };
        Ui.AutomationName(toggle, label);
        toggle.Toggled += async (_, _) => await change(toggle.IsOn); // attached after IsOn, so rendering doesn't fire it
        return Ui.Spread(Ui.Row(8, Ui.Icon(glyph, 14), Ui.Text(label, wrap: false)), toggle);
    }

    private Grid Shortcuts(Status status)
    {
        var grid = new Grid { ColumnSpacing = 8 };
        var buttons = new[]
        {
            Shortcut(Ui.GlyphMail, "Mail", status.MailCount, WindowKind.Mail),
            Shortcut(Ui.GlyphBug, "Dumps", status.DumpCount, WindowKind.Dumps),
            Shortcut(Ui.GlyphDocument, "Logs", null, WindowKind.Logs),
        };
        for (var i = 0; i < buttons.Length; i++)
        {
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            Grid.SetColumn(buttons[i], i);
            grid.Children.Add(buttons[i]);
        }
        return grid;
    }

    private static Button Shortcut(string glyph, string title, int? count, WindowKind kind)
    {
        var label = Ui.Row(4, new TextBlock { Text = title, FontSize = 12 });
        if (count is > 0)
        {
            label.Children.Add(Ui.Badge(count.Value.ToString(System.Globalization.CultureInfo.CurrentCulture)));
        }
        label.HorizontalAlignment = HorizontalAlignment.Center;
        var icon = Ui.Icon(glyph, 18);
        var content = Ui.Column(4, icon, label);
        var button = new Button
        {
            Content = content,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Center,
            Padding = new Thickness(4, 8, 4, 8),
        };
        button.Click += (_, _) => App.Current.Open(kind);
        return button;
    }

    private StackPanel NotRunning()
    {
        if (model.NeedsSetup)
        {
            return Ui.Column(8, Ui.Text("Flock isn't set up for this user yet.", foreground: Ui.SecondaryText),
                Ui.Button("Set Up Flock…", () => App.Current.Open(WindowKind.Setup), accent: true));
        }
        if (model.IsUpdating)
        {
            return Ui.Column(8, Ui.Text("Updating Flock…", foreground: Ui.SecondaryText), new ProgressBar { IsIndeterminate = true });
        }
        var start = Ui.AsyncButton("Start Flock", model.RestartAsync, accent: true);
        start.IsEnabled = !model.IsRestarting;
        return Ui.Column(8, Ui.Text(model.Error ?? "Flock is not running", foreground: Ui.SecondaryText), start);
    }

    private Grid Footer()
    {
        var quit = Ui.Link("Quit Flock", () => App.Current.Quit());
        if (model.NeedsSetup)
        {
            // Restart and Settings need the daemon; the panel's Set Up button is the way on.
            return Ui.Spread(Ui.Row(4), quit);
        }
        var restart = new HyperlinkButton { Content = "Restart", Padding = new Thickness(4, 2, 4, 2) };
        restart.Click += async (_, _) => await model.RestartAsync();
        restart.IsEnabled = model.IsRunning && !model.IsRestarting;
        var settings = Ui.Link("Settings…", () => App.Current.Open(WindowKind.Settings));
        return Ui.Spread(Ui.Row(4, restart, settings), quit);
    }

    private static TextBlock SectionTitle(string title)
    {
        var text = Ui.Caption(title.ToUpperInvariant());
        text.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
        text.FontSize = 11;
        return text;
    }

    private static Border Divider() => new()
    {
        Height = 1,
        Background = Ui.Brush("DividerStrokeColorDefaultBrush"),
        Margin = new Thickness(-16, 0, -16, 0),
    };

    private sealed class ServiceRowControl : Grid, IDisposable
    {
        private readonly PanelServiceRow row;
        private readonly TextBlock name = Ui.Text("", wrap: false);
        private readonly TextBlock detail = Ui.Caption("", wrap: false);
        private readonly Ellipse dot = Ui.StatusDot(false);

        public ServiceRowControl(PanelServiceRow row)
        {
            this.row = row;
            detail.MaxWidth = 210;
            Children.Add(Ui.Spread(Ui.Row(8, dot, name), detail));
            row.PropertyChanged += RowChanged;
            Update();
        }

        public void Dispose() => row.PropertyChanged -= RowChanged;

        private void RowChanged(object? sender, PropertyChangedEventArgs args) => Update();

        private void Update()
        {
            name.Text = row.Name;
            detail.Text = row.Detail;
            dot.Fill = row.Up ? Ui.Success : Ui.Brush("ControlStrongFillColorDisabledBrush");
        }
    }

    private sealed class SiteRowControl : Grid, IDisposable
    {
        private readonly PanelSiteRow row;
        private readonly RuntimeService model;
        private readonly Button button = new()
        {
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            BorderThickness = new Thickness(0),
            Padding = new Thickness(6, 4, 6, 4),
        };

        public SiteRowControl(PanelSiteRow row, RuntimeService model)
        {
            this.row = row;
            this.model = model;
            Children.Add(button);
            row.PropertyChanged += RowChanged;
            button.Click += Clicked;
            Update();
        }

        public void Dispose()
        {
            row.PropertyChanged -= RowChanged;
            button.Click -= Clicked;
        }

        private void Clicked(object sender, RoutedEventArgs args)
        {
            if (row.Url is { Length: > 0 } url)
            {
                model.Open(url);
            }
        }

        private void RowChanged(object? sender, PropertyChangedEventArgs args) => Update();

        private void Update()
        {
            var name = Ui.Column(0, Ui.Text(row.Domain, wrap: false));
            if (row.Problem is { } problem)
            {
                name.Children.Add(Ui.Caption(problem, Ui.Critical));
            }
            var icon = Ui.Icon(row.Secured ? Ui.GlyphLock : Ui.GlyphGlobe, 12, row.Secured ? Ui.Success : Ui.SecondaryText);
            UIElement php = Ui.Caption($"PHP {row.Php}", wrap: false);
            if (row.Debugging)
            {
                var bug = Ui.Icon(Ui.GlyphBug, 12, Ui.Caution);
                Ui.AutomationName(bug, "Debugging");
                php = Ui.Row(6, bug, php);
            }
            if (row.IsFrankenPhp)
            {
                php = Ui.Row(6, Ui.Badge(row.Worker == Status.SiteServer.Octane ? "Octane" : "FrankenPHP", Ui.Tone.Neutral), php);
            }
            if (row.IsShared)
            {
                var shared = Ui.Badge("Shared", Ui.Tone.Success);
                ToolTipService.SetToolTip(shared, $"Shared at {row.ShareUrl}");
                php = Ui.Row(6, shared, php);
            }
            button.Content = Ui.Spread(Ui.Row(8, icon, name), php);
            button.IsEnabled = row.Url is not null;
            ToolTipService.SetToolTip(button, row.Url ?? row.Problem ?? row.Path);
        }
    }
}
