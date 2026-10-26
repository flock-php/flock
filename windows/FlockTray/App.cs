using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Dispatching;
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
public sealed class App : Application, IXamlMetadataProvider
{
    // Without XAML markup there is no generated type info; WinUI's own provider covers its controls.
    private readonly XamlControlsXamlMetaDataProvider metadata = new();
    private ITrayIcon? tray;
    private IPanelWindow? panel;

    public new static App Current => (App)Application.Current;

    private AppHost? host;
    private Task? launchTask;
    private Task? updateTask;
    private Task? quitTask;
    private bool updatingTray;
    private DispatcherQueue? dispatcher;
    private WindowManager? windowManager;
    private IUiDispatcher? uiDispatcher;
    private ITrayIconFactory? trayFactory;
    private IPanelWindowFactory? panelFactory;

    public RuntimeService State => host?.RuntimeService ?? throw new InvalidOperationException("The application has not launched.");

    public ApplicationRuntime Runtime => host?.Runtime ?? throw new InvalidOperationException("The application has not launched.");

    internal static void Run(AppOptions options)
    {
        WinRT.ComWrappersSupport.InitializeComWrappers();
        Application.Start(callback =>
        {
            SynchronizationContext.SetSynchronizationContext(new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread()));
            _ = new App(options);
        });
    }

    internal AppOptions Options { get; }

    internal WindowManager Windows => windowManager ?? throw new InvalidOperationException("The application has not launched.");

    public App() : this(new AppOptions())
    {
    }

    internal App(AppOptions options)
    {
        Options = options;
        if (options.Theme is { } theme)
        {
            RequestedTheme = theme;
        }
        UnhandledException += (_, args) =>
        {
            Log(args.Exception);
            if (host is not null)
            {
                Runtime.RequestQuit();
            }
            else
            {
                Native.ShowError("Flock could not start", $"{args.Exception.Message}\n\nDetails are in %LocalAppData%\\Flock\\tray.log.");
            }
            // An unowned WinUI exception is fatal. Do not mark it handled just to keep the tray alive.
            args.Handled = false;
        };
    }

    public IXamlType GetXamlType(Type type) => metadata.GetXamlType(type);

    public IXamlType GetXamlType(string fullName) => metadata.GetXamlType(fullName);

    public XmlnsDefinition[] GetXmlnsDefinitions() => metadata.GetXmlnsDefinitions();

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        if (launchTask is null)
        {
            launchTask = StartAsync(DispatcherQueue.GetForCurrentThread());
        }
    }

    private async Task StartAsync(DispatcherQueue dispatcher)
    {
        try
        {
            this.dispatcher = dispatcher;
            Resources.MergedDictionaries.Add(new XamlControlsResources());
            DispatcherShutdownMode = DispatcherShutdownMode.OnExplicitShutdown;
            host = AppHost.Create(dispatcher, ReleaseUiAsync, Options);
            await host.StartAsync();
            windowManager = host.GetService<WindowManager>();
            uiDispatcher = host.GetService<IUiDispatcher>();
            trayFactory = host.GetService<ITrayIconFactory>();
            panelFactory = host.GetService<IPanelWindowFactory>();
            if (Options.OnStarted is { } onStarted)
            {
                await onStarted(this);
                return;
            }

            tray = trayFactory is null
                ? throw new InvalidOperationException("The tray icon factory is unavailable.")
                : await trayFactory.CreateAsync(IconPath(), Options.Identity.DisplayName);
            tray.Clicked += TogglePanel;
            tray.ShowRequested += ShowMain;
            tray.LinkReceived += OpenLink;
            tray.MenuRequested += ShowMenu;
            tray.ThemeChanged += UpdateTray;
            tray.Failure += TrayFailure;
            State.Changed += UpdateTray;
            State.NeedsSetup = State.Installation.BundledFlock is not null && !await State.Installation.IsSetUpAsync();

            if (!Options.Identity.IsDevelopment && !State.NeedsSetup && await State.Installation.NeedsUpdateAsync())
            {
                host.StartUpdateInstallation();
            }
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
        catch (Exception error)
        {
            Log(error);
            Native.ShowError("Flock could not start", $"{error.Message}\n\nDetails are in %LocalAppData%\\Flock\\tray.log.");
            await QuitAsync();
        }
    }

    /// <summary>Command line argument of the sign-in autostart.</summary>
    public const string BackgroundArgument = "--background";

    private string IconPath() => TrayIconPath(State, Native.TaskbarIsLight);

    /// <summary>The logomark for the daemon's state, colored for a light or a dark background.</summary>
    /// <param name="extension">.ico for the notification area; .png inside windows (WinUI scales up an .ico's smallest image).</param>
    public static string TrayIconPath(RuntimeService model, bool light, string extension = ".ico")
    {
        var state = !model.IsRunning || model.NeedsSetup ? "TrayStopped" : model.IsDegraded ? "TrayWarning" : "Tray";
        return Path.Combine(AppContext.BaseDirectory, "Assets", $"{state}.{(light ? "Light" : "Dark")}{extension}");
    }

    private void UpdateTray()
    {
        if (updatingTray)
        {
            return;
        }
        updatingTray = true;
        try
        {
            UpdateTrayCore();
        }
        finally
        {
            updatingTray = false;
        }
    }

    private void UpdateTrayCore()
    {
        var tooltip = Options.Identity.DisplayName + " — " + State.Headline;
        if (State.Status is { Warnings.Count: > 0 } status && !State.NeedsSetup)
        {
            tooltip += "\n" + status.Warnings[0];
        }
        tray?.Update(IconPath(), tooltip);
    }

    private void TogglePanel()
    {
        panel ??= panelFactory?.Create() ?? throw new InvalidOperationException("The panel window factory is unavailable.");
        panel.Toggle();
    }

    public void ShowPanel()
    {
        panel ??= panelFactory?.Create() ?? throw new InvalidOperationException("The panel window factory is unavailable.");
        panel.ShowNearTray();
    }

    /// <summary>Opens the app from outside the tray (a start by hand): the setup until Flock is set up, then Settings.</summary>
    private void ShowMain() => Open(State.NeedsSetup ? WindowKind.Setup : WindowKind.Settings);

    /// <summary>
    /// A flock:// link (registered by the installer). flock://install, e.g. from the website's
    /// install buttons, shows the service in Settings → Services (a PHP or Node.js version in
    /// Settings → PHP or Node), which asks before installing anything; before the setup the link
    /// waits, and the pane asks about it once Flock is set up.
    /// flock://secure, from the page served for a site that isn't secured, asks in Settings → Sites.
    /// flock://update, from `flock update`, installs an update of the app in Settings → General
    /// (before the setup without opening Settings).
    /// </summary>
    private void OpenLink(string? text)
    {
        if (UpdateLink.IsUpdateLink(text))
        {
            InstallUpdate(check: true);
            return;
        }
        if (SecureLink.Parse(text) is { } secure && !State.NeedsSetup)
        {
            State.PendingSecure = secure;
            Open(WindowKind.Settings, SettingsPane.Sites);
            return;
        }
        if (InstallLink.Parse(text) is not { } link)
        {
            ShowMain();
            return;
        }
        State.PendingInstall = link;
        if (State.NeedsSetup)
        {
            Open(WindowKind.Setup);
            return;
        }
        ShowInstall(link);
    }

    /// <summary>
    /// Opens the pane that asks about an install link: PHP and Node for their versions, Services
    /// (on the service's details) for everything else.
    /// </summary>
    public void ShowInstall(InstallLink link)
    {
        switch (link.Service)
        {
            case InstallLink.Php:
                Open(WindowKind.Settings, SettingsPane.Php);
                break;
            case InstallLink.Node:
                Open(WindowKind.Settings, SettingsPane.Node);
                break;
            default:
                Open(WindowKind.Settings, SettingsPane.Services);
                windowManager?.Get<SettingsWindow>(WindowKind.Settings)?.ShowService(link.Service);
                break;
        }
    }

    private void ShowMenu()
    {
        if (tray is null)
        {
            return;
        }
        // While an update is available it follows the first item; the rest keep their order.
        var update = State.UpdateService.Available is { } available && !State.UpdateService.IsBusy ? $"Update to {available.Version}…" : null;
        // Before the setup there is nothing to open yet: Mail, Dumps, Logs and Settings all need the daemon.
        List<string?> items = State.NeedsSetup
            ? ["Set Up Flock…", null, "Quit Flock"]
            : ["Open Flock", null, "Mail", "Dumps", "Logs", "Settings…", null, State.IsRunning ? "Restart Flock" : "Start Flock", "Quit Flock"];
        if (update is not null)
        {
            items.Insert(1, update);
        }
        var choice = tray.ShowMenu([.. items], defaultItem: 0);
        if (update is not null && choice >= 1)
        {
            if (choice == 1)
            {
                InstallUpdate();
                return;
            }
            choice--;
        }
        if (State.NeedsSetup)
        {
            switch (choice)
            {
                case 0: Open(WindowKind.Setup); break;
                case 2: Quit(); break;
            }
            return;
        }
        switch (choice)
        {
            case 0: ShowPanel(); break;
            case 2: Open(WindowKind.Mail); break;
            case 3: Open(WindowKind.Dumps); break;
            case 4: Open(WindowKind.Logs); break;
            case 5: Open(WindowKind.Settings); break;
            case 7: _ = State.RestartAsync(); break;
            case 8: Quit(); break;
        }
    }

    /// <summary>
    /// Installs the available update, showing it in Settings → General once Flock is set up. Before
    /// the setup the silent installer runs on its own: Settings has nothing to show yet.
    /// </summary>
    public void InstallUpdate(bool check = false)
    {
        if (updateTask is { IsCompleted: false })
        {
            return;
        }
        if (!State.NeedsSetup)
        {
            Open(WindowKind.Settings, SettingsPane.General);
        }
        updateTask = ObserveUpdateAsync(check);
    }

    private async Task ObserveUpdateAsync(bool check)
    {
        try
        {
            await (check ? State.UpdateService.CheckAndInstallAsync() : State.UpdateService.InstallAsync());
        }
        catch (Exception error)
        {
            Log(error);
        }
    }

    /// <summary>Opens a window, or brings it to the front when it is already open. Until Flock is set up, that is the setup.</summary>
    public void Open(WindowKind kind, SettingsPane? pane = null)
    {
        panel?.Hide();
        if (kind == WindowKind.Logs && windowManager is not null)
        {
            _ = OpenLogsCoreAsync();
            return;
        }
        if (State.NeedsSetup)
        {
            kind = WindowKind.Setup;
        }
        var window = windowManager?.Open(kind);
        if (window is null)
        {
            return;
        }
        if (pane is { } p && window is SettingsWindow settings)
        {
            settings.Select(p);
        }
        ((FlockWindow)window).Present();
    }

    /// <summary>Opens the Logs window on one log.</summary>
    public void OpenLog(LogsViewModel.Source source)
    {
        _ = OpenLogsCoreAsync(source);
    }

    private async Task OpenLogsCoreAsync(LogsViewModel.Source? source = null)
    {
        try
        {
            panel?.Hide();
            var kind = State.NeedsSetup ? WindowKind.Setup : WindowKind.Logs;
            var window = windowManager is null ? null : await windowManager.OpenAsync(kind);
            if (source is not null && window is LogsWindow logs)
            {
                await logs.ShowSourceAsync(source);
            }
            if (window is FlockWindow flockWindow)
            {
                flockWindow.Present();
            }
        }
        catch (Exception error)
        {
            Log(error);
        }
    }

    public void Quit() => _ = QuitAsync();

    public Task QuitAsync()
    {
        if (quitTask is not null)
        {
            return quitTask;
        }
        return quitTask = QuitCoreAsync();
    }

    private async Task QuitCoreAsync()
    {
        try
        {
            if (host is not null)
            {
                await host.QuitAsync();
            }
            else
            {
                await ReleaseUiAsync();
            }
        }
        catch (Exception error)
        {
            Log(error);
        }
        if (dispatcher is { HasThreadAccess: true })
        {
            Exit();
        }
        else
        {
            dispatcher?.TryEnqueue(Exit);
        }
    }

    /// <summary>Appends an unexpected error to %LocalAppData%\Flock\tray.log.</summary>
    public static void Log(Exception e)
    {
        if (Application.Current is App { host: { } appHost } && appHost.GetService<FileLogSink>() is { } sink)
        {
            sink.TryWrite($"{DateTimeOffset.Now:O} fatal: {e}");
            return;
        }
        FileLogSink.WriteEmergency(e);
    }

    private async Task ReleaseUiAsync()
    {
        if (uiDispatcher is not null)
        {
            await uiDispatcher.InvokeAsync(ReleaseUiOnDispatcherAsync, CancellationToken.None);
            return;
        }
        await ReleaseUiOnDispatcherAsync();
    }

    private async Task ReleaseUiOnDispatcherAsync()
    {
        if (windowManager is not null)
        {
            await windowManager.CloseAllAsync();
        }
        panel?.Close();
        panel = null;
        if (tray is not null)
        {
            tray.Clicked -= TogglePanel;
            tray.ShowRequested -= ShowMain;
            tray.LinkReceived -= OpenLink;
            tray.MenuRequested -= ShowMenu;
            tray.ThemeChanged -= UpdateTray;
            tray.Failure -= TrayFailure;
        }
        tray?.Dispose();
        tray = null;
    }

    private void TrayFailure(string message) => State.ShowRuntimeError(message);
}
