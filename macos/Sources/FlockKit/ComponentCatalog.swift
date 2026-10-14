import Foundation

/// GET /v1/components — software Flock installs on request: Valkey and the PostgreSQL
/// command-line tools. On macOS they are built from source in a background job.
public struct ComponentCatalog: Decodable, Sendable, Equatable {
    public var checkedAt: Date?
    public var checkError: String
    public var components: [ServiceComponent]

    enum CodingKeys: String, CodingKey { case checkedAt, checkError, components }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        components = try c.decodeIfPresent([ServiceComponent]?.self, forKey: .components).flatMap { $0 } ?? []
    }

    public subscript(id: String) -> ServiceComponent? {
        components.first { $0.id == id }
    }

    /// A job is running, so the UI polls.
    public var hasRunningJob: Bool {
        components.contains { $0.job?.isRunning == true }
    }
}

/// One installable component ("valkey", "postgres-client").
public struct ServiceComponent: Decodable, Sendable, Equatable, Identifiable {
    public static let valkey = "valkey"
    public static let postgresClient = "postgres-client"

    public var id: String
    public var name: String
    /// Short, e.g. the programs it brings; may be empty.
    public var description: String
    /// Empty when not installed.
    public var installed: String
    /// Empty when unknown.
    public var latest: String
    public var update: Bool
    public var method: Method
    /// How long installing takes, e.g. "about 5 minutes" or "12 MB download".
    public var estimate: String
    /// The running job, or the last failed one until it is dismissed.
    public var job: ComponentJob?

    public var isInstalled: Bool { !installed.isEmpty }

    public enum Method: String, Decodable, Sendable {
        /// Compiled from source (macOS).
        case build
        case download
        case unknown

        public init(from decoder: Decoder) throws {
            self = Method(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    enum CodingKeys: String, CodingKey { case id, name, description, installed, latest, update, method, estimate, job }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        method = try c.decodeIfPresent(Method.self, forKey: .method) ?? .unknown
        estimate = try c.decodeIfPresent(String.self, forKey: .estimate) ?? ""
        job = try c.decodeIfPresent(ComponentJob.self, forKey: .job)
    }
}

/// An install or update of a component, running or failed (finished jobs disappear).
public struct ComponentJob: Decodable, Sendable, Equatable {
    public var action: Action
    public var state: State
    public var phase: Phase
    /// 0...1, or nil when unknown (builds).
    public var progress: Double?
    public var error: String
    public var startedAt: Date?

    public var isRunning: Bool { state == .running }
    public var isFailed: Bool { state == .failed }

    public enum Action: String, Decodable, Sendable {
        case install, update, unknown

        public init(from decoder: Decoder) throws {
            self = Action(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    public enum State: String, Decodable, Sendable {
        case running, failed, unknown

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    public enum Phase: String, Decodable, Sendable {
        case downloading, building, installing, unknown

        public init(from decoder: Decoder) throws {
            self = Phase(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    enum CodingKeys: String, CodingKey { case action, state, phase, progress, error, startedAt }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        action = try c.decodeIfPresent(Action.self, forKey: .action) ?? .unknown
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
        phase = try c.decodeIfPresent(Phase.self, forKey: .phase) ?? .unknown
        let raw = try c.decodeIfPresent(Double.self, forKey: .progress) ?? -1
        progress = raw < 0 ? nil : min(raw, 1)
        error = try c.decodeIfPresent(String.self, forKey: .error) ?? ""
        startedAt = try c.decodeIfPresent(Date.self, forKey: .startedAt)
    }
}
