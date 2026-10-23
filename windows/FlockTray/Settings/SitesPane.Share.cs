using System.Runtime.InteropServices.WindowsRuntime;
using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage;
using Windows.Storage.Pickers;
using Windows.Storage.Streams;

namespace Flock.Tray;

/// <summary>
/// The Share section: the site at a temporary public URL through a Cloudflare quick tunnel, to show
/// work in progress. The daemon builds the assets when they need it, starts the tunnel and ends it
/// after 8 hours; the status follows it through building, starting, running or failed.
/// </summary>
internal sealed partial class SitesPane
{
    // The last QR code decoded, so re-renders (every status poll) don't decode it again.
    private (string Data, BitmapImage Image)? qr;

    private StackPanel Share(Status.Site site)
    {
        var section = Section("Share");
        var share = site.Share;
        if (share is null)
        {
            section.Children.Add(Ui.Caption("Show the site at a temporary public address to preview work in progress, never for production. Visitors don't get debug pages, dumps or the dev tools."));
            var start = Ui.Button("Share…", () => _ = StartShare(site));
            start.IsEnabled = site.Url is not null;
            section.Children.Add(start);
            return section;
        }

        var stop = Ui.Button(share.IsRunning ? "Stop sharing" : share.IsFailed ? "Dismiss" : "Stop", () => _ = Perform(c => c.StopShareAsync(site.Name)));
        switch (share.State)
        {
            case Status.ShareState.Building or Status.ShareState.Starting:
                section.Children.Add(Ui.Caption(share.State == Status.ShareState.Building ? "Building assets…" : "Starting the tunnel…", wrap: false));
                section.Children.Add(new ProgressBar { IsIndeterminate = true, HorizontalAlignment = HorizontalAlignment.Left, Width = 320 });
                section.Children.Add(stop);
                break;
            case Status.ShareState.Running when share.Url is { } url:
                var link = Ui.Link(url, () => Native.Open(url));
                link.Padding = new Thickness(0);
                var copy = Ui.IconButton(Ui.GlyphCopy, "Copy", () => Ui.Copy(url));
                copy.Padding = new Thickness(4);
                section.Children.Add(Ui.Row(4, link, copy));
                if (share.Qr is { Length: > 0 } data)
                {
                    section.Children.Add(QrImage(data, site.Name));
                }
                if (share.ExpiresAt is { } expires)
                {
                    section.Children.Add(Ui.Caption($"Ends at {expires.ToLocalTime():t}", wrap: false));
                }
                if (share.Allow.Count > 0)
                {
                    section.Children.Add(Ui.Caption($"Only for: {string.Join(", ", share.Allow)}"));
                }
                if (share.Debug)
                {
                    section.Children.Add(Ui.Caption("Debug mode is on: visitors see stack traces"));
                }
                if (share.Warning is { } warning)
                {
                    section.Children.Add(Warning(warning));
                }
                if (share.RebuildError is { } rebuildError)
                {
                    section.Children.Add(Warning(rebuildError));
                }
                var buttons = Ui.Row(8);
                if (share.CanRebuild)
                {
                    var rebuild = Ui.Button(share.Rebuilding ? "Rebuilding…" : "Rebuild assets", () => _ = Perform(c => c.RebuildShareAsync(site.Name)),
                        tooltip: "Build the assets again (npm run build), so visitors see your latest changes when they reload");
                    rebuild.IsEnabled = !share.Rebuilding;
                    buttons.Children.Add(rebuild);
                }
                buttons.Children.Add(stop);
                section.Children.Add(buttons);
                break;
            default:
                section.Children.Add(Warning(share.Error ?? "The share stopped."));
                var allow = share.Allow.ToList();
                var debug = share.Debug;
                section.Children.Add(Ui.Row(8, Ui.Button("Try again", () => _ = Perform(c => c.StartShareAsync(site.Name, allow, debug))), stop));
                break;
        }
        return section;
    }

