using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// The catalog services (MySQL, MariaDB, Redis, MongoDB, Meilisearch, RustFS): pick a version line
/// and a port and install, follow the install (a job in the daemon); like PostgreSQL's majors,
/// lines install side by side: install more, switch, update or uninstall them. Each keeps its own data.
/// </summary>
internal sealed partial class ServicesPane
{
    private bool CatalogWorking(string id) => Model.Catalog.IsRunning(id) || Model.Catalog.Current?[id]?.Job?.IsRunning == true;

    private bool CatalogFailed(Service service) => Model.Catalog.Current?[service.Id]?.Job?.IsFailed == true;

    private void CatalogDetail(StackPanel panel, Service service)
    {
        if (Model.Catalog.Current is not { } catalog)
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
        if (!entry.Lines.Any(l => l.Installed))
        {
            if (entry.Job is not { IsRunning: true } && !Model.Catalog.IsRunning(entry.Id))
            {
                panel.Children.Add(CatalogInstallCard(entry));
            }
        }
        else
        {
            panel.Children.Add(CatalogLines(entry));
        }
        panel.Children.Add(CheckFooter(catalog.CheckedAt, catalog.CheckError, postgres: false));
    }

    /// <summary>What it is, the version line and port, and Install (see InstallCard).</summary>
    private Border CatalogInstallCard(CatalogService entry) =>
        InstallCard(entry.Id, entry.Name, entry.Description.Length > 0 ? entry.Description : "Runs on 127.0.0.1 for your sites.",
            entry.Lines.Select(l => (l.Id, l.Latest.Length > 0 ? $"{l.Label} ({l.Latest})" : l.Label, l.Latest)).ToList(), entry.PreferredLine?.Id,
            entry.Port, entry.Port != entry.DefaultPort ? $"Port {entry.DefaultPort} is taken, so Flock suggests {entry.Port}." : null, entry.Estimate,
            (line, port) => RunCatalogAsync(entry.Id, () => Model.Catalog.InstallAsync(entry.Id, line, port)));

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
            actions.Children.Add(Ui.Button("Cancel", () => _ = RunCatalogAsync(id, () => Model.Catalog.CancelAsync(id))));
            Ui.SetEnabled(actions, !Model.Catalog.IsRunning(id));
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
        actions.Children.Add(Ui.Button("Try Again", () => _ = RunCatalogAsync(id, () => Model.Catalog.InstallAsync(id, line)), accent: true, glyph: Ui.GlyphRefresh));
        actions.Children.Add(Ui.Button("Dismiss", () => _ = RunCatalogAsync(id, () => Model.Catalog.DismissAsync(id))));
        actions.Children.Add(Ui.Link("Show Log", () => _ = ShowCatalogLog(entry)));
        Ui.SetEnabled(actions, !Model.Catalog.IsRunning(id));
        content.Children.Add(actions);
        return Inner(content);
    }

    /// <summary>Every line, like PostgreSQL's majors: install next to the others, switch, update, uninstall.</summary>
    private StackPanel CatalogLines(CatalogService entry)
    {
        var id = entry.Id;
        var rows = new List<UIElement>();
        foreach (var line in entry.Lines)
        {
            var target = line;
            var title = Ui.Row(8, Ui.Text($"{entry.Name} {line.Label}", line.Installed ? "BodyStrongTextBlockStyle" : "BodyTextBlockStyle", wrap: false));
            if (line.Active)
            {
                title.Children.Add(Ui.Badge("Active"));
            }
            var info = Ui.Column(2, title, LineFacts(line));
            if (line.Yanked.Length > 0)
            {
                var withdrawn = Ui.Caption(line.Update ? $"Withdrawn: {line.Yanked} — update to replace it" : $"Withdrawn: {line.Yanked}", Ui.Caution);
                ToolTipService.SetToolTip(withdrawn, "This build was withdrawn. Flock replaces it as soon as a fixed build is published.");
                info.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn));
            }

            var actions = Ui.Row(8);
            if (!line.Installed)
            {
                var tip = (line.Latest.Length > 0 ? $"Downloads {entry.Name} {line.Latest}." : "Downloads the newest release.")
                    + (entry.IsInstalled ? $" {entry.Name} {entry.Line} keeps running until you switch." : "")
                    + (line.HasData ? " Its data is still here and comes back." : "");
                actions.Children.Add(Ui.Button("Install", () => _ = RunCatalogAsync(id, () => Model.Catalog.InstallAsync(id, target.Id)), glyph: Ui.GlyphDownload, tooltip: tip));
            }
            else
            {
                if (line.Update)
                {
                    var restart = line.Active ? $"; {entry.Name} restarts for a moment" : "";
                    actions.Children.Add(Ui.Button($"Update to {line.Latest}", () => _ = RunCatalogAsync(id, () => Model.Catalog.InstallAsync(id, target.Id)), glyph: Ui.GlyphDownload,
                        tooltip: $"Installs {line.Latest} in place of {line.Version}. Your data stays{restart}."));
                }
                if (!line.Active)
                {
                    actions.Children.Add(Ui.Button("Switch…", () => _ = SwitchCatalog(entry, target),
                        tooltip: entry.IsInstalled ? $"Stops {entry.Name} {entry.Line} and starts {line.Label} with its own data" : $"Starts {entry.Name} {line.Label}"));
                }
                var more = new MenuFlyout();
                if (!line.Update)
                {
                    more.Items.Add(Item($"Reinstall {line.Version}", () => _ = RunCatalogAsync(id, () => Model.Catalog.InstallAsync(id, target.Id))));
                }
                more.Items.Add(Item("Show Last Log", () => _ = ShowCatalogLog(entry)));
                more.Items.Add(new MenuFlyoutSeparator());
                more.Items.Add(Item("Uninstall…", () => _ = UninstallCatalog(entry, target)));
                var moreButton = Ui.IconButton(Ui.GlyphMore, "More", () => { });
                moreButton.Flyout = more;
                actions.Children.Add(moreButton);
            }
            Ui.SetEnabled(actions, !CatalogWorking(id));
            rows.Add(VersionRow(info, actions));
        }
        return Ui.Column(6, Ui.Text("Versions", "BodyStrongTextBlockStyle"), VersionRows(rows),
            Ui.Caption(entry.Lines.Count > 1
                ? "Installed versions stay side by side. One runs at a time; each keeps its own data, and updates within a version keep it."
                : "Updates keep your data."));
    }

    /// <summary>"8.4.6 · 8.4.7 available · has data", or what is available of a line that isn't installed.</summary>
    private static TextBlock LineFacts(CatalogLine line)
    {
        var parts = new List<string> { line.Installed ? line.Version : line.Latest.Length > 0 ? $"Not installed · {line.Latest} available" : "Not installed" };
        if (line.Update)
        {
            parts.Add(line.Latest == line.Version ? "new build available" : $"{line.Latest} available");
        }
        else if (line.Installed)
        {
            parts.Add("up to date");
        }
        if (line.HasData)
        {
            parts.Add(line.Installed ? "has data" : "data kept");
        }
        return Ui.Caption(string.Join(" · ", parts), line.Update ? Ui.Accent : null, wrap: false);
    }

    private async Task SwitchCatalog(CatalogService entry, CatalogLine target)
    {
        var own = target.HasData ? $"{entry.Name} {target.Label} uses its own data." : $"{entry.Name} {target.Label} starts with empty data.";
        if (!entry.IsInstalled || await Window.Confirm($"Switch to {entry.Name} {target.Label}?",
                $"{entry.Name} {entry.Line} stops; its data stays but isn't used until you switch back. {own}", "Switch"))
        {
            await RunCatalogAsync(entry.Id, () => Model.Catalog.ActivateAsync(entry.Id, target.Id));
        }
    }

    private async Task UninstallCatalog(CatalogService entry, CatalogLine line)
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
        if (await Window.Confirm($"Uninstall {entry.Name} {line.Label}?", details, "Uninstall", destructive: true, extra: extra))
        {
            var deleteData = delete?.IsChecked == true;
            await RunCatalogAsync(entry.Id, () => Model.Catalog.UninstallAsync(entry.Id, line.Id, deleteData));
        }
    }

    private async Task RunCatalogAsync(string id, Func<Task<OperationOutcome>> action)
    {
        await action();
        Kick();
    }

    private Task ShowCatalogLog(CatalogService entry) =>
        JobLogDialog.ShowAsync(Window, $"{entry.Name} install log", token => Model.Catalog.ReadLogAsync(entry.Id, token), () => CatalogWorking(entry.Id));
}