using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>First-run setup and installation progress.</summary>
internal sealed partial class SetupWindow : FlockWindow
{
    private enum Stage { Intro, Checking, Conflicts, Running, Done, Failed }

    private readonly RuntimeService model;
    private string output = "";
    private List<ConflictingApp> conflicts = [];
    private string? startupProblem;
    private Stage stage;
    private string defaultPhp = "", defaultNode = "";
    private bool sawDefaults;

    public SetupWindow(RuntimeService model) : base("Set up Flock", 620, 600, minWidth: 480, minHeight: 480)
    {
        this.model = model;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        model.Changed += ShowDefaults;
        Closed += (_, _) => model.Changed -= ShowDefaults;
        Render(Stage.Intro);
    }

    private void Render(Stage value)
    {
        stage = value;
        IntroStage.Visibility = value == Stage.Intro ? Visibility.Visible : Visibility.Collapsed;
        CheckingStage.Visibility = value == Stage.Checking ? Visibility.Visible : Visibility.Collapsed;
        ConflictsStage.Visibility = value == Stage.Conflicts ? Visibility.Visible : Visibility.Collapsed;
        RunningStage.Visibility = value == Stage.Running ? Visibility.Visible : Visibility.Collapsed;
        DoneStage.Visibility = value == Stage.Done ? Visibility.Visible : Visibility.Collapsed;
        FailedStage.Visibility = value == Stage.Failed ? Visibility.Visible : Visibility.Collapsed;
        if (value == Stage.Conflicts)
        {
            ConflictsItems.ItemsSource = conflicts;
        }
        if (value == Stage.Failed)
        {
            var declined = output.Contains("administrator permission was declined", StringComparison.Ordinal);
            FailedMessage.Text = declined
                ? "Windows' permission prompt was declined. Flock runs, but *.test sites won't resolve and HTTPS isn't trusted until you allow it."
                : "Something went wrong. The output below says what; you can try again.";
            FailedLogText.Text = output;
        }
        if (value == Stage.Done)
        {
            StartupProblem.Text = startupProblem ?? "";
            StartupProblem.Visibility = startupProblem is null ? Visibility.Collapsed : Visibility.Visible;
            ShowDefaults();
        }
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
        LogText.Text = "";
        Render(Stage.Running);
        var result = await model.Installation.InstallAsync(line =>
        {
            output += line + "\n";
            LogText.Text = output.TrimEnd();
        }, retry: retry);
        output = result.Output;
        LogText.Text = output;

        model.NeedsSetup = !await model.Installation.IsSetUpAsync();
        if (result.Ok)
        {
            try
            {
                Native.StartsAtSignIn = Startup.IsChecked == true;
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

    private void ShowDefaults()
    {
        if (stage != Stage.Done || model.Status is not { } status)
        {
            return;
        }
        Defaults.Children.Clear();
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
                Defaults.Children.Add(Ui.Card(Ui.Column(8, Ui.Text($"Installing {names}…"), new ProgressBar { IsIndeterminate = true }, Ui.Caption(detail))));
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
                    Defaults.Children.Add(Ui.Card(row));
                }
            }
        }
        if (Defaults.Children.Count == 0 && sawDefaults)
        {
            Defaults.Children.Add(Ui.Card(Ui.Row(10, Ui.Icon(Ui.GlyphCheck, 14, Ui.Success), Ui.Text($"{Named("PHP", defaultPhp)} and {Named("Node.js", defaultNode)} are installed"))));
        }
        Defaults.Visibility = Defaults.Children.Count > 0 ? Visibility.Visible : Visibility.Collapsed;

        static string Named(string product, string version) => version.Length > 0 ? $"{product} {version}" : product;
    }

    private void CloseClicked(object sender, RoutedEventArgs args) => Close();
    private void RunClicked(object sender, RoutedEventArgs args) => _ = RunAsync();
    private void RetryClicked(object sender, RoutedEventArgs args) => _ = RunAsync(retry: true);

    private void OpenSettingsClicked(object sender, RoutedEventArgs args)
    {
        Close();
        var firstRun = model.Status?.Defaults;
        if (model.PendingInstall is { } link)
        {
            App.Current.ShowInstall(link);
        }
        else
        {
            App.Current.Open(WindowKind.Settings, firstRun?.Php.IsFailed == true ? SettingsPane.Php : firstRun?.Node.IsFailed == true ? SettingsPane.Node : null);
        }
    }
}
