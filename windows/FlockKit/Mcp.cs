namespace Flock.Kit;

/// <summary>The AI clients Flock's MCP server can be registered with (GET /v1/mcp), for Settings → AI.</summary>
public sealed class McpState
{
    public List<McpClient> Clients { get; init; } = [];
    /// <summary>The command the clients start (the installed flock.exe) and its arguments.</summary>
    public string Command { get; init; } = "";
    public List<string> Args { get; init; } = [];
    /// <summary>The JSON to paste into a client Flock doesn't register itself.</summary>
    public string Snippet { get; init; } = "";
}

/// <summary>One AI client (Claude Code, Cursor, VS Code, Windsurf, Codex) and whether Flock is registered with it.</summary>
public sealed class McpClient
{
    public string Id { get; init; } = "";
    public string Name { get; init; } = "";
    /// <summary>One of McpClientState.</summary>
    public string State { get; init; } = McpClientState.Missing;
    /// <summary>The file the registration lives in.</summary>
    public string Config { get; init; } = "";
    /// <summary>Why the state couldn't be read, e.g. a config file Flock can't parse.</summary>
    public string Problem { get; init; } = "";
}

/// <summary>Values of McpClient.State.</summary>
public static class McpClientState
{
    /// <summary>Not installed on this PC.</summary>
    public const string Missing = "missing";
    /// <summary>Installed, Flock not registered.</summary>
    public const string Available = "available";
    public const string Registered = "registered";
}
