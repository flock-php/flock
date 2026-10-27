using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using CatalogService = Flock.Kit.CatalogService;
namespace Flock.Tray;

/// <summary>
///     The catalog services (MySQL, MariaDB, Redis, MongoDB, Meilisearch, RustFS): pick a version line
///     and a port and install, follow the install (a job in the daemon); like PostgreSQL's majors,
///     lines install side by side: install more, switch, update or uninstall them. Each keeps its own data.
/// </summary>
internal sealed partial class ServicesPane
{
    private bool CatalogWorking(string id)
    {
        return Model.Catalog.IsRunning(id) || Model.Catalog.Current?[id]?.Job?.IsRunning == true;
    }

    private bool CatalogFailed(Service service)
    {
        return Model.Catalog.Current?[service.Id]?.Job?.IsFailed == true;
    }

    private void CatalogDetail(StackPanel panel, Service service)
    {
        if (Model.Catalog.Current is not { } catalog)
        {
            panel.Children.Add(LoadingRow("Loading…"));
            return;
        }
        if (catalog[service.Id] is not { } entry)
        {
            panel.Children.Add(Ui.Caption($"This Flock daemon doesn't offer {service.Name}. Update Flock, then try again."));
            return;
        }
        var row = Model.ReleaseActions.ResolveRow(entry.Id);
        if (!row.IsAvailable)
        {
            panel.Children.Add(Ui.Caption(entry.Unavailable.Length > 0 ? entry.Unavailable : $"{entry.Name} isn't available on this computer."));
            panel.Children.Add(CheckFooter(service.Id, Model.ReleaseActions.Resolve(service.Id)));
            return;
        }
        if (row.Job is { } job)
        {
            panel.Children.Add(CatalogJobCard(entry, row, job));
        }
        if (!entry.Lines.Any(l => l.Installed))
        {
            if (entry.Job is null && !Model.Catalog.IsRunning(entry.Id))
            {
                var targets = Model.ReleaseActions.InstallTargets(entry.Id);
                panel.Children.Add(targets.Count == 0
                    ? Inner(Ui.Caption($"Flock offers no {entry.Name} versions for this computer."))
                    : CatalogInstallCard(entry, row, targets));
            }
        }
        else
        {
            panel.Children.Add(CatalogLines(entry, row));
        }
        panel.Children.Add(CheckFooter(service.Id, Model.ReleaseActions.Resolve(service.Id)));
    }

    /// <summary>What it is, the version line and port, and Install (see InstallCard).</summary>
    private Border CatalogInstallCard(CatalogService entry, ServiceReleaseRow row, IReadOnlyList<ServiceReleaseTarget> targets)
    {
        return InstallCard(entry.Id, entry.Name, entry.Description.Length > 0 ? entry.Description : "Runs on 127.0.0.1 for your sites.",
            targets, row.Port, row.Port != row.DefaultPort ? $"Port {row.DefaultPort} is taken, so Flock suggests {row.Port}." : null, row.Estimate);
    }

    /// <summary>A running or failed install.</summary>
    private Border CatalogJobCard(CatalogService entry, ServiceReleaseRow row, CatalogJob job)
    {
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
            foreach (var command in row.JobCommands)
            {
                actions.Children.Add(CatalogJobCommand(entry, job, command));
            }
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
        foreach (var command in row.JobCommands)
        {
            actions.Children.Add(CatalogJobCommand(entry, job, command));
        }
        content.Children.Add(actions);
        return Inner(content);
    }

    /// <summary>Every line, like PostgreSQL's majors: install next to the others, switch, update, uninstall.</summary>
    private StackPanel CatalogLines(CatalogService entry, ServiceReleaseRow row)
    {
        var rows = new List<UIElement>();
        foreach (var line in row.Lines)
        {
            var source = entry.Lines.First(candidate => candidate.Id == line.Target.Line);
            var title = Ui.Row(8, Ui.Text($"{entry.Name} {line.Target.Label}", line.IsInstalled ? "BodyStrongTextBlockStyle" : "BodyTextBlockStyle", wrap: false));
            if (line.IsActive)
            {
                title.Children.Add(Ui.Badge("Active"));
            }
            var info = Ui.Column(2, title, Ui.Caption(line.Facts, line.HasUpdate ? Ui.Accent : null, false));
            if (source.Yanked.Length > 0)
            {
                var withdrawn = Ui.Caption(line.HasUpdate ? $"Withdrawn: {source.Yanked} — update to replace it" : $"Withdrawn: {source.Yanked}", Ui.Caution);
                ToolTipService.SetToolTip(withdrawn, "This build was withdrawn. Flock replaces it as soon as a fixed build is published.");
                info.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn));
            }

