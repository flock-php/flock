using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Node;
using Flock.Tray.Features.Php;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Retained PHP rows and named commands for the PHP pane.</summary>
public sealed class PhpPaneViewModel : ObservableObject, IDisposable
{
    private readonly PhpService service;
    private readonly CollectionSynchronizer<string, PhpVersionViewModel> synchronizer = new();

    public PhpPaneViewModel(PhpService service)
    {
        this.service = service;
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<PhpVersionViewModel> Versions { get; } = [];
    public PhpCatalog? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        synchronizer.Synchronize(Versions, service.Current?.Versions ?? [], row => row.Minor, value => value.Minor,
            value => new PhpVersionViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
    }
}
/// <summary>Retained state and per-minor actions for one PHP release.</summary>
public sealed class PhpVersionViewModel : ObservableObject
{
    private readonly PhpService service;
    private PhpRelease value;

    public PhpVersionViewModel(PhpService service, PhpRelease value)
    {
        this.service = service;
        this.value = value;
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Minor), CanRun);
        CancelCommand = new AsyncRelayCommand(() => service.CancelAsync(Minor), CanRun);
        DismissCommand = new AsyncRelayCommand(() => service.DismissAsync(Minor), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Minor), CanRun);
    }

    public string Minor => value.Minor;
    public PhpRelease Value => value;
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public bool IsDefault => value.IsDefault;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public ComponentJob? Job => value.Job;
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand CancelCommand { get; }
    public IAsyncRelayCommand DismissCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }

    public void Update(PhpRelease next)
    {
        if (value.Minor != next.Minor)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(IsDefault));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(Job));
        InstallCommand.NotifyCanExecuteChanged();
        CancelCommand.NotifyCanExecuteChanged();
        DismissCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !service.IsRunning(Minor);
}

/// <summary>Retained FrankenPHP rows and named commands for the PHP pane.</summary>
public sealed class FrankenPhpPaneViewModel : ObservableObject, IDisposable
{
    private readonly FrankenPhpService service;
    private readonly CollectionSynchronizer<string, FrankenPhpVersionViewModel> synchronizer = new();

    public FrankenPhpPaneViewModel(FrankenPhpService service)
    {
        this.service = service;
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<FrankenPhpVersionViewModel> Builds { get; } = [];
    public FrankenPhpState? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        synchronizer.Synchronize(Builds, service.Current?.Builds ?? [], row => row.Php, value => value.Php,
            value => new FrankenPhpVersionViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
    }
}

/// <summary>Retained state and per-minor actions for one FrankenPHP build.</summary>
public sealed class FrankenPhpVersionViewModel : ObservableObject
{
    private readonly FrankenPhpService service;
    private FrankenPhpBuild value;

    public FrankenPhpVersionViewModel(FrankenPhpService service, FrankenPhpBuild value)
    {
        this.service = service;
        this.value = value;
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Php), CanRun);
        CancelCommand = new AsyncRelayCommand(() => service.CancelAsync(Php), CanRun);
        DismissCommand = new AsyncRelayCommand(() => service.DismissAsync(Php), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Php), CanRun);
    }

    public string Php => value.Php;
    public FrankenPhpBuild Value => value;
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public ComponentJob? Job => value.Job;
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand CancelCommand { get; }
    public IAsyncRelayCommand DismissCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }

    public void Update(FrankenPhpBuild next)
    {
        if (value.Php != next.Php)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(Job));
        InstallCommand.NotifyCanExecuteChanged();
        CancelCommand.NotifyCanExecuteChanged();
        DismissCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !service.IsRunning(Php);
}

/// <summary>Retained Node rows and named commands for the Node pane.</summary>
public sealed class NodePaneViewModel : ObservableObject, IDisposable
{
    private readonly NodeService service;
    private readonly CollectionSynchronizer<string, NodeVersionViewModel> synchronizer = new();

    public NodePaneViewModel(NodeService service)
    {
        this.service = service;
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<NodeVersionViewModel> Versions { get; } = [];
    public NodeCatalog? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        synchronizer.Synchronize(Versions, service.Current?.Versions ?? [], row => row.Major, value => value.Major,
            value => new NodeVersionViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
    }
}

/// <summary>Retained state and per-major actions for one Node.js release.</summary>
public sealed class NodeVersionViewModel : ObservableObject
{
    private readonly NodeService service;
    private NodeRelease value;

    public NodeVersionViewModel(NodeService service, NodeRelease value)
    {
        this.service = service;
        this.value = value;
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Major), CanRun);
        UpdateNpmCommand = new AsyncRelayCommand(() => service.UpdateNpmAsync(Major), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Major), CanRun);
        SetDefaultCommand = new AsyncRelayCommand(() => service.SetDefaultAsync(Major), CanRun);
    }

    public string Major => value.Major;
    public NodeRelease Value => value;
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public string Npm => value.Npm;
    public string NpmLatest => value.NpmLatest;
    public bool IsDefault => value.IsDefault;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand UpdateNpmCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }
    public IAsyncRelayCommand SetDefaultCommand { get; }

    public void Update(NodeRelease next)
    {
        if (value.Major != next.Major)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Npm));
        OnPropertyChanged(nameof(NpmLatest));
        OnPropertyChanged(nameof(IsDefault));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        InstallCommand.NotifyCanExecuteChanged();
        UpdateNpmCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
        SetDefaultCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !service.IsRunning(Major);
}