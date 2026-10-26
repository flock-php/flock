using System.Reflection;
using Flock.Kit;
using Flock.Tray.ViewModels;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed class MailPane(SettingsWindow window, RuntimeService model) : Pane(window, model)
{
    private readonly MailSettingsViewModel viewModel = new(model);
    public override string Title => "Mail";
    public override string Subtitle => "Flock catches every email your apps send — nothing leaves your PC.";

    public override UIElement Build()
    {
        var mail = viewModel.Mail;
        var port = viewModel.Port;
        var running = viewModel.Running;

        return Ui.Column(0,
            Ui.Section("SMTP server",
                Ui.Setting("Host", null, Ui.Text("127.0.0.1", foreground: Ui.SecondaryText, wrap: false)),
                Ui.Setting("Port", null, Ui.Text(port.ToString(System.Globalization.CultureInfo.InvariantCulture), foreground: Ui.SecondaryText, wrap: false)),
                Ui.Setting("Status", null, Ui.Row(8, Ui.StatusDot(running), Ui.Text(running ? "Running" : "Stopped", wrap: false)))),
            Ui.Section("Environment variables",
                Ui.Card(Ui.CodeBlock(viewModel.Environment)),
                GeneralPane.Footnote("MAIL_USERNAME picks the inbox, so every project gets its own.")),
            Ui.Section(null,
                Ui.Setting("Caught emails", (Model.Status?.MailCount ?? 0).ToString(System.Globalization.CultureInfo.CurrentCulture),
                    Ui.Button("Open Mail", () => App.Current.Open(WindowKind.Mail)))));
    }
}

internal sealed class DumpsPane(SettingsWindow window, RuntimeService model) : Pane(window, model)
{
    private readonly DumpsViewModel viewModel = new(model);
    public override string Title => "Dumps";
    public override string Subtitle => "Send dump() and dd() output to Flock instead of the page or terminal — no changes to your projects.";

    public override UIElement Build()
    {
        var capture = new ToggleSwitch { IsOn = viewModel.Capturing };
        capture.Toggled += (_, _) => _ = viewModel.SetDumpsAsync(capture.IsOn);

        var editor = new ComboBox { MinWidth = 160 };
        foreach (var e in EditorInfo.All)
        {
            editor.Items.Add(e.Name());
        }
        editor.SelectedIndex = EditorInfo.All.ToList().IndexOf(EditorInfo.Parse(Model.Preferences.Snapshot.Editor));
        editor.SelectionChanged += async (_, _) =>
        {
            if (editor.SelectedIndex >= 0)
            {
                try
                {
                    await Model.Preferences.UpdateAsync(value => value with { Editor = EditorInfo.All[editor.SelectedIndex].ToString() });
                }
                catch (OperationCanceledException)
                {
                }
                catch (Exception error) when (error is IOException or UnauthorizedAccessException)
                {
                }
            }
        };

        return Ui.Column(0,
            Ui.Section(null,
                Ui.Setting("Capture dumps", "Turn it off to see dumps in the page again.", capture),
                Ui.Setting("Captured", viewModel.Count.ToString(System.Globalization.CultureInfo.CurrentCulture),
                    Ui.Button("Open Dumps", () => App.Current.Open(WindowKind.Dumps)))),
            Ui.Section("Sources", Ui.Setting("Open files in", "Used when clicking a dump's file and line.", editor)));
    }
}

internal sealed class DebuggerPane(SettingsWindow window, RuntimeService model) : Pane(window, model)
{
    private readonly DebuggerViewModel viewModel = new(model);
    public override string Title => "Debugger";
    public override string Subtitle => "Step through requests and commands with Xdebug.";

    public override UIElement Build()
    {
        var enabled = viewModel.Enabled;
        var detect = new ToggleSwitch { IsOn = enabled };
        detect.Toggled += (_, _) => _ = Model.SetXdebugAsync(detect.IsOn);
        var breakpoints = new ToggleSwitch { IsOn = viewModel.Breakpoints, IsEnabled = enabled };
        breakpoints.Toggled += (_, _) => _ = viewModel.SetBreakpointsAsync(breakpoints.IsOn);

        var debugging = new List<UIElement>();
        if (enabled && breakpoints.IsOn)
        {
            foreach (var site in Model.Status?.Sites ?? [])
            {
                if (site.Debugging)
                {
                    debugging.Add(Ui.Setting(site.Domain, "Breakpoints in PhpStorm", null, Ui.GlyphBug));
                }
            }
        }

        var availability = new List<UIElement>();
        foreach (var php in viewModel.Config?.Php ?? [])
        {
            UIElement state = php.XdebugAvailable
                ? Ui.Row(6, Ui.Icon("\uE73E", 12, Ui.Success), Ui.Text("Available", wrap: false))
                : Ui.Row(6, Ui.Icon("\uE711", 12, Ui.SecondaryText), Ui.Text("Not in this build", foreground: Ui.SecondaryText, wrap: false));
            availability.Add(Ui.Setting($"PHP {php.Version}", null, state));
        }

        var column = Ui.Column(0,
            Ui.Section(null,
                Ui.Setting("Detect Xdebug requests", "Xdebug loads only for requests that ask for it, so everything else stays fast.", detect),
                Ui.Setting("Detect PhpStorm breakpoints", "While a project has breakpoints in PhpStorm, every request to its site is debugged.", breakpoints)));
        if (debugging.Count > 0)
        {
            column.Children.Add(Ui.Section("Debugging now", [.. debugging]));
        }
        column.Children.Add(Ui.Section("Connect your IDE",
            Ui.Setting("Client port", null, Ui.Text("9003", foreground: Ui.SecondaryText, wrap: false)),
            Ui.Setting("Mode", null, Ui.Text("debug, develop", foreground: Ui.SecondaryText, wrap: false)),
            Ui.Setting("Start a session", "Set a breakpoint in PhpStorm and listen for debug connections, use the Xdebug browser extension, or add ?XDEBUG_TRIGGER=1 to the URL. On the command line, run `flock debug artisan …`.", null)));
        column.Children.Add(Ui.Section("Availability", [.. availability]));
        return column;
    }
}

