using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Data;

namespace Flock.Tray;

/// <summary>Registers Flock's MCP server with installed AI clients.</summary>
internal sealed class AiPane : Pane
{
    private readonly AiViewModel viewModel;
    private readonly StackPanel clientRows = new() { Spacing = 4 };
    private readonly KeyedRowsBinding<string, McpClientViewModel> binding;

    public AiPane(SettingsWindow window, RuntimeService model) : base(window, model)
    {
        viewModel = new AiViewModel(model.Mcp);
        binding = new KeyedRowsBinding<string, McpClientViewModel>(clientRows, row => row.Id, CreateRow);
        viewModel.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName is nameof(viewModel.Clients) or nameof(viewModel.Snippet))
            {
                binding.Synchronize(viewModel.Clients);
            }
        };
    }

    public override string Title => "AI";
    public override string Subtitle => "Let AI agents like Claude Code and Cursor work with Flock: your sites' URLs and PHP versions, logs, dumps and mail, and installing versions and services.";
    public override bool RetainsRoot => true;

    public override Task ActivateAsync(CancellationToken hidden) => viewModel.RefreshAsync(hidden);

    public override UIElement Build()
    {
        binding.Synchronize(viewModel.Clients);
        var clients = Ui.Column(4, clientRows, GeneralPane.Footnote("Added once, Flock works in every project. Restart the client, or reload its MCP servers, after adding Flock."));
        return Ui.Column(0,
            Ui.Section("AI clients", clients),
            Ui.Section("Other clients",
                Ui.Card(Ui.CodeBlock(viewModel.Snippet)),
                GeneralPane.Footnote("Add this to the client's MCP settings.")));
    }

    private static Border CreateRow(McpClientViewModel row)
    {
        var add = Ui.Button("Add", () => { });
        var remove = Ui.Button("Remove", () => { });
        add.SetBinding(Button.CommandProperty, new Binding { Source = row, Path = new PropertyPath(nameof(row.RegisterCommand)) });
        remove.SetBinding(Button.CommandProperty, new Binding { Source = row, Path = new PropertyPath(nameof(row.UnregisterCommand)) });
        var description = Ui.Text(row.Description, foreground: Ui.SecondaryText);
        var controls = Ui.Row(8, add, remove);
        void Update()
        {
            description.Text = row.Description;
            add.Visibility = row.IsAvailable ? Visibility.Visible : Visibility.Collapsed;
            remove.Visibility = row.IsRegistered ? Visibility.Visible : Visibility.Collapsed;
        }
        row.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName is nameof(row.Description) or nameof(row.IsAvailable) or nameof(row.IsRegistered))
            {
                Update();
            }
        };
        Update();
        return Ui.Setting(row.Name, null, Ui.Column(2, description, controls));
    }
}