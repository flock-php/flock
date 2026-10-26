using Flock.Kit;
using Flock.Tray.ViewModels;
using System.ComponentModel;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// A terminal-like timeline: newest request on top, its dumps, queries, logs, views and jobs one
/// after another, no sidebar.
/// </summary>
internal sealed partial class DumpsWindow : FlockWindow
{
    private readonly DumpsViewModel viewModel;
    private readonly HtmlView timeline;
    private Task timelineLoad = Task.CompletedTask;
    private readonly Task initialRefresh;
    private readonly ContentControl content = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly AutoSuggestBox search = new() { PlaceholderText = "Search dumps", QueryIcon = new SymbolIcon(Symbol.Find), Width = 280 };
    private readonly AppBarToggleButton capture = new() { Icon = new SymbolIcon(Symbol.Download), Label = "Capture Dumps" };
    private readonly AppBarToggleButton latestOnly = new() { Icon = new SymbolIcon(Symbol.Clock), Label = "Latest Request Only" };
    private readonly AppBarButton clear = new() { Icon = new SymbolIcon(Symbol.Delete), Label = "Clear" };
    private readonly AppBarButton captureKinds = new() { Icon = new SymbolIcon(Symbol.Filter), Label = "Capture" };
    private readonly Dictionary<string, ToggleMenuFlyoutItem> kindItems = [];
    private readonly ToggleMenuFlyoutItem dumpsInPage = new() { Text = "Show dump() in the Page Too" };
    private readonly MenuFlyoutItem unsupported = new() { IsEnabled = false, Visibility = Visibility.Collapsed };
    private readonly InfoBar error;
    private bool rendering;
    private bool released;

    public DumpsWindow(DumpsViewModel viewModel) : base("Dumps", 920, 620)
    {
        this.viewModel = viewModel;
        timeline = new HtmlView(allowScripts: true, blockRemote: true, whiteBackground: false, shell: viewModel.Model.Shell);
        error = Ui.ErrorBar(() =>
        {
            viewModel.DismissError();
            Render();
        });

        capture.Click += async (_, _) =>
        {
            if (!rendering)
            {
                await viewModel.SetDumpsAsync(capture.IsChecked == true);
            }
        };
        ToolTipService.SetToolTip(capture, "Intercept dump() and dd() output from your sites and artisan commands");

        latestOnly.IsChecked = viewModel.LatestOnly;
        latestOnly.Click += async (_, _) =>
        {
            try
            {
                await viewModel.SetLatestOnlyAsync(latestOnly.IsChecked == true);
            }
            catch (OperationCanceledException)
            {
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException)
            {
            }
        };
        ToolTipService.SetToolTip(latestOnly, "Show only the latest request or command");

        var editors = new MenuFlyout();
        foreach (var editor in EditorInfo.All)
        {
            var item = new RadioMenuFlyoutItem { Text = editor.Name(), GroupName = "editor", IsChecked = EditorInfo.Parse(viewModel.Editor) == editor };
            item.Click += async (_, _) =>
            {
                try
                {
                    await viewModel.SetEditorAsync(editor);
                }
                catch (OperationCanceledException)
                {
                }
                catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException)
                {
                }
            };
            editors.Items.Add(item);
        }
        var editorButton = new AppBarButton { Icon = new SymbolIcon(Symbol.Edit), Label = "Editor", Flyout = editors };
        ToolTipService.SetToolTip(editorButton, "Editor used when clicking a dump's file and line");

        clear.Click += async (_, _) =>
        {
            if (await Confirm("Clear all dumps?", "Every captured dump, query, log, view and job is deleted, from all sites and commands.", "Clear", destructive: true))
            {
                await viewModel.ClearCommand.ExecuteAsync(null);
            }
        };

