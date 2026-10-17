using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Grouped list of services with a detail column. Shows the installed
/// services or every service Flock offers. PostgreSQL (its versions) and
/// the catalog services (MySQL, Redis, Meilisearch, ...) are installed and updated from their
/// details (see ServicesPane.Installs.cs and ServicesPane.Catalog.cs).
/// </summary>
internal sealed partial class ServicesPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    private static readonly string[] CategoryOrder = ["Core", "Database", "Cache & Queue", "Search", "Storage", "Mail", "Debugging"];
    // Show every service, or only the installed ones (and those installing). Kept while the app runs.
    private static bool showAll = true;
    private string? selection;

    public override string Title => "Services";
    public override string Subtitle => "Databases, caches, search and the tools around your sites. Everything listens on 127.0.0.1 only.";

    public override void Shown(CancellationToken hidden)
    {
        Store.WatchesServices = true;
        hidden.Register(() => Store.WatchesServices = false);
        _ = Poll(hidden);
    }

    /// <summary>Shows a service's details, e.g. to install it.</summary>
    public void Select(string id)
    {
        selection = id;
        Rerender();
    }

    /// <summary>
    /// Asks about a pending flock://install link (App.Open) once the services and versions are
    /// loaded: nothing installs until the user confirms here.
    /// </summary>
    private void ConfirmPendingLink()
    {
        if (Model.PendingInstall is not { } link || link.Resolve(Store.Services, Store.Catalog, Store.Postgres) is not { } request)
        {
            return;
        }
        Model.PendingInstall = null;
        // Not while building the pane: a dialog needs the window's content in place.
        Window.DispatcherQueue.TryEnqueue(async () =>
        {
            if (request.Target is not { } target)
            {
                await Ui.Inform(Window.Content.XamlRoot, request.Title, request.Message);
                return;
            }
            if (!await Window.Confirm(request.Title, request.Message, "Install"))
            {
                return;
            }
            var line = target.Line;
            if (target.Service == "postgres")
            {
                await Run(line, "Installing…", c => c.InstallPostgresAsync(line));
            }
            else
            {
                await RunCatalog(target.Service, c => c.InstallCatalogServiceAsync(target.Service, line));
            }
        });
    }

    public override UIElement Build()
    {
        var all = Store.Services;
        if (all.Count == 0)
        {
            return Ui.EmptyState(Ui.GlyphServer, "No services");
        }
        // With "Installed", also the selected service, so showing one from elsewhere still works.
        var services = showAll ? all : all.Where(s => !s.IsOnRequest || s.Installed || WorkText(s) is not null || CatalogFailed(s) || s.Id == selection).ToList();
        var selected = services.FirstOrDefault(s => s.Id == selection) ?? services[0];
        ConfirmPendingLink();

        var list = new StackPanel { Spacing = 4 };
        var filter = new SelectorBar();
        var installedItem = new SelectorBarItem { Text = "Installed" };
        var allItem = new SelectorBarItem { Text = "All" };
        filter.Items.Add(installedItem);
        filter.Items.Add(allItem);
        filter.SelectedItem = showAll ? allItem : installedItem;
        filter.SelectionChanged += (_, _) =>
        {
            var wantAll = filter.SelectedItem == allItem;
            if (wantAll != showAll)
            {
                showAll = wantAll;
                Rerender();
            }
        };
        ToolTipService.SetToolTip(filter, "Show only installed services, or every service Flock offers");
        filter.Margin = new Thickness(-4, -8, 0, 4);
        list.Children.Add(filter);
        var present = services.Select(s => s.Category).Distinct().ToList();
        var categories = CategoryOrder.Where(present.Contains).Concat(present.Except(CategoryOrder).Order()).ToList();
        foreach (var category in categories)
        {
            var heading = Ui.Text(category, "BodyStrongTextBlockStyle");
            heading.Margin = new Thickness(1, list.Children.Count == 0 ? 0 : 16, 0, 4);
            list.Children.Add(heading);
            foreach (var service in services.Where(s => s.Category == category))
            {
                list.Children.Add(Row(service, service.Id == selected.Id));
            }
        }
        if (services.Count < all.Count)
        {
            var more = Ui.Link("More services…", () =>
            {
                showAll = true;
                Rerender();
            });
            ToolTipService.SetToolTip(more, "Show every service Flock offers: MySQL, Redis, Meilisearch and more");
            more.Margin = new Thickness(0, 12, 0, 0);
            list.Children.Add(more);
        }

        // The details get the wider share: PostgreSQL's versions and the install progress live there.
        var grid = new Grid { ColumnSpacing = 16 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star), MinWidth = 220 });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1.15, GridUnitType.Star), MinWidth = 300, MaxWidth = 460 });
        var detail = Detail(selected);
        Grid.SetColumn(detail, 1);
        grid.Children.Add(list);
        grid.Children.Add(detail);
        return grid;
    }

    private Button Row(Service service, bool isSelected)
    {
        var info = Ui.Row(12, Ui.StatusDot(service.Running), Ui.Column(1, Ui.Text(service.Name, "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(DetailText(service), wrap: false)));
        if (service.Problem.Length > 0)
        {
            var warning = Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution);
            ToolTipService.SetToolTip(warning, service.Problem);
            info.Children.Add(warning);
        }
        if (UpdateHint(service) is { } update)
        {
            var hint = Ui.Icon(Ui.GlyphDownload, 12, Ui.Accent);
            ToolTipService.SetToolTip(hint, update);
            Ui.AutomationName(hint, update);
            info.Children.Add(hint);
        }

        var actions = Ui.Row(8);
        var working = WorkText(service);
        if (working is not null)
        {
            var ring = new ProgressRing { Width = 16, Height = 16 };
            ToolTipService.SetToolTip(ring, working);
            actions.Children.Add(ring);
        }
        if (service.Installed)
        {
            if (service.HasConfigurablePort)
            {
                actions.Children.Add(Ui.Button("Port…", () => _ = EditPort(service)));
            }
            if (service.Stoppable)
            {
                var toggle = Ui.Button(service.Running ? "Stop" : "Start", async () =>
                {
                    if (!service.Running)
                    {
                        await Store.PerformOnServicesAsync(c => c.StartServiceAsync(service.Id));
                    }
                    else if (await Window.Confirm($"Stop {service.Name}?", "Apps that use it can't connect until you start it again.", "Stop", destructive: true))
                    {
                        await Store.PerformOnServicesAsync(c => c.StopServiceAsync(service.Id));
                    }
                });
                // Not while PostgreSQL switches or a service installs: the daemon (re)starts them itself.
                toggle.IsEnabled = !Store.IsBusy && !(service.Id == "postgres" ? PostgresWorking : CatalogWorking(service.Id));
                actions.Children.Add(toggle);
            }
        }
        else if (working is not null)
        {
            actions.Children.Add(Ui.Caption(working, wrap: false));
        }
        else if (!service.Available)
        {
            actions.Children.Add(Ui.Caption("Not available", wrap: false));
        }
        else if (IsInstallable(service))
        {
            // Opens the details, where it is installed (with a version choice for PostgreSQL).
            actions.Children.Add(Ui.Button("Install…", () => Select(service.Id), glyph: Ui.GlyphDownload));
        }
        else
        {
            actions.Children.Add(Ui.Caption("Not installed", wrap: false));
        }

        // A card that selects the service for the detail column.
        var card = new Button
        {
            Content = Ui.Spread(info, actions),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Padding = new Thickness(16, 10, 12, 10),
            Background = Ui.Brush(isSelected ? "SubtleFillColorSecondaryBrush" : "CardBackgroundFillColorDefaultBrush"),
            BorderBrush = isSelected ? Ui.Accent : Ui.Brush("CardStrokeColorDefaultBrush"),
            Opacity = service.Available ? 1 : 0.6,
        };
        if (!service.Available && service.Unavailable.Length > 0)
        {
            ToolTipService.SetToolTip(card, service.Unavailable);
        }
        card.Click += (_, _) => Select(service.Id);
        return card;
    }

    private static string DetailText(Service service)
    {
        var parts = new List<string>();
        if (service.Version.Length > 0)
        {
            parts.Add(service.Version);
        }
        if (service.Port > 0)
        {
            parts.Add($"Port {service.Port}");
        }
        if (!service.Running && service.Installed)
        {
            parts.Add("Stopped");
        }
        return string.Join(" · ", parts);
    }

    private Border Detail(Service service)
    {
        var panel = new StackPanel { Spacing = 14 };
        panel.Children.Add(Ui.Row(8, Ui.StatusDot(service.Running), Ui.Text(service.Name, "SubtitleTextBlockStyle")));
        var card = Ui.Card(panel, new Thickness(16));
        card.VerticalAlignment = VerticalAlignment.Top;

        // A service Flock can't run here has nothing to configure or connect to: just say why.
        if (!service.Available)
        {
            panel.Children.Add(Hero(Ui.GlyphServer, "Not available on Windows",
                service.Unavailable.Length > 0 ? service.Unavailable : $"Flock can't run {service.Name} on Windows."));
            return card;
        }

        // Why it isn't working; the stopped DNS server's explanation below already includes it.
        if (service.Problem.Length > 0 && !(service.Id == "dns" && !service.Running))
        {
            var problem = Ui.Caption(service.Problem, Ui.Caution);
            problem.IsTextSelectionEnabled = true;
            var icon = Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution);
            icon.VerticalAlignment = VerticalAlignment.Top;
            icon.Margin = new Thickness(0, 2, 0, 0);
            var row = new Grid { ColumnSpacing = 6 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            Grid.SetColumn(problem, 1);
            row.Children.Add(icon);
            row.Children.Add(problem);
            panel.Children.Add(row);
        }

        // What the core services do, as on macOS (ServiceInfo.explanation): they have nothing else to show.
        var tld = Model.Status?.Dns.Tld is { Length: > 0 } t ? t : "test";
        if (service.Id == "web")
        {
            panel.Children.Add(Ui.Caption(service.Running
                ? $"Caddy serves every site over HTTPS (https://app.{tld}) with certificates from Flock's local certificate authority, which `flock install` trusts."
                : "Sites don't open while the web server is down. Its log may tell why; `flock restart` starts it again."));
        }
        if (service.Id == "dns")
        {
            panel.Children.Add(Ui.Caption(service.Running
                ? $"Windows sends every name under .{tld} here (a DNS rule set up by `flock install`), so any subdomain of a site works (tenant1.app.{tld}, a.b.app.{tld}) without adding it."
                : $"Wildcard subdomains don't resolve: {(service.Problem.Length > 0 ? service.Problem : "the DNS server isn't running")}. Site domains still work through the hosts file; add the subdomains you use to their site (Sites → … → Add Domain…)."));
        }
        // The web server and DNS ports: shown, but only `flock install` changes them.
        if (service.Installed && service.Port > 0 && service.Id is "web" or "dns")
        {
            panel.Children.Add(Ui.Spread(Ui.Text("Port", "BodyStrongTextBlockStyle"), Ui.Caption($"{service.Port} · set by `flock install`", wrap: false)));
        }
        if (service.Id == "hosts")
        {
            panel.Children.Add(Ui.Caption(service.Installed
                ? "The Flock hosts helper (a small Windows service) writes every site's domain and added domains into the hosts file, so they resolve even where the DNS rule doesn't apply (company DNS policies, VPNs, browsers with their own secure DNS)."
                : service.Problem.Length > 0
                    ? "Sites resolve through Flock's DNS rule instead. The hosts file is only a fallback for where that rule is overridden (company DNS policies, VPNs, browsers with their own secure DNS). Updating Flock tries the helper again."
                    : "The hosts helper is not installed, so site domains don't resolve. Run `flock install` in a terminal once; Windows asks for permission."));
        }
        if (service.Id == "postgres")
        {
            PostgresDetail(panel);
        }
        if (service.IsOnRequest)
        {
            CatalogDetail(panel, service);
        }

        // How to connect: only once there is something to connect to.
        if (service.Installed || !IsInstallable(service))
        {
            if (service.IsDatabase && TablePlus() is { } tablePlus)
            {
                panel.Children.Add(Ui.Spread(Ui.Text("TablePlus", "BodyStrongTextBlockStyle"), Ui.Button("Open", () =>
                    System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(tablePlus, service.Url) { UseShellExecute = false }))));
            }
            if (service.Env.Length > 0)
            {
                panel.Children.Add(Ui.Column(6, Ui.Text("Environment variables", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Env)));
            }
            if (service.Log.Length > 0)
            {
                panel.Children.Add(Ui.Spread(Ui.Text("Logs", "BodyStrongTextBlockStyle"), Ui.Button("Open", () => Native.Open(FlockPaths.LogFile(service.Log)))));
            }
            if (service.Url.Length > 0)
            {
                panel.Children.Add(Ui.Column(6, Ui.Text("Connection", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Url)));
            }
        }
        return card;
    }

    private static string? TablePlus()
    {
        foreach (var root in new[] { Environment.SpecialFolder.ProgramFiles, Environment.SpecialFolder.LocalApplicationData })
        {
            var path = Path.Combine(Environment.GetFolderPath(root), "TablePlus", "TablePlus.exe");
            if (File.Exists(path))
            {
                return path;
            }
        }
        return null;
    }

    private async Task EditPort(Service service)
    {
        var box = new NumberBox
        {
            Header = "Port",
            Value = service.Port,
            Minimum = 1024,
            Maximum = 65535,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
        };
        var dialog = new ContentDialog
        {
            Title = $"{service.Name} port",
            Content = Ui.Column(12, box, Ui.Caption("Ports 1024–65535. Update the port in your projects' .env files too.")),
            PrimaryButtonText = "Save",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary || double.IsNaN(box.Value))
        {
            return;
        }
        var port = (int)box.Value;
        if (port != service.Port && port is >= 1024 and <= 65535)
        {
            await Store.PerformOnServicesAsync(c => c.SetServicePortAsync(service.Id, port));
        }
    }
}
