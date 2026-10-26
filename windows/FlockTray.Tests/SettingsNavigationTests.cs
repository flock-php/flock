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
}