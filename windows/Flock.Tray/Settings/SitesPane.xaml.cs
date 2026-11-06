using System.Globalization;
using Flock.Kit;
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
    private readonly bool loaded;

    // Sites whose FrankenPHP restart is in flight.
    private readonly HashSet<string> restarting = [];
    private readonly SitesViewModel viewModel;
    private bool active;
    private CancellationTokenSource? activeLifetime;
    private Task pendingSecure = Task.CompletedTask;

    public SitesPane(SettingsWindow window, RuntimeService model, DeepLinkCoordinator deepLinks)
    {
        Window = window;
        Model = model;
        viewModel = new SitesViewModel(model.Sites);
        this.deepLinks = deepLinks;
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

    public string Title => Strings.Pane_Sites;
    public string Subtitle => Strings.Sites_Subtitle;
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
        if (await Window.Confirm(resolution.Title, resolution.Message, Strings.Sites_Secure))
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
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, site.Name), Localization.Format(Strings.Sites_RemoveSiteDetails, site.Domain),
                Strings.Common_Remove, true))
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
        var name = new TextBox { Header = Strings.Sites_ProxyName, PlaceholderText = "my-app" };
        var upstream = new TextBox { Header = Strings.Sites_Upstream, PlaceholderText = Strings.Sites_UpstreamPlaceholderExample };
        var dialog = new ContentDialog
        {
            Title = Strings.Sites_AddProxyTitle,
            Content = Ui.Column(12,
                Ui.Text(Localization.Format(Strings.Sites_AddProxyDetails, tld)),
                name, upstream),
            PrimaryButtonText = Strings.Common_Add,
            CloseButtonText = Strings.Common_Cancel,
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

        var heading = Ui.Column(2, Ui.Row(8, Ui.Text(proxy.Name, "SubtitleTextBlockStyle", wrap: false), Ui.Badge(Strings.Sites_Proxy, Ui.Tone.Neutral)));
        if (proxy.Url is { } proxyUrl)
        {
            var link = Ui.Link(proxyUrl, () => Model.Open(proxyUrl));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var open = Ui.IconButton(Ui.GlyphOpen, Localization.Format(Strings.Common_OpenTarget, proxy.Url ?? proxy.Domain), () =>
        {
            if (proxy.Url is { } url)
            {
                Model.Open(url);
            }
        });
        open.IsEnabled = proxy.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphDelete, Strings.Sites_RemoveProxy, () => Start(proxy.Name, () => RemoveProxyAsync(proxy))));
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
            server.Children.Add(Ui.Caption(Localization.Format(Strings.Sites_NothingAnswersYet, proxy.UpstreamLabel), Ui.TertiaryText));
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
        var change = Ui.Button(Strings.Sites_Change, () => Start(proxy.Name, () => ChangeUpstreamAsync(proxy)));
        panel.Children.Add(Section(Strings.Sites_Proxy,
            Field(Strings.Sites_Server, server),
            Field("HTTPS", https),
            Field(Strings.Sites_Upstream, Ui.Row(8, upstream, change)),
            Ui.Caption(Localization.Format(Strings.Sites_ProxyFootnote, proxy.Domain), Ui.TertiaryText)));

        var card = Ui.Card(panel, new Thickness(20, 16, 16, 20));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    private async Task ChangeUpstreamAsync(Status.Proxy proxy)
    {
        if (await Window.Ask(Localization.Format(Strings.Sites_ChangeUpstreamTitle, proxy.Name), Strings.Sites_ChangeUpstreamDetails,
                Strings.Sites_UpstreamPlaceholder, Strings.Sites_ChangeAction, proxy.UpstreamLabel) is { } upstream && upstream != proxy.UpstreamLabel)
        {
            await Model.Sites.ProxyAsync(proxy.Name, upstream);
        }
    }

    private async Task RemoveProxyAsync(Status.Proxy proxy)
    {
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, proxy.Name),
                Localization.Format(Strings.Sites_RemoveProxyDetails, proxy.Domain, proxy.Upstream), Strings.Common_Remove, true))
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
        var name = Ui.Row(8, Ui.Text(site.Name, "SubtitleTextBlockStyle", wrap: false));
        if (site.Parked)
        {
            name.Children.Add(Ui.Badge(Strings.Sites_ProjectFolderBadge, Ui.Tone.Neutral));
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

        var open = Ui.IconButton(Ui.GlyphOpen, Localization.Format(Strings.Common_OpenTarget, site.Url ?? site.Domain), () => Model.Open(site));
        open.IsEnabled = site.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, Strings.Common_ShowInExplorer, () => Model.Reveal(site.Path)),
            Ui.IconButton(Ui.GlyphPuzzle, Strings.Sites_PhpExtensions, () => Start("extensions:" + site.Name, () => ShowExtensions(site))), More(site));
        if (!site.Parked)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphDelete, Strings.Sites_RemoveSite, () => Start(site.Name, () => RemoveSiteAsync(site))));
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
        var https = new ToggleSwitch { IsOn = secured, MinWidth = 0 };
        Ui.AutomationName(https, "HTTPS");
        https.Toggled += (_, _) =>
        {
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Secured: https.IsOn), () => row?.SetDraftSecured(https.IsOn)), false);
        };
        var domains = Ui.Column(2);
        foreach (var domain in site.Domains)
        {
            UIElement shown = domain.Problem is { } domainProblem ? Warning($"{domain.Host}: {domainProblem}")
                : domain.Url is { Length: > 0 } url ? DomainLink(domain, url)
                : Ui.Text(domain.Host, wrap: false);
            var target = domain;
            var remove = Ui.IconButton(Ui.GlyphRemove, Localization.Format(Strings.Common_RemoveTarget, domain.Host), () => Start(site.Name, () => RemoveDomain(site, target)));
            remove.Padding = new Thickness(4);
            var domainRow = new Grid { ColumnSpacing = 4 };
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            Grid.SetColumn(remove, 1);
            domainRow.Children.Add(shown);
            domainRow.Children.Add(remove);
            domains.Children.Add(domainRow);
        }
        var addDomain = Ui.Link(Strings.Sites_AddDomain, () => Start(site.Name, () => AddDomain(site)));
        addDomain.Padding = new Thickness(0);
        domains.Children.Add(addDomain);
        var siteSection = Section(Strings.Sites_Site, Field("HTTPS", https), Field(Strings.Sites_Domains, domains));
        if (Model.Status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 } dns)
        {
            siteSection.Children.Add(Ui.Caption(Localization.Format(Strings.Sites_SubdomainsOnlyHere, dns.WildcardProblem), Ui.Caution));
        }
        siteSection.Children.Add(Field(Strings.Sites_Root, RootPicker(site)));
        foreach (var hint in RootHints(site))
        {
            siteSection.Children.Add(hint);
        }
        siteSection.Children.Add(Ui.Caption(
            Strings.Sites_RootFootnote, Ui.TertiaryText));
        panel.Children.Add(siteSection);
        panel.Children.Add(Share(site));

        // Versions: PHP and Node.js, with what pins or isolates them.
        var phpPicker = Picker("PHP", php, site.PhpSource, site.PhpPinned, site.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => new SiteChange(Php: version), site.Name, value => row?.SetDraftPhp(value));
        var nodePicker = Picker("Node", node, site.NodeSource, Major(site.NodePinned), site.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => new SiteChange(Node: version), site.Name, value => row?.SetDraftNode(value));
        var versions = Section(Strings.Versions_Versions, Field("PHP", phpPicker), Field("Node.js", nodePicker));
        foreach (var hint in Hints(site))
        {
            versions.Children.Add(hint);
        }
        versions.Children.Add(Ui.Caption(
            Strings.Sites_VersionsFootnote,
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
        Ui.AutomationName(server, Strings.Sites_Server);

        var section = Section(Strings.Sites_Server, Field(Strings.Sites_RunsOn, server));
        if (franken)
        {
            var octane = (row?.Worker ?? site.Worker) == Status.SiteServer.Octane;
            var mode = new ComboBox { MinWidth = 150 };
            mode.Items.Add(new ComboBoxItem { Content = Strings.Sites_Classic });
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
            Ui.AutomationName(mode, Strings.Debugger_Mode);
            section.Children.Add(Field(Strings.Debugger_Mode, mode));
            if (!site.OctaneAvailable)
            {
                ToolTipService.SetToolTip(mode, Strings.Sites_OctaneTooltip);
            }

            if (site.FrankenPhp is { } state)
            {
                foreach (var line in FrankenPhpState(site, state))
                {
                    section.Children.Add(line);
                }
            }
            var restart = Ui.Button(Strings.Panel_Restart, () => Start(site.Name, () => Restart(site.Name)),
                tooltip: Strings.Sites_RestartFrankenPhpTooltip);
            restart.IsEnabled = site.FrankenPhp is { IsRunning: true } && !restarting.Contains(site.Name);
            var log = site.FrankenPhp is { Log.Length: > 0 } f ? f.Log : $"frankenphp-{site.Name}";
            var buttons = Ui.Row(8, restart, Ui.Button(Strings.Sites_ShowLog, () => App.Current.OpenLog(new LogsViewModel.ServiceSource(log))));
            if (restarting.Contains(site.Name))
            {
                buttons.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
            section.Children.Add(buttons);
        }
        section.Children.Add(Ui.Caption(franken && !site.OctaneAvailable
            ? Strings.Sites_FrankenPhpFootnoteOctane
            : Strings.Sites_FrankenPhpFootnote, Ui.TertiaryText));
        return section;
    }

    /// <summary>The status lines of a site's FrankenPHP: running, downloading, or why php-cgi serves it meanwhile.</summary>
    private static IEnumerable<UIElement> FrankenPhpState(Status.Site site, Status.SiteFrankenPhp state)
    {
        if (state.IsRunning)
        {
            var text = Localization.Format(site.Worker == Status.SiteServer.Octane ? Strings.Sites_FrankenPhpRunningWorker : Strings.Sites_FrankenPhpRunning,
                state.Version, state.PhpVersion);
            yield return Ui.Row(6, Ui.StatusDot(true), Ui.Caption(text, wrap: false));
        }
        else if (state.IsInstalling)
        {
            var downloading = Localization.Format(Strings.Sites_DownloadingFrankenPhp, site.Php);
            yield return Ui.Caption(state.HasProgress ? $"{downloading} {state.Progress.ToString("P0", CultureInfo.CurrentCulture)}" : downloading, wrap: false);
            yield return new ProgressBar
            {
                Minimum = 0,
                Maximum = 1,
                Value = state.HasProgress ? state.Progress : 0,
                IsIndeterminate = !state.HasProgress,
                HorizontalAlignment = HorizontalAlignment.Left,
                Width = 320
            };
            yield return Ui.Caption(Strings.Sites_PhpCgiUntilReady);
        }
        else
        {
            if (state.Problem.Length > 0)
            {
                yield return Warning(state.Problem);
            }
            yield return Ui.Caption(Strings.Sites_PhpCgiMeanwhile);
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
        var follow = pinned.Length > 0 ? Localization.Format(Strings.Sites_UseFileVersion, Path.GetFileName(file), $"{label} {pinned}")
            : defaultVersion.Length > 0 ? Localization.Format(Strings.Sites_DefaultVersion, $"{label} {defaultVersion}")
            : Strings.Sites_Default;
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
            Status.VersionSource.Isolated => Localization.Format(Strings.Sites_VersionIsolated, $"{label} {current}"),
            Status.VersionSource.Project => Localization.Format(Strings.Sites_VersionPinned, $"{label} {pinned}", Path.GetFileName(file)),
            _ => Localization.Format(Strings.Sites_VersionDefault, label)
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
        var follow = site.RootPinnedHere ? Localization.Format(Strings.Sites_UseFileVersion, ".flockrc", RootLabel(site.RootPinned))
            : site.RootSource != Status.VersionSource.Default ? Strings.Sites_RootAutomatic
            : site.RootState == Status.DocumentRootState.NotFound ? Strings.Sites_RootAutomaticNone
            : Localization.Format(Strings.Sites_RootAutomaticFound, RootLabel(site.Root));
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
        picker.Items.Add(Strings.Sites_ChooseFolder);
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
        Ui.AutomationName(picker, Strings.Sites_DocumentRoot);
        ToolTipService.SetToolTip(picker, site.RootSource switch
        {
            Status.VersionSource.Isolated => Localization.Format(Strings.Sites_RootIsolated, RootLabel(site.Root)),
            Status.VersionSource.Project => Localization.Format(Strings.Sites_RootPinned, RootLabel(site.Root)),
            _ => Strings.Sites_RootDefault
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
            await Ui.Inform(Window.XamlRoot, Strings.Sites_ChooseFolderInProject,
                Localization.Format(Strings.Sites_ChooseFolderInProjectDetails, folder, site.Path));
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
                Strings.Sites_RootNotFound,
                null, null);
        }
        else if (site.RootState == Status.DocumentRootState.Missing)
        {
            yield return Hint(Localization.Format(Strings.Sites_RootMissing, site.Root), null, null);
        }
        else if (site.RootState == Status.DocumentRootState.Empty)
        {
            yield return Ui.Caption(Localization.Format(Strings.Sites_RootNoIndex, RootLabel(site.Root)), Ui.TertiaryText);
        }
        else if (site.RootSource == Status.VersionSource.Isolated && site.RootPinnedHere)
        {
            yield return Hint(Localization.Format(Strings.Sites_RootShadowed, RootLabel(site.Root), RootLabel(site.RootPinned)),
                Localization.Format(Strings.Sites_UseFile, ".flockrc"), () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Root: ""))));
        }
    }

    /// <summary>"src/www", or "Project folder" for ".".</summary>
    private static string RootLabel(string root)
    {
        return root == "." ? Strings.Sites_ProjectFolderBadge : root;
    }

    /// <summary>Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.</summary>
    private IEnumerable<UIElement> Hints(Status.Site site)
    {
        if (site.PhpSource == Status.VersionSource.Isolated && site.PhpPinned.Length > 0 && site.PhpPinned != site.Php)
        {
            yield return Hint(Localization.Format(Strings.Sites_VersionShadowed, $"PHP {site.Php}", site.PhpPinned, Path.GetFileName(site.PhpFile)),
                Localization.Format(Strings.Sites_UseFile, Path.GetFileName(site.PhpFile)), () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Php: ""))));
        }
        if (site.NodeSource == Status.VersionSource.Isolated && site.NodePinned.Length > 0 && Major(site.NodePinned) != Major(site.Node ?? ""))
        {
            yield return Hint(Localization.Format(Strings.Sites_VersionShadowed, $"Node {site.Node}", site.NodePinned, Path.GetFileName(site.NodeFile)),
                Localization.Format(Strings.Sites_UseFile, Path.GetFileName(site.NodeFile)), () => Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Node: ""))));
        }
        if (site.NodeShadowed.Length > 0)
        {
            yield return Hint(Localization.Format(Strings.Sites_NodeWins, site.NodePinned, site.NodeShadowed), null, null);
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
        var link = Ui.Link(missing ? Strings.Sites_BoostAdd : Strings.Sites_BoostUpdate, () => Start(site.Name, () => AddBoostGuideline(site, missing)));
        link.Padding = new Thickness(0);
        return Ui.Column(2, Ui.Caption(missing
            ? Strings.Sites_BoostMissing
            : Strings.Sites_BoostOutdated), link);
    }

    private async Task AddBoostGuideline(Status.Site site, bool missing)
    {
        var title = Localization.Format(missing ? Strings.Sites_BoostAddQuestion : Strings.Sites_BoostUpdateQuestion, site.Name);
        var details =
            Localization.Format(Strings.Sites_BoostDetails, site.Path);
        if (await Window.Confirm(title, details, missing ? Strings.Common_Add : Strings.Common_Update))
        {
            await Model.Sites.AddBoostGuidelineAsync(site.Name);
        }
    }

    private HyperlinkButton DomainLink(Status.SiteDomain domain, string url)
    {
        var link = Ui.Link(domain.Alias ? Localization.Format(Strings.Sites_Alias, domain.Host) : domain.Host, () => Model.Open(url));
        link.Padding = new Thickness(0);
        ToolTipService.SetToolTip(link, Localization.Format(Strings.Common_OpenTarget, url));
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
            Title = Localization.Format(Strings.Sites_ExtensionsTitle, site.Name),
            Content = Ui.Column(10, scroller,
                Ui.Caption(Strings.Sites_ExtensionsFootnote)),
            CloseButtonText = Strings.Common_Done,
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
                    list.Children.Add(Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption(Strings.Common_Loading)));
                }
                return;
            }
            list.Children.Add(Ui.Caption(report.Laravel ? Localization.Format(Strings.Sites_ExtensionsLaravel, report.Php) : $"PHP {report.Php}"));
            if (report.Extensions.Count == 0)
            {
                list.Children.Add(Ui.Text(Strings.Sites_ExtensionsNone, foreground: Ui.SecondaryText));
            }
            foreach (var ext in report.Extensions)
            {
                var (glyph, color) = !ext.Available ? (Ui.GlyphWarning, Ui.Critical) : ext.Declared ? (Ui.GlyphPuzzle, Ui.Success) : (Ui.GlyphInfo, Ui.Caution);
                var detail = ext.Declared ? Localization.Format(Strings.Sites_ExtensionRequiredBy, string.Join(", ", ext.RequiredBy))
                    : Localization.Format(Strings.Sites_ExtensionUsed, ext.Hint);
                if (!ext.Available)
                {
                    detail += " — " + Localization.Format(Strings.Sites_ExtensionMissing, report.Php);
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
                    actions.Children.Add(Ui.Link(Strings.Common_Cancel, () =>
                    {
                        confirming = null;
                        Render();
                    }));
                    actions.Children.Add(Ui.Button(Strings.Sites_Require, () => Start("extensions:" + site.Name, () => Add(name)), true,
                        tooltip: Localization.Format(Strings.Sites_RequireTooltip, name, site.Path)));
                }
                else if (!ext.Declared)
                {
                    var name = ext.Name;
                    var add = Ui.Button(Strings.Sites_AddToComposer, () =>
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

    internal async Task AddDomain(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var depth = dns?.MaxDepth ?? 4;
        var details = dns is { Wildcard: true }
            ? Localization.Plural(Strings.ResourceManager, "Sites_AddDomainWildcard", depth, site.Domain, $"{site.Name}-2.{dns.Tld}")
            : dns?.WildcardProblem is { Length: > 0 } p
                ? Localization.Format(Strings.Sites_AddDomainNoWildcardBecause, p, $"{site.Name}-2.{dns.Tld}")
                : Localization.Format(Strings.Sites_AddDomainNoWildcard, $"{site.Name}-2.{dns?.Tld ?? "test"}");
        if (await Window.Ask(Localization.Format(Strings.Sites_AddDomainTitle, site.Name), details,
                Localization.Format(Strings.Sites_AddDomainPlaceholder, $"{site.Name}-2.{dns?.Tld ?? "test"}"), Strings.Common_Add) is { } domain)
        {
            await Model.Sites.AddDomainAsync(site.Name, domain);
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

        // Pin and unpin items appear only when there is something to pin or unpin (the menu always has the domain items).
        if (site.Php.Length > 0 && !(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere))
        {
            menu.Items.Add(Item(Localization.Format(Strings.Sites_PinPhpItem, site.Php), () => Start(site.Name, () => Pin(site, "php", site.Php,
                Localization.Format(Strings.Sites_PinPhpDetails, site.Php, site.Path)))));
        }
        if (nodeMajor.Length > 0 && !(site.NodeSource == Status.VersionSource.Project && site.NodePinnedHere))
        {
            menu.Items.Add(Item(Localization.Format(Strings.Sites_PinNodeItem, nodeMajor), () => Start(site.Name, () => Pin(site, "node", nodeMajor,
                Localization.Format(Strings.Sites_PinNodeDetails, nodeMajor, nodeFile, site.Path)))));
        }
        if (site.RootState is "" or Status.DocumentRootState.Empty && site.RootSource != Status.VersionSource.Project)
        {
            menu.Items.Add(Item(Localization.Format(Strings.Sites_PinRootItem, RootLabel(site.Root)), () => Start(site.Name, () => Pin(site, "root", site.Root,
                Localization.Format(Strings.Sites_PinRootDetails, site.Root, site.Path, RootLabel(site.Root))))));
        }
        if (site.PhpPinnedHere || site.NodePinnedHere || site.RootPinnedHere)
        {
            if (menu.Items.Count > 0)
            {
                menu.Items.Add(new MenuFlyoutSeparator());
            }
            if (site.PhpPinnedHere)
            {
                menu.Items.Add(Item(Localization.Format(Strings.Sites_UnpinPhpItem, Path.GetFileName(site.PhpFile)), () => Start(site.Name, () => Unpin(site, "php",
                    Localization.Format(Strings.Sites_UnpinPhpDetails, site.PhpFile)))));
            }
            if (site.NodePinnedHere)
            {
                menu.Items.Add(Item(Localization.Format(Strings.Sites_UnpinNodeItem, Path.GetFileName(site.NodeFile)), () => Start(site.Name, () => Unpin(site, "node",
                    Localization.Format(Strings.Sites_UnpinNodeDetails, site.Path)))));
            }
            if (site.RootPinnedHere)
            {
                menu.Items.Add(Item(Strings.Sites_UnpinRootItem, () => Start(site.Name, () => Unpin(site, "root",
                    Localization.Format(Strings.Sites_UnpinRootDetails, site.Path)))));
            }
        }

        var button = Ui.IconButton(Ui.GlyphMore, Strings.Sites_PinMenuTooltip, () => { });
        button.Flyout = menu;
        button.IsEnabled = menu.Items.Count > 0;
        return button;
    }

    private async Task RemoveDomain(Status.Site site, Status.SiteDomain domain)
    {
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, domain.Host), Localization.Format(Strings.Sites_RemoveDomainDetails, domain.Host, site.Name),
                Strings.Common_Remove, true))
        {
            await Model.Sites.RemoveDomainAsync(site.Name, domain.Entry);
        }
    }

    private async Task Pin(Status.Site site, string tool, string version, string details)
    {
        var shown = tool == "root" ? RootLabel(version) : version;
        var question = tool switch
        {
            "php" => Strings.Sites_PinPhpQuestion,
            "root" => Strings.Sites_PinRootQuestion,
            _ => Strings.Sites_PinNodeQuestion
        };
        if (await Window.Confirm(Localization.Format(question, shown, site.Name), details + " " + Strings.Sites_ChangesAFile, Strings.Sites_Pin))
        {
            await Model.Sites.PinAsync(site.Name, tool, version);
        }
    }

    private async Task Unpin(Status.Site site, string tool, string details)
    {
        var question = tool switch
        {
            "php" => Strings.Sites_UnpinPhpQuestion,
            "root" => Strings.Sites_UnpinRootQuestion,
            _ => Strings.Sites_UnpinNodeQuestion
        };
        if (await Window.Confirm(Localization.Format(question, site.Name), details + " " + Strings.Sites_ChangesFiles, Strings.Sites_Unpin))
        {
            await Model.Sites.UnpinAsync(site.Name, tool);
        }
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