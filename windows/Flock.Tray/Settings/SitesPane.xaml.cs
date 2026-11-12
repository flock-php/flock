using Flock.Kit;
using Flock.Tray.Controls;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     The sites in a list, and the selected one's details beside it, as in Services: its address,
///     domains and document root, its PHP and Node.js versions and what serves it. Each version picker
///     offers the project's pinned version (or the default) first, then the installed versions, which
///     isolate the site on this machine; the document root picker works the same way. The "…" menu
///     pins a version or the document root into the project or removes it again. Each site runs on
///     php-cgi or on FrankenPHP (classic, or Laravel Octane's worker mode). Proxies (`flock proxy`)
///     follow the sites in a group of their own.
/// </summary>
internal sealed partial class SitesPane : UserControl, ISettingsPane
{
    private readonly RuntimeService Model;
    private readonly SettingsWindow Window;
    private readonly Dictionary<string, Task> actions = [];
    private readonly DeepLinkCoordinator deepLinks;
    private readonly DomainApplyService domainApply;
    private readonly bool loaded;

    // Sites whose FrankenPHP restart is in flight.
    private readonly HashSet<string> restarting = [];
    private readonly SitesViewModel viewModel;
    private bool active;
    private CancellationTokenSource? activeLifetime;
    private Task pendingSecure = Task.CompletedTask;
    // The per-site suffix switch in flight, and the last one that failed. The details are rebuilt whenever
    // the site changes, so the switch's busy button, CLI output and declined note live here and paint onto
    // whichever Domain row is current (macOS's applying / applyLines / applyDeclined).
    private string? applyingDomain;
    private (string Site, string Applied, string Output, bool Declined)? domainSwitchFailed;

    public SitesPane(SettingsWindow window, RuntimeService model, DeepLinkCoordinator deepLinks)
    {
        Window = window;
        Model = model;
        viewModel = new SitesViewModel(model.Sites);
        this.deepLinks = deepLinks;
        domainApply = new DomainApplyService(new CliProcessRunner());
        InitializeComponent();
        DataContext = viewModel;
        loaded = true;
        Model.Changed += StateChanged;
        viewModel.SelectionChanged += UpdateRetained;
        UpdateRetained();
    }

    public SitesPane(SettingsWindow window, RuntimeService model) : this(window, model, new DeepLinkCoordinator(model))
    {
    }

