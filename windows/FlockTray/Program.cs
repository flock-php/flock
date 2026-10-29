using Flock.Kit;

namespace Flock.Tray;

/// <summary>
/// Entry point: `FlockTray --selftest` checks selected daemon endpoints and exits (the output goes
/// to the console it was started from); otherwise the tray app starts, once.
/// </summary>
public static class Program
{
    [STAThread]
    private static int Main(string[] args)
    {
        if (args.Contains("--selftest"))
        {
            Native.AttachParentConsole();
            return RunSelfTestAsync().GetAwaiter().GetResult();
        }

        var identity = LaunchIdentity.ForExecutable(Environment.ProcessPath!, Installation.Uninstaller is not null,
            args.Contains("--dev"));
        // Installed launches share one instance; local builds only activate their own executable.
        using var single = new Mutex(initiallyOwned: true, identity.MutexName, out var first);
        if (!first)
        {
            Native.SignalRunningInstance(args.FirstOrDefault(SecureLink.IsFlockLink), identity.WindowClass);
            return 0;
        }

        // Unpackaged apps would put WebView2's profile next to the exe, which may not be writable.
        Environment.SetEnvironmentVariable("WEBVIEW2_USER_DATA_FOLDER",
            Path.Combine(identity.DataDirectory, "WebView2"));

        try
        {
            App.Run(new AppOptions
            {
                Identity = identity,
                PreferencesPath = Path.Combine(identity.DataDirectory, "tray.json"),
            });
        }
        catch (Exception e)
        {
            // Without this, a failure while starting up just makes the app vanish.
            App.Log(e);
            Native.ShowError("Flock could not start", $"{e.Message}\n\nDetails are in %LocalAppData%\\Flock\\tray.log.");
            return 1;
        }
        return 0;
    }

    private static async Task<int> RunSelfTestAsync()
    {
        await using var host = AppHost.Create();
        await host.StartAsync();
        return await SelfTest.RunAsync(host.Client, Console.Out);
    }
}
