using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class AcceptedInstallTrackerTests
{
    [Fact]
    public async Task AcceptedInstallCompletesAfterPaneNavigationAndOnlyOnce()
    {
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var paneNavigation = new CancellationTokenSource();
        var calls = 0;
        var errors = new List<Exception>();
        var tracker = new AcceptedInstallTracker(errors.Add);

        tracker.Start(async _ =>
        {
            Interlocked.Increment(ref calls);
            entered.SetResult();
            await release.Task;
        });
        await entered.Task;

        paneNavigation.Cancel();
        release.SetResult();
        await tracker.StopAsync();

        Assert.Equal(1, calls);
        Assert.Empty(errors);
    }

    [Fact]
    public async Task ShutdownCancelsAndAwaitsAcceptedInstall()
    {
        var canceled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var tracker = new AcceptedInstallTracker(_ => throw new InvalidOperationException("unexpected log"));

        tracker.Start(async token =>
        {
            try
            {
                await Task.Delay(Timeout.InfiniteTimeSpan, token);
            }
            catch (OperationCanceledException)
            {
                canceled.SetResult();
                throw;
            }
        });

        await tracker.StopAsync();
        await canceled.Task;
    }

    [Fact]
    public async Task UnexpectedInstallFailureIsLoggedWithoutEscapingShutdown()
    {
        var error = new InvalidOperationException("install failed");
        var logged = new TaskCompletionSource<Exception>(TaskCreationOptions.RunContinuationsAsynchronously);
        var tracker = new AcceptedInstallTracker(logged.SetResult);

        tracker.Start(_ => Task.FromException(error));

        await tracker.StopAsync();

        Assert.Same(error, await logged.Task);
    }
}
