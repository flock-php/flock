using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics;

namespace Flock.Tray;

/// <summary>
/// A Flock window: Mica backdrop, the app icon, content drawn into the title bar area, and a size
/// given in device-independent pixels. Subclasses fill Body.
/// </summary>
internal abstract class FlockWindow : Window
{
    private readonly Grid root = new();
    private readonly TextBlock caption;
    private readonly ContentControl body = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };

    protected FlockWindow(string title, int width, int height, int minWidth = 640, int minHeight = 420)
    {
        Title = title;
        Hwnd = Win32Interop.GetWindowFromWindowId(AppWindow.Id);
        AppWindow.SetIcon(System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.ico"));
        SystemBackdrop = new MicaBackdrop();

        ExtendsContentIntoTitleBar = true;
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        var titleBar = new Grid { Height = 48, Padding = new Thickness(16, 0, 0, 0) };
        caption = Ui.Text(title, "CaptionTextBlockStyle", wrap: false);
        titleBar.Children.Add(Ui.Row(12,
            new Image { Source = new Microsoft.UI.Xaml.Media.Imaging.BitmapImage(new Uri(System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.ico"))), Width = 16, Height = 16 },
            caption));
        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        root.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        root.Children.Add(titleBar);
        Grid.SetRow(body, 1);
        root.Children.Add(body);
        Content = root;
        SetTitleBar(titleBar);

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
        get => caption.Text;
        set
        {
            Title = value;
            caption.Text = value;
        }
    }

    /// <summary>Asks before a change; see Ui.Confirm.</summary>
    public Task<bool> Confirm(string question, string details, string action, bool destructive = false, UIElement? extra = null) =>
        Ui.Confirm(Content.XamlRoot, question, details, action, destructive, extra);

    /// <summary>Physical pixels per device-independent pixel on the window's monitor.</summary>
    public double Scale => Math.Max(1, Native.GetDpiForWindow(Hwnd) / 96.0);

    protected UIElement? Body
    {
        get => body.Content as UIElement;
        set => body.Content = value;
    }

    /// <summary>Shows the window and brings it to the front (a tray app is never the foreground app on its own).</summary>
    public void Present()
    {
        Activate();
        Native.SetForegroundWindow(Hwnd);
    }

    private void Center()
    {
        var area = DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Primary).WorkArea;
        var size = AppWindow.Size;
        AppWindow.Move(new PointInt32(area.X + (area.Width - size.Width) / 2, area.Y + (area.Height - size.Height) / 2));
    }
}
