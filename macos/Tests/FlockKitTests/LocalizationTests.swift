import Foundation
import Testing

/// Resources/Localizable.xcstrings, which build-app.sh compiles into the app: every text is
/// translated into each language, with the same placeholders.
private let catalogURL = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    .appendingPathComponent("Resources/Localizable.xcstrings")

private let languages = ["cs", "es"]

/// The plural forms each language needs for whole numbers (CLDR); Czech "many" is for fractions.
private let pluralForms = ["en": ["one", "other"], "cs": ["one", "few", "many", "other"], "es": ["one", "other"]]

private struct Catalog: Decodable {
    var sourceLanguage: String
    var strings: [String: Entry]

    struct Entry: Decodable {
        var shouldTranslate: Bool?
        var localizations: [String: Localization]?
    }

    struct Localization: Decodable {
        var stringUnit: StringUnit?
        var variations: Variations?
    }

    struct Variations: Decodable {
        var plural: [String: Localization]?
    }

    struct StringUnit: Decodable {
        var state: String
        var value: String
    }
}

/// "%@", "%lld", ... in order of their argument, so "%2$@ %1$lld" reads as ["lld", "@"].
private func placeholders(_ text: String) -> [String] {
    let regex = /%(?:(\d+)\$)?(lld|ld|d|@)/
    var next = 0
    var found: [(Int, String)] = []
    for match in text.matches(of: regex) {
        next += 1
        found.append((match.1.flatMap { Int($0) } ?? next, String(match.2)))
    }
    return found.sorted { $0.0 < $1.0 }.map(\.1)
}

@Test func everyTextIsTranslated() throws {
    let catalog = try JSONDecoder().decode(Catalog.self, from: Data(contentsOf: catalogURL))
    #expect(catalog.sourceLanguage == "en")
    #expect(!catalog.strings.isEmpty)

    for (key, entry) in catalog.strings where entry.shouldTranslate != false {
        // English only has plural forms (and texts whose key isn't the English).
        for language in languages + (entry.localizations?["en"] == nil ? [] : ["en"]) {
            guard let localization = entry.localizations?[language] else {
                Issue.record("\(language) is missing for \(key.debugDescription)")
                continue
            }
            if let plural = localization.variations?.plural {
                #expect(Set(plural.keys).isSuperset(of: pluralForms[language] ?? []), "\(language) plural forms of \(key.debugDescription)")
                for (form, variant) in plural {
                    #expect(variant.stringUnit?.state == "translated", "\(language) \(form) of \(key.debugDescription)")
                    let found = placeholders(variant.stringUnit?.value ?? "")
                    var withoutCount = placeholders(key)
                    if let count = withoutCount.firstIndex(of: "lld") {
                        withoutCount.remove(at: count)
                    }
                    // "one" may leave the count out: "Show its worktree".
                    #expect(found == placeholders(key) || form == "one" && found == withoutCount,
                            "\(language) \(form) placeholders of \(key.debugDescription)")
                }
            } else {
                #expect(localization.stringUnit?.state == "translated", "\(language) of \(key.debugDescription)")
                if let value = localization.stringUnit?.value {
                    #expect(placeholders(value) == placeholders(key), "\(language) placeholders of \(key.debugDescription): \(value.debugDescription)")
                }
            }
        }
    }
}
