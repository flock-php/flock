import Foundation

/// GET /v1/status
public struct Status: Decodable, Sendable, Equatable {
    public var version: String
    public var pid: Int
    public var startedAt: Date
    public var dns: DNSStatus
    public var httpPort: Int
    public var httpsPort: Int
    public var mail: String
    public var postgres: String?
    /// "9.1.2 on 127.0.0.1:6379" when Valkey is installed.
    public var valkey: String?
    /// The installed catalog services (MySQL, Redis, ...).
    public var extras: [Extra]
    /// Xdebug detection: requests that ask for Xdebug go to a PHP pool that loads it.
    public var xdebug: Bool
    /// Sites with PhpStorm breakpoints send every request to Xdebug.
    public var xdebugBreakpoints: Bool
    public var dumps: Bool
    public var mailCount: Int
    public var dumpCount: Int
    public var sites: [Site]
    public var processes: [Process]
    public var warnings: [String]

    public struct DNSStatus: Decodable, Sendable, Equatable {
        public var tld: String
        public var addr: String
        /// Every name under the TLD resolves (any subdomain). Always true on macOS while the DNS
        /// server runs; on Windows a company DNS policy can override Flock's rule.
        public var wildcard: Bool
        public var wildcardProblem: String
        /// Subdomain levels below a site (and its aliases) that are served.
        public var maxDepth: Int

        public init(tld: String, addr: String, wildcard: Bool = false, wildcardProblem: String = "", maxDepth: Int = 4) {
            self.tld = tld
            self.addr = addr
            self.wildcard = wildcard
            self.wildcardProblem = wildcardProblem
            self.maxDepth = maxDepth
        }

        enum CodingKeys: String, CodingKey {
            case tld, addr, wildcard, wildcardProblem, maxDepth
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            tld = try c.decodeIfPresent(String.self, forKey: .tld) ?? ""
            addr = try c.decodeIfPresent(String.self, forKey: .addr) ?? ""
            wildcard = try c.decodeIfPresent(Bool.self, forKey: .wildcard) ?? !addr.isEmpty
            wildcardProblem = try c.decodeIfPresent(String.self, forKey: .wildcardProblem) ?? ""
            maxDepth = try c.decodeIfPresent(Int.self, forKey: .maxDepth) ?? 4
        }
    }

    /// One of a site's extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test").
    public struct SiteDomain: Decodable, Sendable, Equatable, Identifiable {
        /// As stored, and as the API takes it for removal.
        public var entry: String
        /// The full name, e.g. tenant1.app.test.
        public var host: String
        public var alias: Bool
        public var url: String?
        /// Why it isn't served, e.g. another site uses it.
        public var problem: String?

        public var id: String { entry }
    }

    public struct Site: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var domain: String
        public var path: String
        /// The PHP version the site uses; `phpSource` says why.
        public var php: String
        public var phpSource: VersionSource
        /// What the project pins (in `phpFile`), also when an isolation wins over it; empty when nothing is pinned.
        public var phpPinned: String
        public var phpFile: String
        /// The Node.js version (usually a major) the site uses; `nodeSource` says why.
        public var node: String?
        public var nodeSource: VersionSource
        public var nodePinned: String
        public var nodeFile: String
        /// A .nvmrc / .node-version that .flockrc's node= wins over, e.g. ".nvmrc: 20".
        public var nodeShadowed: String
        public var secured: Bool
        public var parked: Bool
        public var url: String?
        public var problem: String?
        public var domains: [SiteDomain]
        /// PhpStorm breakpoints send every request of the site to Xdebug.
        public var debugging: Bool

        public var id: String { name }

        /// The file pinning PHP sits in the site's own folder (so it can be unpinned there).
        public var phpPinnedHere: Bool { !phpFile.isEmpty && Self.inFolder(phpFile, path) }
        public var nodePinnedHere: Bool { !nodeFile.isEmpty && Self.inFolder(nodeFile, path) }

        private static func inFolder(_ file: String, _ folder: String) -> Bool {
            URL(fileURLWithPath: file).deletingLastPathComponent().standardizedFileURL.path == URL(fileURLWithPath: folder).standardizedFileURL.path
        }

