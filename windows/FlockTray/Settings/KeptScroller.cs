using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// A scroll area a pane keeps across renders (Services, Sites: the list and the details each scroll
/// on their own), so it stays where the user scrolled it although Build() creates everything else
/// anew.
/// </summary>
internal sealed class KeptScroller
{
    private readonly DispatcherQueue queue;
    private Panel? host;
    private double offset;
    // An offset is about to be put back: the reset to the top that moving causes isn't the user's.
    private bool restoring;

    public KeptScroller(DispatcherQueue queue)
    {
        this.queue = queue;
        View.ViewChanged += (_, e) =>
        {
            if (!e.IsIntermediate && !restoring)
            {
                offset = View.VerticalOffset;
            }
        };
    }

    public ScrollViewer View { get; } = new() { VerticalScrollBarVisibility = ScrollBarVisibility.Auto };

    /// <summary>Starts at the top next time, e.g. for another item's details.</summary>
    public void ToTop() => offset = 0;

    /// <summary>
    /// Shows content in the scroller and moves it from the previous render into host, scrolled where
    /// it was. The previous panel is remembered rather than asked for (Parent): once that render left
    /// the window, Parent is null although the panel still holds the scroller, and adding it
    /// elsewhere would fail. Moving it resets its offset, so that is put back once it is laid out.
    /// </summary>
    public ScrollViewer Place(Panel host, UIElement content)
    {
        View.Content = content;
        this.host?.Children.Remove(View);
        host.Children.Add(View);
        this.host = host;
        var keep = offset;
        if (keep > 0)
        {
            restoring = true;
            queue.TryEnqueue(DispatcherQueuePriority.Low, () =>
            {
                restoring = false;
                View.ChangeView(null, keep, null, disableAnimation: true);
            });
        }
        return View;
    }
}
