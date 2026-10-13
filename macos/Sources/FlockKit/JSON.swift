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
