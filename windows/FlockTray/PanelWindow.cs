using System.Text.RegularExpressions;
using Flock.Kit;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
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

    private readonly AppModel model;
    private readonly IntPtr hwnd;
    private readonly ScrollViewer scroller = new() { VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
    private DateTime hiddenAt;
    private Native.POINT anchor;
    private bool dirty = true;

    public PanelWindow(AppModel model)
    {
        this.model = model;
        hwnd = Microsoft.UI.Win32Interop.GetWindowFromWindowId(AppWindow.Id);
        Title = "Flock";
        SystemBackdrop = new DesktopAcrylicBackdrop();

        var presenter = OverlappedPresenter.Create();
        presenter.SetBorderAndTitleBar(hasBorder: true, hasTitleBar: false);
        presenter.IsResizable = false;
        presenter.IsMaximizable = false;
        presenter.IsMinimizable = false;
        presenter.IsAlwaysOnTop = true;
        AppWindow.SetPresenter(presenter);
        AppWindow.IsShownInSwitchers = false;

        Content = scroller;
        Ui.Shortcut(scroller, Windows.System.VirtualKey.Escape, Windows.System.VirtualKeyModifiers.None, Hide);
        Ui.SettingsShortcut(scroller);
        Activated += (_, e) =>
        {
            if (e.WindowActivationState == WindowActivationState.Deactivated)
            {
                Hide();
            }
        };
        scroller.ActualThemeChanged += (_, _) => dirty = true;
        model.Changed += () =>
        {
            dirty = true;
            if (AppWindow.IsVisible)
            {
                Render();
                Fit();
            }
        };
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
        _ = model.RefreshAsync();
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
        var w = (int)(Width * scale);
        var h = (int)Math.Min(height * scale, area.Height * 0.85);
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
        var root = new StackPanel { Spacing = 12, Padding = new Thickness(16), Width = Width };
        root.Children.Add(Header());
        if (model.ActionError is { } failure)
        {
            // Shown whether or not the daemon runs (a failed restart, a toggle the daemon refused).
            var banner = Ui.ErrorBar(model.DismissError);
            banner.Margin = new Thickness(0);
            Ui.ShowError(banner, failure);
            root.Children.Add(banner);
        }

        if (model.Status is { } status)
        {
            if (status.Warnings.Count > 0)
            {
                root.Children.Add(Warnings(status.Warnings));
            }
            root.Children.Add(Divider());
            root.Children.Add(Services(status));
            root.Children.Add(Divider());
            root.Children.Add(Sites(status.Sites));
            root.Children.Add(Divider());
            root.Children.Add(Toggles(status));
            root.Children.Add(Divider());
            root.Children.Add(Shortcuts(status));
        }
        else
        {
            root.Children.Add(NotRunning());
        }

        root.Children.Add(Divider());
        root.Children.Add(Footer());
        scroller.Content = root;
        // Fit again once the content has really been laid out (the first measure can come too early).
        root.Loaded += (_, _) =>
        {
            if (AppWindow.IsVisible)
            {
                Fit();
            }
        };
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
        var title = Ui.Column(0, Ui.Text("Flock", "BodyStrongTextBlockStyle"), Ui.Caption(model.Headline));
        var leading = Ui.Row(10, icon, title);
        return model.IsRestarting ? Ui.Spread(leading, new ProgressRing { Width = 16, Height = 16 }) : Ui.Spread(leading);
    }

    private static StackPanel Warnings(List<string> warnings)
    {
        var panel = Ui.Column(4);
        foreach (var warning in warnings)
        {
            var text = Ui.Caption(warning, Ui.Caution);
            panel.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), text));
            text.MaxWidth = Width - 54;
        }
        return panel;
    }

    private static StackPanel Services(Status status)
    {
        var panel = Ui.Column(5, SectionTitle("Services"));
        panel.Children.Add(ServiceRow("Web", $"http :{status.HttpPort} · https :{status.HttpsPort}", true));
        if (status.Dns.Wildcard)
        {
            panel.Children.Add(ServiceRow("DNS", $"*.{status.Dns.Tld}", true));
        }
        else if (status.Dns.Tld.Length > 0)
        {
            // Wildcard DNS is overridden or not set up: only the hosts file's names resolve.
            var helperDown = status.Warnings.Any(w => w.Contains("hosts helper", StringComparison.Ordinal));
            panel.Children.Add(ServiceRow("Hosts", "site domains only", !helperDown));
        }
        panel.Children.Add(ServiceRow("Mail", status.Mail.Length == 0 ? "stopped" : $"smtp {status.Mail}", status.Mail.Length > 0));
        if (status.Postgres is { } postgres)
        {
            panel.Children.Add(ServiceRow("PostgreSQL", postgres, status.Processes.FirstOrDefault(p => p.Name == "postgres")?.Up ?? false));
        }
        if (status.Valkey is { } valkey)
        {
            panel.Children.Add(ServiceRow("Valkey", valkey, status.Processes.FirstOrDefault(p => p.Name == "valkey")?.Up ?? false));
        }
        foreach (var extra in status.Extras)
        {
            panel.Children.Add(ServiceRow(extra.Name, extra.Enabled ? $"{extra.Version} on 127.0.0.1:{extra.Port}" : "stopped", extra.Up));
        }

        // PHP pools: one FPM process on macOS, several php-cgi workers (php8.4-cgi-1, -2, ...) on Windows.
        foreach (var pool in status.Processes.Where(p => p.Name.StartsWith("php", StringComparison.Ordinal)).GroupBy(p => PoolName(p.Name)))
        {
            var workers = pool.ToList();
            var up = workers.Count(w => w.Up);
            var restarts = workers.Sum(w => w.Restarts);
            string detail;
            if (up == 0)
            {
                detail = workers.FirstOrDefault(w => w.LastError is not null)?.LastError ?? "down";
            }
            else
            {
                detail = workers.Count > 1 ? $"{up} of {workers.Count} workers up" : "up";
                if (restarts > 0)
                {
                    detail += $" (restarted {restarts}×)";
                }
            }
            panel.Children.Add(ServiceRow(pool.Key, detail, up > 0));
        }

        foreach (var process in status.Processes.Where(p => !p.Up && !p.Name.StartsWith("php", StringComparison.Ordinal)))
        {
            var error = Ui.Caption($"{process.Name}: {process.LastError ?? "down"}", Ui.Critical);
            error.MaxLines = 3;
            panel.Children.Add(error);
        }
        return panel;
    }

    private static string PoolName(string process)
    {
        var m = Regex.Match(process, @"^php(\d+\.\d+)-(?:cgi|fpm)");
        return m.Success ? $"PHP {m.Groups[1].Value}" : process;
    }

    private static Grid ServiceRow(string name, string detail, bool up)
    {
        var text = Ui.Caption(detail, wrap: false);
        text.MaxWidth = 210;
        return Ui.Spread(Ui.Row(8, Ui.StatusDot(up), Ui.Text(name, wrap: false)), text);
    }

    private StackPanel Sites(List<Status.Site> sites)
    {
        var panel = Ui.Column(4, SectionTitle("Sites"));
        if (sites.Count == 0)
        {
            panel.Children.Add(Ui.Caption("No sites yet — run `flock link` or `flock park`, or add a site in Settings."));
            return panel;
        }

        var list = Ui.Column(2);
        foreach (var site in sites)
        {
            list.Children.Add(SiteRow(site));
        }
        panel.Children.Add(new ScrollViewer { Content = list, MaxHeight = 240, VerticalScrollBarVisibility = ScrollBarVisibility.Auto });
        return panel;
    }

    private static Button SiteRow(Status.Site site)
    {
        var name = Ui.Column(0, Ui.Text(site.Domain, wrap: false));
        if (site.Problem is { } problem)
        {
            name.Children.Add(Ui.Caption(problem, Ui.Critical));
        }
        var icon = Ui.Icon(site.Secured ? Ui.GlyphLock : Ui.GlyphGlobe, 12, site.Secured ? Ui.Success : Ui.SecondaryText);
        var row = Ui.Spread(Ui.Row(8, icon, name), Ui.Caption($"PHP {site.Php}", wrap: false));

        var button = new Button
        {
            Content = row,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            BorderThickness = new Thickness(0),
            Padding = new Thickness(6, 4, 6, 4),
            IsEnabled = site.Url is not null,
        };
        ToolTipService.SetToolTip(button, site.Url ?? site.Problem ?? site.Path);
        button.Click += (_, _) => AppModel.Open(site);
        return button;
    }

    private StackPanel Toggles(Status status)
    {
        return Ui.Column(2,
            Toggle(Ui.GlyphBug, "Xdebug", status.Xdebug, on => model.SetXdebugAsync(on)),
            Toggle(Ui.GlyphDownload, "Capture dumps", status.Dumps, on => model.SetDumpsAsync(on)));
    }

    private static Grid Toggle(string glyph, string label, bool isOn, Func<bool, Task> change)
    {
        var toggle = new ToggleSwitch { IsOn = isOn, OnContent = "", OffContent = "", MinWidth = 0 };
        Ui.AutomationName(toggle, label);
        toggle.Toggled += (_, _) => _ = change(toggle.IsOn); // attached after IsOn, so rendering doesn't fire it
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
        var start = Ui.Button("Start Flock", () => _ = model.RestartAsync(), accent: true);
        start.IsEnabled = !model.IsRestarting;
        return Ui.Column(8, Ui.Text(model.Error ?? "Flock is not running", foreground: Ui.SecondaryText), start);
    }

    private Grid Footer()
    {
        var restart = Ui.Link("Restart", () => _ = model.RestartAsync());
        restart.IsEnabled = model.IsRunning && !model.IsRestarting;
        var settings = Ui.Link("Settings…", () => App.Current.Open(WindowKind.Settings));
        var quit = Ui.Link("Quit Flock", () => App.Current.Quit());
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
}
