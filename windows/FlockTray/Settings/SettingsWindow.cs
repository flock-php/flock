using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Flock.Tray.ViewModels;
using Flock.Tray.Infrastructure;

namespace Flock.Tray;

/// <summary>The panes of the Settings window, in navigation order.</summary>
public enum SettingsPane { General, Sites, Php, Node, Services, Mail, Dumps, Debugger, Ai, About }

/// <summary>
/// One pane of the Settings window. Build() renders it from the current state; it is called again
/// whenever the daemon's state changes, so it must not keep state in the controls it creates
/// (panes that need some, like a text field being typed in, keep those controls in fields).
/// </summary>
internal abstract class Pane(SettingsWindow window, RuntimeService model)
{
    protected SettingsWindow Window { get; } = window;
    protected RuntimeService Model { get; } = model;

    public abstract string Title { get; }
    public abstract string Subtitle { get; }

    public abstract UIElement Build();

    public virtual bool RetainsRoot => false;

    private UIElement? root;

    public UIElement GetRoot() => RetainsRoot ? root ??= Build() : Build();

    /// <summary>
    /// The pane fills the window's height and scrolls its parts itself (Services: the list and the
    /// details each), instead of the window scrolling it as a whole.
    /// </summary>
    public virtual bool ScrollsItself => false;

    /// <summary>Called when the pane is shown; the token is cancelled when it is hidden.</summary>
    public virtual void Shown(CancellationToken hidden)
    {
    }

    public virtual Task ActivateAsync(CancellationToken hidden)
    {
        Shown(hidden);
        return Task.CompletedTask;
    }

    public virtual Task ReleaseAsync() => Task.CompletedTask;

    public virtual void Hidden()
    {
    }

    protected async Task PerformAsync(Func<Kit.FlockClient, Task> change)
    {
        await change(Model.Client);
        await Model.Configuration.RefreshAsync();
    }

    protected Panel Rerender() => Window.Rerender();
}

