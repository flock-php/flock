import Foundation

public enum FlockJSON {
    /// Decoder for the daemon's JSON: snake_case keys and Go's RFC 3339 times,
    /// which carry anywhere from zero to nine fractional digits.
    public static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let string = try container.decode(String.self)
            guard let date = parseDate(string) else {
                throw DecodingError.dataCorruptedError(in: container, debugDescription: "Invalid RFC 3339 date: \(string)")
            }
            return date
        }
        return decoder
    }

    /// Decoder for GET /v1/events: like `decoder()`, but keys stay as sent, so the keys of a log
    /// message's context (the app's own, e.g. `user_id`) aren't turned into camelCase.
    public static func eventDecoder() -> JSONDecoder {
        let decoder = decoder()
        decoder.keyDecodingStrategy = .useDefaultKeys
        return decoder
    }

    /// Parses RFC 3339 with or without fractional seconds of any precision.
    /// ISO8601DateFormatter only understands up to millisecond fractions reliably,
    /// so the fraction is split off and added back by hand.
    public static func parseDate(_ string: String) -> Date? {
        var base = string
        var fraction: Double = 0

        if let dot = string.firstIndex(of: "."),
           let end = string[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let digits = string[string.index(after: dot)..<end]
            fraction = Double("0." + digits) ?? 0
            base = String(string[..<dot] + string[end...])
        }

        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: base).map { $0.addingTimeInterval(fraction) }
    }
}

/// Any JSON value, for data the app only shows: a query's bindings, a log message's context.
public enum JSONValue: Decodable, Sendable, Hashable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    private struct Key: CodingKey {
        var stringValue: String
        var intValue: Int? { nil }
        init(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }

    public init(from decoder: Decoder) throws {
        if let c = try? decoder.container(keyedBy: Key.self) {
            var object: [String: JSONValue] = [:]
            for key in c.allKeys {
                object[key.stringValue] = try c.decode(JSONValue.self, forKey: key)
            }
            self = .object(object)
            return
        }
        if var c = try? decoder.unkeyedContainer() {
            var array: [JSONValue] = []
            while !c.isAtEnd {
                array.append(try c.decode(JSONValue.self))
            }
            self = .array(array)
            return
        }
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let bool = try? c.decode(Bool.self) {
            self = .bool(bool)
        } else if let number = try? c.decode(Double.self) {
            self = .number(number)
        } else {
            self = .string(try c.decode(String.self))
        }
    }

    /// JSON text, keys sorted; `indent` pretty-prints nested arrays and objects.
    public func json(indent: String? = nil) -> String {
        render(indent: indent, level: 0)
    }

    private func render(indent: String?, level: Int) -> String {
        let newline = indent.map { "\n" + String(repeating: $0, count: level + 1) } ?? ""
        let close = indent.map { "\n" + String(repeating: $0, count: level) } ?? ""
        let separator = ","
        switch self {
        case .null: return "null"
        case let .bool(b): return b ? "true" : "false"
        case let .number(n): return Self.format(n)
        case let .string(s): return Self.quote(s)
        case let .array(values):
            guard !values.isEmpty else { return "[]" }
            return "[" + values.map { newline + $0.render(indent: indent, level: level + 1) }.joined(separator: separator) + close + "]"
        case let .object(object):
            guard !object.isEmpty else { return "{}" }
            let colon = indent == nil ? ":" : ": "
            return "{" + object.keys.sorted().map { key in
                newline + Self.quote(key) + colon + object[key]!.render(indent: indent, level: level + 1)
            }.joined(separator: separator) + close + "}"
        }
    }

    /// Whole numbers without a fraction ("42", not "42.0").
    static func format(_ n: Double) -> String {
        n.rounded() == n && abs(n) < 1e15 ? String(Int64(n)) : String(n)
    }

    static func quote(_ s: String) -> String {
        var out = "\""
        for scalar in s.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            case _ where scalar.value < 0x20: out += String(format: "\\u%04x", scalar.value)
            default: out.unicodeScalars.append(scalar)
            }
        }
        return out + "\""
    }
}
