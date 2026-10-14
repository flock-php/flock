import AppKit
import FlockKit
import Observation

/// Shared state for the menu bar panel and all windows. Polls the daemon's status.
@MainActor
@Observable
final class AppModel {
    let client = FlockClient()
    let installation = Installation.current

    private(set) var status: Status?
    /// Why the status couldn't be read (the daemon isn't running); replaced by every poll.
    private(set) var error: String?
    /// The last failed user action (restart, a toggle). Polls leave it alone, so it stays until
    /// it is dismissed or the next user action succeeds.
    var actionError: String?
    private(set) var isRestarting = false

    /// Flock isn't set up for this user yet, and this app can do it (see Installation).
    var needsSetup: Bool
    /// The app is replacing the installed CLI with its bundled one.
    private(set) var isUpdating = false

    /// The pane the Settings window shows; the panel opens Settings at a pane.
    var settingsPane: SettingsPane = Snapshot.pane.flatMap(SettingsPane.init(rawValue:)) ?? .general
    /// The service selected in Settings → Services.
    var servicesSelection: String? = Snapshot.service

    /// Points the Settings window at a pane (and a service of the Services pane).
    func showSettings(_ pane: SettingsPane, service: String? = nil) {
        settingsPane = pane
        if let service {
            servicesSelection = service
        }
    }

    let mail: MailStore
    let dumps: DumpStore
    let logs: LogStore
    let settings: SettingsStore

    @ObservationIgnored private var pollTask: Task<Void, Never>?

    /// UserDefaults key: hash of the CLI this app installed last (see Installation.needsUpdate).
    private static let installedFlockKey = "installedFlock"

    init() {
        mail = MailStore(client: client)
        dumps = DumpStore(client: client)
        logs = LogStore(client: client)
        settings = SettingsStore(client: client)
        needsSetup = installation.needsSetup
        startPolling()
        settings.onChange = { [weak self] in await self?.refresh() }

        // Snapshots (a development aid) must never change the installation.
        if Snapshot.target == nil, !needsSetup, installation.needsUpdate(recorded: UserDefaults.standard.string(forKey: Self.installedFlockKey)) {
            Task { await updateInstallation() }
        }
    }

    var isRunning: Bool { status != nil }
    var isDegraded: Bool { status?.isDegraded ?? false }

    var menuBarSymbol: String {
        guard isRunning else { return "bird" }
        return isDegraded ? "exclamationmark.triangle.fill" : "bird.fill"
    }

    /// "Running · v1.2", "Needs attention · v1.2", "Not running" (or "Not set up", "Updating…").
    var headline: String {
        if isUpdating {
            return "Updating…"
        }
        if let status {
            return "\(status.isDegraded ? "Needs attention" : "Running") · v\(status.version)"
        }
        return needsSetup ? "Not set up" : "Not running"
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
        // PostgreSQL, Valkey or a catalog service changed (maybe from the CLI): the Services
        // pane's versions follow.
        if previous != nil, previous?.postgres != new.postgres || previous?.valkey != new.valkey
            || previous?.extras.map(\.version) != new.extras.map(\.version) {
            Task { await settings.refreshInstallsIfLoaded() }
        }
    }

    /// Where a toggle's error is shown: the panel's banner, the Settings window's error bar or
    /// the Dumps window's.
    enum ErrorTarget {
        case panel, settings, dumps
    }

    func setXdebug(_ on: Bool, from target: ErrorTarget = .panel) async {
        await update(target) { try await $0.updateSettings(xdebug: on) }
    }

    func setDumps(_ on: Bool, from target: ErrorTarget = .panel) async {
        await update(target) { try await $0.updateSettings(dumps: on) }
    }

    private func update(_ target: ErrorTarget, _ change: @Sendable (FlockClient) async throws -> Status) async {
        do {
            apply(try await change(client))
            actionError = nil
            switch target {
            case .panel: break
            case .settings: settings.error = nil
            case .dumps: dumps.error = nil
            }
        } catch {
            switch target {
            case .panel: actionError = error.localizedDescription
            case .settings: settings.error = error.localizedDescription
            case .dumps: dumps.error = error.localizedDescription
            }
        }
    }

    /// Runs `flock restart` (which also starts a stopped daemon via launchd).
    func restart() async {
        isRestarting = true
        defer { isRestarting = false }

        let binary = FlockPaths.flockBinary
        guard FileManager.default.fileExists(atPath: binary.path) else {
            actionError = needsSetup
                ? "Flock is not set up for this user yet — click Set Up Flock… first."
                : "Flock is not installed for this user (\(binary.path) is missing) — run `flock install` once."
            return
        }
        let result = await Installation.run(binary, ["restart"])
        actionError = result.ok ? nil : result.output
        await refresh()
    }

    /// Runs the bundled `flock install` after the app was updated. It restarts the daemon; macOS
    /// asks for the password only if DNS or the CA need setting up again.
    func updateInstallation() async {
        isUpdating = true
        let result = await installation.install()
        isUpdating = false
        if result.ok {
            recordInstalled()
        } else {
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = "Flock could not update"
            alert.informativeText = "\(result.output)\n\nYou can run `flock install` in a terminal, or reinstall Flock."
            NSApp.activate()
            alert.runModal()
        }
        await refresh()
    }

    /// Runs the bundled `flock install` for the setup window.
    func install(onLine: @escaping @Sendable (String) -> Void) async -> Installation.Result {
        let result = await installation.install(onLine: onLine)
        needsSetup = installation.needsSetup
        if result.ok {
            recordInstalled()
            actionError = nil
        }
        await refresh()
        return result
    }

    /// Remembers the CLI this app installed, so later updates can tell it from a hand-installed one.
    private func recordInstalled() {
        UserDefaults.standard.set(Installation.hash(installation.installedFlock), forKey: Self.installedFlockKey)
    }

    /// Runs `flock uninstall --keep-app` (the app removes itself afterwards, see removeApp);
    /// purge also deletes the user's settings and databases.
    func uninstall(purge: Bool, onLine: @escaping @Sendable (String) -> Void) async -> Installation.Result {
        guard let flock = installation.uninstaller else {
            return Installation.Result(ok: false, output: "Flock's CLI is not installed — there is nothing to uninstall.")
        }
        let result = await Installation.run(flock, ["uninstall", "--keep-app"] + (purge ? ["--purge"] : []), onLine: onLine)
        needsSetup = installation.needsSetup
        await refresh()
        return result
    }

    /// Where this app runs from, when that is an Applications folder (not a development build).
    var installedAppURL: URL? {
        let app = Bundle.main.bundleURL
        let folders = [URL(fileURLWithPath: "/Applications"), FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications")]
        guard app.pathExtension == "app", folders.contains(where: { $0.standardizedFileURL.path == app.deletingLastPathComponent().standardizedFileURL.path }) else {
            return nil
        }
        return app
    }

    /// After uninstalling: forgets the app's preferences, moves the app to the Trash and quits.
    func removeAppAndQuit() {
        if let domain = Bundle.main.bundleIdentifier {
            UserDefaults.standard.removePersistentDomain(forName: domain)
        }
        if let app = installedAppURL {
            try? FileManager.default.trashItem(at: app, resultingItemURL: nil)
        }
        NSApp.terminate(nil)
    }

    func open(_ site: Status.Site) {
        guard let string = site.url, let url = URL(string: string) else { return }
        NSWorkspace.shared.open(url)
    }
}