/// <summary>Settings in the Windows 11 style: navigation on the left, the selected pane as cards on the right.</summary>
internal sealed partial class SettingsWindow : FlockWindow
{
    private readonly RuntimeService model;
    private readonly NavigationView navigation = new()
    {
        PaneDisplayMode = NavigationViewPaneDisplayMode.Left,
        IsSettingsVisible = false,
        IsBackButtonVisible = NavigationViewBackButtonVisible.Collapsed,
        IsPaneToggleButtonVisible = false,
        OpenPaneLength = 220,
    };
    private readonly TextBlock title = Ui.Text("", "TitleTextBlockStyle");
    // Colored in Show(): theme brushes looked up before the window exists come from the light theme.
    private readonly TextBlock subtitle = Ui.Text("", "CaptionTextBlockStyle");
    private readonly ScrollViewer scroller = new() { VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
    private readonly InfoBar error;
    private readonly Dictionary<SettingsPane, Pane> panes = [];
    internal SitesPane Sites => (SitesPane)panes[SettingsPane.Sites];
    private SettingsPane current = SettingsPane.General;
    private readonly SettingsWindowViewModel viewModel;
    private readonly Action<Features.Preferences.PreferenceData> preferencesChanged;
    private readonly Action preferencesErrorChanged;

    public SettingsWindow(RuntimeService model, DeepLinkCoordinator? deepLinks = null) : base("Flock Settings", 980, 680, minWidth: 820, minHeight: 520)
    {
        this.model = model;
        viewModel = new SettingsWindowViewModel(ActivatePaneAsync);
        preferencesChanged = _ => Rerender_();
        preferencesErrorChanged = Rerender_;
        // The Settings store's errors, and the model's (Xdebug and dumps toggles, restarts).
        error = Ui.ErrorBar(() =>
        {
            model.DismissError();
            model.Preferences.DismissError();
            model.Configuration.DismissError();
            model.Php.DismissError();
            model.FrankenPhp.DismissError();
            model.Node.DismissError();
            model.Services.DismissError();
            model.Postgres.DismissError();
            model.Catalog.DismissError();
            model.Mcp.DismissError();
            model.Sites.DismissError();
        });
        error.Margin = new Thickness(36, 0, 36, 16);
        error.MaxWidth = PageWidth - 72;

        panes[SettingsPane.General] = new GeneralPane(this, model);
        deepLinks ??= new DeepLinkCoordinator(model);
        panes[SettingsPane.Sites] = new SitesPane(this, model, deepLinks);
        panes[SettingsPane.Php] = new PhpPane(this, model);
        panes[SettingsPane.Node] = new NodePane(this, model);
        panes[SettingsPane.Services] = new ServicesPane(this, model, deepLinks);
        panes[SettingsPane.Mail] = new MailPane(this, model);
        panes[SettingsPane.Dumps] = new DumpsPane(this, model);
        panes[SettingsPane.Debugger] = new DebuggerPane(this, model);
        panes[SettingsPane.Ai] = new AiPane(this, model);
        panes[SettingsPane.About] = new AboutPane(this, model);

        foreach (var (pane, glyph) in new[]
        {
            (SettingsPane.General, Ui.GlyphSettings), (SettingsPane.Sites, Ui.GlyphGlobe), (SettingsPane.Php, Ui.GlyphCode),
            (SettingsPane.Node, Ui.GlyphTerminal), (SettingsPane.Services, Ui.GlyphServer), (SettingsPane.Mail, Ui.GlyphMail),
            (SettingsPane.Dumps, Ui.GlyphDownload), (SettingsPane.Debugger, Ui.GlyphBug), (SettingsPane.Ai, Ui.GlyphRobot), (SettingsPane.About, Ui.GlyphInfo),
        })
        {
            navigation.MenuItems.Add(new NavigationViewItem { Content = panes[pane].Title, Icon = new FontIcon { Glyph = glyph, FontFamily = Ui.Symbols }, Tag = pane });
        }
        navigation.SelectionChanged += (_, e) =>
        {
            if (e.SelectedItem is NavigationViewItem { Tag: SettingsPane pane })
            {
                StartActivation(pane);
            }
        };

        // The side padding is inside the scroller, so its scroll bar runs along the window's edge
        // instead of over the cards.
        var page = new Grid { Padding = new Thickness(0, 8, 0, 0) };
        page.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        page.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        page.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        var heading = Ui.Column(2, title, subtitle);
        heading.Margin = new Thickness(36, 0, 36, 20);
        // The pane's MaxWidth includes its 36 side padding; this margin is outside it.
        heading.MaxWidth = PageWidth - 72;
        Grid.SetRow(scroller, 1);
        Grid.SetRow(error, 2);
        page.Children.Add(heading);
        page.Children.Add(scroller);
        page.Children.Add(error);
        navigation.Content = page;
        Body = navigation;

        model.Changed += Rerender_;
        model.Configuration.Changed += Rerender_;
        model.Php.Changed += Rerender_;
        model.FrankenPhp.Changed += Rerender_;
        model.Node.Changed += Rerender_;
        model.Services.Changed += Rerender_;
        model.Postgres.Changed += Rerender_;
        model.Catalog.Changed += Rerender_;
        model.Dumps.Changed += Rerender_;
        model.Preferences.Changed += preferencesChanged;
        model.Preferences.ErrorChanged += preferencesErrorChanged;
        model.Sites.Changed += Rerender_;
        Closed += (_, _) =>
        {
            model.Changed -= Rerender_;
            model.Configuration.Changed -= Rerender_;
            model.Php.Changed -= Rerender_;
            model.FrankenPhp.Changed -= Rerender_;
            model.Node.Changed -= Rerender_;
            model.Services.Changed -= Rerender_;
            model.Postgres.Changed -= Rerender_;
            model.Catalog.Changed -= Rerender_;
            model.Dumps.Changed -= Rerender_;
            model.Preferences.Changed -= preferencesChanged;
            model.Preferences.ErrorChanged -= preferencesErrorChanged;
            model.Sites.Changed -= Rerender_;
        };

        navigation.SelectedItem = navigation.MenuItems.OfType<NavigationViewItem>().First(i => (SettingsPane)i.Tag == SettingsPane.General);
        _ = viewModel.Navigation.ActivateAsync(SettingsPane.General);
        _ = model.Configuration.RefreshAsync();
        _ = model.Services.RefreshAsync();
    }

    /// <summary>Shows a pane (from the tray menu or another window).</summary>
    public void Select(SettingsPane pane)
    {
        if (viewModel.Navigation.Selected == pane)
        {
            return;
        }
        navigation.SelectedItem = navigation.MenuItems.OfType<NavigationViewItem>().First(i => (SettingsPane)i.Tag == pane);
    }

    /// <summary>Shows a service's details in the Services pane.</summary>
    public void ShowService(string id)
    {
        Select(SettingsPane.Services);
        ((ServicesPane)panes[SettingsPane.Services]).Select(id);
    }

    private async Task ActivatePaneAsync(SettingsPane pane, CancellationToken hidden)
    {
        if (current != pane)
        {
            panes[current].Hidden();
        }
        current = pane;
        title.Text = panes[pane].Title;
        subtitle.Text = panes[pane].Subtitle;
        subtitle.Foreground = Ui.SecondaryText;
        Rerender();
        scroller.ChangeView(null, 0, null, disableAnimation: true);
        await panes[pane].ActivateAsync(hidden);
    }

    private void Rerender_()
    {
        if (current != SettingsPane.Ai)
        {
            Rerender();
        }
    }

    private void StartActivation(SettingsPane pane) => _ = viewModel.Navigation.ActivateAsync(pane);

    /// <summary>Renders the current pane again from the latest state.</summary>
    public Panel Rerender()
    {
        if (scroller.Content is Grid previousHost)
        {
            foreach (var previousContainer in previousHost.Children.OfType<Panel>())
            {
                previousContainer.Children.Clear();
            }
        }
        Ui.ShowError(error, model.RuntimeError ?? model.Preferences.Error ?? model.Configuration.Error ?? model.Php.Error ?? model.FrankenPhp.Error ??
            model.Node.Error ?? model.Services.Error ?? model.Postgres.Error ?? model.Catalog.Error ?? model.Mcp.Error ??
             model.Sites.Error ?? model.Dumps.Error ?? model.ActionError);
        // A pane that scrolls itself gets the viewport's height: a Grid passes it on, a StackPanel doesn't.
        var fills = panes[current].ScrollsItself && model.Status is not null;
        scroller.VerticalScrollMode = fills ? ScrollMode.Disabled : ScrollMode.Auto;
        scroller.VerticalScrollBarVisibility = fills ? ScrollBarVisibility.Disabled : ScrollBarVisibility.Auto;
        Panel container = fills
            ? new Grid { Padding = new Thickness(36, 0, 36, 16), MaxWidth = PageWidth }
            : new StackPanel { Padding = new Thickness(36, 0, 36, 24), MaxWidth = PageWidth };
        if (model.Status is null && current != SettingsPane.About)
        {
            container.Children.Add(Ui.EmptyState(Ui.GlyphWarning, "Flock is not running", model.Error ?? "Start the daemon to change its settings.",
                Ui.Button("Start Flock", () => _ = model.RestartAsync(), accent: true)));
        }
        else
        {
            try
            {
                var paneRoot = panes[current].GetRoot();
                container.Children.Add(paneRoot);
            }
            catch (Exception e)
            {
                // Never leave the previous pane's content under this pane's title.
                App.Log(e);
                container.Children.Add(Ui.EmptyState(Ui.GlyphWarning, $"{panes[current].Title} couldn't be shown", $"{e.Message}\n\nDetails are in %LocalAppData%\\Flock\\tray.log."));
            }
        }
        // The scroller centers a MaxWidth child by its desired width, not the width it gets, which
        // pushed narrow panes right (and past the window's edge at some widths). A Grid in between
        // takes the viewport's width and centers the container properly.
        var host = new Grid();
        host.Children.Add(container);
        scroller.Content = host;
        return container;
    }

    private const double PageWidth = 1000;

    // Pickers need the window they belong to (unpackaged apps have no implicit owner).

    public async Task<string?> PickFolder()
    {
        var picker = new Windows.Storage.Pickers.FolderPicker();
        picker.FileTypeFilter.Add("*");
        WinRT.Interop.InitializeWithWindow.Initialize(picker, Hwnd);
        var folder = await picker.PickSingleFolderAsync();
        return folder?.Path;
    }

    /// <summary>Several folders at once, through the shell's dialog; one, through FolderPicker, if that dialog fails.</summary>
    public async Task<List<string>> PickFolders(string title)
    {
        try
        {
            return Native.PickFolders(Hwnd, title);
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidCastException)
        {
            App.Log(e);
        }
        return await PickFolder() is { } path ? [path] : [];
    }

    /// <summary>Asks for one line of text; null when cancelled or left empty.</summary>
    public async Task<string?> Ask(string question, string details, string placeholder, string action)
    {
        var box = new TextBox { PlaceholderText = placeholder };
        var content = new StackPanel { Spacing = 12 };
        content.Children.Add(new TextBlock { Text = details, TextWrapping = TextWrapping.Wrap });
        content.Children.Add(box);
        var dialog = new ContentDialog
        {
            Title = question,
            Content = content,
            PrimaryButtonText = action,
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Content.XamlRoot,
        };
        box.KeyDown += (_, e) =>
        {
            if (e.Key == Windows.System.VirtualKey.Enter)
            {
                box.Tag = "submitted";
                dialog.Hide();
            }
        };
        var result = await dialog.ShowAsync();
        var text = box.Text.Trim();
        return (result == ContentDialogResult.Primary || box.Tag is "submitted") && text.Length > 0 ? text : null;
    }

    public XamlRoot XamlRoot => Content.XamlRoot;

    public override async Task ReleaseAsync()
    {
        await viewModel.DisposeAsync();
        foreach (var pane in panes.Values)
        {
            await pane.ReleaseAsync();
        }
        await base.ReleaseAsync();
    }
}
