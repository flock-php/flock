using System.Net;
using System.Net.Sockets;
using Flock.Kit;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Flock.Tray.Infrastructure;

/// <summary>
/// Resolves the TLD on Windows for a daemon in WSL. Windows sends *.test to 127.0.0.1:53 (the
/// NRPT rule, which can't name a port), but WSL keeps port 53 for its own DNS, so the daemon's DNS
/// server there is out of reach. The app answers instead, as that server would: every name under
/// the daemon's TLD is 127.0.0.1, where mirrored networking has the distro's web server.
/// UDP only, like the lookups Windows sends for answers this small. Does nothing for a local daemon.
/// </summary>
public sealed class WslDnsService(FlockBackend backend, Func<CancellationToken, Task<string>> tld,
    ILogger<WslDnsService>? logger = null, IPEndPoint? endpoint = null, TimeProvider? timeProvider = null) : BackgroundService
{
    public static readonly TimeSpan RetryDelay = TimeSpan.FromSeconds(5);

    private readonly ILogger logger = logger ?? NullLogger<WslDnsService>.Instance;
    private readonly IPEndPoint endpoint = endpoint ?? new IPEndPoint(IPAddress.Loopback, 53);
    private readonly TimeProvider timeProvider = timeProvider ?? TimeProvider.System;
    private string? lastProblem;
    private readonly TaskCompletionSource<IPEndPoint> listening = new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <summary>The address the server answers on, once it does (tests listen on port 0).</summary>
    public Task<IPEndPoint> Listening => listening.Task;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (!backend.IsWsl)
        {
            return;
        }
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                // The daemon names the TLD; until it answers there are no sites to resolve either.
                var name = await tld(stoppingToken);
                if (name.Length > 0)
                {
                    await ServeAsync(name, stoppingToken);
                }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                return;
            }
            catch (Exception error)
            {
                // Retried every few seconds (the daemon is down, or another program has the port): log a problem once.
                if (error.Message != lastProblem)
                {
                    lastProblem = error.Message;
                    logger.LogWarning("DNS for the WSL daemon is not available on {Endpoint}: {Message}", endpoint, error.Message);
                }
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

    private async Task ServeAsync(string name, CancellationToken cancellationToken)
    {
        using var socket = new Socket(AddressFamily.InterNetwork, SocketType.Dgram, ProtocolType.Udp);
        // Windows reports an unreachable client as a reset on the next receive; ignore those.
        socket.IOControl(unchecked((int)0x9800000C), [0], null);
        socket.Bind(endpoint);
        listening.TrySetResult((IPEndPoint)socket.LocalEndPoint!);
        lastProblem = null;
        logger.LogInformation("Answering *.{Tld} lookups on {Endpoint} for the daemon in WSL.", name, socket.LocalEndPoint);

        // Larger than any query: a datagram that doesn't fit would end the receive with an error.
        var buffer = new byte[4096];
        while (true)
        {
            var received = await socket.ReceiveFromAsync(buffer, SocketFlags.None, new IPEndPoint(IPAddress.Any, 0), cancellationToken);
            if (WildcardDns.Answer(buffer.AsSpan(0, received.ReceivedBytes), name) is { } answer)
            {
                await socket.SendToAsync(answer, SocketFlags.None, received.RemoteEndPoint, cancellationToken);
            }
        }
    }
}