internal sealed class AboutPane(SettingsWindow window, RuntimeService model) : Pane(window, model)
{
    public override string Title => "About";
    public override string Subtitle => "A native local development environment for Laravel.";

    public override UIElement Build()
    {
        var icon = new Image
        {
            Source = new Microsoft.UI.Xaml.Media.Imaging.BitmapImage(new Uri(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.png"))),
            Width = 72,
            Height = 72,
        };
        var version = Assembly.GetExecutingAssembly().GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion ?? "dev";

        var header = Ui.Row(20, icon, Ui.Column(2, Ui.Text("Flock", "TitleLargeTextBlockStyle"), Ui.Text("A native local development environment for Laravel.", foreground: Ui.SecondaryText)));
        header.Margin = new Thickness(0, 0, 0, 24);

        return Ui.Column(0, header,
            Ui.Section(null,
                Ui.Setting("App", null, Ui.Text(version.Split('+')[0], foreground: Ui.SecondaryText, wrap: false)),
                Ui.Setting("Daemon", null, Ui.Text(Model.Status?.Version ?? "not running", foreground: Ui.SecondaryText, wrap: false)),
                Ui.Setting("Data folder", FlockPaths.Root, Ui.Button("Show in Explorer", () => Model.Reveal(FlockPaths.Root))),
                Ui.Setting("Flock documentation", null, Ui.Button("Open", () => Model.Open("https://flockphp.com/docs"))),
                Ui.Setting("Found a bug?", "Opens a new GitHub issue with your versions filled in.", Ui.Button("Report an issue", () => Model.Open(IssueUrl(version.Split('+')[0])))),
                Ui.Setting("License", "Copyright © Guava s.r.o. Licensed under the AGPL-3.0.", Ui.Button("View", () => Model.Open("https://github.com/flock-php/flock/blob/main/LICENSE")))),
            Ui.Section("Uninstall",
                Ui.Setting("Uninstall Flock", "Stops Flock and removes it from this PC. Your sites, databases and settings are kept.",
                    Ui.Button("Uninstall Flock…", () => _ = ConfirmUninstall()))));
    }

    // IssueUrl opens a blank GitHub issue with the versions filled in, so reports say what they run on.
    private string IssueUrl(string appVersion)
    {
        var body = $"\n\n---\nApp: {appVersion}\nDaemon: {Model.Status?.Version ?? "not running"}\nOS: {System.Runtime.InteropServices.RuntimeInformation.OSDescription}\n";
        return "https://github.com/flock-php/flock/issues/new?body=" + Uri.EscapeDataString(body);
    }

    private async Task ConfirmUninstall()
    {
        if (Installation.Uninstaller is null)
        {
            Model.ShowRuntimeError("This build of Flock wasn't installed with Flock-Setup.exe. Run `flock uninstall` in a terminal and delete the app's folder.");
            return;
        }
        var purge = new CheckBox { Content = "Also delete my sites' settings and databases" };
        var dialog = new ContentDialog
        {
            Title = "Uninstall Flock?",
            Content = Ui.Column(12,
                Ui.Text("Flock stops and is removed from your PATH, together with its PHP and Node.js versions, certificates, logs and caches. Windows asks for permission to remove the hosts helper and Flock's trusted certificate."),
                Ui.Text($"Your sites' settings and your PostgreSQL and Redis databases stay in {FlockPaths.Root}, so reinstalling picks up where you left off — unless you also delete them:", foreground: Ui.SecondaryText),
                purge),
            PrimaryButtonText = "Uninstall",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot,
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary && Installation.StartUninstaller(purge.IsChecked == true))
        {
            App.Current.Quit();
        }
    }
}
