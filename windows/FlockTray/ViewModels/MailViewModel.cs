using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned projections for the Mail settings pane.</summary>
public sealed class MailSettingsViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService model;

    public MailSettingsViewModel(RuntimeService model)
    {
        this.model = model;
        model.Configuration.Changed += NotifyState;
        model.Services.Changed += NotifyState;
    }

    public Flock.Kit.Service? Mail => model.Services.Current.FirstOrDefault(service => service.Id == "mail");
    public int Port => model.Configuration.Current?.Ports.Smtp ?? Mail?.Port ?? 0;
    public bool Running => Mail?.Running == true;
    public string Environment => Mail?.Env ?? "";

    public void Dispose()
    {
        model.Configuration.Changed -= NotifyState;
        model.Services.Changed -= NotifyState;
    }

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Mail));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(Running));
        OnPropertyChanged(nameof(Environment));
    }
}