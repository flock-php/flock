using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Node.js lines from nodejs.org, like the PHP pane: install, update, make default and uninstall
/// them. Exact versions a project pins (.nvmrc) are offered by `node` in the terminal instead.
/// </summary>
internal sealed class NodePane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    // Majors with an install or uninstall in flight (and what to show meanwhile); the daemon answers when it's done.
    private readonly Dictionary<string, string> busy = [];
    private bool checking;

    public override string Title => "Node";
    public override string Subtitle => "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.";

    public override void Shown(CancellationToken hidden) => _ = Store.RefreshNodeAsync();

    public override UIElement Build()
    {
        if (Store.Node is not { } catalog)
        {
            return Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Text("Loading Node.js versions…", foreground: Ui.SecondaryText));
        }

        var checkedText = catalog.CheckedAt is { } at ? $"Checked {Ui.Relative(at)}" : "Not checked yet";
        UIElement check = checking
            ? new ProgressRing { Width = 14, Height = 14 }
            : Ui.Link("Check now", async () =>
            {
                checking = true;
                Rerender();
                await Store.PerformOnNodeAsync(c => c.CheckNodeReleasesAsync());
                checking = false;
                Rerender();
            });
        var header = Ui.Spread(Ui.Text("Versions", "BodyStrongTextBlockStyle"), Ui.Row(6, Ui.Caption(checkedText, wrap: false), check));
        header.Margin = new Thickness(1, 0, 0, 6);

        var versions = new StackPanel { Spacing = 4, Margin = new Thickness(0, 0, 0, 24) };
        versions.Children.Add(header);
        foreach (var release in catalog.Versions)
        {
            versions.Children.Add(Row(release));
        }
        if (catalog.Versions.Count == 0)
        {
            versions.Children.Add(Ui.Card(Ui.Text("No Node.js versions to show yet.", foreground: Ui.SecondaryText)));
        }
        if (catalog.CheckError.Length > 0)
        {
            versions.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption($"Couldn't reach nodejs.org: {catalog.CheckError}", Ui.Caution)));
        }
        versions.Children.Add(GeneralPane.Footnote("Official builds from nodejs.org, verified against their checksums. A project that pins an exact version in .nvmrc gets it offered when you run node, npm or npx there. "
            + "Global packages (npm install -g) belong to a Node line and survive its updates; those of the default version are on your PATH."));
        return versions;
    }

    private Border Row(NodeRelease release)
    {
        var title = Ui.Row(8, Ui.Text($"Node {release.Major}", "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(VersionText(release), wrap: false));
        if (release.IsDefault)
        {
            title.Children.Add(Ui.Badge("Default"));
        }
        if (SupportBadge(release) is { } badge)
        {
            title.Children.Add(badge);
        }

        var icon = Ui.Icon(Ui.GlyphTerminal, 20, release.IsInstalled ? Ui.Accent : Ui.TertiaryText);
        var info = Ui.Row(14, icon, Ui.Column(2, title, Detail(release)));
        return Ui.Card(Ui.Spread(info, Actions(release)));
    }

    private static string VersionText(NodeRelease release) =>
        release.Update ? $"{release.Installed} → {release.Latest}" : release.IsInstalled ? release.Installed : release.Latest;

    private static Border? SupportBadge(NodeRelease release)
    {
        var until = release.EndOfLife.Length > 0 ? $" until {release.EndOfLife}" : "";
        var (text, tone, tip) = release.Support switch
        {
            NodeSupport.Lts => (release.Codename.Length > 0 ? $"LTS · {release.Codename}" : "LTS", Ui.Tone.Accent, $"Long-term support{until}"),
            NodeSupport.Maintenance => ("Maintenance", Ui.Tone.Neutral, $"Security fixes only{until}"),
            NodeSupport.Current => ("Current", Ui.Tone.Neutral, $"The newest line, without long-term support{until}"),
            NodeSupport.EndOfLife => ("End of life", Ui.Tone.Caution, release.EndOfLife.Length > 0 ? $"No more updates since {release.EndOfLife}" : "No more updates"),
            _ => (null, Ui.Tone.Neutral, ""),
        };
        if (text is null)
        {
            return null;
        }
        var badge = Ui.Badge(text, tone);
        ToolTipService.SetToolTip(badge, tip);
        return badge;
    }

    /// <summary>The status line under the name.</summary>
    private UIElement Detail(NodeRelease release)
    {
        if (busy.TryGetValue(release.Major, out var step))
        {
            return Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(step, wrap: false));
        }
        var (status, color) = release switch
        {
            { Update: true } => ("Update available", Ui.Accent),
            { IsInstalled: true } => ("Up to date", Ui.SecondaryText),
            _ => ("Not installed", Ui.SecondaryText),
        };
        var line = Ui.Row(0, Ui.Caption(status, color, wrap: false));
        if (release.Npm.Length > 0)
        {
            line.Children.Add(Ui.Caption($" · npm {release.Npm}", wrap: false));
            if (release.NpmLatest.Length > 0)
            {
                line.Children.Add(Ui.Caption($" (npm {release.NpmLatest} available)", Ui.Accent, wrap: false));
            }
        }
        return line;
    }

    private StackPanel Actions(NodeRelease release)
    {
        var actions = Ui.Row(8);
        if (busy.ContainsKey(release.Major))
        {
            return actions;
        }
        if (!release.IsInstalled)
        {
            actions.Children.Add(Ui.Button("Install", () => _ = Run(release.Major, $"Downloading Node.js {release.Latest}…", c => c.InstallNodeMajorAsync(release.Major)),
                glyph: Ui.GlyphDownload, tooltip: $"Downloads Node.js {release.Latest} from nodejs.org"));
            return actions;
        }
        if (release.Update)
        {
            actions.Children.Add(Ui.Button($"Update to {release.Latest}", () => _ = Run(release.Major, $"Downloading Node.js {release.Latest}…", c => c.InstallNodeMajorAsync(release.Major)),
                accent: true, glyph: Ui.GlyphDownload, tooltip: $"Downloads Node.js {release.Latest}, then removes {release.Installed}. Global packages stay."));
        }
        if (release.NpmLatest.Length > 0)
        {
            actions.Children.Add(Ui.Button($"Update npm", () => _ = Run(release.Major, $"Updating npm to {release.NpmLatest}…", c => c.UpdateNpmAsync(release.Major)),
                glyph: Ui.GlyphDownload, tooltip: $"Installs npm {release.NpmLatest}, the newest npm that supports Node.js {release.Installed}, for Node {release.Major}"));
        }
        if (!release.IsDefault)
        {
            actions.Children.Add(Ui.Button("Make Default", () => _ = MakeDefault(release.Major)));
        }
        actions.Children.Add(More(release));
        return actions;
    }

    private Button More(NodeRelease release)
    {
        var uninstall = new MenuFlyoutItem { Text = "Uninstall…", IsEnabled = !release.IsDefault };
        uninstall.Click += async (_, _) =>
        {
            if (await Window.Confirm($"Uninstall Node {release.Major}?",
                    $"Its global npm packages are removed too. Projects and sites that use Node {release.Major} fall back to the default version until you install it again.", "Uninstall"))
            {
                await Run(release.Major, "Uninstalling…", c => c.UninstallNodeAsync(release.Major));
            }
        };
        var menu = new MenuFlyout();
        menu.Items.Add(uninstall);
        var button = Ui.IconButton(Ui.GlyphMore, "More", () => { });
        button.Flyout = menu;
        return button;
    }

    private async Task Run(string major, string step, Func<FlockClient, Task<NodeCatalog>> action)
    {
        busy[major] = step;
        Rerender();
        try
        {
            await Store.PerformOnNodeAsync(action);
        }
        finally
        {
            busy.Remove(major);
            Rerender();
        }
    }

    private async Task MakeDefault(string major)
    {
        await Perform(c => c.UpdateSettingsAsync(new SettingsChange(DefaultNode: major)));
        await Store.RefreshNodeAsync();
    }
}
