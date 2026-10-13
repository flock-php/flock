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
    public var redis: String?
    public var xdebug: Bool
    public var dumps: Bool
    public var mailCount: Int
    public var dumpCount: Int
    public var sites: [Site]
    public var processes: [Process]
    public var warnings: [String]

    public struct DNSStatus: Decodable, Sendable, Equatable {
        public var tld: String
        public var addr: String
    }

    public struct Site: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var domain: String
        public var path: String
        public var php: String
        public var secured: Bool
        public var parked: Bool
        public var url: String?
        public var problem: String?

        public var id: String { name }
    }

    public struct Process: Decodable, Sendable, Equatable, Identifiable {
        public var name: String
        public var up: Bool
        public var restarts: Int
        public var lastError: String?

        public var id: String { name }
    }

    /// Something needs attention: a warning, or a supervised process that is down.
    public var isDegraded: Bool {
        !warnings.isEmpty || processes.contains { !$0.up }
    }

    enum CodingKeys: String, CodingKey {
        case version, pid, startedAt, dns, httpPort, httpsPort, mail, postgres, redis
        case xdebug, dumps, mailCount, dumpCount, sites, processes, warnings
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
        redis = try c.decodeIfPresent(String.self, forKey: .redis)
        xdebug = try c.decodeIfPresent(Bool.self, forKey: .xdebug) ?? false
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
