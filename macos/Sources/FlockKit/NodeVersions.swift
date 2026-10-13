import Foundation

/// GET /v1/node/versions — every Node.js major line from nodejs.org worth offering (supported
/// lines, and end-of-life LTS lines back to 16), plus anything installed, with install state.
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
    /// LTS name such as "Jod"; empty for lines that aren't LTS.
    public var codename: String
    public var support: Support
    /// End-of-life date, e.g. "2027-04-30"; empty when unknown.
    public var endOfLife: String
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

    public enum Support: String, Decodable, Sendable {
        case current, lts, maintenance, eol, unknown

        public init(from decoder: Decoder) throws {
            self = Support(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    enum CodingKeys: String, CodingKey {
        case major, latest, codename, support, endOfLife, installed, update, npm, npmLatest
        case isDefault = "default"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        major = try c.decode(String.self, forKey: .major)
        latest = try c.decodeIfPresent(String.self, forKey: .latest) ?? ""
        codename = try c.decodeIfPresent(String.self, forKey: .codename) ?? ""
        support = try c.decodeIfPresent(Support.self, forKey: .support) ?? .unknown
        endOfLife = try c.decodeIfPresent(String.self, forKey: .endOfLife) ?? ""
        installed = try c.decodeIfPresent(String.self, forKey: .installed) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        update = try c.decodeIfPresent(Bool.self, forKey: .update) ?? false
        npm = try c.decodeIfPresent(String.self, forKey: .npm) ?? ""
        npmLatest = try c.decodeIfPresent(String.self, forKey: .npmLatest) ?? ""
    }
}
