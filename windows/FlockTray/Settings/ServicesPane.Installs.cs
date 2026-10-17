using System.Globalization;
using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;

namespace Flock.Tray;

/// <summary>
/// Installing, updating and switching PostgreSQL versions, and installing catalog services
/// (downloads; the daemon runs them as jobs). State that other windows or the
/// CLI start shows up too: the daemon reports PostgreSQL's busy majors and the component jobs.
/// </summary>
internal sealed partial class ServicesPane
{
    // PostgreSQL majors with a request of this window in flight, and what to show meanwhile.
    private readonly Dictionary<string, string> pendingMajors = [];
    // Components with a request of this window in flight (the request returns quickly; the job follows).
    private readonly HashSet<string> pendingComponents = [];
    private bool checking;
    // The poll loop's wait; cancelled to look again right away after starting something.
    private CancellationTokenSource? nap;

    /// <summary>Some PostgreSQL operation runs: other PostgreSQL actions wait for it.</summary>
    private bool PostgresWorking => pendingMajors.Count > 0 || Store.Postgres?.AnyBusy == true;

    private bool ComponentWorking(string id) => pendingComponents.Contains(id) || Store.Components?[id]?.Job?.IsRunning == true;

    private static bool IsInstallable(Service service) => service.Id == "postgres" || service.IsOnRequest;

