using System.Globalization;
using Microsoft.UI;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.ApplicationModel.DataTransfer;
using Windows.UI;

namespace Flock.Tray;

/// <summary>
/// Small builders for the WinUI controls the windows are made of (the counterpart of the macOS
/// app's SwiftUI components): Fluent text styles, cards, badges, status dots, code blocks.
/// </summary>
internal static class Ui
{
    public static readonly FontFamily Mono = new("Cascadia Mono, Consolas");
    public static readonly FontFamily Symbols = new("Segoe Fluent Icons, Segoe MDL2 Assets");

    // Glyphs of Segoe Fluent Icons.
    public const string GlyphSettings = "\uE713";
    public const string GlyphGlobe = "\uE774";
    public const string GlyphCode = "\uE943";
    public const string GlyphMail = "\uE715";
    public const string GlyphBug = "\uEBE8";
    public const string GlyphInfo = "\uE946";
    public const string GlyphFolder = "\uE8B7";
    public const string GlyphWarning = "\uE7BA";
    public const string GlyphRefresh = "\uE72C";
    public const string GlyphDelete = "\uE74D";
    public const string GlyphAdd = "\uE710";
    public const string GlyphRemove = "\uE738";
    public const string GlyphCopy = "\uE8C8";
    public const string GlyphLock = "\uE72E";
    public const string GlyphMore = "\uE712";
    public const string GlyphDownload = "\uE896";
    public const string GlyphLightning = "\uE945";
    public const string GlyphTerminal = "\uE756";
    public const string GlyphServer = "\uE968";
    public const string GlyphDiagnostic = "\uE9D9";
    public const string GlyphDocument = "\uE8A5";
    public const string GlyphOpen = "\uE8A7";
    public const string GlyphAttach = "\uE723";
    public const string GlyphHistory = "\uE81C";
    public const string GlyphTray = "\uE7B8";
    public const string GlyphSearch = "\uE721";

    public static T Resource<T>(string key) => (T)Application.Current.Resources[key];

    public static Brush Brush(string key) => Resource<Brush>(key);

    public static Brush SecondaryText => Brush("TextFillColorSecondaryBrush");
    public static Brush TertiaryText => Brush("TextFillColorTertiaryBrush");
    public static Brush Success => Brush("SystemFillColorSuccessBrush");
    public static Brush Caution => Brush("SystemFillColorCautionBrush");
    public static Brush Critical => Brush("SystemFillColorCriticalBrush");
    public static Brush Accent => Brush("AccentTextFillColorPrimaryBrush");

    public static TextBlock Text(string text, string style = "BodyTextBlockStyle", Brush? foreground = null, bool wrap = true, bool selectable = false)
    {
        var block = new TextBlock
        {
            Text = text,
            Style = Resource<Style>(style),
            TextWrapping = wrap ? TextWrapping.Wrap : TextWrapping.NoWrap,
            TextTrimming = wrap ? TextTrimming.None : TextTrimming.CharacterEllipsis,
            IsTextSelectionEnabled = selectable,
        };
        if (foreground is not null)
        {
            block.Foreground = foreground;
        }
        return block;
    }

    public static TextBlock Caption(string text, Brush? foreground = null, bool wrap = true) =>
        Text(text, "CaptionTextBlockStyle", foreground ?? SecondaryText, wrap);

    public static TextBlock Strong(string text) => Text(text, "BodyStrongTextBlockStyle", wrap: false);

    public static TextBlock MonoText(string text, double size = 13) => new()
    {
        Text = text,
        FontFamily = Mono,
        FontSize = size,
        IsTextSelectionEnabled = true,
        TextWrapping = TextWrapping.NoWrap,
    };

    public static FontIcon Icon(string glyph, double size = 16, Brush? foreground = null)
    {
        var icon = new FontIcon { Glyph = glyph, FontFamily = Symbols, FontSize = size };
        if (foreground is not null)
        {
            icon.Foreground = foreground;
        }
        return icon;
    }

    public static StackPanel Row(double spacing, params UIElement[] children) => Stack(Orientation.Horizontal, spacing, children);

    public static StackPanel Column(double spacing, params UIElement[] children) => Stack(Orientation.Vertical, spacing, children);

    private static StackPanel Stack(Orientation orientation, double spacing, UIElement[] children)
    {
        var panel = new StackPanel { Orientation = orientation, Spacing = spacing };
        foreach (var child in children)
        {
            if (orientation == Orientation.Horizontal && child is FrameworkElement fe && fe.VerticalAlignment == VerticalAlignment.Stretch)
            {
                fe.VerticalAlignment = VerticalAlignment.Center;
            }
            panel.Children.Add(child);
            owners.AddOrUpdate(child, panel);
        }
        return panel;
    }

