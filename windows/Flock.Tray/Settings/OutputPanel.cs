using Flock.Tray.Features.Sites;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     The output of a server Flock runs (a Node.js site's dev server, a proxy's command): the last
///     lines of its log, refreshed every second while the server runs, with Clear and Open Log. The
///     Sites pane keeps one and moves it into each rebuilt detail, so the text and scroll position
///     survive the status updates.
/// </summary>
internal sealed class OutputPanel
{
    private readonly SitesService sites;
    private readonly TextBlock text = Ui.MonoText("", 12);
    private readonly ScrollViewer scroller;
    private string? log;
    private bool live;
    private CancellationTokenSource? polling;

    public OutputPanel(SitesService sites)
    {
        this.sites = sites;
        text.TextWrapping = TextWrapping.Wrap;
        scroller = new ScrollViewer
        {
            Content = text,
            Height = 220,
            Padding = new Thickness(10, 8, 10, 8),
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        var clear = Ui.Button(Strings.Logs_Clear, () => _ = ClearAsync());
        var open = Ui.Button(Strings.Sites_OpenLog, () =>
        {
            if (log is not null)
            {
                App.Current.OpenLog(new LogsViewModel.ServiceSource(log));
            }
        });
        Root = Ui.Column(6,
            Ui.Spread(Ui.Text(Strings.Sites_Output, "BodyStrongTextBlockStyle"), clear, open),
            new Border
            {
                Child = scroller,
                Background = Ui.Brush("ControlFillColorDefaultBrush"),
                BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
                BorderThickness = new Thickness(1),
                CornerRadius = new CornerRadius(4),
            });
    }

    public StackPanel Root { get; }

    // The panel Root was put in last. Root.Parent can't be relied on to find it, and WinUI refuses
    // (0x800F1000) an element that is still another panel's child.
    private Panel? owner;

    /// <summary>
    ///     Moves the panel for logName into the panel into (out of the detail it was in), and reads
    ///     the log now, and every second while live (the server starts or runs).
    /// </summary>
    public void ShowIn(Panel into, string logName, bool isLive)
    {
        owner?.Children.Remove(Root);
        into.Children.Add(Root);
        owner = into;
        var changed = logName != log;
        log = logName;
        if (changed)
        {
            text.Text = "";
        }
        if (changed || isLive != live || polling is null)
        {
            live = isLive;
            Restart();
        }
    }

    /// <summary>Stops reading, while the pane is hidden or shows something without output.</summary>
    public void Stop()
    {
        polling?.Cancel();
        polling = null;
    }

    private void Restart()
    {
        Stop();
        polling = new CancellationTokenSource();
        _ = PollAsync(polling.Token);
    }

    private async Task PollAsync(CancellationToken token)
    {
        try
        {
            do
            {
                await ReadAsync(token);
                if (!live)
                {
                    return;
                }
                await Task.Delay(TimeSpan.FromSeconds(1), token);
            } while (!token.IsCancellationRequested);
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private async Task ReadAsync(CancellationToken token)
    {
        if (log is not { } name)
        {
            return;
        }
        var output = await sites.ServiceLogAsync(name, token);
        if (token.IsCancellationRequested || name != log)
        {
            return;
        }
        var trimmed = (output ?? "").TrimEnd();
        var shown = trimmed.Length == 0 ? Strings.Sites_NoOutputYet : trimmed;
        if (shown == text.Text)
        {
            return;
        }
        // Follow the output while it's scrolled to the end, like a terminal.
        var atEnd = scroller.VerticalOffset >= scroller.ScrollableHeight - 4;
        text.Text = shown;
        if (atEnd)
        {
            scroller.UpdateLayout();
            scroller.ChangeView(null, scroller.ScrollableHeight, null, true);
        }
    }

    private async Task ClearAsync()
    {
        // Only for the log still shown: another may have been selected while it was cleared.
        if (log is { } name && await sites.ClearServiceLogAsync(name) && name == log)
        {
            text.Text = Strings.Sites_NoOutputYet;
        }
    }
}
