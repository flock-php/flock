import Foundation

/// How Flock set up a git worktree it serves under its site (see `Status.Site.worktree`).
public struct WorktreeSetup: Decodable, Sendable, Equatable {
    public var state: State
    /// What a running setup does now, e.g. "composer install".
    public var step: String
    /// Why the setup failed.
    public var error: String?
    /// The name of its service log.
    public var log: String
    /// The database mode asked for; nil leaves it to the project's flock.yml.
    public var dbMode: DatabaseMode?
    /// The worktree's own database; empty when it uses the site's.
    public var database: String

    public enum State: String, Decodable, Sendable {
        case queued, running, ready, failed, unknown

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    /// The setup is waiting or running.
    public var busy: Bool { state == .queued || state == .running }

    enum CodingKeys: String, CodingKey {
        case state, step, error, log, dbMode, database
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
        step = try c.decodeIfPresent(String.self, forKey: .step) ?? ""
        error = try c.decodeIfPresent(String.self, forKey: .error)
        log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        dbMode = (try c.decodeIfPresent(String.self, forKey: .dbMode)).flatMap(DatabaseMode.init(rawValue:))
        database = try c.decodeIfPresent(String.self, forKey: .database) ?? ""
    }
}

/// A worktree's database: the site's, a copy of it, or an empty one with the migrations run.
public enum DatabaseMode: String, Codable, Sendable, CaseIterable, Identifiable {
    case shared, copy, fresh

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .shared: "The site's"
        case .copy: "A copy of the site's"
        case .fresh: "A new, empty one"
        }
    }
}

/// A worktree whose folder is gone, whose database Flock kept.
public struct LeftWorktree: Decodable, Sendable, Equatable, Identifiable {
    /// Where the worktree was; names it for removal.
    public var path: String
    public var site: String
    public var database: String

    public var id: String { path }

    enum CodingKeys: String, CodingKey {
        case path, site, database
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        site = try c.decodeIfPresent(String.self, forKey: .site) ?? ""
        database = try c.decodeIfPresent(String.self, forKey: .database) ?? ""
    }
}

/// A new worktree: its folder, and its setup.
public struct WorktreeCreated: Decodable, Sendable {
    public var path: String
    public var setup: WorktreeSetup
}

extension Status.Site {
    /// A git worktree of another site (`parent`).
    public var isWorktree: Bool { parent != nil }

    /// What a worktree is called: its branch, else the name it's served under.
    public var worktreeLabel: String {
        if !branch.isEmpty { return branch }
        return name.split(separator: ".").first.map(String.init) ?? name
    }
}

extension Array where Element == Status.Site {
    /// The sites without the worktrees, which go under their site (see `worktrees(of:)`).
    public var topLevel: [Status.Site] { filter { !$0.isWorktree } }

    /// The worktrees of the site named name.
    public func worktrees(of name: String) -> [Status.Site] { filter { $0.parent == name } }

    /// The sites as lists show them: each followed by its worktrees.
    public var nested: [Status.Site] { topLevel.flatMap { [$0] + worktrees(of: $0.name) } }
}
