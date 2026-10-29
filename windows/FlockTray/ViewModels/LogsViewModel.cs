using System.Collections.ObjectModel;
using System.ComponentModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Infrastructure;
namespace Flock.Tray.ViewModels;

/// <summary>Owns the selected log, its source-bound reads, filtering, polling, and window lifetime.</summary>
public sealed class LogsViewModel : ObservableObject, IAsyncDisposable
{

    private readonly FlockClient _client;
    private readonly IUiDispatcher? _dispatcher;
    private readonly OperationErrors featureErrors = new();
    private readonly Lock gate = new();
    private readonly SharedRead<LogIndex> indexRequest;
    private readonly CancellationTokenSource lifetime = new();
    private readonly OperationRunner _operations;
    private readonly Dictionary<Source, SharedRead<LogContent>> requests = [];
    private readonly Dictionary<Source, OperationErrors> resourceErrors = [];
    private readonly ObservableCollection<Source> sources = [];
    private readonly TimeProvider _timeProvider;
    private bool autoRefreshEnabled;
    private readonly IAsyncRelayCommand? clearCommand;
    private LogLevelFilter level = LogLevelFilter.All;
    private Task? pollTask;
    private string query = "";
    private bool released;
    private long selectionGeneration;
    private bool windowActive = true;

    public LogsViewModel(FlockClient client, OperationRunner operations, IUiDispatcher? dispatcher = null,
        TimeProvider? timeProvider = null)
    {
        _client = client;
        _operations = operations;
        _dispatcher = dispatcher;
        _timeProvider = timeProvider ?? TimeProvider.System;
        indexRequest = new SharedRead<LogIndex>(dispatcher);
        featureErrors.PropertyChanged += ErrorsChanged;
        operations.RunningChanged += OperationRunningChanged;

        RefreshCommand = new AsyncRelayCommand(RefreshAsync);
        ShowSourceCommand = new AsyncRelayCommand<Source>(source => source is null ? Task.CompletedTask : ShowSourceAsync(source));
        clearCommand = new AsyncRelayCommand(ClearSelectedAsync, () => CanClear);
        ClearCommand = clearCommand;
    }

    public LogIndex Index { get; private set; } = new();
    public IReadOnlyList<Source> Sources => sources;
    public IReadOnlyList<SiteSource> SiteSources => sources.OfType<SiteSource>().ToList();
    public IReadOnlyList<ServiceSource> ServiceSources => sources.OfType<ServiceSource>().ToList();
    public Source? Selection { get; private set; }
    public LogContent? Current { get; private set; }
    public IReadOnlyList<LogEntry> FilteredEntries { get; private set; } = [];
    public string FilteredText { get; private set; } = "";
    public string? Error => SelectedErrors()?.Action ?? featureErrors.Action ?? SelectedErrors()?.Refresh ?? featureErrors.Refresh;
    public string? LoadError => SelectedErrors()?.Refresh;
    public string? ActionError => SelectedErrors()?.Action ?? featureErrors.Action;
    public bool IsLoading { get; private set; }
    public bool IsClearing => clearCommand?.IsRunning == true;
    public bool CanClear => Selection is SiteSource && clearCommand?.IsRunning != true && Selection is { } source && !_operations.IsRunning(Key(source));

    public string Query
    {
        get => query;
        set
        {
            if (SetProperty(ref query, value ?? ""))
            {
                Refilter();
            }
        }
    }

    public LogLevelFilter Level
    {
        get => level;
        set
        {
            if (SetProperty(ref level, value))
            {
                Refilter();
            }
        }
    }

    public bool AutoRefreshEnabled
    {
        get => autoRefreshEnabled;
        set
        {
            if (!SetProperty(ref autoRefreshEnabled, value))
                return;

            if (value)
            {
                pollTask ??= PollAsync(lifetime.Token);
            }
            OnPropertyChanged(nameof(IsPolling));
        }
    }

    public bool IsPolling => pollTask is not null && !pollTask.IsCompleted && AutoRefreshEnabled;
    public IAsyncRelayCommand RefreshCommand { get; }
    public IAsyncRelayCommand<Source> ShowSourceCommand { get; }
    public IAsyncRelayCommand ClearCommand { get; }

