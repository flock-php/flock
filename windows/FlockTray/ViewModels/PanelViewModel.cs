using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray.ViewModels;

/// <summary>Retained panel state and commands projected from the application model.</summary>
public sealed class PanelViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService model;
    private readonly CollectionSynchronizer<string, PanelSiteRow> siteSynchronizer = new();
    private readonly CollectionSynchronizer<string, PanelServiceRow> serviceSynchronizer = new();
    private bool released;

    public PanelViewModel(RuntimeService model)
    {
        this.model = model;
        model.Changed += ModelChanged;
        model.Dumps.Changed += DumpsChanged;
        ToggleDumpsCommand = new AsyncRelayCommand<bool>(SetDumpsAsync, _ => !model.Dumps.IsCaptureBusy);
        ToggleXdebugCommand = new AsyncRelayCommand<bool>(SetXdebugAsync);
        RestartCommand = new AsyncRelayCommand(model.RestartAsync, () => !IsRestarting);
        InstallUpdateCommand = new RelayCommand(() => App.Current.InstallUpdate(), () => CanInstallUpdate);
        OpenMailCommand = new RelayCommand(() => App.Current.Open(WindowKind.Mail));
        OpenDumpsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Dumps));
        OpenLogsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Logs));
        OpenSetupCommand = new RelayCommand(() => App.Current.Open(WindowKind.Setup));
        OpenSettingsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Settings));
        QuitCommand = new RelayCommand(() => App.Current.Quit());
        RefreshRows();
    }

    public RuntimeService Model => model;
    public Status? Status => model.Status;
    public string? Error => model.Error;
    public string? ActionError => model.RuntimeError ?? model.ActionError ?? model.Dumps.ActionError;
    public bool IsRunning => model.IsRunning;
    public bool IsDegraded => model.IsDegraded;
    public bool IsRestarting => model.IsRestarting;
    public bool IsUpdating => model.IsUpdating;
    public bool IsCaptureBusy => model.Dumps.IsCaptureBusy;
    public bool NeedsSetup => model.NeedsSetup;
    public string Headline => model.Headline;
    public string DisplayName => Application.Current is App app ? app.Options.Identity.DisplayName : "Flock";
    public UpdateFeed.Item? AvailableUpdate => model.UpdateService.Available;
    public Features.Update.UpdatePhase UpdateState => model.UpdateService.State;
    public bool HasStatusContent => Status is not null && !NeedsSetup;
    public bool HasSites => HasStatusContent && Sites.Count > 0;
    public string NotRunningMessage => model.Error ?? "Flock is not running";
    public bool HasActionError => ActionError is not null;
    public bool HasUpdate => AvailableUpdate is not null;
    public bool IsUpdateBusy => model.UpdateService.IsBusy;
    public bool CanInstallUpdate => HasUpdate && !IsUpdateBusy;
    public string UpdateMessage => !IsUpdateBusy && AvailableUpdate is { } update ? $"Flock {update.Version} is available" : model.UpdateService.Summary;
    public int MailCount => Status?.MailCount ?? 0;
    public int DumpCount => Status?.DumpCount ?? 0;
    public Visibility RestartVisibility => IsRestarting ? Visibility.Visible : Visibility.Collapsed;
    public Visibility UpdateVisibility => HasUpdate ? Visibility.Visible : Visibility.Collapsed;
    public Visibility StatusContentVisibility => HasStatusContent ? Visibility.Visible : Visibility.Collapsed;
    public Visibility WarningsVisibility => Status?.Warnings.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    public Visibility SitesVisibility => HasSites ? Visibility.Visible : Visibility.Collapsed;
    public Visibility EmptySitesVisibility => HasSites ? Visibility.Collapsed : Visibility.Visible;
    public Visibility NotRunningVisibility => HasStatusContent ? Visibility.Collapsed : Visibility.Visible;
    public Visibility SetupVisibility => NeedsSetup ? Visibility.Visible : Visibility.Collapsed;
    public Visibility UpdatingVisibility => !NeedsSetup && IsUpdating ? Visibility.Visible : Visibility.Collapsed;
    public Visibility StoppedVisibility => !NeedsSetup && !IsUpdating ? Visibility.Visible : Visibility.Collapsed;
    public Visibility FooterVisibility => HasStatusContent ? Visibility.Visible : Visibility.Collapsed;
    public Visibility MailCountVisibility => MailCount > 0 ? Visibility.Visible : Visibility.Collapsed;
    public Visibility DumpCountVisibility => DumpCount > 0 ? Visibility.Visible : Visibility.Collapsed;
    public bool CaptureEnabled => !IsCaptureBusy;
    public bool StartEnabled => !IsRestarting;
    public bool RestartEnabled => IsRunning && !IsRestarting;
    public ObservableCollection<PanelSiteRow> Sites { get; } = [];
    public ObservableCollection<PanelServiceRow> Services { get; } = [];
    public IAsyncRelayCommand<bool> ToggleDumpsCommand { get; }
    public IAsyncRelayCommand<bool> ToggleXdebugCommand { get; }
    public IAsyncRelayCommand RestartCommand { get; }
    public IRelayCommand InstallUpdateCommand { get; }
    public IRelayCommand OpenMailCommand { get; }
    public IRelayCommand OpenDumpsCommand { get; }
    public IRelayCommand OpenLogsCommand { get; }
    public IRelayCommand OpenSetupCommand { get; }
    public IRelayCommand OpenSettingsCommand { get; }
    public IRelayCommand QuitCommand { get; }

    public Task SetDumpsAsync(bool enabled) => model.SetDumpsAsync(enabled);

    public Task SetXdebugAsync(bool enabled) => model.SetXdebugAsync(enabled);

    public void OpenSite(string url) => model.Open(url);

    public void DismissError()
    {
        model.DismissError();
        model.Dumps.DismissError();
    }

    public void Dispose()
    {
        if (released)
        {
            return;
        }
        released = true;
        model.Changed -= ModelChanged;
        model.Dumps.Changed -= DumpsChanged;
    }

    private void ModelChanged()
    {
        RefreshRows();
        OnPropertyChanged(nameof(Status));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsRunning));
        OnPropertyChanged(nameof(IsDegraded));
        OnPropertyChanged(nameof(IsRestarting));
        OnPropertyChanged(nameof(IsUpdating));
        OnPropertyChanged(nameof(IsCaptureBusy));
        OnPropertyChanged(nameof(NeedsSetup));
        OnPropertyChanged(nameof(Headline));
        OnPropertyChanged(nameof(DisplayName));
        OnPropertyChanged(nameof(AvailableUpdate));
        OnPropertyChanged(nameof(UpdateState));
        OnPropertyChanged(nameof(HasStatusContent));
        OnPropertyChanged(nameof(HasSites));
        OnPropertyChanged(nameof(NotRunningMessage));
        OnPropertyChanged(nameof(HasActionError));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(IsUpdateBusy));
        OnPropertyChanged(nameof(CanInstallUpdate));
        OnPropertyChanged(nameof(UpdateMessage));
        OnPropertyChanged(nameof(MailCount));
        OnPropertyChanged(nameof(DumpCount));
        OnPropertyChanged(nameof(RestartVisibility));
        OnPropertyChanged(nameof(UpdateVisibility));
        OnPropertyChanged(nameof(StatusContentVisibility));
        OnPropertyChanged(nameof(WarningsVisibility));
        OnPropertyChanged(nameof(SitesVisibility));
        OnPropertyChanged(nameof(EmptySitesVisibility));
        OnPropertyChanged(nameof(NotRunningVisibility));
        OnPropertyChanged(nameof(SetupVisibility));
        OnPropertyChanged(nameof(UpdatingVisibility));
        OnPropertyChanged(nameof(StoppedVisibility));
        OnPropertyChanged(nameof(FooterVisibility));
        OnPropertyChanged(nameof(MailCountVisibility));
        OnPropertyChanged(nameof(DumpCountVisibility));
        OnPropertyChanged(nameof(CaptureEnabled));
        OnPropertyChanged(nameof(StartEnabled));
        OnPropertyChanged(nameof(RestartEnabled));
        RestartCommand.NotifyCanExecuteChanged();
        ToggleDumpsCommand.NotifyCanExecuteChanged();
        InstallUpdateCommand.NotifyCanExecuteChanged();
    }

    private void DumpsChanged()
    {
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsCaptureBusy));
        OnPropertyChanged(nameof(HasActionError));
        OnPropertyChanged(nameof(UpdatingVisibility));
        OnPropertyChanged(nameof(StoppedVisibility));
        OnPropertyChanged(nameof(CaptureEnabled));
        ToggleDumpsCommand.NotifyCanExecuteChanged();
    }

    private void RefreshRows()
    {
        var status = model.Status;
        siteSynchronizer.Synchronize(Sites, status?.Sites ?? [], row => row.Domain, site => site.Domain,
            site => new PanelSiteRow(site), (row, site) => row.Update(site));

        List<PanelServiceValue> values = status is null ? [] : BuildServices(status);
        serviceSynchronizer.Synchronize(Services, values, row => row.Id, row => row.Id,
            value => new PanelServiceRow(value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(HasSites));
    }

    private static List<PanelServiceValue> BuildServices(Status status)
    {
        var values = new List<PanelServiceValue>
        {
            new("web", "Web", $"http :{status.HttpPort} · https :{status.HttpsPort}", true),
        };
        if (status.Dns.Wildcard)
        {
            values.Add(new("dns", "DNS", $"*.{status.Dns.Tld}", true));
        }
        else if (status.Dns.Tld.Length > 0)
        {
            var helperDown = status.Warnings.Any(w => w.Contains("hosts helper", StringComparison.Ordinal));
            values.Add(new("hosts", "Hosts", "site domains only", !helperDown));
        }
        values.Add(new("mail", "Mail", status.Mail.Length == 0 ? "stopped" : $"smtp {status.Mail}", status.Mail.Length > 0));
        if (status.Postgres is { } postgres)
        {
            values.Add(new("postgres", "PostgreSQL", postgres, status.Processes.FirstOrDefault(p => p.Name == "postgres")?.Up ?? false));
        }
        foreach (var extra in status.Extras)
        {
            values.Add(new(extra.Name, extra.Name, extra.Enabled ? $"{extra.Version} on 127.0.0.1:{extra.Port}" : "stopped", extra.Up));
        }
        foreach (var pool in status.Processes.Where(p => p.Name.StartsWith("php", StringComparison.Ordinal)).GroupBy(p => PoolName(p.Name)))
        {
            var workers = pool.ToList();
            var up = workers.Count(worker => worker.Up);
            var detail = up == 0
                ? workers.FirstOrDefault(worker => worker.LastError is not null)?.LastError ?? "down"
                : workers.Count > 1 ? $"{up} of {workers.Count} workers up" : "up";
            var restarts = workers.Sum(worker => worker.Restarts);
            if (up > 0 && restarts > 0)
            {
                detail += $" (restarted {restarts}×)";
            }
            values.Add(new PanelServiceValue(pool.Key, pool.Key, detail, up > 0));
        }
        foreach (var process in status.Processes.Where(p => !p.Up && !p.Name.StartsWith("php", StringComparison.Ordinal)))
        {
            values.Add(new PanelServiceValue("process:" + process.Name, process.Name, process.LastError ?? "down", false));
        }
        return values;
    }

    private static string PoolName(string process)
    {
        var match = System.Text.RegularExpressions.Regex.Match(process, @"^php(\d+\.\d+)-(?:cgi|fpm)");
        return match.Success ? $"PHP {match.Groups[1].Value}" : process;
    }
}

