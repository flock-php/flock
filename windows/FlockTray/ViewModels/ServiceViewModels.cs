using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using CatalogEntry = Flock.Kit.CatalogService;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.ViewModels;

/// <summary>Retained service rows and the three shared service snapshots used by the Services pane.</summary>
public sealed class ServicesPaneViewModel : ObservableObject, IDisposable
{
    private readonly ServicesService services;
    private readonly PostgresService postgres;
    private readonly CatalogFeatureService catalog;
    private readonly CollectionSynchronizer<string, ServiceViewModel> serviceSynchronizer = new();
    private readonly CollectionSynchronizer<string, CatalogServiceViewModel> catalogSynchronizer = new();
    private string? selectedId;
    private bool showAll = true;
    private string? postgresDraftMajor;
    private int postgresDraftPort;
    private bool postgresDraftChanged;

    public ServicesPaneViewModel(ServicesService services, PostgresService postgres, CatalogFeatureService catalog)
    {
        this.services = services;
        this.postgres = postgres;
        this.catalog = catalog;
        services.Changed += RefreshRows;
        postgres.Changed += RefreshRows;
        catalog.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<ServiceViewModel> Services { get; } = [];
    public ObservableCollection<CatalogServiceViewModel> CatalogServices { get; } = [];
    public PostgresVersions? Postgres => postgres.Current;
    public ServiceCatalog? Catalog => catalog.Current;
    public string? Error => services.Error ?? postgres.Error ?? catalog.Error;
    public bool IsPostgresRunning => postgres.IsRunning;
    public bool IsCatalogChecking => catalog.IsChecking;
    public bool ShowAll
    {
        get => showAll;
        set => SetProperty(ref showAll, value);
    }
    public string? SelectedId
    {
        get => selectedId;
        set => SetProperty(ref selectedId, value);
    }
    public string? PostgresDraftMajor
    {
        get => postgresDraftMajor ?? Postgres?.Majors.FirstOrDefault()?.Major;
        set
        {
            if (SetProperty(ref postgresDraftMajor, value))
                postgresDraftChanged = true;
        }
    }
    public int PostgresDraftPort
    {
        get => postgresDraftPort == 0 ? Postgres?.Port ?? 0 : postgresDraftPort;
        set
        {
            if (SetProperty(ref postgresDraftPort, value))
                postgresDraftChanged = true;
        }
    }

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        Task.WhenAll(services.RefreshAsync(observerToken, lifetimeToken), postgres.RefreshAsync(observerToken, lifetimeToken), catalog.RefreshAsync(observerToken, lifetimeToken));

    public bool IsServiceRunning(string id) => services.IsRunning(id);
    public bool IsPostgresBusy => postgres.Current?.AnyBusy == true || postgres.IsRunning;
    public bool IsCatalogBusy(string id) => catalog.IsRunning(id) || catalog.Current?[id]?.Job?.IsRunning == true;
    public Task<OperationOutcome> CheckPostgresAsync(CancellationToken token = default) => postgres.CheckAsync(token);
    public Task<OperationOutcome> CheckCatalogAsync(CancellationToken token = default) => catalog.CheckAsync(token);

    public void Dispose()
    {
        services.Changed -= RefreshRows;
        postgres.Changed -= RefreshRows;
        catalog.Changed -= RefreshRows;
    }

    private void RefreshRows()
    {
        serviceSynchronizer.Synchronize(Services, services.Current, row => row.Id, value => value.Id,
            value => new ServiceViewModel(services, value), (row, value) => row.Update(value));
        catalogSynchronizer.Synchronize(CatalogServices, catalog.Current?.Services ?? [], row => row.Id, value => value.Id,
            value => new CatalogServiceViewModel(catalog, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Postgres));
        OnPropertyChanged(nameof(Catalog));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsPostgresRunning));
        OnPropertyChanged(nameof(IsCatalogChecking));
        OnPropertyChanged(nameof(IsPostgresBusy));
        if (!postgresDraftChanged && postgres.Current is { } currentPostgres)
        {
            postgresDraftMajor = currentPostgres.Majors.FirstOrDefault()?.Major;
            postgresDraftPort = currentPostgres.Port;
            OnPropertyChanged(nameof(PostgresDraftMajor));
            OnPropertyChanged(nameof(PostgresDraftPort));
        }
    }
}