    public async ValueTask DisposeAsync()
    {
        Task? polling;
        SharedRead<LogIndex>[] indexReads;
        SharedRead<LogContent>[] logReads;
        lock (gate)
        {
            if (released)
            {
                return;
            }
            released = true;
            windowActive = false;
            lifetime.Cancel();
            polling = pollTask;
            indexReads = [indexRequest];
            logReads = [.. requests.Values];
        }
        _operations.RunningChanged -= OperationRunningChanged;
        featureErrors.PropertyChanged -= ErrorsChanged;
        await Task.WhenAll(indexReads.Select(read => read.DisposeAsync().AsTask()).Concat(logReads.Select(read => read.DisposeAsync().AsTask())));
        if (polling is not null)
        {
            await polling;
        }
        lifetime.Dispose();
    }

    /// <summary>Whether the native window is visible and not minimized.</summary>
    public void SetWindowActive(bool active)
    {
        lock (gate)
        {
            windowActive = active;
        }
    }

    /// <summary>Selects a source immediately and retires the prior source's read.</summary>
    public void SelectSource(Source? source)
    {
        SharedRead<LogContent>? previousRequest = null;
        var changed = !Equals(Selection, source);
        lock (gate)
        {
            if (released)
            {
                return;
            }
            previousRequest = Selection is not null && requests.TryGetValue(Selection, out var request) ? request : null;
            selectionGeneration++;
            Selection = source;
            if (changed)
            {
                Current = null;
                IsLoading = false;
                FilteredEntries = [];
                FilteredText = "";
            }
        }
        previousRequest?.Invalidate();
        OnPropertyChanged(nameof(Selection));
        if (changed)
        {
            OnPropertyChanged(nameof(Current));
            OnPropertyChanged(nameof(IsLoading));
            OnPropertyChanged(nameof(FilteredEntries));
            OnPropertyChanged(nameof(FilteredText));
            OnPropertyChanged(nameof(LoadError));
            OnPropertyChanged(nameof(ActionError));
            OnPropertyChanged(nameof(Error));
        }
        clearCommand?.NotifyCanExecuteChanged();
    }

    /// <summary>Selects a source before waiting for the index, then loads only if that intent is current.</summary>
    public async Task ShowSourceAsync(Source source, CancellationToken observerToken = default)
    {
        ArgumentNullException.ThrowIfNull(source);
        SelectSource(source);
        var intent = selectionGeneration;
        await RefreshIndexAsync(observerToken);
        if (!IsCurrentIntent(source, intent))
        {
            return;
        }
        await RefreshSourceAsync(source, intent, observerToken);
    }

    public async Task RefreshAsync(CancellationToken observerToken = default)
    {
        var source = Selection;
        var intent = selectionGeneration;
        await RefreshIndexAsync(observerToken);
        if (source is not null && IsCurrentIntent(source, intent))
        {
            await RefreshSourceAsync(source, intent, observerToken);
        }
    }

    /// <summary>Loads a selected source without requiring an index request.</summary>
    public Task LoadSelectedAsync(Source source, CancellationToken observerToken = default)
    {
        SelectSource(source);
        return RefreshSourceAsync(source, selectionGeneration, observerToken);
    }

    public Task RefreshSelectedAsync(CancellationToken observerToken = default)
    {
        var source = Selection;
        return source is null ? Task.CompletedTask : RefreshSourceAsync(source, selectionGeneration, observerToken);
    }

    public Task RefreshIndexAsync(CancellationToken observerToken = default)
    {
        lock (gate)
        {
            if (released)
            {
                return Task.CompletedTask;
            }
        }
        return indexRequest.JoinReadAsync(
            token => _client.LogsAsync(token),
            ApplyIndex,
            PublishIndexFailure,
            observerToken,
            lifetimeToken: lifetime.Token);
    }

    public async Task ClearAsync(string site, CancellationToken applicationToken = default)
    {
        await ClearAsync(new SiteSource(site), applicationToken);
    }

