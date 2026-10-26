using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using System.ComponentModel;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray;

/// <summary>
/// Laravel logs of every site and the logs of Flock's services, with a search, a level filter
/// (site logs) and an auto-refresh of the selected log.
/// </summary>
internal sealed partial class LogsWindow : FlockWindow
{
    private readonly LogsViewModel viewModel;
    private readonly StackPanel siteRows = new() { Spacing = 2 };
    private readonly StackPanel serviceRows = new() { Spacing = 2 };
    private readonly KeyedRowsBinding<LogsViewModel.Source, LogsViewModel.Source> siteBinding;
    private readonly KeyedRowsBinding<LogsViewModel.Source, LogsViewModel.Source> serviceBinding;
    private readonly Dictionary<LogsViewModel.Source, Button> sourceButtons = [];
    private readonly ContentControl content = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly AppBarButton clear = new() { Icon = new SymbolIcon(Symbol.Delete), Label = "Clear", Visibility = Visibility.Collapsed };
    private readonly AutoSuggestBox search = new() { PlaceholderText = "Search messages and context", QueryIcon = new SymbolIcon(Symbol.Find), Width = 260 };
    private readonly ComboBox levels = new() { MinWidth = 170, IsEnabled = false };
    private readonly AppBarToggleButton autoRefresh = new() { Icon = new SymbolIcon(Symbol.Sync), Label = "Auto-refresh" };
    private readonly InfoBar error = Ui.ErrorBar();
    private (LogsViewModel.LogContent Data, string Query, LogLevelFilter Level)? rendered;

