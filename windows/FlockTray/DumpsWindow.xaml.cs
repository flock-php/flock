using System.ComponentModel;
using System.Text.Json;
using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>A terminal-like timeline of dumps, queries, logs, views, and jobs.</summary>
internal sealed partial class DumpsWindow : FlockWindow
{
    private readonly ToggleMenuFlyoutItem dumpsInPage = new() { Text = "Show dump() in the Page Too" };
    private readonly MenuFlyout editors = new();
    private readonly Task initialRefresh;
    private readonly Dictionary<string, ToggleMenuFlyoutItem> kindItems = [];
    private readonly HtmlView timeline;
    private readonly MenuFlyoutItem unsupported = new() { IsEnabled = false, Visibility = Visibility.Collapsed };
    private readonly DumpsViewModel viewModel;
    private bool released;
    private Task timelineLoad = Task.CompletedTask;

    public DumpsWindow(DumpsViewModel viewModel) : base("Dumps", 920, 620)
    {
        this.viewModel = viewModel;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        WindowBody.DataContext = viewModel;
        timeline = new HtmlView(true, true, false, viewModel.Model.Shell);
        CaptureKinds.Flyout = BuildCaptureMenu();
        EditorButton.Flyout = BuildEditorMenu();
        ToolTipService.SetToolTip(Capture, "Intercept dump() and dd() output from your sites and artisan commands");
        ToolTipService.SetToolTip(LatestOnly, "Show only the latest request or command");
        ToolTipService.SetToolTip(EditorButton, "Editor used when clicking a dump's file and line");
        ToolTipService.SetToolTip(CaptureKinds, "What is captured next to dump() and dd()");
        viewModel.PropertyChanged += ViewModelChanged;
        viewModel.AcquireVisibility();
        Closed += (_, _) => Unsubscribe();
        Render(reloadTimeline: true);
        initialRefresh = viewModel.RefreshAsync();
        Ui.SettingsShortcut(WindowRoot);
    }

    private MenuFlyout BuildCaptureMenu()
    {
        var menu = new MenuFlyout();
        foreach (var kind in EventKind.All)
        {
            var item = new ToggleMenuFlyoutItem { Text = EventKind.Name(kind) };
            item.Click += async (_, _) => await viewModel.SetCaptureAsync(kind, item.IsChecked);
            kindItems[kind] = item;
            menu.Items.Add(item);
        }
        menu.Items.Add(new MenuFlyoutSeparator());
        dumpsInPage.Click += async (_, _) => await viewModel.SetDumpsInPageAsync(dumpsInPage.IsChecked);
        menu.Items.Add(dumpsInPage);
        menu.Items.Add(unsupported);
        return menu;
    }

    private MenuFlyout BuildEditorMenu()
    {
        foreach (var editor in EditorInfo.All)
        {
            var item = new RadioMenuFlyoutItem { Text = editor.Name(), GroupName = "editor", IsChecked = EditorInfo.Parse(viewModel.Editor) == editor };
            item.Click += async (_, _) =>
            {
                try { await viewModel.SetEditorAsync(editor); }
                catch (OperationCanceledException) { }
                catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException) { }
            };
            editors.Items.Add(item);
        }
        return editors;
    }

    private void SearchChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        viewModel.Query = Search.Text ?? "";
    }

    private async void CaptureClicked(object sender, RoutedEventArgs args)
    {
        if (Capture.IsChecked is { } enabled) await viewModel.SetDumpsAsync(enabled);
    }

    private async void LatestOnlyClicked(object sender, RoutedEventArgs args)
    {
        try { await viewModel.SetLatestOnlyAsync(LatestOnly.IsChecked == true); }
        catch (OperationCanceledException) { }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException) { }
    }

    private async void ClearClicked(object sender, RoutedEventArgs args)
    {
        if (await Confirm("Clear all dumps?", "Every captured dump, query, log, view and job is deleted, from all sites and commands.", "Clear", true))
        {
            await viewModel.ClearCommand.ExecuteAsync(null);
        }
    }

    private void DismissError(InfoBar sender, object args)
    {
        viewModel.DismissError();
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        Render(RequiresTimelineReload(args.PropertyName));
    }

    private static bool RequiresTimelineReload(string? propertyName)
    {
        return propertyName is nameof(DumpsViewModel.VisibleDumps) or nameof(DumpsViewModel.VisibleEvents) or nameof(DumpsViewModel.Header)
            or nameof(DumpsViewModel.Editor);
    }

    private void Render(bool reloadTimeline)
    {
        var capturing = viewModel.Capturing;
        Capture.IsChecked = capturing;
        Capture.Label = capturing ? "Capturing" : "Capture Dumps";
        Capture.IsEnabled = viewModel.Status is not null && !viewModel.IsBusy;
        LatestOnly.IsChecked = viewModel.LatestOnly;
        ClearButton.IsEnabled = viewModel.CanClear;
        Ui.ShowError(ErrorBar, viewModel.Error);
        foreach (var (kind, item) in kindItems)
        {
            item.IsChecked = !(viewModel.Status?.CaptureOff.Contains(kind) ?? false);
            item.IsEnabled = capturing && !viewModel.IsBusy;
        }
        dumpsInPage.IsChecked = viewModel.Status?.DumpsInPage == true;
        dumpsInPage.IsEnabled = capturing && !viewModel.IsBusy;
        var editor = EditorInfo.Parse(viewModel.Editor);
        foreach (var item in editors.Items.OfType<RadioMenuFlyoutItem>())
        {
            item.IsChecked = item.Text == editor.Name();
        }
        var missing = viewModel.Status?.CaptureUnsupported ?? [];
        unsupported.Text = missing.Count > 0 ? $"Capture unavailable: PHP {string.Join(", ", missing)}" : "";
        unsupported.Visibility = missing.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        var visibleDumps = viewModel.VisibleDumps;
        var visibleEvents = viewModel.VisibleEvents;
        var count = visibleDumps.Count + visibleEvents.Count(e => e.Kind != EventKind.Request);
        Heading = count == 0 ? "Dumps" : $"Dumps ({count})";
        if (count == 0)
        {
            ContentHost.Content = Ui.EmptyState(Ui.GlyphBug, viewModel.Dumps.Count == 0 && viewModel.Events.Count == 0 ? "No dumps yet" : "No matching dumps",
                capturing
                    ? "dump() and dd() output, queries, logs, views and jobs from your sites and artisan commands appear here."
                    : "Capture is off — turn it on to collect dump() and dd() output here.",
                capturing ? null : Ui.AsyncButton("Start Capturing", () => viewModel.SetDumpsAsync(true), true));
            return;
        }
        if (reloadTimeline)
        {
            timelineLoad = timeline.LoadAsync(DumpTimeline.Html(visibleDumps, visibleEvents, viewModel.Header ?? "", EditorInfo.Parse(viewModel.Editor)));
        }
        ContentHost.Content = timeline;
    }

    private void Unsubscribe()
    {
        viewModel.PropertyChanged -= ViewModelChanged;
    }

    public override async Task ReleaseAsync()
    {
        if (released) return;
        released = true;
        Unsubscribe();
        await timelineLoad;
        await timeline.ReleaseAsync();
        await initialRefresh;
        viewModel.Dispose();
    }
}