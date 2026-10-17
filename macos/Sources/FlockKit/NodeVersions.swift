import Foundation

/// GET /v1/node/versions — every Node.js major the package manifest offers (16 and newer, one
/// release each), plus anything installed, with install state.
public struct NodeCatalog: Decodable, Sendable, Equatable {
    public var checkedAt: Date?
    public var checkError: String
    public var versions: [NodeRelease]

    enum CodingKeys: String, CodingKey { case checkedAt, checkError, versions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        checkError = try c.decodeIfPresent(String.self, forKey: .checkError) ?? ""
        versions = try c.decodeIfPresent([NodeRelease].self, forKey: .versions) ?? []
    }
}

/// One Node.js major line (e.g. "22") and what is installed of it.
public struct NodeRelease: Decodable, Sendable, Equatable, Identifiable {
    public var major: String
    /// The newest published release of the line, e.g. "22.21.0".
    public var latest: String
    /// The newest installed release of the line; empty when none is.
    public var installed: String
    public var isDefault: Bool
    /// A newer release than the installed one exists.
    public var update: Bool
    /// The npm the installed version uses (bundled, or updated); empty when none is installed.
    public var npm: String
    /// The newest npm that supports the installed version, when it is newer than `npm`.
    public var npmLatest: String

    public var id: String { major }
    public var isInstalled: Bool { !installed.isEmpty }

    enum CodingKeys: String, CodingKey {
        case major, latest, installed, update, npm, npmLatest
        case isDefault = "default"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        major = try c.decode(String.self, forKey: .major)
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        npm = try c.decodeIfPresent(String.self, forKey: .npm) ?? ""
        npmLatest = try c.decodeIfPresent(String.self, forKey: .npmLatest) ?? ""
    }
}
