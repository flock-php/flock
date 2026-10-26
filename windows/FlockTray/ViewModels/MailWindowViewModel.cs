using System.Collections.ObjectModel;
using System.ComponentModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Mail;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Owns one mail window's retained list, selection, detail, and lifetime.</summary>
public sealed class MailViewModel : ObservableObject, IAsyncDisposable
{
    private readonly MailService service;
    private readonly MailDetailViewModel detail;
    private readonly RuntimeService model;
    private readonly IDisposable visibility;
    private readonly ObservableCollection<MailRow> rows = [];
    private readonly CollectionSynchronizer<string, MailRow> synchronizer = new();
    private bool released;
    private string? selectedId;
    private Task? selectionTask;

    public MailViewModel(MailService service, MailDetailViewModel detail, RuntimeService model)
    {
        this.service = service;
        this.detail = detail;
        this.model = model;
        visibility = service.AcquireVisibility();
        service.Changed += ServiceChanged;
        model.Changed += ModelChanged;
        detail.PropertyChanged += DetailChanged;
        RefreshCommand = new AsyncRelayCommand(RefreshAsync);
        ClearCommand = new AsyncRelayCommand(ClearAsync, () => CanClear);
        ApplyMessages();
    }

    public IReadOnlyList<MailRow> Messages => rows;
    public RuntimeService Model => model;
    public string? SelectedId => selectedId;
    public MailDetailViewModel Detail => detail;
    public string? Error => service.Error ?? detail.Error;
    public string? ListError => service.Error;
    public bool IsLoading { get; private set; }
    public bool IsClearing => service.IsClearing;
    public bool CanClear => rows.Count > 0 && !service.IsClearing;
    public string SmtpAddress => model.Status?.Mail is { Length: > 0 } value ? value : "127.0.0.1:2525";
    public string Env => service.Env;
    public IAsyncRelayCommand RefreshCommand { get; }
    public IAsyncRelayCommand ClearCommand { get; }

    public event Action? Changed;

    public async Task RefreshAsync()
    {
        if (released)
        {
            return;
        }
        IsLoading = true;
        OnPropertyChanged(nameof(IsLoading));
        try
        {
            await service.RefreshAsync();
        }
        finally
        {
            IsLoading = false;
            OnPropertyChanged(nameof(IsLoading));
        }
    }

    /// <summary>Invalidates selection before any detail read starts and hides the old detail.</summary>
    public Task SelectAsync(string? id)
    {
        if (released)
        {
            return Task.CompletedTask;
        }
        if (selectedId == id)
        {
            return selectionTask ?? Task.CompletedTask;
        }
        selectedId = id;
        OnPropertyChanged(nameof(SelectedId));
        OnPropertyChanged(nameof(Error));
        Changed?.Invoke();
        selectionTask = detail.SelectAsync(id);
        return selectionTask;
    }

    public async ValueTask DisposeAsync()
    {
        if (released)
        {
            return;
        }
        released = true;
        service.Changed -= ServiceChanged;
        model.Changed -= ModelChanged;
        detail.PropertyChanged -= DetailChanged;
        visibility.Dispose();
        await detail.DisposeAsync();
        selectionTask = null;
    }

    private async Task ClearAsync()
    {
        await SelectAsync(null);
        await service.ClearAsync();
        ClearCommand.NotifyCanExecuteChanged();
    }

    private void ServiceChanged()
    {
        ApplyMessages();
        if (selectedId is not null && !rows.Any(row => row.Id == selectedId))
        {
            selectionTask = SelectAsync(null);
        }
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ListError));
        OnPropertyChanged(nameof(IsClearing));
        ClearCommand.NotifyCanExecuteChanged();
        Changed?.Invoke();
    }

    private void ModelChanged()
    {
        OnPropertyChanged(nameof(SmtpAddress));
        Changed?.Invoke();
    }

    private void DetailChanged(object? sender, PropertyChangedEventArgs args)
    {
        OnPropertyChanged(nameof(Error));
        Changed?.Invoke();
    }

    private void ApplyMessages()
    {
        synchronizer.Synchronize(rows, service.Messages, row => row.Id, summary => summary.Id,
            summary => new MailRow(summary), (row, summary) => row.Update(summary));
        OnPropertyChanged(nameof(Messages));
    }

    public sealed class MailRow : ObservableObject
    {
        private MailSummary summary;

        internal MailRow(MailSummary summary) => this.summary = summary;

        public string Id => summary.Id;
        public string Inbox => summary.Inbox;
        public string From => summary.From;
        public IReadOnlyList<string> To => summary.To;
        public string Subject => summary.Subject;
        public DateTimeOffset Date => summary.Date;
        public int Size => summary.Size;

        internal void Update(MailSummary value)
        {
            if (ReferenceEquals(summary, value))
            {
                return;
            }
            summary = value;
            OnPropertyChanged(string.Empty);
        }
    }
}