    public string Title => "Sites";
    public string Subtitle => "Sites you added one by one, plus every folder inside your project folders.";
    public UIElement Root => this;
    public bool ScrollsItself => true;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        active = true;
        activeLifetime?.Dispose();
        activeLifetime = CancellationTokenSource.CreateLinkedTokenSource(hidden);
        ApplyStatus();
        await HandlePendingSecureAsync(hidden);
    }

    public Task ReleaseAsync()
    {
        active = false;
        activeLifetime?.Cancel();
        Model.Changed -= StateChanged;
        viewModel.SelectionChanged -= UpdateRetained;
        return DrainAsync();
    }

    public void Hidden()
    {
        active = false;
        activeLifetime?.Cancel();
    }

    private void StateChanged()
    {
        ApplyStatus();
        if (active && Model.PendingSecure is not null && pendingSecure.IsCompleted)
        {
            pendingSecure = ObservePendingSecureAsync(activeLifetime?.Token ?? CancellationToken.None);
        }
    }

    private async Task ObservePendingSecureAsync(CancellationToken cancellationToken)
    {
        try
        {
            await HandlePendingSecureAsync(cancellationToken);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private void Start(string resource, Func<Task> action, bool refreshAfter = true)
    {
        if (actions.TryGetValue(resource, out var running) && !running.IsCompleted)
        {
            return;
        }
        actions[resource] = ObserveActionAsync(resource, action, refreshAfter);
    }

    private async Task ObserveActionAsync(string resource, Func<Task> action, bool refreshAfter)
    {
        try
        {
            await action();
        }
        catch (OperationCanceledException) when (activeLifetime?.IsCancellationRequested == true)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
        finally
        {
            actions.Remove(resource);
            if (refreshAfter)
            {
                UpdateRetained();
            }
        }
    }

    private async Task UpdateSiteAsync(string site, SiteChange change, Func<Action?>? setDraft)
    {
        var rollback = setDraft?.Invoke();
        try
        {
            var outcome = await Model.Sites.UpdateAsync(site, change);
            if (outcome == OperationOutcome.Succeeded)
            {
                return;
            }
            rollback?.Invoke();
            if (outcome == OperationOutcome.Failed)
            {
                viewModel.Apply(Model.Status);
            }
            UpdateRetained();
        }
        catch
        {
            rollback?.Invoke();
            UpdateRetained();
            throw;
        }
    }

    private async Task DrainAsync()
    {
        var tasks = actions.Values.Append(pendingSecure).ToArray();
        if (tasks.Length > 0)
        {
            await Task.WhenAll(tasks);
        }
        activeLifetime?.Dispose();
        activeLifetime = null;
    }

    private async Task HandlePendingSecureAsync(CancellationToken hidden)
    {
        var resolution = await deepLinks.ConsumeSecureAsync(hidden);
        if (resolution is null)
        {
            return;
        }
        if (resolution.Site is { } site)
        {
            viewModel.Select(site);
        }
        if (resolution.Site is null)
        {
            await Ui.Inform(Window.XamlRoot, resolution.Title, resolution.Message);
            return;
        }
        if (await Window.Confirm(resolution.Title, resolution.Message, "Secure"))
        {
            await Model.Sites.UpdateAsync(resolution.Site, new SiteChange(Secured: true), hidden);
        }
    }

    private async Task AddSiteAsync()
    {
        if (await Window.PickFolder() is { } path)
        {
            await Model.Sites.LinkAsync(path);
        }
    }

    private async Task RemoveSiteAsync(Status.Site site)
    {
        if (await Window.Confirm($"Remove {site.Name}?", $"{site.Domain} stops being served. The project's files stay where they are.", "Remove", true))
        {
            await Model.Sites.UnlinkAsync(site.Name);
        }
    }

    internal void Select(string name)
    {
        var previous = viewModel.Selected;
        var previousProxy = viewModel.SelectedProxy;
        // Before the pane was shown (a notification opening it), its rows aren't there yet.
        viewModel.Apply(Model.Status);
        viewModel.Select(name);
        if (!ReferenceEquals(previous, viewModel.Selected) || !ReferenceEquals(previousProxy, viewModel.SelectedProxy))
        {
            DetailScroller.ChangeView(null, 0, null, true);
        }
    }

    private void UpdateRetained()
    {
        RefreshDetail();
    }

    private void ApplyStatus()
    {
        var selectedBefore = viewModel.Selected;
        var proxyBefore = viewModel.SelectedProxy;
        if (viewModel.Apply(Model.Status) && ReferenceEquals(selectedBefore, viewModel.Selected) && ReferenceEquals(proxyBefore, viewModel.SelectedProxy))
        {
            RefreshDetail();
        }
    }

    private void Rerender()
    {
        RefreshDetail();
    }

    private void RefreshDetail()
    {
        if (!loaded)
        {
            return;
        }
        // A proxy: the site list has no selection, and the other way round.
        if (viewModel.SelectedProxy is { } proxy)
        {
            SiteList.SelectedItem = null;
            if (!ReferenceEquals(ProxyList.SelectedItem, proxy))
            {
                ProxyList.SelectedItem = proxy;
            }
            DetailHost.Content = ProxyDetail(proxy.Snapshot);
            return;
        }
        // No sites: the pane shows its empty state instead (HasSites).
        if (viewModel.Selected is not { } row)
        {
            DetailHost.Content = null;
            return;
        }
        ProxyList.SelectedItem = null;
        if (!ReferenceEquals(SiteList.SelectedItem, row))
        {
            SiteList.SelectedItem = row;
        }
        DetailHost.Content = row.Snapshot.IsWorktree ? WorktreeDetail(row.Snapshot) : Detail(row.Snapshot, row);
    }

    private void AddSiteClicked(object sender, RoutedEventArgs args)
    {
        Start("add-site", AddSiteAsync, false);
    }

    private void SiteSelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (SiteList.SelectedItem is SiteRowViewModel row)
        {
            Select(row.Name);
        }
    }

    private void ProxySelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (ProxyList.SelectedItem is ProxyRowViewModel row)
        {
            Select(row.Name);
        }
    }

    private void AddProxyClicked(object sender, RoutedEventArgs args)
    {
        Start("add-proxy", AddProxyAsync, false);
    }

    /// <summary>Asks for a name and an upstream, and serves &lt;name&gt;.test from it.</summary>
    private async Task AddProxyAsync()
    {
        var tld = Model.Status?.Dns.Tld is { Length: > 0 } t ? t : "test";
        var name = new TextBox { Header = "Name", PlaceholderText = "my-app" };
        var upstream = new TextBox { Header = "Upstream", PlaceholderText = "Port, host:port or URL, e.g. 3000" };
        var dialog = new ContentDialog
        {
            Title = "Add a proxy",
            Content = Ui.Column(12,
                Ui.Text($"Flock serves <name>.{tld} from a server of its own, e.g. a Next.js, Nuxt or Vite dev server. A port means 127.0.0.1."),
                name, upstream),
            PrimaryButtonText = "Add",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var proxyName = name.Text.Trim();
        var proxyUpstream = upstream.Text.Trim();
        if (proxyName.Length == 0 || proxyUpstream.Length == 0)
        {
            return;
        }
        if (await Model.Sites.ProxyAsync(proxyName, proxyUpstream) == OperationOutcome.Succeeded)
        {
            Select(proxyName);
        }
    }

    /// <summary>The selected proxy: its address and actions, then its server, HTTPS and upstream.</summary>
    private Border ProxyDetail(Status.Proxy proxy)
    {
        var panel = new StackPanel { Spacing = 20 };

        var heading = Ui.Column(2, Ui.Row(8, Ui.Text(proxy.Name, "SubtitleTextBlockStyle", wrap: false), Ui.Badge("Proxy", Ui.Tone.Neutral)));
        if (proxy.Url is { } proxyUrl)
        {
            var link = Ui.Link(proxyUrl, () => Model.Open(proxyUrl));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {proxy.Url ?? proxy.Domain}", () =>
        {
            if (proxy.Url is { } url)
            {
                Model.Open(url);
            }
        });
        open.IsEnabled = proxy.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphDelete, "Remove proxy", () => Start(proxy.Name, () => RemoveProxyAsync(proxy))));
        actions.VerticalAlignment = VerticalAlignment.Top;
        var header = new Grid { ColumnSpacing = 8 };
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(actions, 1);
        header.Children.Add(heading);
        header.Children.Add(actions);
        panel.Children.Add(header);
        if (proxy.Problem is { } problem)
        {
            panel.Children.Add(Warning(problem));
        }

        var server = Ui.Column(2, Ui.Row(6, Ui.StatusDot(proxy.Running), Ui.Text(proxy.ServerLabel, wrap: false)));
        if (!proxy.Running)
        {
            server.Children.Add(Ui.Caption($"Nothing answers at {proxy.UpstreamLabel} yet.", Ui.TertiaryText));
        }
        var https = new ToggleSwitch { IsOn = proxy.Secured, MinWidth = 0 };
        Ui.AutomationName(https, "HTTPS");
        https.Toggled += (_, _) =>
        {
            // Rerendered afterwards, so a refused change snaps back.
            var on = https.IsOn;
            Start(proxy.Name, () => Model.Sites.SecureProxyAsync(proxy.Name, on));
        };
        var upstream = Ui.MonoText(proxy.Upstream);
        upstream.VerticalAlignment = VerticalAlignment.Center;
        var change = Ui.Button("Change…", () => Start(proxy.Name, () => ChangeUpstreamAsync(proxy)));
        panel.Children.Add(Section("Proxy",
            Field("Server", server),
            Field("HTTPS", https),
            Field("Upstream", Ui.Row(8, upstream, change)),
            Ui.Caption(
                $"Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Flock doesn't start the server, so start it as usual. Until it answers, {proxy.Domain} shows a page saying so.",
                Ui.TertiaryText)));

        var card = Ui.Card(panel, new Thickness(20, 16, 16, 20));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    private async Task ChangeUpstreamAsync(Status.Proxy proxy)
    {
        if (await Window.Ask($"Change the upstream of {proxy.Name}",
                "A port means 127.0.0.1; give a host and port (localhost:5173) or a URL (https://127.0.0.1:8443) for anything else.",
                "Port, host:port or URL", "Change", proxy.UpstreamLabel) is { } upstream && upstream != proxy.UpstreamLabel)
        {
            await Model.Sites.ProxyAsync(proxy.Name, upstream);
        }
    }

    private async Task RemoveProxyAsync(Status.Proxy proxy)
    {
        if (await Window.Confirm($"Remove {proxy.Name}?", $"Flock stops serving {proxy.Domain}. The server at {proxy.Upstream} keeps running.", "Remove", true))
        {
            await Model.Sites.RemoveProxyAsync(proxy.Name);
        }
    }

    /// <summary>The selected site: its address, then Site, Versions and Server sections of labelled settings.</summary>
    private Border Detail(Status.Site site, SiteRowViewModel? row = null)
    {
        var config = Model.Configuration.Current;
        var secured = row?.Secured ?? site.Secured;
        var php = row?.Php ?? site.Php;
        var node = row?.Node ?? Major(site.Node ?? "");
        var panel = new StackPanel { Spacing = 20 };

        // Header: name, address and folder, and the site's actions.
        var name = Ui.Row(8, Ui.Text(DisplayName(site), "SubtitleTextBlockStyle", wrap: false));
        if (site.Parked)
        {
            name.Children.Add(Ui.Badge("Project folder", Ui.Tone.Neutral));
        }
        var heading = Ui.Column(2, name);
        if (site.Url is { } siteUrl)
        {
            var link = Ui.Link(siteUrl, () => Model.Open(site));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var path = Ui.Caption(site.Path, Ui.TertiaryText, false);
        ToolTipService.SetToolTip(path, site.Path);
        heading.Children.Add(path);

        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {site.Url ?? site.Domain}", () => Model.Open(site));
        open.IsEnabled = site.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Model.Reveal(site.Path)),
            Ui.IconButton(Ui.GlyphPuzzle, "PHP extensions", () => Start("extensions:" + site.Name, () => ShowExtensions(site))), More(site));
        if (!site.Parked)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphDelete, "Remove site", () => Start(site.Name, () => RemoveSiteAsync(site))));
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

        // Site: HTTPS, the suffix it's served under, and the domains besides its own.
        var https = new ToggleSwitch { IsOn = secured, MinWidth = 0 };
        Ui.AutomationName(https, "HTTPS");
        https.Toggled += (_, _) =>
        {
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Secured: https.IsOn), () => row?.SetDraftSecured(https.IsOn)), false);
        };
        var siteSection = Section("Site",
            Field("HTTPS", https),
            Field("Domain", DomainRow(site)),
            Field("Aliases", DomainList(site, site.Domains.Where(domain => domain.Alias), "Add Alias…", "Another name for the site, with its own subdomains", () => AddAliasAsync(site))),
            Field("Subdomains", DomainList(site, site.Domains.Where(domain => !domain.Alias), "Add Subdomain…", $"A name under {site.Domain}, following the site's suffix", () => AddSubdomainAsync(site))));
        if (Model.Status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 } dns)
        {
            siteSection.Children.Add(Ui.Caption($"Subdomains only work when you add them here: {dns.WildcardProblem}.", Ui.Caution));
        }
        siteSection.Children.Add(Field("Root", RootPicker(site)));
        foreach (var hint in RootHints(site))
        {
            siteSection.Children.Add(hint);
        }
        siteSection.Children.Add(Ui.Caption(
            "The folder Flock serves the site from. One picked here applies on this machine only; pin it (…) to write it into .flockrc for everyone.", Ui.TertiaryText));
        panel.Children.Add(siteSection);
        panel.Children.Add(Share(site));

        // Versions: PHP and Node.js, with what pins or isolates them.
        var phpPicker = Picker("PHP", php, site.PhpSource, site.PhpPinned, site.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => new SiteChange(Php: version), site.Name, value => row?.SetDraftPhp(value));
        var nodePicker = Picker("Node", node, site.NodeSource, Major(site.NodePinned), site.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => new SiteChange(Node: version), site.Name, value => row?.SetDraftNode(value));
        var versions = Section("Versions", Field("PHP", phpPicker), Field("Node.js", nodePicker));
        foreach (var hint in Hints(site))
        {
            versions.Children.Add(hint);
        }
        versions.Children.Add(Ui.Caption(
            "A version picked here applies on this machine only. To set it for everyone working on the project, pin it (…): PHP goes into .flockrc, Node.js into .nvmrc.",
            Ui.TertiaryText));
        panel.Children.Add(versions);

        panel.Children.Add(Server(site, row));

        if (Boost(site) is { } boost)
        {
            panel.Children.Add(Section("Laravel Boost", boost));
        }

        // Last, so screenshots and the docs find it at the end of the details.
        panel.Children.Add(Worktrees(site));

        var card = Ui.Card(panel, new Thickness(20, 16, 16, 20));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    /// <summary>
    ///     The Site section's Domain row (macOS's field("Domain")): the shared suffix picker with its live
    ///     check, and an Apply that runs the bundled CLI's `flock site tld`. The picker seeds itself on the
    ///     site's effective suffix (a preset row, or the custom field with a suffix the menu doesn't offer);
    ///     a site that follows the global one is pushed onto Inherit. Inherit has no verdict to wait for, so
    ///     its Apply is offered only while the site pins a suffix today (macOS's canApplySuffix). A switch in
    ///     flight and a failed one come from the pane's fields, so they survive a re-render of the details.
    /// </summary>
    private StackPanel DomainRow(Status.Site site)
    {
        var global = Model.Status?.Dns.Tld is { Length: > 0 } tld ? tld : "test";
        var switching = applyingDomain == site.Name;
        // The check is wired before the suffix arrives, so the seeded row's verdict starts with it (as on macOS).
        var picker = new SuffixPickerControl { AllowInherit = true };
        picker.CheckAsync = tld => Model.Client.CheckTldAsync(tld);
        picker.Current = site.Tld;
        if (site.SiteTld.Length == 0)
        {
            picker.Select(SuffixPickerControl.InheritTag);
        }
        var apply = new Button
        {
            Content = new BusyContent { Text = "Apply", BusyText = "Applying…", IsBusy = switching },
            VerticalAlignment = VerticalAlignment.Top,
        };
        void UpdateApply() =>
            apply.IsEnabled = applyingDomain is null && (picker.IsInherit ? site.SiteTld.Length > 0 : picker.CanApply);
        picker.Changed += UpdateApply;
        UpdateApply();
        apply.Click += async (_, _) =>
        {
            if (applyingDomain is not null)
            {
                return;
            }
            // The draft is captured now, so a mid-run edit can't change what runs; Inherit names the global
            // suffix in the declined note, as on macOS.
            var inherit = picker.IsInherit;
            var applied = inherit ? global : picker.DraftNormalized ?? "";
            if (applied.Length == 0)
            {
                return;
            }
            var arguments = new List<string> { "site", "tld" };
            arguments.Add(inherit ? "--inherit" : applied);
            arguments.Add(site.Name);
            applyingDomain = site.Name;
            domainSwitchFailed = null;
            Rerender();
            try
            {
                var (ok, declined, output) = await domainApply.RunAsync(arguments);
                if (!ok)
                {
                    domainSwitchFailed = (site.Name, applied, output, declined);
                }
            }
            catch (Exception error)
            {
                // A CLI run that throws would otherwise reach App.UnhandledException and quit the app.
                App.Log(error);
                domainSwitchFailed = (site.Name, applied, $"The suffix switch did not finish: {error.Message}", false);
            }
            finally
            {
                applyingDomain = null;
                // The bundled CLI already wrote the site's suffix: the status poll (2 s) re-seeds this row
                // from the fresh snapshot — no config read here, unlike the global switch, which moves the
                // TLD the General card shows.
                Rerender();
            }
        };
        var row = Ui.Column(6,
            Ui.Row(8, picker, apply),
            Ui.Caption($"The suffix {site.Name} is served under. Inherit follows Flock's global (.{global}); a picked suffix pins {site.Name} to its own.", Ui.TertiaryText));
        if (domainSwitchFailed is { } failed && failed.Site == site.Name)
        {
            row.Children.Add(CliOutput(failed.Output));
            if (failed.Declined)
            {
                row.Children.Add(Ui.Caption(
                    $"You declined administrator approval. Flock's config is on .{failed.Applied} for {site.Name} already; finish the switch by running `flock install` in a terminal.", Ui.Caution));
            }
        }
        return row;
    }

    /// <summary>One of the two domain lists (macOS's field("Aliases") and field("Subdomains")): each entry
    /// with its own Remove, and the link that opens the dialog adding another.</summary>
    private StackPanel DomainList(Status.Site site, IEnumerable<Status.SiteDomain> domains, string addLabel, string addHelp, Func<Task> add)
    {
        var list = Ui.Column(2);
        foreach (var domain in domains)
        {
            UIElement shown = domain.Problem is { } domainProblem ? Warning($"{domain.Host}: {domainProblem}")
                : domain.Url is { Length: > 0 } url ? DomainLink(domain, url)
                : Ui.Text(domain.Host, wrap: false);
            var target = domain;
            var remove = Ui.IconButton(Ui.GlyphRemove, $"Remove {domain.Host}", () => Start(site.Name, () => RemoveDomain(site, target)));
            remove.Padding = new Thickness(4);
            var domainRow = new Grid { ColumnSpacing = 4 };
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            Grid.SetColumn(remove, 1);
            domainRow.Children.Add(shown);
            domainRow.Children.Add(remove);
            list.Children.Add(domainRow);
        }
        var addDomain = Ui.Link(addLabel, () => Start(site.Name, add));
        addDomain.Padding = new Thickness(0);
        ToolTipService.SetToolTip(addDomain, addHelp);
        list.Children.Add(addDomain);
        return list;
    }

    /// <summary>A bundled CLI run's aggregated output, shown under the control that ran it (the SetupWindow's
    /// log box, compact): monospaced, selectable, horizontally scrollable.</summary>
    private static Border CliOutput(string output) =>
        Ui.Card(new ScrollViewer { MaxHeight = 160, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, Content = Ui.MonoText(output, 12) }, new Thickness(12, 10));

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

    /// <summary>
    ///     What runs the site's PHP: php-cgi, or FrankenPHP (classic or Laravel Octane's worker mode),
    ///     with FrankenPHP's state, Restart and its log.
    /// </summary>
    private StackPanel Server(Status.Site site, SiteRowViewModel? row = null)
    {
        var franken = (row?.Server ?? site.Server) == Status.SiteServer.FrankenPhp;
        var server = new ComboBox { MinWidth = 140 };
        server.Items.Add(new ComboBoxItem { Content = "PHP-CGI" });
        // FrankenPHP needs PHP 8.2; a site already on it keeps the option (the daemon explains the fallback).
        server.Items.Add(new ComboBoxItem { Content = "FrankenPHP", IsEnabled = franken || SupportsFrankenPhp(site.Php) });
        server.SelectedIndex = franken ? 1 : 0;
        server.SelectionChanged += (_, _) =>
        {
            if (server.SelectedIndex < 0 || server.SelectedIndex == 1 == franken)
            {
                return;
            }
            var change = server.SelectedIndex == 1 ? Status.SiteServer.FrankenPhp : Status.SiteServer.FastCgi;
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Server: change), () => row?.SetDraftServer(change)), false);
        };
        Ui.AutomationName(server, "Server");

        var section = Section("Server", Field("Runs on", server));
        if (franken)
        {
            var octane = (row?.Worker ?? site.Worker) == Status.SiteServer.Octane;
            var mode = new ComboBox { MinWidth = 150 };
            mode.Items.Add(new ComboBoxItem { Content = "Classic" });
            mode.Items.Add(new ComboBoxItem { Content = "Laravel Octane", IsEnabled = octane || site.OctaneAvailable });
            mode.SelectedIndex = octane ? 1 : 0;
            mode.SelectionChanged += (_, _) =>
            {
                if (mode.SelectedIndex < 0 || mode.SelectedIndex == 1 == octane)
                {
                    return;
                }
                var worker = mode.SelectedIndex == 1 ? Status.SiteServer.Octane : "";
                Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Worker: worker), () => row?.SetDraftWorker(worker)), false);
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
            var restart = Ui.Button("Restart", () => Start(site.Name, () => Restart(site.Name)),
                tooltip: "Restart FrankenPHP, e.g. so Octane's workers load changed code");
            restart.IsEnabled = site.FrankenPhp is { IsRunning: true } && !restarting.Contains(site.Name);
            var log = site.FrankenPhp is { Log.Length: > 0 } f ? f.Log : $"frankenphp-{site.Name}";
            var buttons = Ui.Row(8, restart, Ui.Button("Show Log", () => App.Current.OpenLog(new LogsViewModel.ServiceSource(log))));
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
                Width = 320
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
            await Model.Sites.RestartAsync(site);
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
        return major > 8 || major == 8 && minor >= 2;
    }

    /// <summary>
    ///     A version picker: first the project's pinned version ("Use .nvmrc (Node 20)") or the default,
    ///     which follow the project; then the installed versions, which isolate the site.
    /// </summary>
    private ComboBox Picker(string label, string current, string source, string pinned, string file, string defaultVersion,
        List<string> installed, Func<string, SiteChange> change, string siteName, Func<string, Action?>? draft = null)
    {
        // No default yet while the first run installs it.
        var follow = pinned.Length > 0 ? $"Use {Path.GetFileName(file)} ({label} {pinned})"
            : defaultVersion.Length > 0 ? $"Default ({label} {defaultVersion})"
            : "Default";
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
            Start(siteName, () => UpdateSiteAsync(siteName, change(isolate), () => draft?.Invoke(isolate)), false);
        };
        ToolTipService.SetToolTip(picker, source switch
        {
            Status.VersionSource.Isolated => $"{label} {current} is set for this site on this machine (isolated).",
            Status.VersionSource.Project => $"The project pins {label} {pinned} in {Path.GetFileName(file)}.",
            _ => $"The site follows Flock's default {label} version."
        });
        return picker;
    }

    /// <summary>
    ///     The document root picker: first the project's (.flockrc) or the detected one, which follow
    ///     the project; then the folders with an index.php Flock found and the one set here, which set
    ///     the root on this machine; then any folder in the project.
    /// </summary>
    private ComboBox RootPicker(Status.Site site)
    {
        var follow = site.RootPinnedHere ? $"Use .flockrc ({RootLabel(site.RootPinned)})"
            : site.RootSource != Status.VersionSource.Default ? "Automatic"
            : site.RootState == Status.DocumentRootState.NotFound ? "Automatic (none found)"
            : $"Automatic ({RootLabel(site.Root)})";
        var folders = site.RootSuggestions.ToList();
        if (site.RootSource == Status.VersionSource.Isolated && !folders.Contains(site.Root))
        {
            folders.Add(site.Root);
        }

        var picker = new ComboBox { MinWidth = 190 };
        picker.Items.Add(follow);
        foreach (var folder in folders)
        {
            picker.Items.Add(RootLabel(folder));
        }
        picker.Items.Add("Choose Folder…");
        picker.SelectedIndex = site.RootSource == Status.VersionSource.Isolated ? folders.IndexOf(site.Root) + 1 : 0;
        var selected = picker.SelectedIndex;
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedIndex < 0 || picker.SelectedIndex == selected)
            {
                return;
            }
            if (picker.SelectedIndex == picker.Items.Count - 1)
            {
                picker.SelectedIndex = selected;
                Start(site.Name, () => ChooseRoot(site));
                return;
            }
            var root = picker.SelectedIndex == 0 ? "" : folders[picker.SelectedIndex - 1];
            Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Root: root)));
        };
        Ui.AutomationName(picker, "Document root");
        ToolTipService.SetToolTip(picker, site.RootSource switch
        {
            Status.VersionSource.Isolated => $"{RootLabel(site.Root)} is set for this site on this machine.",
            Status.VersionSource.Project => $"The project sets {RootLabel(site.Root)} in .flockrc.",
            _ => "Flock serves the first of public\\, www\\, web\\ and webroot\\ with an index.php, else the project folder."
        });
        return picker;
    }

    /// <summary>Asks for a folder in the project and serves the site from it on this machine.</summary>
    private async Task ChooseRoot(Status.Site site)
    {
        if (await Window.PickFolder() is not { } folder)
        {
            return;
        }
        if (site.RootFor(folder) is not { } root)
        {
            await Ui.Inform(Window.XamlRoot, "Choose a folder in the project",
                $"{folder} isn't in {site.Path}. The document root must be a folder in the project.");
            return;
        }
        await Model.Sites.UpdateAsync(site.Name, new SiteChange(Root: root));
    }

    /// <summary>Why the site isn't served from its root, and a root set here over the project's.</summary>
    private IEnumerable<UIElement> RootHints(Status.Site site)
    {
        if (site.RootState == Status.DocumentRootState.NotFound)
        {
            yield return Hint(
                "No public\\, www\\, web\\ or webroot\\ with an index.php was found, so Flock shows a page instead of the site. Choose the folder with the app's index.php.",
                null, null);
        }
        else if (site.RootState == Status.DocumentRootState.Missing)
        {
            yield return Hint($"Choose the folder with the app's index.php, or create {site.Root}.", null, null);
        }
        else if (site.RootState == Status.DocumentRootState.Empty)
        {
            yield return Ui.Caption($"{RootLabel(site.Root)} has no index.php or index.html, so Flock shows a page instead of the site until it does.", Ui.TertiaryText);
        }
        else if (site.RootSource == Status.VersionSource.Isolated && site.RootPinnedHere)
        {
            yield return Hint($"{RootLabel(site.Root)} is set here; the project sets {RootLabel(site.RootPinned)} in .flockrc.",
                "Use .flockrc", () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Root: ""))));
        }
    }

    /// <summary>"src/www", or "Project folder" for ".".</summary>
    private static string RootLabel(string root)
    {
        return root == "." ? "Project folder" : root;
    }

    /// <summary>Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.</summary>
    private IEnumerable<UIElement> Hints(Status.Site site)
    {
        if (site.PhpSource == Status.VersionSource.Isolated && site.PhpPinned.Length > 0 && site.PhpPinned != site.Php)
        {
            yield return Hint($"PHP {site.Php} is isolated here; the project pins {site.PhpPinned} in {Path.GetFileName(site.PhpFile)}.",
                $"Use {Path.GetFileName(site.PhpFile)}", () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Php: ""))));
        }
        if (site.NodeSource == Status.VersionSource.Isolated && site.NodePinned.Length > 0 && Major(site.NodePinned) != Major(site.Node ?? ""))
        {
            yield return Hint($"Node {site.Node} is isolated here; the project pins {site.NodePinned} in {Path.GetFileName(site.NodeFile)}.",
                $"Use {Path.GetFileName(site.NodeFile)}", () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Node: ""))));
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
        var link = Ui.Link(missing ? "Add Flock to its guidelines…" : "Update it…", () => Start(site.Name, () => AddBoostGuideline(site, missing)));
        link.Padding = new Thickness(0);
        return Ui.Column(2, Ui.Caption(missing
            ? "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
            : "Flock's guideline in the project's Boost guidelines is outdated."), link);
    }

    private async Task AddBoostGuideline(Status.Site site, bool missing)
    {
        var title = missing ? $"Add Flock to {site.Name}'s Boost guidelines?" : $"Update Flock's guideline in {site.Name}?";
        var details =
            $"Writes .ai\\guidelines\\flock.md in {site.Path} and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. " +
            "Commit it with the rest of .ai\\guidelines. This changes files in your project.";
        if (await Window.Confirm(title, details, missing ? "Add" : "Update"))
        {
            await Model.Sites.AddBoostGuidelineAsync(site.Name);
        }
    }

    private HyperlinkButton DomainLink(Status.SiteDomain domain, string url)
    {
        var link = Ui.Link(domain.Alias ? $"{domain.Host} (alias)" : domain.Host, () => Model.Open(url));
        link.Padding = new Thickness(0);
        ToolTipService.SetToolTip(link, $"Open {url}");
        return link;
    }

    /// <summary>
    ///     A site's PHP extensions: what composer.json and the locked packages require, what a Laravel
    ///     app's configuration shows it uses without requiring it, and whether Flock's PHP has each.
    ///     Adding one to composer.json is confirmed inline (only one dialog can be open at a time).
    /// </summary>
    private async Task ShowExtensions(Status.Site site)
    {
        var list = new StackPanel { Spacing = 6 };
        var scroller = new ScrollViewer { Content = list, Width = 560, Height = 320 };
        var dialog = new ContentDialog
        {
            Title = $"PHP extensions of {site.Name}",
            Content = Ui.Column(10, scroller,
                Ui.Caption("Requiring an extension in composer.json makes composer install refuse a server without it, instead of the app failing there.")),
            CloseButtonText = "Done",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot
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
                    actions.Children.Add(Ui.Link("Cancel", () =>
                    {
                        confirming = null;
                        Render();
                    }));
                    actions.Children.Add(Ui.Button("Require", () => Start("extensions:" + site.Name, () => Add(name)), true,
                        tooltip: $"Runs composer require ext-{name}:* in {site.Path}, which changes composer.json and composer.lock. Commit them with the project."));
                }
                else if (!ext.Declared)
                {
                    var name = ext.Name;
                    var add = Ui.Button("Add to composer.json…", () =>
                    {
                        confirming = name;
                        Render();
                    });
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
            (var fresh, error) = await Model.Sites.RequireExtensionAsync(site.Name, name, activeLifetime?.Token ?? CancellationToken.None);
            report = fresh ?? report;
            adding = null;
            Render();
        }

        Render();
        var load = Model.Sites.SiteExtensionsAsync(site.Name, activeLifetime?.Token ?? CancellationToken.None);
        var shown = dialog.ShowAsync().AsTask();
        (report, error) = await load;
        if (report is null && error is null)
        {
            dialog.Hide();
            await shown;
            return;
        }
        Render();
        await shown;
    }

    /// <summary>
    ///     Adds an alias: a name of its own beside the site (macOS's AddAliasSheet). The label lands under the
    ///     picked suffix — Inherit rides the site's suffix and moves with it, while a picked suffix pins the
    ///     alias in place, even one equal to the site's own (the API forces the pin). A suffix new to this
    ///     machine is provisioned on the spot: the add chains the bundled CLI's `flock install`, prompts included.
    /// </summary>
    internal async Task AddAliasAsync(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var global = dns?.Tld is { Length: > 0 } tld ? tld : "test";
        var siteSuffix = site.Tld.Length > 0 ? site.Tld : global; // what an inherited alias rides (an old daemon names none)
        var name = new TextBox { Header = "Name", PlaceholderText = "api2" };
        var picker = new SuffixPickerControl { AllowInherit = true };
        picker.CheckAsync = tld => Model.Client.CheckTldAsync(tld);
        // Here Current only names the Inherit row (Add is gated on the checked draft, not on CanApply), so an
        // old daemon that reports no effective suffix still gets an honest "Inherit (.test)" label.
        picker.Current = siteSuffix;
        picker.Select(SuffixPickerControl.InheritTag); // the sheet opens on Inherit, as on macOS
        var lands = Ui.Caption("", wrap: false);
        var dialog = new ContentDialog
        {
            Title = $"Add an alias to {site.Name}",
            Content = Ui.Column(12,
                Ui.Caption($"An alias is a separate name for {site.Name}, with its own subdomains; the label is one word, no dots. Inherit follows {site.Name}'s suffix (.{siteSuffix}) and moves with it; a picked suffix pins the alias in place, and a suffix new to this PC is provisioned on the spot."
                           + (dns is { Wildcard: false, WildcardProblem.Length: > 0 } problem
                               ? $" Listing names matters here: on this PC only the site names and the names you add resolve ({problem.WildcardProblem})."
                               : "")),
                name,
                picker,
                lands),
            PrimaryButtonText = "Add",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        void UpdateAdd()
        {
            // Add needs a label, and on a picked suffix the daemon's verdict: a pending or refused check keeps it
            // off. A suffix equal to the site's own is a legal explicit pin, so this isn't the picker's CanApply.
            dialog.IsPrimaryButtonEnabled = name.Text.Trim().Length > 0 && (picker.IsInherit || picker.DraftNormalized is not null);
            lands.Text = $"{(name.Text.Trim() is { Length: > 0 } label ? label : "name")}.{(picker.IsInherit ? siteSuffix : picker.DraftNormalized ?? "suffix")} is what lands.";
        }
        name.TextChanged += (_, _) => UpdateAdd();
        picker.Changed += UpdateAdd;
        UpdateAdd();
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var domain = name.Text.Trim();
        var suffix = picker.IsInherit ? SuffixPickerControl.InheritTag : picker.DraftNormalized;
        if (domain.Length == 0 || suffix is not { Length: > 0 })
        {
            return;
        }
        // R8, read before the add (macOS's isNewSuffix): the suffix is new to this machine when it is neither
        // the global one nor one of the suffixes already served. Inherit never provisions anything; a daemon
        // from before the list chains once, and `flock install` is idempotent.
        var fresh = suffix != SuffixPickerControl.InheritTag
                    && dns is not null && suffix != dns.Tld && !dns.Suffixes.Any(s => s.Tld.Length > 0 && s.Tld == suffix);
        if (await Model.Sites.AddDomainAsync(site.Name, domain, suffix) != OperationOutcome.Succeeded)
        {
            return; // the window's error bar carries the refusal, as for every other site mutation
        }
        if (fresh)
        {
            await ProvisionSuffixAsync(site.Name, suffix);
        }
        Select(site.Name);
    }

    /// <summary>
    ///     The chained `flock install` for a suffix new to the machine (macOS's chainInstall in its
    ///     ProvisionSheet): the alias is in the config already, and the bundled CLI makes the suffix resolve
    ///     with its own trusted certificates. The aggregated output appears only when the run ends in failure
    ///     or a declined prompt; a finished run closes the dialog by itself.
    /// </summary>
    private async Task ProvisionSuffixAsync(string siteName, string suffix)
    {
        var running = Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption($"Flock is making .{suffix} resolve…", wrap: false));
        var output = Ui.MonoText("", 12);
        var box = Ui.Card(new ScrollViewer { MaxHeight = 160, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, Content = output }, new Thickness(12, 10));
        var declined = Ui.Caption("", Ui.Caution);
        box.Visibility = Visibility.Collapsed;
        declined.Visibility = Visibility.Collapsed;
        var dialog = new ContentDialog
        {
            Title = $"Provisioning .{suffix}",
            Content = Ui.Column(10, running, box, declined),
            CloseButtonText = "Hide",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot
        };
        // Closing while the CLI runs is allowed (Hide is how the worktree job's dialog puts it); then the
        // late success must not hide a dialog that is already gone.
        var closed = false;
        dialog.Closed += (_, _) => closed = true;
        var run = domainApply.RunAsync(["install"]);
        var shown = dialog.ShowAsync().AsTask();
        var (ok, wasDeclined, text) = await run;
        if (ok)
        {
            if (!closed)
            {
                dialog.Hide();
            }
            await shown;
            return;
        }
        // Ended short: the dialog keeps the CLI's own words, as General's Domain card does inline.
        dialog.Title = $"Provisioning .{suffix} didn't finish";
        running.Visibility = Visibility.Collapsed;
        output.Text = text;
        box.Visibility = Visibility.Visible;
        dialog.CloseButtonText = "Close";
        if (wasDeclined)
        {
            declined.Text = $"You declined administrator approval. The alias is added to {siteName} already; finish by running `flock install` in a terminal to make .{suffix} resolve.";
            declined.Visibility = Visibility.Visible;
        }
        await shown;
    }

    /// <summary>
    ///     Adds a subdomain: one name under the site's domain ("tenant1", "a.b"). There is no suffix to pick
    ///     — it hangs under the site and follows its suffix, and the daemon's classification decides what
    ///     lands (macOS's AddSubdomainSheet). Nothing to provision: the site's suffix is served already.
    /// </summary>
    private async Task AddSubdomainAsync(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var depth = dns?.MaxDepth ?? 4;
        var name = new TextBox { Header = "Name", PlaceholderText = "tenant1 or a.b" };
        var dialog = new ContentDialog
        {
            Title = $"Add a subdomain to {site.Name}",
            Content = Ui.Column(12,
                Ui.Caption(dns is { Wildcard: false, WildcardProblem.Length: > 0 } problem
                    ? $"Wildcard subdomains don't work on this PC ({problem.WildcardProblem}), so add each one you use: it's written to the hosts file. A subdomain hangs under {site.Domain} and follows the site's suffix."
                    : $"Subdomains up to {depth} levels below {site.Domain} already work on this PC; list one to see it here and where wildcard DNS doesn't. It hangs under {site.Domain} and follows the site's suffix."),
                name),
            PrimaryButtonText = "Add",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        void UpdateAdd() => dialog.IsPrimaryButtonEnabled = name.Text.Trim().Length > 0;
        name.TextChanged += (_, _) => UpdateAdd();
        UpdateAdd();
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var subdomain = name.Text.Trim();
        if (subdomain.Length == 0)
        {
            return;
        }
        if (await Model.Sites.AddDomainAsync(site.Name, subdomain) == OperationOutcome.Succeeded)
        {
            Select(site.Name);
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
        var nodeFile = site.NodePinnedHere ? Path.GetFileName(site.NodeFile) : ".nvmrc";

        // Renames first: the display name through the API, the domain name through the bundled CLI. Worktrees
        // never see this menu — the details route them to WorktreeDetail, which has no "…" button at all.
        menu.Items.Add(Item("Rename…", () => Start(site.Name, () => RenameTitleAsync(site))));
        if (site.Title.Length > 0)
        {
            menu.Items.Add(Item("Use the site name", () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Title: "")))));
        }
        menu.Items.Add(Item("Change the domain name…", () => Start(site.Name, () => RenameHostAsync(site))));
        menu.Items.Add(new MenuFlyoutSeparator());

        // Pin and unpin items appear only when there is something to pin or unpin (the menu always has the rename items).
        if (site.Php.Length > 0 && !(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere))
        {
            menu.Items.Add(Item($"Pin PHP {site.Php} to Project…", () => Start(site.Name, () => Pin(site, "php", site.Php,
                $"Writes php={site.Php} to .flockrc in {site.Path}. Commit it, and everyone using Flock on this project gets PHP {site.Php}."))));
        }
        if (nodeMajor.Length > 0 && !(site.NodeSource == Status.VersionSource.Project && site.NodePinnedHere))
        {
            menu.Items.Add(Item($"Pin Node {nodeMajor} to Project…", () => Start(site.Name, () => Pin(site, "node", nodeMajor,
                $"Writes {nodeMajor} to {nodeFile} in {site.Path}. Commit it, and Flock, nvm and other Node tools use Node {nodeMajor} for this project."))));
        }
        if (site.RootState is "" or Status.DocumentRootState.Empty && site.RootSource != Status.VersionSource.Project)
        {
            menu.Items.Add(Item($"Pin Document Root ({RootLabel(site.Root)}) to Project…", () => Start(site.Name, () => Pin(site, "root", site.Root,
                $"Writes root={site.Root} to .flockrc in {site.Path}. Commit it, and everyone using Flock on this project serves it from {RootLabel(site.Root)}."))));
        }
        if (site.PhpPinnedHere || site.NodePinnedHere || site.RootPinnedHere)
        {
            if (menu.Items.Count > 0)
            {
                menu.Items.Add(new MenuFlyoutSeparator());
            }
            if (site.PhpPinnedHere)
            {
                menu.Items.Add(Item($"Unpin PHP ({Path.GetFileName(site.PhpFile)})…", () => Start(site.Name, () => Unpin(site, "php",
                    $"Removes the PHP version from {site.PhpFile}. The site then uses Flock's default PHP."))));
            }
            if (site.NodePinnedHere)
            {
                menu.Items.Add(Item($"Unpin Node ({Path.GetFileName(site.NodeFile)})…", () => Start(site.Name, () => Unpin(site, "node",
                    $"Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in {site.Path}. The site then uses Flock's default Node.js."))));
            }
            if (site.RootPinnedHere)
            {
                menu.Items.Add(Item("Unpin Document Root (.flockrc)…", () => Start(site.Name, () => Unpin(site, "root",
                    $"Removes root= from .flockrc in {site.Path}. Flock then finds the document root itself."))));
            }
        }

        var button = Ui.IconButton(Ui.GlyphMore, "Rename the site, or pin versions and the document root to the project", () => { });
        button.Flyout = menu;
        button.IsEnabled = menu.Items.Count > 0;
        return button;
    }

    /// <summary>The site's display name: its title, or the site name when no title is set
    /// (macOS's title.isEmpty ? name : title).</summary>
    private static string DisplayName(Status.Site site) => site.Title.Length > 0 ? site.Title : site.Name;

    /// <summary>
    ///     Renames the display name (macOS's click-to-rename header): a plain API change — the title only
    ///     changes what Flock shows, never what the site is served as. Empty means inherit again, which the
    ///     wire spells "title":"" (Kit pins the empty-string three-state for tld and host the same way).
    ///     A refusal lands in the window's error bar like every other site mutation; a success arrives
    ///     with the next status poll.
    /// </summary>
    private async Task RenameTitleAsync(Status.Site site)
    {
        var title = new TextBox { Header = "Display name", PlaceholderText = site.Name, Text = site.Title };
        var dialog = new ContentDialog
        {
            Title = $"Rename {DisplayName(site)}",
            Content = Ui.Column(12,
                Ui.Caption("Shown in Flock. Leave empty to use the site name.", Ui.TertiaryText),
                title),
            PrimaryButtonText = "Rename",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        await Model.Sites.UpdateAsync(site.Name, new SiteChange(Title: title.Text.Trim()));
    }

    /// <summary>
    ///     Changes the domain name — the label the site is served as under its suffix (macOS's
    ///     RenameHostSheet). Nothing is validated here: the bundled CLI's `flock site host` owns the label
    ///     rules and retargets the site's and its worktrees' .env files in one run, so its output —
    ///     refusals included — is what the user reads. Only a site that overrides the name today offers
    ///     going back to the folder name.
    /// </summary>
    private async Task RenameHostAsync(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var global = dns?.Tld is { Length: > 0 } tld ? tld : "test";
        var suffix = site.Tld.Length > 0 ? site.Tld : global; // what it serves under (an old daemon names none)
        var name = new TextBox
        {
            Header = "Domain name", PlaceholderText = site.Name,
            Text = site.SiteHost.Length > 0 ? site.SiteHost : site.Name
        };
        var lands = Ui.Caption("", wrap: false);
        var dialog = new ContentDialog
        {
            Title = $"Change the domain name of {DisplayName(site)}",
            Content = Ui.Column(12,
                name,
                lands,
                Ui.Caption($"The suffix stays .{suffix}; it is chosen on the site's Domain row.", Ui.TertiaryText)),
            PrimaryButtonText = "Rename",
            SecondaryButtonText = site.SiteHost.Length > 0 ? "Use the folder name" : "",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        void UpdateRename()
        {
            // Rename needs a label; whether it is a valid one is the CLI's call, not ours (macOS's canRename).
            dialog.IsPrimaryButtonEnabled = name.Text.Trim().Length > 0;
            lands.Text = $"The site will be served at {(name.Text.Trim() is { Length: > 0 } typed ? typed : "name")}.{suffix}";
        }
        name.TextChanged += (_, _) => UpdateRename();
        UpdateRename();
        var result = await dialog.ShowAsync();
        if (result is not (ContentDialogResult.Primary or ContentDialogResult.Secondary))
        {
            return;
        }
        var arguments = result == ContentDialogResult.Secondary
            ? new List<string> { "site", "host", "--default", site.Name }
            : new List<string> { "site", "host", name.Text.Trim(), site.Name };
        await RenameHostRunAsync(site, arguments);
    }

    /// <summary>
    ///     The CLI run of a domain rename, in its own dialog (macOS's chained ProvisionSheet, T4's
    ///     ProvisionSuffixAsync): a finished run closes the dialog by itself; a run that ended short keeps
    ///     the CLI's own words, and a declined elevation prompt adds the finish step, as for the suffix switch.
    /// </summary>
    private async Task RenameHostRunAsync(Status.Site site, IReadOnlyList<string> arguments)
    {
        var running = Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption("Flock is renaming the domain…", wrap: false));
        var output = Ui.MonoText("", 12);
        var box = Ui.Card(new ScrollViewer { MaxHeight = 160, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, Content = output }, new Thickness(12, 10));
        var declined = Ui.Caption("", Ui.Caution);
        box.Visibility = Visibility.Collapsed;
        declined.Visibility = Visibility.Collapsed;
        var dialog = new ContentDialog
        {
            Title = "Renaming the domain…",
            Content = Ui.Column(10, running, box, declined),
            CloseButtonText = "Hide",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot
        };
        // Closing while the CLI runs is allowed (as in ProvisionSuffixAsync); the late success must not
        // hide a dialog that is already gone.
        var closed = false;
        dialog.Closed += (_, _) => closed = true;
        var run = domainApply.RunAsync(arguments);
        var shown = dialog.ShowAsync().AsTask();
        var (ok, wasDeclined, text) = await run;
        if (ok)
        {
            if (!closed)
            {
                dialog.Hide();
            }
            await shown;
            return;
        }
        // Ended short: the dialog keeps the CLI's own words, as the suffix switch and the chained install do.
        dialog.Title = $"Renaming {site.Name}'s domain didn't finish";
        running.Visibility = Visibility.Collapsed;
        output.Text = text;
        box.Visibility = Visibility.Visible;
        dialog.CloseButtonText = "Close";
        if (wasDeclined)
        {
            declined.Text = "You declined administrator approval. Finish by running `flock site host` in a terminal.";
            declined.Visibility = Visibility.Visible;
        }
        await shown;
    }

    private async Task RemoveDomain(Status.Site site, Status.SiteDomain domain)
    {
        if (await Window.Confirm($"Remove {domain.Host}?", $"{domain.Host} no longer opens {site.Name}.", "Remove", true))
        {
            await Model.Sites.RemoveDomainAsync(site.Name, domain.Entry);
        }
    }

    private async Task Pin(Status.Site site, string tool, string version, string details)
    {
        var shown = tool == "root" ? RootLabel(version) : version;
        if (await Window.Confirm($"Pin {ToolName(tool)} {shown} to {site.Name}?", details + " This changes a file in your project.", "Pin"))
        {
            await Model.Sites.PinAsync(site.Name, tool, version);
        }
    }

    private async Task Unpin(Status.Site site, string tool, string details)
    {
        if (await Window.Confirm($"Unpin {ToolName(tool)} from {site.Name}?", details + " This changes files in your project.", "Unpin"))
        {
            await Model.Sites.UnpinAsync(site.Name, tool);
        }
    }

    private static string ToolName(string tool)
    {
        return tool switch
        {
            "php" => "PHP",
            "root" => "the document root",
            _ => "Node"
        };
    }

    private static MenuFlyoutItem Item(string text, Action click)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => click();
        return item;
    }

    /// <summary>"22.11.0" → "22"; pins and isolations are majors.</summary>
    private static string Major(string version)
    {
        return version.Split('.')[0];
    }
}