import Foundation

/// Editors that dump sources can be opened in.
public enum Editor: String, CaseIterable, Sendable, Identifiable {
    case phpstorm, vscode, cursor, zed, none

    public var id: String { rawValue }

    public var name: String {
        switch self {
        case .phpstorm: "PhpStorm"
        case .vscode: "VS Code"
        case .cursor: "Cursor"
        case .zed: "Zed"
        case .none: "Don't link"
        }
    }

    /// A URL that opens file (at line, when known) in the editor.
    public func url(file: String, line: Int) -> URL? {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "?#")
        guard let path = file.addingPercentEncoding(withAllowedCharacters: allowed) else { return nil }
        let at = line > 0 ? ":\(line)" : ""

        return switch self {
        case .phpstorm: URL(string: "phpstorm://open?file=\(path)\(line > 0 ? "&line=\(line)" : "")")
        case .vscode: URL(string: "vscode://file\(path)\(at)")
        case .cursor: URL(string: "cursor://file\(path)\(at)")
        case .zed: URL(string: "zed://file\(path)\(at)")
        case .none: nil
        }
    }
}

/// Renders dumps as one terminal-like page: requests newest first, each request's dumps
/// in the order they happened, every dump with its source, origin and time.
public enum DumpTimeline {
    /// Dumps belonging to the most recent request / CLI run.
    public static func latestGroup(of dumps: [Dump]) -> [Dump] {
        guard let group = dumps.first?.group else { return [] }
        return dumps.filter { $0.group == group }
    }

    /// Case-insensitive match on the dumped value, source file and origin.
    public static func filter(_ dumps: [Dump], query: String) -> [Dump] {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return dumps }
        return dumps.filter { dump in
            [dump.text, dump.source?.file ?? "", dump.origin, dump.label ?? ""]
                .contains { $0.localizedCaseInsensitiveContains(q) }
        }
    }

    /// `dumps` must be newest first (as the API returns them).
    public static func html(dumps: [Dump], header: String, editor: Editor) -> String {
        var groups: [(key: String, dumps: [Dump])] = []
        for dump in dumps {
            if let last = groups.last, last.key == dump.group, !dump.group.isEmpty {
                groups[groups.count - 1].dumps.append(dump)
            } else {
                groups.append((dump.group, [dump]))
            }
        }

        var body = ""
        for group in groups {
            let chronological = group.dumps.reversed()
            guard let first = chronological.first else { continue }
            body += """
            <section class="group">
            <header class="origin"><span class="what">\(escape(first.origin.isEmpty ? "dump" : first.origin))</span>\
            \(first.site.isEmpty ? "" : "<span class=\"site\">\(escape(first.site))</span>")\
            <time>\(time(first.time))</time></header>
            """
            for dump in chronological {
                body += """
                <article class="dump">
                <div class="meta">\(source(dump, editor: editor))\
                <time>\(time(dump.time))</time></div>
                \(dump.html)
                </article>
                """
            }
            body += "</section>\n"
        }

        return """
        <!doctype html><html><head><meta charset="utf-8">
        <style>
        :root { color-scheme: dark; }
        html, body { margin: 0; background: #18171B; color: #d4d4d8; font: 12px -apple-system, system-ui, sans-serif; }
        .group { border-bottom: 1px solid #2c2b31; }
        .origin { position: sticky; top: 0; z-index: 5; display: flex; gap: 10px; align-items: baseline;
                  padding: 7px 14px; background: #222127; border-bottom: 1px solid #2c2b31;
                  font: 11.5px ui-monospace, Menlo, monospace; color: #e4e4e7; }
        .origin .site { color: #a78bfa; }
        .dump + .dump { border-top: 1px dashed #2c2b31; }
        .meta { display: flex; gap: 10px; align-items: baseline; padding: 8px 14px 0;
                font: 11px ui-monospace, Menlo, monospace; color: #8b8b94; }
        .meta a { color: #7dd3fc; text-decoration: none; }
        .meta a:hover { text-decoration: underline; }
        time { margin-left: auto; color: #71717a; font: 11px ui-monospace, Menlo, monospace; }
        pre.sf-dump { margin: 0 !important; padding: 6px 14px 12px !important; background: transparent !important;
                      font-size: 12px !important; }
        </style>
        \(header)
        </head><body>
        \(body)
        </body></html>
        """
    }

    private static func source(_ dump: Dump, editor: Editor) -> String {
        guard let source = dump.source, !source.file.isEmpty else { return "<span>dump</span>" }
        let text = escape(shortPath(source.file) + (source.line > 0 ? ":\(source.line)" : ""))
        guard let url = editor.url(file: source.file, line: source.line) else {
            return "<span title=\"\(escape(source.file))\">\(text)</span>"
        }
        return "<a href=\"\(escape(url.absoluteString))\" title=\"\(escape(source.file))\">\(text)</a>"
    }

    /// The path relative to the project (everything after app/, resources/, routes/, ... when present).
    static func shortPath(_ file: String) -> String {
        for marker in ["/app/", "/resources/", "/routes/", "/database/", "/config/", "/tests/", "/vendor/", "/bootstrap/"] {
            if let range = file.range(of: marker, options: .backwards) {
                return String(file[file.index(after: range.lowerBound)...])
            }
        }
        return file.split(separator: "/").suffix(3).joined(separator: "/")
    }

    private static func time(_ date: Date) -> String {
        date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits))
    }

    static func escape(_ s: String) -> String {
        s.replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
    }
}

extension Dump {
    /// "GET https://app.test/x" for requests, the command line for CLI runs.
    public var origin: String {
        if let request, !request.uri.isEmpty {
            return "\(request.method) \(request.uri)"
        }
        if let commandLine, !commandLine.isEmpty {
            return commandLine
        }
        return ""
    }
}
