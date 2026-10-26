using System.ComponentModel;
using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Mail;

/// <summary>Owns the shared mail list. Message details and selection belong to a window scope.</summary>
public sealed class MailService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner operations;
    private readonly SharedRead<List<MailSummary>> request;
    private readonly SharedRead<List<Service>> environmentRequest;
    private readonly OperationErrors errors = new();
    private readonly object gate = new();
    private List<MailSummary> current = [];
    private int visibleConsumers;
    private bool clearInProgress;

    public MailService(FlockClient client, OperationRunner operations, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.operations = operations;
        request = new SharedRead<List<MailSummary>>(dispatcher);
        environmentRequest = new SharedRead<List<Service>>(dispatcher);
        errors.PropertyChanged += ErrorsChanged;
        operations.RunningChanged += RunningChanged;
    }

    public IReadOnlyList<MailSummary> Messages => current;
    public string Env { get; private set; } = "";
    public string? RefreshError => errors.Refresh;
    public string? ActionError => errors.Action;
    public string? Error => errors.DisplayedMessage;
    public bool IsClearing => operations.IsRunning("mail:clear");

    public event Action? Changed;

    /// <summary>Registers one live mail window for status-count refreshes.</summary>
    public IDisposable AcquireVisibility()
    {
        lock (gate)
        {
            visibleConsumers++;
        }
        return new Visibility(this);
    }

    public async Task RefreshAsync(CancellationToken observerToken = default)
    {
        await request.JoinReadAsync(client.MailAsync, Apply, PublishRefreshFailure, observerToken);
        await environmentRequest.JoinReadAsync(client.ServicesAsync, services =>
        {
            var value = services.FirstOrDefault(service => service.Id == "mail")?.Env ?? Env;
            if (value != Env)
            {
                Env = value;
                Changed?.Invoke();
            }
        }, _ => { }, observerToken);
    }

    public Task RefreshIfVisibleAsync(CancellationToken observerToken = default)
    {
        lock (gate)
        {
            if (visibleConsumers == 0)
            {
                return Task.CompletedTask;
            }
        }
        return RefreshAsync(observerToken);
    }

    /// <summary>Deletes mail without replaying an ambiguous DELETE, then reads the authoritative list.</summary>
    public async Task<OperationOutcome> ClearAsync(CancellationToken applicationToken = default)
    {
        var outcome = await operations.RunMutationAsync("mail:clear", errors, async token =>
        {
            lock (gate)
            {
                clearInProgress = true;
            }
            request.Invalidate();
            try
            {
                await client.ClearMailAsync(cancellationToken: token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);

        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            lock (gate)
            {
                clearInProgress = false;
            }
            request.Invalidate();
            await RefreshAsync(applicationToken);
        }
        else
        {
            lock (gate)
            {
                clearInProgress = false;
            }
        }
        return outcome;
    }

    public void DismissError()
    {
        errors.ClearAction();
        errors.ClearRefresh();
    }

    public async ValueTask DisposeAsync()
    {
        operations.RunningChanged -= RunningChanged;
        errors.PropertyChanged -= ErrorsChanged;
        await request.DisposeAsync();
        await environmentRequest.DisposeAsync();
    }

    private void Apply(List<MailSummary> value)
    {
        lock (gate)
        {
            if (clearInProgress)
            {
                return;
            }
        }
        var changed = !Same(current, value);
        current = value;
        var hadError = errors.Refresh is not null;
        errors.ClearRefresh();
        if (changed || hadError)
        {
            Changed?.Invoke();
        }
    }

    private void PublishRefreshFailure(Exception error) => errors.SetRefresh(error);

    private void ErrorsChanged(object? sender, PropertyChangedEventArgs args) => Changed?.Invoke();

    private void RunningChanged(string key, bool running)
    {
        if (key == "mail:clear")
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

    private static bool Same(IReadOnlyList<MailSummary> left, IReadOnlyList<MailSummary> right) =>
        left.Count == right.Count && left.Zip(right).All(pair =>
            pair.First.Id == pair.Second.Id && pair.First.Inbox == pair.Second.Inbox && pair.First.From == pair.Second.From &&
            pair.First.Subject == pair.Second.Subject && pair.First.Date == pair.Second.Date && pair.First.Size == pair.Second.Size &&
            pair.First.To.SequenceEqual(pair.Second.To, StringComparer.Ordinal));

    private sealed class Visibility(MailService service) : IDisposable
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
