import Foundation

/// GET /v1/catalog — the services Flock installs on request besides PostgreSQL and Valkey:
/// MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense and RustFS. Their lines install side by
/// side, and one of them (the active one) runs on the service's port.
public struct ServiceCatalog: Decodable, Sendable, Equatable {
    public var checkedAt: Date?
    public var checkError: String
    public var services: [CatalogService]

    enum CodingKeys: String, CodingKey { case checkedAt, checkError, services }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        services = try c.decodeIfPresent([CatalogService]?.self, forKey: .services).flatMap { $0 } ?? []
    }

    public subscript(id: String) -> CatalogService? {
        services.first { $0.id == id }
    }

    /// An install runs, so the UI polls.
    public var hasRunningJob: Bool {
        services.contains { $0.job?.isRunning == true }
    }
}

/// One catalog service with its install state.
public struct CatalogService: Decodable, Sendable, Equatable, Identifiable {
    public var id: String
    public var name: String
    public var category: String
    public var description: String
    public var available: Bool
    /// Why it isn't available on this platform.
    public var unavailable: String

    /// How long installing takes, e.g. "about 180 MB download".
    public var estimate: String
    /// The active version; empty when none.
    public var installed: String
    /// The line of the active version ("8.4").
    public var line: String
    /// The newest release of `line`; empty when unknown.
    public var latest: String
    public var update: Bool
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself.
    public var yanked: String
    /// The configured port, or the one Flock suggests when not installed.
    public var port: Int
    public var defaultPort: Int
    public var lines: [CatalogLine]
    /// The running install, or the last failed one until it is dismissed.
    public var job: CatalogJob?

    public var isInstalled: Bool { !installed.isEmpty }

    /// The active line, else the newest.
    public var preferredLine: CatalogLine? {
        lines.first { $0.active } ?? lines.first
    }

    enum CodingKeys: String, CodingKey {
        case id, name, category, description, available, unavailable, estimate, installed, line, latest, update, yanked, port, defaultPort, lines, job
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? "Other"
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? true
        unavailable = try c.decodeIfPresent(String.self, forKey: .unavailable) ?? ""
        estimate = try c.decodeIfPresent(String.self, forKey: .estimate) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        line = try c.decodeIfPresent(String.self, forKey: .line) ?? ""
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        yanked = try c.decodeIfPresent(String.self, forKey: .yanked) ?? ""
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
        defaultPort = try c.decodeIfPresent(Int.self, forKey: .defaultPort) ?? port
        lines = try c.decodeIfPresent([CatalogLine]?.self, forKey: .lines).flatMap { $0 } ?? []
        job = try c.decodeIfPresent(CatalogJob.self, forKey: .job)
    }
}

/// A release line of a catalog service ("8.4 LTS").
public struct CatalogLine: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public var id: String
    public var label: String
    /// The newest release; empty when unknown.
    public var latest: String
    /// Installed (lines install side by side; one of them is active).
    public var installed: Bool
    /// The installed version; empty when not installed.
    public var version: String
    /// The installed version is the one that runs.
    public var active: Bool
    /// `latest` (or a newer build of it) can replace `version`.
    public var update: Bool
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself.
    public var yanked: String
    /// The line has data (each line keeps its own).
    public var hasData: Bool

    enum CodingKeys: String, CodingKey { case id, label, latest, installed, version, active, update, yanked, hasData }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? id
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? false
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        yanked = try c.decodeIfPresent(String.self, forKey: .yanked) ?? ""
        hasData = try c.decodeIfPresent(Bool.self, forKey: .hasData) ?? false
    }
}

/// An install of a catalog service, running or failed (finished installs disappear).
public struct CatalogJob: Decodable, Sendable, Equatable {
    public var job: ComponentJob
    /// What it installs; empty until the newest release is known.
    public var version: String
    public var line: String

    public var isRunning: Bool { job.isRunning }
    public var isFailed: Bool { job.isFailed }

    enum CodingKeys: String, CodingKey { case version, line }

    public init(from decoder: Decoder) throws {
        job = try ComponentJob(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        line = try c.decodeIfPresent(String.self, forKey: .line) ?? ""
    }
}
