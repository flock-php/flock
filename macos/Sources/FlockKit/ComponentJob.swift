import Foundation

/// An install or update running in the daemon (PHP, FrankenPHP, a catalog service), or a failed one
/// until it is dismissed; finished jobs disappear.
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
        case downloading, installing, unknown

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
