using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Storage.Pickers;

namespace Flock.Tray;

/// <summary>Caught mail: the list on the left, the selected message on the right.</summary>
internal sealed partial class MailWindow : FlockWindow
{
    private readonly MailStore store;
    private readonly ListView list = new() { SelectionMode = ListViewSelectionMode.Single };
    private readonly ContentControl detail = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly Grid listArea = new();
    private readonly InfoBar error = Ui.ErrorBar();
    private readonly AppBarButton clear;
    private List<MailSummary> shown = [];
    private string? shownEnv;
    private string? selected;

    public MailWindow(AppModel model) : base("Mail", 1000, 660)
    {
        store = model.Mail;

        var refresh = new AppBarButton { Icon = new SymbolIcon(Symbol.Refresh), Label = "Refresh" };
        refresh.Click += (_, _) => _ = store.RefreshAsync();
        clear = new AppBarButton { Icon = new SymbolIcon(Symbol.Delete), Label = "Clear All" };
        clear.Click += async (_, _) => await ConfirmClear();
        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        toolbar.PrimaryCommands.Add(refresh);
        toolbar.PrimaryCommands.Add(clear);

        list.SelectionChanged += (_, _) =>
        {
            if (list.SelectedIndex >= 0 && list.SelectedIndex < shown.Count && shown[list.SelectedIndex].Id != selected)
            {
                selected = shown[list.SelectedIndex].Id;
                ShowDetail(selected);
            }
        };
        listArea.Children.Add(list);

        var split = new Grid { ColumnSpacing = 0 };
        split.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(330) });
        split.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var detailCard = new Border
        {
            Child = detail,
            Background = Ui.Brush("LayerFillColorDefaultBrush"),
            BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
            BorderThickness = new Thickness(1, 1, 0, 0),
            CornerRadius = new CornerRadius(8, 0, 0, 0),
        };
        Grid.SetColumn(detailCard, 1);
        split.Children.Add(listArea);
        split.Children.Add(detailCard);
        detail.Content = NoSelection();

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
        Ui.SettingsShortcut(root);

        store.Changed += Render;
        store.IsVisible = true;
        Closed += (_, _) =>
        {
            store.IsVisible = false;
            store.Changed -= Render;
        };
        Render();
        _ = store.RefreshAsync();
    }

    private void Render()
    {
        Ui.ShowError(error, store.Error);
        clear.IsEnabled = store.Messages.Count > 0;

        if (!Json.Same(shown, store.Messages))
        {
            shown = store.Messages;
            list.Items.Clear();
            foreach (var message in shown)
            {
                list.Items.Add(Row(message));
            }
            var index = shown.FindIndex(m => m.Id == selected);
            list.SelectedIndex = index;
            if (index < 0 && selected is not null)
            {
                selected = null;
                detail.Content = NoSelection();
            }
        }
        // The .env lines arrive with the first refresh, or change with the port.
        if (selected is null && store.Env != shownEnv)
        {
            detail.Content = NoSelection();
        }

        listArea.Children.Clear();
        listArea.Children.Add(list);
        if (shown.Count == 0)
        {
            listArea.Children.Add(Ui.EmptyState(Ui.GlyphMail, "No mail"));
        }
    }

    /// <summary>
    /// While no message is selected: what a project's .env needs for Flock to catch its mail, with a
    /// copy button.
    /// </summary>
    private StackPanel NoSelection()
    {
        shownEnv = store.Env;
        if (store.Env.Length == 0)
        {
            return Ui.EmptyState(Ui.GlyphMail, "No message selected");
        }
        var note = Ui.Caption("MAIL_USERNAME picks the inbox, so every project gets its own.");
        note.TextAlignment = TextAlignment.Center;
        return Ui.EmptyState(Ui.GlyphMail, "Catch your app's mail", "Add these lines to your project's .env:",
            Ui.Column(8, Ui.CodeBlock(store.Env), note));
    }

    private static StackPanel Row(MailSummary message)
    {
        var from = Ui.Text(message.From, "BodyStrongTextBlockStyle", wrap: false);
        var header = Ui.Spread(from, Ui.Caption(Ui.Relative(message.Date), wrap: false));
        var subject = Ui.Text(message.Subject.Length == 0 ? "(no subject)" : message.Subject, wrap: false);
        var to = Ui.Caption(string.Join(", ", message.To), wrap: false);
        return new StackPanel
        {
            Spacing = 2,
            Padding = new Thickness(0, 6, 0, 6),
            Children = { header, subject, Ui.Row(6, Ui.Badge(message.Inbox, Ui.Tone.Neutral), to) },
        };
    }

    private async Task ConfirmClear()
    {
        var dialog = new ContentDialog
        {
            Title = "Delete all caught mail?",
            PrimaryButtonText = "Delete All",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Content.XamlRoot,
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary)
        {
            selected = null;
            detail.Content = NoSelection();
            await store.ClearAsync();
        }
    }

    private async void ShowDetail(string id)
    {
        detail.Content = new ProgressRing { Width = 32, Height = 32 };
        try
        {
            var message = await store.DetailAsync(id);
            if (selected == id)
            {
                detail.Content = new MailDetailView(this, store, message);
            }
        }
        catch (FlockException e)
        {
            if (selected == id)
            {
                detail.Content = Ui.EmptyState(Ui.GlyphWarning, "Could not load message", e.Message);
            }
        }
    }

    /// <summary>Asks where to save an attachment, then downloads it there.</summary>
    public async Task SaveAttachment(MailDetail message, MailDetail.Attachment attachment)
    {
        var picker = new FileSavePicker { SuggestedFileName = attachment.Filename.Length == 0 ? "attachment" : attachment.Filename };
        var extension = System.IO.Path.GetExtension(attachment.Filename);
        picker.FileTypeChoices.Add(extension.Length > 0 ? extension.TrimStart('.').ToUpperInvariant() + " file" : "File",
            [extension.Length > 0 ? extension : ".bin"]);
        WinRT.Interop.InitializeWithWindow.Initialize(picker, Hwnd);
        var file = await picker.PickSaveFileAsync();
        if (file is null)
        {
            return;
        }
        try
        {
            var data = await store.AttachmentAsync(message.Id, attachment.Index);
            await File.WriteAllBytesAsync(file.Path, data);
        }
        catch (Exception e) when (e is FlockException or IOException or UnauthorizedAccessException)
        {
            store.Error = e.Message;
            Render();
        }
    }
}

