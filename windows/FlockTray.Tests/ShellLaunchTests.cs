using System.ComponentModel;
using System.Diagnostics;
using Flock.Tray.Infrastructure;
using Microsoft.Extensions.Logging.Abstractions;

namespace Flock.Tray.Tests;

public sealed class ShellLaunchTests
{
    [Fact]
    public void SuccessfulAssociationLaunchReturnsSuccess()
    {
        ProcessStartInfo? observed = null;
        var launcher = new ShellLauncher(NullLogger<ShellLauncher>.Instance, start =>
        {
            observed = start;
            return new Process();
        });

        var result = launcher.Open("https://example.test/path");

        Assert.True(result.Succeeded);
        Assert.Empty(result.Message);
        Assert.Equal("https://example.test/path", observed!.FileName);
        Assert.True(observed.UseShellExecute);
    }

    [Fact]
    public void UnsupportedEditorAssociationReturnsVisibleFailure()
    {
        var launcher = new ShellLauncher(NullLogger<ShellLauncher>.Instance, _ => throw new Win32Exception("no association"));

        var result = launcher.Open("phpstorm://open?file=C:/work/app.php&line=4");

        Assert.False(result.Succeeded);
        Assert.Equal("Could not open this item.", result.Message);
    }

    [Fact]
    public void ConnectionLaunchKeepsTheConnectionAsOneArgument()
    {
        ProcessStartInfo? observed = null;
        var launcher = new ShellLauncher(NullLogger<ShellLauncher>.Instance, start =>
        {
            observed = start;
            return new Process();
        });

        var result = launcher.OpenConnection("TablePlus.exe", "postgres://user:p%40ss@127.0.0.1:5432/app");

        Assert.True(result.Succeeded);
        Assert.False(observed!.UseShellExecute);
        Assert.Equal(["postgres://user:p%40ss@127.0.0.1:5432/app"], observed.ArgumentList);
    }
}