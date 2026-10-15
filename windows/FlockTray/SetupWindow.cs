using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// First start after installing: explains what setting Flock up does, runs the bundled
/// `flock install` (one UAC prompt) and shows its output. The macOS app's onboarding sheet.
/// </summary>
internal sealed class SetupWindow : FlockWindow
{
    private enum Stage { Intro, Running, Done, Failed }

    private readonly AppModel model;
    private readonly TextBlock log = Ui.MonoText("", 12);
    private string output = "";

    public SetupWindow(AppModel model) : base("Set up Flock", 620, 600, minWidth: 480, minHeight: 480)
    {
        this.model = model;
        Render(Stage.Intro);
    }

    private void Render(Stage stage)
    {
        var page = new StackPanel { Padding = new Thickness(32, 8, 32, 24), Spacing = 0 };
        switch (stage)
        {
            case Stage.Intro:
                page.Children.Add(Header("Welcome to Flock", "Flock runs your Laravel sites, PHP, mail and databases on this PC. Setting it up takes a moment:"));
                page.Children.Add(Ui.Section(null,
                    Ui.Setting("Runs in the background", "The Flock daemon serves your sites and services, and starts whenever you sign in.", null, Ui.GlyphServer),
                    Ui.Setting("php, composer and node in your terminal", "Flock's versions come first on your PATH. Open a new terminal after setup.", null, Ui.GlyphTerminal),
                    Ui.Setting("*.test sites with trusted HTTPS", "Windows asks once for administrator permission: for the small service that adds your sites to the hosts file, and to trust Flock's certificate.", null, Ui.GlyphLock)));
                page.Children.Add(Buttons(
                    Ui.Button("Not now", Close),
                    Ui.Button("Set up Flock", () => _ = RunAsync(), accent: true)));
                break;

            case Stage.Running:
                page.Children.Add(Header("Setting up Flock…", "Windows asks for administrator permission in a moment."));
                page.Children.Add(new ProgressBar { IsIndeterminate = true, Margin = new Thickness(0, 0, 0, 16) });
                page.Children.Add(Log());
                break;

            case Stage.Done:
                page.Children.Add(Header("Flock is ready", null));
                page.Children.Add(Ui.Section("Next steps",
                    Ui.Setting("Open a new terminal", "Terminals that were already open still have the old PATH.", null, Ui.GlyphTerminal),
                    Ui.Setting("Link a project", "In a Laravel project, run `flock link`: the site is then at http://<folder>.test. `flock secure` adds HTTPS.", null, Ui.GlyphGlobe),
                    Ui.Setting("Install PHP", "Settings → PHP installs versions; running `php` in a terminal offers to install a missing one.", null, Ui.GlyphCode)));
                page.Children.Add(Buttons(
                    Ui.Button("Open Settings", () =>
                    {
                        Close();
                        App.Current.Open(WindowKind.Settings);
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
                    Ui.Button("Try Again", () => _ = RunAsync(), accent: true)));
                break;
        }
        Body = new ScrollViewer { Content = page };
    }

    private async Task RunAsync()
    {
        output = "";
        log.Text = "";
        Render(Stage.Running);

        var result = await Installation.InstallAsync(line =>
        {
            output += line + "\n";
            log.Text = output.TrimEnd();
        });
        output = result.Output;
        log.Text = output;

        model.NeedsSetup = !Installation.IsSetUp;
        if (result.Ok)
        {
            Installation.RecordInstalled();
        }
        Render(result.Ok ? Stage.Done : Stage.Failed);
        await model.RefreshAsync();
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
