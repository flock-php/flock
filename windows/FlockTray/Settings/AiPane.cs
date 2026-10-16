using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// Registers Flock's MCP server with AI clients. It is added once and works in every project: the
/// server finds the project the agent works in.
/// </summary>
internal sealed class AiPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    /// <summary>The client being added or removed.</summary>
    private string? working;

    public override string Title => "AI";
    public override string Subtitle => "Let AI agents like Claude Code and Cursor work with Flock: your sites' URLs and PHP versions, logs, dumps and mail, and installing versions and services.";

    public override void Shown(CancellationToken hidden) => _ = Store.RefreshMcpAsync();

    public override UIElement Build()
    {
        var mcp = Store.Mcp;
        var clients = new List<UIElement>();
        if (mcp is null)
        {
            clients.Add(Ui.Card(new ProgressRing { IsActive = true, Width = 20, Height = 20 }));
        }
        else
        {
            foreach (var client in mcp.Clients)
            {
                clients.Add(Row(client));
            }
        }
        clients.Add(GeneralPane.Footnote("Added once, Flock works in every project. Restart the client, or reload its MCP servers, after adding Flock."));

        return Ui.Column(0,
            Ui.Section("AI clients", [.. clients]),
            Ui.Section("Other clients",
                Ui.Card(Ui.CodeBlock(mcp?.Snippet ?? "")),
                GeneralPane.Footnote("Add this to the client's MCP settings.")));
    }

    private Border Row(McpClient client)
    {
        var description = client.State switch
        {
            McpClientState.Registered => "Flock is added",
            McpClientState.Available => "Installed",
            _ => "Not installed",
        };
        if (client.Problem.Length > 0)
        {
            description += " — " + client.Problem;
        }

        Button? button = client.State switch
        {
            McpClientState.Registered => Ui.Button("Remove", () => _ = Change(client, add: false)),
            McpClientState.Available => Ui.Button("Add", () => _ = Change(client, add: true)),
            _ => null,
        };
        if (button is null)
        {
            return Ui.Setting(client.Name, description, null);
        }
        button.IsEnabled = working is null;

        UIElement control = button;
        if (working == client.Id)
        {
            control = Ui.Row(8, new ProgressRing { IsActive = true, Width = 16, Height = 16 }, button);
        }
        return Ui.Setting(client.Name, description, control);
    }

    private async Task Change(McpClient client, bool add)
    {
        working = client.Id;
        Rerender();
        try
        {
            await Store.PerformOnMcpAsync(c => add ? c.RegisterMcpAsync(client.Id) : c.UnregisterMcpAsync(client.Id));
        }
        finally
        {
            working = null;
            Rerender();
        }
    }
}
