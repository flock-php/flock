import Foundation

/// GET /v1/php/versions — every PHP minor Flock knows about (php.net releases from 7.4 up,
/// plus anything installed), with install state and background build jobs.
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

    /// Whether any build is queued or running, so the UI polls faster.
    public var hasActiveJob: Bool {
        versions.contains { $0.job?.isActive == true }
    }
}

/// One PHP minor version (e.g. "8.4") and what can be done with it.
public struct PHPRelease: Decodable, Sendable, Equatable, Identifiable {
    public var minor: String
    public var latest: String
    public var installed: String
    public var isDefault: Bool
    /// Still maintained upstream; false means end of life.
    public var supported: Bool
    public var status: Status
    /// The update is already built, so installing it is instant.
    public var updateReady: Bool
    public var xdebug: Bool
    /// Why a version is unsupported, in plain words; empty otherwise.
    public var note: String
    public var job: PHPBuildJob?

    public var id: String { minor }
    public var isInstalled: Bool { !installed.isEmpty }

    public enum Status: String, Decodable, Sendable {
        case installed, update, ready, likely, unsupported, failed, unknown

        public init(from decoder: Decoder) throws {
            self = Status(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    enum CodingKeys: String, CodingKey {
        case minor, latest, installed, supported, status, updateReady, xdebug, note, job
        case isDefault = "default"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        minor = try c.decode(String.self, forKey: .minor)
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        supported = try c.decodeIfPresent(Bool.self, forKey: .supported) ?? true
        status = try c.decodeIfPresent(Status.self, forKey: .status) ?? .unknown
        updateReady = try c.decodeIfPresent(Bool.self, forKey: .updateReady) ?? false
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        note = try c.decodeIfPresent(String.self, forKey: .note) ?? ""
        job = try c.decodeIfPresent(PHPBuildJob.self, forKey: .job)
    }
}

/// A build of one PHP version, queued or run by the daemon.
public struct PHPBuildJob: Decodable, Sendable, Equatable {
    public var state: State
    /// Installs the result when the build succeeds.
    public var install: Bool
    /// Started by Flock in the background rather than by the user.
    public var auto: Bool
    /// The latest line of the build log, for a one-line progress text.
    public var step: String
    public var fullVersion: String
    public var startedAt: Date?
    public var finishedAt: Date?
    public var error: String

    public enum State: String, Decodable, Sendable {
        case queued, running, succeeded, failed, unknown

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    public var isActive: Bool { state == .queued || state == .running }

    enum CodingKeys: String, CodingKey { case state, install, auto, step, fullVersion, startedAt, finishedAt, error }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
        install = try c.decodeIfPresent(Bool.self, forKey: .install) ?? false
        auto = try c.decodeIfPresent(Bool.self, forKey: .auto) ?? false
        step = try c.decodeIfPresent(String.self, forKey: .step) ?? ""
        fullVersion = try c.decodeIfPresent(String.self, forKey: .fullVersion) ?? ""
        startedAt = try c.decodeIfPresent(Date.self, forKey: .startedAt)
        finishedAt = try c.decodeIfPresent(Date.self, forKey: .finishedAt)
        error = try c.decodeIfPresent(String.self, forKey: .error) ?? ""
    }
}
