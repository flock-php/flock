import Foundation

/// GET /v1/php/versions — every PHP minor the channel's manifest offers, plus anything installed,
/// with install state and install jobs.
public struct PHPCatalog: Decodable, Sendable, Equatable {
    public var checkedAt: Date?
    public var checkError: String
    public var versions: [PHPRelease]

    enum CodingKeys: String, CodingKey { case checkedAt, checkError, versions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        versions = try c.decodeIfPresent([PHPRelease].self, forKey: .versions) ?? []
    }

    /// Whether any install runs, so the UI polls faster.
    public var hasActiveJob: Bool {
        versions.contains { $0.job?.isRunning == true }
    }
}

/// One PHP minor version (e.g. "8.4") and what can be done with it.
public struct PHPRelease: Decodable, Sendable, Equatable, Identifiable {
    public var minor: String
    /// The newest release in the channel, e.g. "8.4.26"; empty when the channel has none.
    public var latest: String
    public var installed: String
    public var isDefault: Bool
    public var status: Status
    /// `latest` (or a newer build of it) can be installed.
    public var update: Bool
    /// Download size of `latest` in bytes.
    public var size: Int64
    /// `latest` is a pre-release (beta channel).
    public var prerelease: Bool
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself as
    /// soon as a good build is published.
    public var yanked: String
    public var xdebug: Bool
    /// The running install or update, or the last failed one until it is dismissed.
    public var job: ComponentJob?

    public var id: String { minor }
    public var isInstalled: Bool { !installed.isEmpty }

    public enum Status: String, Decodable, Sendable {
        case installed, update, available, unknown

        public init(from decoder: Decoder) throws {
            self = Status(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    enum CodingKeys: String, CodingKey {
        case minor, latest, installed, status, update, size, prerelease, yanked, xdebug, job
        case isDefault = "default"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        minor = try c.decode(String.self, forKey: .minor)
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        status = try c.decodeIfPresent(Status.self, forKey: .status) ?? .unknown
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        size = try c.decodeIfPresent(Int64.self, forKey: .size) ?? 0
        prerelease = try c.decodeIfPresent(Bool.self, forKey: .prerelease) ?? false
        yanked = try c.decodeIfPresent(String.self, forKey: .yanked) ?? ""
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        job = try c.decodeIfPresent(ComponentJob.self, forKey: .job)
    }
}
