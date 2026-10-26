using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class LaunchIdentityTests
{
    [Fact]
    public void InstalledLaunchesKeepTheProductionActivationContract()
    {
        var identity = LaunchIdentity.ForExecutable(@"C:\Apps\Flock\FlockTray.exe", installed: true, forceDevelopment: false);

        Assert.False(identity.IsDevelopment);
        Assert.Equal(@"Local\Flock.Tray", identity.MutexName);
        Assert.Equal(Native.TrayWindowClass, identity.WindowClass);
    }

    [Fact]
    public void LocalLaunchesOnlyShareIdentityWithTheSameExecutable()
    {
        var first = LaunchIdentity.ForExecutable(@"C:\Source\Flock\FlockTray.exe", false, false);
        var repeated = LaunchIdentity.ForExecutable(@"c:\source\flock\FlockTray.exe", false, false);
        var other = LaunchIdentity.ForExecutable(@"C:\Other\Flock\FlockTray.exe", false, false);

        Assert.True(first.IsDevelopment);
        Assert.Equal(first, repeated);
        Assert.NotEqual(first.MutexName, LaunchIdentity.Production.MutexName);
        Assert.NotEqual(first.WindowClass, LaunchIdentity.Production.WindowClass);
        Assert.NotEqual(first.DataDirectory, LaunchIdentity.Production.DataDirectory);
        Assert.NotEqual(first.MutexName, other.MutexName);
        Assert.NotEqual(first.WindowClass, other.WindowClass);
    }

    [Fact]
    public void ExplicitDevelopmentModeOverridesInstalledDetection()
    {
        var identity = LaunchIdentity.ForExecutable(@"C:\Apps\Flock\FlockTray.exe", true, true);

        Assert.True(identity.IsDevelopment);
        Assert.NotEqual(LaunchIdentity.Production.WindowClass, identity.WindowClass);
    }

    [Fact]
    public async Task NativeActivationTargetsOnlyTheMatchingBuild()
    {
        var directory = Path.Combine(Path.GetTempPath(), Guid.NewGuid().ToString());
        var first = LaunchIdentity.ForExecutable(Path.Combine(directory, "first.exe"), false, false);
        var second = LaunchIdentity.ForExecutable(Path.Combine(directory, "second.exe"), false, false);
        using var firstTray = new TrayService(new Win32TrayNativeApi(), windowClass: first.WindowClass);
        using var secondTray = new TrayService(new Win32TrayNativeApi(), windowClass: second.WindowClass);
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Tray.Dark.ico");
        await firstTray.StartAsync(icon, "Flock activation test 1");
        await secondTray.StartAsync(icon, "Flock activation test 2");
        string? firstLink = null, secondLink = null;
        firstTray.LinkReceived += link => firstLink = link;
        secondTray.LinkReceived += link => secondLink = link;

        Native.SignalRunningInstance("flock://update", second.WindowClass);

        Assert.Null(firstLink);
        Assert.Equal("flock://update", secondLink);
    }
}
