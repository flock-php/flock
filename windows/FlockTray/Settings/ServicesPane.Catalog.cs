using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// The catalog services (MySQL, MariaDB, Redis, MongoDB, Meilisearch, RustFS): pick a version line
/// and a port and install, follow the install (a job in the daemon), update, switch lines or
/// uninstall. Each line keeps its own data.
/// </summary>
internal sealed partial class ServicesPane
{
    // Catalog services with a request of this window in flight (installs return quickly; the job follows).
    private readonly HashSet<string> pendingCatalog = [];
    // The line and port picked for a service that isn't installed yet, kept across re-renders.
    private readonly Dictionary<string, string> pickedLine = [];
    private readonly Dictionary<string, double> pickedPort = [];

    private bool CatalogWorking(string id) => pendingCatalog.Contains(id) || Store.Catalog?[id]?.Job?.IsRunning == true;

    private bool CatalogFailed(Service service) => Store.Catalog?[service.Id]?.Job?.IsFailed == true;

    private void CatalogDetail(StackPanel panel, Service service)
    {
        if (Store.Catalog is not { } catalog)
        {
            panel.Children.Add(Loading("Loading…"));
            return;
        }
        if (catalog[service.Id] is not { } entry)
        {
            panel.Children.Add(Ui.Caption($"This Flock daemon doesn't offer {service.Name}. Update Flock, then try again."));
            return;
        }

        if (entry.Job is { } job)
        {
            panel.Children.Add(CatalogJobCard(entry, job));
        }
        if (!entry.IsInstalled)
        {
            if (entry.Job is not { IsRunning: true } && !pendingCatalog.Contains(entry.Id))
            {
                panel.Children.Add(CatalogInstallCard(entry));
            }
        }
        else
        {
            panel.Children.Add(CatalogLines(entry));
        }
        if (entry.Id == "redis")
        {
            panel.Children.Add(Ui.Caption("Laravel on Windows talks to Redis through predis (REDIS_CLIENT=predis below), so each project needs `composer require predis/predis`."));
        }
        panel.Children.Add(CheckFooter(catalog.CheckedAt, catalog.CheckError, postgres: false));
    }

    /// <summary>What it is, the version line and port, and Install.</summary>
    private Border CatalogInstallCard(CatalogService entry)
    {
        var content = Ui.Column(10);
        content.Children.Add(Ui.Text($"Install {entry.Name}", "BodyStrongTextBlockStyle"));
        if (entry.Description.Length > 0)
        {
            content.Children.Add(Ui.Caption(entry.Description));
        }

        var lines = new ComboBox { Header = "Version", MinWidth = 220 };
        foreach (var line in entry.Lines)
        {
            lines.Items.Add(new ComboBoxItem { Content = line.Latest.Length > 0 ? $"{line.Label} ({line.Latest})" : line.Label, Tag = line.Id });
        }
        var picked = pickedLine.GetValueOrDefault(entry.Id) ?? entry.PreferredLine?.Id;
        lines.SelectedItem = lines.Items.OfType<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == picked) ?? lines.Items.FirstOrDefault();
        lines.SelectionChanged += (_, _) =>
        {
            if (lines.SelectedItem is ComboBoxItem { Tag: string id })
            {
                pickedLine[entry.Id] = id;
            }
        };
        if (entry.Lines.Count > 1)
        {
            content.Children.Add(lines);
        }

        var port = new NumberBox
        {
            Header = "Port",
            Value = pickedPort.GetValueOrDefault(entry.Id, entry.Port),
            Minimum = 1024,
            Maximum = 65535,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
            MinWidth = 140,
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        port.ValueChanged += (_, _) => pickedPort[entry.Id] = port.Value;
        content.Children.Add(port);
        if (entry.Port != entry.DefaultPort)
        {
            content.Children.Add(Ui.Caption($"Port {entry.DefaultPort} is taken, so Flock suggests {entry.Port}."));
        }

        var install = Ui.Button($"Install {entry.Name}", () =>
        {
            var line = (lines.SelectedItem as ComboBoxItem)?.Tag as string;
            int? chosen = double.IsNaN(port.Value) ? null : (int)port.Value;
            _ = RunCatalog(entry.Id, c => c.InstallCatalogServiceAsync(entry.Id, line, chosen));
        }, accent: true, glyph: Ui.GlyphDownload);
        var actions = Ui.Row(10, install);
        if (entry.Estimate.Length > 0)
        {
            actions.Children.Add(Ui.Caption(Capitalize(entry.Estimate), wrap: false));
        }
        content.Children.Add(actions);
        return Inner(content);
    }