        enum CodingKeys: String, CodingKey {
            case name, domain, path, php, phpSource, phpPinned, phpFile, node, nodeSource, nodePinned, nodeFile, nodeShadowed
            case secured, parked, url, problem, domains, debugging
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.decode(String.self, forKey: .name)
            domain = try c.decodeIfPresent(String.self, forKey: .domain) ?? ""
            path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
            php = try c.decodeIfPresent(String.self, forKey: .php) ?? ""
            phpSource = try c.decodeIfPresent(VersionSource.self, forKey: .phpSource) ?? .default
            phpPinned = try c.decodeIfPresent(String.self, forKey: .phpPinned) ?? ""
            phpFile = try c.decodeIfPresent(String.self, forKey: .phpFile) ?? ""
            node = try c.decodeIfPresent(String.self, forKey: .node)
            nodeSource = try c.decodeIfPresent(VersionSource.self, forKey: .nodeSource) ?? .default
            nodePinned = try c.decodeIfPresent(String.self, forKey: .nodePinned) ?? ""
            nodeFile = try c.decodeIfPresent(String.self, forKey: .nodeFile) ?? ""
            nodeShadowed = try c.decodeIfPresent(String.self, forKey: .nodeShadowed) ?? ""
            secured = try c.decodeIfPresent(Bool.self, forKey: .secured) ?? false
            parked = try c.decodeIfPresent(Bool.self, forKey: .parked) ?? false
            url = try c.decodeIfPresent(String.self, forKey: .url)
            problem = try c.decodeIfPresent(String.self, forKey: .problem)
            domains = try c.decodeIfPresent([SiteDomain].self, forKey: .domains) ?? []
            debugging = try c.decodeIfPresent(Bool.self, forKey: .debugging) ?? false
        }
    }

    /// Where a site's PHP or Node.js version comes from, in this order: set for the site in Flock
    /// on this machine (`flock isolate`), pinned in the project (`flock pin`), Flock's default.
    public enum VersionSource: String, Decodable, Sendable {
        case isolated, project, `default`

        public init(from decoder: Decoder) throws {
            self = VersionSource(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .default
        }
    }

    public struct Process: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var up: Bool
        public var restarts: Int
        public var lastError: String?

        public var id: String { name }
    }

    /// An installed catalog service, for the menu bar panel.
    public struct Extra: Decodable, Sendable, Equatable, Identifiable {
        public var id: String
        public var name: String
        public var version: String
        public var port: Int
        public var enabled: Bool
        public var up: Bool

        enum CodingKeys: String, CodingKey { case id, name, version, port, enabled, up }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = try c.decode(String.self, forKey: .id)
            name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
            version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
            port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 0
            enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
            up = try c.decodeIfPresent(Bool.self, forKey: .up) ?? false
        }
    }

    /// Something needs attention: a warning, or a supervised process that is down.
    public var isDegraded: Bool {
        !warnings.isEmpty || processes.contains { !$0.up }
    }

    enum CodingKeys: String, CodingKey {
        case version, pid, startedAt, dns, httpPort, httpsPort, mail, postgres, valkey, extras
        case xdebug, xdebugBreakpoints, dumps, mailCount, dumpCount, sites, processes, warnings
    }

    // Go omits empty slices (`omitempty`) or sends null; treat both as empty.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decode(String.self, forKey: .version)
        pid = try c.decode(Int.self, forKey: .pid)
        startedAt = try c.decode(Date.self, forKey: .startedAt)
        dns = try c.decodeIfPresent(DNSStatus.self, forKey: .dns) ?? DNSStatus(tld: "", addr: "")
        httpPort = try c.decodeIfPresent(Int.self, forKey: .httpPort) ?? 0
        httpsPort = try c.decodeIfPresent(Int.self, forKey: .httpsPort) ?? 0
        mail = try c.decodeIfPresent(String.self, forKey: .mail) ?? ""
        postgres = try c.decodeIfPresent(String.self, forKey: .postgres)
        valkey = try c.decodeIfPresent(String.self, forKey: .valkey)
        extras = try c.decodeIfPresent([Extra].self, forKey: .extras) ?? []
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
        xdebugBreakpoints = try c.decodeIfPresent(Bool.self, forKey: .xdebugBreakpoints) ?? false
        dumps = try c.decodeIfPresent(Bool.self, forKey: .dumps) ?? false
        mailCount = try c.decodeIfPresent(Int.self, forKey: .mailCount) ?? 0
        dumpCount = try c.decodeIfPresent(Int.self, forKey: .dumpCount) ?? 0
        sites = try c.decodeIfPresent([Site].self, forKey: .sites) ?? []
        processes = try c.decodeIfPresent([Process].self, forKey: .processes) ?? []
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings) ?? []
    }
}

/// An entry of GET /v1/mail.
public struct MailSummary: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public var id: String
    public var inbox: String
    public var from: String
    public var to: [String]
    public var subject: String
    public var date: Date
    public var size: Int

    enum CodingKeys: String, CodingKey { case id, inbox, from, to, subject, date, size }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        inbox = try c.decodeIfPresent(String.self, forKey: .inbox) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        to = try c.decodeIfPresent([String].self, forKey: .to) ?? []
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        date = try c.decodeIfPresent(Date.self, forKey: .date) ?? .distantPast
        size = try c.decodeIfPresent(Int.self, forKey: .size) ?? 0
    }
}

