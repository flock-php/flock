using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// First start after installing: explains what setting Flock up does, makes sure no other local
/// environment (Herd, Laragon, ...) holds Flock's ports, runs the bundled `flock install` (one UAC
/// prompt) and shows its output. The macOS app's onboarding sheet.
/// </summary>
internal sealed class SetupWindow : FlockWindow
{
    private enum Stage { Intro, Checking, Conflicts, Running, Done, Failed }

    private readonly RuntimeService model;
    private readonly TextBlock log = Ui.MonoText("", 12);
    // Applied once setup succeeds; Settings → General changes it later.
    private readonly CheckBox startup = new() { Content = "Open Flock when I sign in", IsChecked = true, Margin = new Thickness(0, 12, 0, 0) };
    // The first run's install of the default PHP and Node.js, updated in place as the status changes.
    private readonly StackPanel defaults = new() { Spacing = 4, Margin = new Thickness(0, 0, 0, 24) };
    private string output = "";
    // The programs to quit before Flock can be set up, by app.
    private List<ConflictingApp> conflicts = [];
    private string? startupProblem;
    private Stage stage;
    // The versions the first run installs, as last seen: the status leaves them out once both are set.
    private string defaultPhp = "", defaultNode = "";
    private bool sawDefaults;

    public SetupWindow(RuntimeService model) : base("Set up Flock", 620, 600, minWidth: 480, minHeight: 480)
    {
        this.model = model;
        model.Changed += ShowDefaults;
        Closed += (_, _) => model.Changed -= ShowDefaults;
        Render(Stage.Intro);
    }

    private void Render(Stage stage)
    {
        this.stage = stage;
        var page = new StackPanel { Padding = new Thickness(32, 8, 32, 24), Spacing = 0 };
        switch (stage)
        {
            case Stage.Intro:
                page.Children.Add(Header("Welcome to Flock", "Flock runs your Laravel sites, PHP, mail and databases on this PC. Setting it up takes a moment:"));
                page.Children.Add(Ui.Section(null,
                    Ui.Setting("Runs in the background", "The Flock daemon serves your sites and services, and starts whenever you sign in.", null, Ui.GlyphServer),
                    Ui.Setting("php, composer and node in your terminal", "Flock's versions come first on your PATH. Open a new terminal after setup.", null, Ui.GlyphTerminal),
                    Ui.Setting("*.test sites with trusted HTTPS", "Windows asks once for administrator permission: for the small service that adds your sites to the hosts file, and to trust Flock's certificate.", null, Ui.GlyphLock)));
                page.Children.Add(startup);
                page.Children.Add(Buttons(
                    Ui.Button("Not now", Close),
                    Ui.Button("Set up Flock", () => _ = RunAsync(), accent: true)));
                break;

            case Stage.Checking:
                page.Children.Add(Header("Checking Flock's ports…", null));
                page.Children.Add(new ProgressBar { IsIndeterminate = true, Margin = new Thickness(0, 0, 0, 16) });
                break;

            case Stage.Conflicts:
                page.Children.Add(Header("Quit other local environments first",
                    "Flock needs these ports for itself, but other programs use them. Running both would leave neither working."));
                page.Children.Add(Ui.Section(null, [.. conflicts.Select(app => (UIElement)Ui.Setting(app.Title, app.Detail, null, Ui.GlyphWarning))]));
                page.Children.Add(Buttons(
                    Ui.Button("Not now", Close),
                    Ui.Button("Check again", () => _ = RunAsync(), accent: true)));
                break;

            case Stage.Running:
                page.Children.Add(Header("Setting up Flock…", "Windows asks for administrator permission in a moment."));
                page.Children.Add(new ProgressBar { IsIndeterminate = true, Margin = new Thickness(0, 0, 0, 16) });
                page.Children.Add(Log());
                break;

            case Stage.Done:
                page.Children.Add(Header("Flock is ready", null));
                if (defaults.Parent is Panel shown)
                {
                    shown.Children.Remove(defaults);
                }
                page.Children.Add(defaults);
                ShowDefaults();
                var steps = new List<UIElement>();
                if (startupProblem is not null)
                {
                    steps.Add(Ui.Setting("Open Flock at sign-in", startupProblem, null, Ui.GlyphWarning));
                }
                steps.AddRange([
                    Ui.Setting("Open a new terminal", "Terminals that were already open still have the old PATH.", null, Ui.GlyphTerminal),
                    Ui.Setting("Link a project", "In a Laravel project, run `flock link`: the site is then at http://<folder>.test. `flock secure` adds HTTPS.", null, Ui.GlyphGlobe),
                    Ui.Setting("PHP and Node.js", "Flock installs the newest PHP and Node.js LTS as the defaults. Settings → PHP and Node install other versions.", null, Ui.GlyphCode),
                ]);
                page.Children.Add(Ui.Section("Next steps", [.. steps]));
                page.Children.Add(Buttons(
                    Ui.Button("Open Settings", () =>
                    {
                        Close();
                        // A link that waited for the setup, or the pane to try a failed default again.
                        var firstRun = model.Status?.Defaults;
                        if (model.PendingInstall is { } link)
                        {
                            App.Current.ShowInstall(link);
                        }
                        else
                        {
                            App.Current.Open(WindowKind.Settings, firstRun?.Php.IsFailed == true ? SettingsPane.Php : firstRun?.Node.IsFailed == true ? SettingsPane.Node : null);
                        }
                    }),
                    Ui.Button("Done", Close, accent: true)));
                break;

            case Stage.Failed:
                var declined = output.Contains("administrator permission was declined", StringComparison.Ordinal);
                page.Children.Add(Header("Setup didn't finish", declined
                    ? "Windows' permission prompt was declined. Flock runs, but *.test sites won't resolve and HTTPS isn't trusted until you allow it."
                    : "Something went wrong. The output below says what; you can try again."));
                page.Children.Add(Log());
                page.Children.Add(Buttons(
                    Ui.Button("Close", Close),
                    Ui.Button("Try Again", () => _ = RunAsync(retry: true), accent: true)));
                break;
        }
        Body = new ScrollViewer { Content = page };
    }

