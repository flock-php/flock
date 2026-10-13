using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed class NodePane : Pane
{
    // Kept across renders: the user may be typing while the daemon's state refreshes.
    private readonly TextBox version = new() { PlaceholderText = "e.g. 20 or 20.19.5", Width = 220 };
    private readonly Button install;
    private bool installing;
    private string? result;

    public NodePane(SettingsWindow window, AppModel model) : base(window, model)
    {
        install = Ui.Button("Install", () => _ = Install(), accent: true);
        version.KeyDown += (_, e) =>
        {
            if (e.Key == Windows.System.VirtualKey.Enter)
            {
                _ = Install();
            }
        };
        version.TextChanged += (_, _) => install.IsEnabled = !installing && version.Text.Trim().Length > 0;
    }

    public override string Title => "Node";
    public override string Subtitle => "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.";

    public override UIElement Build()
    {
        if (Store.Config is not { } config)
        {
            return new StackPanel();
        }
        var page = new StackPanel();

        // Installed majors plus the configured default, so the picker always has a match.
        var majors = config.NodeMajors;
        if (config.DefaultNode.Length > 0 && !majors.Contains(config.DefaultNode))
        {
            majors.Insert(0, config.DefaultNode);
        }
        var picker = new ComboBox { MinWidth = 140 };
        foreach (var major in majors)
        {
            picker.Items.Add($"Node {major}");
        }
        picker.SelectedIndex = majors.IndexOf(config.DefaultNode);
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedIndex >= 0)
            {
                var major = majors[picker.SelectedIndex];
                _ = Perform(c => c.UpdateSettingsAsync(new SettingsChange(DefaultNode: major)));
            }
        };
        page.Children.Add(Ui.Section("Default", Ui.Setting("Default version", "Used outside projects and by projects that don't name a version.", picker)));

        var installed = new List<UIElement>();
        if (config.Node.Count == 0)
        {
            installed.Add(Ui.Card(Ui.Text("No Node.js installed yet.", foreground: Ui.SecondaryText)));
        }
        foreach (var full in config.Node)
        {
            var name = Ui.Row(8, Ui.Text($"Node {full}", "BodyStrongTextBlockStyle", wrap: false));
            if (full.StartsWith(config.DefaultNode + ".", StringComparison.Ordinal))
            {
                name.Children.Add(Ui.Badge("Default"));
            }
            installed.Add(Ui.Card(Ui.Row(14, Ui.Icon(Ui.GlyphTerminal, 18, Ui.Success), name)));
        }
        page.Children.Add(Ui.Section("Installed versions", [.. installed]));

        install.IsEnabled = !installing && version.Text.Trim().Length > 0;
        version.IsEnabled = !installing;
        var form = Ui.Row(8, Ui.Detach(version), Ui.Detach(install));
        if (installing)
        {
            form.Children.Add(new ProgressRing { Width = 16, Height = 16 });
            form.Children.Add(Ui.Caption("Downloading…", wrap: false));
        }
        var installCard = Ui.Column(8, form);
        if (result is not null)
        {
            installCard.Children.Add(Ui.Caption(result));
        }
        page.Children.Add(Ui.Section("Install a version",
            Ui.Card(installCard),
            GeneralPane.Footnote("Official builds from nodejs.org, verified against their checksums.")));
        return page;
    }

    private async Task Install()
    {
        var requested = version.Text.Trim();
        if (requested.Length == 0 || installing)
        {
            return;
        }
        installing = true;
        result = null;
        Rerender();
        try
        {
            var full = await Store.Client.InstallNodeAsync(requested);
            result = $"Installed Node {full}.";
            version.Text = "";
        }
        catch (FlockException e)
        {
            Store.ShowError(e.Message);
        }
        installing = false;
        await Store.RefreshAsync();
        Rerender();
    }
}
