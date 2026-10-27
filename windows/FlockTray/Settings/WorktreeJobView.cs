using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Follows a worktree's job in a dialog: creating it, setting it up again or switching its
/// database, then its setup step by step with its log. The dialog never closes by itself: Hide
/// leaves the job running (a failure is then notified), and once it's done it offers Open in
/// Browser, or Run Setup Again after a failure.
/// </summary>
internal sealed class WorktreeJobView
{
    private enum Phase { Starting, Running, Ready, Failed }

    private readonly ContentDialog dialog;
    private readonly RuntimeService model;
    private readonly StackPanel headline = Ui.Row(6);
    private readonly TextBlock log = new() { FontFamily = Ui.Mono, FontSize = 12, IsTextSelectionEnabled = true, Margin = new Thickness(8) };
    private readonly ScrollViewer logScroller;
    private readonly CancellationTokenSource lifetime = new();
    private Phase phase = Phase.Starting;
    private string? path;
    private string step = "";
    private string error = "";

    /// <summary>Takes the dialog over (a new one, or the New Worktree dialog once Create is clicked).</summary>
    public WorktreeJobView(ContentDialog dialog, RuntimeService model, string title)
    {
        this.dialog = dialog;
        this.model = model;
        logScroller = new ScrollViewer
        {
            Content = log,
            Height = 260,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        var box = new Border
        {
            Child = logScroller,
            Width = 560,
            CornerRadius = new CornerRadius(6),
            BorderThickness = new Thickness(1),
            BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
            Background = Ui.Brush("CardBackgroundFillColorDefaultBrush"),
        };
        dialog.Title = title;
        dialog.Content = Ui.Column(12, headline, box);
        dialog.Resources["ContentDialogMaxWidth"] = 660.0;
        dialog.SecondaryButtonText = "";
        dialog.PrimaryButtonClick += PrimaryClicked;
        dialog.Closed += (_, _) =>
        {
            lifetime.Cancel();
            if (path is not null)
            {
                App.Current.WorktreeNotifier.Unfollow(path);
            }
        };
        Show();
    }

    /// <summary>The worktree, once its setup is done (Close then shows it).</summary>
    public Status.Site? Ready => phase == Phase.Ready ? Worktree : null;

    private Status.Site? Worktree => path is null ? null
        : model.Status?.Sites.FirstOrDefault(s => s.IsWorktree && string.Equals(s.Path, path, StringComparison.OrdinalIgnoreCase));

    /// <summary>
    /// Starts the job (its folder, or why it couldn't start) and follows the setup until it's ready
    /// or fails, or the dialog closes. Hiding the dialog doesn't stop the job.
    /// </summary>
    public async Task RunAsync(Func<CancellationToken, Task<(string? Path, string? Error)>> start)
    {
        phase = Phase.Starting;
        log.Text = "";
        Show();
        var (folder, failure) = await start(CancellationToken.None);
        if (failure is not null || folder is null)
        {
            path ??= folder;
            error = failure ?? "Flock didn't say where the worktree is.";
            phase = Phase.Failed;
            Show();
            return;
        }
        path = folder;
        if (lifetime.IsCancellationRequested)
        {
            // Hidden while it started: the notifier tells of a failure.
            return;
        }
        App.Current.WorktreeNotifier.Follow(folder);
        step = "";
        phase = Phase.Running;
        Show();

        var token = lifetime.Token;
        while (!token.IsCancellationRequested)
        {
            try
            {
                var setup = await model.Client.WorktreeSetupAsync(folder, token);
                if (setup.Log.Length > 0)
                {
                    try
                    {
                        ShowLog(await model.Client.ServiceLogAsync(setup.Log, 400, token));
                    }
                    catch (FlockException)
                    {
                        // Not written yet.
                    }
                }
                if (setup.State == Status.WorktreeState.Ready)
                {
                    // The status gives its address.
                    await model.RefreshAsync();
                    phase = Phase.Ready;
                    Show();
                    return;
                }
                if (setup.IsFailed)
                {
                    error = "Setup failed: " + (setup.Error.Length > 0 ? setup.Error : "see the log");
                    phase = Phase.Failed;
                    Show();
                    return;
                }
                step = setup.Step;
                Show();
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested)
            {
                return;
            }
            catch (FlockException)
            {
                // Not known yet (a moment after creating it), or the daemon restarts: ask again.
            }
            try
            {
                await Task.Delay(TimeSpan.FromMilliseconds(500), token);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }

    private void ShowLog(string text)
    {
        if (text == log.Text)
        {
            return;
        }
        log.Text = text;
        logScroller.UpdateLayout();
        logScroller.ChangeView(null, logScroller.ScrollableHeight, null, disableAnimation: true);
    }

    /// <summary>The headline and the buttons for the phase.</summary>
    private void Show()
    {
        headline.Children.Clear();
        switch (phase)
        {
            case Phase.Starting or Phase.Running:
                var text = phase == Phase.Starting ? "Starting…"
                    : step.Length > 0 ? char.ToUpperInvariant(step[0]) + step[1..] + "…"
                    : "Setting up…";
                headline.Children.Add(new ProgressRing { Width = 16, Height = 16 });
                headline.Children.Add(Ui.Text(text, wrap: false));
                dialog.PrimaryButtonText = "";
                dialog.CloseButtonText = "Hide";
                dialog.DefaultButton = ContentDialogButton.None;
                break;
            case Phase.Ready:
                var url = Worktree?.Url;
                headline.Children.Add(Ui.Icon(Ui.GlyphCheck, 14, Ui.Success));
                headline.Children.Add(Ui.Text(url is null ? "Ready" : $"Ready at {url}", foreground: Ui.Success, wrap: false, selectable: true));
                dialog.PrimaryButtonText = url is null ? "" : "Open in Browser";
                dialog.CloseButtonText = "Close";
                dialog.DefaultButton = ContentDialogButton.Close;
                break;
            case Phase.Failed:
                var icon = Ui.Icon(Ui.GlyphWarning, 14, Ui.Caution);
                icon.VerticalAlignment = VerticalAlignment.Top;
                var message = Ui.Text(error, foreground: Ui.Caution, selectable: true);
                message.MaxWidth = 520;
                headline.Children.Add(icon);
                headline.Children.Add(message);
                dialog.PrimaryButtonText = path is null ? "" : "Run Setup Again";
                dialog.CloseButtonText = "Close";
                dialog.DefaultButton = ContentDialogButton.Close;
                break;
        }
        dialog.IsPrimaryButtonEnabled = true;
    }

    // Open in Browser and Run Setup Again keep the dialog open.
    private void PrimaryClicked(ContentDialog sender, ContentDialogButtonClickEventArgs args)
    {
        args.Cancel = true;
        if (phase == Phase.Ready && Worktree is { } worktree)
        {
            model.Open(worktree);
        }
        else if (phase == Phase.Failed && path is { } folder)
        {
            var name = Worktree?.Name ?? folder;
            _ = Observe(RunAsync(token => SetupAgainAsync(name, folder, token)));
        }
    }

    private async Task<(string? Path, string? Error)> SetupAgainAsync(string name, string folder, CancellationToken token) =>
        (folder, await model.Sites.SetupWorktreeAsync(name, folder, again: true, token));

    private static async Task Observe(Task task)
    {
        try
        {
            await task;
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    /// <summary>Shows a new dialog following a job, until it is closed; the worktree when it got ready.</summary>
    public static async Task<Status.Site?> ShowAsync(SettingsWindow window, RuntimeService model, string title,
        Func<CancellationToken, Task<(string? Path, string? Error)>> start)
    {
        var dialog = new ContentDialog { XamlRoot = window.XamlRoot };
        var view = new WorktreeJobView(dialog, model, title);
        var run = view.RunAsync(start);
        await dialog.ShowAsync();
        await Observe(run);
        return view.Ready;
    }
}
