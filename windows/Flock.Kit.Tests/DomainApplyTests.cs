namespace Flock.Kit.Tests;

/// <summary>
/// Pins the sentinel the bundled CLI prints when the user answers the UAC prompt with "No".
/// The macOS Installation.permissionDeclined matches the same string, case-sensitively.
/// </summary>
public class DomainApplyTests
{
    [Fact]
    public void FindsTheSentinelAnywhereInTheOutput()
    {
        Assert.True(DomainApply.IsDeclined("elevating: administrator permission was declined"));
        Assert.True(DomainApply.IsDeclined(DomainApply.DeclinedSentinel));
        Assert.True(DomainApply.IsDeclined("step 1 ok\ninstalling CA\nAdministrator note: the prompt said administrator permission was declined and we gave up\n"));
    }

    [Theory]
    [InlineData("")]
    [InlineData("installed hosts helper and CA")]
    // The daemon installed the cert; a different program declined something else.
    [InlineData("chrome said: permission was declined")]
    // Close, but not what the CLI prints.
    [InlineData("administrator permission declined")]
    // The match is case-sensitive, as on macOS.
    [InlineData("Administrator Permission Was Declined")]
    public void DeclinesToMatchAnythingElse(string output)
    {
        Assert.False(DomainApply.IsDeclined(output));
    }
}
