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
    /// PHP versions from php.net with install state; loaded by the PHP pane.
    private(set) var php: PHPCatalog?
    /// FrankenPHP builds per PHP minor; loaded by the PHP pane.
    private(set) var frankenPHP: FrankenPHPState?
    /// Node.js lines from nodejs.org with install state; loaded by the Node pane.
    private(set) var node: NodeCatalog?
    /// PostgreSQL majors with install state; loaded by the Services pane.
    private(set) var postgres: PostgresVersions?
    /// Valkey (macOS); loaded by the Services pane.
    private(set) var components: ComponentCatalog?
    /// MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS; loaded by the Services pane.
    private(set) var catalog: ServiceCatalog?
    /// The AI clients Flock's MCP server can be registered with; loaded by the AI pane.
    private(set) var mcp: MCPState?
    /// Catalog services this window asked to install that the daemon hasn't reported a job for yet.
    private(set) var catalogPending: Set<String> = []
    /// PostgreSQL operations this window started that haven't answered yet, by major.
    private(set) var postgresPending: [String: PostgresMajor.Busy] = [:]
    var error: String?
    private(set) var isBusy = false
    var isVisible = false

    init(client: FlockClient) {
        self.client = client
    }

    func refresh() async {
        if Snapshot.isFixture { return }
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

    /// Reloads the AI clients (only the AI pane lists them).
    func refreshMCP() async {
        if Snapshot.isFixture { return }
        do {
            let fresh = try await client.mcp()
            if fresh != mcp {
                mcp = fresh
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Registers or removes Flock with an AI client and adopts the list the daemon returns.
    func performOnMCP(_ change: @Sendable (FlockClient) async throws -> MCPState) async {
        isBusy = true
        defer { isBusy = false }
        do {
            mcp = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
            await refreshMCP()
        }
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

    /// Reloads the PHP catalog (not part of `refresh`, it is only needed by the PHP pane).
    func refreshPHP() async {
        do {
            let fresh = try await client.phpVersions()
            if fresh != php {
                php = fresh
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs a PHP action and adopts the catalog it returns; installs also change config and status.
    func performOnPHP(_ change: @Sendable (FlockClient) async throws -> PHPCatalog) async {
        isBusy = true
        defer { isBusy = false }
        do {
            php = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    /// Reloads the FrankenPHP builds (only the PHP pane lists them).
    func refreshFrankenPHP() async {
        if Snapshot.isFixture { return }
        do {
            let fresh = try await client.frankenPHP()
            if fresh != frankenPHP {
                frankenPHP = fresh
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs a FrankenPHP action and adopts the builds it returns; installs reload sites waiting for them.
    func performOnFrankenPHP(_ change: @escaping @Sendable (FlockClient) async throws -> FrankenPHPState) async {
        do {
            frankenPHP = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await onChange?()
    }

    /// Reloads the Node.js catalog (only the Node pane needs it).
    func refreshNode() async {
        do {
            let fresh = try await client.nodeVersions()
            if fresh != node {
                node = fresh
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs a Node.js action and adopts the catalog it returns.
    func performOnNode(_ change: @Sendable (FlockClient) async throws -> NodeCatalog) async {
        do {
            node = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
    }

    // MARK: PostgreSQL versions and components (Services pane)

    /// Reloads PostgreSQL versions, components (Valkey) and the catalog; the
    /// Services pane needs them.
    func refreshInstalls() async {
        if Snapshot.isFixture { return }
        do {
            async let postgres = client.postgresVersions()
            async let components = client.components()
            async let catalog = client.serviceCatalog()
            let fresh = try await (postgres, components, catalog)
            adopt(fresh.0)
            if fresh.1 != self.components {
                self.components = fresh.1
            }
            adopt(fresh.2)
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Reloads them only when the Services pane has loaded them before.
    func refreshInstallsIfLoaded() async {
        if isVisible, postgres != nil || components != nil || catalog != nil {
            await refreshInstalls()
        }
    }

    /// A PostgreSQL operation, or a component job, runs — so the Services pane polls.
    var installsBusy: Bool {
        !postgresPending.isEmpty || (postgres?.isBusy ?? false) || (components?.hasRunningJob ?? false)
            || !catalogPending.isEmpty || (catalog?.hasRunningJob ?? false)
    }

    /// What a major is doing: the daemon's state, or what this window just asked for while the
    /// daemon hasn't reported it yet.
    func busy(_ major: PostgresMajor) -> PostgresMajor.Busy {
        major.isBusy ? major.busy : (postgresPending[major.major] ?? .idle)
    }

    /// Runs a PostgreSQL action (installs can take minutes) and adopts the versions it returns.
    /// Polling during the request shows the daemon's own busy state.
    func performOnPostgres(_ major: String, _ pending: PostgresMajor.Busy, _ change: @escaping @Sendable (FlockClient) async throws -> PostgresVersions) async {
        postgresPending[major] = pending
        defer { postgresPending[major] = nil }
        do {
            adopt(try await change(client))
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    /// "Check for Updates": asks for new PostgreSQL, component and catalog releases at once.
    func checkForUpdates() async {
        if Snapshot.isFixture { return }
        do {
            async let postgres = client.checkPostgres()
            async let components = client.checkComponents()
            async let catalog = client.checkServiceCatalog()
            let fresh = try await (postgres, components, catalog)
            adopt(fresh.0)
            self.components = fresh.1
            adopt(fresh.2)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Runs a catalog action for service `id` and adopts the catalog it returns.
    func performOnCatalog(_ id: String, _ change: @escaping @Sendable (FlockClient) async throws -> ServiceCatalog) async {
        catalogPending.insert(id)
        defer { catalogPending.remove(id) }
        do {
            adopt(try await change(client))
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    func catalogLog(_ id: String) async -> String {
        if Snapshot.isFixture { return SnapshotFixtures.log }
        return (try? await client.catalogLog(id)) ?? ""
    }

    /// Runs a component action and adopts the catalog it returns.
    func performOnComponents(_ change: @escaping @Sendable (FlockClient) async throws -> ComponentCatalog) async {
        do {
            components = try await change(client)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        await refresh()
        await onChange?()
    }

    func componentLog(_ id: String) async -> String {
        if Snapshot.isFixture { return SnapshotFixtures.log }
        return (try? await client.componentLog(id)) ?? ""
    }

    private func adopt(_ fresh: PostgresVersions) {
        if fresh != postgres {
            postgres = fresh
        }
    }

    private func adopt(_ fresh: ServiceCatalog) {
        if fresh != catalog {
            catalog = fresh
        }
    }

    /// Fills the store with fixture data for `--snapshot` renders of the Services pane.
    func loadFixture(services: [Service], postgres: PostgresVersions?, components: ComponentCatalog?, catalog: ServiceCatalog? = nil) {
        self.services = services
        self.postgres = postgres
        self.components = components
        self.catalog = catalog
    }

    func phpInstallLog(_ minor: String) async -> String {
        (try? await client.phpInstallLog(minor)) ?? ""
    }

    /// The PHP extensions a site needs, or why the daemon couldn't tell.
    func siteExtensions(_ site: String) async -> Result<ExtensionReport, Error> {
        do {
            return .success(try await client.siteExtensions(site: site))
        } catch {
            return .failure(error)
        }
    }

    /// Adds `ext-<name>` to a site's composer.json, then refreshes the sites (a problem may be gone).
    func requireExtension(site: String, name: String) async -> Result<ExtensionReport, Error> {
        let result: Result<ExtensionReport, Error>
        do {
            result = .success(try await client.requireExtension(site: site, name: name))
        } catch {
            result = .failure(error)
        }
        await onChange?()
        return result
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
