using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Settings;

/// <summary>
/// Git worktrees: the daemon serves each worktree of a site at &lt;branch&gt;.&lt;site&gt;.test and sets it
/// up (dependencies, .env, installs, hooks, optionally a database of its own). A site lists its
/// worktrees and creates new ones; a
/// worktree shows its branch and folder, its database and its setup. Its PHP, Node.js, HTTPS and
/// document root are its site's, so they are only shown here.
/// </summary>
internal sealed partial class SitesPane
{
    // The database pickers' modes and their labels, in their order.
    internal static readonly string[] DatabaseModes = [Status.WorktreeDatabase.Shared, Status.WorktreeDatabase.Copy, Status.WorktreeDatabase.Fresh];
    internal static string[] DatabaseChoices => [Strings.Worktree_DatabaseShared, Strings.Worktree_DatabaseCopy, Strings.Worktree_DatabaseFresh];

    /// <summary>A site's worktrees (each selects it) and New Worktree.</summary>
    private StackPanel Worktrees(Status.Site site)
    {
        var section = Section(Strings.Worktree_Worktrees);
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
            section.Children.Add(Ui.Caption(Localization.Format(Strings.Worktree_Explanation, site.Domain), Ui.TertiaryText));
        }
        section.Children.Add(Ui.Button(Strings.Worktree_New, () => Start(site.Name, () => NewWorktree(site)), glyph: Ui.GlyphAdd));
        return section;
    }

    /// <summary>A worktree's state in its site's list: its setup, or its own database.</summary>
    private static UIElement WorktreeSummary(Status.Site worktree) => worktree.Worktree switch
    {
        { IsBusy: true } setup => Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(setup.Step.Length > 0 ? setup.Step : Strings.Worktree_SettingUp, wrap: false)),
        { IsFailed: true } => Ui.Caption(Strings.Worktree_SetupFailed, Ui.Critical, wrap: false),
        { HasOwnDatabase: true } setup => Ui.Badge(setup.DbMode.Length > 0 ? setup.DbMode : Strings.Worktree_OwnDatabase, Ui.Tone.Neutral),
        _ => Ui.Row(0),
    };

    /// <summary>Asks for the new worktree in a dialog, then creates it; a failure shows in the dialog.</summary>
    private async Task NewWorktree(Status.Site site)
    {
        var token = activeLifetime?.Token ?? CancellationToken.None;
        var form = new NewWorktreeView(site);
        var dialog = new ContentDialog
        {
            Title = Localization.Format(Strings.Worktree_NewTitle, site.Name),
            Content = form,
            PrimaryButtonText = Strings.Worktree_Create,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 520.0;

        // Create turns the dialog into the job's progress; it stays open until it is closed.
        WorktreeJobView? job = null;
        Task? run = null;
        dialog.PrimaryButtonClick += (_, args) =>
        {
            if (job is not null)
            {
                return;
            }
            args.Cancel = true;
            var (name, start, db) = (form.Branch, form.Start, form.Database);
            job = new WorktreeJobView(dialog, Model, Localization.Format(Strings.Worktree_NewTitle, site.Name));
            run = job.RunAsync(cancellationToken => Model.Sites.CreateWorktreeAsync(site.Name, name, start, db, cancellationToken));
        };
        var load = form.LoadBranchesAsync(Model.Sites, token);
        await dialog.ShowAsync();
        await load;
        if (run is not null)
        {
            await run;
        }
        if (job?.Ready is { } created)
        {
            Select(created.Name);
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
        var heading = Ui.Column(2, Ui.Row(8, Ui.Text(label, "SubtitleTextBlockStyle", wrap: false), Ui.Badge(Strings.Worktree_Worktree, Ui.Tone.Neutral)));
        if (worktree.Url is { } url)
        {
            var link = Ui.Link(url, () => Model.Open(worktree));
            link.Padding = new Thickness(0);
            heading.Children.Add(link);
        }
        var path = Ui.Caption(worktree.Path, Ui.TertiaryText, wrap: false);
        ToolTipService.SetToolTip(path, worktree.Path);
        heading.Children.Add(path);

        var open = Ui.IconButton(Ui.GlyphOpen, Localization.Format(Strings.Common_OpenTarget, worktree.Url ?? worktree.Domain), () => Model.Open(worktree));
        open.IsEnabled = worktree.Url is not null;
        var actions = Ui.Row(0, open, Ui.IconButton(Ui.GlyphDelete, Strings.Worktree_Remove, () => Start(worktree.Name, () => RemoveWorktree(worktree))));
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
        var reveal = Ui.IconButton(Ui.GlyphFolder, Strings.Common_ShowInExplorer, () => Model.Reveal(worktree.Path));
        reveal.Padding = new Thickness(4);
        Grid.SetColumn(reveal, 1);
        folder.Children.Add(folderText);
        folder.Children.Add(reveal);
        UIElement address = worktree.Url is { } shown ? WorktreeLink(shown, () => Model.Open(worktree)) : Ui.Text(worktree.Domain, wrap: false);
        UIElement site = hasParent ? WorktreeLink(parent, () => Select(parent)) : Ui.Text(parent, wrap: false);
        panel.Children.Add(Section(Strings.Worktree_Worktree,
            Field(Strings.NewWorktree_Branch, Ui.Text(worktree.Branch.Length > 0 ? worktree.Branch : Strings.Worktree_DetachedHead, wrap: false)),
            Field(Strings.General_Folder, folder),
            Field("URL", address),
            Field(Strings.Sites_Site, site)));

        // Settings: the site's, so they are changed there.
        var settings = Section(Strings.Worktree_Settings,
            Field("PHP", Ui.Text($"PHP {worktree.Php}", wrap: false)),
            Field("Node.js", Ui.Text(worktree.Node is { Length: > 0 } node ? $"Node {node}" : Strings.Worktree_None, wrap: false)),
            Field("HTTPS", Ui.Text(worktree.Secured ? Strings.Worktree_On : Strings.Worktree_Off, wrap: false)));
        settings.Children.Add(Ui.Caption(Localization.Format(Strings.Worktree_UsesSiteSettings, parent), Ui.TertiaryText));
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
        var picker = new ComboBox { MinWidth = 150, PlaceholderText = Strings.Worktree_ProjectDefault };
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
        Ui.AutomationName(picker, Strings.NewWorktree_Database);
        if (setup is null)
        {
            ToolTipService.SetToolTip(picker, Strings.Worktree_SetUpFirst);
        }

        var current = setup is { HasOwnDatabase: true } ? setup.Database : Strings.Worktree_DatabaseShared;
        var section = Section(Strings.NewWorktree_Database, Field(Strings.Worktree_Current, Ui.Text(current, wrap: false, selectable: true)),
            Field(Strings.NewWorktree_Database, picker));
        section.Children.Add(Ui.Caption(Strings.Worktree_SharedDatabaseNote, Ui.TertiaryText));
        return section;
    }

    /// <summary>Confirms switching the worktree's database (dropping its own), then switches it as the worktree's job.</summary>
    private async Task SwitchDatabase(Status.Site worktree, string mode)
    {
        var label = worktree.WorktreeLabel;
        var parent = worktree.Parent;
        if (worktree.Worktree is { HasOwnDatabase: true } setup)
        {
            var dropped = mode switch
            {
                Status.WorktreeDatabase.Copy => Strings.Worktree_DropDetailsCopy,
                Status.WorktreeDatabase.Fresh => Strings.Worktree_DropDetailsFresh,
                _ => Strings.Worktree_DropDetailsShared,
            };
            if (!await Window.Confirm(Localization.Format(Strings.Worktree_DropQuestion, setup.Database), Localization.Format(dropped, label, parent, setup.Database),
                    Strings.Worktree_Switch, destructive: true))
            {
                return;
            }
        }
        else
        {
            var (question, details) = mode switch
            {
                Status.WorktreeDatabase.Copy => (Strings.Worktree_GiveCopyQuestion, Strings.Worktree_GiveCopyDetails),
                Status.WorktreeDatabase.Fresh => (Strings.Worktree_GiveFreshQuestion, Strings.Worktree_GiveFreshDetails),
                _ => (Strings.Worktree_GiveSharedQuestion, Strings.Worktree_GiveSharedDetails),
            };
            if (!await Window.Confirm(Localization.Format(question, label, parent), details, Strings.Worktree_Switch))
            {
                return;
            }
        }
        await FollowJob(Localization.Format(Strings.Worktree_SwitchingDatabase, label), worktree.Path, token => Model.Sites.SwitchWorktreeDatabaseAsync(worktree.Name, worktree.Path, mode, token));
    }

    /// <summary>The worktree's setup: queued, running (the step), set up or failed (why), with its log and Run Setup Again.</summary>
    private StackPanel WorktreeSetup(Status.Site worktree)
    {
        var section = Section(Strings.Worktree_Setup);
        if (worktree.Worktree is not { } setup)
        {
            section.Children.Add(Ui.Caption(Strings.Worktree_NotSetUp));
            section.Children.Add(Ui.Button(Strings.Worktree_SetUpAction, () => Start(worktree.Name, () => FollowJob(Localization.Format(Strings.Worktree_SettingUpTitle, worktree.WorktreeLabel), worktree.Path,
                token => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again: false, token)))));
            return section;
        }

        if (setup.IsBusy)
        {
            var step = setup.State == Status.WorktreeState.Queued ? Strings.Worktree_Waiting
                : setup.Step.Length > 0 ? setup.Step + "…" : Strings.Worktree_SettingUp;
            section.Children.Add(Ui.Caption(step, wrap: false));
            section.Children.Add(new ProgressBar { IsIndeterminate = true, HorizontalAlignment = HorizontalAlignment.Left, Width = 320 });
        }
        else if (setup.IsFailed)
        {
            section.Children.Add(Warning(setup.Error.Length > 0 ? setup.Error : Strings.Worktree_SetupFailedSentence));
            section.Children.Add(Ui.Caption(Strings.Worktree_StillServed));
        }
        else
        {
            section.Children.Add(Ui.Row(6, Ui.StatusDot(true), Ui.Caption(Strings.Worktree_IsSetUp, wrap: false)));
        }

        var buttons = Ui.Row(8);
        if (setup.Log.Length > 0)
        {
            var name = worktree.Name;
            var log = setup.Log;
            buttons.Children.Add(Ui.Button(Strings.Sites_ShowLog, () => Start("log:" + name, () => JobLogDialog.ShowAsync(Window,
                Localization.Format(Strings.Worktree_SetupLogTitle, worktree.WorktreeLabel),
                token => Model.Client.ServiceLogAsync(log, 400, token),
                () => Model.Status?.Sites.FirstOrDefault(s => s.Name == name)?.Worktree?.IsBusy == true))));
        }
        var again = Ui.Button(Strings.Worktree_RunAgain, () => Start(worktree.Name, () => FollowJob(Localization.Format(Strings.Worktree_SettingUpTitle, worktree.WorktreeLabel),
            worktree.Path, token => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again: true, token))),
            tooltip: Strings.Worktree_RunAgainTooltip);
        again.IsEnabled = !setup.IsBusy;
        buttons.Children.Add(again);
        section.Children.Add(buttons);
        return section;
    }

    /// <summary>Starts a job of the worktree at path (its error, or null) and follows it in a progress dialog.</summary>
    private Task FollowJob(string title, string path, Func<CancellationToken, Task<string?>> start) =>
        WorktreeJobView.ShowAsync(Window, Model, title, async token => (path, await start(token)));

    /// <summary>
    /// Removes the worktree with git (after its before_delete hook), and its own database; the
    /// branch stays. When git refuses (e.g. uncommitted changes), offers to remove it anyway.
    /// </summary>
    private async Task RemoveWorktree(Status.Site worktree)
    {
        var label = worktree.WorktreeLabel;
        var details = worktree.Worktree is { HasOwnDatabase: true } setup
            ? Localization.Format(Strings.Worktree_RemoveDetailsDatabase, worktree.Path, setup.Database)
            : Localization.Format(Strings.Worktree_RemoveDetails, worktree.Path);
        if (worktree.Branch.Length > 0)
        {
            details += " " + Localization.Format(Strings.Worktree_BranchStays, worktree.Branch);
        }
        if (!await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, label), details, Strings.Common_Remove, destructive: true))
        {
            return;
        }
        var error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: false);
        if (error is not null)
        {
            if (!await Window.Confirm(Localization.Format(Strings.Worktree_CouldNotRemove, label), error, Strings.Worktree_RemoveAnyway, destructive: true))
            {
                return;
            }
            error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: true);
            if (error is not null)
            {
                await Ui.Inform(Window.XamlRoot, Localization.Format(Strings.Worktree_CouldNotRemove, label), error);
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
