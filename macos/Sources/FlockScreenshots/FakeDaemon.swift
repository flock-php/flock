import Foundation
import Network

/// Answers the app's API calls from fixture files, on a loopback port it announces in
/// FLOCK_HOME/api.json, so FlockClient works unchanged. GET /v1/php/versions reads
/// php_versions.json (.json, .txt or .html). "{{now}}" and "{{now-3h}}" (s, m, h, d) in a fixture
/// become times relative to now, so "3 hours ago" stays true. Changes (POST, PATCH, DELETE) are
/// refused: a shot shows state.
///
/// The fixtures are shared with windows/FlockScreenshots and come from a Windows machine, so
/// `MacFixtures` turns them into what a Mac's daemon answers (paths, services) first.
final class FakeDaemon: @unchecked Sendable {
    private let fixtures: URL
    private let listener: NWListener
    private let queue = DispatchQueue(label: "FakeDaemon")

    init(fixtures: URL, home: URL) throws {
        self.fixtures = fixtures
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: parameters)
        listener.newConnectionHandler = { [weak self] connection in self?.serve(connection) }

        let ready = DispatchSemaphore(value: 0)
        listener.stateUpdateHandler = { state in
            if case .ready = state { ready.signal() }
            if case let .failed(error) = state { fatalError("fake daemon: \(error)") }
        }
        listener.start(queue: queue)
        ready.wait()

        let port = listener.port?.rawValue ?? 0
        try Data(#"{"url":"http://127.0.0.1:\#(port)","token":"screenshots"}"#.utf8)
            .write(to: home.appendingPathComponent("api.json"))
    }

    /// tools/screenshots/fixtures in this checkout.
    static let fixtures = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("tools/screenshots/fixtures", isDirectory: true)

    /// A new, empty folder for FLOCK_HOME.
    static func scratchHome() throws -> URL {
        let home = FileManager.default.temporaryDirectory.appendingPathComponent("flock-screenshots-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        return home
    }

    func stop() {
        listener.cancel()
    }

    private func serve(_ connection: NWConnection) {
        connection.start(queue: queue)
        receive(connection, buffer: Data())
    }

    /// Reads up to the end of the request's head; the app's GETs have no body worth reading.
    private func receive(_ connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, done, error in
            guard let self else { return }
            var buffer = buffer
            if let data {
                buffer.append(data)
            }
            if let end = buffer.range(of: Data("\r\n\r\n".utf8)) {
                answer(connection, head: String(decoding: buffer[..<end.lowerBound], as: UTF8.self))
            } else if done || error != nil {
                connection.cancel()
            } else {
                receive(connection, buffer: buffer)
            }
        }
    }

