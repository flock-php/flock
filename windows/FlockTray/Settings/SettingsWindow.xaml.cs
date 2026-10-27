using System.Runtime.InteropServices;
using Windows.Storage.Pickers;
using Windows.System;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using WinRT.Interop;
namespace Flock.Tray;

/// <summary>The panes of the Settings window, in navigation order.</summary>
public enum SettingsPane { General, Sites, Php, Node, Services, Mail, Dumps, Debugger, Ai, About }

internal interface ISettingsPane
{
    string Title { get; }
    string Subtitle { get; }
    UIElement Root { get; }
    bool ScrollsItself { get; }
    Task ActivateAsync(CancellationToken hidden);
    Task ReleaseAsync();
    void Hidden();
}

/// <summary>Settings in the Windows 11 style: navigation on the left, the selected pane as cards on the right.</summary>
internal sealed partial class SettingsWindow : FlockWindow
{
    private readonly RuntimeService model;
    private readonly Dictionary<SettingsPane, ISettingsPane> panes = [];
    private readonly SettingsWindowViewModel viewModel;
    private SettingsPane current = SettingsPane.General;
    private readonly bool initialized;
    private bool unsubscribed;

    public SettingsWindow(RuntimeService model, DeepLinkCoordinator? deepLinks = null) : base("Flock Settings", 980, 680, 820, 520)
    {
        this.model = model;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        viewModel = new SettingsWindowViewModel(ActivatePaneAsync);
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

        model.Changed += StateChanged;
        model.Configuration.Changed += StateChanged;
        model.Php.Changed += StateChanged;
        model.FrankenPhp.Changed += StateChanged;
        model.Node.Changed += StateChanged;
        model.Services.Changed += StateChanged;
        model.Postgres.Changed += StateChanged;
        model.Catalog.Changed += StateChanged;
        model.Dumps.Changed += StateChanged;
        model.Preferences.StateChanged += StateChanged;
        model.Sites.Changed += StateChanged;
        Closed += ClosedWindow;

        initialized = true;
        Navigation.SelectedItem = Navigation.MenuItems.OfType<NavigationViewItem>()
            .First(i => (SettingsPane)Enum.Parse(typeof(SettingsPane), (string)i.Tag) == SettingsPane.General);
        _ = viewModel.Navigation.ActivateAsync(SettingsPane.General);
        _ = model.Configuration.RefreshAsync();
        _ = model.Services.RefreshAsync();
    }

    internal SitesPane Sites => (SitesPane)panes[SettingsPane.Sites];

    public XamlRoot XamlRoot => Content.XamlRoot;

    /// <summary>Shows a pane (from the tray menu or another window).</summary>
    public void Select(SettingsPane pane)
    {
        if (viewModel.Navigation.Selected == pane)
        {
            return;
        }
        Navigation.SelectedItem = Navigation.MenuItems.OfType<NavigationViewItem>().First(i => (SettingsPane)Enum.Parse(typeof(SettingsPane), (string)i.Tag) == pane);
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
        PaneTitle.Text = panes[pane].Title;
        Subtitle.Text = panes[pane].Subtitle;
        Scroller.ChangeView(null, 0, null, true);
        ShowPane();
        await panes[pane].ActivateAsync(hidden);
    }

    private void NavigationSelectionChanged(NavigationView sender, NavigationViewSelectionChangedEventArgs args)
    {
        if (initialized && args.SelectedItem is NavigationViewItem { Tag: string tag } && Enum.TryParse<SettingsPane>(tag, out var pane))
        {
            _ = viewModel.Navigation.ActivateAsync(pane);
        }
    }

    private void StateChanged()
    {
        if (!initialized)
        {
            return;
        }
        ShowPane();
    }

    private void ShowPane()
    {
        UpdateError();
        var pane = panes[current];
        var fills = pane.ScrollsItself && model.Status is not null;
        Scroller.VerticalScrollMode = fills ? ScrollMode.Disabled : ScrollMode.Auto;
        Scroller.VerticalScrollBarVisibility = fills ? ScrollBarVisibility.Disabled : ScrollBarVisibility.Auto;
        if (model.Status is null && current != SettingsPane.About)
        {
            PaneHost.Children.Clear();
            PaneHost.Children.Add(Ui.EmptyState(Ui.GlyphWarning, "Flock is not running", model.Error ?? "Start the daemon to change its settings.",
                Ui.Button("Start Flock", () => _ = model.RestartAsync(), true)));
            return;
        }
        if (PaneHost.Children.Count == 1 && ReferenceEquals(PaneHost.Children[0], pane.Root))
        {
            return;
        }
        PaneHost.Children.Clear();
        PaneHost.Children.Add(pane.Root);
    }

    private void UpdateError()
    {
        Ui.ShowError(ErrorBar, model.RuntimeError ?? model.Preferences.Error ?? model.Configuration.Error ?? model.Php.Error ?? model.FrankenPhp.Error ??
            model.Node.Error ?? model.Services.Error ??
            model.Postgres.Error ?? model.Catalog.Error ?? model.Mcp.Error ?? model.Sites.Error ?? model.Dumps.Error ?? model.ActionError);
    }

    private void DismissError(InfoBar sender, object args)
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
    }

    private void ClosedWindow(object sender, WindowEventArgs args)
    {
        Unsubscribe();
    }

    private void Unsubscribe()
    {
        if (unsubscribed)
        {
            return;
        }
        unsubscribed = true;
        model.Changed -= StateChanged;
        model.Configuration.Changed -= StateChanged;
        model.Php.Changed -= StateChanged;
        model.FrankenPhp.Changed -= StateChanged;
        model.Node.Changed -= StateChanged;
        model.Services.Changed -= StateChanged;
        model.Postgres.Changed -= StateChanged;
        model.Catalog.Changed -= StateChanged;
        model.Dumps.Changed -= StateChanged;
        model.Preferences.StateChanged -= StateChanged;
        model.Sites.Changed -= StateChanged;
        Closed -= ClosedWindow;
    }

    // Pickers need the window they belong to (unpackaged apps have no implicit owner).
    public async Task<string?> PickFolder()
    {
        var picker = new FolderPicker();
        picker.FileTypeFilter.Add("*");
        InitializeWithWindow.Initialize(picker, Hwnd);
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
        catch (Exception e) when (e is COMException or InvalidCastException)
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
            XamlRoot = Content.XamlRoot
        };
        box.KeyDown += (_, e) =>
        {
            if (e.Key == VirtualKey.Enter)
            {
                box.Tag = "submitted";
                dialog.Hide();
            }
        };
        var result = await dialog.ShowAsync();
        var text = box.Text.Trim();
        return (result == ContentDialogResult.Primary || box.Tag is "submitted") && text.Length > 0 ? text : null;
    }

    public override async Task ReleaseAsync()
    {
        Unsubscribe();
        await viewModel.DisposeAsync();
        foreach (var pane in panes.Values)
        {
            await pane.ReleaseAsync();
        }
        await base.ReleaseAsync();
    }
}