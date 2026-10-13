using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed class SitesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    public override string Title => "Sites";
    public override string Subtitle => "Linked folders and everything inside parked folders.";

    public override UIElement Build()
    {
        var sites = Model.Status?.Sites ?? [];
        var versions = Store.Config?.Php.Select(p => p.Version).ToList() ?? [];
        var defaultPhp = Store.Config?.DefaultPhp ?? "";

        var link = Ui.Button("Link Folder…", async () =>
        {
            if (await Window.PickFolder() is { } path)
            {
                await Perform(c => c.LinkSiteAsync(path));
            }
        }, accent: true, glyph: Ui.GlyphAdd);
        link.HorizontalAlignment = HorizontalAlignment.Right;
        link.Margin = new Thickness(0, 0, 0, 12);

        var page = Ui.Column(4, link);
        if (sites.Count == 0)
        {
            page.Children.Add(Ui.EmptyState(Ui.GlyphGlobe, "No sites", "Link a folder here, or park a folder in General."));
            return page;
        }
        foreach (var site in sites)
        {
            page.Children.Add(Row(site, versions, defaultPhp));
        }
        return page;
    }

    private Border Row(Status.Site site, List<string> versions, string defaultPhp)
    {
        var name = Ui.Row(6, Ui.Text(site.Name, "BodyStrongTextBlockStyle", wrap: false));
        if (site.Parked)
        {
            name.Children.Add(Ui.Badge("Parked", Ui.Tone.Neutral));
        }
        var info = Ui.Column(2, name);
        if (site.Problem is { } problem)
        {
            info.Children.Add(Ui.Row(4, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption(problem, Ui.Caution)));
        }
        else
        {
            info.Children.Add(Ui.Caption(site.Url ?? site.Domain, wrap: false));
            info.Children.Add(Ui.Caption(site.Path, Ui.TertiaryText, wrap: false));
        }

        // Pinning the default version is the same as following it.
        var choices = versions.Contains(site.Php) ? versions : [.. versions, site.Php];
        var php = new ComboBox { MinWidth = 130 };
        foreach (var version in choices)
        {
            php.Items.Add(version == defaultPhp ? $"PHP {version} (default)" : $"PHP {version}");
        }
        php.SelectedIndex = choices.IndexOf(site.Php);
        php.SelectionChanged += (_, _) =>
        {
            if (php.SelectedIndex < 0)
            {
                return;
            }
            var version = choices[php.SelectedIndex];
            var pin = version == defaultPhp ? "" : version;
            _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Php: pin)));
        };
        ToolTipService.SetToolTip(php, "PHP version");

        var https = new ToggleSwitch { IsOn = site.Secured, OnContent = "HTTPS", OffContent = "HTTP", MinWidth = 0 };
        https.Toggled += (_, _) => _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Secured: https.IsOn)));

        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {site.Url ?? site.Domain}", () => AppModel.Open(site));
        open.IsEnabled = site.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Native.Reveal(site.Path)));
        if (!site.Parked)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphDelete, "Unlink", () => _ = Perform(c => c.UnlinkSiteAsync(site.Name))));
        }

        return Ui.Card(Ui.Spread(info, php, https, actions));
    }
}
