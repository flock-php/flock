using Microsoft.Extensions.Hosting;

namespace Flock.Tray.Infrastructure;

/// <summary>Runs update checks on a hosted lifetime instead of an unobserved async-void loop.</summary>
public sealed class UpdateScheduleService(Flock.Tray.Features.Update.UpdateService updates, TimeProvider timeProvider) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (!updates.IsEnabled)
        {
            return;
        }
        try
        {
            await Task.Delay(TimeSpan.FromMinutes(1), timeProvider, stoppingToken);
        }
        catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
        {
            return;
        }
        while (!stoppingToken.IsCancellationRequested)
        {
            await updates.CheckAsync(userInitiated: false, stoppingToken);
            try
            {
                await Task.Delay(TimeSpan.FromDays(1), timeProvider, stoppingToken);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                return;
            }
        }
    }
}