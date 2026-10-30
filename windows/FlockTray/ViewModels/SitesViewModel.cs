using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;

namespace Flock.Tray.ViewModels;

/// <summary>Retained site and domain rows over the daemon's accepted status snapshot.</summary>
internal sealed partial class SitesViewModel(SitesService service) : ObservableObject
{
    private readonly CollectionSynchronizer<string, SiteRowViewModel> _synchronizer = new();

    public SitesService Service { get; } = service;
    public ObservableCollection<SiteRowViewModel> Sites { get; } = [];
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Error))]
    public partial SiteRowViewModel? Selected { get; private set; }
    public string? Error => Selected is { } row ? Service.ErrorFor(row.Name) : Service.Error;
    public bool HasSites => Sites.Count > 0;

    public event Action? SelectionChanged;

    public bool Apply(Status? status)
    {
        if (status is null)
        {
            return false;
        }
        var selectedBefore = Selected;
        var worktreesBefore = WorktreeNames(selectedBefore);
        var selectedChanged = false;
        // Each site's worktrees right after it, so the list nests them.
        _synchronizer.Synchronize(Sites, status.SitesWithWorktrees(), site => site.Name, site => site.Name,
            site => new SiteRowViewModel(site), (row, site) =>
            {
                var changed = row.Apply(site);
                // A site's details list its worktrees.
                if (ReferenceEquals(row, selectedBefore) || (selectedBefore is not null && row.Snapshot.Parent == selectedBefore.Name))
                {
                    selectedChanged |= changed;
                }
            });
        if (Selected is null || !Sites.Contains(Selected))
        {
            Select(Sites.FirstOrDefault());
        }
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(HasSites));
        return !ReferenceEquals(selectedBefore, Selected) || selectedChanged || !worktreesBefore.SequenceEqual(WorktreeNames(selectedBefore));
    }

    private List<string> WorktreeNames(SiteRowViewModel? site) =>
        site is null ? [] : Sites.Where(row => row.Snapshot.Parent == site.Name).Select(row => row.Name).ToList();

    public void Select(SiteRowViewModel? row)
    {
        if (ReferenceEquals(Selected, row))
        {
            return;
        }
        Selected = row;
        SelectionChanged?.Invoke();
    }

    public void Select(string? name) => Select(name is null ? null : Sites.FirstOrDefault(site => site.Name == name));
}



/// <summary>A stable site row that keeps drafts while its status DTO reference changes.</summary>
public sealed partial class SiteRowViewModel : ObservableObject
{
    private bool? draftSecured;
    private string? draftPhp;
    private string? draftNode;
    private string? draftServer;
    private string? draftWorker;

    public SiteRowViewModel(Status.Site site)
    {
        this.Snapshot = site;
        Domains = new ObservableCollection<DomainRowViewModel>(site.Domains.Select(domain => new DomainRowViewModel(domain)));
    }

    public string Name => Snapshot.Name;
    public string Domain => Snapshot.Domain;
    public string Path => Snapshot.Path;
    public string? Url => Snapshot.Url;
    public Status.Site Snapshot { get; private set; }
    public ObservableCollection<DomainRowViewModel> Domains { get; }
    public bool Secured => draftSecured ?? Snapshot.Secured;
    public string Php => draftPhp ?? Snapshot.Php;
    public string Node => draftNode ?? Major(Snapshot.Node ?? "");
    public string Server => draftServer ?? Snapshot.Server;
    public string Worker => draftWorker ?? Snapshot.Worker;

    public bool Parked => Snapshot.Parked;
    public string PhpSource => Snapshot.PhpSource;
    public string PhpPinned => Snapshot.PhpPinned;
    public string PhpFile => Snapshot.PhpFile;
    public string NodeSource => Snapshot.NodeSource;
    public string NodePinned => Snapshot.NodePinned;
    public string NodeFile => Snapshot.NodeFile;
    public string NodeShadowed => Snapshot.NodeShadowed;
    public string? Problem => Summary(Snapshot);
    public bool OctaneAvailable => Snapshot.OctaneAvailable;
    public Status.SiteFrankenPhp? FrankenPhp => Snapshot.FrankenPhp;
    public string Boost => Snapshot.Boost;
    public bool IsFrankenPhp => Server == Status.SiteServer.FrankenPhp;
    public bool PhpPinnedHere => Snapshot.PhpPinnedHere;
    public bool NodePinnedHere => Snapshot.NodePinnedHere;
    public bool IsShared => Snapshot.IsShared;
    public string ListTitle => Snapshot.IsWorktree ? Snapshot.WorktreeLabel : Name;
    /// <summary>A worktree is indented under its site.</summary>
    public Thickness ListMargin => new(Snapshot.IsWorktree ? 20 : 0, 0, 0, 0);
    public bool IsSettingUp => Snapshot.Worktree is { IsBusy: true };
    public string SetupStep => Snapshot.Worktree is { Step.Length: > 0 } setup ? setup.Step : "Setting up…";
    public bool HasOwnDatabase => Snapshot.Worktree is { HasOwnDatabase: true };
    public string DatabaseLabel => Snapshot.Worktree is { DbMode.Length: > 0 } setup ? setup.DbMode : "own database";
    public string DatabaseTooltip => $"Its own database: {Snapshot.Worktree?.Database}";

