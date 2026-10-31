using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class SettingsNavigationTests
{
    [Fact]
    public async Task NewSelectionWaitsForTheCanceledActivationAndSamePaneIsANoOp()
    {
        var firstStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var firstRelease = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var secondStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var calls = 0;
        await using var navigation = new SettingsNavigationViewModel(async (pane, token) =>
        {
            if (Interlocked.Increment(ref calls) == 1)
            {
                firstStarted.SetResult();
                await firstRelease.Task;
                token.ThrowIfCancellationRequested();
            }
            else
            {
                secondStarted.SetResult();
            }
        });

        var first = navigation.ActivateAsync(SettingsPane.General);
        await firstStarted.Task;
        var second = navigation.ActivateAsync(SettingsPane.Ai);
        Assert.False(second.IsCompleted);
        firstRelease.SetResult();
        await second;
        await secondStarted.Task;
        Assert.Same(second, navigation.ActivateAsync(SettingsPane.Ai));
    }

    [Fact]
    public async Task RetryStartsANewActivationTask()
    {
        var firstRelease = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var firstStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var secondStarted = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var secondRelease = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var calls = 0;
        await using var navigation = new SettingsNavigationViewModel(async (_, _) =>
        {
            if (Interlocked.Increment(ref calls) == 1)
            {
                firstStarted.SetResult();
                await firstRelease.Task;
            }
            else
            {
                secondStarted.SetResult();
                await secondRelease.Task;
            }
        });

        var first = navigation.ActivateAsync(SettingsPane.General);
        await firstStarted.Task;
        firstRelease.SetResult();
        await first;
        var retry = navigation.RetryAsync();

        Assert.NotSame(first, retry);
        await secondStarted.Task;
        secondRelease.SetResult();
        await retry;
        Assert.Equal(2, calls);
    }
}
