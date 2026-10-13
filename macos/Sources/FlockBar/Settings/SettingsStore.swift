import AppKit
import FlockKit
import Observation

/// Configuration and services for the Settings window.
@MainActor
@Observable
final class SettingsStore {
    private let client: FlockClient
    /// Lets the menu bar status catch up after a change.
    @ObservationIgnored var onChange: (() async -> Void)?

    private(set) var config: FlockConfig?
    private(set) var services: [Service] = []
    var error: String?
    private(set) var isBusy = false
    var isVisible = false

    init(client: FlockClient) {
        self.client = client
    }

    func refresh() async {
        do {
            let (config, services) = try await (client.config(), client.services())
            if config != self.config {
                self.config = config
            }
            if services != self.services {
                self.services = services
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    func refreshIfVisible() async {
        if isVisible {
            await refresh()
        }
    }

    /// Runs a change against the daemon, then reloads everything that may depend on it.
    func perform(_ change: @Sendable (FlockClient) async throws -> Void) async {
        isBusy = true
        defer { isBusy = false }
        do {
            try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    /// Changes services and adopts the list the daemon returns.
    func performOnServices(_ change: @Sendable (FlockClient) async throws -> [Service]) async {
        isBusy = true
        defer { isBusy = false }
        do {
            services = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await onChange?()
    }

    /// Lets the user pick folders; empty when cancelled.
    static func chooseDirectories(prompt: String, multiple: Bool) -> [URL] {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = multiple
        panel.prompt = prompt
        NSApp.activate()
        return panel.runModal() == .OK ? panel.urls : []
    }
}