    private func answer(_ connection: NWConnection, head: String) {
        let parts = head.split(separator: "\r\n").first?.split(separator: " ") ?? []
        let method = parts.count > 0 ? String(parts[0]) : ""
        let path = parts.count > 1 ? String(parts[1].split(separator: "?").first ?? "") : ""

        let (status, type, body): (Int, String, String)
        if method != "GET" {
            (status, type, body) = (501, "application/json", #"{"error":"screenshots don't change anything (\#(method) \#(path))"}"#)
        } else if let fixture = fixture(path) {
            (status, type, body) = (200, fixture.type, fixture.body)
        } else {
            (status, type, body) = (404, "application/json", #"{"error":"no fixture for \#(path)"}"#)
        }
        if status != 200 {
            FileHandle.standardError.write(Data("  fake daemon: \(status) \(method) \(path)\n".utf8))
        }

        let data = Data(body.utf8)
        let response = "HTTP/1.1 \(status) \(status == 200 ? "OK" : "Error")\r\nContent-Type: \(type)\r\nContent-Length: \(data.count)\r\nConnection: close\r\n\r\n"
        connection.send(content: Data(response.utf8) + data, completion: .contentProcessed { _ in connection.cancel() })
    }

    private func fixture(_ path: String) -> (type: String, body: String)? {
        let name = (path.removingPercentEncoding ?? path)
            .trimmingPrefix("/")
            .replacingOccurrences(of: "v1/", with: "")
            .replacingOccurrences(of: "/", with: "_")
        for (ext, type) in [("json", "application/json"), ("txt", "text/plain"), ("html", "text/html")] {
            let file = fixtures.appendingPathComponent("\(name).\(ext)")
            guard let text = try? String(contentsOf: file, encoding: .utf8) else { continue }
            let timed = Self.times(text)
            return (type, ext == "json" ? MacFixtures.adapt(json: timed, name: name) : MacFixtures.paths(timed))
        }
        return nil
    }

    /// "{{now}}", "{{now-40s}}", "{{now-3h}}" → ISO 8601 times relative to now.
    static func times(_ text: String) -> String {
        let pattern = #/\{\{now(?:-(?<amount>\d+)(?<unit>[smhd]))?\}\}/#
        return text.replacing(pattern) { match in
            let amount = Double(match.output.amount.map(String.init) ?? "0") ?? 0
            let unit: Double = switch match.output.unit {
            case "s": 1
            case "m": 60
            case "h": 3600
            case "d": 86400
            default: 0
            }
            return Date().addingTimeInterval(-amount * unit).formatted(.iso8601)
        }
    }
}

/// What tells the fixtures (written on Windows) from a Mac's daemon: paths, PHP-FPM pools, the
/// DNS server's port, no hosts file, Redis and Typesense, PHP from 8.1 on.
enum MacFixtures {
    /// The Windows-only parts of the answer for a fixture, swapped for what macOS has.
    static func adapt(json text: String, name: String) -> String {
        guard var value = try? JSONSerialization.jsonObject(with: Data(text.utf8)) else { return paths(text) }
        value = strings(in: value) { names(paths($0)) }
        switch name {
        case "status":
            // The DNS server listens on 53535 (config.json's port) for macOS's resolver, not on 53.
            if var status = value as? [String: Any], var dns = status["dns"] as? [String: Any] {
                dns["addr"] = "127.0.0.1:53535"
                status["dns"] = dns
                value = status
            }
        case "services":
            // Windows resolves *.test through the hosts file; macOS has a resolver for the TLD.
            value = (value as? [[String: Any]])?.filter { $0["id"] as? String != "hosts" }.map(offered).map(dnsPort) ?? value
        case "php_versions":
            // PHP 7.4 and 8.0 are Windows-only.
            if var versions = value as? [String: Any], let list = versions["versions"] as? [[String: Any]] {
                versions["versions"] = list.filter { !["7.4", "8.0"].contains($0["minor"] as? String ?? "") }
                value = versions
            }
        case "catalog":
            if var catalog = value as? [String: Any] {
                catalog["services"] = (catalog["services"] as? [[String: Any]])?.map(offered)
                value = catalog
            }
        default:
            break
        }
        guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.withoutEscapingSlashes]) else { return text }
        return String(decoding: data, as: UTF8.self)
    }

    private static func dnsPort(_ service: [String: Any]) -> [String: Any] {
        guard service["id"] as? String == "dns" else { return service }
        var service = service
        service["port"] = 53535
        return service
    }

    /// Redis and Typesense have no Windows build; on a Mac they are offered like the others.
    private static func offered(_ service: [String: Any]) -> [String: Any] {
        guard let reason = service["unavailable"] as? String, reason.contains("Windows") else { return service }
        var service = service
        service["available"] = true
        service["unavailable"] = ""
        return service
    }

    /// C:\Users\demo\Code\acme → /Users/demo/Code/acme, and the data folder in Application
    /// Support. Also inside escaped JSON (a log entry's details), where each \ is doubled.
    static func paths(_ text: String) -> String {
        let pattern = #/C:(?<slash>\\+)Users\\+demo(?<rest>(?:\\+[^\\\s"'(),:;<>]+)*)/#
        return text.replacing(pattern) { match in
            let slash = String(match.output.slash)
            let path = "/Users/demo" + match.output.rest.replacingOccurrences(of: slash, with: "/")
            return path.replacingOccurrences(of: "/Users/demo/AppData/Roaming/Flock", with: "/Users/demo/Library/Application Support/Flock")
        }
    }

    /// php-cgi workers (php8.5-cgi-1) are PHP-FPM pools on macOS (php8.5-fpm).
    static func names(_ text: String) -> String {
        text.replacing(#/php(?<minor>\d+\.\d+)-cgi(?:-\d+)?/#) { "php\($0.output.minor)-fpm" }
    }

    private static func strings(in value: Any, _ transform: (String) -> String) -> Any {
        switch value {
        case let string as String: transform(string)
        case let array as [Any]: array.map { strings(in: $0, transform) }
        case let object as [String: Any]: object.mapValues { strings(in: $0, transform) }
        default: value
        }
    }
}
