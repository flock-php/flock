import Foundation

/// GET /v1/frankenphp — the FrankenPHP builds, one per PHP minor: installed, published, being
/// installed. A build downloads by itself when a site switched to FrankenPHP needs it.
public struct FrankenPHPState: Decodable, Sendable, Equatable {
    /// FrankenPHP is published for this platform.
    public var available: Bool
    public var checkedAt: Date?
    public var checkError: String
    public var builds: [FrankenPHPBuild]

    /// What a daemon without FrankenPHP (or an older one) answers with.
    public static let unavailable = FrankenPHPState(available: false)

    public init(available: Bool, checkedAt: Date? = nil, checkError: String = "", builds: [FrankenPHPBuild] = []) {
        self.available = available
        self.checkedAt = checkedAt
        self.checkError = checkError
        self.builds = builds
    }

    enum CodingKeys: String, CodingKey { case available, checkedAt, checkError, builds }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? false
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        builds = try c.decodeIfPresent([FrankenPHPBuild]?.self, forKey: .builds).flatMap { $0 } ?? []
    }

    /// A job is running, so the UI polls.
    public var hasRunningJob: Bool {
        builds.contains { $0.job?.isRunning == true }
    }
}

/// The FrankenPHP build for one PHP minor.
public struct FrankenPHPBuild: Decodable, Sendable, Equatable, Identifiable {
    /// The PHP minor, e.g. "8.4".
    public var php: String
    /// The installed FrankenPHP version; empty when not installed.
    public var installed: String
    /// The embedded PHP of the installed build, e.g. "8.4.14".
    public var phpVersion: String
    /// The newest PHP release with a build in the channel, e.g. "8.4.26"; empty when unknown.
    public var latest: String
    public var update: Bool
    /// Download size in bytes.
    public var size: Int64
    /// `latest` is a pre-release (beta channel).
    public var prerelease: Bool
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself
    /// as soon as a good build is published.
    public var yanked: String
    /// The running install or update, or the last failed one until it is dismissed.
    public var job: ComponentJob?

    public var id: String { php }
    public var isInstalled: Bool { !installed.isEmpty }

    enum CodingKeys: String, CodingKey { case php, installed, phpVersion, latest, update, size, prerelease, yanked, job }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        php = try c.decode(String.self, forKey: .php)
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        phpVersion = try c.decodeIfPresent(String.self, forKey: .phpVersion) ?? ""
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        size = try c.decodeIfPresent(Int64.self, forKey: .size) ?? 0
        prerelease = try c.decodeIfPresent(Bool.self, forKey: .prerelease) ?? false
        yanked = try c.decodeIfPresent(String.self, forKey: .yanked) ?? ""
        job = try c.decodeIfPresent(ComponentJob.self, forKey: .job)
    }
}
