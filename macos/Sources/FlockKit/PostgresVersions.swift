import Foundation

/// GET /v1/postgres/versions — the PostgreSQL majors the channel offers (plus any installed or with
/// databases), which are installed, and the one that runs. Only one major runs at a time; each keeps
/// its own databases. psql, pg_dump and the other client tools come with every installed major.
public struct PostgresVersions: Decodable, Sendable, Equatable {
    /// The major selected to run ("18"); empty when none is installed.
    public var active: String
    /// The postgres process is up.
    public var running: Bool
    /// Not switched off by the user.
    public var enabled: Bool
    public var port: Int
    public var checkedAt: Date?
    public var checkError: String
    /// Newest first.
    public var majors: [PostgresMajor]

    enum CodingKeys: String, CodingKey { case active, running, enabled, port, checkedAt, checkError, majors }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        active = try c.decodeIfPresent(String.self, forKey: .active) ?? ""
        running = try c.decodeIfPresent(Bool.self, forKey: .running) ?? false
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        majors = try c.decodeIfPresent([PostgresMajor]?.self, forKey: .majors).flatMap { $0 } ?? []
    }

    /// The major that runs (or would, when stopped).
    public var activeMajor: PostgresMajor? {
        majors.first { $0.major == active && $0.isInstalled }
    }

    public var hasInstalled: Bool { majors.contains(where: \.isInstalled) }

    /// An operation runs on some major (maybe started by another window or the CLI), so the UI polls.
    public var isBusy: Bool { majors.contains(where: \.isBusy) }

    /// Some installed major has a newer minor.
    public var hasUpdate: Bool { majors.contains { $0.isInstalled && $0.update } }
}

/// One PostgreSQL major line (e.g. "18") and what is installed of it.
public struct PostgresMajor: Decodable, Sendable, Equatable, Identifiable {
    public var major: String
    /// The installed release, e.g. "18.6"; empty when not installed.
    public var installed: String
    /// The newest release for this platform; empty when unknown.
    public var latest: String
    /// A newer release (or a newer build of the installed one) can be installed.
    public var update: Bool
    /// `latest` is a pre-release (beta channel).
    public var prerelease: Bool
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself as
    /// soon as a good build is published.
    public var yanked: String
    public var active: Bool
    /// A data directory (cluster) exists for this major, even when it isn't installed (any more).
    public var hasData: Bool
    /// Bytes of the data directory.
    public var dataSize: Int64
    /// Upstream end of life, "2030-11-14"; empty when unknown.
    public var eol: String
    public var busy: Busy

    public var id: String { major }
    public var isInstalled: Bool { !installed.isEmpty }
    public var isBusy: Bool { busy != .idle }

    /// An operation in flight on this major.
    public enum Busy: Sendable, Equatable {
        case idle, installing, updating, switching, uninstalling
        /// A state this app doesn't know yet; still busy, described generically.
        case other(String)

        public init(_ raw: String) {
            switch raw {
            case "": self = .idle
            case "installing": self = .installing
            case "updating": self = .updating
            case "switching": self = .switching
            case "uninstalling": self = .uninstalling
            default: self = .other(raw)
            }
        }
    }

    enum CodingKeys: String, CodingKey { case major, installed, latest, update, prerelease, yanked, active, hasData, dataSize, eol, busy }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        major = try c.decode(String.self, forKey: .major)
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        prerelease = try c.decodeIfPresent(Bool.self, forKey: .prerelease) ?? false
        yanked = try c.decodeIfPresent(String.self, forKey: .yanked) ?? ""
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? false
        hasData = try c.decodeIfPresent(Bool.self, forKey: .hasData) ?? false
        dataSize = try c.decodeIfPresent(Int64.self, forKey: .dataSize) ?? 0
        eol = try c.decodeIfPresent(String.self, forKey: .eol) ?? ""
        busy = Busy(try c.decodeIfPresent(String.self, forKey: .busy) ?? "")
    }

    /// The end-of-life date, parsed from `eol`.
    public var eolDate: Date? {
        guard !eol.isEmpty else { return nil }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.date(from: eol)
    }

    public enum Support: Equatable, Sendable {
        case unknown
        /// Maintained until the date, more than ~6 months away.
        case supported(until: Date)
        /// End of life within ~6 months.
        case endingSoon(Date)
        case ended(Date)
    }

    /// Where the major stands upstream at `now`; "soon" is within 182 days.
    public func support(now: Date = Date()) -> Support {
        guard let date = eolDate else { return .unknown }
        if date <= now {
            return .ended(date)
        }
        return date.timeIntervalSince(now) < 182 * 86_400 ? .endingSoon(date) : .supported(until: date)
    }
}