        // What is captured next to the dumps (with the capture extension), and whether dump() still
        // shows in the page: dd() always does.
        var kinds = new MenuFlyout();
        foreach (var kind in EventKind.All)
        {
            var item = new ToggleMenuFlyoutItem { Text = EventKind.Name(kind) };
            item.Click += async (_, _) => await viewModel.SetCaptureAsync(kind, item.IsChecked);
            kindItems[kind] = item;
            kinds.Items.Add(item);
        }
        kinds.Items.Add(new MenuFlyoutSeparator());
        dumpsInPage.Click += async (_, _) => await viewModel.SetDumpsInPageAsync(dumpsInPage.IsChecked);
        kinds.Items.Add(dumpsInPage);
        kinds.Items.Add(unsupported);
        captureKinds.Flyout = kinds;
        ToolTipService.SetToolTip(captureKinds, "What is captured next to dump() and dd()");

        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        foreach (var command in new ICommandBarElement[] { capture, captureKinds, latestOnly, editorButton, clear })
        {
            toolbar.PrimaryCommands.Add(command);
        }
        search.TextChanged += (_, _) =>
        {
            viewModel.Query = search.Text ?? "";
            Render();
        };
        search.VerticalAlignment = VerticalAlignment.Center;
        search.Margin = new Thickness(16, 0, 0, 0);

        var top = Ui.Spread(search, toolbar);
        var root = new Grid();
        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        root.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        Grid.SetRow(content, 1);
        Grid.SetRow(error, 2);
        root.Children.Add(top);
        root.Children.Add(content);
        root.Children.Add(error);
        Body = root;
        Ui.SettingsShortcut(root);

        viewModel.PropertyChanged += ViewModelChanged;
        viewModel.AcquireVisibility();
        Closed += (_, _) => Unsubscribe();
        Render();
        initialRefresh = viewModel.RefreshAsync();
    }

    private void Render()
    {
        rendering = true;
        var capturing = viewModel.Capturing;
        capture.IsChecked = capturing;
        capture.Label = capturing ? "Capturing" : "Capture Dumps";
        capture.IsEnabled = viewModel.Status is not null && !viewModel.IsBusy;
        clear.IsEnabled = viewModel.CanClear;
        rendering = false;
        Ui.ShowError(error, viewModel.Error);

        foreach (var (kind, item) in kindItems)
        {
            item.IsChecked = !(viewModel.Status?.CaptureOff.Contains(kind) ?? false);
            item.IsEnabled = capturing && !viewModel.IsBusy;
        }
        dumpsInPage.IsChecked = viewModel.Status?.DumpsInPage == true;
        dumpsInPage.IsEnabled = capturing && !viewModel.IsBusy;
        var missing = viewModel.Status?.CaptureUnsupported ?? [];
        unsupported.Text = missing.Count > 0 ? $"Capture unavailable: PHP {string.Join(", ", missing)}" : "";
        unsupported.Visibility = missing.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        var visibleDumps = viewModel.VisibleDumps;
        var visibleEvents = viewModel.VisibleEvents;
        var count = visibleDumps.Count + visibleEvents.Count(e => e.Kind != EventKind.Request);
        Heading = count == 0 ? "Dumps" : $"Dumps ({count})";

        if (count == 0)
        {
            UIElement? action = capturing ? null : Ui.AsyncButton("Start Capturing", () => viewModel.SetDumpsAsync(true), accent: true);
            content.Content = Ui.EmptyState(Ui.GlyphBug,
                viewModel.Dumps.Count == 0 && viewModel.Events.Count == 0 ? "No dumps yet" : "No matching dumps",
                capturing
                    ? "dump() and dd() output, queries, logs, views and jobs from your sites and artisan commands appear here."
                    : "Capture is off — turn it on to collect dump() and dd() output here.",
                action);
            return;
        }

        timelineLoad = timeline.LoadAsync(DumpTimeline.Html(visibleDumps, visibleEvents, viewModel.Header ?? "", EditorInfo.Parse(viewModel.Editor)));
        content.Content = timeline;
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args) => Render();

    private void Unsubscribe()
    {
        viewModel.PropertyChanged -= ViewModelChanged;
    }

    public override async Task ReleaseAsync()
    {
        if (released)
        {
            return;
        }
        released = true;
        Unsubscribe();
        await timelineLoad;
        await timeline.ReleaseAsync();
        await initialRefresh;
        viewModel.Dispose();
    }
}
