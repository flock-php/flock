import Foundation

/// A link from the website or the docs that asks to install a service:
/// `flock://install/<service>` or `flock://install/<service>/<version line>`, e.g.
/// `flock://install/mysql/8.4` or `flock://install/postgres/18`.
///
/// Any web page can open such a link, so it never installs anything by itself: the app shows
/// what it would install and waits for the user to confirm (see `resolve`).
public struct InstallLink: Equatable, Sendable {
    public var service: String
    /// The version line ("8.4", a PostgreSQL major); nil for the newest.
    public var line: String?

    public init(service: String, line: String? = nil) {
        self.service = service
        self.line = line
    }

    public init?(url: URL) {
        guard url.scheme?.lowercased() == "flock", url.host?.lowercased() == "install" else { return nil }
        let parts = url.path.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        guard (1...2).contains(parts.count), Self.isSafe(parts[0], maxLength: 32) else { return nil }
        if parts.count == 2, !Self.isSafe(parts[1], maxLength: 16) {
            return nil
        }
        service = parts[0].lowercased()
        line = parts.count == 2 ? parts[1] : nil
    }

    /// Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.
    private static func isSafe(_ part: String, maxLength: Int) -> Bool {
        !part.isEmpty && part.count <= maxLength
            && part.unicodeScalars.allSatisfy { CharacterSet.alphanumerics.contains($0) && $0.isASCII || $0 == "." || $0 == "-" }
    }

    /// What the link asks for, measured against what this Flock offers and has installed.
    /// Nil while the services and versions are still loading.
    public func resolve(services: [Service], catalog: ServiceCatalog?, postgres: PostgresVersions?) -> Resolution? {
        guard !services.isEmpty else { return nil }
        guard let service = services.first(where: { $0.id == self.service }) else {
            return .unknown(name: self.service)
        }
        guard service.available else {
            return .unavailable(name: service.name, reason: service.unavailable)
        }

        if service.id == "postgres" {
            guard let postgres else { return nil }
            let wanted = line.flatMap { line in postgres.majors.first { $0.major == line } } ?? (line == nil ? postgres.majors.first : nil)
            guard let major = wanted else { return .unknownVersion(name: service.name, line: line ?? "") }
            if major.isInstalled {
                return .installed(name: "PostgreSQL \(major.major)")
            }
            return .install(Install(service: service.id, name: service.name, line: major.major, label: major.major, version: major.latest))
        }

        guard let catalog else { return nil }
        guard let entry = catalog[service.id] else { return .unknown(name: service.name) }
        let wanted = line.flatMap { line in entry.lines.first { $0.id == line } } ?? (line == nil ? entry.lines.first : nil)
        guard let target = wanted else { return .unknownVersion(name: entry.name, line: line ?? "") }
        if target.installed {
            return .installed(name: "\(entry.name) \(target.label)")
        }
        if entry.isInstalled {
            // Another line runs; switching replaces it, which the Services pane explains better.
            let current = entry.lines.first { $0.installed }?.label ?? entry.line
            return .otherInstalled(name: entry.name, installed: current, requested: target.label)
        }
        return .install(Install(service: entry.id, name: entry.name, line: target.id, label: target.label, version: target.latest))
    }

    public enum Resolution: Equatable, Sendable {
        /// Can be installed once the user confirms.
        case install(Install)
        /// Already installed: nothing to do.
        case installed(name: String)
        /// Another version line of the service is installed.
        case otherInstalled(name: String, installed: String, requested: String)
        /// The service doesn't run on this platform.
        case unavailable(name: String, reason: String)
        /// This Flock doesn't know the service.
        case unknown(name: String)
        /// This Flock doesn't offer that version line.
        case unknownVersion(name: String, line: String)
    }

    public struct Install: Equatable, Sendable {
        public var service: String
        public var name: String
        /// The line to pass to the daemon ("8.4", "18").
        public var line: String
        /// How the line reads ("8.4 LTS").
        public var label: String
        /// The release that gets installed; empty when not known yet.
        public var version: String
    }
}

extension InstallLink.Resolution {
    public var title: String {
        switch self {
        case .install(let install): "Install \(install.name) \(install.label)?"
        case .installed(let name): "\(name) is already installed"
        case .otherInstalled(let name, let installed, _): "\(name) \(installed) is installed"
        case .unavailable(let name, _): "\(name) isn't available on this computer"
        case .unknown(let name): "Flock doesn't offer \(name)"
        case .unknownVersion(let name, let line): "Flock doesn't offer \(name) \(line)"
        }
    }

    public var message: String {
        switch self {
        case .install(let install):
            let what = install.version.isEmpty ? "\(install.name) \(install.label)" : "\(install.name) \(install.version)"
            return "A link asked Flock to install \(what). It runs on 127.0.0.1 for your sites."
        case .installed:
            return "You'll find it in Settings → Services."
        case .otherInstalled(let name, _, let requested):
            return "To use \(name) \(requested) instead, switch versions in Settings → Services. Each version keeps its own data."
        case .unavailable(_, let reason):
            return reason.isEmpty ? "Flock can't run it on this platform." : reason
        case .unknown, .unknownVersion:
            return "The link may be meant for a newer Flock. Update Flock, then try again."
        }
    }

    /// The confirmation, when there is something to install.
    public var confirmation: InstallLink.Install? {
        if case .install(let install) = self { install } else { nil }
    }
}
