using System.Net;
using System.Runtime.CompilerServices;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;

namespace Flock.Tray.Tests;

public sealed class RuntimeTests
{
    [Fact]
    public async Task HostOwnsOneClientAndQuitIsIdempotent()
    {
        var releases = 0;
        await using var host = AppHost.Create(releaseUi: () =>
        {
            Interlocked.Increment(ref releases);
            return Task.CompletedTask;
        });
        await host.StartAsync();
        Assert.Same(host.Client, host.GetService<Flock.Kit.FlockClient>());
        Assert.Null(host.GetService<StatusPollingService>());
        Assert.Null(host.GetService<Flock.Tray.Features.Update.UpdateService>());

        var first = host.QuitAsync();
        var second = host.QuitAsync();
        Assert.Same(first, second);
        await first;
        Assert.Equal(1, releases);
    }

    [Fact]
    public void RepeatedQuitRequestsSignalStopOnce()
    {
        var stopSignals = 0;
        var runtime = new ApplicationRuntime(() => Interlocked.Increment(ref stopSignals));

        runtime.RequestQuit();
        runtime.RequestQuit();
        Assert.Equal(1, stopSignals);
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
    public async Task SharedReadSuppressesAnOlderCompletion()
    {
        var request = new SharedRead<int>();
        var oldStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var oldRelease = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var published = new List<int>();

        var old = request.RunReadAsync(async token =>
        {
            oldStarted.SetResult();
            await oldRelease.Task;
            return 1;
        }, published.Add, _ => { }, applicationToken: CancellationToken.None);
        await oldStarted.Task;
        var current = request.RunReadAsync(_ => Task.FromResult(2), published.Add, _ => { }, applicationToken: CancellationToken.None);
        await current;
        oldRelease.SetResult();
        await old;

        Assert.Equal([2], published);
        await request.DisposeAsync();
    }

    [Fact]
    public async Task SharedReadJoinsAnEquivalentRead()
    {
        await using var request = new SharedRead<int>();
        var started = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var loads = 0;
        var published = new List<int>();

        var first = request.JoinReadAsync(async _ =>
        {
            Interlocked.Increment(ref loads);
            started.SetResult();
            await release.Task;
            return 42;
        }, published.Add, _ => { }, CancellationToken.None);
        await started.Task;

        var second = request.JoinReadAsync(_ => Task.FromResult(7), published.Add, _ => { }, CancellationToken.None);
        release.SetResult();
        await Task.WhenAll(first, second);

        Assert.Equal(1, loads);
        Assert.Equal([42], published);
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

    [Fact]
    public async Task ClosingWindowReleasesItsRootOnce()
    {
        var root = new TestWindowRoot();
        var manager = new WindowManager(new TestWindowFactory(root), new ImmediateUiDispatcher());

        manager.Open(WindowKind.Settings);
        await manager.CloseAsync(WindowKind.Settings);
        await manager.CloseAsync(WindowKind.Settings);

        Assert.Equal(1, root.ReleaseCount);
    }

    [Fact]
    public async Task ReleaseFailureIsObservedByTheWindowManager()
    {
        var error = new InvalidOperationException("release failed");
        var root = new TestWindowRoot(error);
        var manager = new WindowManager(new TestWindowFactory(root), new ImmediateUiDispatcher());

        manager.Open(WindowKind.Settings);
        root.RequestClose();
        await root.NativeClosed.Task;

        Assert.Equal(1, root.ReleaseCount);
    }

    [Fact]
    public async Task ReopenWaitsForTheClosingRootToFinish()
    {
        var first = new TestWindowRoot(pending: true);
        var second = new TestWindowRoot();
        var factory = new TestWindowFactory(first, second);
        var manager = new WindowManager(factory, new ImmediateUiDispatcher());

        var oldWindow = manager.Open(WindowKind.Settings);
        var close = manager.CloseAsync(WindowKind.Settings);
        var reopen = manager.OpenAsync(WindowKind.Settings);

        Assert.False(reopen.IsCompleted);
        Assert.Equal(1, factory.CreatedCount);

        first.Release.SetResult();
        await close;
        var newWindow = await reopen;

        Assert.Equal(2, factory.CreatedCount);
        Assert.NotSame(oldWindow, newWindow);
        Assert.Equal(1, first.ReleaseCount);
        Assert.Equal(1, first.ActivateCount);
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

    private sealed class TestWindowFactory(params TestWindowRoot[] roots) : IWindowFactory
    {
        private readonly Queue<TestWindowRoot> roots = new(roots);

        public int CreatedCount { get; private set; }

        public IWindowRoot Create(WindowKind kind)
        {
            CreatedCount++;
            return roots.Dequeue();
        }
    }

    private sealed class TestWindowRoot : IWindowRoot
    {
        private readonly Exception? releaseFailure;

        public TestWindowRoot(Exception? releaseFailure = null, bool pending = false)
        {
            this.releaseFailure = releaseFailure;
            if (!pending)
            {
                Release.SetResult();
            }
        }

        public Window Window { get; } = (Window)RuntimeHelpers.GetUninitializedObject(typeof(Window));

        public TaskCompletionSource Release { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource NativeClosed { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public int ReleaseCount { get; private set; }

        public int ActivateCount { get; private set; }

        public event Action<WindowClosingEventArgs>? Closing;

        public event Action? Closed;

        public void Activate() => ActivateCount++;

        public void Close()
        {
            var args = new WindowClosingEventArgs();
            Closing?.Invoke(args);
            if (!args.Cancel)
            {
                Closed?.Invoke();
                NativeClosed.TrySetResult();
            }
        }

        public void RequestClose()
        {
            var args = new WindowClosingEventArgs();
            Closing?.Invoke(args);
        }

        public async ValueTask DisposeAsync()
        {
            ReleaseCount++;
            if (releaseFailure is not null)
            {
                throw releaseFailure;
            }
            await Release.Task;
        }
    }

    private sealed class ImmediateUiDispatcher : IUiDispatcher
    {
        public Task InvokeAsync(Action callback, CancellationToken cancellationToken = default)
        {
            callback();
            return Task.CompletedTask;
        }

        public Task InvokeAsync(Func<Task> callback, CancellationToken cancellationToken = default) => callback();
    }
}
