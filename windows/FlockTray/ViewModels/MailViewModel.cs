using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned projections for the Mail settings pane.</summary>
public sealed partial class MailSettingsViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService _model;

    public MailSettingsViewModel(RuntimeService model)
    {
        _model = model;
        model.Configuration.Changed += NotifyState;
        model.Services.Changed += NotifyState;
        model.Changed += NotifyState;
    }

    public Flock.Kit.Service? Mail => _model.Services.Current.FirstOrDefault(service => service.Id == "mail");
    public int Port => _model.Configuration.Current?.Ports.Smtp ?? Mail?.Port ?? 0;
    public bool Running => Mail?.Running == true;
    public string Environment => Mail?.Env ?? "";
    public int Count => _model.Status?.MailCount ?? 0;

    public void Dispose()
    {
        _model.Configuration.Changed -= NotifyState;
        _model.Services.Changed -= NotifyState;
        _model.Changed -= NotifyState;
    }

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Mail));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(Running));
        OnPropertyChanged(nameof(Environment));
        OnPropertyChanged(nameof(Count));
    }
}