    // The panel Row/Column last put each element into. Once that panel has left the window (another
    // pane is shown), FrameworkElement.Parent reports null, yet the element still belongs to it and
    // can't be added anywhere else until it is removed.
    private static readonly System.Runtime.CompilerServices.ConditionalWeakTable<UIElement, Panel> owners = new();

    /// <summary>A grid row: content on the left (stretching), trailing elements on the right.</summary>
    public static Grid Spread(UIElement leading, params UIElement[] trailing)
    {
        var grid = new Grid { ColumnSpacing = 12 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.Children.Add(leading);
        if (leading is FrameworkElement l)
        {
            l.VerticalAlignment = VerticalAlignment.Center;
        }
        for (var i = 0; i < trailing.Length; i++)
        {
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            Grid.SetColumn((FrameworkElement)trailing[i], i + 1);
            ((FrameworkElement)trailing[i]).VerticalAlignment = VerticalAlignment.Center;
            grid.Children.Add(trailing[i]);
        }
        return grid;
    }

    public static Button Button(string text, Action onClick, bool accent = false, string? glyph = null, string? tooltip = null)
    {
        object content = glyph is null ? text : Row(8, Icon(glyph, 14), new TextBlock { Text = text });
        var button = new Button { Content = content };
        if (accent)
        {
            button.Style = Resource<Style>("AccentButtonStyle");
        }
        button.Click += (_, _) => onClick();
        if (tooltip is not null)
        {
            ToolTipService.SetToolTip(button, tooltip);
        }
        return button;
    }

    /// <summary>A borderless icon button, like the macOS app's .borderless buttons.</summary>
    public static Button IconButton(string glyph, string tooltip, Action onClick)
    {
        var button = new Button
        {
            Content = Icon(glyph, 14),
            Padding = new Thickness(8, 6, 8, 6),
            Background = new SolidColorBrush(Colors.Transparent),
            BorderThickness = new Thickness(0),
        };
        ToolTipService.SetToolTip(button, tooltip);
        AutomationName(button, tooltip);
        button.Click += (_, _) => onClick();
        return button;
    }

    public static HyperlinkButton Link(string text, Action onClick)
    {
        var link = new HyperlinkButton { Content = text, Padding = new Thickness(4, 2, 4, 2) };
        link.Click += (_, _) => onClick();
        return link;
    }

    public static void AutomationName(DependencyObject element, string name) =>
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(element, name);

    /// <summary>A Windows 11 settings card: a rounded, slightly raised surface.</summary>
    public static Border Card(UIElement content, Thickness? padding = null) => new()
    {
        Child = content,
        Padding = padding ?? new Thickness(16, 12, 16, 12),
        Background = Brush("CardBackgroundFillColorDefaultBrush"),
        BorderBrush = Brush("CardStrokeColorDefaultBrush"),
        BorderThickness = new Thickness(1),
        CornerRadius = new CornerRadius(6),
    };

    /// <summary>One setting: header and description on the left, its control on the right.</summary>
    public static Border Setting(string header, string? description, UIElement? control, string? glyph = null)
    {
        var text = Column(1, Text(header, wrap: true));
        if (description is not null)
        {
            text.Children.Add(Caption(description));
        }
        UIElement leading = glyph is null ? text : Row(16, Icon(glyph, 18), text);
        return Card(control is null ? leading : Spread(leading, control));
    }

    /// <summary>A group of cards under a small heading, like the sections of Windows Settings.</summary>
    public static StackPanel Section(string? title, params UIElement[] cards)
    {
        var panel = new StackPanel { Spacing = 4, Margin = new Thickness(0, 0, 0, 24) };
        if (title is not null)
        {
            panel.Children.Add(new TextBlock
            {
                Text = title,
                Style = Resource<Style>("BodyStrongTextBlockStyle"),
                Margin = new Thickness(1, 0, 0, 6),
            });
        }
        foreach (var card in cards)
        {
            panel.Children.Add(card);
        }
        return panel;
    }

    /// <summary>Green when running, gray when stopped.</summary>
    public static Ellipse StatusDot(bool on) => new()
    {
        Width = 8,
        Height = 8,
        Fill = on ? Success : Brush("ControlStrongFillColorDisabledBrush"),
        VerticalAlignment = VerticalAlignment.Center,
    };

    public enum Tone { Accent, Neutral, Caution, Critical }

    /// <summary>A small capsule label such as "Default".</summary>
    public static Border Badge(string text, Tone tone = Tone.Accent)
    {
        var (fg, bg) = tone switch
        {
            Tone.Caution => (Caution, Brush("SystemFillColorCautionBackgroundBrush")),
            Tone.Critical => (Critical, Brush("SystemFillColorCriticalBackgroundBrush")),
            Tone.Neutral => (SecondaryText, Brush("ControlFillColorSecondaryBrush")),
            _ => (Accent, Brush("SubtleFillColorSecondaryBrush")),
        };
        return new Border
        {
            Child = new TextBlock { Text = text, FontSize = 11, FontWeight = FontWeights.SemiBold, Foreground = fg },
            Background = bg,
            CornerRadius = new CornerRadius(8),
            Padding = new Thickness(7, 1, 7, 2),
            VerticalAlignment = VerticalAlignment.Center,
        };
    }

    /// <summary>A monospaced, selectable block of text with a copy button (environment snippets).</summary>
    public static Grid CodeBlock(string text)
    {
        var grid = new Grid();
        grid.Children.Add(new Border
        {
            Child = new ScrollViewer
            {
                Content = MonoText(text.Length == 0 ? "—" : text, 12),
                HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
                VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            },
            Padding = new Thickness(10, 8, 40, 8),
            Background = Brush("ControlFillColorSecondaryBrush"),
            BorderBrush = Brush("ControlStrokeColorDefaultBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(4),
        });
        if (text.Length > 0)
        {
            var copy = IconButton(GlyphCopy, "Copy", () => Copy(text));
            copy.HorizontalAlignment = HorizontalAlignment.Right;
            copy.VerticalAlignment = VerticalAlignment.Top;
            copy.Margin = new Thickness(0, 2, 2, 0);
            grid.Children.Add(copy);
        }
        return grid;
    }

    /// <summary>Removes a control from its panel so it can be placed in a newly rendered one.</summary>
    public static T Detach<T>(T element) where T : FrameworkElement
    {
        if (owners.TryGetValue(element, out var owner))
        {
            owner.Children.Remove(element);
            owners.Remove(element);
        }
        if (element.Parent is Panel panel)
        {
            panel.Children.Remove(element);
        }
        return element;
    }

    public static void Copy(string text)
    {
        var package = new DataPackage();
        package.SetText(text);
        Clipboard.SetContent(package);
    }

    /// <summary>A centered placeholder for empty lists ("No mail").</summary>
    public static StackPanel EmptyState(string glyph, string title, string? description = null, UIElement? action = null)
    {
        var panel = Column(8, Icon(glyph, 40, TertiaryText), Text(title, "SubtitleTextBlockStyle"));
        panel.HorizontalAlignment = HorizontalAlignment.Center;
        panel.VerticalAlignment = VerticalAlignment.Center;
        panel.MaxWidth = 420;
        panel.Padding = new Thickness(24);
        foreach (var child in panel.Children.OfType<FrameworkElement>())
        {
            child.HorizontalAlignment = HorizontalAlignment.Center;
        }
        if (description is not null)
        {
            var caption = Caption(description);
            caption.TextAlignment = TextAlignment.Center;
            caption.HorizontalAlignment = HorizontalAlignment.Center;
            panel.Children.Add(caption);
        }
        if (action is FrameworkElement a)
        {
            a.HorizontalAlignment = HorizontalAlignment.Center;
            a.Margin = new Thickness(0, 8, 0, 0);
            panel.Children.Add(a);
        }
        return panel;
    }

    /// <summary>The red/orange strip under a window when the last request failed.</summary>
    public static InfoBar ErrorBar(Action? dismiss = null)
    {
        var bar = new InfoBar
        {
            Severity = InfoBarSeverity.Error,
            IsClosable = dismiss is not null,
            IsOpen = false,
            Margin = new Thickness(12, 0, 12, 12),
        };
        if (dismiss is not null)
        {
            bar.CloseButtonClick += (_, _) => dismiss();
        }
        return bar;
    }

    public static void ShowError(InfoBar bar, string? message)
    {
        bar.Message = message ?? "";
        bar.IsOpen = message is not null;
    }

    /// <summary>"3 minutes ago", "yesterday", like SwiftUI's relative date format.</summary>
    public static string Relative(DateTimeOffset date)
    {
        var span = DateTimeOffset.Now - date;
        return span.TotalSeconds switch
        {
            < 45 => "just now",
            < 90 => "a minute ago",
            < 3600 => $"{(int)Math.Round(span.TotalMinutes)} minutes ago",
            < 5400 => "an hour ago",
            < 86400 => $"{(int)Math.Round(span.TotalHours)} hours ago",
            < 172800 => "yesterday",
            _ => date.ToLocalTime().ToString("d", CultureInfo.CurrentCulture),
        };
    }

    public static string Bytes(long size) => size switch
    {
        < 1024 => $"{size} bytes",
        < 1024 * 1024 => $"{size / 1024.0:0.#} KB",
        _ => $"{size / (1024.0 * 1024):0.#} MB",
    };

    public static Color Rgb(byte r, byte g, byte b) => Color.FromArgb(255, r, g, b);
}
