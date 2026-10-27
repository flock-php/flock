using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Retained panel state and commands projected from the application model.</summary>
public sealed class PanelViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService model;
    private readonly CollectionSynchronizer<string, PanelSiteRow> siteSynchronizer = new();
    private readonly CollectionSynchronizer<string, PanelServiceRow> serviceSynchronizer = new();
    private HashSet<string>? expanded;
    private bool released;

    public PanelViewModel(RuntimeService model)
    {
        this.model = model;
        model.Changed += ModelChanged;
        model.Dumps.Changed += DumpsChanged;
        ToggleDumpsCommand = new AsyncRelayCommand<bool>(SetDumpsAsync, _ => !model.Dumps.IsCaptureBusy);
        RestartCommand = new AsyncRelayCommand(model.RestartAsync, () => !IsRestarting);
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
    public UpdateFeed.Item? AvailableUpdate => model.UpdateService.Available;
    public Features.Update.UpdatePhase UpdateState => model.UpdateService.State;
    public ObservableCollection<PanelSiteRow> Sites { get; } = [];
    public ObservableCollection<PanelServiceRow> Services { get; } = [];
    public IAsyncRelayCommand<bool> ToggleDumpsCommand { get; }
    public IAsyncRelayCommand RestartCommand { get; }

    public Task SetDumpsAsync(bool enabled) => model.SetDumpsAsync(enabled);

    /// <summary>Shows or hides a site's worktrees under it, and remembers it in the preferences.</summary>
    public void ToggleWorktrees(string site)
    {
        if (!Expanded.Add(site))
        {
            Expanded.Remove(site);
        }
        RefreshRows();
        _ = SaveExpandedAsync(string.Join('\n', Expanded.Order(StringComparer.Ordinal)));
    }

    /// <summary>Shows a site's worktrees without remembering it (for screenshots).</summary>
    internal void ShowWorktrees(string site)
    {
        if (Expanded.Add(site))
        {
            RefreshRows();
        }
    }

    // Read once: the panel's own toggles are the newer state from then on.
    private HashSet<string> Expanded => expanded ??= model.Preferences.Snapshot.ExpandedSites
        .Split('\n', StringSplitOptions.RemoveEmptyEntries).ToHashSet(StringComparer.Ordinal);

    private async Task SaveExpandedAsync(string value)
    {
        try
        {
            await model.Preferences.UpdateAsync(data => data with { ExpandedSites = value });
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException or OperationCanceledException)
        {
            // PreferencesService reports a failed save (Error); the panel keeps the state until it quits.
        }
    }

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
        OnPropertyChanged(nameof(AvailableUpdate));
        OnPropertyChanged(nameof(UpdateState));
        RestartCommand.NotifyCanExecuteChanged();
        ToggleDumpsCommand.NotifyCanExecuteChanged();
    }

    private void DumpsChanged()
    {
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsCaptureBusy));
        ToggleDumpsCommand.NotifyCanExecuteChanged();
    }

    private void RefreshRows()
    {
        var status = model.Status;
        // Worktrees are listed under their site, which shows them when it is expanded.
        List<PanelSiteValue> sites = status is null ? [] : status.Sites.Where(site => !site.IsWorktree)
            .Select(site => new PanelSiteValue(site, status.WorktreesOf(site.Name), Expanded.Contains(site.Name))).ToList();
        siteSynchronizer.Synchronize(Sites, sites, row => row.Domain, value => value.Site.Domain,
            value => new PanelSiteRow(value), (row, value) => row.Update(value));

        List<PanelServiceValue> values = status is null ? [] : BuildServices(status);
        serviceSynchronizer.Synchronize(Services, values, row => row.Id, row => row.Id,
            value => new PanelServiceRow(value), (row, value) => row.Update(value));
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
        if (status.Valkey is { } valkey)
        {
            values.Add(new("valkey", "Valkey", valkey, status.Processes.FirstOrDefault(p => p.Name == "valkey")?.Up ?? false));
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

/// <summary>A site of the panel, with its worktrees and whether they are shown.</summary>
public sealed record PanelSiteValue(Status.Site Site, IReadOnlyList<Status.Site> Worktrees, bool Expanded);

public sealed class PanelSiteRow(PanelSiteValue value) : ObservableObject
{
    private Status.Site current = value.Site;
    private PanelSiteValue entry = value;

    public string Name => current.Name;
    public IReadOnlyList<Status.Site> Worktrees => entry.Worktrees;
    public bool Expanded => entry.Expanded;
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

    public void Update(PanelSiteValue value)
    {
        entry = value;
        current = value.Site;
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

    public void Update(PanelServiceValue value)
    {
        current = value;
        OnPropertyChanged(string.Empty);
    }
}

public sealed record PanelServiceValue(string Id, string Name, string Detail, bool Up);
