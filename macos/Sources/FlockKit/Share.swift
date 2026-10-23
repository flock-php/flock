import Foundation

/// A site's tunnel (`flock share`): a temporary public URL for showing work in progress.
public struct ShareStatus: Decodable, Sendable, Equatable {
    public var site: String
    public var state: State
    /// The public address, once running.
    public var url: String?
    /// Emails (or *@domain) allowed in; empty: anyone with the URL.
    public var allow: [String]
    /// Debug mode is on for visitors (stack traces), whatever .env says.
    public var debug: Bool
    public var startedAt: Date?
    /// When the share stops by itself, once running.
    public var expiresAt: Date?
    /// The assets were built for the share.
    public var built: Bool
    /// Flock builds the project's assets, so they can be rebuilt while sharing.
    public var canRebuild: Bool
    public var rebuilding: Bool
    public var rebuildError: String?
    public var error: String?
    /// What to know about how the share protects the app, e.g. that the cached config keeps debug mode on.
    public var warning: String?
    /// The name of its service log.
    public var log: String
    /// The URL as a QR code: a base64 PNG.
    public var qr: String?

    public enum State: String, Decodable, Sendable {
        /// Building the assets (npm run build).
        case building
        /// Downloading cloudflared or waiting for the tunnel's address.
        case starting
        case running
        /// See `error`.
        case failed
        case unknown

        public init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    /// The QR code's PNG.
    public var qrPNG: Data? { qr.flatMap { Data(base64Encoded: $0) } }

    enum CodingKeys: String, CodingKey {
        case site, state, url, allow, debug, startedAt, expiresAt, built, canRebuild, rebuilding, rebuildError, error, warning, log, qr
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        site = try c.decodeIfPresent(String.self, forKey: .site) ?? ""
        state = try c.decodeIfPresent(State.self, forKey: .state) ?? .unknown
        url = try c.decodeIfPresent(String.self, forKey: .url)
        allow = try c.decodeIfPresent([String].self, forKey: .allow) ?? []
        debug = try c.decodeIfPresent(Bool.self, forKey: .debug) ?? false
        startedAt = try c.decodeIfPresent(Date.self, forKey: .startedAt)
        expiresAt = try c.decodeIfPresent(Date.self, forKey: .expiresAt)
        built = try c.decodeIfPresent(Bool.self, forKey: .built) ?? false
        canRebuild = try c.decodeIfPresent(Bool.self, forKey: .canRebuild) ?? false
        rebuilding = try c.decodeIfPresent(Bool.self, forKey: .rebuilding) ?? false
        rebuildError = try c.decodeIfPresent(String.self, forKey: .rebuildError)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        warning = try c.decodeIfPresent(String.self, forKey: .warning)
        log = try c.decodeIfPresent(String.self, forKey: .log) ?? ""
        qr = try c.decodeIfPresent(String.self, forKey: .qr)
    }

    /// Splits what was typed into the allow list: addresses separated by commas or whitespace.
    public static func parseAllow(_ text: String) -> [String] {
        text.split(whereSeparator: { $0 == "," || $0 == ";" || $0.isWhitespace }).map(String.init)
    }
}