    public Action SetDraftSecured(bool value)
    {
        var previous = draftSecured;
        SetDraft(ref draftSecured, value, nameof(Secured));
        return () => RestoreDraft(ref draftSecured, previous, nameof(Secured));
    }

    public Action SetDraftPhp(string value)
    {
        var previous = draftPhp;
        SetDraft(ref draftPhp, value, nameof(Php));
        return () => RestoreDraft(ref draftPhp, previous, nameof(Php));
    }

    public Action SetDraftNode(string value)
    {
        var previous = draftNode;
        SetDraft(ref draftNode, value, nameof(Node));
        return () => RestoreDraft(ref draftNode, previous, nameof(Node));
    }

    public Action SetDraftServer(string value)
    {
        var previous = draftServer;
        SetDraft(ref draftServer, value, nameof(Server));
        return () => RestoreDraft(ref draftServer, previous, nameof(Server));
    }

    public Action SetDraftWorker(string value)
    {
        var previous = draftWorker;
        SetDraft(ref draftWorker, value, nameof(Worker));
        return () => RestoreDraft(ref draftWorker, previous, nameof(Worker));
    }

    internal bool Apply(Status.Site value)
    {
        var renderedChanged = RenderedFieldsChanged(Snapshot, value);
        Snapshot = value;
        if (draftSecured == value.Secured) draftSecured = null;
        if (draftPhp == value.Php) draftPhp = null;
        if (draftNode == Major(value.Node ?? "")) draftNode = null;
        if (draftServer == value.Server) draftServer = null;
        if (draftWorker == value.Worker) draftWorker = null;
        var incoming = value.Domains.Select(domain => domain.Entry).ToHashSet();
        var existing = Domains.ToDictionary(domain => domain.Entry);
        for (var index = Domains.Count - 1; index >= 0; index--)
        {
            if (!incoming.Contains(Domains[index].Entry)) Domains.RemoveAt(index);
        }
        for (var index = 0; index < value.Domains.Count; index++)
        {
            var domain = value.Domains[index];
            if (!existing.TryGetValue(domain.Entry, out var row))
            {
                row = new DomainRowViewModel(domain);
                existing[domain.Entry] = row;
                Domains.Insert(index, row);
            }
            else
            {
                row.Apply(domain);
                if (ReferenceEquals(Domains[index], row))
                    continue;

                Domains.Remove(row);
                Domains.Insert(index, row);
            }
        }
        foreach (var property in new[] { nameof(Name), nameof(Domain), nameof(Path), nameof(Url), nameof(Secured), nameof(Php), nameof(Node), nameof(Server), nameof(Worker), nameof(Parked), nameof(PhpSource), nameof(PhpPinned), nameof(PhpFile), nameof(NodeSource), nameof(NodePinned), nameof(NodeFile), nameof(NodeShadowed), nameof(Problem), nameof(OctaneAvailable), nameof(FrankenPhp), nameof(Boost), nameof(IsFrankenPhp), nameof(PhpPinnedHere), nameof(NodePinnedHere), nameof(IsShared), nameof(ListTitle), nameof(ListMargin), nameof(IsSettingUp), nameof(SetupStep), nameof(HasOwnDatabase), nameof(DatabaseLabel), nameof(DatabaseTooltip) })
        {
            OnPropertyChanged(property);
        }
        return renderedChanged;
    }

    private static string? Summary(Status.Site site)
    {
        return site.Problem
               ?? (site.Worktree is { IsFailed: true, Error.Length: > 0 } setup ? setup.Error : null)
               ?? site.Notice
               ?? site.Domains.Select(domain => domain.Problem is { } problem ? $"{domain.Host}: {problem}" : null).FirstOrDefault(problem => problem is not null)
               ?? (site.IsFrankenPhp && site.FrankenPhp is { IsRunning: false, IsInstalling: false, Problem.Length: > 0 } frankenPhp ? frankenPhp.Problem : null);
    }

