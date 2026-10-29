namespace Flock.Tray.Infrastructure;

/// <summary>Production adapter for the raw notification-area Win32 calls.</summary>
public sealed class Win32TrayNativeApi : ITrayNativeApi
{
    public IntPtr ModuleHandle() => Native.ModuleHandle();

    public int RegisterWindowMessage(string name) => Native.RegisterTrayWindowMessage(name);

    public ushort RegisterClass(string name, Func<TrayNativeMessage, IntPtr?> callback, out bool alreadyRegistered, out int lastError) =>
        Native.RegisterTrayClass(name, callback, out alreadyRegistered, out lastError);

    public IntPtr CreateWindow(string className, IntPtr instance, out int lastError) => Native.CreateTrayWindow(className, instance, out lastError);

    public IntPtr LoadImage(string path, IntPtr window, out int lastError) => Native.LoadTrayIcon(path, window, out lastError);

    public bool Notify(TrayNotifyOperation operation, TrayNotification notification) => Native.NotifyTray(operation, notification);

    public bool DestroyIcon(IntPtr icon) => Native.DestroyTrayIcon(icon);

    public bool DestroyWindow(IntPtr window) => Native.DestroyTrayWindow(window);

    public bool UnregisterClass(string name, IntPtr instance) => Native.UnregisterTrayClass(name, instance);

    public IntPtr CreatePopupMenu() => Native.CreateTrayPopupMenu();

    public bool AppendMenu(IntPtr menu, bool separator, IntPtr id, string? text) => Native.AppendTrayMenu(menu, separator, id, text);

    public bool SetMenuDefaultItem(IntPtr menu, int item) => Native.SetTrayMenuDefaultItem(menu, item);

    public int TrackPopupMenu(IntPtr menu, IntPtr window, TrayPoint cursor) => Native.TrackTrayPopupMenu(menu, window, new Native.POINT { X = cursor.X, Y = cursor.Y });

    public bool DestroyMenu(IntPtr menu) => Native.DestroyTrayMenu(menu);

    public bool SetForegroundWindow(IntPtr window) => Native.SetTrayForegroundWindow(window);

    public TrayPoint CursorPosition()
    {
        var point = Native.CursorPosition();
        return new TrayPoint(point.X, point.Y);
    }

    public bool PostMessage(IntPtr window, int message) => Native.PostTrayMessage(window, message);
}