            var actions = Ui.Row(8);
            foreach (var command in line.PrimaryCommands)
            {
                actions.Children.Add(CatalogCommandButton(entry, source, line, command));
            }
            if (line.MoreCommands.Count > 0)
            {
                var more = new MenuFlyout();
                foreach (var command in line.MoreCommands)
                {
                    switch (command)
                    {
                        case ServiceRowCommandKind.Activate:
                            more.Items.Add(Item("Switch…", () => _ = SwitchCatalog(entry, source, line.Target)));
                            break;
                        case ServiceRowCommandKind.Uninstall:
                            more.Items.Add(Item("Uninstall…", () => _ = UninstallCatalog(entry, source, line.Target)));
                            break;
                        case ServiceRowCommandKind.Reinstall:
                            more.Items.Add(Item($"Reinstall {source.Version}", () => _ = RunCatalogAsync(entry.Id,
                                () => Model.ReleaseActions.RunAsync(line.Target, command))));
                            break;
                        case ServiceRowCommandKind.ShowLog:
                            more.Items.Add(Item("Show Last Log", () => _ = ShowCatalogLog(entry)));
                            break;
                    }
                }
                var moreButton = Ui.IconButton(Ui.GlyphMore, "More", () => { });
                moreButton.Flyout = more;
                actions.Children.Add(moreButton);
            }
            rows.Add(VersionRow(info, actions));
        }
        return Ui.Column(6, Ui.Text("Versions", "BodyStrongTextBlockStyle"), VersionRows(rows),
            Ui.Caption(entry.Lines.Count > 1
                ? "Installed versions stay side by side. One runs at a time; each keeps its own data, and updates within a version keep it."
                : "Updates keep your data."));
    }

    private Button CatalogCommandButton(CatalogService entry, CatalogLine source, ServiceReleaseLine line, ServiceRowCommandKind command)
    {
        return command switch
        {
            ServiceRowCommandKind.Install => Ui.Button("Install", () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(line.Target, command)),
                glyph: Ui.GlyphDownload, tooltip: (source.Latest.Length > 0 ? $"Downloads {entry.Name} {source.Latest}." : "Downloads the newest release.")
                                                  + (entry.IsInstalled ? $" {entry.Name} {entry.Line} keeps running until you switch." : "")
                                                  + (source.HasData ? " Its data is still here and comes back." : "")),
            ServiceRowCommandKind.Update => Ui.Button($"Update to {source.Latest}", () => _ = RunCatalogAsync(entry.Id,
                    () => Model.ReleaseActions.RunAsync(line.Target, command)), true, Ui.GlyphDownload,
                $"Installs {source.Latest} in place of {source.Version}. Your data stays{(source.Active ? $"; {entry.Name} restarts for a moment" : "")}."),
            ServiceRowCommandKind.Uninstall => Ui.Button("Uninstall…", () => _ = UninstallCatalog(entry, source, line.Target)),
            _ => throw new ArgumentOutOfRangeException(nameof(command), command, null)
        };
    }

    private FrameworkElement CatalogJobCommand(CatalogService entry, CatalogJob job, ServiceRowCommandKind command)
    {
        var target = new ServiceReleaseTarget(entry.Id, ServiceReleaseSource.Catalog, job.Line, entry.Name, job.Version);
        return command switch
        {
            ServiceRowCommandKind.ShowLog => Ui.Link("Show Log", () => _ = ShowCatalogLog(entry)),
            ServiceRowCommandKind.Cancel => Ui.Button("Cancel", () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command))),
            ServiceRowCommandKind.Retry => Ui.Button("Try Again", () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command)),
                true, Ui.GlyphRefresh),
            ServiceRowCommandKind.Dismiss => Ui.Button("Dismiss", () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command))),
            _ => throw new ArgumentOutOfRangeException(nameof(command), command, null)
        };
    }

    private async Task SwitchCatalog(CatalogService entry, CatalogLine target, ServiceReleaseTarget releaseTarget)
    {
        var own = target.HasData ? $"{entry.Name} {target.Label} uses its own data." : $"{entry.Name} {target.Label} starts with empty data.";
        if (!entry.IsInstalled || await Window.Confirm($"Switch to {entry.Name} {target.Label}?",
                $"{entry.Name} {entry.Line} stops; its data stays but isn't used until you switch back. {own}", "Switch"))
        {
            await RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(releaseTarget, ServiceRowCommandKind.Activate));
        }
    }

    private async Task UninstallCatalog(CatalogService entry, CatalogLine line, ServiceReleaseTarget releaseTarget)
    {
        var next = entry.Lines.FirstOrDefault(l => l.Installed && l.Id != line.Id);
        var details = !line.Active ? ""
            : next is not null ? $"{entry.Name} {line.Label} stops and {entry.Name} {next.Label} becomes the active version. "
            : $"{entry.Name} {line.Label} stops; your sites can't use {entry.Name} until you install a version again. ";
        details += line.HasData
            ? $"Its data stays unless you delete it below; installing {line.Label} again picks it up."
            : "It holds no data.";

        CheckBox? delete = null;
        UIElement? extra = null;
        if (line.HasData)
        {
            delete = new CheckBox { Content = "Also delete its data" };
            var warning = Ui.Caption("Deleted data can't be recovered.", Ui.Caution);
            warning.Visibility = Visibility.Collapsed;
            warning.Margin = new Thickness(28, -4, 0, 0);
            delete.Checked += (_, _) => warning.Visibility = Visibility.Visible;
            delete.Unchecked += (_, _) => warning.Visibility = Visibility.Collapsed;
            extra = Ui.Column(4, delete, warning);
        }
        if (await Window.Confirm($"Uninstall {entry.Name} {line.Label}?", details, "Uninstall", true, extra))
        {
            var deleteData = delete?.IsChecked == true;
            await RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(releaseTarget, ServiceRowCommandKind.Uninstall, deleteData));
        }
    }

    private async Task RunCatalogAsync(string id, Func<Task<OperationOutcome>> action)
    {
        Kick();
        await action();
        Kick();
    }

    private Task ShowCatalogLog(CatalogService entry)
    {
        return JobLogDialog.ShowAsync(Window, $"{entry.Name} install log", token => Model.Catalog.ReadLogAsync(entry.Id, token), () => CatalogWorking(entry.Id));
    }
}