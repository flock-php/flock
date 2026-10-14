using Flock.Kit;
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
    private readonly LogStore store;
    private readonly ListView sources = new() { SelectionMode = ListViewSelectionMode.Single };
    private readonly List<LogStore.Source?> rows = [];
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
    private LogStore.Source? selection;
    // What the detail shows now; an auto-refresh that changes nothing leaves it (and its scroll position) alone.
    private (LogStore.Source? Source, object? Data, string Query, LogLevelFilter Level)? rendered;
    private CancellationTokenSource? polling;

    public LogsWindow(AppModel model) : base("Logs", 1000, 660)
    {
        store = model.Logs;

        var refresh = new AppBarButton { Icon = new SymbolIcon(Symbol.Refresh), Label = "Refresh" };
        refresh.Click += async (_, _) =>
        {
            await store.LoadIndexAsync();
            await Reload();
        };
        clear.Click += async (_, _) =>
        {
            if (selection is LogStore.SiteSource site
                && await Confirm($"Clear the log of {site.Name}?", $"The Laravel log files in storage\\logs of {site.Name} are emptied. This can't be undone.", "Clear", destructive: true))
            {
                await store.ClearAsync(site.Name);
            }
        };
        autoRefresh.Click += (_, _) =>
        {
            polling?.Cancel();
            polling = autoRefresh.IsChecked == true ? new CancellationTokenSource() : null;
            if (polling is not null)
            {
                _ = Poll(polling.Token);
            }
        };
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
        levels.SelectionChanged += (_, _) => Render();
        ToolTipService.SetToolTip(levels, "Levels of a site's Laravel log to show");
        Ui.AutomationName(levels, "Level");
        search.TextChanged += (_, _) => Render();
        var filters = Ui.Row(8, search, levels);
        filters.Margin = new Thickness(16, 0, 0, 0);

        sources.SelectionChanged += async (_, _) =>
        {
            var index = sources.SelectedIndex;
            if (index < 0 || index >= rows.Count || rows[index] is not { } source || source == selection)
            {
                return;
            }
            selection = source;
            clear.Visibility = source is LogStore.SiteSource ? Visibility.Visible : Visibility.Collapsed;
            levels.IsEnabled = source is LogStore.SiteSource;
            rendered = null;
            content.Content = new ProgressRing { Width = 32, Height = 32 };
            await Reload();
        };
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
        split.Children.Add(sources);
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

        store.Changed += Render;
        Closed += (_, _) =>
        {
            polling?.Cancel();
            store.Changed -= Render;
        };
        _ = store.LoadIndexAsync();
    }

    private LogLevelFilter Level => levels.SelectedIndex >= 0 ? LogFilter.Choices[levels.SelectedIndex].Filter : LogLevelFilter.All;

    /// <summary>Reloads the selected log every 2 seconds while the window is shown (not minimized or hidden).</summary>
    private async Task Poll(CancellationToken stop)
    {
        while (!stop.IsCancellationRequested)
        {
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(2), stop);
            }
            catch (TaskCanceledException)
            {
                return;
            }
            var minimized = AppWindow.Presenter is Microsoft.UI.Windowing.OverlappedPresenter { State: Microsoft.UI.Windowing.OverlappedPresenterState.Minimized };
            if (Visible && !minimized && !stop.IsCancellationRequested)
            {
                await Reload();
            }
        }
    }

    private async Task Reload()
    {
        if (selection is not null)
        {
            await store.LoadAsync(selection);
        }
    }

    private void Render()
    {
        Ui.ShowError(error, store.Error);
        RenderSources();

        var query = search.Text ?? "";
        var key = (selection, selection is LogStore.ServiceSource ? store.ServiceText : (object)store.Entries, query, Level);
        if (selection is null || (rendered is { } shown && shown.Equals(key)))
        {
            return;
        }
        rendered = key;

        switch (selection)
        {
            case LogStore.SiteSource site:
                var entries = LogFilter.Filter(store.Entries, query, Level);
                content.Content = store.Entries.Count == 0
                    ? Ui.EmptyState(Ui.GlyphDocument, "No log entries", $"storage\\logs of {site.Name} is empty.")
                    : entries.Count == 0
                        ? Ui.EmptyState(Ui.GlyphSearch, "No matching log entries", "Nothing in the latest entries matches the search and level.")
                        : Entries(entries);
                break;
            case LogStore.ServiceSource:
                var lines = LogFilter.FilterLines(store.ServiceText, query);
                var text = MailDetailView.Monospaced(store.ServiceText.Length == 0 ? "(empty)" : lines.Length == 0 ? "(no matching lines)" : lines);
                content.Content = text;
                // Logs grow at the bottom: start there.
                text.Loaded += (_, _) => text.ChangeView(null, text.ScrollableHeight, null, disableAnimation: true);
                break;
        }
    }

    private void RenderSources()
    {
        var wanted = new List<LogStore.Source?> { null };
        wanted.AddRange(store.Index.Sites.Select(s => (LogStore.Source?)new LogStore.SiteSource(s)));
        wanted.Add(null);
        wanted.AddRange(store.Index.Services.Select(s => (LogStore.Source?)new LogStore.ServiceSource(s)));
        if (wanted.SequenceEqual(rows))
        {
            return;
        }

        rows.Clear();
        rows.AddRange(wanted);
        sources.Items.Clear();
        var headers = 0;
        foreach (var row in rows)
        {
            if (row is null)
            {
                var header = Ui.Caption(headers++ == 0 ? "SITES" : "SERVICES");
                header.FontWeight = FontWeights.SemiBold;
                header.Margin = new Thickness(0, 10, 0, 2);
                sources.Items.Add(new ListViewItem { Content = header, IsEnabled = false, MinHeight = 24 });
                continue;
            }
            var glyph = row is LogStore.SiteSource ? Ui.GlyphGlobe : Ui.GlyphSettings;
            sources.Items.Add(Ui.Row(10, Ui.Icon(glyph, 14), Ui.Text(row.Name, wrap: false)));
        }
        sources.SelectedIndex = selection is null ? -1 : rows.IndexOf(selection);
    }

    private static ListView Entries(List<LogEntry> entries)
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
