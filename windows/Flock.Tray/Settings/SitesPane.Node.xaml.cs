using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
namespace Flock.Tray.Settings;

/// <summary>
///     What Flock runs for a site or proxy: a Node.js site's dev server (started on the site's first
///     visit, asleep after 30 idle minutes) and a proxy's start command, each with its state, Start
///     and Stop, and its output.
/// </summary>
internal sealed partial class SitesPane
{
    /// <summary>The site's type: PHP, or a Node.js app whose dev server Flock runs.</summary>
    private ComboBox TypePicker(Status.Site site)
    {
        var node = site.IsNode;
        var type = new ComboBox { MinWidth = 140 };
        type.Items.Add(new ComboBoxItem { Content = "PHP" });
        type.Items.Add(new ComboBoxItem { Content = "Node.js" });
        type.SelectedIndex = node ? 1 : 0;
        type.SelectionChanged += (_, _) =>
        {
            if (type.SelectedIndex < 0 || type.SelectedIndex == 1 == node)
            {
                return;
            }
            var change = type.SelectedIndex == 1 ? Status.SiteType.Node : Status.SiteType.Php;
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Type: change), null), false);
        };
        Ui.AutomationName(type, Strings.Sites_Type);
        return type;
    }

    /// <summary>A Node.js site's dev server: its state, Start, Stop and Restart, the command and its output.</summary>
    private StackPanel DevServer(Status.Site site)
    {
        var section = Section(Strings.Sites_DevServer);
        if (site.NodeServer is not { } server)
        {
            section.Children.Add(Ui.Caption(Strings.Sites_DevServerFirstVisit, Ui.TertiaryText));
            return section;
        }
        var tone = server.State switch
        {
            Status.NodeServerState.Running => Ui.Tone.Success,
            Status.NodeServerState.Starting => Ui.Tone.Caution,
            Status.NodeServerState.Failed => Ui.Tone.Critical,
            _ => Ui.Tone.Neutral,
        };
        section.Children.Add(Field(Strings.Sites_State, StateLine(tone, server.StateLabel,
            server.State != Status.NodeServerState.Failed ? server.Problem : "")));
        var command = Ui.MonoText(server.Command, 12);
        command.Foreground = Ui.SecondaryText;
        section.Children.Add(Field(Strings.Sites_Command, command));

        var buttons = Ui.Row(8);
        if (server.CanStart)
        {
            buttons.Children.Add(Ui.Button(Strings.Services_Start, () => Start(site.Name, () => Model.Sites.RestartAsync(site.Name))));
        }
        if (server.CanStop)
        {
            buttons.Children.Add(Ui.Button(Strings.Share_Stop, () => Start(site.Name, () => Model.Sites.StopSiteAsync(site.Name))));
        }
        if (server.CanRestart)
        {
            buttons.Children.Add(Ui.Button(Strings.Panel_Restart, () => Start(site.Name, () => Restart(site.Name))));
        }
        section.Children.Add(buttons);
        Output(section, server.Log, server.State is Status.NodeServerState.Starting or Status.NodeServerState.Running);
        section.Children.Add(Ui.Caption(
            Strings.Sites_DevServerFootnote,
            Ui.TertiaryText));
        return section;
    }

    /// <summary>A proxy's start command: its state, Start and Stop, the command and its output; or a way to add one.</summary>
    private StackPanel ProxyCommandSection(Status.Proxy proxy)
    {
        var section = Section(Strings.Proxy_Commands);
        if (proxy.Command is not { } command)
        {
            section.Children.Add(Ui.Button(Strings.Proxy_AddCommands, () => Start(proxy.Name, () => ChangeCommandAsync(proxy))));
            section.Children.Add(Ui.Caption(Strings.Proxy_CommandHelp, Ui.TertiaryText));
            return section;
        }
        var tone = command.State switch
        {
            Status.ProxyState.Running => Ui.Tone.Success,
            Status.ProxyState.Starting or Status.ProxyState.Stopping => Ui.Tone.Caution,
            Status.ProxyState.Failed => Ui.Tone.Critical,
            _ => Ui.Tone.Neutral,
        };
        section.Children.Add(Field(Strings.Sites_State, StateLine(tone, command.StateLabel,
            command.State == Status.ProxyState.Starting ? command.Problem : "")));
        section.Children.Add(Field(Strings.Services_Start, Ui.MonoText(command.Command, 12)));
        if (command.StopCommand.Length > 0)
        {
            section.Children.Add(Field(Strings.Share_Stop, Ui.MonoText(command.StopCommand, 12)));
        }
        var folder = Ui.Text(command.Dir, wrap: false);
        ToolTipService.SetToolTip(folder, command.Dir);
        section.Children.Add(Field(Strings.General_Folder, folder));

        var buttons = Ui.Row(8);
        if (command.CanStart)
        {
            buttons.Children.Add(Ui.Button(Strings.Services_Start, () => Start(proxy.Name, () => Model.Sites.StartProxyAsync(proxy.Name))));
        }
        if (command.CanStop)
        {
            buttons.Children.Add(Ui.Button(Strings.Share_Stop, () => Start(proxy.Name, () => Model.Sites.StopProxyAsync(proxy.Name))));
        }
        buttons.Children.Add(Ui.Button(Strings.Sites_Change, () => Start(proxy.Name, () => ChangeCommandAsync(proxy))));
        section.Children.Add(buttons);
        Output(section, command.Log,
            command.State is Status.ProxyState.Starting or Status.ProxyState.Running or Status.ProxyState.Stopping);
        section.Children.Add(Ui.Caption(Strings.Proxy_CommandHelp, Ui.TertiaryText));
        return section;
    }

    /// <summary>A state dot with its text, and a second line saying what's wrong (problem may be empty).</summary>
    private static StackPanel StateLine(Ui.Tone tone, string text, string problem)
    {
        var line = Ui.Column(2, Ui.Row(6, Ui.StateDot(tone), Ui.Text(text)));
        if (problem.Length > 0)
        {
            line.Children.Add(Ui.Caption(problem, Ui.Caution));
        }
        return line;
    }

    /// <summary>Puts the output panel for log into section, read every second while live; one panel moves between the details.</summary>
    private void Output(Panel section, string log, bool live)
    {
        outputShown = true;
        output ??= new OutputPanel(Model.Sites);
        output.ShowIn(section, log, live && active);
    }

    /// <summary>The Start, Stop and Folder fields of a proxy's commands, in the Add Proxy dialog and in their own.</summary>
    private sealed class CommandFields
    {
        public required TextBox Start { get; init; }
        public required TextBox Stop { get; init; }
        public required TextBox Folder { get; init; }
        public required StackPanel Panel { get; init; }

        /// <summary>What was entered; without a start command, none (an empty command removes them).</summary>
        public ProxyChange Change => Start.Text.Trim() is { Length: > 0 } start
            ? new ProxyChange(Command: start, Dir: Folder.Text.Trim(), StopCommand: Stop.Text.Trim())
            : new ProxyChange(Command: "");
    }

    private CommandFields NewCommandFields(Status.ProxyCommand? current)
    {
        // The commands are typed in a monospaced font; their labels stay in the dialog's own, like Name and Upstream.
        static TextBlock Label(string text) => new()
        {
            Text = text,
            FontFamily = Application.Current.Resources.TryGetValue("ContentControlThemeFontFamily", out var font) && font is FontFamily family
                ? family : new FontFamily("Segoe UI Variable Text"),
        };
        var start = new TextBox { Header = Label(Strings.Services_Start), PlaceholderText = "npm run dev", Text = current?.Command ?? "", FontFamily = Ui.Mono };
        var stop = new TextBox { Header = Label(Strings.Share_Stop), PlaceholderText = "docker compose stop", Text = current?.StopCommand ?? "", FontFamily = Ui.Mono };
        var folder = new TextBox
        {
            Header = Strings.General_Folder,
            Text = current?.Dir is { Length: > 0 } dir ? dir : Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        };
        var choose = Ui.Button(Strings.Common_Choose, () => _ = ChooseFolderAsync());
        choose.VerticalAlignment = VerticalAlignment.Bottom;
        async Task ChooseFolderAsync()
        {
            if (await Window.PickFolder() is { } picked)
            {
                folder.Text = picked;
            }
        }
        var folderRow = new Grid { ColumnSpacing = 8 };
        folderRow.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        folderRow.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(choose, 1);
        folderRow.Children.Add(folder);
        folderRow.Children.Add(choose);
        return new CommandFields
        {
            Start = start, Stop = stop, Folder = folder,
            Panel = Ui.Column(12, start,
                Ui.Column(4, stop, Ui.Caption(Strings.Proxy_StopCommandHint, Ui.TertiaryText)),
                folderRow),
        };
    }

    /// <summary>Asks for a proxy's commands: Start, Stop and Folder. An empty start command removes them.</summary>
    private async Task ChangeCommandAsync(Status.Proxy proxy)
    {
        var current = proxy.Command;
        var fields = NewCommandFields(current);
        var dialog = new ContentDialog
        {
            Title = Localization.Format(current is null ? Strings.Proxy_AddCommandsTitle : Strings.Proxy_CommandsTitle, proxy.Name),
            Content = Ui.DialogForm(Window.XamlRoot, fields.Panel),
            PrimaryButtonText = Strings.Common_Save,
            SecondaryButtonText = current is null ? "" : Strings.Proxy_RemoveCommands,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        var change = await dialog.ShowAsync() switch
        {
            ContentDialogResult.Secondary => new ProxyChange(Command: ""),
            ContentDialogResult.Primary => fields.Change,
            _ => null,
        };
        if (change is null || current is null && change.Command == "")
        {
            return;
        }
        await Model.Sites.UpdateProxyAsync(proxy.Name, change);
    }
}
