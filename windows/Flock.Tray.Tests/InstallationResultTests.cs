namespace Flock.Tray.Tests;

public sealed class InstallationResultTests
{
    [Fact]
    public void PermissionDeclinedGoesByTheExitCodeNotTheText()
    {
        const string text = "Error: privileged setup: administrator permission was declined";

        Assert.True(new Installation.Result(false, text, 4).PermissionDeclined);
        Assert.False(new Installation.Result(false, text, 1).PermissionDeclined);
        Assert.False(new Installation.Result(false, text).PermissionDeclined);
        Assert.False(new Installation.Result(false, "The process did not start.", -1).PermissionDeclined);
    }
}
