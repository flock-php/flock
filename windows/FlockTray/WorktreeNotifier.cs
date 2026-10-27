using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Dispatching;
using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;

namespace Flock.Tray;

/// <summary>
/// Posts a Windows notification when a worktree's setup fails and no progress dialog follows it:
/// worktrees an agent app or git created, or whose progress was hidden. Clicking it shows the
/// worktree in Settings → Sites. Notifications are registered for the unpackaged app when it
/// starts; where that isn't possible (screenshots, tests, an older Windows) nothing is posted.
/// </summary>
internal sealed class WorktreeNotifier
{
    // Worktrees whose setup a progress dialog shows, by path.
    private readonly HashSet<string> followed = new(StringComparer.OrdinalIgnoreCase);
    private RuntimeService? model;
    private DispatcherQueue? dispatcher;
    private Status? previous;
    private bool registered;

    public void Follow(string path) => followed.Add(path);

    public void Unfollow(string path) => followed.Remove(path);

    /// <summary>Registers the app's notifications and watches the status; does nothing when notifications can't be registered.</summary>
    public void Start(RuntimeService state, DispatcherQueue queue)
    {
        try
        {
            if (!AppNotificationManager.IsSupported())
            {
                return;
            }
            // The handler goes first: Register() routes clicks to it from then on.
            AppNotificationManager.Default.NotificationInvoked += Invoked;
            AppNotificationManager.Default.Register();
            registered = true;
        }
        catch (Exception error)
        {
            App.Log(error);
            return;
        }
        model = state;
        dispatcher = queue;
        previous = state.Status;
        state.Changed += Check;
    }

    /// <summary>Called when the app quits, so a click on an old notification doesn't start it again.</summary>
    public void Stop()
    {
        if (!registered)
        {
            return;
        }
        registered = false;
        if (model is not null)
        {
            model.Changed -= Check;
        }
        try
        {
            AppNotificationManager.Default.NotificationInvoked -= Invoked;
            AppNotificationManager.Default.Unregister();
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    /// <summary>Notifies of the worktrees whose setup failed since the last status.</summary>
    private void Check()
    {
        var status = model?.Status;
        if (status is null || ReferenceEquals(status, previous))
        {
            return;
        }
        var before = previous;
        previous = status;
        if (before is null)
        {
            return;
        }
        var states = before.Sites.GroupBy(s => s.Name).ToDictionary(g => g.Key, g => g.First().Worktree?.State);
        foreach (var site in status.Sites)
        {
            if (site.IsWorktree && site.Worktree is { IsFailed: true } setup && !followed.Contains(site.Path)
                && states.TryGetValue(site.Name, out var state) && state != Status.WorktreeState.Failed)
            {
                Post(site, setup);
            }
        }
    }

    private static void Post(Status.Site worktree, Status.WorktreeSetup setup)
    {
        try
        {
            var notification = new AppNotificationBuilder()
                .AddArgument("site", worktree.Name)
                .AddText($"Setting up {worktree.WorktreeLabel} failed")
                .AddText($"{worktree.Parent}: {(setup.Error.Length > 0 ? setup.Error : "see its log")}")
                .BuildNotification();
            AppNotificationManager.Default.Show(notification);
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    // Clicks arrive on a background thread.
    private void Invoked(AppNotificationManager sender, AppNotificationActivatedEventArgs args)
    {
        if (args.Arguments.TryGetValue("site", out var name))
        {
            dispatcher?.TryEnqueue(() => App.Current.OpenSite(name));
        }
    }
}
