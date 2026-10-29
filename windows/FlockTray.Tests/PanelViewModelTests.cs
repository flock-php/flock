using Flock.Kit;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class PanelViewModelTests
{
    [Fact]
    public void SiteRowProjectsPanelLabelsAndVisibility()
    {
        var row = new PanelSiteRow(new Status.Site
        {
            Domain = "app.test",
            Path = "C:\\Projects\\app",
            Php = "8.4",
            Secured = true,
            Debugging = true,
            Server = Status.SiteServer.FrankenPhp,
            Worker = Status.SiteServer.Octane,
            Url = "https://app.test",
            Problem = "The document root is missing.",
            Share = new Status.SiteShare { Url = "https://share.example", State = Status.ShareState.Running },
        });

        Assert.Equal("\uE72E", row.SecurityGlyph);
        Assert.Equal("PHP 8.4", row.PhpLabel);
        Assert.Equal("Octane", row.WorkerLabel);
        Assert.Equal(Microsoft.UI.Xaml.Visibility.Visible, row.ProblemVisibility);
        Assert.Equal(Microsoft.UI.Xaml.Visibility.Visible, row.DebuggingVisibility);
        Assert.Equal(Microsoft.UI.Xaml.Visibility.Visible, row.FrankenPhpVisibility);
        Assert.Equal(Microsoft.UI.Xaml.Visibility.Visible, row.SharedVisibility);
        Assert.True(row.HasUrl);
        Assert.Equal("Shared at https://share.example", row.ShareTooltip);
        Assert.Equal("https://app.test", row.SiteTooltip);
    }

    [Fact]
    public void SiteRowUsesProblemOrPathForTooltipWithoutUrl()
    {
        var problemRow = new PanelSiteRow(new Status.Site { Domain = "broken.test", Path = "C:\\broken", Problem = "The site is unavailable." });
        var pathRow = new PanelSiteRow(new Status.Site { Domain = "missing.test", Path = "C:\\missing" });

        Assert.Equal("The site is unavailable.", problemRow.SiteTooltip);
        Assert.Equal("C:\\missing", pathRow.SiteTooltip);
    }

    [Fact]
    public void ServiceRowUpdatesInPlaceAndRaisesNotification()
    {
        var row = new PanelServiceRow(new PanelServiceValue("web", "Web", "up", true));
        var changed = new List<string?>();
        row.PropertyChanged += (_, args) => changed.Add(args.PropertyName);

        row.Update(new PanelServiceValue("web", "Web", "down", false));

        Assert.Equal("down", row.Detail);
        Assert.False(row.Up);
        Assert.Contains(string.Empty, changed);
    }

    [Fact]
    public async Task DaemonSwitchShowsTheRequestThenFollowsTheDaemon()
    {
        var notified = 0;
        var daemon = false;
        var change = new TaskCompletionSource();
        var toggle = new DaemonSwitch(() => notified++);

        var running = toggle.ChangeAsync(true, value =>
        {
            Assert.True(value);
            return change.Task;
        });

        Assert.True(toggle.Show(daemon));
        Assert.Equal(0, notified);

        daemon = true;
        change.SetResult();
        await running;

        Assert.True(toggle.Show(daemon));
        Assert.Equal(1, notified);

        // A later change from the CLI shows straight away.
        daemon = false;
        Assert.False(toggle.Show(daemon));
    }

    [Fact]
    public async Task DaemonSwitchSnapsBackWhenTheChangeIsRefused()
    {
        var notified = 0;
        var toggle = new DaemonSwitch(() => notified++);

        await toggle.ChangeAsync(true, _ => Task.CompletedTask);

        Assert.False(toggle.Show(false));
        Assert.Equal(1, notified);
    }

    [Fact]
    public async Task DaemonSwitchSnapsBackWhenTheChangeThrows()
    {
        var notified = 0;
        var toggle = new DaemonSwitch(() => notified++);

        await Assert.ThrowsAsync<InvalidOperationException>(() => toggle.ChangeAsync(true, _ => throw new InvalidOperationException()));

        Assert.False(toggle.Show(false));
        Assert.Equal(1, notified);
    }

    [Fact]
    public async Task DaemonSwitchKeepsTheLatestRequestWhileItRuns()
    {
        var notified = 0;
        var first = new TaskCompletionSource();
        var second = new TaskCompletionSource();
        var toggle = new DaemonSwitch(() => notified++);

        var firstRun = toggle.ChangeAsync(true, _ => first.Task);
        var secondRun = toggle.ChangeAsync(false, _ => second.Task);
        first.SetResult();
        await firstRun;

        Assert.False(toggle.Show(true));
        Assert.Equal(0, notified);

        second.SetResult();
        await secondRun;

        Assert.True(toggle.Show(true));
        Assert.Equal(1, notified);
    }
}
