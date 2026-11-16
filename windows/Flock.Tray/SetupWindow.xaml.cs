using System.Security;
using System.Text.RegularExpressions;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.Settings;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;
namespace Flock.Tray;

/// <summary>First-run setup and installation progress.</summary>
internal sealed partial class SetupWindow : FlockWindow
{

    private readonly RuntimeService model;
    private List<ConflictingApp> conflicts = [];
    private string defaultPhp = "", defaultNode = "";
    private string? dnsWarning;
    private string output = "";
    private bool sawDefaults;
    private Stage stage;
    private string? startupProblem;

    public SetupWindow(RuntimeService model) : base(Strings.Setup_SetUpFlock, 620, 600, 480, 480)
    {
        this.model = model;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        LanguageHost.Children.Add(Settings.LanguageSetting.Create(model.Preferences));
        ShowLinks(StarText, Strings.Setup_Star);
        model.Changed += ShowDefaults;
        Closed += (_, _) => model.Changed -= ShowDefaults;
        Closed += (_, _) => SkipWslSetup();
        if (model.Installation.Backend.WslDistro is { } distro)
        {
            ShowWslSetup(distro);
        }
        Render(Stage.Intro);
    }

    /// <summary>
    ///     A daemon in WSL is set up in its distro: here, setup is only what Windows needs to open its
    ///     sites. The app isn't registered to open at sign-in, where it would look for a daemon on Windows.
    /// </summary>
    private void ShowWslSetup(string distro)
    {
        IntroLead.Text = Localization.Format(Strings.Setup_WslIntro, distro);
        DaemonCard.Visibility = Visibility.Collapsed;
        TerminalCard.Visibility = Visibility.Collapsed;
        TrustTitle.Text = Strings.Setup_WslTrustedHttpsTitle;
        TrustDetail.Text = Localization.Format(Strings.Setup_WslTrustedHttpsDescription, distro);
        Startup.Visibility = Visibility.Collapsed;
        NextStepsTitle.Visibility = Visibility.Collapsed;
        NextSteps.Visibility = Visibility.Collapsed;
    }

    /// <summary>Closing the window without setting up ("Not now") leaves it until the next start.</summary>
    private void SkipWslSetup()
    {
        if (model.NeedsSetup && !model.Installation.IsInstalling && model.Installation.SkipSetup())
        {
            model.NeedsSetup = false;
            model.NotifyChanged();
        }
    }

    /// <summary>A text with Markdown links, [label](url), as the translations write them (the macOS
    /// and Linux apps share the text).</summary>
    private static void ShowLinks(TextBlock block, string text)
    {
        var start = 0;
        foreach (Match link in MarkdownLink().Matches(text))
        {
            block.Inlines.Add(new Run { Text = text[start..link.Index] });
            var hyperlink = new Hyperlink { NavigateUri = new Uri(link.Groups[2].Value) };
            hyperlink.Inlines.Add(new Run { Text = link.Groups[1].Value });
            block.Inlines.Add(hyperlink);
            start = link.Index + link.Length;
        }
        block.Inlines.Add(new Run { Text = text[start..] });
    }

    [GeneratedRegex(@"\[([^\]]+)\]\(([^)]+)\)")]
    private static partial Regex MarkdownLink();