/// <summary>Retained state and service-level commands for one daemon service.</summary>
public sealed class ServiceViewModel : ObservableObject
{
    private readonly ServicesService service;
    private Service value;
    private int draftPort;
    private bool portChanged;

    public ServiceViewModel(ServicesService service, Service value)
    {
        this.service = service;
        this.value = value;
        draftPort = value.Port;
        SavePortCommand = new AsyncRelayCommand(SavePortAsync, () => !service.IsRunning(Id));
    }

    public string Id => value.Id;
    public Service Value => value;
    public string Name => value.Name;
    public string Category => value.Category;
    public string Description => value.Description;
    public bool Available => value.Available;
    public string Unavailable => value.Unavailable;
    public string Version => value.Version;
    public int Port => value.Port;
    public bool Installed => value.Installed;
    public bool Enabled => value.Enabled;
    public bool Running => value.Running;
    public bool Stoppable => value.Stoppable;
    public string Env => value.Env;
    public string Log => value.Log;
    public string Url => value.Url;
    public string Problem => value.Problem;
    public bool IsOnRequest => value.IsOnRequest;
    public bool HasConfigurablePort => value.HasConfigurablePort;
    public bool IsDatabase => value.IsDatabase;
    public int DraftPort
    {
        get => draftPort;
        set
        {
            if (SetProperty(ref draftPort, value))
            {
                portChanged = true;
                SavePortCommand.NotifyCanExecuteChanged();
            }
        }
    }
    public IAsyncRelayCommand SavePortCommand { get; }

    public Task<OperationOutcome> StartAsync(CancellationToken token = default) => service.StartAsync(Id, token);
    public Task<OperationOutcome> StopAsync(CancellationToken token = default) => service.StopAsync(Id, token);

    public void Update(Service next)
    {
        if (value.Id != next.Id)
        {
            return;
        }
        var oldPort = value.Port;
        value = next;
        if (!portChanged || draftPort == oldPort)
        {
            draftPort = next.Port;
            portChanged = false;
            OnPropertyChanged(nameof(DraftPort));
        }
        OnPropertyChanged(nameof(Name));
        OnPropertyChanged(nameof(Category));
        OnPropertyChanged(nameof(Description));
        OnPropertyChanged(nameof(Available));
        OnPropertyChanged(nameof(Unavailable));
        OnPropertyChanged(nameof(Version));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Enabled));
        OnPropertyChanged(nameof(Running));
        OnPropertyChanged(nameof(Stoppable));
        OnPropertyChanged(nameof(Env));
        OnPropertyChanged(nameof(Log));
        OnPropertyChanged(nameof(Url));
        OnPropertyChanged(nameof(Problem));
        SavePortCommand.NotifyCanExecuteChanged();
    }

    private async Task SavePortAsync()
    {
        if (!portChanged || draftPort == value.Port)
        {
            return;
        }
        var outcome = await service.SetPortAsync(Id, draftPort);
        if (outcome == OperationOutcome.Succeeded)
        {
            portChanged = false;
        }
    }
}

/// <summary>Retained state and draft controls for one PostgreSQL version form.</summary>
public sealed class PostgresMajorViewModel : ObservableObject
{
    private PostgresMajor value;

    public PostgresMajorViewModel(PostgresService service, PostgresMajor value)
    {
        this.value = value;
        Service = service;
        InstallCommand = new AsyncRelayCommand(InstallAsync, () => !service.IsRunning);
        UpdateCommand = new AsyncRelayCommand(() => service.UpdateAsync(Major), () => !service.IsRunning);
        ActivateCommand = new AsyncRelayCommand(() => service.ActivateAsync(Major), () => !service.IsRunning);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Major, DeleteData), () => !service.IsRunning);
    }

    private PostgresService Service { get; }
    public string Major => value.Major;
    public PostgresMajor Value => value;
    public string Installed => value.Installed;
    public string Latest => value.Latest;
    public bool IsInstalled => value.IsInstalled;
    public bool Active => value.Active;
    public bool UpdateAvailable => value.Update;
    public bool HasData => value.HasData;
    public long DataSize => value.DataSize;
    public string Busy => value.Busy;
    public bool DeleteData { get; set; }
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand UpdateCommand { get; }
    public IAsyncRelayCommand ActivateCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }

    public void Update(PostgresMajor next)
    {
        if (value.Major != next.Major)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(Active));
        OnPropertyChanged(nameof(UpdateAvailable));
        OnPropertyChanged(nameof(HasData));
        OnPropertyChanged(nameof(DataSize));
        OnPropertyChanged(nameof(Busy));
        InstallCommand.NotifyCanExecuteChanged();
        UpdateCommand.NotifyCanExecuteChanged();
        ActivateCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
    }

    private Task<OperationOutcome> InstallAsync() => Service.InstallAsync(Major);
}

