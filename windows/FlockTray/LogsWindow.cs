using Flock.Kit;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray;

/// <summary>Laravel logs of every site and the logs of Flock's services.</summary>
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
    private readonly InfoBar error = Ui.ErrorBar();
    private LogStore.Source? selection;

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
            if (selection is LogStore.SiteSource site)
            {
                await store.ClearAsync(site.Name);
            }
        };
        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        toolbar.PrimaryCommands.Add(refresh);
        toolbar.PrimaryCommands.Add(clear);

        sources.SelectionChanged += async (_, _) =>
        {
            var index = sources.SelectedIndex;
            if (index < 0 || index >= rows.Count || rows[index] is not { } source || source == selection)
            {
                return;
            }
            selection = source;
            clear.Visibility = source is LogStore.SiteSource ? Visibility.Visible : Visibility.Collapsed;
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
        root.Children.Add(toolbar);
        root.Children.Add(split);
        root.Children.Add(error);
        Body = root;

        store.Changed += Render;
        Closed += (_, _) => store.Changed -= Render;
        _ = store.LoadIndexAsync();
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

        switch (selection)
        {
            case LogStore.SiteSource site:
                content.Content = store.Entries.Count == 0
                    ? Ui.EmptyState(Ui.GlyphDocument, "No log entries", $"storage\\logs of {site.Name} is empty.")
                    : Entries(store.Entries);
                break;
            case LogStore.ServiceSource:
                var text = MailDetailView.Monospaced(store.ServiceText.Length == 0 ? "(empty)" : store.ServiceText);
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
