namespace Flock.Kit.Tests;

public class FlockBackendTests
{
    [Theory]
    [InlineData(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock", "Ubuntu")]
    [InlineData(@"\\WSL.LOCALHOST\Ubuntu-24.04\home\me\.local\share\flock", "Ubuntu-24.04")]
    [InlineData(@"\\wsl$\Debian\home\me\.local\share\flock", "Debian")]
    [InlineData("//wsl.localhost/Ubuntu/home/me/.local/share/flock", "Ubuntu")]
    [InlineData(@"\\wsl.localhost\Ubuntu", "Ubuntu")]
    public void AFolderOnADistrosShareIsWsl(string root, string distro)
    {
        var backend = FlockBackend.ForRoot(root);

        Assert.True(backend.IsWsl);
        Assert.Equal(distro, backend.WslDistro);
    }

    [Theory]
    [InlineData(@"C:\Users\me\AppData\Roaming\Flock")]
    [InlineData(@"\\server\share\flock")]
    [InlineData(@"\\wsl.localhost\")]
    [InlineData("")]
    public void AnyOtherFolderIsLocal(string root)
    {
        var backend = FlockBackend.ForRoot(root);

        Assert.False(backend.IsWsl);
        Assert.Same(FlockBackend.Local, backend);
    }

    [Fact]
    public void LinuxPathsOpenThroughTheShareTheRootNames()
    {
        var localhost = FlockBackend.ForRoot(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock");
        var dollar = FlockBackend.ForRoot(@"\\wsl$\Ubuntu\home\me\.local\share\flock");

        Assert.Equal(@"\\wsl.localhost\Ubuntu\home\me\Projects\app", localhost.ToWindowsPath("/home/me/Projects/app"));
        Assert.Equal(@"\\wsl$\Ubuntu\etc\php.ini", dollar.ToWindowsPath("/etc/php.ini"));
    }

    [Theory]
    [InlineData("https://app.test")]
    [InlineData(@"C:\Users\me\Projects\app")]
    [InlineData(@"\\wsl.localhost\Ubuntu\home\me\app")]
    public void OtherTargetsOpenAsTheyAre(string target)
    {
        var backend = FlockBackend.ForRoot(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock");

        Assert.Equal(target, backend.ToWindowsPath(target));
        Assert.Equal("/home/me/app", FlockBackend.Local.ToWindowsPath("/home/me/app"));
    }

    [Fact]
    public void WslRunsTheLinuxCliInTheDistro()
    {
        var (fileName, arguments) = FlockBackend.ForRoot(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock").Cli("restart");

        Assert.Equal("wsl.exe", fileName);
        Assert.Equal(["-d", "Ubuntu", "--", "flk", "restart"], arguments);
    }

    [Fact]
    public void LocallyTheCliIsFlocksOwn()
    {
        var (fileName, arguments) = FlockBackend.Local.Cli("restart");

        Assert.Equal(FlockPaths.FlockBinary, fileName);
        Assert.Equal(["restart"], arguments);
    }
}