    private void Render(Stage value)
    {
        stage = value;
        IntroStage.Visibility = value == Stage.Intro ? Visibility.Visible : Visibility.Collapsed;
        CheckingStage.Visibility = value == Stage.Checking ? Visibility.Visible : Visibility.Collapsed;
        ConflictsStage.Visibility = value == Stage.Conflicts ? Visibility.Visible : Visibility.Collapsed;
        RunningStage.Visibility = value == Stage.Running ? Visibility.Visible : Visibility.Collapsed;
        DoneStage.Visibility = value == Stage.Done ? Visibility.Visible : Visibility.Collapsed;
        FailedStage.Visibility = value == Stage.Failed ? Visibility.Visible : Visibility.Collapsed;
        switch (value)
        {
            case Stage.Conflicts:
                ConflictsItems.ItemsSource = conflicts;
                break;
            case Stage.Failed:
            {
                var declined = output.Contains("administrator permission was declined", StringComparison.Ordinal);
                FailedMessage.Text = declined
                    ? Strings.Setup_Declined
                    : Strings.Setup_Failed;
                FailedLogText.Text = output;
                break;
            }
            case Stage.Done:
                StartupProblem.Text = startupProblem ?? "";
                StartupProblem.Visibility = startupProblem is null ? Visibility.Collapsed : Visibility.Visible;
                DnsWarning.Message = dnsWarning ?? string.Empty;
                DnsWarning.IsOpen = dnsWarning is not null;
                ShowDefaults();
                break;
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
        // Success hides the running log. The Group Policy line is the only signal that Windows
        // ignores the DNS rule, so the done stage has to show it.
        dnsWarning = result.Ok ? DnsWarningText(output) : null;
        if (result.Ok && !model.Installation.Backend.IsWsl)
        {
            try
            {
                Native.StartsAtSignIn = Startup.IsChecked == true;
            }
            catch (Exception e) when (e is UnauthorizedAccessException or SecurityException or IOException)
            {
                startupProblem = Localization.Format(Strings.Setup_CouldNotChangeStartup, e.Message);
            }
        }
        model.NotifyChanged();
        Render(result.Ok ? Stage.Done : Stage.Failed);
        await model.RefreshAsync();
    }

    /// <summary>
    ///     The Group Policy warning from setup output, as a sentence, or null. The <c>!</c> is a log
    ///     marker, not something to show on the done stage.
    /// </summary>
    internal static string? DnsWarningText(string output)
    {
        foreach (var raw in output.Split('\n'))
        {
            var line = raw.TrimEnd('\r').Trim();
            if (line.StartsWith('!'))
            {
                line = line[1..].TrimStart();
            }
            if (!line.Contains("Group Policy", StringComparison.Ordinal) || line.Length == 0)
            {
                continue;
            }
            var sentence = char.ToUpperInvariant(line[0]) + line[1..];
            return sentence.EndsWith('.') ? sentence : sentence + ".";
        }
        return null;
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
                var both = pending.Count > 1;
                var names = both ? Localization.Format(Strings.Setup_TwoNames, pending[0].Name, pending[1].Name) : pending[0].Name;
                var waiting = pending.All(d => !d.Install.IsInstalling);
                var detail = waiting
                    ? Localization.Format(both ? Strings.Setup_InstalledWhenDownloadableBoth : Strings.Setup_InstalledWhenDownloadable, names)
                    : both ? Strings.Setup_UsedAsDefaultsBoth : Strings.Setup_UsedAsDefault;
                Defaults.Children.Add(Ui.Card(Ui.Column(8, Ui.Text(Localization.Format(Strings.Setup_Installing, names)), new ProgressBar { IsIndeterminate = true },
                    Ui.Caption(detail))));
            }
            foreach (var (name, install, pane) in new[] { (php, state.Php, "PHP"), (node, state.Node, "Node") })
            {
                if (install.IsFailed)
                {
                    var icon = Ui.Icon(Ui.GlyphWarning, 14, Ui.Critical);
                    icon.VerticalAlignment = VerticalAlignment.Top;
                    icon.Margin = new Thickness(0, 2, 0, 0);
                    var text = Ui.Caption(install.Error.Length > 0
                        ? Localization.Format(Strings.Setup_CouldNotInstallBecause, name, install.Error.TrimEnd('.'), pane)
                        : Localization.Format(Strings.Setup_CouldNotInstall, name, pane));
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
            Defaults.Children.Add(Ui.Card(Ui.Row(10, Ui.Icon(Ui.GlyphCheck, 14, Ui.Success),
                Ui.Text(Localization.Format(Strings.Setup_BothInstalled, Named("PHP", defaultPhp), Named("Node.js", defaultNode))))));
        }
        Defaults.Visibility = Defaults.Children.Count > 0 ? Visibility.Visible : Visibility.Collapsed;

        static string Named(string product, string version)
        {
            return version.Length > 0 ? $"{product} {version}" : product;
        }
    }

    private void CloseClicked(object sender, RoutedEventArgs args)
    {
        Close();
    }
    private void RunClicked(object sender, RoutedEventArgs args)
    {
        _ = RunAsync();
    }
    private void RetryClicked(object sender, RoutedEventArgs args)
    {
        _ = RunAsync(retry: true);
    }

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

    private enum Stage { Intro, Checking, Conflicts, Running, Done, Failed }
}