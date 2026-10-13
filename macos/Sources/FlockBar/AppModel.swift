import AppKit
import FlockKit
import Observation

/// Shared state for the menu bar panel and all windows. Polls the daemon's status.
@MainActor
@Observable
final class AppModel {
    let client = FlockClient()

    private(set) var status: Status?
    private(set) var error: String?
    private(set) var isRestarting = false

    let mail: MailStore
    let dumps: DumpStore
    let logs: LogStore
    let settings: SettingsStore

    @ObservationIgnored private var pollTask: Task<Void, Never>?

    init() {
        mail = MailStore(client: client)
        dumps = DumpStore(client: client)
        logs = LogStore(client: client)
        settings = SettingsStore(client: client)
        startPolling()
        settings.onChange = { [weak self] in await self?.refresh() }
    }

    var isRunning: Bool { status != nil }
    var isDegraded: Bool { status?.isDegraded ?? false }

    var menuBarSymbol: String {
        guard isRunning else { return "bird" }
        return isDegraded ? "exclamationmark.triangle.fill" : "bird.fill"
    }

    private func startPolling() {
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    func refresh() async {
        do {
            apply(try await client.status())
        } catch {
            status = nil
            self.error = error.localizedDescription
        }
    }

    private func apply(_ new: Status) {
        let previous = status
        status = new
        error = nil
        Task { await settings.refreshIfVisible() }

        // Lists in open windows follow the counters in the status.
        if previous?.mailCount != new.mailCount {
            Task { await mail.refreshIfVisible() }
        }
        if previous?.dumpCount != new.dumpCount {
            Task { await dumps.refreshIfVisible() }
        }
    }

    func setXdebug(_ on: Bool) async {
        await update { try await $0.updateSettings(xdebug: on) }
    }

    func setDumps(_ on: Bool) async {
        await update { try await $0.updateSettings(dumps: on) }
    }

    private func update(_ change: @Sendable (FlockClient) async throws -> Status) async {
        do {
            apply(try await change(client))
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs `flock restart` (which also starts a stopped daemon via launchd).
    func restart() async {
        isRestarting = true
        defer { isRestarting = false }

        let binary = FlockPaths.flockBinary
        let result = await Task.detached { () -> String? in
            let process = Process()
            process.executableURL = binary
            process.arguments = ["restart"]
            let output = Pipe()
            process.standardOutput = output
            process.standardError = output
            do {
                try process.run()
                process.waitUntilExit()
            } catch {
                return "Could not run \(binary.path): \(error.localizedDescription)"
            }
            guard process.terminationStatus != 0 else { return nil }
            let data = output.fileHandleForReading.readDataToEndOfFile()
            return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        }.value

        if let result {
            error = result
        }
        await refresh()
    }

    func open(_ site: Status.Site) {
        guard let string = site.url, let url = URL(string: string) else { return }
        NSWorkspace.shared.open(url)
    }
}
