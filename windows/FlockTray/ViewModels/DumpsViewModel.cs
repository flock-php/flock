using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Dumps;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned dump timeline state, preferences, and commands.</summary>
public sealed class DumpsViewModel : ObservableObject, IDisposable
{
    private readonly DumpsService service;
    private readonly PreferencesService preferences;
    private readonly RuntimeService? model;
    private string query = "";
    private bool released;

    public DumpsViewModel(DumpsService service, PreferencesService preferences, RuntimeService model)
    {
        this.service = service;
        this.preferences = preferences;
        this.model = model;
        service.Changed += NotifyState;
        preferences.Changed += PreferencesChanged;
        preferences.ErrorChanged += NotifyState;
        model.Changed += NotifyState;
        model.Configuration.Changed += NotifyState;
        SetDumpsCommand = new AsyncRelayCommand<bool>(SetDumpsAsync, _ => !service.IsCaptureBusy);
        ClearCommand = new AsyncRelayCommand(ClearAsync, () => CanClear);
    }

    public DumpsViewModel(RuntimeService model) : this(model.Dumps, model.Preferences, model)
    {
    }

    public IReadOnlyList<Dump> Dumps => service.Dumps;
    public IReadOnlyList<RuntimeEvent> Events => service.Events;
    public RuntimeService Model => model ?? throw new InvalidOperationException("The dump view model has no runtime owner.");
    public Status? Status => model?.Status;
    public IReadOnlyList<Dump> VisibleDumps =>
        DumpTimeline.Filter(LatestOnly ? DumpTimeline.LatestGroup(service.Dumps, service.Events).Dumps : service.Dumps, Query);
    public IReadOnlyList<RuntimeEvent> VisibleEvents =>
        DumpTimeline.Filter(LatestOnly ? DumpTimeline.LatestGroup(service.Dumps, service.Events).Events : service.Events.Where(e => e.IsKnown).ToList(), Query);
    public string? Header => service.Header;
    public bool HeaderMissing => service.HeaderMissing;
    public bool Capturing => model?.Status?.Dumps == true;
    public int Count => model?.Status?.DumpCount ?? service.Dumps.Count;
    public string Editor => preferences.Snapshot.Editor;
    public bool LatestOnly => preferences.Snapshot.DumpsLatestOnly;
    public string? Error => service.Error ?? preferences.Error ?? model?.Configuration.Error ?? model?.ActionError;
    public string? ActionError => service.ActionError ?? preferences.ActionError ?? model?.ActionError;
    public bool IsBusy => service.IsClearing || service.IsCaptureBusy;
    public bool CanClear => (service.Dumps.Count > 0 || service.Events.Count > 0) && !service.IsClearing;

    public string Query
    {
        get => query;
        set
        {
            if (SetProperty(ref query, value ?? ""))
            {
                OnPropertyChanged(nameof(VisibleDumps));
                OnPropertyChanged(nameof(VisibleEvents));
            }
        }
    }

    public IAsyncRelayCommand<bool> SetDumpsCommand { get; }
    public IAsyncRelayCommand ClearCommand { get; }

    public Task SetDumpsAsync(bool enabled) => service.SetCaptureAsync(enabled);

    public Task SetCaptureAsync(string kind, bool enabled)
    {
        var off = (Status?.CaptureOff ?? []).Where(value => value != kind).ToList();
        if (!enabled)
        {
            off.Add(kind);
        }
        return Model.Configuration.UpdateAsync(new SettingsChange(CaptureOff: off));
    }

    public Task SetDumpsInPageAsync(bool enabled) => Model.Configuration.UpdateAsync(new SettingsChange(DumpsInPage: enabled));

    public Task RefreshAsync(CancellationToken cancellationToken = default) => service.RefreshAsync(cancellationToken);

    public async Task SetLatestOnlyAsync(bool value, CancellationToken cancellationToken = default)
    {
        await preferences.UpdateAsync(current => current with { DumpsLatestOnly = value }, cancellationToken);
    }

    public async Task SetEditorAsync(Editor editor, CancellationToken cancellationToken = default)
    {
        await preferences.UpdateAsync(current => current with { Editor = editor.ToString() }, cancellationToken);
    }

    public Task<OperationOutcome> ClearAsync() => service.ClearAsync();

    public void DismissError()
    {
        service.DismissError();
        preferences.DismissError();
        model?.DismissError();
        model?.Configuration.DismissError();
    }

    public void Dispose()
    {
        if (released)
        {
            return;
        }
        released = true;
        service.Changed -= NotifyState;
        preferences.Changed -= PreferencesChanged;
        preferences.ErrorChanged -= NotifyState;
        if (model is not null)
        {
            model.Changed -= NotifyState;
            model.Configuration.Changed -= NotifyState;
        }
        visibility?.Dispose();
        visibility = null;
    }

    private IDisposable? visibility;

    public void AcquireVisibility()
    {
        visibility ??= service.AcquireVisibility();
    }

    private void PreferencesChanged(PreferenceData _) => NotifyState();

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Dumps));
        OnPropertyChanged(nameof(Events));
        OnPropertyChanged(nameof(VisibleEvents));
        OnPropertyChanged(nameof(Status));
        OnPropertyChanged(nameof(VisibleDumps));
        OnPropertyChanged(nameof(Header));
        OnPropertyChanged(nameof(HeaderMissing));
        OnPropertyChanged(nameof(Capturing));
        OnPropertyChanged(nameof(Count));
        OnPropertyChanged(nameof(Editor));
        OnPropertyChanged(nameof(LatestOnly));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsBusy));
        OnPropertyChanged(nameof(CanClear));
        ClearCommand.NotifyCanExecuteChanged();
        SetDumpsCommand.NotifyCanExecuteChanged();
    }
}
