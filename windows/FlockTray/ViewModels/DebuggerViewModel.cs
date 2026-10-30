using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned projections and commands for debugger settings.</summary>
public sealed partial class DebuggerViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService _model;
    private readonly DaemonSwitch _xdebug;
    private readonly DaemonSwitch _breakpoints;

    public DebuggerViewModel(RuntimeService model)
    {
        _model = model;
        _xdebug = new DaemonSwitch(NotifyState);
        _breakpoints = new DaemonSwitch(NotifyState);
        _model.Changed += NotifyState;
        _model.Configuration.Changed += NotifyState;
    }

    /// <summary>The Xdebug switch, bound two-way: setting it asks the daemon, reading it follows the daemon.</summary>
    public bool Enabled
    {
        get => _xdebug.Show(_model.Status?.Xdebug == true);
        set
        {
            if (value != Enabled)
            {
                SetXdebugCommand.Execute(value);
            }
        }
    }

    /// <summary>The PhpStorm breakpoints switch, bound two-way like <see cref="Enabled" />.</summary>
    public bool Breakpoints
    {
        get => _breakpoints.Show(_model.Status?.XdebugBreakpoints == true);
        set
        {
            if (value != Breakpoints)
            {
                SetBreakpointsCommand.Execute(value);
            }
        }
    }

    public Flock.Kit.FlockConfig? Config => _model.Configuration.Current;
    public IReadOnlyList<string> DebuggingSites => Enabled && Breakpoints
        ? (_model.Status?.Sites ?? []).Where(site => site.Debugging).Select(site => site.Domain).ToList()
        : [];
    public IReadOnlyList<PhpAvailability> PhpVersions => (Config?.Php ?? []).Select(php => new PhpAvailability(php.Version, php.XdebugAvailable)).ToList();
    public Visibility DebuggingVisibility => DebuggingSites.Count > 0
        ? Visibility.Visible
        : Visibility.Collapsed;
    [RelayCommand]
    public Task SetXdebugAsync(bool enabled) => _xdebug.ChangeAsync(enabled, _model.SetXdebugAsync);

    [RelayCommand]
    public Task SetBreakpointsAsync(bool enabled) => _breakpoints.ChangeAsync(enabled, _model.SetXdebugBreakpointsAsync);

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
            Label = $"PHP {version}";
            AvailableVisibility = available ? Visibility.Visible : Visibility.Collapsed;
            UnavailableVisibility = available ? Visibility.Collapsed : Visibility.Visible;
        }

        public string Version { get; }
        public string Label { get; }
        public Visibility AvailableVisibility { get; }
        public Visibility UnavailableVisibility { get; }
    }
}
