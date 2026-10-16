using Flock.Kit;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;

namespace Flock.Tray;

/// <summary>
/// Entry point: `FlockTray --selftest` checks every API call against the running daemon and exits
/// (the output goes to the console it was started from); otherwise the tray app starts, once.
/// </summary>
public static class Program
{
    [STAThread]
    private static int Main(string[] args)
    {
        if (args.Contains("--selftest"))
        {
            Native.AttachParentConsole();
            using var client = new FlockClient();
            return SelfTest.RunAsync(client, Console.Out).GetAwaiter().GetResult();
        }

        // One tray icon per user session: a second start (e.g. from the Start menu) brings the
        // running app's main window to the front, and one for a flock:// link hands it over.
        using var single = new Mutex(initiallyOwned: true, @"Local\Flock.Tray", out var first);
        if (!first)
        {
            Native.SignalRunningInstance(args.FirstOrDefault(SecureLink.IsFlockLink));
            return 0;
        }

        WinRT.ComWrappersSupport.InitializeComWrappers();
        // Unpackaged apps would put WebView2's profile next to the exe, which may not be writable.
        Environment.SetEnvironmentVariable("WEBVIEW2_USER_DATA_FOLDER",
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Flock", "WebView2"));

        try
        {
            Application.Start(callback =>
            {
                SynchronizationContext.SetSynchronizationContext(new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread()));
                _ = new App();
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
}
