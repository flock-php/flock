using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>The panes of the Settings window, in navigation order.</summary>
public enum SettingsPane { General, Sites, Php, Node, Services, Mail, Dumps, Debugger, About }

/// <summary>
/// One pane of the Settings window. Build() renders it from the current state; it is called again
/// whenever the daemon's state changes, so it must not keep state in the controls it creates
/// (panes that need some, like a text field being typed in, keep those controls in fields).
/// </summary>
internal abstract class Pane(SettingsWindow window, AppModel model)
{
    protected SettingsWindow Window { get; } = window;
    protected AppModel Model { get; } = model;
    protected SettingsStore Store => Model.Settings;

    public abstract string Title { get; }
    public abstract string Subtitle { get; }

    public abstract UIElement Build();

    /// <summary>Called when the pane is shown; the token is cancelled when it is hidden.</summary>
    public virtual void Shown(CancellationToken hidden)
    {
    }

    protected Task Perform(Func<Kit.FlockClient, Task> change) => Store.PerformAsync(change);

    protected Panel Rerender() => Window.Rerender();
}

/// <summary>Settings in the Windows 11 style: navigation on the left, the selected pane as cards on the right.</summary>
internal sealed partial class SettingsWindow : FlockWindow
{
    private readonly AppModel model;
    private readonly NavigationView navigation = new()
    {
        PaneDisplayMode = NavigationViewPaneDisplayMode.Left,
        IsSettingsVisible = false,
        IsBackButtonVisible = NavigationViewBackButtonVisible.Collapsed,
        IsPaneToggleButtonVisible = false,
        OpenPaneLength = 220,
    };
    private readonly TextBlock title = Ui.Text("", "TitleTextBlockStyle");
    private readonly TextBlock subtitle = Ui.Caption("");
    private readonly ScrollViewer scroller = new() { VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
    private readonly InfoBar error;
    private readonly Dictionary<SettingsPane, Pane> panes = [];
    private SettingsPane current = SettingsPane.General;
    private CancellationTokenSource? shown;

    public SettingsWindow(AppModel model) : base("Flock Settings", 980, 680, minWidth: 820, minHeight: 520)
    {
        this.model = model;
        error = Ui.ErrorBar(() => model.Settings.DismissError());
        error.Margin = new Thickness(0, 0, 0, 16);

        panes[SettingsPane.General] = new GeneralPane(this, model);
        panes[SettingsPane.Sites] = new SitesPane(this, model);
        panes[SettingsPane.Php] = new PhpPane(this, model);
        panes[SettingsPane.Node] = new NodePane(this, model);
        panes[SettingsPane.Services] = new ServicesPane(this, model);
        panes[SettingsPane.Mail] = new MailPane(this, model);
        panes[SettingsPane.Dumps] = new DumpsPane(this, model);
        panes[SettingsPane.Debugger] = new DebuggerPane(this, model);
        panes[SettingsPane.About] = new AboutPane(this, model);

        foreach (var (pane, glyph) in new[]
        {
            (SettingsPane.General, Ui.GlyphSettings), (SettingsPane.Sites, Ui.GlyphGlobe), (SettingsPane.Php, Ui.GlyphCode),
            (SettingsPane.Node, Ui.GlyphTerminal), (SettingsPane.Services, Ui.GlyphServer), (SettingsPane.Mail, Ui.GlyphMail),
            (SettingsPane.Dumps, Ui.GlyphDownload), (SettingsPane.Debugger, Ui.GlyphBug), (SettingsPane.About, Ui.GlyphInfo),
        })
        {
            navigation.MenuItems.Add(new NavigationViewItem { Content = panes[pane].Title, Icon = new FontIcon { Glyph = glyph, FontFamily = Ui.Symbols }, Tag = pane });
        }
        navigation.SelectionChanged += (_, e) =>
        {
            if (e.SelectedItem is NavigationViewItem { Tag: SettingsPane pane } && pane != current)
            {
                Show(pane);
            }
        };

        var page = new Grid { Padding = new Thickness(36, 8, 36, 0), MaxWidth = 1000, HorizontalAlignment = HorizontalAlignment.Stretch };
        page.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        page.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        page.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        var heading = Ui.Column(2, title, subtitle);
        heading.Margin = new Thickness(0, 0, 0, 20);
        Grid.SetRow(scroller, 1);
        Grid.SetRow(error, 2);
        page.Children.Add(heading);
        page.Children.Add(scroller);
        page.Children.Add(error);
        navigation.Content = page;
        Body = navigation;

        model.Changed += Rerender_;
        model.Settings.Changed += Rerender_;
        Preferences.Shared.Changed += Rerender_;
        model.Settings.IsVisible = true;
        Closed += (_, _) =>
        {
            shown?.Cancel();
            model.Settings.IsVisible = false;
            model.Changed -= Rerender_;
            model.Settings.Changed -= Rerender_;
            Preferences.Shared.Changed -= Rerender_;
        };

        Select(SettingsPane.General);
        _ = model.Settings.RefreshAsync();
    }

    /// <summary>Shows a pane (from the tray menu or another window).</summary>
    public void Select(SettingsPane pane)
    {
        navigation.SelectedItem = navigation.MenuItems.OfType<NavigationViewItem>().First(i => (SettingsPane)i.Tag == pane);
        Show(pane);
    }

    private void Show(SettingsPane pane)
    {
        current = pane;
        shown?.Cancel();
        shown = new CancellationTokenSource();
        title.Text = panes[pane].Title;
        subtitle.Text = panes[pane].Subtitle;
        Rerender();
        scroller.ChangeView(null, 0, null, disableAnimation: true);
        panes[pane].Shown(shown.Token);
    }

    private void Rerender_() => Rerender();

    /// <summary>Renders the current pane again from the latest state.</summary>
    public Panel Rerender()
    {
        Ui.ShowError(error, model.Settings.Error);
        var container = new StackPanel { Padding = new Thickness(0, 0, 0, 24) };
        if (model.Status is null && current != SettingsPane.About)
        {
            container.Children.Add(Ui.EmptyState(Ui.GlyphWarning, "Flock is not running", model.Error ?? "Start the daemon to change its settings.",
                Ui.Button("Start Flock", () => _ = model.RestartAsync(), accent: true)));
        }
        else
        {
            container.Children.Add(panes[current].Build());
        }
        scroller.Content = container;
        return container;
    }

    // Pickers need the window they belong to (unpackaged apps have no implicit owner).

    public async Task<string?> PickFolder()
    {
        var picker = new Windows.Storage.Pickers.FolderPicker();
        picker.FileTypeFilter.Add("*");
        WinRT.Interop.InitializeWithWindow.Initialize(picker, Hwnd);
        var folder = await picker.PickSingleFolderAsync();
        return folder?.Path;
    }

    public async Task<bool> Confirm(string question, string details, string action)
    {
        var dialog = new ContentDialog
        {
            Title = question,
            Content = details,
            PrimaryButtonText = action,
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Content.XamlRoot,
        };
        return await dialog.ShowAsync() == ContentDialogResult.Primary;
    }

    public XamlRoot XamlRoot => Content.XamlRoot;
}