    private static bool RenderedFieldsChanged(Status.Site previous, Status.Site current)
    {
        return previous.Name != current.Name
               || previous.Domain != current.Domain
               || previous.Path != current.Path
               || previous.Php != current.Php
               || previous.PhpSource != current.PhpSource
               || previous.PhpPinned != current.PhpPinned
               || previous.PhpFile != current.PhpFile
               || previous.Node != current.Node
               || previous.NodeSource != current.NodeSource
               || previous.NodePinned != current.NodePinned
               || previous.NodeFile != current.NodeFile
               || previous.NodeShadowed != current.NodeShadowed
               || previous.Secured != current.Secured
               || previous.Parked != current.Parked
               || previous.Url != current.Url
               || previous.Problem != current.Problem
               || previous.Notice != current.Notice
               || previous.Root != current.Root
               || previous.RootSource != current.RootSource
               || previous.RootPinned != current.RootPinned
               || previous.RootFile != current.RootFile
               || previous.RootState != current.RootState
               || !previous.RootSuggestions.SequenceEqual(current.RootSuggestions)
               || !DomainsEqual(previous.Domains, current.Domains)
               || previous.Server != current.Server
               || previous.Worker != current.Worker
               || previous.OctaneAvailable != current.OctaneAvailable
               || !FrankenPhpEqual(previous.FrankenPhp, current.FrankenPhp)
               || previous.Boost != current.Boost
               || !ShareEqual(previous.Share, current.Share)
               || previous.Parent != current.Parent
               || previous.Branch != current.Branch
               || !WorktreeEqual(previous.Worktree, current.Worktree);
    }

    private static bool WorktreeEqual(Status.WorktreeSetup? previous, Status.WorktreeSetup? current)
    {
        return previous is null && current is null || previous is not null && current is not null &&
            previous.State == current.State && previous.Step == current.Step && previous.Error == current.Error &&
            previous.Log == current.Log && previous.DbMode == current.DbMode && previous.Database == current.Database;
    }

    private static bool DomainsEqual(IReadOnlyList<Status.SiteDomain> previous, IReadOnlyList<Status.SiteDomain> current)
    {
        return previous.Count == current.Count && previous.Zip(current).All(pair =>
            pair.First.Entry == pair.Second.Entry && pair.First.Host == pair.Second.Host && pair.First.Alias == pair.Second.Alias &&
            pair.First.Url == pair.Second.Url && pair.First.Problem == pair.Second.Problem);
    }

    private static bool FrankenPhpEqual(Status.SiteFrankenPhp? previous, Status.SiteFrankenPhp? current)
    {
        return previous is null && current is null || previous is not null && current is not null &&
            previous.State == current.State && previous.Version == current.Version && previous.PhpVersion == current.PhpVersion &&
            previous.Port == current.Port && previous.Progress == current.Progress && previous.Problem == current.Problem && previous.Log == current.Log;
    }

    private static bool ShareEqual(Status.SiteShare? previous, Status.SiteShare? current)
    {
        return previous is null && current is null || previous is not null && current is not null &&
            previous.Site == current.Site && previous.State == current.State && previous.Url == current.Url &&
            previous.Allow.SequenceEqual(current.Allow) && previous.Debug == current.Debug && previous.StartedAt == current.StartedAt &&
            previous.ExpiresAt == current.ExpiresAt && previous.Built == current.Built && previous.CanRebuild == current.CanRebuild &&
            previous.Rebuilding == current.Rebuilding && previous.RebuildError == current.RebuildError && previous.Error == current.Error &&
            previous.Warning == current.Warning && previous.Log == current.Log && previous.Qr == current.Qr;
    }

    private void SetDraft<T>(ref T? field, T value, string property) where T : struct
    {
        if (!EqualityComparer<T?>.Default.Equals(field, value))
        {
            field = value;
            OnPropertyChanged(property);
        }
    }

    private void SetDraft(ref string? field, string value, string property)
    {
        if (field == value)
            return;

        field = value;
        OnPropertyChanged(property);
    }

    private void RestoreDraft<T>(ref T? field, T? value, string property) where T : struct
    {
        if (EqualityComparer<T?>.Default.Equals(field, value))
        {
            return;
        }
        field = value;
        OnPropertyChanged(property);
    }

    private void RestoreDraft(ref string? field, string? value, string property)
    {
        if (field == value)
        {
            return;
        }
        field = value;
        OnPropertyChanged(property);
    }

    private static string Major(string value) => value.Split('.')[0];
}

/// <summary>A stable _domain row whose DTO is replaced only when status accepts a new snapshot.</summary>
public sealed partial class DomainRowViewModel(Status.SiteDomain domain) : ObservableObject
{
    private Status.SiteDomain _domain = domain;

    public string Entry => _domain.Entry;
    public string Host => _domain.Host;
    public bool Alias => _domain.Alias;
    public string? Url => _domain.Url;
    public string? Problem => _domain.Problem;

    internal void Apply(Status.SiteDomain value)
    {
        _domain = value;
        OnPropertyChanged(nameof(Entry));
        OnPropertyChanged(nameof(Host));
        OnPropertyChanged(nameof(Alias));
        OnPropertyChanged(nameof(Url));
        OnPropertyChanged(nameof(Problem));
    }
}
