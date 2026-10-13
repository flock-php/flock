using System.Diagnostics;
using System.Security.Cryptography;
using System.Text;
using Flock.Kit;
using Microsoft.Win32;

namespace Flock.Tray;

/// <summary>
/// Setting Flock up from the app. The installer puts flock.exe and flock-helper.exe next to
/// FlockTray.exe; `flock install` copies them to %AppData%\Flock\bin, registers the daemon and,
/// with one UAC prompt, the hosts helper and the CA. Development builds run without a bundled CLI
/// and leave installing to the terminal, as before.
/// </summary>
public static class Installation
{
    /// <summary>The CLI shipped with this app, or null for a development build.</summary>
    public static string? BundledFlock =>
        Path.Combine(AppContext.BaseDirectory, "flock.exe") is var path && File.Exists(path) ? path : null;

    /// <summary>The Inno Setup uninstaller, present when the app was installed by Flock-Setup.exe.</summary>
    public static string? Uninstaller =>
        Path.Combine(AppContext.BaseDirectory, "unins000.exe") is var path && File.Exists(path) ? path : null;

    /// <summary>The daemon is registered to start at sign-in (scheduled task, or the Run key fallback).</summary>
    public static bool IsSetUp
    {
        get
        {
            if (!File.Exists(FlockPaths.FlockBinary))
            {
                return false;
            }
            using (var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run"))
            {
                if (key?.GetValue("Flock Daemon") is string)
                {
                    return true;
                }
            }
            return Run("schtasks", "/Query", "/TN", "Flock Daemon") == 0;
        }
    }

    /// <summary>
    /// The installed CLI should be replaced by the bundled one: it differs, and the installed one is
    /// what this app installed last time (or nothing is recorded yet). A CLI installed by hand from
    /// another build is left alone.
    /// </summary>
    public static bool NeedsUpdate
    {
        get
        {
            if (BundledFlock is not { } bundled || !File.Exists(FlockPaths.FlockBinary))
            {
                return false;
            }
            var installed = Hash(FlockPaths.FlockBinary);
            if (installed == Hash(bundled))
            {
                return false;
            }
            var recorded = Preferences.Shared.InstalledFlock;
            return string.IsNullOrEmpty(recorded) || recorded == installed;
        }
    }

    /// <summary>Remembers the CLI this app installed, so later updates can tell it from a hand-installed one.</summary>
    public static void RecordInstalled()
    {
        if (File.Exists(FlockPaths.FlockBinary))
        {
            Preferences.Shared.InstalledFlock = Hash(FlockPaths.FlockBinary);
            Preferences.Shared.Save();
        }
    }

    /// <summary>Runs `flock install` from the bundled CLI, passing each output line to onLine.</summary>
    public static Task<Result> InstallAsync(Action<string>? onLine = null)
    {
        if (BundledFlock is not { } bundled)
        {
            return Task.FromResult(new Result(false, "This build of Flock has no bundled CLI — run `flock install` in a terminal."));
        }
        return RunFlockAsync(bundled, ["install"], onLine);
    }

    public sealed record Result(bool Ok, string Output);

    /// <summary>Runs the flock CLI without a console window, streaming its output.</summary>
    public static async Task<Result> RunFlockAsync(string binary, string[] args, Action<string>? onLine = null)
    {
        var start = new ProcessStartInfo(binary)
        {
            CreateNoWindow = true,
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8,
        };
        foreach (var arg in args)
        {
            start.ArgumentList.Add(arg);
        }

        var output = new StringBuilder();
        var context = SynchronizationContext.Current;
        void Line(string? line)
        {
            if (line is null)
            {
                return;
            }
            lock (output)
            {
                output.AppendLine(line);
            }
            if (onLine is not null)
            {
                // Output arrives on worker threads; the UI wants it on its own thread.
                if (context is not null)
                {
                    context.Post(_ => onLine(line), null);
                }
                else
                {
                    onLine(line);
                }
            }
        }

        try
        {
            using var process = new Process { StartInfo = start };
            process.OutputDataReceived += (_, e) => Line(e.Data);
            process.ErrorDataReceived += (_, e) => Line(e.Data);
            process.Start();
            process.BeginOutputReadLine();
            process.BeginErrorReadLine();
            await process.WaitForExitAsync();
            lock (output)
            {
                return new Result(process.ExitCode == 0, output.ToString().Trim());
            }
        }
        catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            return new Result(false, $"Could not run {binary}: {e.Message}");
        }
    }

    /// <summary>
    /// Starts uninstalling: the installer's uninstaller (which runs `flock uninstall`, then removes
    /// the app) when there is one. purge also deletes the user's settings and databases.
    /// Returns false when there is no uninstaller (a development build).
    /// </summary>
    public static bool StartUninstaller(bool purge)
    {
        if (Uninstaller is not { } uninstaller)
        {
            return false;
        }
        // /SILENT skips the uninstaller's own "Are you sure" question; the app asked already.
        Process.Start(new ProcessStartInfo(uninstaller, purge ? "/SILENT /PURGE" : "/SILENT") { UseShellExecute = true });
        return true;
    }

    private static string Hash(string path)
    {
        try
        {
            using var stream = File.OpenRead(path);
            return Convert.ToHexString(SHA256.HashData(stream));
        }
        catch (IOException)
        {
            return "";
        }
    }

    private static int Run(string file, params string[] args)
    {
        var start = new ProcessStartInfo(file) { CreateNoWindow = true, UseShellExecute = false };
        foreach (var arg in args)
        {
            start.ArgumentList.Add(arg);
        }
        try
        {
            using var process = Process.Start(start)!;
            process.WaitForExit();
            return process.ExitCode;
        }
        catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            return -1;
        }
    }
}
