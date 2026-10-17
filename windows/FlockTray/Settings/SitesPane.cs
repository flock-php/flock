using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// The sites in a list, and the selected one's details beside it, as in Services: its address and
/// domains, its PHP and Node.js versions and what serves it. Each version picker offers the
/// project's pinned version (or the default) first, then the installed versions, which isolate the
/// site on this machine; the "…" menu pins a version into the project or removes it again. Each
/// site runs on php-cgi or on FrankenPHP (classic, or Laravel Octane's worker mode).
/// </summary>
internal sealed class SitesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    private string? selection;
    // The list and the details scroll separately, and keep their place across re-renders.
    private readonly KeptScroller listScroller = new(window.DispatcherQueue);
    private readonly KeptScroller detailScroller = new(window.DispatcherQueue);

    public override string Title => "Sites";
    public override string Subtitle => "Sites you added one by one, plus every folder inside your project folders.";
    public override bool ScrollsItself => true;

    private void Select(string name)
    {
        if (name != selection)
        {
            detailScroller.ToTop();
        }
        selection = name;
        Rerender();
    }

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
        if (request.Site is { } linked)
        {
            selection = linked;
        }
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

        var add = Ui.Button("Add Site…", async () =>
        {
            if (await Window.PickFolder() is { } path)
            {
                await Perform(c => c.LinkSiteAsync(path));
            }
        }, accent: true, glyph: Ui.GlyphAdd);
        if (sites.Count == 0)
        {
            return Ui.EmptyState(Ui.GlyphGlobe, "No sites", "Add a single site here, or add a project folder in General.", add);
        }
        var selected = sites.FirstOrDefault(s => s.Name == selection) ?? sites[0];

        var list = new StackPanel { Spacing = 4, Margin = new Thickness(0, 0, 14, 8) };
        foreach (var site in sites)
        {
            list.Children.Add(ListRow(site, site.Name == selected.Name));
        }
        add.Margin = new Thickness(0, 0, 0, 12);
        var left = new Grid();
        left.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        left.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        left.Children.Add(add);
        Grid.SetRow(listScroller.Place(left, list), 1);

        // A fixed list and the details in the rest, as in Services.
        var grid = new Grid { ColumnSpacing = 16 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(280) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star), MinWidth = 340 });
        grid.Children.Add(left);
        var detail = Detail(selected);
        detail.Margin = new Thickness(0, 0, 14, 8);
        Grid.SetColumn(detailScroller.Place(grid, detail), 1);
        return grid;
    }

    /// <summary>A site in the list: its name and address, a lock when it is secured, a warning when something is wrong.</summary>
    private Button ListRow(Status.Site site, bool isSelected)
    {
        var text = Ui.Column(1, Ui.Text(site.Name, "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(site.Domain, wrap: false));
        var marks = Ui.Row(6);
        if (Problem(site) is { } problem)
        {
            var warning = Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution);
            ToolTipService.SetToolTip(warning, problem);
            marks.Children.Add(warning);
        }
        if (site.Secured)
        {
            var secured = Ui.Icon(Ui.GlyphLock, 12, Ui.SecondaryText);
            ToolTipService.SetToolTip(secured, "Served over HTTPS");
            marks.Children.Add(secured);
        }
        var card = new Button
        {
            Content = Ui.Spread(text, marks),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Padding = new Thickness(14, 8, 12, 8),
            Background = Ui.Brush(isSelected ? "SubtleFillColorSecondaryBrush" : "CardBackgroundFillColorDefaultBrush"),
            BorderBrush = isSelected ? Ui.Accent : Ui.Brush("CardStrokeColorDefaultBrush"),
        };
        ToolTipService.SetToolTip(card, site.Path);
        card.Click += (_, _) => Select(site.Name);
        return card;
    }

    /// <summary>What a site's list row warns about: the site, one of its domains, or its FrankenPHP.</summary>
    private static string? Problem(Status.Site site) =>
        site.Problem
        ?? site.Domains.Select(d => d.Problem is { } p ? $"{d.Host}: {p}" : null).FirstOrDefault(p => p is not null)
        ?? (site.IsFrankenPhp && site.FrankenPhp is { IsRunning: false, IsInstalling: false, Problem.Length: > 0 } f ? f.Problem : null);

    /// <summary>The selected site: its address, then Site, Versions and Server sections of labelled settings.</summary>
    private Border Detail(Status.Site site)
    {
        var config = Store.Config;
        var panel = new StackPanel { Spacing = 20 };

        // Header: name, address and folder, and the site's actions.
        var name = Ui.Row(8, Ui.Text(site.Name, "SubtitleTextBlockStyle", wrap: false));
        if (site.Parked)
        {
            name.Children.Add(Ui.Badge("Project folder", Ui.Tone.Neutral));
        }
        var heading = Ui.Column(2, name);
        if (site.Url is { } siteUrl)
        {
            var link = Ui.Link(siteUrl, () => AppModel.Open(site));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var path = Ui.Caption(site.Path, Ui.TertiaryText, wrap: false);
        ToolTipService.SetToolTip(path, site.Path);
        heading.Children.Add(path);

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
        actions.VerticalAlignment = VerticalAlignment.Top;
        var header = new Grid { ColumnSpacing = 8 };
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(actions, 1);
        header.Children.Add(heading);
        header.Children.Add(actions);
        panel.Children.Add(header);
        if (site.Problem is { } problem)
        {
            panel.Children.Add(Warning(problem));
        }

        // Site: HTTPS and the domains besides its own.
        var https = new ToggleSwitch { IsOn = site.Secured, MinWidth = 0 };
        Ui.AutomationName(https, "HTTPS");
        https.Toggled += (_, _) => _ = Perform(c => c.UpdateSiteAsync(site.Name, new SiteChange(Secured: https.IsOn)));
        var domains = Ui.Column(2);
        foreach (var domain in site.Domains)
        {
            UIElement shown = domain.Problem is { } domainProblem ? Warning($"{domain.Host}: {domainProblem}")
                : domain.Url is { Length: > 0 } url ? DomainLink(domain, url)
                : Ui.Text(domain.Host, wrap: false);
            var target = domain;
            var remove = Ui.IconButton(Ui.GlyphRemove, $"Remove {domain.Host}", () => _ = RemoveDomain(site, target));
            remove.Padding = new Thickness(4);
            var row = new Grid { ColumnSpacing = 4 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            Grid.SetColumn(remove, 1);
            row.Children.Add(shown);
            row.Children.Add(remove);
            domains.Children.Add(row);
        }
        var addDomain = Ui.Link("Add Domain…", () => _ = AddDomain(site));
        addDomain.Padding = new Thickness(0);
        domains.Children.Add(addDomain);
        var siteSection = Section("Site", Field("HTTPS", https), Field("Domains", domains));
        if (Model.Status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 } dns)
        {
            siteSection.Children.Add(Ui.Caption($"Subdomains only work when you add them here: {dns.WildcardProblem}.", Ui.Caution));
        }
        panel.Children.Add(siteSection);

        // Versions: PHP and Node.js, with what pins or isolates them.
        var php = Picker("PHP", site.Php, site.PhpSource, site.PhpPinned, site.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => new SiteChange(Php: version), site.Name);
        var node = Picker("Node", Major(site.Node ?? ""), site.NodeSource, Major(site.NodePinned), site.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => new SiteChange(Node: version), site.Name);
        var versions = Section("Versions", Field("PHP", php), Field("Node.js", node));
        foreach (var hint in Hints(site))
        {
            versions.Children.Add(hint);
        }
        versions.Children.Add(Ui.Caption("A version picked here applies on this machine only. To set it for everyone working on the project, pin it (…): PHP goes into .flockrc, Node.js into .nvmrc.", Ui.TertiaryText));
        panel.Children.Add(versions);

        panel.Children.Add(Server(site));

        if (Boost(site) is { } boost)
        {
            panel.Children.Add(Section("Laravel Boost", boost));
        }

        var card = Ui.Card(panel, new Thickness(20, 16, 16, 20));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    /// <summary>A section of the details: a heading and its settings.</summary>
    private static StackPanel Section(string title, params UIElement[] rows)
    {
        var section = Ui.Column(8, Ui.Text(title, "BodyStrongTextBlockStyle"));
        foreach (var row in rows)
        {
            section.Children.Add(row);
        }
        return section;
    }

    /// <summary>A labelled setting: the label in a fixed column, so the controls line up.</summary>
    private static Grid Field(string label, UIElement control)
    {
        var row = new Grid { ColumnSpacing = 12 };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(96) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var text = Ui.Text(label, foreground: Ui.SecondaryText, wrap: false);
        text.VerticalAlignment = VerticalAlignment.Center;
        if (control is FrameworkElement element)
        {
            element.HorizontalAlignment = HorizontalAlignment.Left;
            if (element is StackPanel)
            {
                text.VerticalAlignment = VerticalAlignment.Top;
            }
        }
        Grid.SetColumn((FrameworkElement)control, 1);
        row.Children.Add(text);
        row.Children.Add(control);
        return row;
    }

    private static Grid Warning(string text)
    {
        var icon = Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution);
        icon.VerticalAlignment = VerticalAlignment.Top;
        icon.Margin = new Thickness(0, 3, 0, 0);
        var caption = Ui.Caption(text, Ui.Caution);
        caption.IsTextSelectionEnabled = true;
        var row = new Grid { ColumnSpacing = 6 };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        Grid.SetColumn(caption, 1);
        row.Children.Add(icon);
        row.Children.Add(caption);
        return row;
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

        var section = Section("Server", Field("Runs on", server));
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
            section.Children.Add(Field("Mode", mode));
            if (!site.OctaneAvailable)
            {
                ToolTipService.SetToolTip(mode, "Install Laravel Octane in the project (composer require laravel/octane) to use worker mode.");
            }

            if (site.FrankenPhp is { } state)
            {
                foreach (var line in FrankenPhpState(site, state))
                {
                    section.Children.Add(line);
                }
            }
            var restart = Ui.Button("Restart", () => _ = Restart(site.Name), tooltip: "Restart FrankenPHP, e.g. so Octane's workers load changed code");
            restart.IsEnabled = site.FrankenPhp is { IsRunning: true } && !restarting.Contains(site.Name);
            var log = site.FrankenPhp is { Log.Length: > 0 } f ? f.Log : $"frankenphp-{site.Name}";
            var buttons = Ui.Row(8, restart, Ui.Button("Show Log", () => App.Current.OpenLog(new LogStore.ServiceSource(log))));
            if (restarting.Contains(site.Name))
            {
                buttons.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
            section.Children.Add(buttons);
        }
        section.Children.Add(Ui.Caption(franken && !site.OctaneAvailable
            ? "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-CGI. For worker mode, install Laravel Octane in the project (composer require laravel/octane)."
            : "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-CGI.", Ui.TertiaryText));
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
                yield return Warning(state.Problem);
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
    }

    /// <summary>The project uses Laravel Boost, and Flock's guideline is missing from it or outdated.</summary>
    private StackPanel? Boost(Status.Site site)
    {
        if (site.Boost is not (Status.BoostState.Missing or Status.BoostState.Outdated))
        {
            return null;
        }
        var missing = site.Boost == Status.BoostState.Missing;
        var link = Ui.Link(missing ? "Add Flock to its guidelines…" : "Update it…", () => _ = AddBoostGuideline(site, missing));
        link.Padding = new Thickness(0);
        return Ui.Column(2, Ui.Caption(missing
            ? "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
            : "Flock's guideline in the project's Boost guidelines is outdated."), link);
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

    /// <summary>A note that wraps, with an action under it.</summary>
    private static StackPanel Hint(string text, string? action, Action? onClick)
    {
        var icon = Ui.Icon(Ui.GlyphInfo, 12, Ui.Caution);
        icon.VerticalAlignment = VerticalAlignment.Top;
        icon.Margin = new Thickness(0, 3, 0, 0);
        var note = new Grid { ColumnSpacing = 6 };
        note.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        note.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var caption = Ui.Caption(text, Ui.Caution);
        Grid.SetColumn(caption, 1);
        note.Children.Add(icon);
        note.Children.Add(caption);
        var hint = Ui.Column(0, note);
        if (action is not null && onClick is not null)
        {
            var link = Ui.Link(action, onClick);
            link.Margin = new Thickness(18, 0, 0, 0);
            hint.Children.Add(link);
        }
        return hint;
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

        var button = Ui.IconButton(Ui.GlyphMore, "Pin versions to the project", () => { });
        button.Flyout = menu;
        button.IsEnabled = menu.Items.Count > 0;
        return button;
    }

    private async Task RemoveDomain(Status.Site site, Status.SiteDomain domain)
    {
        if (await Window.Confirm($"Remove {domain.Host}?", $"{domain.Host} no longer opens {site.Name}.", "Remove", destructive: true))
        {
            await Perform(c => c.RemoveDomainAsync(site.Name, domain.Entry));
        }
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
