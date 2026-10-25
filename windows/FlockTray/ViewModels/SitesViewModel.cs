using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;

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

    public event Action? SelectionChanged;

    public void Apply(Status? status)
    {
        if (status is null)
        {
            return;
        }
        _synchronizer.Synchronize(Sites, status.Sites, site => site.Name, site => site.Name,
            site => new SiteRowViewModel(site), (row, site) => row.Apply(site));
        if (Selected is null || !Sites.Contains(Selected))
        {
            Select(Sites.FirstOrDefault());
        }
        OnPropertyChanged(nameof(Error));
    }

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
    public string? Problem => Snapshot.Problem;
    public bool OctaneAvailable => Snapshot.OctaneAvailable;
    public Status.SiteFrankenPhp? FrankenPhp => Snapshot.FrankenPhp;
    public string Boost => Snapshot.Boost;
    public bool IsFrankenPhp => Server == Status.SiteServer.FrankenPhp;
    public bool PhpPinnedHere => Snapshot.PhpPinnedHere;
    public bool NodePinnedHere => Snapshot.NodePinnedHere;
    public bool IsShared => Snapshot.IsShared;

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

    internal void Apply(Status.Site value)
    {
        Snapshot = value;
        if (draftSecured == value.Secured) draftSecured = null;
        if (draftPhp == value.Php) draftPhp = null;
        if (draftNode == Major(value.Node ?? "")) draftNode = null;
        if (draftServer == value.Server) draftServer = null;
        if (draftWorker == value.Worker) draftWorker = null;
        var incoming = value.Domains.ToDictionary(domain => domain.Entry);
        for (var index = Domains.Count - 1; index >= 0; index--)
        {
            if (!incoming.ContainsKey(Domains[index].Entry)) Domains.RemoveAt(index);
        }
        for (var index = 0; index < value.Domains.Count; index++)
        {
            var domain = value.Domains[index];
            var row = Domains.FirstOrDefault(item => item.Entry == domain.Entry);
            if (row is null)
                Domains.Insert(index, new DomainRowViewModel(domain));
            else
            {
                row.Apply(domain);
                if (Domains.IndexOf(row) == index)
                    continue;

                Domains.Remove(row);
                Domains.Insert(index, row);
            }
        }
        foreach (var property in new[] { nameof(Name), nameof(Domain), nameof(Path), nameof(Url), nameof(Secured), nameof(Php), nameof(Node), nameof(Server), nameof(Worker), nameof(Parked), nameof(PhpSource), nameof(PhpPinned), nameof(PhpFile), nameof(NodeSource), nameof(NodePinned), nameof(NodeFile), nameof(NodeShadowed), nameof(Problem), nameof(OctaneAvailable), nameof(FrankenPhp), nameof(Boost), nameof(IsFrankenPhp), nameof(PhpPinnedHere), nameof(NodePinnedHere), nameof(IsShared) })
        {
            OnPropertyChanged(property);
        }
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
