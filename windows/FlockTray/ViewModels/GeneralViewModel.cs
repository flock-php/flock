using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned state and commands for the General settings pane.</summary>
public sealed class GeneralViewModel : ObservableObject, IDisposable
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
        RefreshCommand = new AsyncRelayCommand(RefreshAsync);
        RestartCommand = new AsyncRelayCommand(this.restart, () => !this.isRestarting());
        SetChannelCommand = new AsyncRelayCommand<string?>(value => value is null ? Task.CompletedTask : SetChannelAsync(value));
        SetDefaultPhpCommand = new AsyncRelayCommand<string?>(value => value is null ? Task.CompletedTask : SetDefaultPhpAsync(value));
        SetDefaultNodeCommand = new AsyncRelayCommand<string?>(value => value is null ? Task.CompletedTask : SetDefaultNodeAsync(value));
    }

    private readonly Func<Status?> statusValue;
    private readonly Func<string?> runtimeErrorValue;

    public FlockConfig? Config => configuration.Current;
    public Status? Status => statusValue();
    public string? Error => configuration.Error ?? runtimeErrorValue();
    public bool IsBusy => configuration.IsRunning || isRestarting();
    public IAsyncRelayCommand RefreshCommand { get; }
    public IAsyncRelayCommand RestartCommand { get; }
    public IAsyncRelayCommand<string?> SetChannelCommand { get; }
    public IAsyncRelayCommand<string?> SetDefaultPhpCommand { get; }
    public IAsyncRelayCommand<string?> SetDefaultNodeCommand { get; }

    public Task RefreshAsync(CancellationToken observerToken = default) => configuration.RefreshAsync(observerToken);
    public Task SetChannelAsync(string channel) => configuration.SetChannelAsync(channel);
    public Task SetDefaultPhpAsync(string version) => configuration.SetDefaultPhpAsync(version);
    public Task SetDefaultNodeAsync(string major) => configuration.SetDefaultNodeAsync(major);

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
        RestartCommand.NotifyCanExecuteChanged();
    }
}