/// GET /v1/mail/{id}
public struct MailDetail: Decodable, Sendable, Equatable {
    public var id: String
    public var inbox: String
    public var from: String
    public var to: [String]
    public var cc: [String]
    public var subject: String
    public var date: Date
    public var html: String
    public var text: String
    public var headers: [Header]
    public var attachments: [Attachment]

    public struct Header: Decodable, Sendable, Equatable, Hashable {
        public var name: String
        public var value: String
    }

    public struct Attachment: Decodable, Sendable, Equatable, Identifiable {
        public var index: Int
        public var filename: String
        public var contentType: String
        public var size: Int

        public var id: Int { index }
    }

    enum CodingKeys: String, CodingKey {
        case id, inbox, from, to, cc, subject, date, html, text, headers, attachments
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        inbox = try c.decodeIfPresent(String.self, forKey: .inbox) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        to = try c.decodeIfPresent([String].self, forKey: .to) ?? []
        cc = try c.decodeIfPresent([String].self, forKey: .cc) ?? []
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        date = try c.decodeIfPresent(Date.self, forKey: .date) ?? .distantPast
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        headers = try c.decodeIfPresent([Header].self, forKey: .headers) ?? []
        attachments = try c.decodeIfPresent([Attachment].self, forKey: .attachments) ?? []
    }
}

/// An entry of GET /v1/dumps.
public struct Dump: Decodable, Sendable, Equatable, Identifiable, Hashable {
    public var id: String
    public var time: Date
    public var site: String
    public var source: Source?
    public var request: Request?
    public var commandLine: String?
    /// Shared by every dump of one HTTP request or CLI run.
    public var group: String
    public var label: String?
    public var text: String
    /// Dump body only; render it after the shared header from `FlockClient.dumpHeader()`.
    public var html: String

    public struct Source: Decodable, Sendable, Equatable, Hashable {
        public var file: String
        public var line: Int
        public var name: String

        enum CodingKeys: String, CodingKey { case file, line, name }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            file = try c.decodeIfPresent(String.self, forKey: .file) ?? ""
            line = try c.decodeIfPresent(Int.self, forKey: .line) ?? 0
            name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        }
    }

    public struct Request: Decodable, Sendable, Equatable, Hashable {
        public var method: String
        public var uri: String
    }

    /// "Controller.php:42" — the last path components keep list rows short.
    public var shortSource: String? {
        guard let source, !source.file.isEmpty else { return nil }
        let parts = source.file.split(separator: "/").suffix(2).joined(separator: "/")
        return source.line > 0 ? "\(parts):\(source.line)" : parts
    }

    enum CodingKeys: String, CodingKey { case id, time, site, source, request, commandLine, group, label, text, html }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        time = try c.decodeIfPresent(Date.self, forKey: .time) ?? .distantPast
        site = try c.decodeIfPresent(String.self, forKey: .site) ?? ""
        source = try c.decodeIfPresent(Source.self, forKey: .source)
        request = try c.decodeIfPresent(Request.self, forKey: .request)
        commandLine = try c.decodeIfPresent(String.self, forKey: .commandLine)
        group = try c.decodeIfPresent(String.self, forKey: .group) ?? ""
        label = try c.decodeIfPresent(String.self, forKey: .label)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
    }
}

/// GET /v1/logs
public struct LogIndex: Decodable, Sendable, Equatable {
    public var services: [String]
    public var sites: [String]

    enum CodingKeys: String, CodingKey { case services, sites }

    public init(services: [String] = [], sites: [String] = []) {
        self.services = services
        self.sites = sites
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        services = try c.decodeIfPresent([String].self, forKey: .services) ?? []
        sites = try c.decodeIfPresent([String].self, forKey: .sites) ?? []
    }
}

/// An entry of GET /v1/logs/sites/{name}: one parsed Laravel log record.
public struct LogEntry: Decodable, Sendable, Equatable, Identifiable {
    public var time: Date
    public var env: String
    public var level: String
    public var message: String
    public var details: String

    /// Entries have no id on the wire; time + message is unique enough for list diffing.
    public var id: String { "\(time.timeIntervalSince1970)|\(level)|\(message)" }

    enum CodingKeys: String, CodingKey { case time, env, level, message, details }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        time = try c.decodeIfPresent(Date.self, forKey: .time) ?? .distantPast
        env = try c.decodeIfPresent(String.self, forKey: .env) ?? ""
        level = try c.decodeIfPresent(String.self, forKey: .level)?.lowercased() ?? "info"
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        details = try c.decodeIfPresent(String.self, forKey: .details) ?? ""
    }
}