    public LogsWindow(LogsViewModel viewModel) : base("Logs", 1000, 660)
    {
        this.viewModel = viewModel;
        siteBinding = new KeyedRowsBinding<LogsViewModel.Source, LogsViewModel.Source>(siteRows, source => source, CreateSourceButton,
            (source, _) => sourceButtons.Remove(source));
        serviceBinding = new KeyedRowsBinding<LogsViewModel.Source, LogsViewModel.Source>(serviceRows, source => source, CreateSourceButton,
            (source, _) => sourceButtons.Remove(source));

        var refresh = new AppBarButton { Icon = new SymbolIcon(Symbol.Refresh), Label = "Refresh", Command = viewModel.RefreshCommand };
        clear.Click += ClearClicked;
        autoRefresh.Click += (_, _) => viewModel.AutoRefreshEnabled = autoRefresh.IsChecked == true;
        ToolTipService.SetToolTip(autoRefresh, "Reload the selected log every 2 seconds while this window is open");
        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        toolbar.PrimaryCommands.Add(autoRefresh);
        toolbar.PrimaryCommands.Add(refresh);
        toolbar.PrimaryCommands.Add(clear);

        foreach (var (_, name) in LogFilter.Choices)
        {
            levels.Items.Add(name);
        }
        levels.SelectedIndex = 0;
        levels.SelectionChanged += (_, _) =>
        {
            if (levels.SelectedIndex >= 0)
            {
                viewModel.Level = LogFilter.Choices[levels.SelectedIndex].Filter;
            }
        };
        ToolTipService.SetToolTip(levels, "Levels of a site's Laravel log to show");
        Ui.AutomationName(levels, "Level");
        search.TextChanged += (_, _) => viewModel.Query = search.Text ?? "";
        var filters = Ui.Row(8, search, levels);
        filters.Margin = new Thickness(16, 0, 0, 0);

        var sourcePanel = new StackPanel { Spacing = 4, Padding = new Thickness(8, 0, 8, 8) };
        var siteHeader = Ui.Caption("SITES");
        siteHeader.FontWeight = FontWeights.SemiBold;
        siteHeader.Margin = new Thickness(8, 10, 0, 2);
        var serviceHeader = Ui.Caption("SERVICES");
        serviceHeader.FontWeight = FontWeights.SemiBold;
        serviceHeader.Margin = new Thickness(8, 18, 0, 2);
        sourcePanel.Children.Add(siteHeader);
        sourcePanel.Children.Add(siteRows);
        sourcePanel.Children.Add(serviceHeader);
        sourcePanel.Children.Add(serviceRows);
        var sourceScroll = new ScrollViewer { Content = sourcePanel, VerticalScrollBarVisibility = ScrollBarVisibility.Auto };

        content.Content = Ui.EmptyState(Ui.GlyphDocument, "Pick a log");
        var split = new Grid();
        split.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(250) });
        split.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var detail = new Border
        {
            Child = content,
            Background = Ui.Brush("LayerFillColorDefaultBrush"),
            BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
            BorderThickness = new Thickness(1, 1, 0, 0),
            CornerRadius = new CornerRadius(8, 0, 0, 0),
        };
        Grid.SetColumn(detail, 1);
        split.Children.Add(sourceScroll);
        split.Children.Add(detail);

        var root = new Grid();
        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        root.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        Grid.SetRow(split, 1);
        Grid.SetRow(error, 2);
        root.Children.Add(Ui.Spread(filters, toolbar));
        root.Children.Add(split);
        root.Children.Add(error);
        Body = root;
        Ui.SettingsShortcut(root);

        viewModel.PropertyChanged += ViewModelChanged;
        AppWindow.Changed += WindowChanged;
        viewModel.SetWindowActive(IsWindowActive());
        Render();
        viewModel.RefreshCommand.Execute(null);
    }

    /// <summary>Shows one log, such as a site's FrankenPHP log from Settings → Sites.</summary>
    public Task ShowSourceAsync(LogsViewModel.Source source) => viewModel.ShowSourceAsync(source);

    public override async Task ReleaseAsync()
    {
        AppWindow.Changed -= WindowChanged;
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.SetWindowActive(false);
        siteBinding.Release();
        serviceBinding.Release();
        await viewModel.DisposeAsync();
    }

    private Button CreateSourceButton(LogsViewModel.Source source)
    {
        var glyph = source is LogsViewModel.SiteSource ? Ui.GlyphGlobe : Ui.GlyphSettings;
        var button = new Button
        {
            Content = Ui.Row(10, Ui.Icon(glyph, 14), Ui.Text(source.Name, wrap: false)),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Left,
            Padding = new Thickness(8, 7, 8, 7),
            Command = viewModel.ShowSourceCommand,
            CommandParameter = source,
        };
        Ui.AutomationName(button, $"Show {source.Name} log");
        sourceButtons[source] = button;
        return button;
    }

    private async void ClearClicked(object sender, RoutedEventArgs args)
    {
        try
        {
            if (viewModel.Selection is LogsViewModel.SiteSource site
                && await Confirm($"Clear the log of {site.Name}?", $"The Laravel log files in storage\\logs of {site.Name} are emptied. This can't be undone.", "Clear", destructive: true))
            {
                await viewModel.ClearCommand.ExecuteAsync(null);
                Render();
            }
        }
        catch (Exception exception)
        {
            App.Log(exception);
        }
    }

    private void WindowChanged(Microsoft.UI.Windowing.AppWindow sender, Microsoft.UI.Windowing.AppWindowChangedEventArgs args)
    {
        if (args.DidVisibilityChange || args.DidPresenterChange)
        {
            viewModel.SetWindowActive(IsWindowActive());
        }
    }

    private bool IsWindowActive() => AppWindow.IsVisible && AppWindow.Presenter is not Microsoft.UI.Windowing.OverlappedPresenter { State: Microsoft.UI.Windowing.OverlappedPresenterState.Minimized };

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args) => Render();

    private void Render()
    {
        Ui.ShowError(error, viewModel.Error);
        clear.Visibility = viewModel.Selection is LogsViewModel.SiteSource ? Visibility.Visible : Visibility.Collapsed;
        clear.IsEnabled = viewModel.CanClear;
        levels.IsEnabled = viewModel.Selection is LogsViewModel.SiteSource;
        autoRefresh.IsChecked = viewModel.AutoRefreshEnabled;
        RenderSources();

        if (viewModel.Selection is null)
        {
            return;
        }
        if (viewModel.Current is not { } current || current.Source != viewModel.Selection)
        {
            rendered = null;
            content.Content = viewModel.LoadError is { } failure
                ? Ui.EmptyState(Ui.GlyphWarning, "Log could not be loaded", failure)
                : new ProgressRing { Width = 32, Height = 32 };
            return;
        }

        var key = (current, viewModel.Query, viewModel.Level);
        if (rendered is { } shown && shown.Equals(key))
        {
            return;
        }
        rendered = key;
        switch (current)
        {
            case LogsViewModel.SiteContent site:
                content.Content = site.Entries.Count == 0
                    ? Ui.EmptyState(Ui.GlyphDocument, "No log entries", $"storage\\logs of {site.Site.Name} is empty.")
                    : viewModel.FilteredEntries.Count == 0
                        ? Ui.EmptyState(Ui.GlyphSearch, "No matching log entries", "Nothing in the latest entries matches the search and level.")
                        : Entries(viewModel.FilteredEntries);
                break;
            case LogsViewModel.ServiceContent service:
                var text = MailDetailView.Monospaced(service.Text.Length == 0 ? "(empty)" : viewModel.FilteredText.Length == 0 ? "(no matching lines)" : viewModel.FilteredText);
                content.Content = text;
                text.Loaded += (_, _) => text.ChangeView(null, text.ScrollableHeight, null, disableAnimation: true);
                break;
        }
    }

    private void RenderSources()
    {
        siteBinding.Synchronize(viewModel.Sources.OfType<LogsViewModel.SiteSource>());
        serviceBinding.Synchronize(viewModel.Sources.OfType<LogsViewModel.ServiceSource>());
        foreach (var pair in sourceButtons)
        {
            pair.Value.Background = pair.Key == viewModel.Selection
                ? Ui.Brush("SubtleFillColorSecondaryBrush")
                : Ui.Brush("CardBackgroundFillColorDefaultBrush");
        }
    }

    private static ListView Entries(IReadOnlyList<LogEntry> entries)
    {
        var list = new ListView { SelectionMode = ListViewSelectionMode.None, Padding = new Thickness(0, 4, 0, 4) };
        foreach (var entry in entries)
        {
            list.Items.Add(Entry(entry));
        }
        return list;
    }

    private static StackPanel Entry(LogEntry entry)
    {
        var level = new Border
        {
            Child = new TextBlock { Text = entry.Level.ToUpperInvariant(), FontSize = 10, FontWeight = FontWeights.Bold, Foreground = new SolidColorBrush(Microsoft.UI.Colors.White) },
            Background = new SolidColorBrush(LevelColor(entry.Level)),
            CornerRadius = new CornerRadius(4),
            Padding = new Thickness(6, 1, 6, 2),
        };
        var header = Ui.Row(8, level, Ui.Caption(entry.Time.ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss", System.Globalization.CultureInfo.InvariantCulture), wrap: false));
        if (entry.Env.Length > 0)
        {
            header.Children.Add(Ui.Caption(entry.Env, wrap: false));
        }

        var message = Ui.Text(entry.Message, selectable: true);
        message.MaxLines = 3;
        var panel = new StackPanel { Spacing = 4, Padding = new Thickness(0, 8, 0, 8), Children = { header, message } };

        if (entry.Details.Length > 0)
        {
            var details = new Expander
            {
                Header = "Details",
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch,
                Content = new ScrollViewer
                {
                    Content = Ui.MonoText(entry.Details, 12),
                    HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
                    MaxHeight = 360,
                },
            };
            details.Expanding += (_, _) => message.MaxLines = 0;
            details.Collapsed += (_, _) => message.MaxLines = 3;
            panel.Children.Add(details);
        }
        return panel;
    }

    private static Windows.UI.Color LevelColor(string level) => level switch
    {
        "debug" => Ui.Rgb(118, 118, 118),
        "info" => Ui.Rgb(0, 95, 184),
        "notice" => Ui.Rgb(0, 128, 128),
        "warning" => Ui.Rgb(157, 93, 0),
        _ => Ui.Rgb(196, 43, 28),
    };
}