    /// <summary>A running or failed install.</summary>
    private Border CatalogJobCard(CatalogService entry, CatalogJob job)
    {
        var id = entry.Id;
        var content = Ui.Column(8);
        var actions = Ui.Row(8);
        var target = job.Version.Length > 0 ? $"{entry.Name} {job.Version}" : entry.Name;
        if (job.IsRunning)
        {
            var verb = job.Action == "update" ? "Updating" : "Installing";
            var percent = job.HasProgress ? $"{Math.Round(job.Progress * 100)} %" : "";
            content.Children.Add(Ui.Spread(Ui.Text($"{verb} {target}", "BodyStrongTextBlockStyle"), Ui.Caption(percent, wrap: false)));
            content.Children.Add(new ProgressBar { Minimum = 0, Maximum = 1, Value = job.HasProgress ? job.Progress : 0, IsIndeterminate = !job.HasProgress });
            var phase = job.PhaseText + (job.StartedAt is { Year: > 2000 } started ? $" · started {Ui.Relative(started)}" : "");
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowCatalogLog(entry)));
            actions.Children.Add(Ui.Button("Cancel", () => _ = RunCatalog(id, c => c.CancelCatalogJobAsync(id))));
            Ui.SetEnabled(actions, !pendingCatalog.Contains(id));
            content.Children.Add(Ui.Spread(Ui.Caption(phase), actions));
            return Inner(content);
        }

        content.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 14, Ui.Critical),
            Ui.Text(job.Action == "update" ? $"Updating {entry.Name} failed" : $"Installing {target} failed", "BodyStrongTextBlockStyle")));
        var error = Ui.Caption(job.Error.Length > 0 ? job.Error : "Flock reported no details; the log may tell more.", Ui.Critical);
        error.IsTextSelectionEnabled = true;
        error.MaxLines = 4;
        ToolTipService.SetToolTip(error, job.Error);
        content.Children.Add(error);
        var line = job.Line.Length > 0 ? job.Line : null;
        actions.Children.Add(Ui.Button("Try Again", () => _ = RunCatalog(id, c => c.InstallCatalogServiceAsync(id, line)), accent: true, glyph: Ui.GlyphRefresh));
        actions.Children.Add(Ui.Button("Dismiss", () => _ = RunCatalog(id, c => c.DismissCatalogJobAsync(id))));
        actions.Children.Add(Ui.Link("Show Log", () => _ = ShowCatalogLog(entry)));
        Ui.SetEnabled(actions, !pendingCatalog.Contains(id));
        content.Children.Add(actions);
        return Inner(content);
    }

    /// <summary>The installed line with update and uninstall, and the other lines to switch to.</summary>
    private StackPanel CatalogLines(CatalogService entry)
    {
        var id = entry.Id;
        var locked = CatalogWorking(id);
        var list = Ui.Column(6, Ui.Text("Versions", "BodyStrongTextBlockStyle"));
        foreach (var line in entry.Lines)
        {
            var content = Ui.Column(8);
            if (line.Installed)
            {
                var status = entry.Update ? Ui.Caption($"{entry.Installed} · {entry.Latest} available", Ui.Accent) : Ui.Caption($"{entry.Installed} · up to date");
                var more = new MenuFlyout();
                more.Items.Add(Item(entry.Update ? $"Update to {entry.Latest}" : $"Reinstall {entry.Installed}", () => _ = RunCatalog(id, c => c.InstallCatalogServiceAsync(id, line.Id))));
                more.Items.Add(Item("Show Last Log", () => _ = ShowCatalogLog(entry)));
                more.Items.Add(new MenuFlyoutSeparator());
                more.Items.Add(Item("Uninstall…", () => _ = UninstallCatalog(entry)));
                var moreButton = Ui.IconButton(Ui.GlyphMore, "More", () => { });
                moreButton.Flyout = more;
                moreButton.IsEnabled = !locked;
                content.Children.Add(Ui.Spread(Ui.Column(1, Ui.Text($"{entry.Name} {line.Label}", "BodyStrongTextBlockStyle", wrap: false), status), moreButton));
                if (entry.Update)
                {
                    var update = Ui.Button($"Update to {entry.Latest}", () => _ = RunCatalog(id, c => c.InstallCatalogServiceAsync(id, line.Id)), glyph: Ui.GlyphDownload,
                        tooltip: "Runs in the background; your data stays");
                    update.IsEnabled = !locked;
                    content.Children.Add(update);
                }
            }
            else
            {
                var detail = line.Latest.Length > 0 ? $"{line.Latest} available" : "Not installed";
                if (line.HasData)
                {
                    detail += " · has data";
                }
                var target = line;
                var switchButton = Ui.Button("Switch…", async () =>
                {
                    var own = target.HasData ? $"{entry.Name} {target.Label} uses its own data from before." : $"{entry.Name} {target.Label} starts with empty data.";
                    if (await Window.Confirm($"Switch to {entry.Name} {target.Label}?",
                        $"The installed version is replaced; its data stays but isn't used until you switch back. {own}", "Switch"))
                    {
                        await RunCatalog(id, c => c.InstallCatalogServiceAsync(id, target.Id));
                    }
                });
                switchButton.IsEnabled = !locked;
                content.Children.Add(Ui.Spread(Ui.Column(1, Ui.Text($"{entry.Name} {line.Label}", wrap: false), Ui.Caption(detail, wrap: false)), switchButton));
            }
            list.Children.Add(Inner(content));
        }
        list.Children.Add(Ui.Caption(entry.Lines.Count > 1
            ? "One version runs at a time. Each version keeps its own data; updates within a version keep it."
            : "Updates keep your data."));
        return list;
    }

    private async Task UninstallCatalog(CatalogService entry)
    {
        var deleteData = new CheckBox { Content = "Also delete its data" };
        var dialog = new ContentDialog
        {
            Title = $"Uninstall {entry.Name}?",
            Content = Ui.Column(12, Ui.Caption($"{entry.Name} stops and your sites can't use it until you install it again. Keep its data to pick up where you left off."), deleteData),
            PrimaryButtonText = "Uninstall",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot,
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary)
        {
            var delete = deleteData.IsChecked == true;
            await RunCatalog(entry.Id, c => c.UninstallCatalogServiceAsync(entry.Id, delete));
        }
    }

    private async Task RunCatalog(string id, Func<FlockClient, Task<ServiceCatalog>> action)
    {
        pendingCatalog.Add(id);
        Rerender();
        try
        {
            await Store.PerformOnCatalogAsync(action);
        }
        finally
        {
            pendingCatalog.Remove(id);
            Rerender();
            Kick();
        }
    }

    private Task ShowCatalogLog(CatalogService entry) =>
        ShowLog($"{entry.Name} install log", () => Store.CatalogLogAsync(entry.Id), () => CatalogWorking(entry.Id));
}
