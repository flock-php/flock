import FlockKit
import SwiftUI

/// Registers Flock's MCP server with AI clients. It is added once and works in every project:
/// the server finds the project the agent works in.
struct AIPane: View {
    @Environment(AppModel.self) private var model
    /// The client being added or removed.
    @State private var working: String?

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "AI", subtitle: "Let AI agents like Claude Code and Cursor work with Flock: your sites' URLs and PHP versions, logs, dumps and mail, and installing versions and services.")
            Form {
                Section {
                    if let mcp = store.mcp {
                        ForEach(mcp.clients) { client in
                            row(client)
                        }
                    } else {
                        ProgressView().frame(maxWidth: .infinity)
                    }
                } header: {
                    Text("AI clients")
                } footer: {
                    Text("Added once, Flock works in every project. Restart the client, or reload its MCP servers, after adding Flock.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    CodeBlock(text: store.mcp?.snippet ?? "")
                } header: {
                    Text("Other clients")
                } footer: {
                    Text("Add this to the client's MCP settings.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .task { await store.refreshMCP() }
    }

    private func row(_ client: MCPClient) -> some View {
        LabeledContent {
            HStack(spacing: 8) {
                if working == client.id {
                    ProgressView().controlSize(.small)
                }
                switch client.state {
                case .registered:
                    Button("Remove") { change(client, add: false) }
                case .available:
                    Button("Add") { change(client, add: true) }
                case .missing:
                    EmptyView()
                }
            }
            .disabled(working != nil)
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    StatusDot(on: client.state == .registered)
                    Text(client.name)
                }
                Text(caption(client))
                    .font(.caption).foregroundStyle(.secondary)
                if !client.problem.isEmpty {
                    Label(client.problem, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
            }
        }
    }

    private func caption(_ client: MCPClient) -> String {
        switch client.state {
        case .registered: "Flock is added"
        case .available: "Installed"
        case .missing: "Not installed"
        }
    }

    private func change(_ client: MCPClient, add: Bool) {
        let store = model.settings
        working = client.id
        Task {
            await store.performOnMCP { [id = client.id] flock in
                if add {
                    return try await flock.registerMCP(id)
                }
                return try await flock.unregisterMCP(id)
            }
            working = nil
        }
    }
}