    /// <summary>
    /// Polls every 2 s while something installs; otherwise the store reloads when the service list
    /// changes, and a slow poll catches jobs that the CLI started.
    /// </summary>
    private async Task Poll(CancellationToken hidden)
    {
        while (!hidden.IsCancellationRequested)
        {
            await Store.RefreshServiceVersionsAsync();
            var fast = PostgresWorking || pendingComponents.Count > 0 || Store.Components?.HasRunningJob == true
                || pendingCatalog.Count > 0 || Store.Catalog?.HasRunningJob == true;
            using var wake = CancellationTokenSource.CreateLinkedTokenSource(hidden);
            nap = wake;
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(fast ? 2 : 30), wake.Token);
            }
            catch (TaskCanceledException)
            {
            }
            finally
            {
                nap = null;
            }
        }
    }

    private void Kick() => nap?.Cancel();

    /// <summary>The tooltip of a service row's update hint; null when there is nothing to update.</summary>
    private string? UpdateHint(Service service)
    {
        if (service.Id == "postgres")
        {
            if (Store.Postgres?.Majors.FirstOrDefault(m => m.Update) is { } major)
            {
                return major.Latest == major.Installed ? $"A new build of PostgreSQL {major.Latest} is available" : $"PostgreSQL {major.Latest} is available";
            }
        }
        if (Store.Catalog?[service.Id] is { Update: true } entry)
        {
            return $"{entry.Name} {entry.Latest} is available";
        }
        return null;
    }

    /// <summary>What a service row says while something about it runs; null when idle.</summary>
    private string? WorkText(Service service)
    {
        if (service.Id == "postgres")
        {
            if (pendingMajors.Count > 0)
            {
                return pendingMajors.First().Value;
            }
            if (Store.Postgres?.Majors.FirstOrDefault(m => m.IsBusy) is { } busy)
            {
                return busy.BusyText;
            }
        }
        if (CatalogWorking(service.Id))
        {
            return Store.Catalog?[service.Id]?.Job?.PhaseText ?? "Working…";
        }
        return null;
    }

    // PostgreSQL

    private void PostgresDetail(StackPanel panel)
    {
        if (Store.Postgres is not { } versions)
        {
            panel.Children.Add(Loading("Loading PostgreSQL versions…"));
            return;
        }
        if (!versions.AnyInstalled)
        {
            panel.Children.Add(PostgresWorking ? FirstInstall(versions) : PostgresEmpty(versions));
        }
        else
        {
            panel.Children.Add(ActiveCard(versions));
            panel.Children.Add(VersionList(versions));
        }
        panel.Children.Add(CheckFooter(versions.CheckedAt, versions.CheckError, postgres: true));
        panel.Children.Add(Ui.Caption("psql, pg_dump and the other command-line tools come with PostgreSQL. In a terminal, Flock uses those of the newest installed version, which work with every version above and with remote servers."));
    }

    /// <summary>Nothing installed yet: the newest version one click away, the others in a menu.</summary>
    private Border PostgresEmpty(PostgresVersions versions)
    {
        if (versions.Majors.Count == 0)
        {
            return Inner(Ui.Caption("Flock offers no PostgreSQL versions for this computer."));
        }
        var newest = versions.Majors[0];
        var install = Ui.Button($"Install PostgreSQL {newest.Major}", () => _ = Install(versions, newest), accent: true, glyph: Ui.GlyphDownload,
            tooltip: newest.Latest.Length > 0 ? $"Downloads PostgreSQL {newest.Latest} and starts it" : "Downloads the newest release and starts it");

        var others = new MenuFlyout { Placement = Microsoft.UI.Xaml.Controls.Primitives.FlyoutPlacementMode.Bottom };
        foreach (var major in versions.Majors.Skip(1))
        {
            var label = $"PostgreSQL {major.Major}" + (major.Latest.Length > 0 ? $" ({major.Latest})" : "");
            if (major.HasData)
            {
                label += " · your databases";
            }
            else if (major.EndOfLifeState(Today) is EndOfLife.Soon or EndOfLife.Past)
            {
                label += $" · end of life {Month(major.EolDate!.Value)}";
            }
            others.Items.Add(Item(label, () => _ = Install(versions, major)));
        }
        UIElement[] actions = others.Items.Count > 0 ? [install, new DropDownButton { Content = "Another version", Flyout = others }] : [install];
        var hero = Hero(Ui.GlyphServer, "PostgreSQL isn't installed",
            "Install the newest version to give your apps a database. You can add other versions later; each one keeps its own databases.",
            actions);
        var kept = versions.Majors.Where(m => m.HasData).Select(m => m.Major).ToList();
        if (kept.Count > 0)
        {
            var note = Ui.Caption($"Databases of PostgreSQL {string.Join(", ", kept)} are still here — installing that version again picks them up.");
            note.TextAlignment = TextAlignment.Center;
            ((Panel)hero.Child).Children.Add(note);
        }
        return hero;
    }

    /// <summary>The very first install: it downloads, creates the cluster and starts it.</summary>
    private Border FirstInstall(PostgresVersions versions)
    {
        var major = versions.Majors.FirstOrDefault(m => m.IsBusy) ?? versions.Majors.FirstOrDefault(m => pendingMajors.ContainsKey(m.Major));
        var name = major is null ? "PostgreSQL" : $"PostgreSQL {(major.Latest.Length > 0 ? major.Latest : major.Major)}";
        var verb = major is { Busy: PostgresBusy.Uninstalling } ? "Uninstalling" : "Installing";
        return Inner(Ui.Column(10,
            Ui.Text($"{verb} {name}…", "BodyStrongTextBlockStyle"),
            new ProgressBar { IsIndeterminate = true },
            Ui.Caption("Downloading the server and creating its database cluster. This takes a minute or two; you can close this window meanwhile.")),
            new Thickness(16, 14, 16, 14));
    }

    /// <summary>The active version: what runs, where, and how much data it holds.</summary>
    private Border ActiveCard(PostgresVersions versions)
    {
        if (versions.ActiveMajor is not { } active)
        {
            return Inner(Ui.Caption("No version is active. Switch to an installed version below to start PostgreSQL."));
        }
        var (state, up) = versions.Running ? ("Running", true) : versions.Enabled ? ("Not running", false) : ("Stopped", false);
        var facts = $"{state} · Port {versions.Port}" + (active.HasData ? $" · {Ui.Bytes(active.DataSize)} of data" : "");

        var text = Ui.Column(2, Ui.Caption("Active version", wrap: false), Ui.Text($"PostgreSQL {active.Installed}", "SubtitleTextBlockStyle", wrap: false),
            Ui.Row(6, Ui.StatusDot(up), Ui.Caption(facts)));
        if (BusyText(active) is { } busy)
        {
            text.Children.Add(Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(busy, wrap: false)));
        }
        else if (active.Update)
        {
            var what = active.Latest == active.Installed ? $"A new build of {active.Latest}" : active.Latest;
            text.Children.Add(Ui.Caption($"{what} is available — update it below; your databases stay.", Ui.Accent));
        }
        if (active.Yanked.Length > 0)
        {
            var withdrawn = Ui.Caption($"This build was withdrawn: {active.Yanked}", Ui.Caution);
            ToolTipService.SetToolTip(withdrawn, "Flock replaces it as soon as a fixed build is published.");
            text.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn));
        }
        var icon = Ui.Icon(Ui.GlyphServer, 26, up ? Ui.Accent : Ui.TertiaryText);
        icon.VerticalAlignment = VerticalAlignment.Top;
        icon.Margin = new Thickness(0, 4, 0, 0);
        return Inner(Ui.Row(14, icon, text), new Thickness(16, 14, 16, 14));
    }

    private StackPanel VersionList(PostgresVersions versions)
    {
        var rows = new StackPanel();
        foreach (var major in versions.Majors)
        {
            if (rows.Children.Count > 0)
            {
                rows.Children.Add(new Border { Height = 1, Background = Ui.Brush("DividerStrokeColorDefaultBrush") });
            }
            rows.Children.Add(MajorRow(versions, major));
        }
        return Ui.Column(6, Ui.Text("Versions", "BodyStrongTextBlockStyle"), Inner(rows, new Thickness(14, 2, 8, 2)));
    }

    private string? BusyText(PostgresMajor major) =>
        pendingMajors.TryGetValue(major.Major, out var pending) ? (major.IsBusy ? major.BusyText : pending) : major.IsBusy ? major.BusyText : null;

    private StackPanel MajorRow(PostgresVersions versions, PostgresMajor major)
    {
        var title = Ui.Row(8, Ui.Text($"PostgreSQL {major.Major}", "BodyStrongTextBlockStyle", wrap: false));
        if (major.Active)
        {
            title.Children.Add(Ui.Badge("Active"));
        }
        var busy = BusyText(major);
        var top = busy is null && major.IsInstalled ? Ui.Spread(title, More(versions, major)) : Ui.Spread(title);
        var row = Ui.Column(4, top, Facts(major));
        row.Padding = new Thickness(0, 10, 0, 10);
        if (busy is not null)
        {
            row.Children.Add(Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(busy, wrap: false)));
            return row;
        }

        var actions = Ui.Row(8);
        if (!major.IsInstalled)
        {
            var tip = (major.Latest.Length > 0 ? $"Downloads PostgreSQL {major.Latest}." : "Downloads the newest release.")
                + (versions.Active.Length > 0 ? $" PostgreSQL {versions.Active} stays active until you switch." : "")
                + (major.HasData ? " Its databases are still here and come back." : "");
            actions.Children.Add(Ui.Button("Install", () => _ = Install(versions, major), glyph: Ui.GlyphDownload, tooltip: tip));
        }
        else
        {
            if (major.Update)
            {
                var restart = major.Active && versions.Running ? "; PostgreSQL restarts for a moment" : "";
                actions.Children.Add(Ui.Button($"Update to {major.Latest}", () => _ = Run(major.Major, "Updating…", c => c.UpdatePostgresAsync(major.Major)),
                    glyph: Ui.GlyphDownload, tooltip: $"Installs {major.Latest} in place of {major.Installed}. Your databases stay{restart}."));
            }
            if (!major.Active)
            {
                actions.Children.Add(Ui.Button($"Switch to {major.Major}", () => _ = Switch(versions, major),
                    tooltip: versions.Active.Length > 0 ? $"Stops PostgreSQL {versions.Active} and starts {major.Major} with its own databases" : $"Starts PostgreSQL {major.Major}"));
            }
        }
        if (actions.Children.Count > 0)
        {
            // One PostgreSQL operation at a time.
            Ui.SetEnabled(actions, !PostgresWorking);
            row.Children.Add(actions);
        }
        return row;
    }

    /// <summary>"18.6.0 · 18.7.0 available · 46 MB of data · supported until Nov 2030", end of life tinted when near.</summary>
    private static TextBlock Facts(PostgresMajor major)
    {
        var text = Ui.Caption("");
        void Add(string part, Microsoft.UI.Xaml.Media.Brush? color = null)
        {
            if (text.Inlines.Count > 0)
            {
                text.Inlines.Add(new Run { Text = " · " });
            }
            var run = new Run { Text = part };
            if (color is not null)
            {
                run.Foreground = color;
            }
            text.Inlines.Add(run);
        }

        Add(major.IsInstalled ? major.Installed : "Not installed");
        if (major.Update)
        {
            Add(major.Latest == major.Installed ? "new build available" : $"{major.Latest} available", Ui.Accent);
        }
        if (major.Yanked.Length > 0)
        {
            Add($"withdrawn: {major.Yanked}", Ui.Caution);
        }
        if (major.Prerelease)
        {
            Add($"{major.Latest} is a pre-release", Ui.Caution);
        }
        if (major.HasData)
        {
            Add(major.IsInstalled ? $"{Ui.Bytes(major.DataSize)} of data" : $"databases kept ({Ui.Bytes(major.DataSize)})");
        }
        if (major.EolDate is { } eol)
        {
            switch (major.EndOfLifeState(Today))
            {
                case EndOfLife.Past:
                    Add($"end of life since {Month(eol)}", Ui.Caution);
                    break;
                case EndOfLife.Soon:
                    Add($"end of life {Month(eol)}", Ui.Caution);
                    break;
                default:
                    Add($"supported until {Month(eol)}", Ui.TertiaryText);
                    break;
            }
            ToolTipService.SetToolTip(text, $"PostgreSQL {major.Major} gets fixes upstream until {eol.ToString("D", CultureInfo.CurrentCulture)}; after that there are no more security updates.");
        }
        return text;
    }

    private static DateOnly Today => DateOnly.FromDateTime(DateTime.Today);

    private static string Month(DateOnly date) => date.ToString("MMM yyyy", CultureInfo.CurrentCulture);

    private Button More(PostgresVersions versions, PostgresMajor major)
    {
        var menu = new MenuFlyout();
        menu.Items.Add(Item("Uninstall…", () => _ = Uninstall(versions, major)));
        var button = Ui.IconButton(Ui.GlyphMore, "More", () => { });
        button.Flyout = menu;
        button.IsEnabled = !PostgresWorking;
        return button;
    }

    private Task Install(PostgresVersions versions, PostgresMajor major) =>
        Run(major.Major, versions.AnyInstalled ? "Downloading and installing…" : "Installing…", c => c.InstallPostgresAsync(major.Major));

    private async Task Switch(PostgresVersions versions, PostgresMajor major)
    {
        if (versions.Active.Length > 0 && !await Window.Confirm($"Switch to PostgreSQL {major.Major}?",
                $"PostgreSQL {versions.Active} stops; its databases stay but aren't available until you switch back. {major.Major} uses its own databases."
                + (major.HasData ? "" : " It starts with a new, empty database cluster."),
                "Switch"))
        {
            return;
        }
        await Run(major.Major, "Switching…", c => c.ActivatePostgresAsync(major.Major));
    }

    private async Task Uninstall(PostgresVersions versions, PostgresMajor major)
    {
        var next = versions.Majors.FirstOrDefault(m => m.IsInstalled && m.Major != major.Major);
        var details = !major.Active ? ""
            : next is not null ? $"PostgreSQL {major.Major} stops and PostgreSQL {next.Major} becomes the active version. "
            : $"PostgreSQL {major.Major} stops; no PostgreSQL runs until you install a version again. ";
        details += major.HasData
            ? $"Its databases stay unless you delete them below; installing {major.Major} again picks them up."
            : "It holds no databases.";

        CheckBox? delete = null;
        UIElement? extra = null;
        if (major.HasData)
        {
            delete = new CheckBox { Content = $"Also delete its databases ({Ui.Bytes(major.DataSize)})" };
            var warning = Ui.Caption("Deleted databases can't be recovered.", Ui.Caution);
            warning.Visibility = Visibility.Collapsed;
            warning.Margin = new Thickness(28, -4, 0, 0);
            delete.Checked += (_, _) => warning.Visibility = Visibility.Visible;
            delete.Unchecked += (_, _) => warning.Visibility = Visibility.Collapsed;
            extra = Ui.Column(4, delete, warning);
        }
        if (await Window.Confirm($"Uninstall PostgreSQL {major.Major}?", details, "Uninstall", destructive: true, extra: extra))
        {
            var deleteData = delete?.IsChecked == true;
            await Run(major.Major, "Uninstalling…", c => c.UninstallPostgresAsync(major.Major, deleteData));
        }
    }

    private async Task Run(string major, string step, Func<FlockClient, Task<PostgresVersions>> action)
    {
        pendingMajors[major] = step;
        Rerender();
        Kick();
        try
        {
            await Store.PerformOnPostgresAsync(action);
        }
        finally
        {
            pendingMajors.Remove(major);
            Rerender();
            Kick();
        }
    }

    // Components

    /// <summary>A component's state: its running job, a failed one, the installed version, or how to install it.</summary>
    private Border ComponentCard(Component component, string label, string uninstallQuestion, string uninstallDetails)
    {
        var id = component.Id;
        var content = Ui.Column(8);
        if (component.Description.Length > 0)
        {
            content.Children.Add(Ui.Caption(component.Description));
        }
        var actions = Ui.Row(8);

        if (component.Job is { IsRunning: true } job)
        {
            var verb = job.Action == "update" ? "Updating" : "Installing";
            var title = $"{verb} {(component.Latest.Length > 0 ? $"{component.Name} {component.Latest}" : label)}";
            var percent = job.HasProgress ? $"{Math.Round(job.Progress * 100)} %" : "";
            content.Children.Add(Ui.Spread(Ui.Text(title, "BodyStrongTextBlockStyle"), Ui.Caption(percent, wrap: false)));
            content.Children.Add(new ProgressBar { Minimum = 0, Maximum = 1, Value = job.HasProgress ? job.Progress : 0, IsIndeterminate = !job.HasProgress });
            var phase = job.PhaseText + (job.StartedAt is { Year: > 2000 } started ? $" · started {Ui.Relative(started)}" : "");
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(component)));
            actions.Children.Add(Ui.Button("Cancel", () => _ = RunComponent(id, c => c.CancelComponentJobAsync(id))));
            Ui.SetEnabled(actions, !pendingComponents.Contains(id));
            content.Children.Add(Ui.Spread(Ui.Caption(phase), actions));
            return Inner(content);
        }

        if (component.Job is { IsFailed: true } failed)
        {
            content.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 14, Ui.Critical),
                Ui.Text(failed.Action == "update" ? "The update failed" : "The installation failed", "BodyStrongTextBlockStyle")));
            var error = Ui.Caption(failed.Error.Length > 0 ? failed.Error : "Flock reported no details; the log may tell more.", Ui.Critical);
            error.IsTextSelectionEnabled = true;
            error.MaxLines = 4;
            ToolTipService.SetToolTip(error, failed.Error);
            content.Children.Add(error);
            actions.Children.Add(Ui.Button("Try Again", () => _ = RunComponent(id, c => c.InstallComponentAsync(id)), accent: true, glyph: Ui.GlyphRefresh));
            actions.Children.Add(Ui.Button("Dismiss", () => _ = RunComponent(id, c => c.DismissComponentJobAsync(id))));
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(component)));
        }
        else if (component.IsInstalled)
        {
            var status = component.Update ? Ui.Caption($"{component.Latest} available", Ui.Accent) : Ui.Caption("Up to date");
            var more = new MenuFlyout();
            more.Items.Add(Item("Show Last Log", () => _ = ShowLog(component)));
            more.Items.Add(new MenuFlyoutSeparator());
            more.Items.Add(Item("Uninstall…", async () =>
            {
                if (await Window.Confirm(uninstallQuestion, uninstallDetails, "Uninstall", destructive: true))
                {
                    await RunComponent(id, c => c.UninstallComponentAsync(id));
                }
            }));
            var moreButton = Ui.IconButton(Ui.GlyphMore, "More", () => { });
            moreButton.Flyout = more;
            moreButton.IsEnabled = !pendingComponents.Contains(id);
            content.Children.Add(Ui.Spread(Ui.Column(1, Ui.Text($"Version {component.Installed}", wrap: false), status), moreButton));
            if (component.Update)
            {
                actions.Children.Add(Ui.Button($"Update to {component.Latest}", () => _ = RunComponent(id, c => c.InstallComponentAsync(id)), glyph: Ui.GlyphDownload,
                    tooltip: component.Estimate.Length > 0 ? $"{Capitalize(component.Estimate)}; runs in the background" : "Runs in the background"));
            }
        }
        else
        {
            actions.Children.Add(Ui.Button("Install", () => _ = RunComponent(id, c => c.InstallComponentAsync(id)), glyph: Ui.GlyphDownload,
                tooltip: component.Latest.Length > 0 ? $"Downloads {component.Name} {component.Latest}" : null));
            if (component.Estimate.Length > 0)
            {
                actions.Children.Add(Ui.Caption(Capitalize(component.Estimate), wrap: false));
            }
        }

        if (actions.Children.Count > 0)
        {
            if (pendingComponents.Contains(id))
            {
                Ui.SetEnabled(actions, false);
                actions.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
            content.Children.Add(actions);
        }
        return Inner(content);
    }

    private async Task RunComponent(string id, Func<FlockClient, Task<ComponentCatalog>> action)
    {
        pendingComponents.Add(id);
        Rerender();
        try
        {
            await Store.PerformOnComponentsAsync(action);
        }
        finally
        {
            pendingComponents.Remove(id);
            Rerender();
            Kick();
        }
    }

    /// <summary>The tail of a component job's output, followed while the job runs.</summary>
    private Task ShowLog(Component component) =>
        ShowLog($"{component.Name} log", () => Store.ComponentLogAsync(component.Id), () => ComponentWorking(component.Id));

    /// <summary>The tail of an install's output, followed while working() holds.</summary>
    private async Task ShowLog(string title, Func<Task<string>> load, Func<bool> working)
    {
        var text = new TextBlock { FontFamily = Ui.Mono, FontSize = 12, IsTextSelectionEnabled = true, Text = "Loading…" };
        var scroller = new ScrollViewer
        {
            Content = text,
            Width = 680,
            Height = 380,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        var dialog = new ContentDialog
        {
            Title = title,
            Content = scroller,
            PrimaryButtonText = "Copy",
            CloseButtonText = "Done",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 760.0;
        dialog.IsPrimaryButtonEnabled = false;
        dialog.PrimaryButtonClick += (_, e) =>
        {
            e.Cancel = true; // copying keeps the dialog open
            Ui.Copy(text.Text);
        };

        using var closed = new CancellationTokenSource();
        dialog.Closed += (_, _) => closed.Cancel();
        _ = Follow();
        await dialog.ShowAsync();

        async Task Follow()
        {
            while (!closed.IsCancellationRequested)
            {
                var log = (await load()).TrimEnd();
                var shown = log.Length == 0 ? "No log yet." : log;
                if (shown != text.Text)
                {
                    text.Text = shown;
                    dialog.IsPrimaryButtonEnabled = log.Length > 0;
                    scroller.UpdateLayout();
                    scroller.ChangeView(null, scroller.ScrollableHeight, null, disableAnimation: true);
                }
                if (!working())
                {
                    return;
                }
                try
                {
                    await Task.Delay(TimeSpan.FromSeconds(2), closed.Token);
                }
                catch (TaskCanceledException)
                {
                    return;
                }
            }
        }
    }

    // Shared pieces

    /// <summary>"Last checked …" with "Check for Updates", and the last check's error, kept small.</summary>
    private StackPanel CheckFooter(DateTimeOffset? checkedAt, string checkError, bool postgres)
    {
        var text = checkedAt is { } at ? $"Last checked {Ui.Relative(at)}" : "Not checked yet";
        UIElement check = checking
            ? new ProgressRing { Width = 14, Height = 14 }
            : Ui.Link("Check for Updates", async () =>
            {
                checking = true;
                Rerender();
                try
                {
                    if (postgres)
                    {
                        await Store.PerformOnPostgresAsync(c => c.CheckPostgresReleasesAsync());
                        await Store.PerformOnComponentsAsync(c => c.CheckComponentsAsync());
                    }
                    else
                    {
                        await Store.PerformOnCatalogAsync(c => c.CheckCatalogAsync());
                    }
                }
                finally
                {
                    checking = false;
                    Rerender();
                }
            });
        var footer = Ui.Column(2, Ui.Spread(Ui.Caption(text, wrap: false), check));
        if (checkError.Length > 0)
        {
            var error = Ui.Caption($"Couldn't check: {checkError}", Ui.Caution);
            error.MaxLines = 2;
            ToolTipService.SetToolTip(error, checkError);
            footer.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), error));
        }
        return footer;
    }

    /// <summary>A surface inside the detail card.</summary>
    private static Border Inner(UIElement content, Thickness? padding = null) => new()
    {
        Child = content,
        Padding = padding ?? new Thickness(14, 12, 14, 12),
        Background = Ui.Brush("CardBackgroundFillColorSecondaryBrush"),
        BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
        BorderThickness = new Thickness(1),
        CornerRadius = new CornerRadius(6),
    };

    /// <summary>A centered call to action for something not installed yet.</summary>
    private static Border Hero(string glyph, string title, string description, params UIElement[] actions)
    {
        var panel = Ui.Column(8, Ui.Icon(glyph, 32, Ui.Accent), Ui.Text(title, "BodyStrongTextBlockStyle"));
        var caption = Ui.Caption(description);
        caption.TextAlignment = TextAlignment.Center;
        panel.Children.Add(caption);
        for (var i = 0; i < actions.Length; i++)
        {
            if (actions[i] is FrameworkElement action)
            {
                action.Margin = new Thickness(0, i == 0 ? 6 : 0, 0, 0);
            }
            panel.Children.Add(actions[i]);
        }
        foreach (var child in panel.Children.OfType<FrameworkElement>())
        {
            child.HorizontalAlignment = HorizontalAlignment.Center;
        }
        return Inner(panel, new Thickness(20, 20, 20, 18));
    }

    private static StackPanel Loading(string text) =>
        Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption(text, wrap: false));

    private static MenuFlyoutItem Item(string text, Action click)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => click();
        return item;
    }

    private static string Capitalize(string text) => text.Length == 0 ? text : char.ToUpper(text[0], CultureInfo.CurrentCulture) + text[1..];
}
