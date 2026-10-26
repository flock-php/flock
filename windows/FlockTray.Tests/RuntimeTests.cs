using System.Net;
using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class RuntimeTests
{
    [Fact]
    public async Task HostOwnsOneClientAndQuitIsIdempotent()
    {
        await using var host = AppHost.Create();
        await host.StartAsync();
        Assert.Same(host.Client, host.GetService<Flock.Kit.FlockClient>());
        Assert.Null(host.GetService<StatusPollingService>());
        Assert.Null(host.GetService<Flock.Tray.Features.Update.UpdateService>());

        var first = host.QuitAsync();
        var second = host.QuitAsync();
        Assert.Same(first, second);
        await first;
    }

    [Fact]
    public async Task DuplicateMutationDoesNotInvokeTheSecondDelegate()
    {
        var runner = new OperationRunner();
        var errors = new OperationErrors();
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var calls = 0;

        var first = runner.RunMutationAsync("settings", errors, async _ =>
        {
            Interlocked.Increment(ref calls);
            entered.SetResult();
            await release.Task;
        }, CancellationToken.None);
        await entered.Task;
        var second = await runner.RunMutationAsync("settings", errors, _ =>
        {
            Interlocked.Increment(ref calls);
            return Task.CompletedTask;
        }, CancellationToken.None);

        Assert.Equal(OperationOutcome.Rejected, second);
        Assert.Equal(1, calls);
        release.SetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
    }

    [Fact]
    public async Task LatestRequestSuppressesAnOlderCompletion()
    {
        var request = new LatestRequest();
        var oldStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var oldRelease = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var published = new List<int>();

        var old = request.RunReadAsync(async token =>
        {
            oldStarted.SetResult();
            await oldRelease.Task;
            return 1;
        }, published.Add, CancellationToken.None);
        await oldStarted.Task;
        var current = request.RunReadAsync(_ => Task.FromResult(2), published.Add, CancellationToken.None);
        await current;
        oldRelease.SetResult();
        await old;

        Assert.Equal([2], published);
        await request.DisposeAsync();
    }

    [Fact]
    public async Task StatusReadCrossingAMutationCannotPublishAfterTheMutation()
    {
        var directory = Directory.CreateTempSubdirectory("flock-status-");
        var config = Path.Combine(directory.FullName, "api.json");
        await File.WriteAllTextAsync(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        var handler = new StatusHandler();
        using var http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        using var client = new FlockClient(http, config);
        var polling = new StatusPollingService(client);
        var published = new List<Status?>();
        polling.StatusPublished = published.Add;

        try
        {
            var old = polling.RefreshAsync();
            await handler.Started.Task;
            polling.BeginMutation();
            handler.Release.SetResult();
            await old;

            Assert.Empty(published);

            polling.EndMutation();
            await polling.RefreshAsync();

            Assert.Single(published);
            Assert.Equal("new", published[0]?.Version);
        }
        finally
        {
            await polling.DisposeAsync();
            directory.Delete(recursive: true);
        }
    }

    [Fact]
    public async Task SharedReadReportsPublicationFailuresToItsCaller()
    {
        await using var request = new SharedRead<int>();

        var error = await Assert.ThrowsAsync<InvalidOperationException>(() => request.RunReadAsync(
            _ => Task.FromResult(1),
            _ => throw new InvalidOperationException("publish failed"),
            _ => throw new InvalidOperationException("failure publish failed")));

        Assert.Equal("publish failed", error.Message);
    }

    [Fact]
    public async Task FileLogSinkDrainsQueuedRecordsInOrder()
    {
        var directory = Directory.CreateTempSubdirectory("flock-sink-");
        var path = Path.Combine(directory.FullName, "tray.log");
        await using (var sink = new FileLogSink(path))
        {
            Assert.True(sink.TryWrite("first"));
            Assert.True(sink.TryWrite("second"));
            await sink.DrainAsync();
        }

        Assert.Equal(["first", "second"], await File.ReadAllLinesAsync(path));
        Directory.Delete(directory.FullName, recursive: true);
    }

    private sealed class StatusHandler : HttpMessageHandler
    {
        public TaskCompletionSource Started { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource Release { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        private int calls;

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (Interlocked.Increment(ref calls) == 1)
            {
                Started.SetResult();
                await Release.Task.WaitAsync(cancellationToken);
                return Json("{\"version\":\"old\"}");
            }
            return Json("{\"version\":\"new\"}");
        }

        private static HttpResponseMessage Json(string content) => new(HttpStatusCode.OK)
        {
            Content = new StringContent(content),
        };
    }
}