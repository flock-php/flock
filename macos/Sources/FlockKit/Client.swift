import Foundation

/// Where Flock keeps its files (FLOCK_HOME overrides it, like the CLI).
public enum FlockPaths {
    public static var root: URL {
        if let home = ProcessInfo.processInfo.environment["FLOCK_HOME"], !home.isEmpty {
            return URL(fileURLWithPath: home, isDirectory: true)
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Flock", isDirectory: true)
    }

    public static var apiJSON: URL { root.appendingPathComponent("api.json") }
    public static var flockBinary: URL { root.appendingPathComponent("bin/flock") }
}

/// Contents of api.json, written by the daemon.
public struct APIConfig: Decodable, Sendable, Equatable {
    public var url: URL
    public var token: String
}

public enum FlockError: LocalizedError, Equatable {
    case notRunning
    case server(status: Int, message: String)

    public var errorDescription: String? {
        switch self {
        case .notRunning: "Flock is not running"
        case let .server(status, message): message.isEmpty ? "Flock answered with HTTP \(status)" : message
        }
    }
}

/// Talks to the daemon's local HTTP API.
public actor FlockClient {
    private let configPath: URL
    private let session: URLSession
    private var config: APIConfig?

    public init(configPath: URL = FlockPaths.apiJSON) {
        self.configPath = configPath
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 5
        configuration.connectionProxyDictionary = [:] // never send localhost traffic through a proxy
        self.session = URLSession(configuration: configuration)
    }

    // MARK: Endpoints

    public func status() async throws -> Status {
        try await decode(Status.self, "GET", "/v1/status")
    }

    /// Changes only the given settings and returns the new status.
    public func updateSettings(xdebug: Bool? = nil, dumps: Bool? = nil) async throws -> Status {
        var body: [String: Bool] = [:]
        body["xdebug"] = xdebug
        body["dumps"] = dumps
        return try await decode(Status.self, "POST", "/v1/settings", body: try JSONEncoder().encode(body))
    }

    public func mail() async throws -> [MailSummary] {
        try await decode([MailSummary]?.self, "GET", "/v1/mail") ?? []
    }

    public func mail(id: String) async throws -> MailDetail {
        try await decode(MailDetail.self, "GET", "/v1/mail/\(escape(id))")
    }

    public func rawMail(id: String) async throws -> String {
        String(decoding: try await send("GET", "/v1/mail/\(escape(id))/raw"), as: UTF8.self)
    }

    public func attachment(mailID: String, index: Int) async throws -> Data {
        try await send("GET", "/v1/mail/\(escape(mailID))/attachments/\(index)")
    }

    public func clearMail(inbox: String? = nil) async throws {
        let query = inbox.map { [URLQueryItem(name: "inbox", value: $0)] } ?? []
        _ = try await send("DELETE", "/v1/mail", query: query)
    }

    public func dumps() async throws -> [Dump] {
        try await decode([Dump]?.self, "GET", "/v1/dumps") ?? []
    }

    public func clearDumps() async throws {
        _ = try await send("DELETE", "/v1/dumps")
    }

    public func logs() async throws -> LogIndex {
        try await decode(LogIndex.self, "GET", "/v1/logs")
    }

    public func serviceLog(_ name: String, lines: Int = 300) async throws -> String {
        let data = try await send("GET", "/v1/logs/services/\(escape(name))", query: [URLQueryItem(name: "lines", value: String(lines))])
        return String(decoding: data, as: UTF8.self)
    }

    public func siteLog(_ name: String, limit: Int = 200) async throws -> [LogEntry] {
        try await decode([LogEntry]?.self, "GET", "/v1/logs/sites/\(escape(name))", query: [URLQueryItem(name: "limit", value: String(limit))]) ?? []
    }

    public func clearSiteLog(_ name: String) async throws {
        _ = try await send("DELETE", "/v1/logs/sites/\(escape(name))")
    }

    // MARK: Transport

    private func decode<T: Decodable>(_ type: T.Type, _ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil) async throws -> T {
        let data = try await send(method, path, query: query, body: body)
        return try FlockJSON.decoder().decode(T.self, from: data)
    }

    /// Sends a request; on a connection failure it re-reads api.json once and retries,
    /// because a restarted daemon may listen on a new port with a new token.
    private func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil) async throws -> Data {
        do {
            return try await attempt(method, path, query: query, body: body, config: try loadConfig(force: false))
        } catch is URLError {
            config = nil
            do {
                return try await attempt(method, path, query: query, body: body, config: try loadConfig(force: true))
            } catch is URLError {
                throw FlockError.notRunning
            }
        } catch FlockError.server(401, _) {
            config = nil
            return try await attempt(method, path, query: query, body: body, config: try loadConfig(force: true))
        }
    }

    private func attempt(_ method: String, _ path: String, query: [URLQueryItem], body: Data?, config: APIConfig) async throws -> Data {
        var components = URLComponents(url: config.url.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query
        }

        var request = URLRequest(url: components.url!)
        request.httpMethod = method
        request.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let message = (try? JSONDecoder().decode([String: String].self, from: data))?["error"]
                ?? String(decoding: data.prefix(500), as: UTF8.self)
            throw FlockError.server(status: status, message: message)
        }
        return data
    }

    private func loadConfig(force: Bool) throws -> APIConfig {
        if let config, !force {
            return config
        }
        guard let data = try? Data(contentsOf: configPath),
              let loaded = try? JSONDecoder().decode(APIConfig.self, from: data) else {
            throw FlockError.notRunning
        }
        config = loaded
        return loaded
    }

    private func escape(_ segment: String) -> String {
        segment.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? segment
    }
}