/// <summary>Retained catalog service state and install form drafts.</summary>
public sealed class CatalogServiceViewModel : ObservableObject
{
    private readonly CatalogFeatureService service;
    private CatalogEntry value;
    private string? draftLine;
    private int draftPort;
    private bool draftChanged;

    public CatalogServiceViewModel(CatalogFeatureService service, CatalogEntry value)
    {
        this.service = service;
        this.value = value;
        draftLine = value.PreferredLine?.Id;
        draftPort = value.Port;
        InstallCommand = new AsyncRelayCommand(InstallAsync, () => !service.IsRunning(Id));
        CancelCommand = new AsyncRelayCommand(() => service.CancelAsync(Id), () => !service.IsRunning(Id));
        DismissCommand = new AsyncRelayCommand(() => service.DismissAsync(Id), () => !service.IsRunning(Id));
    }

    public string Id => value.Id;
    public CatalogEntry Value => value;
    public string Name => value.Name;
    public string Category => value.Category;
    public string Description => value.Description;
    public bool Available => value.Available;
    public string Unavailable => value.Unavailable;
    public string Estimate => value.Estimate;
    public bool IsInstalled => value.IsInstalled;
    public string Line => value.Line;
    public string Installed => value.Installed;
    public int Port => value.Port;
    public int DefaultPort => value.DefaultPort;
    public IReadOnlyList<CatalogLine> Lines => value.Lines;
    public CatalogJob? Job => value.Job;
    public string? DraftLine
    {
        get => draftLine;
        set
        {
            if (SetProperty(ref draftLine, value))
            {
                draftChanged = true;
            }
        }
    }
    public int DraftPort
    {
        get => draftPort;
        set
        {
            if (SetProperty(ref draftPort, value))
            {
                draftChanged = true;
            }
        }
    }
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand CancelCommand { get; }
    public IAsyncRelayCommand DismissCommand { get; }

    public Task<OperationOutcome> ActivateAsync(string line, CancellationToken token = default) => service.ActivateAsync(Id, line, token);
    public Task<OperationOutcome> UninstallAsync(string? line, bool deleteData, CancellationToken token = default) => service.UninstallAsync(Id, line, deleteData, token);
    public Task<string> ReadLogAsync(CancellationToken token = default) => service.ReadLogAsync(Id, token);

    public void Update(CatalogEntry next)
    {
        if (value.Id != next.Id)
        {
            return;
        }
        var oldPort = value.Port;
        value = next;
        if (!draftChanged || (draftPort == oldPort && draftLine == value.PreferredLine?.Id))
        {
            draftLine = next.PreferredLine?.Id;
            draftPort = next.Port;
            draftChanged = false;
            OnPropertyChanged(nameof(DraftLine));
            OnPropertyChanged(nameof(DraftPort));
        }
        OnPropertyChanged(nameof(Name));
        OnPropertyChanged(nameof(Category));
        OnPropertyChanged(nameof(Description));
        OnPropertyChanged(nameof(Available));
        OnPropertyChanged(nameof(Unavailable));
        OnPropertyChanged(nameof(Estimate));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(Line));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(DefaultPort));
        OnPropertyChanged(nameof(Lines));
        OnPropertyChanged(nameof(Job));
        InstallCommand.NotifyCanExecuteChanged();
        CancelCommand.NotifyCanExecuteChanged();
        DismissCommand.NotifyCanExecuteChanged();
    }

    private async Task InstallAsync()
    {
        var outcome = await service.InstallAsync(Id, draftLine, draftPort);
        if (outcome == OperationOutcome.Succeeded)
        {
            draftChanged = false;
        }
    }
}