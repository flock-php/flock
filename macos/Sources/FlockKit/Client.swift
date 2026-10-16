import Foundation

/// Where Flock keeps its files (FLOCK_HOME overrides it, like the CLI).
public enum FlockPaths {
    public static var root: URL {
        if let home = ProcessInfo.processInfo.environment["FLOCK_HOME"], !home.isEmpty {
            return URL(fileURLWithPath: home, isDirectory: true)
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Flock", isDirectory: true)
    }

    public static var apiJSON: URL { root.appendingPathComponent("api.json") }
    public static var flockBinary: URL { root.appendingPathComponent("bin/flock") }
}

/// Contents of api.json, written by the daemon.
public struct APIConfig: Decodable, Sendable, Equatable {
    public var url: URL
    public var token: String
}

public enum FlockError: LocalizedError, Equatable {
    case notRunning
    case server(status: Int, message: String)

    public var errorDescription: String? {
        switch self {
        case .notRunning: "Flock is not running"
        case let .server(status, message): message.isEmpty ? "Flock answered with HTTP \(status)" : message
        }
    }
}

/// Talks to the daemon's local HTTP API.
public actor FlockClient {
    private let configPath: URL
    private let session: URLSession
    private var config: APIConfig?

    /// Default timeout of a request; slow endpoints pass their own.
    static let defaultTimeout: TimeInterval = 5
    /// Changing PHP versions waits for the daemon's work, like the Go CLI (2 minutes).
    static let phpChangeTimeout: TimeInterval = 120
    /// Installing or updating a PostgreSQL major downloads it first (10 minutes).
    static let postgresInstallTimeout: TimeInterval = 600
    /// Switching or uninstalling a PostgreSQL major stops and starts servers (2 minutes).
    static let postgresChangeTimeout: TimeInterval = 120
    /// Restarting a FrankenPHP site waits for its process to come back (1 minute, like the Go CLI).
    static let siteRestartTimeout: TimeInterval = 60
    /// Registering with Claude Code runs its CLI.
    static let mcpChangeTimeout: TimeInterval = 30
    /// Adding Flock's Boost guideline runs `php artisan boost:update`, which boots the app.
    static let boostTimeout: TimeInterval = 120

    public init(configPath: URL = FlockPaths.apiJSON) {
        self.init(configPath: configPath, configuration: .ephemeral)
    }

    /// Tests pass a configuration with a stub URLProtocol.
    init(configPath: URL, configuration: URLSessionConfiguration) {
        self.configPath = configPath
        // Each request sets its own timeout; the session's must not cut the slow ones short.
        configuration.timeoutIntervalForRequest = 600
        configuration.connectionProxyDictionary = [:] // never send localhost traffic through a proxy
        self.session = URLSession(configuration: configuration)
    }

    // MARK: Endpoints

    public func status() async throws -> Status {
        try await decode(Status.self, "GET", "/v1/status")
    }

    /// Changes only the given settings and returns the new status.
    public func updateSettings(xdebug: Bool? = nil, xdebugBreakpoints: Bool? = nil, dumps: Bool? = nil) async throws -> Status {
        try await updateSettings(SettingsChange(xdebug: xdebug, xdebugBreakpoints: xdebugBreakpoints, dumps: dumps))
    }

    /// Changes only the non-nil settings and returns the new status.
    public func updateSettings(_ change: SettingsChange) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/settings", body: try JSONEncoder().encode(change))
    }

    public func config() async throws -> FlockConfig {
        try await decode(FlockConfig.self, "GET", "/v1/config")
    }

    public func services() async throws -> [Service] {
        try await decode([Service]?.self, "GET", "/v1/services") ?? []
    }

    public func startService(_ id: String) async throws -> [Service] {
        try await decode([Service]?.self, "POST", "/v1/services/\(escape(id))/start") ?? []
    }

    public func stopService(_ id: String) async throws -> [Service] {
        try await decode([Service]?.self, "POST", "/v1/services/\(escape(id))/stop") ?? []
    }

