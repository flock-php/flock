using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned projections and commands for debugger settings.</summary>
public sealed partial class DebuggerViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService _model;

    public DebuggerViewModel(RuntimeService model)
    {
        _model = model;
        _model.Changed += NotifyState;
        _model.Configuration.Changed += NotifyState;
    }

    public bool Enabled => _model.Status?.Xdebug == true;
    public bool Breakpoints => _model.Status?.XdebugBreakpoints == true;
    public Flock.Kit.FlockConfig? Config => _model.Configuration.Current;
    public IReadOnlyList<string> DebuggingSites => Enabled && Breakpoints
        ? (_model.Status?.Sites ?? []).Where(site => site.Debugging).Select(site => site.Domain).ToList()
        : [];
    public IReadOnlyList<PhpAvailability> PhpVersions => (Config?.Php ?? []).Select(php => new PhpAvailability(php.Version, php.XdebugAvailable)).ToList();
    public Visibility DebuggingVisibility => DebuggingSites.Count > 0
        ? Visibility.Visible
        : Visibility.Collapsed;
    [RelayCommand]
    public Task SetXdebugAsync(bool enabled) => _model.SetXdebugAsync(enabled);

    [RelayCommand]
    public Task SetBreakpointsAsync(bool enabled) => _model.SetXdebugBreakpointsAsync(enabled);

    public void Dispose()
    {
        _model.Changed -= NotifyState;
        _model.Configuration.Changed -= NotifyState;
    }

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Enabled));
        OnPropertyChanged(nameof(Breakpoints));
        OnPropertyChanged(nameof(Config));
        OnPropertyChanged(nameof(DebuggingSites));
        OnPropertyChanged(nameof(PhpVersions));
        OnPropertyChanged(nameof(DebuggingVisibility));
    }

    public sealed class PhpAvailability
    {
        public PhpAvailability(string version, bool available)
        {
            Version = version;
            AvailableVisibility = available ? Visibility.Visible : Visibility.Collapsed;
            UnavailableVisibility = available ? Visibility.Collapsed : Visibility.Visible;
        }

        public string Version { get; }
        public Visibility AvailableVisibility { get; }
        public Visibility UnavailableVisibility { get; }
    }
}