public sealed class PanelSiteRow(Status.Site value) : ObservableObject
{
    private Status.Site current = value;

    public string Domain => current.Domain;
    public string Path => current.Path;
    public string? Url => current.Url;
    public string? Problem => current.Problem;
    public bool Secured => current.Secured;
    public string Php => current.Php;
    public bool Debugging => current.Debugging;
    public bool IsFrankenPhp => current.IsFrankenPhp;
    public string Worker => current.Worker;
    public bool IsShared => current.IsShared;
    public string? ShareUrl => current.Share?.Url;
    public bool HasUrl => Url is not null;
    public string SiteTooltip => Url ?? Problem ?? Path;
    public string SecurityGlyph => Secured ? "\uE72E" : "\uE774";
    public string PhpLabel => $"PHP {Php}";
    public string WorkerLabel => Worker == Status.SiteServer.Octane ? "Octane" : "FrankenPHP";
    public string ShareTooltip => $"Shared at {ShareUrl}";
    public Visibility ProblemVisibility => Problem is not null ? Visibility.Visible : Visibility.Collapsed;
    public Visibility DebuggingVisibility => Debugging ? Visibility.Visible : Visibility.Collapsed;
    public Visibility FrankenPhpVisibility => IsFrankenPhp ? Visibility.Visible : Visibility.Collapsed;
    public Visibility SharedVisibility => IsShared ? Visibility.Visible : Visibility.Collapsed;
    public Brush SecurityBrush => (Brush)Application.Current.Resources[Secured ? "SystemFillColorSuccessBrush" : "TextFillColorSecondaryBrush"];

    public void Update(Status.Site value)
    {
        current = value;
        OnPropertyChanged(string.Empty);
    }
}

public sealed class PanelServiceRow(PanelServiceValue value) : ObservableObject
{
    private PanelServiceValue current = value;

    public string Id => current.Id;
    public string Name => current.Name;
    public string Detail => current.Detail;
    public bool Up => current.Up;
    public Brush StatusBrush => (Brush)Application.Current.Resources[Up ? "SystemFillColorSuccessBrush" : "ControlStrongFillColorDisabledBrush"];

    public void Update(PanelServiceValue value)
    {
        current = value;
        OnPropertyChanged(string.Empty);
    }
}

public sealed record PanelServiceValue(string Id, string Name, string Detail, bool Up);