    public func setServicePort(_ id: String, port: Int) async throws -> [Service] {
        try await decode([Service]?.self, "PATCH", "/v1/services/\(escape(id))", body: try JSONEncoder().encode(["port": port])) ?? []
    }

    /// Links a directory as a site (name defaults to the directory name on the daemon side).
    public func linkSite(path: String, name: String? = nil, php: String? = nil, secured: Bool = false) async throws -> Status {
        struct Body: Encodable { var path: String; var name: String?; var php: String?; var secured: Bool }
        return try await decode(Status.self, "POST", "/v1/sites", body: try JSONEncoder().encode(Body(path: path, name: name, php: php, secured: secured)))
    }

    public func updateSite(_ name: String, _ change: SiteChange) async throws -> Status {
        try await decode(Status.self, "PATCH", "/v1/sites/\(escape(name))", body: try JSONEncoder().encode(change))
    }

    /// Restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.
    public func restartSite(_ name: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(name))/restart", timeout: Self.siteRestartTimeout)
    }

    public func unlinkSite(_ name: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(name))")
    }

    /// Adds an extra domain: a subdomain ("tenant1", "a.b") or an alias ("my-app-2.test").
    public func addDomain(site: String, domain: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/domains", body: try JSONEncoder().encode(["domain": domain]))
    }

    /// Removes an extra domain, given as `SiteDomain.entry` (or its full name).
    public func removeDomain(site: String, domain: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(site))/domains/\(escape(domain))")
    }

    /// Writes a version (tool "php" or "node"; empty version: the one the site uses now) into the
    /// site's project files and lifts the site's isolation for that tool.
    public func pin(site: String, tool: String, version: String = "") async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/pin", body: try JSONEncoder().encode(["tool": tool, "version": version]))
    }

    /// Removes a tool's version from the site's project files.
    public func unpin(site: String, tool: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/sites/\(escape(site))/pin/\(escape(tool))")
    }

    /// Writes Flock's guideline into the site's Laravel Boost guidelines (.ai/guidelines/flock.md)
    /// and runs `php artisan boost:update`, so the agents' guideline files include it.
    public func addBoostGuideline(site: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/sites/\(escape(site))/boost", timeout: Self.boostTimeout)
    }

    // MARK: AI clients

    /// The AI clients Flock's MCP server can be registered with.
    public func mcp() async throws -> MCPState {
        try await decode(MCPState.self, "GET", "/v1/mcp")
    }

    /// Registers Flock's MCP server with a client ("claude-code", "cursor", "vscode", "windsurf", "codex").
    public func registerMCP(_ client: String) async throws -> MCPState {
        try await decode(MCPState.self, "POST", "/v1/mcp/clients/\(escape(client))", timeout: Self.mcpChangeTimeout)
    }

    /// Removes Flock's MCP server from a client; nothing else in its configuration changes.
    public func unregisterMCP(_ client: String) async throws -> MCPState {
        try await decode(MCPState.self, "DELETE", "/v1/mcp/clients/\(escape(client))", timeout: Self.mcpChangeTimeout)
    }

    public func park(_ path: String) async throws -> Status {
        try await decode(Status.self, "POST", "/v1/parked", body: try JSONEncoder().encode(["path": path]))
    }

    public func unpark(_ path: String) async throws -> Status {
        try await decode(Status.self, "DELETE", "/v1/parked", body: try JSONEncoder().encode(["path": path]))
    }

    /// Downloads and installs a Node.js version ("20", "20.19" or exact); this can take a minute.
    public func installNode(_ version: String) async throws -> String {
        struct Installed: Decodable { var version: String }
        let data = try await send("POST", "/v1/node/install", body: try JSONEncoder().encode(["version": version]), timeout: 300)
        return try FlockJSON.decoder().decode(Installed.self, from: data).version
    }

    // MARK: Node.js versions

    public func nodeVersions() async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "GET", "/v1/node/versions", timeout: 60)
    }

    /// Installs the newest release of a major line, or updates it; a download of about 30 MB.
    public func installNodeMajor(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/versions/\(escape(major))/install", timeout: 600)
    }

    /// Updates a line's npm to the newest version that supports it; its global packages stay.
    public func updateNpm(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/versions/\(escape(major))/npm", timeout: 600)
    }

    public func uninstallNode(_ major: String) async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "DELETE", "/v1/node/versions/\(escape(major))")
    }

    /// Asks the daemon to check nodejs.org (and npm's registry) for new releases now.
    public func checkNodeReleases() async throws -> NodeCatalog {
        try await decode(NodeCatalog.self, "POST", "/v1/node/check", timeout: 60)
    }

    // MARK: PHP versions

    public func phpVersions() async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "GET", "/v1/php/versions")
    }

    /// Installs a version: instantly when a verified build is waiting, otherwise it queues a build that installs when done.
    public func installPHP(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/versions/\(escape(minor))/install", timeout: Self.phpChangeTimeout)
    }

    /// Builds a version without installing it, to verify it or prepare an instant install.
    public func buildPHP(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/versions/\(escape(minor))/build", timeout: Self.phpChangeTimeout)
    }

    public func cancelPHPBuild(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/versions/\(escape(minor))/cancel", timeout: Self.phpChangeTimeout)
    }

    public func uninstallPHP(_ minor: String) async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "DELETE", "/v1/php/versions/\(escape(minor))", timeout: Self.phpChangeTimeout)
    }

    /// Asks the daemon to check php.net for new releases now.
    public func checkPHPReleases() async throws -> PHPCatalog {
        try await decode(PHPCatalog.self, "POST", "/v1/php/check", timeout: 60)
    }

    public func phpBuildLog(_ minor: String) async throws -> String {
        String(decoding: try await send("GET", "/v1/php/versions/\(escape(minor))/log"), as: UTF8.self)
    }

    // MARK: FrankenPHP builds

    /// The builds per PHP minor; `.unavailable` when the daemon has no FrankenPHP (this platform,
    /// or a daemon from before it).
    public func frankenPHP() async throws -> FrankenPHPState {
        do {
            return try await decode(FrankenPHPState.self, "GET", "/v1/frankenphp")
        } catch FlockError.server(let status, _) where status == 404 || status == 501 {
            return .unavailable
        }
    }

    /// Asks the daemon to fetch the list of published builds now.
    public func checkFrankenPHP() async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/check", timeout: 60)
    }

    /// Starts installing, updating or retrying the build for a PHP minor in the background; poll for the job.
    public func installFrankenPHP(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/\(escape(minor))/install", timeout: 30)
    }

    public func cancelFrankenPHPJob(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "POST", "/v1/frankenphp/\(escape(minor))/cancel")
    }

    /// Dismisses a failed install.
    public func dismissFrankenPHPJob(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "DELETE", "/v1/frankenphp/\(escape(minor))/job")
    }

    /// Removes the build; the daemon refuses (409) while a site runs on it.
    public func uninstallFrankenPHP(_ minor: String) async throws -> FrankenPHPState {
        try await decode(FrankenPHPState.self, "DELETE", "/v1/frankenphp/\(escape(minor))", timeout: 60)
    }

    /// The tail of the current or last install's output; empty when there is none.
    public func frankenPHPLog(_ minor: String) async throws -> String {
        do {
            return String(decoding: try await send("GET", "/v1/frankenphp/\(escape(minor))/log"), as: UTF8.self)
        } catch FlockError.server(404, _) {
            return ""
        }
    }

    // MARK: PostgreSQL versions

    /// Cached on the daemon side; it never waits on the network.
    public func postgresVersions() async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "GET", "/v1/postgres/versions")
    }

    /// Asks the daemon to look for new PostgreSQL releases now.
    public func checkPostgres() async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/check", timeout: 60)
    }

    /// Downloads and installs the newest release of a major; the first one installed becomes active and starts.
    public func installPostgres(_ major: String) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/install", timeout: Self.postgresInstallTimeout)
    }

    /// Installs the newest minor of a major; its databases stay, and it restarts when active.
    public func updatePostgres(_ major: String) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/update", timeout: Self.postgresInstallTimeout)
    }

    /// Makes an installed major the one that runs (with its own databases).
    public func activatePostgres(_ major: String) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "POST", "/v1/postgres/versions/\(escape(major))/activate", timeout: Self.postgresChangeTimeout)
    }

    /// Uninstalls a major; its databases stay unless `deleteData` is set.
    public func uninstallPostgres(_ major: String, deleteData: Bool = false) async throws -> PostgresVersions {
        try await decode(PostgresVersions.self, "DELETE", "/v1/postgres/versions/\(escape(major))",
                         query: deleteData ? [URLQueryItem(name: "delete_data", value: "1")] : [], timeout: Self.postgresChangeTimeout)
    }

    // MARK: Catalog (MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS)

    public func serviceCatalog() async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "GET", "/v1/catalog")
    }

    /// Asks the daemon to look for new releases of the catalog services now.
    public func checkServiceCatalog() async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "POST", "/v1/catalog/check", timeout: 60)
    }

    /// Starts installing the newest release of `line` on `port` in the background (nil for the
    /// installed line or newest, and the configured or suggested port); poll for the job.
    public func installCatalogService(_ id: String, line: String? = nil, port: Int? = nil) async throws -> ServiceCatalog {
        let body = try JSONEncoder().encode(CatalogInstall(line: line ?? "", port: port ?? 0))
        return try await decode(ServiceCatalog.self, "POST", "/v1/catalog/\(escape(id))/install", body: body)
    }

    public func cancelCatalogJob(_ id: String) async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "POST", "/v1/catalog/\(escape(id))/cancel")
    }

    /// Dismisses a failed install.
    public func dismissCatalogJob(_ id: String) async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "DELETE", "/v1/catalog/\(escape(id))/job")
    }

    /// Uninstalls a catalog service; its data stays unless `deleteData` is set.
    public func uninstallCatalogService(_ id: String, deleteData: Bool = false) async throws -> ServiceCatalog {
        try await decode(ServiceCatalog.self, "DELETE", "/v1/catalog/\(escape(id))",
                         query: deleteData ? [URLQueryItem(name: "delete_data", value: "1")] : [], timeout: 60)
    }

    /// The tail of the current or last install's output; empty when there is none.
    public func catalogLog(_ id: String) async throws -> String {
        do {
            return String(decoding: try await send("GET", "/v1/catalog/\(escape(id))/log"), as: UTF8.self)
        } catch FlockError.server(404, _) {
            return ""
        }
    }

    private struct CatalogInstall: Encodable {
        var line: String
        var port: Int
    }

    // MARK: Components (Valkey, PostgreSQL command-line tools)

    public func components() async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "GET", "/v1/components")
    }

    /// Asks the daemon to look for new releases of the components now.
    public func checkComponents() async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "POST", "/v1/components/check", timeout: 60)
    }

    /// Starts installing (or updating) a component in the background; poll `components()` for the job.
    public func installComponent(_ id: String) async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "POST", "/v1/components/\(escape(id))/install")
    }

    public func cancelComponentJob(_ id: String) async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "POST", "/v1/components/\(escape(id))/cancel")
    }

    /// Dismisses a failed job.
    public func dismissComponentJob(_ id: String) async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "DELETE", "/v1/components/\(escape(id))/job")
    }

    public func uninstallComponent(_ id: String) async throws -> ComponentCatalog {
        try await decode(ComponentCatalog.self, "DELETE", "/v1/components/\(escape(id))", timeout: 30)
    }

    /// The tail of the current or last job's output; empty when there is none.
    public func componentLog(_ id: String) async throws -> String {
        do {
            return String(decoding: try await send("GET", "/v1/components/\(escape(id))/log"), as: UTF8.self)
        } catch FlockError.server(404, _) {
            return ""
        }
    }

    public func mail() async throws -> [MailSummary] {
        try await decode([MailSummary]?.self, "GET", "/v1/mail") ?? []
    }

    public func mail(id: String) async throws -> MailDetail {
        try await decode(MailDetail.self, "GET", "/v1/mail/\(escape(id))")
    }

    public func rawMail(id: String) async throws -> String {
        String(decoding: try await send("GET", "/v1/mail/\(escape(id))/raw"), as: UTF8.self)
    }

    public func attachment(mailID: String, index: Int) async throws -> Data {
        try await send("GET", "/v1/mail/\(escape(mailID))/attachments/\(index)")
    }

    public func clearMail(inbox: String? = nil) async throws {
        let query = inbox.map { [URLQueryItem(name: "inbox", value: $0)] } ?? []
        _ = try await send("DELETE", "/v1/mail", query: query)
    }

    public func dumps() async throws -> [Dump] {
        try await decode([Dump]?.self, "GET", "/v1/dumps") ?? []
    }

    /// Symfony's dump <script>/<style> header, shared by all dump bodies.
    public func dumpHeader() async throws -> String {
        String(decoding: try await send("GET", "/v1/dumps/header"), as: UTF8.self)
    }

    public func clearDumps() async throws {
        _ = try await send("DELETE", "/v1/dumps")
    }

    public func logs() async throws -> LogIndex {
        try await decode(LogIndex.self, "GET", "/v1/logs")
    }

    public func serviceLog(_ name: String, lines: Int = 300) async throws -> String {
        let data = try await send("GET", "/v1/logs/services/\(escape(name))", query: [URLQueryItem(name: "lines", value: String(lines))])
        return String(decoding: data, as: UTF8.self)
    }

    public func siteLog(_ name: String, limit: Int = 200) async throws -> [LogEntry] {
        try await decode([LogEntry]?.self, "GET", "/v1/logs/sites/\(escape(name))", query: [URLQueryItem(name: "limit", value: String(limit))]) ?? []
    }

    public func clearSiteLog(_ name: String) async throws {
        _ = try await send("DELETE", "/v1/logs/sites/\(escape(name))")
    }

    // MARK: Transport

    private func decode<T: Decodable>(_ type: T.Type, _ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, timeout: TimeInterval? = nil) async throws -> T {
        let data = try await send(method, path, query: query, body: body, timeout: timeout)
        return try FlockJSON.decoder().decode(T.self, from: data)
    }

    /// Sends a request; on a connection failure it re-reads api.json once and retries,
    /// because a restarted daemon may listen on a new port with a new token.
    private func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, timeout: TimeInterval? = nil) async throws -> Data {
        do {
            return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: false))
        } catch let error as URLError where error.code != .timedOut && error.code != .cancelled {
            config = nil
            do {
                return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: true))
            } catch is URLError {
                throw FlockError.notRunning
            }
        } catch FlockError.server(401, _) {
            config = nil
            return try await attempt(method, path, query: query, body: body, timeout: timeout, config: try loadConfig(force: true))
        }
    }

    private func attempt(_ method: String, _ path: String, query: [URLQueryItem], body: Data?, timeout: TimeInterval?, config: APIConfig) async throws -> Data {
        var components = URLComponents(url: config.url.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query
        }

        var request = URLRequest(url: components.url!)
        request.httpMethod = method
        request.timeoutInterval = timeout ?? Self.defaultTimeout
        request.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let message = (try? JSONDecoder().decode([String: String].self, from: data))?["error"]
                ?? String(decoding: data.prefix(500), as: UTF8.self)
            throw FlockError.server(status: status, message: message)
        }
        return data
    }

    private func loadConfig(force: Bool) throws -> APIConfig {
        if let config, !force {
            return config
        }
        guard let data = try? Data(contentsOf: configPath),
              let loaded = try? JSONDecoder().decode(APIConfig.self, from: data) else {
            throw FlockError.notRunning
        }
        config = loaded
        return loaded
    }

    private func escape(_ segment: String) -> String {
        segment.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? segment
    }
}
