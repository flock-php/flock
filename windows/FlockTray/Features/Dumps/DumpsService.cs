using System.ComponentModel;
using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Dumps;

/// <summary>Owns the shared dump list, header, capture setting, and clear operation.</summary>
public sealed class DumpsService : IAsyncDisposable
{
    private const string SettingsKey = "settings";
    private const string ClearKey = "dumps:clear";

    private readonly FlockClient client;
    private readonly OperationRunner operations;
    private readonly SharedRead<List<Dump>> listRequest;
    private readonly SharedRead<List<RuntimeEvent>> eventsRequest;
    private readonly OperationErrors eventsErrors = new();
    private List<RuntimeEvent> events = [];
    private readonly SharedRead<string> headerRequest;
    private readonly OperationErrors actionErrors = new();
    private readonly OperationErrors listErrors = new();
    private readonly OperationErrors headerErrors = new();
    private readonly CancellationTokenSource lifetime = new();
    private readonly object gate = new();
    private List<Dump> dumps = [];
    private string? header;
    private bool headerMissing;
    private int visibleConsumers;
    private bool clearInProgress;

    public DumpsService(FlockClient client, OperationRunner operations, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.operations = operations;
        listRequest = new SharedRead<List<Dump>>(dispatcher);
        eventsRequest = new SharedRead<List<RuntimeEvent>>(dispatcher);
        eventsErrors.PropertyChanged += ErrorsChanged;
        headerRequest = new SharedRead<string>(dispatcher);
        actionErrors.PropertyChanged += ErrorsChanged;
        listErrors.PropertyChanged += ErrorsChanged;
        headerErrors.PropertyChanged += ErrorsChanged;
        operations.RunningChanged += RunningChanged;
    }

    public IReadOnlyList<Dump> Dumps => dumps;
    public IReadOnlyList<RuntimeEvent> Events => events;

    /// <summary>The verified shared Symfony header, or null until it is available.</summary>
    public string? Header => header;

    /// <summary>True when the daemon confirmed that this dump header is not available.</summary>
    public bool HeaderMissing => headerMissing;

    public string? RefreshError => listErrors.Refresh ?? eventsErrors.Refresh ?? headerErrors.Refresh;
    public string? ActionError => actionErrors.Action;
    public string? Error => ActionError ?? RefreshError;
    public bool IsClearing => operations.IsRunning(ClearKey);
    public bool IsCaptureBusy => operations.IsRunning(SettingsKey);
    public Func<Task>? OnCaptureChanged { get; set; }

    public event Action? Changed;

    /// <summary>Registers a live dump window for count-triggered refreshes.</summary>
    public IDisposable AcquireVisibility()
    {
        lock (gate)
        {
            visibleConsumers++;
        }
        return new Visibility(this);
    }

    public Task RefreshAsync(CancellationToken observerToken = default) => RefreshCoreAsync(observerToken);

    public Task RefreshIfVisibleAsync(CancellationToken observerToken = default)
    {
        lock (gate)
        {
            if (visibleConsumers == 0)
            {
                return Task.CompletedTask;
            }
        }
        return RefreshCoreAsync(observerToken);
    }