    private async Task RunAsync(bool retry = false)
    {
        Render(Stage.Checking);
        conflicts = ConflictingApp.Group(await model.Installation.ConflictsAsync());
        if (conflicts.Count > 0)
        {
            Render(Stage.Conflicts);
            return;
        }

        output = "";
        log.Text = "";
        Render(Stage.Running);

        var result = await model.Installation.InstallAsync(line =>
        {
            output += line + "\n";
            log.Text = output.TrimEnd();
        }, retry: retry);
        output = result.Output;
        log.Text = output;

        model.NeedsSetup = !await model.Installation.IsSetUpAsync();
        if (result.Ok)
        {
            try
            {
                Native.StartsAtSignIn = startup.IsChecked == true;
            }
            catch (Exception e) when (e is UnauthorizedAccessException or System.Security.SecurityException or IOException)
            {
                startupProblem = $"Could not change the startup setting: {e.Message}. Settings → General has it too.";
            }
        }
        model.NotifyChanged();
        Render(result.Ok ? Stage.Done : Stage.Failed);
        await model.RefreshAsync();
    }

    /// <summary>
    /// Shows how far the first run's install of the default PHP and Node.js is (Status.Defaults),
    /// once the setup is done.
    /// </summary>
    private void ShowDefaults()
    {
        // While the daemon doesn't answer, keep what was shown.
        if (stage != Stage.Done || model.Status is not { } status)
        {
            return;
        }
        defaults.Children.Clear();
        if (status.Defaults is { } state)
        {
            sawDefaults = true;
            defaultPhp = state.Php.Version.Length > 0 ? state.Php.Version : defaultPhp;
            defaultNode = state.Node.Version.Length > 0 ? state.Node.Version : defaultNode;
            var php = Named("PHP", defaultPhp);
            var node = Named("Node.js", defaultNode);

            var pending = new[] { (Name: php, Install: state.Php), (Name: node, Install: state.Node) }.Where(d => d.Install.IsPending).ToList();
            if (pending.Count > 0)
            {
                var names = string.Join(" and ", pending.Select(d => d.Name));
                var waiting = pending.All(d => !d.Install.IsInstalling);
                var detail = waiting
                    ? $"{names} {(pending.Count > 1 ? "are" : "is")} installed as soon as Flock can download {(pending.Count > 1 ? "them" : "it")}."
                    : $"Your sites and terminals use {(pending.Count > 1 ? "them as the defaults" : "it as the default")}.";
                defaults.Children.Add(Ui.Card(Ui.Column(8,
                    Ui.Text($"Installing {names}…"),
                    new ProgressBar { IsIndeterminate = true },
                    Ui.Caption(detail))));
            }
            foreach (var (name, install, pane) in new[] { (php, state.Php, "PHP"), (node, state.Node, "Node") })
            {
                if (install.IsFailed)
                {
                    var reason = install.Error.Length > 0 ? $": {install.Error.TrimEnd('.')}" : "";
                    var icon = Ui.Icon(Ui.GlyphWarning, 14, Ui.Critical);
                    icon.VerticalAlignment = VerticalAlignment.Top;
                    icon.Margin = new Thickness(0, 2, 0, 0);
                    var text = Ui.Caption($"{name} couldn't be installed{reason}. Try again in Settings → {pane}.");
                    text.IsTextSelectionEnabled = true;
                    var row = new Grid { ColumnSpacing = 10 };
                    row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
                    row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
                    Grid.SetColumn(text, 1);
                    row.Children.Add(icon);
                    row.Children.Add(text);
                    defaults.Children.Add(Ui.Card(row));
                }
            }
        }
        // Both are installed. Not said after a setup that found the defaults set already.
        if (defaults.Children.Count == 0 && sawDefaults)
        {
            defaults.Children.Add(Ui.Card(Ui.Row(10, Ui.Icon(Ui.GlyphCheck, 14, Ui.Success),
                Ui.Text($"{Named("PHP", defaultPhp)} and {Named("Node.js", defaultNode)} are installed"))));
        }
        defaults.Visibility = defaults.Children.Count > 0 ? Visibility.Visible : Visibility.Collapsed;

        static string Named(string product, string version) => version.Length > 0 ? $"{product} {version}" : product;
    }

    private static StackPanel Header(string title, string? text)
    {
        var icon = new Image
        {
            Source = new Microsoft.UI.Xaml.Media.Imaging.BitmapImage(new Uri(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.png"))),
            Width = 48,
            Height = 48,
        };
        var header = Ui.Column(12, icon, Ui.Text(title, "TitleTextBlockStyle"));
        if (text is not null)
        {
            header.Children.Add(Ui.Text(text, foreground: Ui.SecondaryText));
        }
        header.Margin = new Thickness(0, 8, 0, 24);
        return header;
    }

    private Border Log()
    {
        // The log moves from screen to screen; take it out of the previous one first.
        if (log.Parent is ScrollViewer previous)
        {
            previous.Content = null;
        }
        return Ui.Card(new ScrollViewer
        {
            Content = log,
            MaxHeight = 260,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        }, new Thickness(12, 10, 12, 10));
    }

    private static StackPanel Buttons(params UIElement[] buttons)
    {
        var row = Ui.Row(8, buttons);
        row.HorizontalAlignment = HorizontalAlignment.Right;
        row.Margin = new Thickness(0, 8, 0, 0);
        return row;
    }
}
