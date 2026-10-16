import Foundation

/// The AI clients Flock's MCP server can be registered with (GET /v1/mcp), for Settings → AI.
public struct MCPState: Decodable, Sendable, Equatable {
    public var clients: [MCPClient]
    /// The command the clients start (the installed flock binary) and its arguments.
    public var command: String
    public var args: [String]
    /// The JSON to paste into a client Flock doesn't register itself.
    public var snippet: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clients = try c.decodeIfPresent([MCPClient].self, forKey: .clients) ?? []
        command = try c.decodeIfPresent(String.self, forKey: .command) ?? ""
        args = try c.decodeIfPresent([String].self, forKey: .args) ?? []
        snippet = try c.decodeIfPresent(String.self, forKey: .snippet) ?? ""
    }

    enum CodingKeys: String, CodingKey {
        case clients, command, args, snippet
    }
}

/// One AI client (Claude Code, Cursor, VS Code, Windsurf, Codex) and whether Flock is registered with it.
public struct MCPClient: Decodable, Sendable, Equatable, Identifiable {
    public enum State: String, Decodable, Sendable {
        /// Not installed on this Mac.
        case missing
        /// Installed, Flock not registered.
        case available
        case registered

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .missing
        }
    }

    public var id: String
    public var name: String
    public var state: State
    /// The file the registration lives in.
    public var config: String
    /// Why the state couldn't be read, e.g. a config file Flock can't parse.
    public var problem: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .missing
        config = try c.decodeIfPresent(String.self, forKey: .config) ?? ""
        problem = try c.decodeIfPresent(String.self, forKey: .problem) ?? ""
    }

    enum CodingKeys: String, CodingKey {
        case id, name, state, config, problem
    }
}
