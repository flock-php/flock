using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Infrastructure;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned state and commands for the General settings pane.</summary>
public sealed partial class GeneralViewModel : ObservableObject, IDisposable
{
    private readonly ConfigurationService configuration;
    private readonly RuntimeService? model;
    private readonly Func<Task> restart;
    private readonly Func<bool> isRestarting;

    public GeneralViewModel(RuntimeService model)
        : this(model.Configuration, model.RestartAsync, () => model.IsRestarting, () => model.Status, () => model.Error ?? model.ActionError)
    {
        this.model = model;
        model.Changed += NotifyState;
    }

    public GeneralViewModel(ConfigurationService configuration, Func<Task>? restart = null, Func<bool>? isRestarting = null,
        Func<Status?>? status = null, Func<string?>? runtimeError = null)
    {
        this.configuration = configuration;
        this.restart = restart ?? (() => Task.CompletedTask);
        this.isRestarting = isRestarting ?? (() => false);
        statusValue = status ?? (() => null);
        runtimeErrorValue = runtimeError ?? (() => null);
        configuration.Changed += NotifyState;
    }

    private readonly Func<Status?> statusValue;
    private readonly Func<string?> runtimeErrorValue;

    public FlockConfig? Config => configuration.Current;
    public Status? Status => statusValue();
    public string? Error => configuration.Error ?? runtimeErrorValue();
    public bool IsBusy => configuration.IsRunning || isRestarting();
    public Visibility StatusVisibility => Status is null ? Visibility.Collapsed : Visibility.Visible;
    public string StatusText => Status?.IsDegraded == true ? "Needs attention" : "Running";
    public Brush StatusBrush => Status?.IsDegraded == true ? Ui.Caution : Ui.Success;
    public string VersionText => Status?.Version ?? "";
    public string ProcessText => Status is { } status ? $"pid {status.Pid}" : "";
    public Visibility ConfigVisibility => Config is null ? Visibility.Collapsed : Visibility.Visible;
    public string DomainDescription => Status?.Dns is { Wildcard: true }
        ? "Every name, any subdomain included, is resolved by Flock's DNS server. Site domains are also in the hosts file."
        : $"Only site domains and the domains added to sites resolve (the hosts file){(Status?.Dns?.WildcardProblem is { Length: > 0 } problem ? $": {problem}" : string.Empty)}.";
    public string DomainValue => Config is { } config ? $"*.{config.Tld}" : "";
    public string ProjectFoldersDescription => Config is { } config
        ? $"Every folder inside a project folder is served as <folder>.{config.Tld} — the same as `flock park`."
        : "";
    public string Root => Config?.Root ?? "";
    public string PortsDescription => Config is { } config && (config.Ports.Http != 80 || config.Ports.Https != 443)
        ? $"Sites use ports {config.Ports.Http} (HTTP) and {config.Ports.Https} (HTTPS), set in config.json, so their addresses include the port."
        : "";
    public Visibility PortsVisibility => PortsDescription.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    public bool IsBeta => Config?.Channel == "beta";
    public bool BetaEnabled => Config?.Channel != "experimental";
    public Visibility ExperimentalVisibility => Config?.Channel == "experimental" ? Visibility.Visible : Visibility.Collapsed;
    public Task RefreshAsync(CancellationToken observerToken = default) => configuration.RefreshAsync(observerToken);
    public Task SetChannelAsync(string channel) => configuration.SetChannelAsync(channel);
    public Task SetDefaultPhpAsync(string version) => configuration.SetDefaultPhpAsync(version);
    public Task SetDefaultNodeAsync(string major) => configuration.SetDefaultNodeAsync(major);

    [RelayCommand]
    private Task Refresh() => RefreshAsync();

    [RelayCommand(CanExecute = nameof(CanRestart))]
    private Task Restart() => restart();

    private bool CanRestart() => !isRestarting();

    [RelayCommand]
    private Task SetChannel(string? value) => value is null ? Task.CompletedTask : SetChannelAsync(value);

    [RelayCommand]
    private Task SetDefaultPhp(string? value) => value is null ? Task.CompletedTask : SetDefaultPhpAsync(value);

    [RelayCommand]
    private Task SetDefaultNode(string? value) => value is null ? Task.CompletedTask : SetDefaultNodeAsync(value);

    public void Dispose()
    {
        configuration.Changed -= NotifyState;
        if (model is not null)
        {
            model.Changed -= NotifyState;
        }
    }

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Config));
        OnPropertyChanged(nameof(Status));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsBusy));
        OnPropertyChanged(nameof(StatusVisibility));
        OnPropertyChanged(nameof(StatusText));
        OnPropertyChanged(nameof(StatusBrush));
        OnPropertyChanged(nameof(VersionText));
        OnPropertyChanged(nameof(ProcessText));
        OnPropertyChanged(nameof(ConfigVisibility));
        OnPropertyChanged(nameof(DomainDescription));
        OnPropertyChanged(nameof(DomainValue));
        OnPropertyChanged(nameof(ProjectFoldersDescription));
        OnPropertyChanged(nameof(Root));
        OnPropertyChanged(nameof(PortsDescription));
        OnPropertyChanged(nameof(PortsVisibility));
        OnPropertyChanged(nameof(IsBeta));
        OnPropertyChanged(nameof(BetaEnabled));
        OnPropertyChanged(nameof(ExperimentalVisibility));
        RestartCommand.NotifyCanExecuteChanged();
    }
}
