using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>Caught mail: the list on the left, the selected message on the right.</summary>
internal sealed partial class MailWindow : FlockWindow
{
    private readonly MailViewModel viewModel;
    private readonly ListView list = new() { SelectionMode = ListViewSelectionMode.Single };
    private readonly ContentControl detail = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly Grid listArea = new();
    private readonly InfoBar error = Ui.ErrorBar();
    private readonly AppBarButton clear;
    private MailDetailView? detailView;
    private bool rendering;

    private readonly Task initialRefresh;

    internal void SelectFirst() => list.SelectedIndex = 0;

    public MailWindow(MailViewModel viewModel) : base("Mail", 1000, 660)
    {
        this.viewModel = viewModel;

        var refresh = new AppBarButton { Icon = new SymbolIcon(Symbol.Refresh), Label = "Refresh" };
        refresh.Click += async (_, _) => await viewModel.RefreshAsync();
        clear = new AppBarButton { Icon = new SymbolIcon(Symbol.Delete), Label = "Clear All" };
        clear.Click += async (_, _) => await ConfirmClearAsync();
        var toolbar = new CommandBar { DefaultLabelPosition = CommandBarDefaultLabelPosition.Right, HorizontalAlignment = HorizontalAlignment.Right };
        toolbar.PrimaryCommands.Add(refresh);
        toolbar.PrimaryCommands.Add(clear);

        list.SelectionChanged += async (_, _) =>
        {
            if (!rendering)
            {
                await ShowDetailAsync((list.SelectedItem as ListViewItem)?.Tag as string);
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

        viewModel.Changed += Render;
        Closed += (_, _) => viewModel.Changed -= Render;
        Render();
        initialRefresh = viewModel.RefreshAsync();
    }

    private void Render()
    {
        Ui.ShowError(error, viewModel.Error);
        clear.IsEnabled = viewModel.CanClear;

        rendering = true;
        list.Items.Clear();
        foreach (var message in viewModel.Messages)
        {
            list.Items.Add(new ListViewItem { Content = Row(message), Tag = message.Id });
        }
        list.SelectedItem = list.Items.Cast<ListViewItem>().FirstOrDefault(row => (string)row.Tag == viewModel.SelectedId);
        rendering = false;
        if (viewModel.SelectedId is null)
        {
            detail.Content = NoSelection();
        }

        listArea.Children.Clear();
        listArea.Children.Add(list);
        if (viewModel.Messages.Count == 0)
        {
            listArea.Children.Add(Ui.EmptyState(Ui.GlyphMail, "No mail", $"Send mail to {viewModel.SmtpAddress}. MAIL_USERNAME picks the inbox."));
        }
    }

    /// <summary>
    /// While no message is selected: what a project's .env needs for Flock to catch its mail, with a
    /// copy button.
    /// </summary>
    private StackPanel NoSelection()
    {
        if (viewModel.Env.Length == 0)
        {
            return Ui.EmptyState(Ui.GlyphMail, "No message selected");
        }
        var note = Ui.Caption("MAIL_USERNAME picks the inbox, so every project gets its own.");
        note.TextAlignment = TextAlignment.Center;
        return Ui.EmptyState(Ui.GlyphMail, "Catch your app's mail", "Add these lines to your project's .env:",
            Ui.Column(8, Ui.CodeBlock(viewModel.Env), note));
    }

    private static StackPanel Row(MailViewModel.MailRow message)
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

    private async Task ConfirmClearAsync()
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
            await viewModel.SelectAsync(null);
            detail.Content = NoSelection();
            await viewModel.ClearCommand.ExecuteAsync(null);
        }
    }

    private async Task ShowDetailAsync(string? id)
    {
        var selection = viewModel.SelectAsync(id);
        detail.Content = new ProgressRing { Width = 32, Height = 32 };
        if (detailView is not null)
        {
            await detailView.DisposeAsync();
            detailView = null;
        }
        await selection;
        if (id is null || viewModel.SelectedId != id)
        {
            if (viewModel.SelectedId == id)
            {
                detail.Content = NoSelection();
            }
            return;
        }
        if (viewModel.Detail.Current is null)
        {
            detail.Content = Ui.EmptyState(Ui.GlyphWarning, "Could not load message", viewModel.Detail.Error);
            return;
        }
        detailView = new MailDetailView(Hwnd, viewModel.Detail, viewModel.Detail.Current, viewModel.Model.Shell);
        detail.Content = detailView;
    }

