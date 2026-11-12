using Flock.Kit;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Settings;

/// <summary>
/// Git worktrees: the daemon serves each worktree of a site at &lt;branch&gt;.&lt;site&gt;.test and sets it
/// up (dependencies, .env, installs, hooks, optionally a database of its own). A site lists its
/// worktrees and creates new ones; a worktree shows its branch and folder, its database and its setup
/// (see WorktreeDetailViewModel). These are its dialogs and jobs.
/// </summary>
internal sealed partial class SitesPane
{
    /// <summary>Asks for the new worktree in a dialog, then creates it; a failure shows in the dialog.</summary>
    private async Task NewWorktree(Status.Site site)
    {
        var token = activeLifetime?.Token ?? CancellationToken.None;
        var form = new NewWorktreeView(site);
        var dialog = new ContentDialog
        {
            Title = $"New worktree of {site.Name}",
            Content = form,
            PrimaryButtonText = "Create",
            CloseButtonText = "Cancel",
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
            job = new WorktreeJobView(dialog, Model, $"New Worktree of {site.Name}");
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
        await FollowJob($"Switching {label}'s Database", worktree.Path, token => Model.Sites.SwitchWorktreeDatabaseAsync(worktree.Name, worktree.Path, mode, token));
    }

    /// <summary>Sets the worktree up (again: copies what's missing, installs, sets up the database and runs the hooks again).</summary>
    private Task SetUpWorktree(Status.Site worktree, bool again) =>
        FollowJob($"Setting Up {worktree.WorktreeLabel}", worktree.Path, token => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again, token));

    /// <summary>The log of the worktree's setup, followed while it runs.</summary>
    private Task ShowSetupLog(Status.Site worktree)
    {
        var name = worktree.Name;
        var log = worktree.Worktree?.Log ?? "";
        return JobLogDialog.ShowAsync(Window, $"Setup of {worktree.WorktreeLabel}",
            token => Model.Client.ServiceLogAsync(log, 400, token),
            () => Model.Status?.Sites.FirstOrDefault(s => s.Name == name)?.Worktree?.IsBusy == true);
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
}
