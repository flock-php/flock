using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Git worktrees: the daemon serves each worktree of a site at &lt;branch&gt;.&lt;site&gt;.test and sets it
/// up (dependencies, .env, installs, hooks, optionally a database of its own). A site lists its
/// worktrees and creates new ones; a
/// worktree shows its branch and folder, its database and its setup. Its PHP, Node.js, HTTPS and
/// document root are its site's, so they are only shown here.
/// </summary>
internal sealed partial class SitesPane
{
    // A worktree created here, selected once the status lists it.
    private string? selectWhenListed;

    // The database pickers' modes and their labels, in their order.
    private static readonly string[] DatabaseModes = [Status.WorktreeDatabase.Shared, Status.WorktreeDatabase.Copy, Status.WorktreeDatabase.Fresh];
    private static readonly string[] DatabaseChoices = ["The site's", "A copy of the site's", "A new, empty one"];

    /// <summary>Selects the worktree created here once the status lists it (the daemon may list it a moment later).</summary>
    private void SelectCreatedWorktree()
    {
        if (selectWhenListed is { } path && Model.Status?.Sites.FirstOrDefault(s => s.IsWorktree && SamePath(s.Path, path)) is { } created)
        {
            selectWhenListed = null;
            Select(created.Name);
        }
    }

    /// <summary>A site's worktrees (each selects it) and New Worktree.</summary>
    private StackPanel Worktrees(Status.Site site)
    {
        var section = Section("Worktrees");
        var worktrees = Model.Status?.WorktreesOf(site.Name) ?? [];
        foreach (var worktree in worktrees)
        {
            var name = worktree.Name;
            var link = Ui.Link(worktree.WorktreeLabel, () => Select(name));
            link.Padding = new Thickness(0);
            ToolTipService.SetToolTip(link, worktree.Path);
            var domain = Ui.Caption(worktree.Domain, Ui.TertiaryText, wrap: false);
            section.Children.Add(Ui.Spread(Ui.Row(8, link, domain), WorktreeSummary(worktree)));
        }
        if (worktrees.Count == 0)
        {
            section.Children.Add(Ui.Caption($"Every git worktree of the project is served at <branch>.{site.Domain}, with its own dependencies and .env.", Ui.TertiaryText));
        }
        section.Children.Add(Ui.Button("New Worktree…", () => Start(site.Name, () => NewWorktree(site)), glyph: Ui.GlyphAdd));
        return section;
    }

