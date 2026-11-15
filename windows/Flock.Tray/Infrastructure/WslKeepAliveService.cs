using System.Diagnostics;
using Flock.Kit;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Flock.Tray.Infrastructure;

/// <summary>
/// Keeps a WSL daemon's distro running while the app runs. WSL powers a distro off seconds after
/// the last Windows program using it exits (the app's API calls over loopback don't count), which
/// takes the daemon and the sites with it. So the app holds a hidden `wsl.exe … sleep infinity`
/// open, which also boots the distro (and systemd the daemon) when the app starts at sign-in, and
/// starts it again when it ends (e.g. after `wsl --shutdown`). Does nothing for a local daemon.
/// </summary>
public sealed class WslKeepAliveService(FlockBackend backend, ILogger<WslKeepAliveService>? logger = null,
    Func<ProcessStartInfo, CancellationToken, Task>? hold = null, TimeProvider? timeProvider = null) : BackgroundService
{
    public static readonly TimeSpan RetryDelay = TimeSpan.FromSeconds(5);

    private readonly ILogger logger = logger ?? NullLogger<WslKeepAliveService>.Instance;
    private readonly Func<ProcessStartInfo, CancellationToken, Task> hold = hold ?? HoldAsync;
    private readonly TimeProvider timeProvider = timeProvider ?? TimeProvider.System;

    /// <summary>The hidden process that keeps the distro running.</summary>
    public static ProcessStartInfo StartInfo(string distro)
    {
        var start = new ProcessStartInfo("wsl.exe") { UseShellExecute = false, CreateNoWindow = true };
        foreach (var argument in new[] { "-d", distro, "--exec", "sleep", "infinity" })
        {
            start.ArgumentList.Add(argument);
        }
        return start;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (backend.WslDistro is not { } distro)
        {
            return;
        }
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await hold(StartInfo(distro), stoppingToken);
                logger.LogWarning("WSL ({Distro}) stopped; starting it again.", distro);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                return;
            }
            catch (Exception error)
            {
                logger.LogError(error, "Could not keep WSL ({Distro}) running.", distro);
            }
            try
            {
                await Task.Delay(RetryDelay, timeProvider, stoppingToken);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                return;
            }
        }
    }

    /// <summary>Runs the process until it exits or the app stops, which ends it.</summary>
    private static async Task HoldAsync(ProcessStartInfo start, CancellationToken cancellationToken)
    {
        using var process = Process.Start(start) ?? throw new InvalidOperationException("wsl.exe did not start.");
        try
        {
            await process.WaitForExitAsync(cancellationToken);
        }
        finally
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
        }
    }
}
