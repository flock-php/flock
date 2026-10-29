using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned MCP state for the AI settings pane.</summary>
public sealed partial class AiViewModel : ObservableObject, IDisposable
{
    private readonly McpService _mcpService;
    private readonly CollectionSynchronizer<string, McpClientViewModel> rows = new();

    public AiViewModel(McpService mcpService)
    {
        _mcpService = mcpService;
        mcpService.Changed += RefreshRows;
    }

    public ObservableCollection<McpClientViewModel> Clients { get; } = [];
    public string Snippet => _mcpService.Current?.Snippet ?? "";
    public string? Error => _mcpService.Error;
    public string? ActionError => _mcpService.ActionError;
    public Task RefreshAsync(CancellationToken observerToken = default) => _mcpService.RefreshAsync(observerToken);

    public void Dispose() => _mcpService.Changed -= RefreshRows;

    [RelayCommand]
    private Task Refresh() => _mcpService.RefreshAsync();

    private void RefreshRows()
    {
        var incoming = _mcpService.Current?.Clients ?? [];
        rows.Synchronize(Clients, incoming, row => row.Id, value => value.Id,
            value => new McpClientViewModel(_mcpService, value), (row, value) => row.Update(value));
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
