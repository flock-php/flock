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
///     php-cgi or on FrankenPHP (classic, or Laravel Octane's worker mode), or is a Node.js app whose
///     npm run dev Flock runs. Proxies (`flock proxy`) follow the sites in a group of their own.
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
    // Unsaved Command edits per site: a status refresh rebuilds the detail pane, and keeps them.
    private readonly Dictionary<string, string> commandDrafts = [];
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

        var serverRow = Ui.Row(6, Ui.StatusDot(proxy.Running), Ui.Text(proxy.ServerLabel, wrap: false));
        if (proxy.Command is { } command)
        {
            serverRow.Children.Add(proxy.Started
                ? Ui.Button("Stop", () => Start(proxy.Name, () => Model.Sites.StopProxyAsync(proxy.Name)))
                : Ui.Button("Start", () => Start(proxy.Name, () => Model.Sites.StartProxyAsync(proxy.Name))));
            serverRow.Children.Add(Ui.Button("Show Log", () => App.Current.OpenLog(new LogsViewModel.ServiceSource(command.Log))));
        }
        var server = Ui.Column(2, serverRow);
        if (proxy.Command is { State: Status.ProxyCommandState.Failed, Problem.Length: > 0 } failed)
        {
            server.Children.Add(Ui.Caption(failed.Problem, Ui.TertiaryText));
        }
        else if (!proxy.Running)
        {
            server.Children.Add(Ui.Caption(proxy.Command?.State == Status.ProxyCommandState.Starting
                ? $"Waiting for {proxy.UpstreamLabel} to answer…"
                : $"Nothing answers at {proxy.UpstreamLabel} yet.", Ui.TertiaryText));
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
        var commandText = Ui.MonoText(proxy.Command?.Command is { Length: > 0 } configured ? configured : "None");
        commandText.VerticalAlignment = VerticalAlignment.Center;
        var fields = new List<UIElement>
        {
            Field("Server", server),
            Field("HTTPS", https),
            Field("Upstream", Ui.Row(8, upstream, change)),
            Field("Command", Ui.Row(8, commandText, Ui.Button("Change…", () => Start(proxy.Name, () => ChangeProxyCommandAsync(proxy))))),
        };
        if (proxy.Command is { } runs)
        {
            var dir = Ui.MonoText(runs.Dir);
            dir.VerticalAlignment = VerticalAlignment.Center;
            fields.Add(Field("Folder", Ui.Row(8, dir, Ui.Button("Change…", () => Start(proxy.Name, () => ChangeProxyFolderAsync(proxy, runs))))));
            var stop = Ui.MonoText(runs.StopCommand is { Length: > 0 } stopCommand ? stopCommand : "None");
            stop.VerticalAlignment = VerticalAlignment.Center;
            fields.Add(Field("Stop command", Ui.Row(8, stop, Ui.Button("Change…", () => Start(proxy.Name, () => ChangeProxyStopCommandAsync(proxy, runs))))));
        }
        fields.Add(Ui.Caption(proxy.Command is null
                ? $"Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Start the server as usual, or give the proxy a command so Flock starts it. Until it answers, {proxy.Domain} shows a page saying so."
                : "Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Flock runs the command when you click Start, with Flock's PHP and Node.js on PATH and PORT set, and keeps it running until you click Stop or quit Flock. A stop command is only for a command that starts a server in the background and exits.",
            Ui.TertiaryText));
        panel.Children.Add(Section("Proxy", fields.ToArray()));

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

    private async Task ChangeProxyCommandAsync(Status.Proxy proxy)
    {
        if (await Window.Ask($"The command that starts {proxy.Name}",
                "Flock runs it through the shell when you click Start, and keeps it running until you click Stop. Leave it empty for none.",
                "e.g. npm run dev", "Save", proxy.Command?.Command ?? "") is { } command && command.Trim() != (proxy.Command?.Command ?? ""))
        {
            var dir = proxy.Command?.Dir is { Length: > 0 } configured ? configured : Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            await Model.Sites.SetProxyCommandAsync(proxy.Name, command.Trim(), dir, proxy.Command?.StopCommand ?? "");
        }
    }

    private async Task ChangeProxyFolderAsync(Status.Proxy proxy, Status.ProxyCommand command)
    {
        if (await Window.Ask($"The folder {proxy.Name}'s command runs in", "A full path, e.g. C:\\Code\\softpack.", "Folder", "Save", command.Dir) is { Length: > 0 } dir && dir.Trim() != command.Dir)
        {
            await Model.Sites.SetProxyCommandAsync(proxy.Name, command.Command, dir.Trim(), command.StopCommand);
        }
    }

    private async Task ChangeProxyStopCommandAsync(Status.Proxy proxy, Status.ProxyCommand command)
    {
        if (await Window.Ask($"The command that stops {proxy.Name}",
                "Only for a command that starts a server in the background and exits (docker compose up -d): Stop runs this instead. Leave it empty for none.",
                "e.g. docker compose stop", "Save", command.StopCommand) is { } stop && stop.Trim() != command.StopCommand)
        {
            await Model.Sites.SetProxyCommandAsync(proxy.Name, command.Command, command.Dir, stop.Trim());
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
        // A Node.js app's dev server serves everything: no document root, PHP version or extensions.
        var isNode = (row?.Server ?? site.Server) == Status.SiteServer.Node;
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
            var link = Ui.Link(siteUrl, () => Model.Open(site));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var path = Ui.Caption(site.Path, Ui.TertiaryText, false);
        ToolTipService.SetToolTip(path, site.Path);
        heading.Children.Add(path);

        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {site.Url ?? site.Domain}", () => Model.Open(site));
        open.IsEnabled = site.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Model.Reveal(site.Path)));
        if (!isNode)
        {
            actions.Children.Add(Ui.IconButton(Ui.GlyphPuzzle, "PHP extensions", () => Start("extensions:" + site.Name, () => ShowExtensions(site))));
        }
        actions.Children.Add(More(site, isNode));
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
            var remove = Ui.IconButton(Ui.GlyphRemove, $"Remove {domain.Host}", () => Start(site.Name, () => RemoveDomain(site, target)));
            remove.Padding = new Thickness(4);
            var domainRow = new Grid { ColumnSpacing = 4 };
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            domainRow.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            Grid.SetColumn(remove, 1);
            domainRow.Children.Add(shown);
            domainRow.Children.Add(remove);
            domains.Children.Add(domainRow);
        }
        var addDomain = Ui.Link("Add Domain…", () => Start(site.Name, () => AddDomain(site)));
        addDomain.Padding = new Thickness(0);
        domains.Children.Add(addDomain);
        var siteSection = Section("Site", Field("HTTPS", https), Field("Domains", domains));
        if (Model.Status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 } dns)
        {
            siteSection.Children.Add(Ui.Caption($"Subdomains only work when you add them here: {dns.WildcardProblem}.", Ui.Caution));
        }
        if (!isNode)
        {
            siteSection.Children.Add(Field("Root", RootPicker(site)));
            foreach (var hint in RootHints(site))
            {
                siteSection.Children.Add(hint);
            }
            siteSection.Children.Add(Ui.Caption(
                "The folder Flock serves the site from. One picked here applies on this machine only; pin it (…) to write it into .flockrc for everyone.", Ui.TertiaryText));
        }
        panel.Children.Add(siteSection);
        panel.Children.Add(Share(site));

        // Versions: PHP and Node.js, with what pins or isolates them.
        var phpPicker = Picker("PHP", php, site.PhpSource, site.PhpPinned, site.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => new SiteChange(Php: version), site.Name, value => row?.SetDraftPhp(value));
        var nodePicker = Picker("Node", node, site.NodeSource, Major(site.NodePinned), site.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => new SiteChange(Node: version), site.Name, value => row?.SetDraftNode(value));
        var versions = isNode ? Section("Versions", Field("Node.js", nodePicker)) : Section("Versions", Field("PHP", phpPicker), Field("Node.js", nodePicker));
        foreach (var hint in Hints(site, isNode))
        {
            versions.Children.Add(hint);
        }
        versions.Children.Add(Ui.Caption(isNode
            ? "A version picked here applies on this machine only, and restarts the dev server. To set it for everyone working on the project, pin it (…) into .nvmrc."
            : "A version picked here applies on this machine only. To set it for everyone working on the project, pin it (…): PHP goes into .flockrc, Node.js into .nvmrc.",
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
    ///     What serves the site: php-cgi, FrankenPHP (classic or Laravel Octane's worker mode) with
    ///     FrankenPHP's state, Restart and its log, or a Node.js app's dev server.
    /// </summary>
    private StackPanel Server(Status.Site site, SiteRowViewModel? row = null)
    {
        string[] servers = [Status.SiteServer.FastCgi, Status.SiteServer.FrankenPhp, Status.SiteServer.Node];
        var current = row?.Server ?? site.Server;
        var selected = Math.Max(0, Array.IndexOf(servers, current));
        var franken = current == Status.SiteServer.FrankenPhp;
        var isNode = current == Status.SiteServer.Node;
        var server = new ComboBox { MinWidth = 140 };
        server.Items.Add(new ComboBoxItem { Content = "PHP-CGI" });
        // FrankenPHP needs PHP 8.2; a site already on it keeps the option (the daemon explains the fallback).
        server.Items.Add(new ComboBoxItem { Content = "FrankenPHP", IsEnabled = franken || SupportsFrankenPhp(site.Php) });
        server.Items.Add(new ComboBoxItem { Content = "Node.js (npm run dev)" });
        server.SelectedIndex = selected;
        server.SelectionChanged += (_, _) =>
        {
            if (server.SelectedIndex < 0 || server.SelectedIndex == selected)
            {
                return;
            }
            var change = servers[server.SelectedIndex];
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Server: change), () => row?.SetDraftServer(change)), false);
        };
        Ui.AutomationName(server, "Server");

        var section = Section("Server", Field("Runs on", server));
        if (isNode)
        {
            foreach (var line in NodeServerFields(site))
            {
                section.Children.Add(line);
            }
            return section;
        }
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
            : "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-CGI. A Node.js app (Next.js, Nuxt, Vite): Flock runs its npm run dev.", Ui.TertiaryText));
        return section;
    }

    /// <summary>
    ///     A Node.js site's dev server: whether it runs, on which Node.js and port, the command Flock
    ///     runs (and the site's own one), its restart and its log.
    /// </summary>
    private IEnumerable<UIElement> NodeServerFields(Status.Site site)
    {
        var ns = site.NodeServer;
        var dot = Ui.StatusDot(ns is { IsRunning: true });
        if (ns is { IsStarting: true })
        {
            dot.Fill = Ui.Caution;
        }
        else if (ns is { IsFailed: true })
        {
            dot.Fill = Ui.Critical;
        }
        var state = Ui.Column(2, Ui.Row(6, dot, Ui.Text(NodeStateText(ns), wrap: false)));
        if (ns is { IsFailed: true, Problem.Length: > 0 })
        {
            state.Children.Add(Warning(ns.Problem));
        }
        if (ns is { Command.Length: > 0 })
        {
            var running = Ui.MonoText(ns.Command, 12);
            running.IsTextSelectionEnabled = true;
            running.TextWrapping = TextWrapping.Wrap;
            state.Children.Add(running);
        }
        yield return Field("Dev server", state);

        // The site's own command; empty: npm run dev.
        var saved = ns is { Custom: true } ? (ns.Configured.Length > 0 ? ns.Configured : ns.Command) : "";
        var command = new TextBox
        {
            PlaceholderText = "npm run dev",
            Text = commandDrafts.TryGetValue(site.Name, out var draft) ? draft : saved,
            MinWidth = 260,
            FontFamily = Ui.Mono,
        };
        Ui.AutomationName(command, "Command");
        command.TextChanged += (_, _) =>
        {
            if (command.Text == saved)
            {
                commandDrafts.Remove(site.Name);
            }
            else
            {
                commandDrafts[site.Name] = command.Text;
            }
        };
        void Save()
        {
            var text = command.Text.Trim();
            commandDrafts.Remove(site.Name);
            if (text != saved.Trim())
            {
                Start(site.Name, () => Model.Sites.UpdateAsync(site.Name, new SiteChange(Command: text)));
            }
        }
        command.KeyDown += (_, e) =>
        {
            if (e.Key == Windows.System.VirtualKey.Enter)
            {
                Save();
            }
        };
        var save = Ui.Button("Save", Save, tooltip: "Run this command instead of npm run dev; it restarts the dev server");
        yield return Field("Command", Ui.Column(4, Ui.Row(8, command, save),
            Ui.Caption("Leave empty for npm run dev. {port} is replaced by the dev server's port; PORT is set too.", Ui.TertiaryText)));

        var restart = Ui.Button("Restart", () => Start(site.Name, () => Restart(site.Name)),
            tooltip: ns is { Custom: true } ? "Restart the dev server" : $"Restart npm run {ns?.Script ?? "dev"}");
        restart.IsEnabled = ns is { IsUnavailable: false } && !restarting.Contains(site.Name);
        var log = ns is { Log.Length: > 0 } n ? n.Log : $"node-{site.Name}";
        var buttons = Ui.Row(8, restart, Ui.Button("Show Log", () => App.Current.OpenLog(new LogsViewModel.ServiceSource(log))));
        if (restarting.Contains(site.Name))
        {
            buttons.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
        }
        yield return buttons;
        yield return Ui.Caption(
            $"Flock runs the dev server in the project with the Node.js under Versions on port {(ns is { Port: > 0 } ? ns.Port.ToString(System.Globalization.CultureInfo.CurrentCulture) : "…")}, restarts it when it exits, and serves the site's domains and HTTPS from it, hot reload included. A Node.js version picked or pinned here restarts it.",
            Ui.TertiaryText);
    }

    private static string NodeStateText(Status.SiteNodeServer? ns) => ns switch
    {
        null => "Starting…",
        { IsRunning: true } => $"Running on Node.js {ns.Version} — port {ns.Port}",
        { IsStarting: true } => ns.Version.Length > 0 ? $"Starting on Node.js {ns.Version}…" : "Starting…",
        { IsUnavailable: true } => "Node.js isn't installed",
        { IsFailed: true } => "Not running",
        _ => "Unknown",
    };

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
    private IEnumerable<UIElement> Hints(Status.Site site, bool isNode = false)
    {
        if (!isNode && site.PhpSource == Status.VersionSource.Isolated && site.PhpPinned.Length > 0 && site.PhpPinned != site.Php)
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

    internal async Task AddDomain(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var depth = dns?.MaxDepth ?? 4;
        var details = dns is { Wildcard: true }
            ? $"Every subdomain up to {depth} levels below {site.Domain} already works on this PC. Add one here so it also works where wildcard DNS doesn't (it's written to the hosts file), or add an alias like {site.Name}-2.{dns.Tld}."
            : $"Wildcard subdomains don't work on this PC{(dns?.WildcardProblem is { Length: > 0 } p ? $" ({p})" : "")}, so add each one you use: it's written to the hosts file. A full name like {site.Name}-2.{dns?.Tld ?? "test"} adds an alias.";
        if (await Window.Ask($"Add a domain to {site.Name}", details, $"tenant1, a.b or {site.Name}-2.{dns?.Tld ?? "test"}", "Add") is { } domain)
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

    private Button More(Status.Site site, bool isNode = false)
    {
        var menu = new MenuFlyout();
        var nodeMajor = Major(site.Node ?? "");
        var nodeFile = site.NodePinnedHere ? Path.GetFileName(site.NodeFile) : ".nvmrc";

        // Pin and unpin items appear only when there is something to pin or unpin (the menu always has the domain items).
        // A Node.js app has no PHP version or document root to pin (unpinning stays offered).
        if (!isNode && site.Php.Length > 0 && !(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere))
        {
            menu.Items.Add(Item($"Pin PHP {site.Php} to Project…", () => Start(site.Name, () => Pin(site, "php", site.Php,
                $"Writes php={site.Php} to .flockrc in {site.Path}. Commit it, and everyone using Flock on this project gets PHP {site.Php}."))));
        }
        if (nodeMajor.Length > 0 && !(site.NodeSource == Status.VersionSource.Project && site.NodePinnedHere))
        {
            menu.Items.Add(Item($"Pin Node {nodeMajor} to Project…", () => Start(site.Name, () => Pin(site, "node", nodeMajor,
                $"Writes {nodeMajor} to {nodeFile} in {site.Path}. Commit it, and Flock, nvm and other Node tools use Node {nodeMajor} for this project."))));
        }
        if (!isNode && site.RootState is "" or Status.DocumentRootState.Empty && site.RootSource != Status.VersionSource.Project)
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

        var button = Ui.IconButton(Ui.GlyphMore, "Pin versions and the document root to the project", () => { });
        button.Flyout = menu;
        button.IsEnabled = menu.Items.Count > 0;
        return button;
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