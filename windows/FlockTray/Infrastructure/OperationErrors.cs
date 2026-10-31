using CommunityToolkit.Mvvm.ComponentModel;

namespace Flock.Tray.Infrastructure;

/// <summary>Owns action and refresh failures for one resource without sharing a mutable error map.</summary>
public sealed partial class OperationErrors : ObservableObject
{
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(DisplayedMessage))]
    public partial string? Action { get; private set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(DisplayedMessage))]
    public partial string? Refresh { get; private set; }

    /// <summary>The action failure wins over the refresh failure for this resource.</summary>
    public string? DisplayedMessage => Action ?? Refresh;

    /// <summary>Returns true for a direct error value change, excluding the derived display-message notification.</summary>
    internal static bool IsDirectPropertyChange(System.ComponentModel.PropertyChangedEventArgs args) =>
        args.PropertyName is nameof(Action) or nameof(Refresh);

    public void SetAction(Exception error) => Action = error.Message;

    public void SetRefresh(Exception error) => Refresh = error.Message;

    public void ClearAction() => Action = null;

    public void ClearRefresh() => Refresh = null;
}
