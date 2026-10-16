using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.XamlTypeInfo;

namespace Flock.Tray;

public enum WindowKind { Mail, Dumps, Logs, Settings, Setup }

/// <summary>
/// The tray app: a notification area icon with a panel (the macOS menu bar extra), plus the
/// Mail, Dumps, Logs and Settings windows. It keeps running with no window open.
/// </summary>
public sealed partial class App : Application, IXamlMetadataProvider
{
    // Without XAML markup there is no generated type info; WinUI's own provider covers its controls.
    private readonly XamlControlsXamlMetaDataProvider metadata = new();
    private readonly Dictionary<WindowKind, Window> windows = [];
    private Native.TrayIcon? tray;
    private PanelWindow? panel;

    public static new App Current => (App)Application.Current;

    public AppModel Model { get; } = new();

    public App()
    {
        UnhandledException += (_, e) =>
        {
            // A tray app must not disappear because one view failed; keep a trace for bug reports.
            Log(e.Exception);
            e.Handled = true;
        };
    }

    public IXamlType GetXamlType(Type type) => metadata.GetXamlType(type);

    public IXamlType GetXamlType(string fullName) => metadata.GetXamlType(fullName);

    public XmlnsDefinition[] GetXmlnsDefinitions() => metadata.GetXmlnsDefinitions();

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        try
        {
            Resources.MergedDictionaries.Add(new XamlControlsResources());
            DispatcherShutdownMode = DispatcherShutdownMode.OnExplicitShutdown;

            tray = new Native.TrayIcon(IconPath(), "Flock");
            tray.Clicked += TogglePanel;
            tray.ShowRequested += ShowMain;
            tray.LinkReceived += OpenLink;
            tray.MenuRequested += ShowMenu;
            tray.ThemeChanged += UpdateTray;

            Model.Changed += UpdateTray;
            Model.NeedsSetup = Installation.BundledFlock is not null && !Installation.IsSetUp;
            Model.StartPolling();

            if (!Model.NeedsSetup && Installation.NeedsUpdate)
            {
                _ = Model.UpdateInstallationAsync();
            }
            // Started by hand (e.g. from the Start menu): open the main window, so it is clear the
            // app runs (Windows 11 hides new notification area icons). The panel belongs to the
            // tray icon. The sign-in autostart passes --background and stays quiet. A flock://
            // link that starts the app opens Settings → Services or Sites instead.
            var arguments = Environment.GetCommandLineArgs();
            if (arguments.FirstOrDefault(SecureLink.IsFlockLink) is { } link)
            {
                OpenLink(link);
            }
            else if (!arguments.Contains(BackgroundArgument))
            {
                ShowMain();
            }
        }
        catch (Exception e)
        {
            Log(e);
            Native.ShowError("Flock could not start", $"{e.Message}\n\nDetails are in %LocalAppData%\\Flock\\tray.log.");
            tray?.Dispose();
            Exit();
        }
    }

    /// <summary>Command line argument of the sign-in autostart.</summary>
    public const string BackgroundArgument = "--background";

    private string IconPath() => TrayIconPath(Model, Native.TaskbarIsLight);

    /// <summary>The logomark for the daemon's state, colored for a light or a dark background.</summary>
    /// <param name="extension">.ico for the notification area; .png inside windows (WinUI scales up an .ico's smallest image).</param>
    public static string TrayIconPath(AppModel model, bool light, string extension = ".ico")
    {
        var state = !model.IsRunning ? "TrayStopped" : model.IsDegraded ? "TrayWarning" : "Tray";
        return Path.Combine(AppContext.BaseDirectory, "Assets", $"{state}.{(light ? "Light" : "Dark")}{extension}");
    }

    private void UpdateTray()
    {
        var tooltip = "Flock — " + Model.Headline;
        if (Model.Status is { Warnings.Count: > 0 } status)
        {
            tooltip += "\n" + status.Warnings[0];
        }
        tray?.Update(IconPath(), tooltip);
    }

    private void TogglePanel()
    {
        panel ??= new PanelWindow(Model);
        panel.Toggle();
    }

    public void ShowPanel()
    {
        panel ??= new PanelWindow(Model);
        panel.ShowNearTray();
    }

    /// <summary>Opens the app from outside the tray (a start by hand): the setup until Flock is set up, then Settings.</summary>
    private void ShowMain() => Open(Model.NeedsSetup ? WindowKind.Setup : WindowKind.Settings);

    /// <summary>
    /// A flock:// link (registered by the installer). flock://install, e.g. from the website's
    /// install buttons, shows the service in Settings → Services, which asks before installing
    /// anything; before the setup the link waits, and Services asks about it once Flock is set up.
    /// flock://secure, from the page served for a site that isn't secured, asks in Settings → Sites.
    /// </summary>
    private void OpenLink(string? text)
    {
        if (SecureLink.Parse(text) is { } secure && !Model.NeedsSetup)
        {
            Model.PendingSecure = secure;
            Open(WindowKind.Settings, SettingsPane.Sites);
            return;
        }
        if (InstallLink.Parse(text) is not { } link)
        {
            ShowMain();
            return;
        }
        Model.PendingInstall = link;
        if (Model.NeedsSetup)
        {
            Open(WindowKind.Setup);
            return;
        }
        Open(WindowKind.Settings, SettingsPane.Services);
        (windows[WindowKind.Settings] as SettingsWindow)?.ShowService(link.Service);
    }

    private void ShowMenu()
    {
        if (tray is null)
        {
            return;
        }
        var start = Model.NeedsSetup ? "Set Up Flock…" : Model.IsRunning ? "Restart Flock" : "Start Flock";
        string?[] items = ["Open Flock", null, "Mail", "Dumps", "Logs", "Settings…", null, start, "Quit Flock"];
        switch (tray.ShowMenu(items, defaultItem: 0))
        {
            case 0: ShowPanel(); break;
            case 2: Open(WindowKind.Mail); break;
            case 3: Open(WindowKind.Dumps); break;
            case 4: Open(WindowKind.Logs); break;
            case 5: Open(WindowKind.Settings); break;
            case 7 when Model.NeedsSetup: Open(WindowKind.Setup); break;
            case 7: _ = Model.RestartAsync(); break;
            case 8: Quit(); break;
        }
    }

    /// <summary>Opens a window, or brings it to the front when it is already open.</summary>
    public void Open(WindowKind kind, SettingsPane? pane = null)
    {
        panel?.Hide();
        if (!windows.TryGetValue(kind, out var window))
        {
            window = kind switch
            {
                WindowKind.Mail => new MailWindow(Model),
                WindowKind.Dumps => new DumpsWindow(Model),
                WindowKind.Logs => new LogsWindow(Model),
                WindowKind.Setup => new SetupWindow(Model),
                _ => new SettingsWindow(Model),
            };
            windows[kind] = window;
            window.Closed += (_, _) => windows.Remove(kind);
        }
        if (pane is { } p && window is SettingsWindow settings)
        {
            settings.Select(p);
        }
        ((FlockWindow)window).Present();
    }

    /// <summary>Opens the Logs window on one log.</summary>
    public void OpenLog(LogStore.Source source)
    {
        Open(WindowKind.Logs);
        if (windows.GetValueOrDefault(WindowKind.Logs) is LogsWindow logs)
        {
            _ = logs.ShowSource(source);
        }
    }

    public void Quit()
    {
        foreach (var window in windows.Values.ToList())
        {
            window.Close();
        }
        panel?.Close();
        tray?.Dispose();
        tray = null;
        Exit();
    }

    /// <summary>Appends an unexpected error to %LocalAppData%\Flock\tray.log.</summary>
    public static void Log(Exception e)
    {
        try
        {
            var dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Flock");
            Directory.CreateDirectory(dir);
            File.AppendAllText(Path.Combine(dir, "tray.log"), $"{DateTimeOffset.Now:O} {e}\n\n");
        }
        catch (IOException)
        {
        }
    }
}
