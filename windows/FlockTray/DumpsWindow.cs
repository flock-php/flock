using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>A terminal-like timeline: newest request on top, dumps one after another, no sidebar.</summary>
internal sealed partial class DumpsWindow : FlockWindow
{
    private readonly AppModel model;
    private readonly DumpStore store;
    private readonly HtmlView timeline = new(allowScripts: true, blockRemote: true);
    private readonly ContentControl content = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly AutoSuggestBox search = new() { PlaceholderText = "Search dumps", QueryIcon = new SymbolIcon(Symbol.Find), Width = 280 };
    private readonly AppBarToggleButton capture = new() { Icon = new SymbolIcon(Symbol.Download), Label = "Capture Dumps" };
    private readonly AppBarToggleButton latestOnly = new() { Icon = new SymbolIcon(Symbol.Clock), Label = "Latest Request Only" };
    private readonly AppBarButton clear = new() { Icon = new SymbolIcon(Symbol.Delete), Label = "Clear" };
    private readonly InfoBar error;
    private bool rendering;

    public DumpsWindow(AppModel model) : base("Dumps", 920, 620)
    {
        this.model = model;
        store = model.Dumps;
        var prefs = Preferences.Shared;
        // Shows the dumps list's errors and the capture toggle's (which belong to the model).
        error = Ui.ErrorBar(() =>
        {
            store.Error = null;
            model.DismissError();
            Render();
        });

        capture.Click += (_, _) =>
        {
            if (!rendering)
            {
                _ = model.SetDumpsAsync(capture.IsChecked == true);
            }
        };
        ToolTipService.SetToolTip(capture, "Intercept dump() and dd() output from your sites and artisan commands");

        latestOnly.IsChecked = prefs.DumpsLatestOnly;
        latestOnly.Click += (_, _) =>
        {
            prefs.DumpsLatestOnly = latestOnly.IsChecked == true;
            prefs.Save();
            Render();
        };
        ToolTipService.SetToolTip(latestOnly, "Show only the dumps of the latest request or command");

        var editors = new MenuFlyout();
        foreach (var editor in EditorInfo.All)
        {
            var item = new RadioMenuFlyoutItem { Text = editor.Name(), GroupName = "editor", IsChecked = prefs.EditorChoice == editor };
            item.Click += (_, _) => prefs.EditorChoice = editor;
            editors.Items.Add(item);
        }
        var editorButton = new AppBarButton { Icon = new SymbolIcon(Symbol.Edit), Label = "Editor", Flyout = editors };
        ToolTipService.SetToolTip(editorButton, "Editor used when clicking a dump's file and line");

        clear.Click += async (_, _) =>
        {
            if (await Confirm("Clear all dumps?", "Every captured dump is deleted, from all sites and commands.", "Clear", destructive: true))
            {
                await store.ClearAsync();
            }
        };

        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        foreach (var command in new ICommandBarElement[] { capture, latestOnly, editorButton, clear })
        {
            toolbar.PrimaryCommands.Add(command);
        }
        search.TextChanged += (_, _) => Render();
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

        store.Changed += Render;
        model.Changed += Render;
        prefs.Changed += Render;
        store.IsVisible = true;
        Closed += (_, _) =>
        {
            store.IsVisible = false;
            store.Changed -= Render;
            model.Changed -= Render;
            prefs.Changed -= Render;
        };
        Render();
        _ = store.RefreshAsync();
    }

    private void Render()
    {
        rendering = true;
        var capturing = model.Status?.Dumps == true;
        capture.IsChecked = capturing;
        capture.Label = capturing ? "Capturing" : "Capture Dumps";
        capture.IsEnabled = model.Status is not null;
        clear.IsEnabled = store.Dumps.Count > 0;
        rendering = false;
        Ui.ShowError(error, store.Error ?? model.ActionError);

        var source = Preferences.Shared.DumpsLatestOnly ? DumpTimeline.LatestGroup(store.Dumps) : store.Dumps;
        var visible = DumpTimeline.Filter(source, search.Text ?? "");
        Heading = visible.Count == 0 ? "Dumps" : $"Dumps ({visible.Count})";

        if (visible.Count == 0)
        {
            UIElement? action = capturing ? null : Ui.Button("Start Capturing", () => _ = model.SetDumpsAsync(true), accent: true);
            content.Content = Ui.EmptyState(Ui.GlyphBug,
                store.Dumps.Count == 0 ? "No dumps yet" : "No matching dumps",
                capturing
                    ? "dump() and dd() output from your sites and artisan commands appears here."
                    : "Capture is off — turn it on to collect dump() and dd() output here.",
                action);
            return;
        }

        timeline.Load(DumpTimeline.Html(visible, store.Header, Preferences.Shared.EditorChoice));
        content.Content = timeline;
    }
}
