using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Web.WebView2.Core;

namespace Flock.Tray;

/// <summary>
/// Renders an HTML string in WebView2. Mail HTML runs with JavaScript off and remote loads blocked
/// (no tracking pixels, no surprises); dumps need JavaScript for Symfony's collapsible output.
/// Links open in the default browser (or editor, for dump sources) instead of inside the view.
/// </summary>
internal sealed class HtmlView : Grid
{
    // NavigateToString is limited to 2 MB; larger pages are written to a file first.
    private const int MaxInlineLength = 1_500_000;

    private readonly WebView2 web = new();
    private readonly bool allowScripts;
    private readonly bool blockRemote;
    private string? loaded;
    private string? pending;
    private bool ready;
    private string? pageFile;

    public HtmlView(bool allowScripts = false, bool blockRemote = true, bool whiteBackground = false)
    {
        this.allowScripts = allowScripts;
        this.blockRemote = blockRemote;
        web.DefaultBackgroundColor = whiteBackground ? Colors.White : Colors.Transparent;
        Children.Add(web);
        _ = InitializeAsync();
    }

    public void Load(string html)
    {
        if (html == loaded)
        {
            return;
        }
        loaded = html;
        if (ready)
        {
            Show(html);
        }
        else
        {
            pending = html;
        }
    }

    private async Task InitializeAsync()
    {
        try
        {
            await web.EnsureCoreWebView2Async();
        }
        catch (Exception e)
        {
            Children.Clear();
            Children.Add(Ui.EmptyState(Ui.GlyphWarning, "Can't show HTML",
                $"The Microsoft Edge WebView2 Runtime is missing or failed to start ({e.Message}). Install it from microsoft.com/edge/webview2."));
            return;
        }

        var core = web.CoreWebView2;
        core.Settings.IsScriptEnabled = allowScripts;
        core.Settings.AreDevToolsEnabled = false;
        core.Settings.IsStatusBarEnabled = false;
        core.Settings.IsZoomControlEnabled = true;

        if (blockRemote)
        {
            core.AddWebResourceRequestedFilter("*", CoreWebView2WebResourceContext.All);
            core.WebResourceRequested += (_, e) =>
            {
                if (e.Request.Uri.StartsWith("http:", StringComparison.OrdinalIgnoreCase) || e.Request.Uri.StartsWith("https:", StringComparison.OrdinalIgnoreCase))
                {
                    e.Response = core.Environment.CreateWebResourceResponse(null, 403, "Blocked by Flock", "");
                }
            };
        }

        core.NavigationStarting += (_, e) =>
        {
            if (!IsOwnPage(e.Uri))
            {
                e.Cancel = true;
                Native.Open(e.Uri);
            }
        };
        core.NewWindowRequested += (_, e) =>
        {
            e.Handled = true;
            Native.Open(e.Uri);
        };

        ready = true;
        if (pending is not null)
        {
            Show(pending);
            pending = null;
        }
    }

    private bool IsOwnPage(string uri) =>
        uri.StartsWith("data:", StringComparison.OrdinalIgnoreCase) ||
        uri.StartsWith("about:", StringComparison.OrdinalIgnoreCase) ||
        (pageFile is not null && uri.Equals(new Uri(pageFile).AbsoluteUri, StringComparison.OrdinalIgnoreCase));

    private void Show(string html)
    {
        if (html.Length <= MaxInlineLength)
        {
            web.NavigateToString(html);
            return;
        }
        // A new file per page: navigating to the same URI again would not reload it.
        var dir = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "Flock");
        Directory.CreateDirectory(dir);
        var previous = pageFile;
        pageFile = System.IO.Path.Combine(dir, $"page-{Guid.NewGuid():N}.html");
        File.WriteAllText(pageFile, html);
        web.Source = new Uri(pageFile);
        if (previous is not null)
        {
            try
            {
                File.Delete(previous);
            }
            catch (IOException)
            {
            }
        }
    }
}
