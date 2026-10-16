import Foundation

/// GET /v1/config — the persisted configuration plus what is installed.
public struct FlockConfig: Decodable, Sendable, Equatable {
    public var tld: String
    public var defaultPHP: String
    public var defaultNode: String
    public var xdebug: Bool
    public var dumps: Bool
    public var phpAutoBuild: Bool
    public var root: String
    public var ports: Ports
    public var parked: [String]
    public var php: [PHPVersion]
    public var node: [String]

    public struct Ports: Decodable, Sendable, Equatable {
        public var http: Int
        public var https: Int
        public var dns: Int
        public var smtp: Int
        public var postgres: Int
        public var valkey: Int
        public var dumps: Int
        public var api: Int

        enum CodingKeys: String, CodingKey { case http, https, dns, smtp, postgres, valkey, dumps, api }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            func port(_ key: CodingKeys) throws -> Int { try c.decodeIfPresent(Int.self, forKey: key) ?? 0 }
            http = try port(.http)
            https = try port(.https)
            dns = try port(.dns)
            smtp = try port(.smtp)
            postgres = try port(.postgres)
            valkey = try port(.valkey)
            dumps = try port(.dumps)
            api = try port(.api)
        }
    }

    public struct PHPVersion: Decodable, Sendable, Equatable, Identifiable {
        public var version: String
        public var fullVersion: String
        public var ini: String
        public var xdebugAvailable: Bool

        public var id: String { version }

        enum CodingKeys: String, CodingKey { case version, fullVersion, ini, xdebugAvailable }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            version = try c.decode(String.self, forKey: .version)
            fullVersion = try c.decodeIfPresent(String.self, forKey: .fullVersion) ?? version
            ini = try c.decodeIfPresent(String.self, forKey: .ini) ?? ""
            xdebugAvailable = try c.decodeIfPresent(Bool.self, forKey: .xdebugAvailable) ?? false
        }
    }

    // convertFromSnakeCase turns default_php into defaultPhp, so these keys are spelled out.
    enum CodingKeys: String, CodingKey {
        case tld, xdebug, dumps, root, ports, parked, php, node
        case defaultPHP = "defaultPhp"
        case phpAutoBuild = "phpAutoBuild"
        case defaultNode
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tld = try c.decodeIfPresent(String.self, forKey: .tld) ?? "test"
        defaultPHP = try c.decodeIfPresent(String.self, forKey: .defaultPHP) ?? ""
        defaultNode = try c.decodeIfPresent(String.self, forKey: .defaultNode) ?? ""
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        dumps = try c.decodeIfPresent(Bool.self, forKey: .dumps) ?? false
        phpAutoBuild = try c.decodeIfPresent(Bool.self, forKey: .phpAutoBuild) ?? true
        root = try c.decodeIfPresent(String.self, forKey: .root) ?? ""
        ports = try c.decode(Ports.self, forKey: .ports)
        parked = try c.decodeIfPresent([String].self, forKey: .parked) ?? []
        php = try c.decodeIfPresent([PHPVersion].self, forKey: .php) ?? []
        node = try c.decodeIfPresent([String].self, forKey: .node) ?? []
    }

    /// Node majors that are installed, newest first ("22.23.3" → "22").
    public var nodeMajors: [String] {
        var seen = Set<String>()
        return node.compactMap { $0.split(separator: ".").first.map(String.init) }
            .filter { seen.insert($0).inserted }
    }
}

/// GET /v1/services — one background service (web server, database, mail catcher, ...).
public struct Service: Decodable, Sendable, Equatable, Identifiable {
    public var id: String
    public var name: String
    public var category: String
    /// What it is, for services installed on request; may be empty.
    public var description: String
    /// False for a service Flock can't run on this platform; `unavailable` says why.
    public var available: Bool
    public var unavailable: String
    public var version: String
    public var port: Int
    public var installed: Bool
    public var enabled: Bool
    public var running: Bool
    public var stoppable: Bool
    public var env: String
    public var log: String
    public var url: String
    /// Why it isn't working, when that isn't simply "stopped".
    public var problem: String

    enum CodingKeys: String, CodingKey {
        case id, name, category, description, available, unavailable, version, port, installed, enabled, running, stoppable, env, log, url, problem
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? "Other"
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? true
        unavailable = try c.decodeIfPresent(String.self, forKey: .unavailable) ?? ""
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        running = try c.decodeIfPresent(Bool.self, forKey: .running) ?? false
        stoppable = try c.decodeIfPresent(Bool.self, forKey: .stoppable) ?? false
        env = try c.decodeIfPresent(String.self, forKey: .env) ?? ""
        log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        problem = try c.decodeIfPresent(String.self, forKey: .problem) ?? ""
    }

    /// Is this a database that a GUI client like TablePlus can open?
    public var isDatabase: Bool { category == "Database" && !url.isEmpty }
}

/// Body of POST /v1/settings; nil fields are left out and stay unchanged.
public struct SettingsChange: Encodable, Sendable {
    public var xdebug: Bool?
    public var xdebugBreakpoints: Bool?
    public var dumps: Bool?
    public var defaultPHP: String?
    public var defaultNode: String?
    public var phpAutoBuild: Bool?

    public init(xdebug: Bool? = nil, xdebugBreakpoints: Bool? = nil, dumps: Bool? = nil, defaultPHP: String? = nil, defaultNode: String? = nil, phpAutoBuild: Bool? = nil) {
        self.xdebug = xdebug
        self.xdebugBreakpoints = xdebugBreakpoints
        self.dumps = dumps
        self.defaultPHP = defaultPHP
        self.defaultNode = defaultNode
        self.phpAutoBuild = phpAutoBuild
    }

    enum CodingKeys: String, CodingKey {
        case xdebug, dumps
        case xdebugBreakpoints = "xdebug_breakpoints"
        case defaultPHP = "default_php"
        case defaultNode = "default_node"
        case phpAutoBuild = "php_auto_build"
    }
}

/// Body of PATCH /v1/sites/{name}; nil fields are left out. An empty `php` means "use the default".
/// `server` is "fastcgi" or "frankenphp", `worker` "" (classic) or "octane".
public struct SiteChange: Encodable, Sendable {
    public var secured: Bool?
    public var php: String?
    public var node: String?
    public var server: String?
    public var worker: String?

    public init(secured: Bool? = nil, php: String? = nil, node: String? = nil, server: String? = nil, worker: String? = nil) {
        self.secured = secured
        self.php = php
        self.node = node
        self.server = server
        self.worker = worker
    }
}