    public async Task<OperationOutcome> ClearAsync(SiteSource source, CancellationToken applicationToken = default)
    {
        var errors = ErrorsFor(source);
        var request = GetRequest(source);
        var outcome = await _operations.RunMutationAsync(
            Key(source),
            errors,
            async token =>
            {
                request.Invalidate();
                try
                {
                    await _client.ClearSiteLogAsync(source.Name, token);
                }
                finally
                {
                    request.Invalidate();
                }
            },
            applicationToken);

        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshSourceAfterClearAsync(source);
        }
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        clearCommand?.NotifyCanExecuteChanged();
        return outcome;
    }

    public void DismissError()
    {
        featureErrors.ClearAction();
        featureErrors.ClearRefresh();
        foreach (var errors in resourceErrors.Values)
        {
            errors.ClearAction();
            errors.ClearRefresh();
        }
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(LoadError));
    }

    private async Task ClearSelectedAsync()
    {
        if (Selection is SiteSource site)
        {
            await ClearAsync(site);
        }
    }

    private async Task RefreshSourceAfterClearAsync(SiteSource source)
    {
        var intent = selectionGeneration;
        await RefreshSourceAsync(source, intent, CancellationToken.None);
    }

    private async Task RefreshSourceAsync(Source source, long intent, CancellationToken observerToken)
    {
        SharedRead<LogContent>? request;
        lock (gate)
        {
            if (released)
            {
                return;
            }
            if (!requests.TryGetValue(source, out request))
            {
                request = new SharedRead<LogContent>(_dispatcher);
                requests.Add(source, request);
            }
            if (Selection == source && selectionGeneration == intent)
            {
                IsLoading = true;
            }
        }
        if (IsCurrentIntent(source, intent))
        {
            OnPropertyChanged(nameof(IsLoading));
        }
        var read = request!.RunReadAsync(
            token => LoadContentAsync(source, token),
            value => ApplyContent(source, intent, value),
            error => ApplyLoadFailure(source, intent, error),
            lifetimeToken: lifetime.Token);
        await read.WaitAsync(observerToken);
    }

    private Task<LogContent> LoadContentAsync(Source source, CancellationToken token)
    {
        return source switch
        {
            SiteSource site => LoadSiteAsync(site, token),
            ServiceSource service => LoadServiceAsync(service, token),
            _ => throw new ArgumentOutOfRangeException(nameof(source))
        };
    }

    private async Task<LogContent> LoadSiteAsync(SiteSource source, CancellationToken token)
    {
        return new SiteContent(source, await _client.SiteLogAsync(source.Name, cancellationToken: token));
    }

    private async Task<LogContent> LoadServiceAsync(ServiceSource source, CancellationToken token)
    {
        return new ServiceContent(source, await _client.ServiceLogAsync(source.Name, cancellationToken: token));
    }

    private void ApplyContent(Source source, long intent, LogContent value)
    {
        if (!IsCurrentIntent(source, intent))
        {
            return;
        }
        var unchanged = Current is not null && SameContent(Current, value);
        var errors = ErrorsFor(source);
        errors.ClearRefresh();
        if (!unchanged)
        {
            Current = value;
            Refilter();
            OnPropertyChanged(nameof(Current));
            OnPropertyChanged(nameof(FilteredEntries));
            OnPropertyChanged(nameof(FilteredText));
        }
        IsLoading = false;
        OnPropertyChanged(nameof(IsLoading));
        OnPropertyChanged(nameof(LoadError));
        OnPropertyChanged(nameof(Error));
    }

    private void ApplyLoadFailure(Source source, long intent, Exception error)
    {
        if (!IsCurrentIntent(source, intent))
        {
            return;
        }
        ErrorsFor(source).SetRefresh(error);
        IsLoading = false;
        OnPropertyChanged(nameof(IsLoading));
        OnPropertyChanged(nameof(LoadError));
        OnPropertyChanged(nameof(Error));
    }

    private void ApplyIndex(LogIndex value)
    {
        if (!SameIndex(Index, value))
        {
            Index = value;
            var wanted = value.Sites.Select(name => (Source)new SiteSource(name)).Concat(value.Services.Select(name => (Source)new ServiceSource(name)));
            SynchronizeSources(wanted);
            OnPropertyChanged(nameof(Index));
            OnPropertyChanged(nameof(Sources));
            OnPropertyChanged(nameof(SiteSources));
            OnPropertyChanged(nameof(ServiceSources));
        }
        featureErrors.ClearRefresh();
        OnPropertyChanged(nameof(Error));
    }

    private void PublishIndexFailure(Exception error)
    {
        featureErrors.SetRefresh(error);
        OnPropertyChanged(nameof(Error));
    }

    private void SynchronizeSources(IEnumerable<Source> wanted)
    {
        var values = wanted.ToList();
        for (var i = sources.Count - 1; i >= 0; i--)
        {
            if (!values.Contains(sources[i]))
            {
                sources.RemoveAt(i);
            }
        }
        for (var i = 0; i < values.Count; i++)
        {
            if (i < sources.Count && sources[i] == values[i])
            {
                continue;
            }
            var existing = sources.IndexOf(values[i]);
            if (existing >= 0)
            {
                sources.Move(existing, i);
            }
            else
            {
                sources.Insert(i, values[i]);
            }
        }
    }

    private void Refilter()
    {
        switch (Current)
        {
            case SiteContent site:
                FilteredEntries = LogFilter.Filter(site.Entries, Query, Level);
                FilteredText = "";
                break;
            case ServiceContent service:
                FilteredEntries = [];
                FilteredText = LogFilter.FilterLines(service.Text, Query);
                break;
            default:
                FilteredEntries = [];
                FilteredText = "";
                break;
        }
        OnPropertyChanged(nameof(FilteredEntries));
        OnPropertyChanged(nameof(FilteredText));
    }

    private bool IsCurrentIntent(Source source, long intent)
    {
        lock (gate)
        {
            return !released && Selection == source && selectionGeneration == intent;
        }
    }

    private SharedRead<LogContent> GetRequest(Source source)
    {
        lock (gate)
        {
            if (!requests.TryGetValue(source, out var request))
            {
                request = new SharedRead<LogContent>(_dispatcher);
                requests.Add(source, request);
            }
            return request;
        }
    }

    private OperationErrors ErrorsFor(Source source)
    {
        lock (gate)
        {
            if (resourceErrors.TryGetValue(source, out var errors))
            {
                return errors;
            }
            errors = new OperationErrors();
            errors.PropertyChanged += ErrorsChanged;
            resourceErrors.Add(source, errors);
            return errors;
        }
    }

    private OperationErrors? SelectedErrors()
    {
        return Selection is not null && resourceErrors.TryGetValue(Selection, out var errors) ? errors : null;
    }

    private void ErrorsChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (!OperationErrors.IsDirectPropertyChange(args))
        {
            return;
        }
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(LoadError));
    }

    private void OperationRunningChanged(string key, bool running)
    {
        if (key.StartsWith("logs:", StringComparison.Ordinal))
        {
            OnPropertyChanged(nameof(IsClearing));
            clearCommand?.NotifyCanExecuteChanged();
        }
    }

    private string Key(Source source)
    {
        return "logs:" + (source is SiteSource ? "site:" : "service:") + source.Name;
    }

    private static bool SameIndex(LogIndex left, LogIndex right)
    {
        return left.Sites.SequenceEqual(right.Sites, StringComparer.Ordinal) && left.Services.SequenceEqual(right.Services, StringComparer.Ordinal);
    }

    private static bool SameContent(LogContent left, LogContent right)
    {
        return (left, right) switch
        {
            (SiteContent a, SiteContent b) => a.Site == b.Site && a.Entries.Count == b.Entries.Count &&
                                              a.Entries.Zip(b.Entries).All(pair => SameEntry(pair.First, pair.Second)),
            (ServiceContent a, ServiceContent b) => a.Service == b.Service && string.Equals(a.Text, b.Text, StringComparison.Ordinal),
            _ => false
        };
    }

    private static bool SameEntry(LogEntry left, LogEntry right)
    {
        return left.Time == right.Time && string.Equals(left.Env, right.Env, StringComparison.Ordinal) &&
               string.Equals(left.Level, right.Level, StringComparison.Ordinal) && string.Equals(left.Message, right.Message, StringComparison.Ordinal) &&
               string.Equals(left.Details, right.Details, StringComparison.Ordinal);
    }

    private async Task PollAsync(CancellationToken token)
    {
        try
        {
            using var timer = new PeriodicTimer(TimeSpan.FromSeconds(2), _timeProvider);
            while (await timer.WaitForNextTickAsync(token))
            {
                bool active;
                lock (gate)
                {
                    active = windowActive && !released;
                }
                if (AutoRefreshEnabled && active && Selection is not null)
                {
                    try
                    {
                        await RefreshSelectedAsync(token);
                    }
                    catch (OperationCanceledException) when (token.IsCancellationRequested)
                    {
                        return;
                    }
                }
            }
        }
        catch (OperationCanceledException) when (token.IsCancellationRequested)
        {
        }
    }

    public abstract record Source(string Name);

    public sealed record SiteSource(string Name) : Source(Name);

    public sealed record ServiceSource(string Name) : Source(Name);

    public abstract record LogContent
    {
        public Source Source => this switch
        {
            SiteContent site => site.Site,
            ServiceContent service => service.Service,
            _ => throw new ArgumentOutOfRangeException()
        };
    }

    public sealed record SiteContent(SiteSource Site, IReadOnlyList<LogEntry> Entries) : LogContent;

    public sealed record ServiceContent(ServiceSource Service, string Text) : LogContent;
}
