using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned projections and commands for debugger settings.</summary>
public sealed class DebuggerViewModel : ObservableObject
{
    private readonly RuntimeService model;

    public DebuggerViewModel(RuntimeService model)
    {
        this.model = model;
        SetXdebugCommand = new AsyncRelayCommand<bool>(SetXdebugAsync);
        SetBreakpointsCommand = new AsyncRelayCommand<bool>(SetBreakpointsAsync);
    }

    public bool Enabled => model.Status?.Xdebug == true;
    public bool Breakpoints => model.Status?.XdebugBreakpoints == true;
    public Flock.Kit.FlockConfig? Config => model.Configuration.Current;
    public IAsyncRelayCommand<bool> SetXdebugCommand { get; }
    public IAsyncRelayCommand<bool> SetBreakpointsCommand { get; }

    public Task SetXdebugAsync(bool enabled) => model.SetXdebugAsync(enabled);
    public Task SetBreakpointsAsync(bool enabled) => model.SetXdebugBreakpointsAsync(enabled);
}