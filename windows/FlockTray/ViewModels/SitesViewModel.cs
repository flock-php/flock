using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Retained site and domain rows over the daemon's accepted status snapshot.</summary>
public sealed class SitesViewModel : ObservableObject
{
    private readonly CollectionSynchronizer<string, SiteRowViewModel> synchronizer = new();
    private SiteRowViewModel? selected;

    public SitesViewModel(SitesService service)
    {
        Service = service;
    }

    public SitesService Service { get; }
    public ObservableCollection<SiteRowViewModel> Sites { get; } = [];
    public SiteRowViewModel? Selected => selected;
    public string? Error => selected is { } row ? Service.ErrorFor(row.Name) : Service.Error;

    public event Action? SelectionChanged;

    public void Apply(Status? status)
    {
        if (status is null)
        {
            return;
        }
        synchronizer.Synchronize(Sites, status.Sites, site => site.Name, site => site.Name,
            site => new SiteRowViewModel(site), (row, site) => row.Apply(site));
        foreach (var row in Sites)
        {
            if (!ReferenceEquals(row, selected))
            {
                row.IsSelected = false;
            }
        }
        if (selected is null || !Sites.Contains(selected))
        {
            Select(Sites.FirstOrDefault());
        }
        else
        {
            selected.IsSelected = true;
        }
        OnPropertyChanged(nameof(Error));
    }

    public void Select(SiteRowViewModel? row)
    {
        if (ReferenceEquals(selected, row))
        {
            return;
        }
        if (selected is not null)
        {
            selected.IsSelected = false;
        }
        selected = row;
        if (selected is not null)
        {
            selected.IsSelected = true;
        }
        OnPropertyChanged(nameof(Selected));
        OnPropertyChanged(nameof(Error));
        SelectionChanged?.Invoke();
    }

    public void Select(string? name) => Select(name is null ? null : Sites.FirstOrDefault(site => site.Name == name));
}



/// <summary>A stable site row that keeps drafts while its status DTO reference changes.</summary>
public sealed class SiteRowViewModel : ObservableObject
{
    private Status.Site site;
    private bool isSelected;
    private bool? draftSecured;
    private string? draftPhp;
    private string? draftNode;
    private string? draftServer;
    private string? draftWorker;

    public SiteRowViewModel(Status.Site site)
    {
        this.site = site;
        Domains = new ObservableCollection<DomainRowViewModel>(site.Domains.Select(domain => new DomainRowViewModel(domain)));
    }

    public string Name => site.Name;
    public string Domain => site.Domain;
    public string Path => site.Path;
    public string? Url => site.Url;
    public Status.Site Snapshot => site;
    public ObservableCollection<DomainRowViewModel> Domains { get; }
    public bool IsSelected { get => isSelected; internal set => SetProperty(ref isSelected, value); }
    public bool Secured => draftSecured ?? site.Secured;
    public string Php => draftPhp ?? site.Php;
    public string Node => draftNode ?? Major(site.Node ?? "");
    public string Server => draftServer ?? site.Server;
    public string Worker => draftWorker ?? site.Worker;

    public bool Parked => site.Parked;
    public string PhpSource => site.PhpSource;
    public string PhpPinned => site.PhpPinned;
    public string PhpFile => site.PhpFile;
    public string NodeSource => site.NodeSource;
    public string NodePinned => site.NodePinned;
    public string NodeFile => site.NodeFile;
    public string NodeShadowed => site.NodeShadowed;
    public string? Problem => site.Problem;
    public bool OctaneAvailable => site.OctaneAvailable;
    public Status.SiteFrankenPhp? FrankenPhp => site.FrankenPhp;
    public string Boost => site.Boost;
    public bool IsFrankenPhp => Server == Status.SiteServer.FrankenPhp;
    public bool PhpPinnedHere => site.PhpPinnedHere;
    public bool NodePinnedHere => site.NodePinnedHere;

    public void SetDraftSecured(bool value) => SetDraft(ref draftSecured, value, nameof(Secured));
    public void SetDraftPhp(string value) => SetDraft(ref draftPhp, value, nameof(Php));
    public void SetDraftNode(string value) => SetDraft(ref draftNode, value, nameof(Node));
    public void SetDraftServer(string value) => SetDraft(ref draftServer, value, nameof(Server));
    public void SetDraftWorker(string value) => SetDraft(ref draftWorker, value, nameof(Worker));

    internal void Apply(Status.Site value)
    {
        site = value;
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
            if (row is null) Domains.Insert(index, new DomainRowViewModel(domain));
            else
            {
                row.Apply(domain);
                if (Domains.IndexOf(row) != index)
                {
                    Domains.Remove(row);
                    Domains.Insert(index, row);
                }
            }
        }
        foreach (var property in new[] { nameof(Name), nameof(Domain), nameof(Path), nameof(Url), nameof(Secured), nameof(Php), nameof(Node), nameof(Server), nameof(Worker), nameof(Parked), nameof(PhpSource), nameof(PhpPinned), nameof(PhpFile), nameof(NodeSource), nameof(NodePinned), nameof(NodeFile), nameof(NodeShadowed), nameof(Problem), nameof(OctaneAvailable), nameof(FrankenPhp), nameof(Boost), nameof(IsFrankenPhp), nameof(PhpPinnedHere), nameof(NodePinnedHere) })
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
        if (field != value)
        {
            field = value;
            OnPropertyChanged(property);
        }
    }

    private static string Major(string value) => value.Split('.')[0];
}

/// <summary>A stable domain row whose DTO is replaced only when status accepts a new snapshot.</summary>
public sealed class DomainRowViewModel : ObservableObject
{
    private Status.SiteDomain domain;

    public DomainRowViewModel(Status.SiteDomain domain) => this.domain = domain;

    public string Entry => domain.Entry;
    public string Host => domain.Host;
    public bool Alias => domain.Alias;
    public string? Url => domain.Url;
    public string? Problem => domain.Problem;

    internal void Apply(Status.SiteDomain value)
    {
        domain = value;
        OnPropertyChanged(nameof(Entry));
        OnPropertyChanged(nameof(Host));
        OnPropertyChanged(nameof(Alias));
        OnPropertyChanged(nameof(Url));
        OnPropertyChanged(nameof(Problem));
    }
}