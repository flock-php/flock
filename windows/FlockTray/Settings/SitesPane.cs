using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Sites with their PHP and Node.js versions. Each picker offers the project's pinned version (or
/// the default) first, then the installed versions, which isolate the site on this machine; the
/// "…" menu pins a version into the project or removes it again.
/// </summary>
internal sealed class SitesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    public override string Title => "Sites";
    public override string Subtitle => "Sites you added one by one, plus every folder inside your project folders.";

    public override UIElement Build()
    {
        var sites = Model.Status?.Sites ?? [];

        var link = Ui.Button("Add Site…", async () =>
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
            page.Children.Add(Ui.EmptyState(Ui.GlyphGlobe, "No sites", "Add a single site here, or add a project folder in General."));
            return page;
        }
        if (Model.Status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 } dns)
        {
            var notice = Ui.Row(4, Ui.Icon(Ui.GlyphInfo, 12, Ui.Caution),
                Ui.Caption($"Subdomains only work when you add them to a site (… → Add Domain…): {dns.WildcardProblem}.", Ui.Caution));
            notice.Margin = new Thickness(2, 0, 0, 8);
            page.Children.Add(notice);
        }
        foreach (var site in sites)
        {
            page.Children.Add(Row(site));
        }
        page.Children.Add(GeneralPane.Footnote("A version picked here applies on this machine only. To set it for everyone working on a project, pin it (…): PHP goes into .flockrc, Node.js into .nvmrc. Projects that pin a version use it automatically."));
        return page;
    }

    private Border Row(Status.Site site)
    {
        var config = Store.Config;
        var name = Ui.Row(6, Ui.Text(site.Name, "BodyStrongTextBlockStyle", wrap: false));
        if (site.Parked)
        {
            name.Children.Add(Ui.Badge("Project folder", Ui.Tone.Neutral));
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
        foreach (var domain in site.Domains)
        {
            info.Children.Add(domain.Problem is { } domainProblem
                ? Ui.Row(4, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption($"{domain.Host}: {domainProblem}", Ui.Caution, wrap: false))
                : DomainLink(domain));
        }
        foreach (var hint in Hints(site))
        {
            info.Children.Add(hint);
        }

        var php = Picker("PHP", site.Php, site.PhpSource, site.PhpPinned, site.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => new SiteChange(Php: version), site.Name);
        var node = Picker("Node", Major(site.Node ?? ""), site.NodeSource, Major(site.NodePinned), site.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => new SiteChange(Node: version), site.Name);

        var https = new ToggleSwitch { IsOn = site.Secured, OnContent = "HTTPS", OffContent = "HTTP", MinWidth = 0 };
        https.Toggled += (_, _) => _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Secured: https.IsOn)));

        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {site.Url ?? site.Domain}", () => AppModel.Open(site));
        open.IsEnabled = site.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Native.Reveal(site.Path)), More(site));
        if (!site.Parked)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphDelete, "Remove site", () => _ = Perform(c => c.UnlinkSiteAsync(site.Name))));
        }

        return Ui.Card(Ui.Spread(info, Ui.Column(6, php, node), https, actions));
    }

    /// <summary>
    /// A version picker: first the project's pinned version ("Use .nvmrc (Node 20)") or the default,
    /// which follow the project; then the installed versions, which isolate the site.
    /// </summary>
    private ComboBox Picker(string label, string current, string source, string pinned, string file, string defaultVersion,
        List<string> installed, Func<string, SiteChange> change, string siteName)
    {
        var follow = pinned.Length > 0 ? $"Use {System.IO.Path.GetFileName(file)} ({label} {pinned})" : $"Default ({label} {defaultVersion})";
        var versions = installed.ToList();
        if (source == Status.VersionSource.Isolated && current.Length > 0 && !versions.Contains(current))
        {
            versions.Add(current); // isolated to a version that isn't installed (any more)
        }

        var picker = new ComboBox { MinWidth = 190 };
        picker.Items.Add(follow);
        foreach (var version in versions)
        {
            picker.Items.Add($"{label} {version}");
        }
        picker.SelectedIndex = source == Status.VersionSource.Isolated ? versions.IndexOf(current) + 1 : 0;
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedIndex < 0)
            {
                return;
            }
            var isolate = picker.SelectedIndex == 0 ? "" : versions[picker.SelectedIndex - 1];
            _ = Perform(c => c.UpdateSiteAsync(siteName, change(isolate)));
        };
        ToolTipService.SetToolTip(picker, source switch
        {
            Status.VersionSource.Isolated => $"{label} {current} is set for this site on this machine (isolated).",
            Status.VersionSource.Project => $"The project pins {label} {pinned} in {System.IO.Path.GetFileName(file)}.",
            _ => $"The site follows Flock's default {label} version.",
        });
        return picker;
    }

    /// <summary>Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.</summary>
    private IEnumerable<UIElement> Hints(Status.Site site)
    {
        if (site.PhpSource == Status.VersionSource.Isolated && site.PhpPinned.Length > 0 && site.PhpPinned != site.Php)
        {
            yield return Hint($"PHP {site.Php} is isolated here; the project pins {site.PhpPinned} in {System.IO.Path.GetFileName(site.PhpFile)}.",
                $"Use {System.IO.Path.GetFileName(site.PhpFile)}", () => _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Php: ""))));
        }
        if (site.NodeSource == Status.VersionSource.Isolated && site.NodePinned.Length > 0 && Major(site.NodePinned) != Major(site.Node ?? ""))
        {
            yield return Hint($"Node {site.Node} is isolated here; the project pins {site.NodePinned} in {System.IO.Path.GetFileName(site.NodeFile)}.",
                $"Use {System.IO.Path.GetFileName(site.NodeFile)}", () => _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Node: ""))));
        }
        if (site.NodeShadowed.Length > 0)
        {
            yield return Hint($".flockrc's Node {site.NodePinned} wins over {site.NodeShadowed}.", null, null);
        }
    }

    private static HyperlinkButton DomainLink(Status.SiteDomain domain)
    {
        var link = Ui.Link(domain.Alias ? $"{domain.Host} (alias)" : domain.Host, () =>
        {
            if (domain.Url is { Length: > 0 } url)
            {
                Native.Open(url);
            }
        });
        link.Padding = new Thickness(0);
        ToolTipService.SetToolTip(link, $"Open {domain.Url}");
        return link;
    }

    private async Task AddDomain(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var depth = dns?.MaxDepth ?? 4;
        var details = dns is { Wildcard: true }
            ? $"Every subdomain up to {depth} levels below {site.Domain} already works on this PC. Add one here so it also works where wildcard DNS doesn't (it's written to the hosts file), or add an alias like {site.Name}-2.{dns.Tld}."
            : $"Wildcard subdomains don't work on this PC{(dns?.WildcardProblem is { Length: > 0 } p ? $" ({p})" : "")}, so add each one you use: it's written to the hosts file. A full name like {site.Name}-2.{dns?.Tld ?? "test"} adds an alias.";
        if (await Window.Ask($"Add a domain to {site.Name}", details, $"tenant1, a.b or {site.Name}-2.{dns?.Tld ?? "test"}", "Add") is { } domain)
        {
            await Perform(c => c.AddDomainAsync(site.Name, domain));
        }
    }

    private static StackPanel Hint(string text, string? action, Action? onClick)
    {
        var row = Ui.Row(4, Ui.Icon(Ui.GlyphInfo, 12, Ui.Caution), Ui.Caption(text, Ui.Caution, wrap: false));
        if (action is not null && onClick is not null)
        {
            row.Children.Add(Ui.Link(action, onClick));
        }
        return row;
    }

    private Button More(Status.Site site)
    {
        var menu = new MenuFlyout();
        var nodeMajor = Major(site.Node ?? "");
        var nodeFile = site.NodePinnedHere ? System.IO.Path.GetFileName(site.NodeFile) : ".nvmrc";

        if (!(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere))
        {
            menu.Items.Add(Item($"Pin PHP {site.Php} to Project…", () => _ = Pin(site, "php", site.Php,
                $"Writes php={site.Php} to .flockrc in {site.Path}. Commit it, and everyone using Flock on this project gets PHP {site.Php}.")));
        }
        if (nodeMajor.Length > 0 && !(site.NodeSource == Status.VersionSource.Project && site.NodePinnedHere))
        {
            menu.Items.Add(Item($"Pin Node {nodeMajor} to Project…", () => _ = Pin(site, "node", nodeMajor,
                $"Writes {nodeMajor} to {nodeFile} in {site.Path}. Commit it, and Flock, nvm and other Node tools use Node {nodeMajor} for this project.")));
        }
        if (site.PhpPinnedHere || site.NodePinnedHere)
        {
            if (menu.Items.Count > 0)
            {
                menu.Items.Add(new MenuFlyoutSeparator());
            }
            if (site.PhpPinnedHere)
            {
                menu.Items.Add(Item($"Unpin PHP ({System.IO.Path.GetFileName(site.PhpFile)})…", () => _ = Unpin(site, "php",
                    $"Removes the PHP version from {site.PhpFile}. The site then uses Flock's default PHP.")));
            }
            if (site.NodePinnedHere)
            {
                menu.Items.Add(Item($"Unpin Node ({System.IO.Path.GetFileName(site.NodeFile)})…", () => _ = Unpin(site, "node",
                    $"Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in {site.Path}. The site then uses Flock's default Node.js.")));
            }
        }

        if (menu.Items.Count > 0)
        {
            menu.Items.Add(new MenuFlyoutSeparator());
        }
        menu.Items.Add(Item("Add Domain…", () => _ = AddDomain(site)));
        foreach (var domain in site.Domains)
        {
            menu.Items.Add(Item($"Remove {domain.Host}", () => _ = Perform(c => c.RemoveDomainAsync(site.Name, domain.Entry))));
        }

        var button = Ui.IconButton(Ui.GlyphMore, "Domains and project versions", () => { });
        button.Flyout = menu;
        return button;
    }

    private async Task Pin(Status.Site site, string tool, string version, string details)
    {
        var name = tool == "php" ? "PHP" : "Node";
        if (await Window.Confirm($"Pin {name} {version} to {site.Name}?", details + " This changes a file in your project.", "Pin"))
        {
            await Perform(c => c.PinAsync(site.Name, tool, version));
        }
    }

    private async Task Unpin(Status.Site site, string tool, string details)
    {
        var name = tool == "php" ? "PHP" : "Node";
        if (await Window.Confirm($"Unpin {name} from {site.Name}?", details + " This changes files in your project.", "Unpin"))
        {
            await Perform(c => c.UnpinAsync(site.Name, tool));
        }
    }

    private static MenuFlyoutItem Item(string text, Action click)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => click();
        return item;
    }

    /// <summary>"22.11.0" → "22"; pins and isolations are majors.</summary>
    private static string Major(string version) => version.Split('.')[0];
}
