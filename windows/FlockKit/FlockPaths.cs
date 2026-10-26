namespace Flock.Kit;

/// <summary>Where Flock keeps its files (FLOCK_HOME overrides it, like the CLI).</summary>
public static class FlockPaths
{
    /// <summary>%AppData%\Flock — Go's os.UserConfigDir() on Windows.</summary>
    public static string Root
    {
        get
        {
            var home = Environment.GetEnvironmentVariable("FLOCK_HOME");
            if (!string.IsNullOrEmpty(home))
            {
                return home;
            }
            return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "Flock");
        }
    }

    public static string ApiJson => Path.Combine(Root, "api.json");

    public static string FlockBinary => Path.Combine(Root, "bin", OperatingSystem.IsWindows() ? "flock.exe" : "flock");

    public static string LogFile(string name) => Path.Combine(Root, "log", name + ".log");
}