    /// <summary>A worktree's state in its site's list: its setup, or its own database.</summary>
    private static UIElement WorktreeSummary(Status.Site worktree) => worktree.Worktree switch
    {
        { IsBusy: true } setup => Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(setup.Step.Length > 0 ? setup.Step : "Setting up…", wrap: false)),
        { IsFailed: true } => Ui.Caption("Setup failed", Ui.Critical, wrap: false),
        { HasOwnDatabase: true } setup => Ui.Badge(setup.DbMode.Length > 0 ? setup.DbMode : "own database", Ui.Tone.Neutral),
        _ => Ui.Row(0),
    };

    /// <summary>Asks for the branch, where it starts and the database, then creates the worktree; a failure shows in the dialog.</summary>
    private async Task NewWorktree(Status.Site site)
    {
        var branch = new TextBox { Header = "Branch", PlaceholderText = "feature/login" };
        var from = new TextBox { Header = "Start from", PlaceholderText = "current commit" };
        var database = new ComboBox { Header = "Database", MinWidth = 200 };
        database.Items.Add("Project default");
        foreach (var item in DatabaseChoices)
        {
            database.Items.Add(item);
        }
        database.SelectedIndex = 0;
        var working = Ui.Row(8, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption("Creating the worktree…", wrap: false));
        working.Visibility = Visibility.Collapsed;
        var error = Ui.Caption("", Ui.Critical);
        error.IsTextSelectionEnabled = true;
        error.Visibility = Visibility.Collapsed;
        var dialog = new ContentDialog
        {
            Title = $"New worktree of {site.Name}",
            Content = Ui.Column(12,
                Ui.Column(4, branch, Ui.Caption("A branch that exists is checked out. A new one starts from the commit, branch or tag below.", Ui.TertiaryText)),
                from,
                Ui.Column(4, database, Ui.Caption("A database of its own gets the migrations run. Project default is what flock.yml says, else the site's.", Ui.TertiaryText)),
                working, error),
            PrimaryButtonText = "Create",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            IsPrimaryButtonEnabled = false,
            XamlRoot = Window.XamlRoot,
        };
        branch.TextChanged += (_, _) => dialog.IsPrimaryButtonEnabled = branch.Text.Trim().Length > 0;

        string? created = null;
        dialog.PrimaryButtonClick += async (_, args) =>
        {
            // The dialog stays open while git creates the worktree, and shows why it couldn't.
            var deferral = args.GetDeferral();
            dialog.IsPrimaryButtonEnabled = false;
            working.Visibility = Visibility.Visible;
            error.Visibility = Visibility.Collapsed;
            try
            {
                var start = from.Text.Trim();
                var db = database.SelectedIndex > 0 ? DatabaseModes[database.SelectedIndex - 1] : null;
                (created, var failure) = await Model.Sites.CreateWorktreeAsync(site.Name, branch.Text.Trim(), start.Length > 0 ? start : null, db,
                    activeLifetime?.Token ?? CancellationToken.None);
                if (failure is not null)
                {
                    args.Cancel = true;
                    error.Text = failure;
                    error.Visibility = Visibility.Visible;
                }
            }
            finally
            {
                working.Visibility = Visibility.Collapsed;
                dialog.IsPrimaryButtonEnabled = branch.Text.Trim().Length > 0;
                deferral.Complete();
            }
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary && created is not null)
        {
            // The daemon may list it a moment later.
            selectWhenListed = created;
            SelectCreatedWorktree();
        }
    }

    /// <summary>
    /// The selected worktree: its branch, folder and address, its site's settings (read-only), its
    /// database and its setup.
    /// </summary>
    private Border WorktreeDetail(Status.Site worktree)
    {
        var parent = worktree.Parent!;
        var hasParent = Model.Status?.Sites.Any(s => s.Name == parent) == true;
        var label = worktree.WorktreeLabel;
        var panel = new StackPanel { Spacing = 20 };

        // Header: branch, address and folder, and the worktree's actions.
        var heading = Ui.Column(2, Ui.Row(8, Ui.Text(label, "SubtitleTextBlockStyle", wrap: false), Ui.Badge("Worktree", Ui.Tone.Neutral)));
        if (worktree.Url is { } url)
        {
            var link = Ui.Link(url, () => Model.Open(worktree));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var path = Ui.Caption(worktree.Path, Ui.TertiaryText, wrap: false);
        ToolTipService.SetToolTip(path, worktree.Path);
        heading.Children.Add(path);

        var open = Ui.IconButton(Ui.GlyphOpen, $"Open {worktree.Url ?? worktree.Domain}", () => Model.Open(worktree));
        open.IsEnabled = worktree.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphDelete, "Remove worktree", () => Start(worktree.Name, () => RemoveWorktree(worktree))));
        actions.VerticalAlignment = VerticalAlignment.Top;
        var header = new Grid { ColumnSpacing = 8 };
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(actions, 1);
        header.Children.Add(heading);
        header.Children.Add(actions);
        panel.Children.Add(header);
        if (worktree.Problem is { } problem)
        {
            panel.Children.Add(Warning(problem));
        }

        // Worktree: its branch, folder and site.
        var folder = new Grid { ColumnSpacing = 4 };
        folder.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        folder.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var folderText = Ui.Text(worktree.Path, wrap: false);
        folderText.TextTrimming = TextTrimming.CharacterEllipsis;
        folderText.VerticalAlignment = VerticalAlignment.Center;
        ToolTipService.SetToolTip(folderText, worktree.Path);
        var reveal = Ui.IconButton(Ui.GlyphFolder, "Show in Explorer", () => Model.Reveal(worktree.Path));
        reveal.Padding = new Thickness(4);
        Grid.SetColumn(reveal, 1);
        folder.Children.Add(folderText);
        folder.Children.Add(reveal);
        UIElement address = worktree.Url is { } shown ? WorktreeLink(shown, () => Model.Open(worktree)) : Ui.Text(worktree.Domain, wrap: false);
        UIElement site = hasParent ? WorktreeLink(parent, () => Select(parent)) : Ui.Text(parent, wrap: false);
        panel.Children.Add(Section("Worktree",
            Field("Branch", Ui.Text(worktree.Branch.Length > 0 ? worktree.Branch : "None (detached HEAD)", wrap: false)),
            Field("Folder", folder),
            Field("URL", address),
            Field("Site", site)));

        // Settings: the site's, so they are changed there.
        var settings = Section("Settings",
            Field("PHP", Ui.Text($"PHP {worktree.Php}", wrap: false)),
            Field("Node.js", Ui.Text(worktree.Node is { Length: > 0 } node ? $"Node {node}" : "None", wrap: false)),
            Field("HTTPS", Ui.Text(worktree.Secured ? "On" : "Off", wrap: false)));
        settings.Children.Add(Ui.Caption($"Uses {parent}'s settings: PHP, Node.js, HTTPS and the document root (versions the branch pins apply too). Change them on {parent}.", Ui.TertiaryText));
        panel.Children.Add(settings);

        panel.Children.Add(WorktreeDatabase(worktree));
        panel.Children.Add(WorktreeSetup(worktree));

        var card = Ui.Card(panel, new Thickness(20, 16, 16, 20));
        card.VerticalAlignment = VerticalAlignment.Top;
        return card;
    }

    private static HyperlinkButton WorktreeLink(string text, Action onClick)
    {
        var link = Ui.Link(text, onClick);
        link.Padding = new Thickness(0);
        return link;
    }

    /// <summary>The worktree's database: the site's or its own, and a picker that switches it.</summary>
    private StackPanel WorktreeDatabase(Status.Site worktree)
    {
        var setup = worktree.Worktree;
        var picker = new ComboBox { MinWidth = 150, PlaceholderText = "Project default" };
        foreach (var item in DatabaseChoices)
        {
            picker.Items.Add(item);
        }
        // A mode left to flock.yml is unknown here, unless the worktree uses the site's database.
        var selected = Array.IndexOf(DatabaseModes, setup?.DbMode ?? "");
        if (selected < 0 && setup is not { HasOwnDatabase: true })
        {
            selected = 0;
        }
        picker.SelectedIndex = selected;
        picker.IsEnabled = setup is { IsBusy: false };
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedIndex < 0 || picker.SelectedIndex == selected)
            {
                return;
            }
            var mode = DatabaseModes[picker.SelectedIndex];
            // Shown again once the daemon has switched it; until then, and when cancelled, the current one.
            picker.SelectedIndex = selected;
            Start(worktree.Name, () => SwitchDatabase(worktree, mode));
        };
        Ui.AutomationName(picker, "Database");
        if (setup is null)
        {
            ToolTipService.SetToolTip(picker, "Set the worktree up first.");
        }

        var current = setup is { HasOwnDatabase: true } ? setup.Database : "The site's";
        var section = Section("Database", Field("Current", Ui.Text(current, wrap: false, selectable: true)), Field("Database", picker));
        section.Children.Add(Ui.Caption("With the site's database, migrations you run in the worktree change the site's too.", Ui.TertiaryText));
        return section;
    }

    /// <summary>Confirms switching the worktree's database (dropping its own), then switches it as the worktree's job.</summary>
    private async Task SwitchDatabase(Status.Site worktree, string mode)
    {
        var label = worktree.WorktreeLabel;
        var parent = worktree.Parent;
        var what = mode switch
        {
            Status.WorktreeDatabase.Copy => $"a copy of {parent}'s database",
            Status.WorktreeDatabase.Fresh => "an empty database",
            _ => $"{parent}'s database",
        };
        if (worktree.Worktree is { HasOwnDatabase: true } setup)
        {
            if (!await Window.Confirm($"Drop {setup.Database}?", $"{label} then uses {what}. Its database {setup.Database} is dropped, with its data.", "Switch", destructive: true))
            {
                return;
            }
        }
        else
        {
            var details = mode switch
            {
                Status.WorktreeDatabase.Copy => "Flock copies the site's database and runs the branch's new migrations on it.",
                Status.WorktreeDatabase.Fresh => "Flock creates an empty database and runs the migrations.",
                _ => "Migrations you run in the worktree then change the site's database too.",
            };
            if (!await Window.Confirm($"Give {label} {what}?", details, "Switch"))
            {
                return;
            }
        }
        await Model.Sites.SwitchWorktreeDatabaseAsync(worktree.Name, worktree.Path, mode);
    }

    /// <summary>The worktree's setup: queued, running (the step), set up or failed (why), with its log and Run Setup Again.</summary>
    private StackPanel WorktreeSetup(Status.Site worktree)
    {
        var section = Section("Setup");
        if (worktree.Worktree is not { } setup)
        {
            section.Children.Add(Ui.Caption("Flock serves this worktree, but hasn't set it up: its dependencies, .env and database are as they were."));
            section.Children.Add(Ui.Button("Set Up", () => Start(worktree.Name, () => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again: false))));
            return section;
        }

        if (setup.IsBusy)
        {
            var step = setup.State == Status.WorktreeState.Queued ? "Waiting for another setup to finish…"
                : setup.Step.Length > 0 ? setup.Step + "…" : "Setting up…";
            section.Children.Add(Ui.Caption(step, wrap: false));
            section.Children.Add(new ProgressBar { IsIndeterminate = true, HorizontalAlignment = HorizontalAlignment.Left, Width = 320 });
        }
        else if (setup.IsFailed)
        {
            section.Children.Add(Warning(setup.Error.Length > 0 ? setup.Error : "The setup failed."));
            section.Children.Add(Ui.Caption("The worktree is still served."));
        }
        else
        {
            section.Children.Add(Ui.Row(6, Ui.StatusDot(true), Ui.Caption("Set up", wrap: false)));
        }

        var buttons = Ui.Row(8);
        if (setup.Log.Length > 0)
        {
            var name = worktree.Name;
            var log = setup.Log;
            buttons.Children.Add(Ui.Button("Show Log", () => Start("log:" + name, () => JobLogDialog.ShowAsync(Window, $"Setup of {worktree.WorktreeLabel}",
                token => Model.Client.ServiceLogAsync(log, 400, token),
                () => Model.Status?.Sites.FirstOrDefault(s => s.Name == name)?.Worktree?.IsBusy == true))));
        }
        var again = Ui.Button("Run Setup Again", () => Start(worktree.Name, () => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again: true)),
            tooltip: "Copy what's missing, install, set up the database and run the hooks again");
        again.IsEnabled = !setup.IsBusy;
        buttons.Children.Add(again);
        section.Children.Add(buttons);
        return section;
    }

    /// <summary>
    /// Removes the worktree with git (after its before_delete hook), and its own database; the
    /// branch stays. When git refuses (e.g. uncommitted changes), offers to remove it anyway.
    /// </summary>
    private async Task RemoveWorktree(Status.Site worktree)
    {
        var label = worktree.WorktreeLabel;
        var details = $"Flock runs its before_delete hook and deletes {worktree.Path}"
            + (worktree.Worktree is { HasOwnDatabase: true } setup ? $" and its database {setup.Database}." : ".")
            + (worktree.Branch.Length > 0 ? $" The branch {worktree.Branch} stays." : "");
        if (!await Window.Confirm($"Remove {label}?", details, "Remove", destructive: true))
        {
            return;
        }
        var error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: false);
        if (error is not null)
        {
            if (!await Window.Confirm($"Couldn't remove {label}", error, "Remove Anyway", destructive: true))
            {
                return;
            }
            error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: true);
            if (error is not null)
            {
                await Ui.Inform(Window.XamlRoot, $"Couldn't remove {label}", error);
                return;
            }
        }
        if (worktree.Parent is { } parent && Model.Status?.Sites.Any(s => s.Name == parent) == true)
        {
            Select(parent);
        }
    }

    // Windows paths: either slash, any case.
    private static bool SamePath(string a, string b) =>
        string.Equals(a.Replace('/', '\\').TrimEnd('\\'), b.Replace('/', '\\').TrimEnd('\\'), StringComparison.OrdinalIgnoreCase);
}