/// <summary>Owns one selected message, raw-mode generation, attachment reads, and retryable errors.</summary>
public sealed class MailDetailViewModel : ObservableObject, IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly IMailPlatform platform;
    private readonly LatestRequest detailRequest;
    private readonly LatestRequest rawRequest;
    private readonly CancellationTokenSource lifetime = new();
    private readonly object gate = new();
    private MailDetail? detail;
    private string? raw;
    private string? rawError;
    private string? detailError;
    private string? actionError;
    private string? selectedId;
    private string mode = "HTML";
    private long selectionGeneration;
    private long modeGeneration;
    private bool rawLoading;
    private bool released;

    public MailDetailViewModel(FlockClient client, IMailPlatform platform, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.platform = platform;
        detailRequest = new LatestRequest();
        rawRequest = new LatestRequest();
    }

    public MailDetail? Current => detail;
    public string? SelectedId => selectedId;
    public string Mode => mode;
    public string? Raw => raw;
    public string? RawError => rawError;
    public string? DetailError => detailError;
    public string? ActionError => actionError;
    public string? Error => actionError ?? rawError ?? detailError;
    public bool IsLoading { get; private set; }
    public bool IsRawLoading => rawLoading;

    public async Task SelectAsync(string? id)
    {
        long selection;
        lock (gate)
        {
            if (released)
            {
                return;
            }
            selection = ++selectionGeneration;
            selectedId = id;
            detail = null;
            raw = null;
            rawError = null;
            detailError = null;
            actionError = null;
            IsLoading = id is not null;
        }
        detailRequest.Invalidate();
        rawRequest.Invalidate();
        NotifySelection();
        if (id is null)
        {
            return;
        }
        try
        {
            await detailRequest.RunReadAsync(
                token => client.MailAsync(id, token),
                value => ApplyDetail(selection, id, value),
                applicationToken: CancellationToken.None,
                lifetimeToken: lifetime.Token,
                publishFailure: error => ApplyDetailFailure(selection, id, error));
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception) when (!IsCurrent(id, selection))
        {
        }
    }

    public async Task SetModeAsync(string value)
    {
        if (value is not ("HTML" or "Text" or "Raw" or "Headers"))
        {
            throw new ArgumentOutOfRangeException(nameof(value));
        }
        long selection;
        long modeGeneration;
        string? id;
        lock (gate)
        {
            if (released)
            {
                return;
            }
            mode = value;
            modeGeneration = ++this.modeGeneration;
            selection = selectionGeneration;
            id = selectedId;
            raw = null;
            rawError = null;
            rawLoading = false;
        }
        rawRequest.Invalidate();
        OnPropertyChanged(nameof(Mode));
        OnPropertyChanged(nameof(Raw));
        OnPropertyChanged(nameof(RawError));
        OnPropertyChanged(nameof(IsRawLoading));
        OnPropertyChanged(nameof(Error));
        if (value == "Raw" && id is not null && detail is not null && IsCurrent(id, selection))
        {
            await LoadRawAsync(id, selection, modeGeneration);
        }
    }

    public async Task SaveAttachmentAsync(MailDetail.Attachment attachment, IntPtr owner)
    {
        MailDetail? selected;
        lock (gate)
        {
            selected = detail;
            if (released || selected is null)
            {
                return;
            }
        }
        string? path;
        try
        {
            path = await platform.PickSaveFileAsync(owner, attachment.Filename, Path.GetExtension(attachment.Filename), lifetime.Token);
            if (path is null)
            {
                return;
            }
            var data = await client.AttachmentAsync(selected.Id, attachment.Index, lifetime.Token);
            await platform.WriteFileAsync(path, data, lifetime.Token);
            actionError = null;
            OnPropertyChanged(nameof(ActionError));
            OnPropertyChanged(nameof(Error));
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error) when (error is FlockException or IOException or UnauthorizedAccessException)
        {
            actionError = error.Message;
            OnPropertyChanged(nameof(ActionError));
            OnPropertyChanged(nameof(Error));
        }
        catch (Exception error)
        {
            actionError = error.Message;
            OnPropertyChanged(nameof(ActionError));
            OnPropertyChanged(nameof(Error));
        }
    }

    public void DismissError()
    {
        actionError = null;
        rawError = null;
        detailError = null;
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(RawError));
        OnPropertyChanged(nameof(DetailError));
        OnPropertyChanged(nameof(Error));
    }

    public async ValueTask DisposeAsync()
    {
        lock (gate)
        {
            if (released)
            {
                return;
            }
            released = true;
            ++selectionGeneration;
            ++modeGeneration;
            selectedId = null;
            detail = null;
        }
        lifetime.Cancel();
        detailRequest.Invalidate();
        rawRequest.Invalidate();
        await Task.WhenAll(detailRequest.DisposeAsync().AsTask(), rawRequest.DisposeAsync().AsTask());
        lifetime.Dispose();
    }

    private async Task LoadRawAsync(string id, long selection, long mode)
    {
        rawLoading = true;
        OnPropertyChanged(nameof(IsRawLoading));
        try
        {
            await rawRequest.RunReadAsync(
                token => client.RawMailAsync(id, token),
                value => ApplyRaw(selection, mode, id, value),
                applicationToken: CancellationToken.None,
                lifetimeToken: lifetime.Token,
                publishFailure: error => ApplyRawFailure(selection, mode, id, error));
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception) when (!IsCurrent(id, selection, mode))
        {
        }
        if (IsCurrent(id, selection, mode))
        {
            rawLoading = false;
            OnPropertyChanged(nameof(IsRawLoading));
        }
    }

    private void ApplyDetail(long generation, string id, MailDetail value)
    {
        if (!IsCurrent(id, generation))
        {
            return;
        }
        detail = value;
        IsLoading = false;
        detailError = null;
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(IsLoading));
        OnPropertyChanged(nameof(DetailError));
        OnPropertyChanged(nameof(Error));
    }

    private void ApplyDetailFailure(long generation, string id, Exception error)
    {
        if (!IsCurrent(id, generation))
        {
            return;
        }
        IsLoading = false;
        detailError = error.Message;
        OnPropertyChanged(nameof(IsLoading));
        OnPropertyChanged(nameof(DetailError));
        OnPropertyChanged(nameof(Error));
    }

    private void ApplyRaw(long selection, long mode, string id, string value)
    {
        if (!IsCurrent(id, selection, mode))
        {
            return;
        }
        raw = value;
        rawError = null;
        OnPropertyChanged(nameof(Raw));
        OnPropertyChanged(nameof(RawError));
        OnPropertyChanged(nameof(Error));
    }

    private void ApplyRawFailure(long selection, long mode, string id, Exception error)
    {
        if (!IsCurrent(id, selection, mode))
        {
            return;
        }
        raw = null;
        rawError = error.Message;
        OnPropertyChanged(nameof(Raw));
        OnPropertyChanged(nameof(RawError));
        OnPropertyChanged(nameof(Error));
    }

    private bool IsCurrent(string id, long selection, long? mode = null)
    {
        lock (gate)
        {
            return !released && selectedId == id && selectionGeneration == selection && (mode is null || modeGeneration == mode);
        }
    }

    private void NotifySelection()
    {
        OnPropertyChanged(nameof(SelectedId));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Raw));
        OnPropertyChanged(nameof(RawError));
        OnPropertyChanged(nameof(DetailError));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsLoading));
    }
}
