using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Sites with their PHP and Node.js versions. Each picker offers the project's pinned version (or
/// the default) first, then the installed versions, which isolate the site on this machine; the
/// "…" menu pins a version into the project or removes it again. Each site runs on php-cgi or on
/// FrankenPHP (classic, or Laravel Octane's worker mode).
/// </summary>
internal sealed class SitesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    public override string Title => "Sites";
    public override string Subtitle => "Sites you added one by one, plus every folder inside your project folders.";

    /// <summary>
    /// Asks about a pending flock://secure link (App.OpenLink) once the sites are loaded: nothing
    /// changes until the user confirms here.
    /// </summary>
    private void ConfirmPendingLink()
    {
        if (Model.PendingSecure is not { } link || link.Resolve(Model.Status?.Sites) is not { } request)
        {
            return;
        }
        Model.PendingSecure = null;
        // Not while building the pane: a dialog needs the window's content in place.
        Window.DispatcherQueue.TryEnqueue(async () =>
        {
            if (request.Site is not { } site)
            {
                await Ui.Inform(Window.Content.XamlRoot, request.Title, request.Message);
                return;
            }
            if (await Window.Confirm(request.Title, request.Message, "Secure"))
            {
                await Perform(c => c.UpdateSiteAsync(site, new SiteChange(Secured: true)));
            }
        });
    }

    public override UIElement Build()
    {
        var sites = Model.Status?.Sites ?? [];
        ConfirmPendingLink();

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
            // A domain with neither a URL nor a problem (the site itself isn't served) has nothing to show.
            if (domain.Problem is { } domainProblem)
            {
                info.Children.Add(Ui.Row(4, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption($"{domain.Host}: {domainProblem}", Ui.Caution, wrap: false)));
            }
            else if (domain.Url is { Length: > 0 } url)
            {
                info.Children.Add(DomainLink(domain, url));
            }
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
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Native.Reveal(site.Path)),
            Ui.IconButton(Ui.GlyphPuzzle, "PHP extensions", () => _ = ShowExtensions(site)), More(site));
        if (!site.Parked)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphDelete, "Remove site", async () =>
            {
                if (await Window.Confirm($"Remove {site.Name}?", $"{site.Domain} stops being served. The project's files stay where they are.", "Remove", destructive: true))
                {
                    await Perform(c => c.UnlinkSiteAsync(site.Name));
                }
            }));
        }

        return Ui.Card(Ui.Column(12, Ui.Spread(info, Ui.Column(6, php, node), https, actions), Server(site)));
    }

    // Sites whose FrankenPHP restart is in flight.
    private readonly HashSet<string> restarting = [];

    /// <summary>
    /// What runs the site's PHP: php-cgi, or FrankenPHP (classic or Laravel Octane's worker mode),
    /// with FrankenPHP's state, Restart and its log.
    /// </summary>
    private StackPanel Server(Status.Site site)
    {
        var franken = site.IsFrankenPhp;
        var server = new ComboBox { MinWidth = 140 };
        server.Items.Add(new ComboBoxItem { Content = "PHP-CGI" });
        // FrankenPHP needs PHP 8.2; a site already on it keeps the option (the daemon explains the fallback).
        server.Items.Add(new ComboBoxItem { Content = "FrankenPHP", IsEnabled = franken || SupportsFrankenPhp(site.Php) });
        server.SelectedIndex = franken ? 1 : 0;
        server.SelectionChanged += (_, _) =>
        {
            if (server.SelectedIndex < 0 || (server.SelectedIndex == 1) == franken)
            {
                return;
            }
            var change = server.SelectedIndex == 1 ? Status.SiteServer.FrankenPhp : Status.SiteServer.FastCgi;
            _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Server: change)));
        };
        Ui.AutomationName(server, "Server");
        var controls = Ui.Row(8, Ui.Caption("Server", wrap: false), server);

        var section = Ui.Column(4);
        if (franken)
        {
            var octane = site.Worker == Status.SiteServer.Octane;
            var mode = new ComboBox { MinWidth = 150 };
            mode.Items.Add(new ComboBoxItem { Content = "Classic" });
            mode.Items.Add(new ComboBoxItem { Content = "Laravel Octane", IsEnabled = octane || site.OctaneAvailable });
            mode.SelectedIndex = octane ? 1 : 0;
            mode.SelectionChanged += (_, _) =>
            {
                if (mode.SelectedIndex < 0 || (mode.SelectedIndex == 1) == octane)
                {
                    return;
                }
                var worker = mode.SelectedIndex == 1 ? Status.SiteServer.Octane : "";
                _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Worker: worker)));
            };
            Ui.AutomationName(mode, "Mode");
            controls.Children.Add(Ui.Caption("Mode", wrap: false));
            controls.Children.Add(mode);

            var restart = Ui.Button("Restart", () => _ = Restart(site.Name), tooltip: "Restart FrankenPHP, e.g. so Octane's workers load changed code");
            restart.IsEnabled = site.FrankenPhp is { IsRunning: true } && !restarting.Contains(site.Name);
            var log = site.FrankenPhp is { Log.Length: > 0 } f ? f.Log : $"frankenphp-{site.Name}";
            var buttons = Ui.Row(8, restart, Ui.Button("Show Log", () => App.Current.OpenLog(new LogStore.ServiceSource(log))));
            if (restarting.Contains(site.Name))
            {
                buttons.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
            section.Children.Add(Ui.Spread(controls, buttons));
        }
        else
        {
            section.Children.Add(controls);
        }

        section.Children.Add(Ui.Caption("FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-CGI.", Ui.TertiaryText));
        if (franken && !site.OctaneAvailable)
        {
            section.Children.Add(Ui.Caption("Install Laravel Octane in the project (composer require laravel/octane) to use worker mode.", Ui.TertiaryText));
        }
        if (franken && site.FrankenPhp is { } state)
        {
            foreach (var line in FrankenPhpState(site, state))
            {
                section.Children.Add(line);
            }
        }
        return section;
    }

    /// <summary>The status lines of a site's FrankenPHP: running, downloading, or why php-cgi serves it meanwhile.</summary>
    private static IEnumerable<UIElement> FrankenPhpState(Status.Site site, Status.SiteFrankenPhp state)
    {
        if (state.IsRunning)
        {
            var text = $"Running FrankenPHP {state.Version} with PHP {state.PhpVersion}" + (site.Worker == Status.SiteServer.Octane ? " · worker mode" : "");
            yield return Ui.Row(6, Ui.StatusDot(true), Ui.Caption(text, wrap: false));
        }
        else if (state.IsInstalling)
        {
            var percent = state.HasProgress ? $" {Math.Round(state.Progress * 100)}%" : "";
            yield return Ui.Caption($"Downloading FrankenPHP for PHP {site.Php}…{percent}", wrap: false);
            yield return new ProgressBar
            {
                Minimum = 0,
                Maximum = 1,
                Value = state.HasProgress ? state.Progress : 0,
                IsIndeterminate = !state.HasProgress,
                HorizontalAlignment = HorizontalAlignment.Left,
                Width = 320,
            };
            yield return Ui.Caption("PHP-CGI serves the site until it's ready.");
        }
        else
        {
            if (state.Problem.Length > 0)
            {
                yield return Ui.Row(4, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption(state.Problem, Ui.Caution));
            }
            yield return Ui.Caption("PHP-CGI serves the site meanwhile.");
        }
    }

    private async Task Restart(string site)
    {
        restarting.Add(site);
        Rerender();
        try
        {
            await Perform(c => c.RestartSiteAsync(site));
        }
        finally
        {
            restarting.Remove(site);
            Rerender();
        }
    }

    /// <summary>FrankenPHP runs PHP 8.2 and newer; an unknown version doesn't block it (the daemon checks too).</summary>
    private static bool SupportsFrankenPhp(string php)
    {
        var parts = php.Split('.');
        if (parts.Length < 2 || !int.TryParse(parts[0], out var major) || !int.TryParse(parts[1], out var minor))
        {
            return true;
        }
        return major > 8 || (major == 8 && minor >= 2);
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
        if (site.Boost is Status.BoostState.Missing or Status.BoostState.Outdated)
        {
            var missing = site.Boost == Status.BoostState.Missing;
            yield return Ui.Row(4,
                Ui.Caption(missing ? "Uses Laravel Boost." : "Flock's Boost guideline is outdated.", Ui.SecondaryText, wrap: false),
                Ui.Link(missing ? "Add Flock to its guidelines…" : "Update it…", () => _ = AddBoostGuideline(site, missing)));
        }
    }

    private async Task AddBoostGuideline(Status.Site site, bool missing)
    {
        var title = missing ? $"Add Flock to {site.Name}'s Boost guidelines?" : $"Update Flock's guideline in {site.Name}?";
        var details = $"Writes .ai\\guidelines\\flock.md in {site.Path} and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. " +
            "Commit it with the rest of .ai\\guidelines. This changes files in your project.";
        if (await Window.Confirm(title, details, missing ? "Add" : "Update"))
        {
            await Perform(c => c.AddBoostGuidelineAsync(site.Name));
        }
    }

    private static HyperlinkButton DomainLink(Status.SiteDomain domain, string url)
    {
        var link = Ui.Link(domain.Alias ? $"{domain.Host} (alias)" : domain.Host, () => Native.Open(url));
        link.Padding = new Thickness(0);
        ToolTipService.SetToolTip(link, $"Open {url}");
        return link;
    }

    /// <summary>
    /// A site's PHP extensions: what composer.json and the locked packages require, what a Laravel
    /// app's configuration shows it uses without requiring it, and whether Flock's PHP has each.
    /// Adding one to composer.json is confirmed inline (only one dialog can be open at a time).
    /// </summary>
    private async Task ShowExtensions(Status.Site site)
    {
        var list = new StackPanel { Spacing = 6 };
        var scroller = new ScrollViewer { Content = list, Width = 560, Height = 320 };
        var dialog = new ContentDialog
        {
            Title = $"PHP extensions of {site.Name}",
            Content = Ui.Column(10, scroller, Ui.Caption("Requiring an extension in composer.json makes composer install refuse a server without it, instead of the app failing there.")),
            CloseButtonText = "Done",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 640.0;

        ExtensionReport? report = null;
        string? error = null;
        string? confirming = null, adding = null;

        void Render()
        {
            list.Children.Clear();
            if (error is not null)
            {
                list.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Critical), Ui.Caption(error, Ui.Critical)));
            }
            if (report is null)
            {
                if (error is null)
                {
                    list.Children.Add(Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption("Loading…")));
                }
                return;
            }
            list.Children.Add(Ui.Caption(report.Laravel ? $"PHP {report.Php} · Laravel app" : $"PHP {report.Php}"));
            if (report.Extensions.Count == 0)
            {
                list.Children.Add(Ui.Text("The project requires no PHP extensions.", foreground: Ui.SecondaryText));
            }
            foreach (var ext in report.Extensions)
            {
                var (glyph, color) = !ext.Available ? (Ui.GlyphWarning, Ui.Critical) : ext.Declared ? (Ui.GlyphPuzzle, Ui.Success) : (Ui.GlyphInfo, Ui.Caution);
                var detail = ext.Declared ? $"Required by {string.Join(", ", ext.RequiredBy)}" : $"Not required, but used: {ext.Hint}";
                if (!ext.Available)
                {
                    detail += $" — Flock's PHP {report.Php} doesn't include it";
                }
                var caption = Ui.Caption(detail);
                caption.MaxWidth = 360;
                var info = Ui.Row(10, Ui.Icon(glyph, 14, color), Ui.Column(2, Ui.Text(ext.Name, wrap: false), caption));

                var actions = Ui.Row(8);
                if (adding == ext.Name)
                {
                    actions.Children.Add(new ProgressRing { Width = 16, Height = 16 });
                }
                else if (confirming == ext.Name)
                {
                    var name = ext.Name;
                    actions.Children.Add(Ui.Link("Cancel", () => { confirming = null; Render(); }));
                    actions.Children.Add(Ui.Button("Require", () => _ = Add(name), accent: true,
                        tooltip: $"Runs composer require ext-{name}:* in {site.Path}, which changes composer.json and composer.lock. Commit them with the project."));
                }
                else if (!ext.Declared)
                {
                    var name = ext.Name;
                    var add = Ui.Button("Add to composer.json…", () => { confirming = name; Render(); });
                    add.IsEnabled = adding is null;
                    actions.Children.Add(add);
                }
                list.Children.Add(Ui.Spread(info, actions));
            }
        }

        async Task Add(string name)
        {
            confirming = null;
            adding = name;
            Render();
            (var fresh, error) = await Store.RequireExtensionAsync(site.Name, name);
            report = fresh ?? report;
            adding = null;
            Render();
        }

        Render();
        var load = Store.SiteExtensionsAsync(site.Name);
        _ = dialog.ShowAsync();
        (report, error) = await load;
        Render();
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

        // Pin and unpin items appear only when there is something to pin or unpin (the menu always has the domain items).
        if (site.Php.Length > 0 && !(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere))
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
            menu.Items.Add(Item($"Remove {domain.Host}…", async () =>
            {
                if (await Window.Confirm($"Remove {domain.Host}?", $"{domain.Host} no longer opens {site.Name}.", "Remove", destructive: true))
                {
                    await Perform(c => c.RemoveDomainAsync(site.Name, domain.Entry));
                }
            }));
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
