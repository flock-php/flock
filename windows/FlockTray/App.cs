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
            tray.ShowRequested += ShowPanel;
            tray.MenuRequested += ShowMenu;

            Model.Changed += UpdateTray;
            Model.NeedsSetup = Installation.BundledFlock is not null && !Installation.IsSetUp;
            Model.StartPolling();

            // Started by hand: show the panel, so it is clear the app runs (Windows 11 hides new
            // notification area icons), or the setup on the first start after installing. The
            // sign-in autostart passes --background and stays quiet.
            var background = Environment.GetCommandLineArgs().Contains(BackgroundArgument);
            if (Model.NeedsSetup)
            {
                if (!background)
                {
                    Open(WindowKind.Setup);
                }
            }
            else
            {
                if (Installation.NeedsUpdate)
                {
                    _ = Model.UpdateInstallationAsync();
                }
                if (!background)
                {
                    panel = new PanelWindow(Model);
                    panel.ShowAtTrayCorner();
                }
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

    private string IconPath()
    {
        var name = !Model.IsRunning ? "FlockStopped.ico" : Model.IsDegraded ? "FlockWarning.ico" : "Flock.ico";
        return Path.Combine(AppContext.BaseDirectory, "Assets", name);
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
