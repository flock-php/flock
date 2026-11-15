using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

/// <summary>A daemon in WSL is set up and updated in its distro: the app runs nothing here for it.</summary>
public sealed class WslBackendTests : IDisposable
{
    private readonly DirectoryInfo directory = Directory.CreateTempSubdirectory("flock-wsl-");
    private readonly RecordingRunner runner = new();
    private readonly FlockBackend wsl = FlockBackend.ForRoot(@"\\wsl.localhost\Ubuntu\home\me\.local\share\flock");

    public void Dispose() => directory.Delete(recursive: true);

    private sealed class RecordingRunner : CliProcessRunner
    {
        public List<string> Runs { get; } = [];

        public override Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments, TimeSpan? deadline = null, CancellationToken cancellationToken = default)
        {
            Runs.Add(string.Join(' ', [fileName, .. arguments]));
            return Task.FromResult(new CliProcessResult(true, 0, ""));
        }
    }

    private InstallationService Installation() =>
        new(runner, new PreferencesService(Path.Combine(directory.FullName, "tray.json")), backend: wsl);

    [Fact]
    public async Task ItIsSetUpWithoutLookingForTheDaemonsScheduledTask()
    {
        Assert.True(await Installation().IsSetUpAsync());
        Assert.Empty(runner.Runs);
    }

    [Fact]
    public async Task ItNeverUpdatesTheCliOrChecksPortsHere()
    {
        var installation = Installation();

        Assert.False(await installation.NeedsUpdateAsync());
        Assert.Empty(await installation.ConflictsAsync());
        Assert.Empty(runner.Runs);
    }

    [Fact]
    public async Task InstallingSaysWhereToRunIt()
    {
        var result = await Installation().InstallAsync();

        Assert.False(result.Ok);
        Assert.Contains("`flk install` in WSL (Ubuntu)", result.Output);
        Assert.Empty(runner.Runs);
    }

    [Fact]
    public async Task TheAppHoldsTheDistroOpenUntilItStops()
    {
        var held = new TaskCompletionSource<ProcessStartInfo>(TaskCreationOptions.RunContinuationsAsynchronously);
        var service = new WslKeepAliveService(wsl, hold: async (start, token) =>
        {
            held.TrySetResult(start);
            await Task.Delay(Timeout.Infinite, token);
        });

        await service.StartAsync(CancellationToken.None);
        var start = await held.Task.WaitAsync(TimeSpan.FromSeconds(5));
        await service.StopAsync(CancellationToken.None);

        Assert.Equal("wsl.exe", start.FileName);
        Assert.Equal(["-d", "Ubuntu", "--exec", "sleep", "infinity"], start.ArgumentList);
        Assert.True(start.CreateNoWindow);
        Assert.True(service.ExecuteTask!.IsCompletedSuccessfully);
    }

    [Fact]
    public async Task ADistroThatStoppedIsStartedAgain()
    {
        var clock = new Microsoft.Extensions.Time.Testing.FakeTimeProvider();
        var holds = 0;
        var second = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var service = new WslKeepAliveService(wsl, hold: (_, _) =>
        {
            if (Interlocked.Increment(ref holds) == 2)
            {
                second.TrySetResult();
            }
            return Task.CompletedTask;
        }, timeProvider: clock);

        await service.StartAsync(CancellationToken.None);
        // The service runs on the thread pool: advance the clock until its retry delay has passed.
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (!second.Task.IsCompleted && DateTime.UtcNow < deadline)
        {
            Assert.True(Volatile.Read(ref holds) <= 1);
            clock.Advance(WslKeepAliveService.RetryDelay);
            await Task.Delay(10);
        }
        await service.StopAsync(CancellationToken.None);

        Assert.True(second.Task.IsCompleted);
    }

    [Fact]
    public async Task AProcessInTheJobEndsWhenTheJobCloses()
    {
        // What Windows does with the app's job when the app is killed: its handle closes.
        var start = new ProcessStartInfo("ping.exe", "-n 60 127.0.0.1") { UseShellExecute = false, CreateNoWindow = true };
        using var process = Process.Start(start)!;
        var job = KillOnCloseJob.Create();
        try
        {
            Assert.NotNull(job);
            Assert.True(job.Add(process));
            Assert.False(process.HasExited);
        }
        finally
        {
            job?.Dispose();
        }

        await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(5));
    }

    [Fact]
    public async Task ALocalDaemonNeedsNothingKeptOpen()
    {
        var holds = 0;
        var service = new WslKeepAliveService(FlockBackend.Local, hold: (_, _) =>
        {
            holds++;
            return Task.CompletedTask;
        });

        await service.StartAsync(CancellationToken.None);
        await service.ExecuteTask!;

        Assert.Equal(0, holds);
    }

    [Fact]
    public async Task TheAppAnswersTheDaemonsTldOnWindows()
    {
        var service = new WslDnsService(wsl, _ => Task.FromResult("test"), endpoint: new IPEndPoint(IPAddress.Loopback, 0));
        await service.StartAsync(CancellationToken.None);
        var server = await service.Listening.WaitAsync(TimeSpan.FromSeconds(5));
        byte[] query = [0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, (byte)'a', (byte)'p', (byte)'p', 4, (byte)'t', (byte)'e', (byte)'s', (byte)'t', 0, 0, 1, 0, 1];

        using var client = new UdpClient(AddressFamily.InterNetwork);
        await client.SendAsync(query, server);
        var reply = (await client.ReceiveAsync().WaitAsync(TimeSpan.FromSeconds(5))).Buffer;
        await service.StopAsync(CancellationToken.None);

        Assert.Equal([0x12, 0x34], reply[..2]);
        Assert.Equal([127, 0, 0, 1], reply[^4..]);
    }

    [Fact]
    public async Task TheAnswersFollowATldThatChanges()
    {
        var clock = new Microsoft.Extensions.Time.Testing.FakeTimeProvider();
        var asked = 0;
        var service = new WslDnsService(wsl, _ => Task.FromResult(Interlocked.Increment(ref asked) == 1 ? "test" : "dev"),
            endpoint: new IPEndPoint(IPAddress.Loopback, 0), timeProvider: clock);
        await service.StartAsync(CancellationToken.None);
        var server = await service.Listening.WaitAsync(TimeSpan.FromSeconds(5));
        byte[] query = [0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, (byte)'a', (byte)'p', (byte)'p', 3, (byte)'d', (byte)'e', (byte)'v', 0, 0, 1, 0, 1];

        using var client = new UdpClient(AddressFamily.InterNetwork);
        // Refused (no answer records) until the service has asked the daemon again.
        var answers = 0;
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (answers == 0 && DateTime.UtcNow < deadline)
        {
            await client.SendAsync(query, server);
            var reply = (await client.ReceiveAsync().WaitAsync(TimeSpan.FromSeconds(5))).Buffer;
            answers = reply[7];
            clock.Advance(WslDnsService.RefreshInterval);
            await Task.Delay(10);
        }
        await service.StopAsync(CancellationToken.None);

        Assert.Equal(1, answers);
        Assert.True(service.ExecuteTask!.IsCompletedSuccessfully);
    }

    [Fact]
    public async Task ALocalDaemonAnswersItsOwnLookups()
    {
        var asked = false;
        var service = new WslDnsService(FlockBackend.Local, _ =>
        {
            asked = true;
            return Task.FromResult("test");
        }, endpoint: new IPEndPoint(IPAddress.Loopback, 0));

        await service.StartAsync(CancellationToken.None);
        await service.ExecuteTask!;

        Assert.False(asked);
        Assert.False(service.Listening.IsCompleted);
    }

    [Fact]
    public void TheDefaultBackendFollowsFlockHome()
    {
        var installation = new InstallationService(runner, new PreferencesService(Path.Combine(directory.FullName, "tray.json")));

        Assert.Equal(FlockBackend.Current, installation.Backend);
    }
}
