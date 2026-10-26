using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned MCP state for the AI settings pane.</summary>
public sealed class AiViewModel : ObservableObject, IDisposable
{
    private readonly McpService service;
    private readonly CollectionSynchronizer<string, McpClientViewModel> rows = new();

    public AiViewModel(McpService service)
    {
        this.service = service;
        service.Changed += RefreshRows;
        RefreshCommand = new AsyncRelayCommand(() => service.RefreshAsync());
    }

    public ObservableCollection<McpClientViewModel> Clients { get; } = [];
    public string Snippet => service.Current?.Snippet ?? "";
    public string? Error => service.Error;
    public string? ActionError => service.ActionError;
    public IAsyncRelayCommand RefreshCommand { get; }

    public Task RefreshAsync(CancellationToken observerToken = default) => service.RefreshAsync(observerToken);

    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        var incoming = service.Current?.Clients ?? [];
        rows.Synchronize(Clients, incoming, row => row.Id, value => value.Id,
            value => new McpClientViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Snippet));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        foreach (var row in Clients)
        {
            row.RegisterCommand.NotifyCanExecuteChanged();
            row.UnregisterCommand.NotifyCanExecuteChanged();
        }
    }
}