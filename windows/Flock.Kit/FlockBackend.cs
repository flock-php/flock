namespace Flock.Kit;

/// <summary>
/// Where the daemon runs: on this machine (the default), or in a WSL distro. The app talks to
/// both the same way, over api.json's loopback URL; WSL is chosen by pointing FLOCK_HOME at the
/// distro's Flock folder, e.g. \\wsl.localhost\Ubuntu\home\me\.local\share\flock. Flock is then
/// installed, updated and restarted there (`flk`, the Linux CLI), and the paths the daemon
/// reports are Linux paths, which Windows opens through the same share.
/// </summary>
public sealed record FlockBackend
{
    /// <summary>The Linux CLI's name: /usr/bin/flock is the system's file-locking tool.</summary>
    public const string LinuxCli = "flk";

    private static readonly string[] SharePrefixes = [@"\\wsl.localhost\", @"\\wsl$\"];

    private FlockBackend(string? wslDistro, string? sharePrefix)
    {
        WslDistro = wslDistro;
        SharePrefix = sharePrefix;
    }

    public static FlockBackend Local { get; } = new(null, null);

    /// <summary>The backend FLOCK_HOME (or its default, %AppData%\Flock) points at.</summary>
    public static FlockBackend Current => ForRoot(FlockPaths.Root);

    /// <summary>The distro's name when the daemon runs in WSL, else null.</summary>
    public string? WslDistro { get; }

    public bool IsWsl => WslDistro is not null;

    /// <summary>\\wsl.localhost\&lt;distro&gt; (or \\wsl$\&lt;distro&gt;, as FLOCK_HOME spells it).</summary>
    private string? SharePrefix { get; }

    /// <summary>Reads the backend from Flock's folder: a path on a distro's share is WSL.</summary>
    public static FlockBackend ForRoot(string root)
    {
        var path = root.Replace('/', '\\');
        foreach (var prefix in SharePrefixes)
        {
            if (!path.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            var rest = path[prefix.Length..];
            var end = rest.IndexOf('\\');
            var distro = end < 0 ? rest : rest[..end];
            if (distro.Length == 0)
            {
                return Local;
            }
            return new FlockBackend(distro, path[..(prefix.Length + distro.Length)]);
        }
        return Local;
    }

    /// <summary>
    /// A path the daemon reported, as Windows opens it: a Linux path (/home/me/app) goes through
    /// the distro's share; anything else (Windows paths, URLs) is returned as it is.
    /// </summary>
    public string ToWindowsPath(string path)
    {
        if (SharePrefix is null || !path.StartsWith('/'))
        {
            return path;
        }
        return SharePrefix + path.Replace('/', '\\');
    }

    /// <summary>
    /// How to run a CLI command: flock.exe in Flock's folder here, or the distro's `flk` through
    /// wsl.exe, which runs it as the distro's default user (the one whose daemon this is).
    /// </summary>
    public (string FileName, IReadOnlyList<string> Arguments) Cli(params string[] arguments)
    {
        if (WslDistro is null)
        {
            return (FlockPaths.FlockBinary, arguments);
        }
        return ("wsl.exe", ["-d", WslDistro, "--", LinuxCli, .. arguments]);
    }

    /// <summary>The command a person runs to set Flock up for this backend, for messages.</summary>
    public string InstallHint => WslDistro is null
        ? "run `flock install` in a terminal"
        : $"run `{LinuxCli} install` in WSL ({WslDistro})";
}
