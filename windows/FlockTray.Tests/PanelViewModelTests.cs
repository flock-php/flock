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
}
