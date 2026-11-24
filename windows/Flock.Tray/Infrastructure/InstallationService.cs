using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Microsoft.Win32;

namespace Flock.Tray.Infrastructure;

/// <summary>
/// Owns local setup admission while the daemon-owned install job continues after UI close. A daemon
/// in WSL is set up, updated and restarted in its distro; setup here is what Windows needs to open
/// its sites (<see cref="WslWindowsSetup"/>).
/// </summary>
public sealed class InstallationService(CliProcessRunner processRunner, PreferencesService preferences, IUiDispatcher? dispatcher = null,
    FlockBackend? backend = null, WslWindowsSetup? wslSetup = null)
{
    private readonly object gate = new();
    private Task<Installation.Result>? setup;
    private readonly WslWindowsSetup? wsl = (backend ?? FlockBackend.Current) is { IsWsl: true } distro
        ? wslSetup ?? new WslWindowsSetup(distro, FlockPaths.Root, WslWindowsSetup.WindowsCli)
        : null;
    private volatile bool asked;

    public FlockBackend Backend { get; } = backend ?? FlockBackend.Current;

    /// <summary>Whether this app can run setup: it needs a CLI on Windows for it.</summary>
    public bool CanSetUp => wsl?.IsAvailable ?? BundledFlock is not null;

    /// <summary>
    /// Leaves a WSL daemon's setup for this run, as declining its permission prompt does: the
    /// daemon serves the sites either way, so the app is usable without it. False for a daemon on
    /// Windows, which the app can't show before setup.
    /// </summary>
    public bool SkipSetup()
    {
        asked |= wsl is not null;
        return wsl is not null;
    }
    public bool IsInstalling => setup is { IsCompleted: false };
    public Task<List<Conflict>> ConflictsAsync() => Backend.IsWsl ? Task.FromResult(new List<Conflict>()) : Installation.ConflictsAsync(processRunner);

    public Task<Installation.Result> InstallAsync(Action<string>? onLine = null, CancellationToken cancellationToken = default, bool retry = false)
    {
        lock (gate)
        {
            if (setup is not null && !retry)
            {
                return setup;
            }
            setup = InstallCoreAsync(onLine, cancellationToken);
            return setup;
        }
    }

    private async Task<Installation.Result> InstallCoreAsync(Action<string>? onLine, CancellationToken cancellationToken)
    {
        CliProcessResult result;
        if (wsl is not null)
        {
            var outcome = await wsl.SetUpAsync(cancellationToken);
            // A declined prompt must not leave the app at "Not set up": the daemon serves the sites
            // either way, and the next start asks again. Any other failure is still not set up.
            asked = !outcome.Ok && outcome.Output.Contains(WslWindowsSetup.Declined, StringComparison.Ordinal);
            result = new CliProcessResult(true, outcome.Ok ? 0 : 1, outcome.Output);
        }
        else if (Installation.BundledFlock is not { } bundled)
        {
            return new Installation.Result(false, Strings.Setup_NoBundledCli);
        }
        else
        {
            result = await processRunner.RunAsync(bundled, new[] { "install" }, cancellationToken: cancellationToken);
            await RecordInstalledAsync(cancellationToken);
        }
        if (result.Output.Length > 0)
        {
            foreach (var line in result.Output.Split('\n'))
            {
                var text = line.TrimEnd('\r');
                if (onLine is not null)
                {
                    if (dispatcher is null)
                    {
                        onLine(text);
                    }
                    else
                    {
                        await dispatcher.InvokeAsync(() => onLine(text), CancellationToken.None);
                    }
                }
            }
        }
        return new Installation.Result(result.Succeeded, result.Output);
    }

    public async Task<bool> IsSetUpAsync(CancellationToken cancellationToken = default)
    {
        if (wsl is not null)
        {
            return asked || await Task.Run(wsl.IsSetUp, cancellationToken);
        }
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
        var result = await processRunner.RunAsync("schtasks", new[] { "/Query", "/TN", "Flock Daemon" }, TimeSpan.FromSeconds(10), cancellationToken);
        return result.Succeeded;
    }

    public string? BundledFlock => Installation.BundledFlock;

    public async Task<bool> NeedsUpdateAsync(CancellationToken cancellationToken = default) =>
        !Backend.IsWsl && Installation.NeedsUpdate(await preferences.LoadAsync(cancellationToken));

    public async Task RecordInstalledAsync(CancellationToken cancellationToken = default)
    {
        if (Installation.InstalledHash() is { } hash)
        {
            await preferences.UpdateAsync(value => value with { InstalledFlock = hash }, cancellationToken);
        }
    }

    public Task DrainAsync()
    {
        lock (gate)
        {
            return setup ?? Task.CompletedTask;
        }
    }
}
