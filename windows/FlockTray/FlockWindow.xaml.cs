using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.Graphics;

namespace Flock.Tray;

/// <summary>
/// A Flock window: Mica backdrop, the app icon, content drawn into the title bar area, and a size
/// given in device-independent pixels. Subclasses fill Body.
/// </summary>
internal abstract partial class FlockWindow : Window
{
    private TextBlock? activeCaption;

    protected FlockWindow(string title, int width, int height, int minWidth = 640, int minHeight = 420)
    {
        // Derived windows own the observable XAML root. Initializing this base markup would create
        // a tree that WinUI replaces when the derived compiled-XAML view is loaded.
        Title = DisplayTitle(title);
        Hwnd = Win32Interop.GetWindowFromWindowId(AppWindow.Id);
        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.ico"));
        SystemBackdrop = new MicaBackdrop();

        var scale = Scale;
        AppWindow.Resize(new SizeInt32((int)(width * scale), (int)(height * scale)));
        if (AppWindow.Presenter is OverlappedPresenter presenter)
        {
            presenter.PreferredMinimumWidth = (int)(minWidth * scale);
            presenter.PreferredMinimumHeight = (int)(minHeight * scale);
        }
        Center();
    }

    public IntPtr Hwnd { get; }

    /// <summary>
    /// The title in the drawn title bar and in the taskbar ("Dumps (3)"). Window.Title alone only
    /// reaches the taskbar, because the content extends into the title bar.
    /// </summary>
    public string Heading
    {
        get => activeCaption?.Text ?? Title;
        set
        {
            Title = DisplayTitle(value);
            if (activeCaption is not null)
            {
                activeCaption.Text = Title;
            }
        }
    }

    /// <summary>Connects a derived compiled-XAML window to the shared title-bar behavior.</summary>
    protected void InitializeChrome(Grid root, Grid titleBar, TextBlock caption, Image icon)
    {
        activeCaption = caption;
        activeCaption.Text = Title;
        icon.Source = new BitmapImage(new Uri(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.png")));
        if (App.Current.Options.UseSolidBackground)
        {
            root.Background = Ui.Brush("SolidBackgroundFillColorBaseBrush");
        }
        ExtendsContentIntoTitleBar = true;
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        SetTitleBar(titleBar);
    }

    private static string DisplayTitle(string title) => App.Current.Options.Identity.IsDevelopment ? title + " [Dev]" : title;

    /// <summary>Asks before a change; see Ui.Confirm.</summary>
    public Task<bool> Confirm(string question, string details, string action, bool destructive = false, UIElement? extra = null) =>
        Ui.Confirm(Content.XamlRoot, question, details, action, destructive, extra);

    /// <summary>Physical pixels per device-independent pixel on the window's monitor.</summary>
    public double Scale => Math.Max(1, Native.GetDpiForWindow(Hwnd) / 96.0);

    /// <summary>Shows the window and brings it to the front (a tray app is never the foreground app on its own).</summary>
    public void Present()
    {
        Activate();
        Native.SetForegroundWindow(Hwnd);
    }

    /// <summary>Releases window-owned async resources before the native window is destroyed.</summary>
    public virtual Task ReleaseAsync() => Task.CompletedTask;

    private void Center()
    {
        var area = DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Primary).WorkArea;
        var size = AppWindow.Size;
        AppWindow.Move(new PointInt32(area.X + (area.Width - size.Width) / 2, area.Y + (area.Height - size.Height) / 2));
    }
}
