using System.Runtime.InteropServices;
using System.Runtime.InteropServices.WindowsRuntime;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage;
using Windows.Storage.Pickers;
using Windows.Storage.Streams;
using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using WinRT.Interop;
namespace Flock.Tray.Settings;

/// <summary>
///     The Share section: the site at a temporary public URL through a Cloudflare quick tunnel, to show
///     work in progress. The daemon builds the assets when they need it, starts the tunnel and ends it
///     after 8 hours; the status follows it through building, starting, running or failed.
/// </summary>
internal sealed partial class SitesPane
{
    // The last QR code decoded, so re-renders (every status poll) don't decode it again.
    private (string Data, BitmapImage Image)? qr;

    private StackPanel Share(Status.Site site)
    {
        var section = Section(Strings.Share_Share);
        var share = site.Share;
        if (share is null)
        {
            section.Children.Add(Ui.Caption(Strings.Share_Explanation));
            var start = Ui.Button(Strings.Share_ShareEllipsis, () => Start(site.Name, () => StartShare(site)));
            start.IsEnabled = site.Url is not null;
            section.Children.Add(start);
            return section;
        }

        var stop = Ui.Button(share.IsRunning ? Strings.Share_StopSharing : share.IsFailed ? Strings.Share_Dismiss : Strings.Share_Stop, () => Start(site.Name, () => Model.Sites.StopShareAsync(site.Name)));
        switch (share.State)
        {
            case Status.ShareState.Building or Status.ShareState.Starting:
                section.Children.Add(Ui.Caption(share.State == Status.ShareState.Building ? Strings.Share_Building : Strings.Share_Starting, wrap: false));
                section.Children.Add(new ProgressBar { IsIndeterminate = true, HorizontalAlignment = HorizontalAlignment.Left, Width = 320 });
                section.Children.Add(stop);
                break;
            case Status.ShareState.Running when share.Url is { } url:
                var link = Ui.Link(url, () => Model.Open(url));
                link.Padding = new Thickness(0);
                var copy = Ui.IconButton(Ui.GlyphCopy, Strings.Common_Copy, () => Ui.Copy(url));
                copy.Padding = new Thickness(4);
                section.Children.Add(Ui.Row(4, link, copy));
                if (share.Qr is { Length: > 0 } data)
                {
                    section.Children.Add(QrImage(data, site.Name));
                }
                if (share.ExpiresAt is { } expires)
                {
                    section.Children.Add(Ui.Caption(Localization.Format(Strings.Share_EndsAt, expires.ToLocalTime()), wrap: false));
                }
                if (share.Allow.Count > 0)
                {
                    section.Children.Add(Ui.Caption(Localization.Format(Strings.Share_OnlyFor, string.Join(", ", share.Allow))));
                }
                if (share.Debug)
                {
                    section.Children.Add(Ui.Caption(Strings.Share_DebugOn));
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
                    var rebuild = Ui.Button(share.Rebuilding ? Strings.Share_Rebuilding : Strings.Share_Rebuild, () => Start(site.Name, () => Model.Sites.RebuildShareAsync(site.Name)),
                        tooltip: Strings.Share_RebuildTooltip);
                    rebuild.IsEnabled = !share.Rebuilding;
                    buttons.Children.Add(rebuild);
                }
                buttons.Children.Add(stop);
                section.Children.Add(buttons);
                break;
            default:
                section.Children.Add(Warning(share.Error ?? Strings.Share_Stopped));
                var allow = share.Allow.ToList();
                var debug = share.Debug;
                section.Children.Add(Ui.Row(8, Ui.Button(Strings.Common_TryAgain, () => Start(site.Name, () => Model.Sites.StartShareAsync(site.Name, allow, debug))), stop));
                break;
        }
        return section;
    }

    /// <summary>
    ///     Asks who may open the shared site (nobody listed: anyone with the link) and whether visitors see
    ///     the app's debug mode, then shares it.
    /// </summary>
    private async Task StartShare(Status.Site site)
    {
        var box = new TextBox { PlaceholderText = Strings.Share_AllowPlaceholder, Header = Strings.Share_AllowHeader };
        var debug = new CheckBox { Content = Strings.Share_ShowErrors };
        var dialog = new ContentDialog
        {
            Title = Localization.Format(Strings.Share_Title, site.Name),
            Content = Ui.Column(12, box,
                Ui.Caption(Strings.Share_AllowExplanation),
                Ui.Column(2, debug, Ui.Caption(Strings.Share_ShowErrorsExplanation))),
            PrimaryButtonText = Strings.Share_Share,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var allow = box.Text.Split([',', ' ', ';', '\t', '\r', '\n'], StringSplitOptions.RemoveEmptyEntries);
        var withDebug = debug.IsChecked == true;
        await Model.Sites.StartShareAsync(site.Name, allow, withDebug);
    }

    /// <summary>
    ///     The share's QR code: the daemon's PNG (8 pixels per module), shown at 160 pixels. Its context
    ///     menu copies or saves it, and it drags out as a .png file.
    /// </summary>
    private Image QrImage(string data, string site)
    {
        var image = new Image { Width = 160, Height = 160, Stretch = Stretch.Uniform, HorizontalAlignment = HorizontalAlignment.Left, CanDrag = true };
        Ui.AutomationName(image, Strings.Share_QrName);
        ToolTipService.SetToolTip(image, Strings.Share_QrTooltip);
        var copy = new MenuFlyoutItem { Text = Strings.Share_CopyImage };
        copy.Click += (_, _) => _ = CopyQr(data);
        var save = new MenuFlyoutItem { Text = Strings.Share_SaveImage };
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
        catch (Exception e) when (e is FormatException or COMException)
        {
            App.Log(e);
        }
    }

    private static string QrFileName(string site)
    {
        return $"{site}-share-qr.png";
    }

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
        catch (Exception e) when (e is FormatException or COMException)
        {
            App.Log(e);
        }
    }

    /// <summary>Asks where to save the share's QR code, then writes the PNG there.</summary>
    private async Task SaveQr(string data, string site)
    {
        var picker = new FileSavePicker { SuggestedFileName = Path.GetFileNameWithoutExtension(QrFileName(site)) };
        picker.FileTypeChoices.Add(Strings.Share_PngImage, [".png"]);
        InitializeWithWindow.Initialize(picker, Window.Hwnd);
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
            await Ui.Inform(Window.Content.XamlRoot, Strings.Share_CouldNotSaveQr, e.Message);
        }
    }
}