    public override async Task ReleaseAsync()
    {
        if (detailView is not null)
        {
            await detailView.DisposeAsync();
            detailView = null;
        }
        await initialRefresh;
        await viewModel.DisposeAsync();
    }
}

/// <summary>One message: headers, attachments and the HTML / Text / Raw / Headers views.</summary>
internal sealed partial class MailDetailView : Grid, IAsyncDisposable
{
    private readonly IntPtr owner;
    private readonly MailDetailViewModel viewModel;
    private readonly IShellLauncher shell;
    private readonly MailDetail message;
    private readonly ContentControl content = new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
    };
    private readonly SelectorBar modes = new();
    private HtmlView? html;
    private Task htmlLoad = Task.CompletedTask;
    private bool released;

    public MailDetailView(IntPtr owner, MailDetailViewModel viewModel, MailDetail message, IShellLauncher shell)
    {
        this.owner = owner;
        this.viewModel = viewModel;
        this.message = message;
        this.shell = shell;
        viewModel.PropertyChanged += ViewModelChanged;
        RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });

        var head = new StackPanel { Spacing = 8, Padding = new Thickness(20, 16, 20, 12) };
        head.Children.Add(Ui.Text(message.Subject.Length == 0 ? "(no subject)" : message.Subject, "SubtitleTextBlockStyle", selectable: true));

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
                attachments.Children.Add(Ui.AsyncButton($"{attachment.Filename} ({Ui.Bytes(attachment.Size)})",
                    () => viewModel.SaveAttachmentAsync(attachment, owner), glyph: Ui.GlyphAttach, tooltip: "Save…"));
            }
            head.Children.Add(new ScrollViewer
            {
                Content = attachments,
                HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
                VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            });
        }

        foreach (var mode in new[] { "HTML", "Text", "Raw", "Headers" })
        {
            modes.Items.Add(new SelectorBarItem { Text = mode, Tag = mode });
        }
        modes.SelectionChanged += async (_, _) =>
        {
            if (modes.SelectedItem is SelectorBarItem item && item.Tag is string mode)
            {
                await SetModeAsync(mode);
            }
        };
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
        RenderMode();
    }

    public async ValueTask DisposeAsync()
    {
        if (released)
        {
            return;
        }
        released = true;
        viewModel.PropertyChanged -= ViewModelChanged;
        if (html is not null)
        {
            await htmlLoad;
            await html.ReleaseAsync();
            html = null;
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

    private async Task SetModeAsync(string mode)
    {
        await viewModel.SetModeAsync(mode);
        RenderMode();
    }

    private void ViewModelChanged(object? sender, System.ComponentModel.PropertyChangedEventArgs args)
    {
        if (args.PropertyName is nameof(MailDetailViewModel.Raw) or nameof(MailDetailViewModel.RawError) or
            nameof(MailDetailViewModel.IsRawLoading) or nameof(MailDetailViewModel.Error))
        {
            RenderMode();
        }
    }

    private void RenderMode()
    {
        if (released)
        {
            return;
        }
        switch (viewModel.Mode)
        {
            case "HTML":
                if (message.Html.Length == 0)
                {
                    content.Content = Ui.EmptyState(Ui.GlyphDocument, "No HTML part");
                    break;
                }
                html ??= new HtmlView(allowScripts: false, blockRemote: true, whiteBackground: true, shell: shell);
                htmlLoad = html.LoadAsync(message.Html);
                content.Content = html;
                break;
            case "Text":
                content.Content = Monospaced(message.Text.Length == 0 ? "(no text part)" : message.Text);
                break;
            case "Raw":
                if (viewModel.IsRawLoading)
                {
                    content.Content = new ProgressRing { Width = 32, Height = 32 };
                }
                else if (viewModel.RawError is { } error)
                {
                    content.Content = Ui.EmptyState(Ui.GlyphWarning, "Could not load raw message", error,
                        Ui.AsyncButton("Retry", () => SetModeAsync("Raw"), accent: true));
                }
                else
                {
                    content.Content = Monospaced(viewModel.Raw ?? "(empty)");
                }
                break;
            default:
                content.Content = Headers();
                break;
        }
    }

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
