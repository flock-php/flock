import Foundation

/// GET /v1/sites/{name}/extensions — the PHP extensions a site requires (`ext-*` in composer.json
/// and its locked packages), the ones its Laravel configuration shows it uses without requiring
/// them, and whether Flock's PHP has each.
public struct ExtensionReport: Decodable, Sendable, Equatable {
    public var php: String
    /// Hints are looked for (Laravel apps only).
    public var laravel: Bool
    public var extensions: [PHPExtension]

    enum CodingKeys: String, CodingKey { case php, laravel, extensions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        php = try c.decodeIfPresent(String.self, forKey: .php) ?? ""
        laravel = try c.decodeIfPresent(Bool.self, forKey: .laravel) ?? false
        extensions = try c.decodeIfPresent([PHPExtension]?.self, forKey: .extensions).flatMap { $0 } ?? []
    }
}

/// One extension a project needs.
public struct PHPExtension: Decodable, Sendable, Equatable, Identifiable {
    /// Composer's name without "ext-", e.g. "intl".
    public var name: String
    /// "composer.json" and/or the packages that require it.
    public var requiredBy: [String]
    /// Why the project seems to use it although nothing requires it; empty when something does.
    public var hint: String
    /// Flock's PHP has it.
    public var available: Bool

    public var id: String { name }
    public var declared: Bool { !requiredBy.isEmpty }

    enum CodingKeys: String, CodingKey { case name, requiredBy, hint, available }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        requiredBy = try c.decodeIfPresent([String]?.self, forKey: .requiredBy).flatMap { $0 } ?? []
        hint = try c.decodeIfPresent(String.self, forKey: .hint) ?? ""
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? false
    }
}