    /// <summary>Changes capture through the shared settings key used by every settings entry point.</summary>
    public async Task<OperationOutcome> SetCaptureAsync(bool enabled, CancellationToken applicationToken = default)
    {
        var outcome = await operations.RunMutationAsync(SettingsKey, actionErrors, async token =>
        {
            await client.UpdateSettingsAsync(new SettingsChange(Dumps: enabled), token);
        }, applicationToken);

        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            if (OnCaptureChanged is not null)
            {
                await OnCaptureChanged();
            }
        }
        return outcome;
    }

    /// <summary>Deletes dumps once, then reads the authoritative list and header.</summary>
    public async Task<OperationOutcome> ClearAsync(CancellationToken applicationToken = default)
    {
        var outcome = await operations.RunMutationAsync(ClearKey, actionErrors, async token =>
        {
            lock (gate)
            {
                clearInProgress = true;
            }
            InvalidateReads();
            try
            {
                await client.ClearDumpsAsync(token);
                try
                {
                    await client.ClearEventsAsync(token);
                }
                catch (FlockServerException error) when (error.StatusCode == 404)
                {
                }
            }
            finally
            {
                InvalidateReads();
            }
        }, applicationToken);

        lock (gate)
        {
            clearInProgress = false;
        }

        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            InvalidateReads();
            await RefreshCoreAsync(applicationToken);
        }
        Changed?.Invoke();
        return outcome;
    }

    public void DismissError()
    {
        actionErrors.ClearAction();
        listErrors.ClearRefresh();
        headerErrors.ClearRefresh();
        eventsErrors.ClearRefresh();
    }

    public async ValueTask DisposeAsync()
    {
        operations.RunningChanged -= RunningChanged;
        actionErrors.PropertyChanged -= ErrorsChanged;
        listErrors.PropertyChanged -= ErrorsChanged;
        eventsErrors.PropertyChanged -= ErrorsChanged;
        headerErrors.PropertyChanged -= ErrorsChanged;
        lifetime.Cancel();
        await listRequest.DisposeAsync();
        await eventsRequest.DisposeAsync();
        await headerRequest.DisposeAsync();
        lifetime.Dispose();
    }

    private async Task RefreshCoreAsync(CancellationToken observerToken)
    {
        await listRequest.JoinReadAsync(client.DumpsAsync, ApplyDumps, PublishListFailure, observerToken,
            lifetimeToken: lifetime.Token);
        await eventsRequest.JoinReadAsync(ReadEventsAsync, ApplyEvents, error => eventsErrors.SetRefresh(error), observerToken,
            lifetimeToken: lifetime.Token);

        if (dumps.Count > 0)
        {
            await headerRequest.JoinReadAsync(client.DumpHeaderAsync, ApplyHeader, PublishHeaderFailure, observerToken,
            lifetimeToken: lifetime.Token);
        }
    }

    private void ApplyDumps(List<Dump> value)
    {
        lock (gate)
        {
            if (clearInProgress)
            {
                return;
            }
        }
        var changed = !Same(dumps, value);
        dumps = value;
        var hadError = listErrors.Refresh is not null;
        listErrors.ClearRefresh();
        if (changed || hadError)
        {
            Changed?.Invoke();
        }
    }

    private async Task<List<RuntimeEvent>> ReadEventsAsync(CancellationToken token)
    {
        try
        {
            return await client.EventsAsync(cancellationToken: token);
        }
        catch (FlockServerException error) when (error.StatusCode == 404)
        {
            return [];
        }
    }

    private void ApplyEvents(List<RuntimeEvent> value)
    {
        lock (gate)
        {
            if (clearInProgress)
            {
                return;
            }
        }
        var changed = FlockJson.Encode(events) != FlockJson.Encode(value);
        events = value;
        var hadError = eventsErrors.Refresh is not null;
        eventsErrors.ClearRefresh();
        if (changed || hadError)
        {
            Changed?.Invoke();
        }
    }

    private void ApplyHeader(string value)
    {
        var changed = !string.Equals(header, value, StringComparison.Ordinal) || headerMissing;
        header = value;
        headerMissing = false;
        var hadError = headerErrors.Refresh is not null;
        headerErrors.ClearRefresh();
        if (changed || hadError)
        {
            Changed?.Invoke();
        }
    }

    private void PublishListFailure(Exception error) => listErrors.SetRefresh(error);

    private void PublishHeaderFailure(Exception error)
    {
        if (error is FlockServerException { StatusCode: 404 })
        {
            var changed = !headerMissing || header is not null;
            header = null;
            headerMissing = true;
            headerErrors.ClearRefresh();
            if (changed)
            {
                Changed?.Invoke();
            }
            return;
        }
        headerErrors.SetRefresh(error);
    }

    private void InvalidateReads()
    {
        listRequest.Invalidate();
        eventsRequest.Invalidate();
        headerRequest.Invalidate();
    }

    private void ErrorsChanged(object? sender, PropertyChangedEventArgs args) => Changed?.Invoke();

    private void RunningChanged(string key, bool running)
    {
        if (key is SettingsKey or ClearKey)
        {
            Changed?.Invoke();
        }
    }

    private void ReleaseVisibility()
    {
        lock (gate)
        {
            visibleConsumers = Math.Max(0, visibleConsumers - 1);
        }
    }

    private static bool Same(IReadOnlyList<Dump> left, IReadOnlyList<Dump> right) =>
        left.Count == right.Count && left.Zip(right).All(pair =>
            pair.First.Id == pair.Second.Id && pair.First.Time == pair.Second.Time && pair.First.Site == pair.Second.Site &&
            pair.First.Group == pair.Second.Group && pair.First.Label == pair.Second.Label && pair.First.Text == pair.Second.Text &&
            pair.First.Html == pair.Second.Html && pair.First.Origin == pair.Second.Origin &&
            pair.First.Source?.File == pair.Second.Source?.File && pair.First.Source?.Line == pair.Second.Source?.Line &&
            pair.First.Request?.Method == pair.Second.Request?.Method && pair.First.Request?.Uri == pair.Second.Request?.Uri &&
            pair.First.CommandLine == pair.Second.CommandLine);

    private sealed class Visibility(DumpsService service) : IDisposable
    {
        private int released;

        public void Dispose()
        {
            if (Interlocked.Exchange(ref released, 1) == 0)
            {
                service.ReleaseVisibility();
            }
        }
    }
}
