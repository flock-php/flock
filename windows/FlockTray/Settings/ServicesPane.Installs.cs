using System.Globalization;
using Flock.Kit;
using Flock.Tray.Infrastructure;
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
    private bool checking;
    private readonly Dictionary<string, Border> installCards = [];
    // The poll loop's wait; cancelled to look again right away after starting something.
    private CancellationTokenSource? nap;

    /// <summary>Some PostgreSQL operation runs: other PostgreSQL actions wait for it.</summary>
    private bool PostgresWorking => Model.Postgres.IsRunning || Model.Postgres.Current?.AnyBusy == true;


    private static bool IsInstallable(Service service) => service.Id == "postgres" || service.IsOnRequest;

    /// <summary>
    /// Polls every 2 s while something installs; otherwise the store reloads when the service list
    /// changes, and a slow poll catches jobs that the CLI started.
    /// </summary>
    private async Task PollAsync(CancellationToken hidden)
    {
        while (!hidden.IsCancellationRequested)
        {
            await rows.RefreshAsync(hidden);
            var fast = PostgresWorking || Model.Catalog.Current?.HasRunningJob == true;
            using var wake = CancellationTokenSource.CreateLinkedTokenSource(hidden);
            nap = wake;
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(fast ? 2 : 30), Model.TimeProvider, wake.Token);
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
            if (Model.Postgres.Current?.Majors.FirstOrDefault(m => m.Update) is { } major)
            {
                return major.Latest == major.Installed ? $"A new build of PostgreSQL {major.Latest} is available" : $"PostgreSQL {major.Latest} is available";
            }
        }
        if (Model.Catalog.Current?[service.Id] is { } entry && entry.Lines.FirstOrDefault(l => l.Update) is { } line)
        {
            return $"{entry.Name} {line.Latest} is available";
        }
        return null;
    }

    /// <summary>What a service row says while something about it runs; null when idle.</summary>
    private string? WorkText(Service service)
    {
        if (service.Id == "postgres")
        {
            if (Model.Postgres.Current?.Majors.FirstOrDefault(m => m.IsBusy) is { } busy)
            {
                return busy.BusyText;
            }
        }
        if (CatalogWorking(service.Id))
        {
            return Model.Catalog.Current?[service.Id]?.Job?.PhaseText ?? "Working…";
        }
        return null;
    }

    // PostgreSQL

    private void PostgresDetail(StackPanel panel)
    {
        if (Model.Postgres.Current is not { } versions)
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

    /// <summary>Nothing installed yet: the version and port, and Install, as for the catalog services.</summary>
    private Border PostgresEmpty(PostgresVersions versions)
    {
        if (versions.Majors.Count == 0)
        {
            return Inner(Ui.Caption("Flock offers no PostgreSQL versions for this computer."));
        }
        var options = versions.Majors.Select(m => (m.Major, MajorLabel(m), m.Latest)).ToList();
        var hero = InstallCard("postgres", "PostgreSQL",
            "The database for your apps. You can add other versions later; one runs at a time, and each keeps its own databases.",
            options, versions.Majors[0].Major, versions.Port, null, "", (major, port) => InstallFirst(versions, major, port));
        var kept = versions.Majors.Where(m => m.HasData).Select(m => m.Major).ToList();
        if (kept.Count > 0)
        {
            var note = Ui.Caption($"Databases of PostgreSQL {string.Join(", ", kept)} are still here — installing that version again picks them up.");
            note.TextAlignment = TextAlignment.Center;
            ((Panel)hero.Child).Children.Add(note);
        }
        return hero;
    }

    /// <summary>"17 (17.6)", with the kept databases or a near end of life.</summary>
    private static string MajorLabel(PostgresMajor major)
    {
        var label = major.Latest.Length > 0 ? $"{major.Major} ({major.Latest})" : major.Major;
        return major.HasData ? label + " — your databases" : label;
    }

    /// <summary>The first install: the port belongs to PostgreSQL, not to a version, so it is set first.</summary>
    private async Task InstallFirst(PostgresVersions versions, string? major, int? port)
    {
        var chosen = versions.Majors.FirstOrDefault(m => m.Major == major) ?? versions.Majors[0];
        await Model.Postgres.InstallFirstAsync(chosen.Major, port, versions.Port);
    }

    /// <summary>The very first install: it downloads, creates the cluster and starts it.</summary>
    private Border FirstInstall(PostgresVersions versions)
    {
        var major = versions.Majors.FirstOrDefault(m => m.IsBusy);
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

    private StackPanel VersionList(PostgresVersions versions) =>
        Ui.Column(6, Ui.Text("Versions", "BodyStrongTextBlockStyle"), VersionRows(versions.Majors.Select(m => MajorRow(versions, m))),
            Ui.Caption("Installed versions stay side by side. One runs at a time; each keeps its own databases, and updates within a version keep them."));

    private string? BusyText(PostgresMajor major) =>
        major.IsBusy ? major.BusyText : null;

    /// <summary>A major with its actions on the right, laid out like a catalog service's line.</summary>
    private Grid MajorRow(PostgresVersions versions, PostgresMajor major)
    {
        var title = Ui.Row(8, Ui.Text($"PostgreSQL {major.Major}", major.IsInstalled ? "BodyStrongTextBlockStyle" : "BodyTextBlockStyle", wrap: false));
        if (major.Active)
        {
            title.Children.Add(Ui.Badge("Active"));
        }
        var info = Ui.Column(2, title, Facts(major));
        var actions = Ui.Row(8);
        if (BusyText(major) is { } busy)
        {
            actions.Children.Add(new ProgressRing { Width = 14, Height = 14 });
            actions.Children.Add(Ui.Caption(busy, wrap: false));
        }
        else if (!major.IsInstalled)
        {
            var tip = (major.Latest.Length > 0 ? $"Downloads PostgreSQL {major.Latest}." : "Downloads the newest release.")
                + (versions.Active.Length > 0 ? $" PostgreSQL {versions.Active} stays active until you switch." : "")
                + (major.HasData ? " Its databases are still here and come back." : "");
            actions.Children.Add(Ui.Button("Install", () => _ = InstallAsync(versions, major), glyph: Ui.GlyphDownload, tooltip: tip));
        }
        else
        {
            if (major.Update)
            {
                var restart = major.Active && versions.Running ? "; PostgreSQL restarts for a moment" : "";
                actions.Children.Add(Ui.Button($"Update to {major.Latest}", () => _ = RunAsync(major.Major, "Updating…", () => Model.Postgres.UpdateAsync(major.Major)),
                glyph: Ui.GlyphDownload, tooltip: $"Installs {major.Latest} in place of {major.Installed}. Your databases stay{restart}."));
            }
            if (!major.Active)
            {
                actions.Children.Add(Ui.Button("Switch…", () => _ = SwitchAsync(versions, major),
                    tooltip: versions.Active.Length > 0 ? $"Stops PostgreSQL {versions.Active} and starts {major.Major} with its own databases" : $"Starts PostgreSQL {major.Major}"));
            }
            actions.Children.Add(More(versions, major));
        }
        // One PostgreSQL operation at a time.
        Ui.SetEnabled(actions, !PostgresWorking);
        return VersionRow(info, actions);
    }

    /// <summary>"18.6.0 · 18.7.0 available · 46 MB of data · supported until Nov 2030", end of life tinted when near.</summary>
    private static TextBlock Facts(PostgresMajor major)
    {
        // Built from runs; not Ui.Caption(""), whose empty run made the text start with " · ".
        var text = Ui.Caption("x");
        text.Inlines.Clear();
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
        return text;
    }

    private Button More(PostgresVersions versions, PostgresMajor major)
    {
        var menu = new MenuFlyout();
        menu.Items.Add(Item("Uninstall…", () => _ = UninstallAsync(versions, major)));
        var button = Ui.IconButton(Ui.GlyphMore, "More", () => { });
        button.Flyout = menu;
        button.IsEnabled = !PostgresWorking;
        return button;
    }

    private Task InstallAsync(PostgresVersions versions, PostgresMajor major) =>
        RunAsync(major.Major, versions.AnyInstalled ? "Downloading and installing…" : "Installing…", () => Model.Postgres.InstallAsync(major.Major));

    private async Task SwitchAsync(PostgresVersions versions, PostgresMajor major)
    {
        if (versions.Active.Length > 0 && !await Window.Confirm($"Switch to PostgreSQL {major.Major}?",
                $"PostgreSQL {versions.Active} stops; its databases stay but aren't available until you switch back. {major.Major} uses its own databases."
                + (major.HasData ? "" : " It starts with a new, empty database cluster."),
                "Switch"))
        {
            return;
        }
        await RunAsync(major.Major, "Switching…", () => Model.Postgres.ActivateAsync(major.Major));
    }

    private async Task UninstallAsync(PostgresVersions versions, PostgresMajor major)
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
            await RunAsync(major.Major, "Uninstalling…", () => Model.Postgres.UninstallAsync(major.Major, deleteData));
        }
    }

    private static async Task RunAsync(string major, string step, Func<Task<OperationOutcome>> action) => await action();

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
                        await Model.Postgres.CheckAsync();
                    }
                    else
                    {
                        await Model.Catalog.CheckAsync();
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

    /// <summary>
    /// Installing a service that isn't installed yet, the same for PostgreSQL and the catalog
    /// services: the version (when there is a choice) and the port, then Install.
    /// </summary>
    private Border InstallCard(string id, string name, string description, List<(string Id, string Label, string Latest)> versions, string? preferred,
        int port, string? portNote, string estimate, Func<string?, int?, Task> install)
    {
        if (installCards.TryGetValue(id, out var existing))
        {
            return existing;
        }
        var form = Ui.Column(10);
        var picker = new ComboBox { Header = "Version", MinWidth = 220 };
        foreach (var (version, label, _) in versions)
        {
            picker.Items.Add(new ComboBoxItem { Content = label, Tag = version });
        }
        var catalogDraft = rows.CatalogServices.FirstOrDefault(service => service.Id == id);
        var picked = id == "postgres" ? rows.PostgresDraftMajor : catalogDraft?.DraftLine ?? preferred;
        picker.SelectedItem = picker.Items.OfType<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == picked) ?? picker.Items.FirstOrDefault();
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedItem is ComboBoxItem { Tag: string version })
            {
                if (id == "postgres")
                {
                    rows.PostgresDraftMajor = version;
                }
                else if (catalogDraft is not null)
                {
                    catalogDraft.DraftLine = version;
                }
            }
        };
        // With one version there's nothing to pick, but the box still says what installs.
        picker.IsEnabled = versions.Count > 1;
        form.Children.Add(picker);

        var portBox = new NumberBox
        {
            Header = "Port",
            Value = id == "postgres" ? rows.PostgresDraftPort : catalogDraft?.DraftPort ?? port,
            Minimum = 1024,
            Maximum = 65535,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
            MinWidth = 140,
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        portBox.ValueChanged += (_, _) =>
        {
            if (double.IsNaN(portBox.Value))
            {
                return;
            }
            if (id == "postgres")
            {
                rows.PostgresDraftPort = (int)portBox.Value;
            }
            else if (catalogDraft is not null)
            {
                catalogDraft.DraftPort = (int)portBox.Value;
            }
        };
        form.Children.Add(portBox);
        if (portNote is not null)
        {
            form.Children.Add(Ui.Caption(portNote));
        }

        var buttonText = Ui.Text($"Install {name}");
        var button = new Button
        {
            Content = Ui.Row(8, Ui.Icon(Ui.GlyphDownload, 14), buttonText),
            Style = Ui.Resource<Style>("AccentButtonStyle"),
        };
        button.Click += (_, _) =>
        {
            var version = (picker.SelectedItem as ComboBoxItem)?.Tag as string;
            int? chosen = double.IsNaN(portBox.Value) ? null : (int)portBox.Value;
            _ = install(version, chosen);
        };
        // "Install MySQL 8.4.7": the release the picked version downloads.
        void Label()
        {
            var latest = versions.FirstOrDefault(v => v.Id == (picker.SelectedItem as ComboBoxItem)?.Tag as string).Latest;
            buttonText.Text = string.IsNullOrEmpty(latest) ? $"Install {name}" : $"Install {name} {latest}";
        }
        Label();
        picker.SelectionChanged += (_, _) => Label();
        UIElement[] actions = estimate.Length > 0 ? [form, button, Ui.Caption(Capitalize(estimate), wrap: false)] : [form, button];
        var card = Hero(Ui.GlyphServer, $"Install {name}", description, actions);
        installCards.Add(id, card);
        return card;
    }

    /// <summary>Version rows in one surface, divided like the macOS list.</summary>
    private static Border VersionRows(IEnumerable<UIElement> rows)
    {
        var list = new StackPanel();
        foreach (var row in rows)
        {
            if (list.Children.Count > 0)
            {
                list.Children.Add(new Border { Height = 1, Background = Ui.Brush("DividerStrokeColorDefaultBrush") });
            }
            list.Children.Add(row);
        }
        return Inner(list, new Thickness(14, 2, 8, 2));
    }

    /// <summary>One version: what it is on the left, its buttons on the right.</summary>
    private static Grid VersionRow(FrameworkElement info, FrameworkElement actions)
    {
        var row = new Grid { ColumnSpacing = 8, Padding = new Thickness(0, 10, 0, 10) };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        info.VerticalAlignment = VerticalAlignment.Center;
        actions.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(actions, 1);
        row.Children.Add(info);
        row.Children.Add(actions);
        return row;
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