/// <summary>One message: headers, attachments and the HTML / Text / Raw / Headers views.</summary>
internal sealed partial class MailDetailView : Grid
{
    private readonly MailStore store;
    private readonly MailDetail message;
    private readonly ContentControl content = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private HtmlView? html;
    private string? raw;

    public MailDetailView(MailWindow window, MailStore store, MailDetail message)
    {
        this.store = store;
        this.message = message;
        RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });

        var head = new StackPanel { Spacing = 8, Padding = new Thickness(20, 16, 20, 12) };
        var subject = Ui.Text(message.Subject.Length == 0 ? "(no subject)" : message.Subject, "SubtitleTextBlockStyle", selectable: true);
        head.Children.Add(subject);

        var fields = new Grid { ColumnSpacing = 12, RowSpacing = 2 };
        fields.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        fields.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var rows = new List<(string, string)> { ("From", message.From), ("To", string.Join(", ", message.To)) };
        if (message.Cc.Count > 0)
        {
            rows.Add(("Cc", string.Join(", ", message.Cc)));
        }
        rows.Add(("Date", message.Date.ToLocalTime().ToString("f", System.Globalization.CultureInfo.CurrentCulture)));
        rows.Add(("Inbox", message.Inbox));
        for (var i = 0; i < rows.Count; i++)
        {
            fields.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            var name = Ui.Text(rows[i].Item1, foreground: Ui.SecondaryText, wrap: false);
            var value = Ui.Text(rows[i].Item2, selectable: true);
            Grid.SetRow(name, i);
            Grid.SetRow(value, i);
            Grid.SetColumn(value, 1);
            fields.Children.Add(name);
            fields.Children.Add(value);
        }
        head.Children.Add(fields);

        if (message.Attachments.Count > 0)
        {
            var attachments = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
            foreach (var attachment in message.Attachments)
            {
                attachments.Children.Add(Ui.Button($"{attachment.Filename} ({Ui.Bytes(attachment.Size)})",
                    () => _ = window.SaveAttachment(message, attachment), glyph: Ui.GlyphAttach, tooltip: "Save…"));
            }
            head.Children.Add(new ScrollViewer
            {
                Content = attachments,
                HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
                VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            });
        }

        var modes = new SelectorBar();
        foreach (var mode in new[] { "HTML", "Text", "Raw", "Headers" })
        {
            modes.Items.Add(new SelectorBarItem { Text = mode, Tag = mode });
        }
        modes.SelectionChanged += (_, _) => Show((string)modes.SelectedItem.Tag);
        head.Children.Add(modes);

        Children.Add(head);
        var body = new Border
        {
            Child = content,
            BorderBrush = Ui.Brush("DividerStrokeColorDefaultBrush"),
            BorderThickness = new Thickness(0, 1, 0, 0),
        };
        SetRow(body, 1);
        Children.Add(body);

        modes.SelectedItem = modes.Items[message.Html.Length == 0 ? 1 : 0];
    }

    private async void Show(string mode)
    {
        switch (mode)
        {
            case "HTML":
                if (message.Html.Length == 0)
                {
                    content.Content = Ui.EmptyState(Ui.GlyphDocument, "No HTML part");
                    break;
                }
                html ??= new HtmlView(allowScripts: false, blockRemote: true, whiteBackground: true);
                html.Load(message.Html);
                content.Content = html;
                break;
            case "Text":
                content.Content = Monospaced(message.Text.Length == 0 ? "(no text part)" : message.Text);
                break;
            case "Raw":
                if (raw is null)
                {
                    content.Content = new ProgressRing { Width = 32, Height = 32 };
                    try
                    {
                        raw = await store.RawAsync(message.Id);
                    }
                    catch (FlockException e)
                    {
                        raw = e.Message;
                    }
                }
                content.Content = Monospaced(raw);
                break;
            default:
                content.Content = Headers();
                break;
        }
    }

    /// <summary>Scrollable, selectable monospaced text.</summary>
    public static ScrollViewer Monospaced(string text) => new()
    {
        Content = new TextBlock
        {
            Text = text,
            FontFamily = Ui.Mono,
            FontSize = 13,
            IsTextSelectionEnabled = true,
            Margin = new Thickness(16, 12, 16, 12),
        },
        HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        HorizontalScrollMode = ScrollMode.Auto,
    };

    private ScrollViewer Headers()
    {
        var grid = new Grid { ColumnSpacing = 16, RowSpacing = 6, Margin = new Thickness(16, 12, 16, 12) };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(200) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        for (var i = 0; i < message.Headers.Count; i++)
        {
            grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            var name = Ui.Text(message.Headers[i].Name, foreground: Ui.SecondaryText, selectable: true);
            var value = Ui.Text(message.Headers[i].Value, selectable: true);
            Grid.SetRow(name, i);
            Grid.SetRow(value, i);
            Grid.SetColumn(value, 1);
            grid.Children.Add(name);
            grid.Children.Add(value);
        }
        return new ScrollViewer { Content = grid };
    }
}