    /// <summary>
    /// Asks who may open the shared site (nobody listed: anyone with the link) and whether visitors see
    /// the app's debug mode, then shares it.
    /// </summary>
    private async Task StartShare(Status.Site site)
    {
        var box = new TextBox { PlaceholderText = "colleague@acme.com, *@client.com", Header = "Only these people (optional)" };
        var debug = new CheckBox { Content = "Show error details to visitors" };
        var dialog = new ContentDialog
        {
            Title = $"Share {site.Name}",
            Content = Ui.Column(12, box, Ui.Caption("Anyone with the link can open the site unless you list email addresses — Cloudflare then emails them a code. The link stops working after 8 hours or when you stop sharing."),
                Ui.Column(2, debug, Ui.Caption("Turns the app's debug mode on for this share, so visitors see stack traces. Only for people you trust."))),
            PrimaryButtonText = "Share",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var allow = box.Text.Split([',', ' ', ';', '\t', '\r', '\n'], StringSplitOptions.RemoveEmptyEntries);
        var withDebug = debug.IsChecked == true;
        await Perform(c => c.StartShareAsync(site.Name, allow, withDebug));
    }

    /// <summary>
    /// The share's QR code: the daemon's PNG (8 pixels per module), shown at 160 pixels. Its context
    /// menu copies or saves it, and it drags out as a .png file.
    /// </summary>
    private Image QrImage(string data, string site)
    {
        var image = new Image { Width = 160, Height = 160, Stretch = Microsoft.UI.Xaml.Media.Stretch.Uniform, HorizontalAlignment = HorizontalAlignment.Left, CanDrag = true };
        Ui.AutomationName(image, "QR code of the share's address");
        ToolTipService.SetToolTip(image, "Right-click to copy or save the QR code, or drag it out");
        var copy = new MenuFlyoutItem { Text = "Copy image" };
        copy.Click += (_, _) => _ = CopyQr(data);
        var save = new MenuFlyoutItem { Text = "Save image…" };
        save.Click += (_, _) => _ = SaveQr(data, site);
        image.ContextFlyout = new MenuFlyout { Items = { copy, save } };
        image.DragStarting += async (_, e) =>
        {
            var deferral = e.GetDeferral();
            try
            {
                var path = Path.Combine(Path.GetTempPath(), QrFileName(site));
                await File.WriteAllBytesAsync(path, Convert.FromBase64String(data));
                e.Data.SetStorageItems([await StorageFile.GetFileFromPathAsync(path)]);
                e.Data.RequestedOperation = DataPackageOperation.Copy;
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or FormatException)
            {
                App.Log(ex);
                e.Cancel = true;
            }
            finally
            {
                deferral.Complete();
            }
        };
        if (qr is { } cached && cached.Data == data)
        {
            image.Source = cached.Image;
            return image;
        }
        _ = LoadQr(image, data);
        return image;
    }

    private async Task LoadQr(Image image, string data)
    {
        try
        {
            var bitmap = new BitmapImage();
            using var stream = new InMemoryRandomAccessStream();
            await stream.WriteAsync(Convert.FromBase64String(data).AsBuffer());
            stream.Seek(0);
            await bitmap.SetSourceAsync(stream);
            qr = (data, bitmap);
            image.Source = bitmap;
        }
        catch (Exception e) when (e is FormatException or System.Runtime.InteropServices.COMException)
        {
            App.Log(e);
        }
    }

    private static string QrFileName(string site) => $"{site}-share-qr.png";

    /// <summary>Copies the share's QR code to the clipboard as an image.</summary>
    private static async Task CopyQr(string data)
    {
        try
        {
            // The clipboard reads the stream when an app pastes, so it stays open.
            var stream = new InMemoryRandomAccessStream();
            await stream.WriteAsync(Convert.FromBase64String(data).AsBuffer());
            stream.Seek(0);
            var package = new DataPackage();
            package.SetBitmap(RandomAccessStreamReference.CreateFromStream(stream));
            Clipboard.SetContent(package);
        }
        catch (Exception e) when (e is FormatException or System.Runtime.InteropServices.COMException)
        {
            App.Log(e);
        }
    }

    /// <summary>Asks where to save the share's QR code, then writes the PNG there.</summary>
    private async Task SaveQr(string data, string site)
    {
        var picker = new FileSavePicker { SuggestedFileName = Path.GetFileNameWithoutExtension(QrFileName(site)) };
        picker.FileTypeChoices.Add("PNG image", [".png"]);
        WinRT.Interop.InitializeWithWindow.Initialize(picker, Window.Hwnd);
        var file = await picker.PickSaveFileAsync();
        if (file is null)
        {
            return;
        }
        try
        {
            await File.WriteAllBytesAsync(file.Path, Convert.FromBase64String(data));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or FormatException)
        {
            await Ui.Inform(Window.Content.XamlRoot, "Could not save the QR code", e.Message);
        }
    }
}
