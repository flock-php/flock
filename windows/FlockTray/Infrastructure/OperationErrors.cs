using CommunityToolkit.Mvvm.ComponentModel;

namespace Flock.Tray.Infrastructure;

/// <summary>Owns action and refresh failures for one resource without sharing a mutable error map.</summary>
public sealed class OperationErrors : ObservableObject
{
    private string? action;
    private string? refresh;

    public string? Action
    {
        get => action;
        private set => SetProperty(ref action, value);
    }

    public string? Refresh
    {
        get => refresh;
        private set => SetProperty(ref refresh, value);
    }

    /// <summary>The action failure wins over the refresh failure for this resource.</summary>
    public string? DisplayedMessage => Action ?? Refresh;

    public void SetAction(Exception error) => Action = error.Message;

    public void SetRefresh(Exception error) => Refresh = error.Message;

    public void ClearAction() => Action = null;

    public void ClearRefresh() => Refresh = null;
}