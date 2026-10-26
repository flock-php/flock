using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class TrayServiceTests
{
    [Fact]
    public async Task RealNativeTrayCanStartAndReleaseItsWindow()
    {
        using var tray = new TrayService(new Win32TrayNativeApi());

        await tray.StartAsync(Path.Combine(AppContext.BaseDirectory, "Assets", "Tray.Dark.ico"), "Flock test");

        Assert.NotEqual(IntPtr.Zero, tray.Handle);
        tray.Dispose();
        Assert.Equal(IntPtr.Zero, tray.Handle);
    }

    [Theory]
    [InlineData(Failure.RegisterMessage)]
    [InlineData(Failure.RegisterClass)]
    [InlineData(Failure.CreateWindow)]
    [InlineData(Failure.LoadImage)]
    [InlineData(Failure.Add)]
    public async Task StartupFailureDoesNotLeaveAcquiredNativeResources(Failure failure)
    {
        var native = new FakeTrayNative { Failure = failure };
        var tray = new TrayService(native);

        await Assert.ThrowsAnyAsync<Exception>(() => tray.StartAsync("first.ico", "Flock"));

        Assert.Equal(failure == Failure.Add ? 1 : 0, native.DestroyIconCalls);
        Assert.Equal(failure is Failure.LoadImage or Failure.Add ? 1 : 0, native.DestroyWindowCalls);
        Assert.Equal(failure is Failure.CreateWindow or Failure.LoadImage or Failure.Add ? 1 : 0, native.UnregisterCalls);
        Assert.Equal(0, native.NotifyCalls.Count(operation => operation == TrayNotifyOperation.Delete));
    }

    [Fact]
    public async Task CleanupRunsInReverseOrderAndDisposeIsIdempotent()
    {
        var native = new FakeTrayNative();
        var tray = new TrayService(native);
        await tray.StartAsync("first.ico", "Flock");

        tray.Dispose();
        tray.Dispose();

        Assert.Equal(["notify:Add", "notify:Delete", "destroy-icon", "destroy-window", "unregister"], native.Calls);
    }

    [Fact]
    public async Task ExpectedAlreadyRegisteredClassIsUsableAndNotUnregistered()
    {
        var native = new FakeTrayNative { AlreadyRegistered = true };
        var tray = new TrayService(native);
        await tray.StartAsync("first.ico", "Flock");

        tray.Dispose();

        Assert.Equal(0, native.UnregisterCalls);
        Assert.Equal(1, native.DestroyIconCalls);
        Assert.Equal(1, native.DestroyWindowCalls);
    }

    [Fact]
    public async Task FailedUpdateRetainsWorkingIconAndDestroysReplacementOnce()
    {
        var native = new FakeTrayNative();
        var tray = new TrayService(native);
        await tray.StartAsync("first.ico", "Flock");
        native.FailModify = true;

        tray.Update("second.ico", "Changed");

        Assert.Equal(2, native.LoadImageCalls);
        Assert.Equal(1, native.DestroyIconCalls);
        Assert.Equal((IntPtr)22, native.ModifyIcons[0]);
        native.FailModify = false;
        tray.Update("second.ico", "Changed");
        Assert.Equal(2, native.DestroyIconCalls);
    }

    [Fact]
    public async Task TaskbarReAddFailureIsContainedAndReported()
    {
        var native = new FakeTrayNative();
        var failures = new List<string>();
        var tray = new TrayService(native);
        tray.Failure += failures.Add;
        await tray.StartAsync("first.ico", "Flock");
        native.FailReAdd = true;

        native.Emit(new TrayNativeMessage(native.Window, native.TaskbarMessage, IntPtr.Zero, IntPtr.Zero, null, null));

        Assert.Single(failures);
        Assert.Contains("re-add", failures[0]);
    }

    [Fact]
    public async Task NativeCallbackExceptionsDoNotCrossTheCallbackBoundary()
    {
        var native = new FakeTrayNative();
        var failures = new List<string>();
        var tray = new TrayService(native);
        tray.Failure += failures.Add;
        tray.Clicked += () => throw new InvalidOperationException("callback failure");
        await tray.StartAsync("first.ico", "Flock");

        native.Emit(new TrayNativeMessage(native.Window, 0x8001, IntPtr.Zero, (IntPtr)0x0202, null, null));

        Assert.Single(failures);
        Assert.Contains("callback", failures[0]);
    }

    [Fact]
    public async Task StartupPreservesOriginalFailureWhenCleanupFails()
    {
        var native = new FakeTrayNative { Failure = Failure.Add, ThrowOnDestroyIcon = true };
        var tray = new TrayService(native);

        var error = await Assert.ThrowsAsync<InvalidOperationException>(() => tray.StartAsync("first.ico", "Flock"));

        Assert.Contains("NIM_ADD", error.Message);
    }

    public enum Failure
    {
        None,
        RegisterMessage,
        RegisterClass,
        CreateWindow,
        LoadImage,
        Add,
    }

    private sealed class FakeTrayNative : ITrayNativeApi
    {
        public Failure Failure { get; set; }
        public bool AlreadyRegistered { get; set; }
        public bool FailModify { get; set; }
        public bool FailReAdd { get; set; }
        public bool ThrowOnDestroyIcon { get; set; }
        public int TaskbarMessage { get; } = 42;
        public IntPtr Window { get; private set; }
        public int DestroyIconCalls { get; private set; }
        public int DestroyWindowCalls { get; private set; }
        public int UnregisterCalls { get; private set; }
        public int LoadImageCalls { get; private set; }
        public List<TrayNotifyOperation> NotifyCalls { get; } = [];
        public List<IntPtr> ModifyIcons { get; } = [];
        public List<string> Calls { get; } = [];
        private Func<TrayNativeMessage, IntPtr?>? callback;

        public IntPtr ModuleHandle() => (IntPtr)7;

        public int RegisterWindowMessage(string name) => Failure == Failure.RegisterMessage ? 0 : TaskbarMessage;

        public ushort RegisterClass(string name, Func<TrayNativeMessage, IntPtr?> callback, out bool alreadyRegistered, out int lastError)
        {
            this.callback = callback;
            alreadyRegistered = AlreadyRegistered;
            lastError = AlreadyRegistered || Failure == Failure.RegisterClass ? 1410 : 0;
            return Failure == Failure.RegisterClass || AlreadyRegistered ? (ushort)0 : (ushort)1;
        }

        public IntPtr CreateWindow(string className, IntPtr instance, out int lastError)
        {
            lastError = Failure == Failure.CreateWindow ? 5 : 0;
            Window = Failure == Failure.CreateWindow ? IntPtr.Zero : (IntPtr)11;
            return Window;
        }

        public IntPtr LoadImage(string path, IntPtr window, out int lastError)
        {
            lastError = Failure == Failure.LoadImage ? 2 : 0;
            LoadImageCalls++;
            return Failure == Failure.LoadImage ? IntPtr.Zero : (IntPtr)(LoadImageCalls + 20);
        }

        public bool Notify(TrayNotifyOperation operation, TrayNotification notification)
        {
            NotifyCalls.Add(operation);
            Calls.Add($"notify:{operation}");
            if (operation == TrayNotifyOperation.Modify)
            {
                ModifyIcons.Add(notification.Icon);
                return !FailModify;
            }
            if (operation == TrayNotifyOperation.Add && Failure == Failure.Add)
            {
                return false;
            }
            if (operation == TrayNotifyOperation.Add && FailReAdd && NotifyCalls.Count > 1)
            {
                return false;
            }
            return true;
        }

        public bool DestroyIcon(IntPtr icon)
        {
            DestroyIconCalls++;
            Calls.Add("destroy-icon");
            if (ThrowOnDestroyIcon)
            {
                throw new InvalidOperationException("cleanup failed");
            }
            return true;
        }

        public bool DestroyWindow(IntPtr window)
        {
            DestroyWindowCalls++;
            Calls.Add("destroy-window");
            return true;
        }

        public bool UnregisterClass(string name, IntPtr instance)
        {
            UnregisterCalls++;
            Calls.Add("unregister");
            return true;
        }

        public IntPtr CreatePopupMenu() => (IntPtr)1;
        public bool AppendMenu(IntPtr menu, bool separator, IntPtr id, string? text) => true;
        public bool SetMenuDefaultItem(IntPtr menu, int item) => true;
        public int TrackPopupMenu(IntPtr menu, IntPtr window, TrayPoint cursor) => 0;
        public bool DestroyMenu(IntPtr menu) => true;
        public bool SetForegroundWindow(IntPtr window) => true;
        public TrayPoint CursorPosition() => new(0, 0);
        public bool PostMessage(IntPtr window, int message) => true;

        public void Emit(TrayNativeMessage message) => callback!(message);
    }
}
