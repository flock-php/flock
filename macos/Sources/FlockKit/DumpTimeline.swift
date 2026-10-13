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
            <header class="origin">\(originHTML(first))\
            \(first.site.isEmpty ? "" : "<span class=\"site\">\(escape(first.site))</span>")\
            <time>\(time(first.time))</time></header>
            """
            for dump in chronological {
                body += """
                <article class="dump">
                <div class="meta">\(source(dump, editor: editor))\
                <time>\(time(dump.time))</time></div>
                \(cleanedHTML(of: dump))
                </article>
                """
            }
            body += "</section>\n"
        }

        return """
        <!doctype html><html><head><meta charset="utf-8">
        \(header)
        <style>\(style)</style>
        </head><body>
        \(body)
        </body></html>
        """
    }

    /// Native-looking theme: system fonts and colors, light and dark mode, cards on the window
    /// background, Xcode-like syntax colors for Symfony's dump markup (its JS keeps working).
    static let style = """
    :root {
      color-scheme: light dark;
      --text: #1d1d1f; --secondary: #6e6e73; --tertiary: #a1a1a6;
      --card: #ffffff; --card-border: rgba(0,0,0,.08); --shadow: 0 1px 2px rgba(0,0,0,.06);
      --header: rgba(246,246,248,.85); --separator: rgba(0,0,0,.08); --hover: rgba(0,0,0,.05);
      --accent: #0a64d8; --string: #c41a16; --number: #1c00cf; --key: #326d74; --type: #6c36a9;
      --keyword: #9b2393; --meta: #8a8a8e; --get: #1f9d55; --post: #c87400; --cli: #6e6e73;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --text: #f5f5f7; --secondary: #98989d; --tertiary: #6e6e73;
        --card: #2a2a2d; --card-border: rgba(255,255,255,.07); --shadow: 0 1px 2px rgba(0,0,0,.35);
        --header: rgba(30,30,32,.85); --separator: rgba(255,255,255,.08); --hover: rgba(255,255,255,.06);
        --accent: #4aa3ff; --string: #fc6a5d; --number: #d0bf69; --key: #67b7a4; --type: #a167e6;
        --keyword: #fc5fa3; --meta: #8e8e93; --get: #4cd07d; --post: #ffb340; --cli: #98989d;
      }
    }
    html, body { margin: 0; background: transparent; color: var(--text);
                 font: 13px -apple-system, BlinkMacSystemFont, system-ui, sans-serif; -webkit-font-smoothing: antialiased; }
    body { padding: 0 0 16px; }

    .group { padding: 0 14px; }
    .origin { position: sticky; top: 0; z-index: 10; display: flex; gap: 8px; align-items: center;
              margin: 0 -14px; padding: 9px 16px 7px; background: var(--header);
              -webkit-backdrop-filter: saturate(180%) blur(20px); backdrop-filter: saturate(180%) blur(20px);
              border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
    .origin .verb { font: 600 10px ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: .04em;
                    padding: 2px 6px; border-radius: 5px; color: #fff; background: var(--get); }
    .origin .verb.post, .origin .verb.put, .origin .verb.patch, .origin .verb.delete { background: var(--post); }
    .origin .verb.cli { background: var(--cli); }
    .origin .what { color: var(--text); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
    .origin .site { padding: 1px 7px; border-radius: 10px; background: var(--hover); font-size: 11px; white-space: nowrap; }

    .dump { margin: 10px 0 0; background: var(--card); border: 1px solid var(--card-border);
            border-radius: 10px; box-shadow: var(--shadow); overflow: hidden; }
    .meta { display: flex; gap: 8px; align-items: center; padding: 7px 12px;
            border-bottom: 1px solid var(--separator); font-size: 11.5px; color: var(--secondary); }
    .meta a, .meta span.src { font: 11.5px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--accent); text-decoration: none; }
    .meta span.src { color: var(--secondary); }
    .meta a:hover { text-decoration: underline; }
    time { margin-left: auto; color: var(--tertiary); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }

    pre.sf-dump, pre.sf-dump .sf-dump-default {
      background: transparent !important; color: var(--text) !important; z-index: auto !important;
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace !important; }
    pre.sf-dump { margin: 0 !important; padding: 10px 12px !important; }
    pre.sf-dump .sf-dump-str { color: var(--string) !important; font-weight: normal !important; }
    pre.sf-dump .sf-dump-num { color: var(--number) !important; font-weight: normal !important; }
    pre.sf-dump .sf-dump-const { color: var(--keyword) !important; font-weight: 600 !important; }
    pre.sf-dump .sf-dump-key, pre.sf-dump .sf-dump-index { color: var(--key) !important; }
    pre.sf-dump .sf-dump-note, pre.sf-dump .sf-dump-ellipsis-note { color: var(--type) !important; }
    pre.sf-dump .sf-dump-public, pre.sf-dump .sf-dump-protected, pre.sf-dump .sf-dump-private { color: var(--text) !important; }
    pre.sf-dump .sf-dump-meta, pre.sf-dump .sf-dump-ref, pre.sf-dump .sf-dump-ellipsis { color: var(--meta) !important; }
    pre.sf-dump .sf-dump-toggle span { color: var(--tertiary); }
    pre.sf-dump a.sf-dump-toggle { border-radius: 3px; }
    pre.sf-dump a.sf-dump-toggle:hover { background: var(--hover); }
    .sf-dump-hover:hover { background-color: var(--hover) !important; color: inherit !important; }
    """

    /// Dumps captured by older Flock versions embed Symfony's whole header; its stylesheet would
    /// override the theme for every dump on the page, so drop inline styles from bodies.
    static func cleanedHTML(of dump: Dump) -> String {
        dump.html.replacingOccurrences(of: #"<style[^>]*>[\s\S]*?</style>"#, with: "", options: .regularExpression)
    }

    /// "GET" pill + URL for requests, "CLI" pill + command for artisan and scripts.
    private static func originHTML(_ dump: Dump) -> String {
        if let request = dump.request, !request.uri.isEmpty {
            let verb = request.method.uppercased()
            return "<span class=\"verb \(escape(verb.lowercased()))\">\(escape(verb))</span><span class=\"what\">\(escape(request.uri))</span>"
        }
        if let command = dump.commandLine, !command.isEmpty {
            return "<span class=\"verb cli\">CLI</span><span class=\"what\">\(escape(command))</span>"
        }
        return "<span class=\"what\">dump</span>"
    }

    private static func source(_ dump: Dump, editor: Editor) -> String {
        guard let source = dump.source, !source.file.isEmpty else { return "<span>dump</span>" }
        let text = escape(shortPath(source.file) + (source.line > 0 ? ":\(source.line)" : ""))
        guard let url = editor.url(file: source.file, line: source.line) else {
            return "<span class=\"src\" title=\"\(escape(source.file))\">\(text)</span>"
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
