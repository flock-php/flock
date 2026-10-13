using System.Globalization;
using System.Text;
using System.Text.RegularExpressions;

namespace Flock.Kit;

/// <summary>Editors that dump sources can be opened in.</summary>
public enum Editor { PhpStorm, VsCode, Cursor, Zed, None }

public static class EditorInfo
{
    public static IReadOnlyList<Editor> All { get; } = Enum.GetValues<Editor>();

    public static string Name(this Editor editor) => editor switch
    {
        Editor.PhpStorm => "PhpStorm",
        Editor.VsCode => "VS Code",
        Editor.Cursor => "Cursor",
        Editor.Zed => "Zed",
        _ => "Don't link",
    };

    /// <summary>
    /// A URL that opens file (at line, when known) in the editor. Windows paths are written with
    /// forward slashes ("C:\code\x.php" → vscode://file/C:/code/x.php:3).
    /// </summary>
    public static string? Url(this Editor editor, string file, int line)
    {
        var path = EscapePath(file.Replace('\\', '/'));
        var rooted = path.StartsWith('/') ? path : "/" + path;
        var at = line > 0 ? $":{line}" : "";

        return editor switch
        {
            Editor.PhpStorm => $"phpstorm://open?file={path}" + (line > 0 ? $"&line={line}" : ""),
            Editor.VsCode => $"vscode://file{rooted}{at}",
            Editor.Cursor => $"cursor://file{rooted}{at}",
            Editor.Zed => $"zed://file{rooted}{at}",
            _ => null,
        };
    }

    /// <summary>Percent-encodes everything outside RFC 3986's path characters (like Foundation's urlPathAllowed, minus ? and #).</summary>
    private static string EscapePath(string path)
    {
        var b = new StringBuilder();
        foreach (var by in Encoding.UTF8.GetBytes(path))
        {
            var c = (char)by;
            if (by < 0x80 && (char.IsAsciiLetterOrDigit(c) || "-._~/:@!$&'()*+,;=".Contains(c)))
            {
                b.Append(c);
            }
            else
            {
                b.Append('%').Append(by.ToString("X2", CultureInfo.InvariantCulture));
            }
        }
        return b.ToString();
    }

    public static Editor Parse(string? value) =>
        Enum.TryParse<Editor>(value, ignoreCase: true, out var editor) ? editor : Editor.PhpStorm;
}

/// <summary>
/// Renders dumps as one terminal-like page: requests newest first, each request's dumps in the
/// order they happened, every dump with its source, origin and time.
/// </summary>
public static partial class DumpTimeline
{
    /// <summary>Dumps belonging to the most recent request / CLI run.</summary>
    public static List<Dump> LatestGroup(IReadOnlyList<Dump> dumps)
    {
        if (dumps.Count == 0)
        {
            return [];
        }
        var group = dumps[0].Group;
        return dumps.Where(d => d.Group == group).ToList();
    }

    /// <summary>Case-insensitive match on the dumped value, source file and origin.</summary>
    public static List<Dump> Filter(IReadOnlyList<Dump> dumps, string query)
    {
        var q = query.Trim();
        if (q.Length == 0)
        {
            return dumps.ToList();
        }
        return dumps.Where(d => new[] { d.Text, d.Source?.File ?? "", d.Origin, d.Label ?? "" }
            .Any(s => s.Contains(q, StringComparison.CurrentCultureIgnoreCase))).ToList();
    }

    /// <summary>dumps must be newest first (as the API returns them).</summary>
    public static string Html(IReadOnlyList<Dump> dumps, string header, Editor editor)
    {
        var groups = new List<(string Key, List<Dump> Dumps)>();
        foreach (var dump in dumps)
        {
            if (groups.Count > 0 && groups[^1].Key == dump.Group && dump.Group.Length > 0)
            {
                groups[^1].Dumps.Add(dump);
            }
            else
            {
                groups.Add((dump.Group, [dump]));
            }
        }

        var body = new StringBuilder();
        foreach (var (_, members) in groups)
        {
            var chronological = Enumerable.Reverse(members).ToList();
            var first = chronological[0];
            body.Append("<section class=\"group\">\n<header class=\"origin\">").Append(OriginHtml(first))
                .Append(first.Site.Length == 0 ? "" : $"<span class=\"site\">{Escape(first.Site)}</span>")
                .Append($"<time>{Time(first.Time)}</time></header>\n");
            foreach (var dump in chronological)
            {
                body.Append("<article class=\"dump\">\n<div class=\"meta\">").Append(Source(dump, editor))
                    .Append($"<time>{Time(dump.Time)}</time></div>\n")
                    .Append(CleanedHtml(dump)).Append("\n</article>\n");
            }
            body.Append("</section>\n");
        }

        return $"""
            <!doctype html><html><head><meta charset="utf-8">
            {header}
            <style>{Style}</style>
            </head><body>
            {body}
            </body></html>
            """;
    }

    /// <summary>
    /// Native-looking theme: Windows fonts and colors, light and dark mode, cards on the window
    /// background, editor-like syntax colors for Symfony's dump markup (its JS keeps working).
    /// </summary>
    public const string Style = """
        :root {
          color-scheme: light dark;
          --text: #1b1b1b; --secondary: #5d5d5d; --tertiary: #8a8a8a;
          --card: #ffffff; --card-border: rgba(0,0,0,.0578); --shadow: 0 1px 2px rgba(0,0,0,.04);
          --header: rgba(243,243,243,.92); --separator: rgba(0,0,0,.08); --hover: rgba(0,0,0,.04);
          --accent: #005fb8; --string: #a31515; --number: #098658; --key: #0451a5; --type: #6f42c1;
          --keyword: #af00db; --meta: #767676; --get: #0f7b0f; --post: #9d5d00; --cli: #5d5d5d;
        }
        @media (prefers-color-scheme: dark) {
          :root {
            --text: #ffffff; --secondary: #c5c5c5; --tertiary: #9a9a9a;
            --card: #2b2b2b; --card-border: rgba(255,255,255,.07); --shadow: 0 1px 2px rgba(0,0,0,.3);
            --header: rgba(32,32,32,.92); --separator: rgba(255,255,255,.08); --hover: rgba(255,255,255,.06);
            --accent: #60cdff; --string: #ce9178; --number: #b5cea8; --key: #9cdcfe; --type: #c586c0;
            --keyword: #569cd6; --meta: #9d9d9d; --get: #6ccb5f; --post: #fce100; --cli: #9d9d9d;
          }
        }
        html, body { margin: 0; background: transparent; color: var(--text);
                     font: 14px "Segoe UI Variable Text", "Segoe UI", system-ui, sans-serif; }
        body { padding: 0 0 16px; }

        .group { padding: 0 14px; }
        .origin { position: sticky; top: 0; z-index: 10; display: flex; gap: 8px; align-items: center;
                  margin: 0 -14px; padding: 9px 16px 7px; background: var(--header);
                  backdrop-filter: saturate(180%) blur(20px);
                  border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
        .origin .verb { font: 600 10px "Cascadia Mono", Consolas, monospace; letter-spacing: .04em;
                        padding: 2px 6px; border-radius: 4px; color: #fff; background: var(--get); }
        .origin .verb.post, .origin .verb.put, .origin .verb.patch, .origin .verb.delete { background: var(--post); color: #000; }
        .origin .verb.cli { background: var(--cli); }
        .origin .what { color: var(--text); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
        .origin .site { padding: 1px 7px; border-radius: 10px; background: var(--hover); font-size: 11px; white-space: nowrap; }

        .dump { margin: 10px 0 0; background: var(--card); border: 1px solid var(--card-border);
                border-radius: 8px; box-shadow: var(--shadow); overflow: hidden; }
        .meta { display: flex; gap: 8px; align-items: center; padding: 7px 12px;
                border-bottom: 1px solid var(--separator); font-size: 12px; color: var(--secondary); }
        .meta a, .meta span.src { font: 12px "Cascadia Mono", Consolas, monospace; color: var(--accent); text-decoration: none; }
        .meta span.src { color: var(--secondary); }
        .meta a:hover { text-decoration: underline; }
        time { margin-left: auto; color: var(--tertiary); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }

        pre.sf-dump, pre.sf-dump .sf-dump-default {
          background: transparent !important; color: var(--text) !important; z-index: auto !important;
          font: 12.5px/1.5 "Cascadia Mono", Consolas, monospace !important; }
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
        """;

    /// <summary>
    /// Dumps captured by older Flock versions embed Symfony's whole header; its stylesheet would
    /// override the theme for every dump on the page, so drop inline styles from bodies.
    /// </summary>
    public static string CleanedHtml(Dump dump) => InlineStyle().Replace(dump.Html, "");

    [GeneratedRegex(@"<style[^>]*>[\s\S]*?</style>")]
    private static partial Regex InlineStyle();

    /// <summary>"GET" pill + URL for requests, "CLI" pill + command for artisan and scripts.</summary>
    private static string OriginHtml(Dump dump)
    {
        if (dump.Request is { Uri.Length: > 0 } request)
        {
            var verb = request.Method.ToUpperInvariant();
            return $"<span class=\"verb {Escape(verb.ToLowerInvariant())}\">{Escape(verb)}</span><span class=\"what\">{Escape(request.Uri)}</span>";
        }
        if (dump.CommandLine is { Length: > 0 } command)
        {
            return $"<span class=\"verb cli\">CLI</span><span class=\"what\">{Escape(command)}</span>";
        }
        return "<span class=\"what\">dump</span>";
    }

    private static string Source(Dump dump, Editor editor)
    {
        if (dump.Source is not { File.Length: > 0 } source)
        {
            return "<span>dump</span>";
        }
        var text = Escape(ShortPath(source.File) + (source.Line > 0 ? $":{source.Line}" : ""));
        var url = editor.Url(source.File, source.Line);
        if (url is null)
        {
            return $"<span class=\"src\" title=\"{Escape(source.File)}\">{text}</span>";
        }
        return $"<a href=\"{Escape(url)}\" title=\"{Escape(source.File)}\">{text}</a>";
    }

    /// <summary>The path relative to the project (everything after app/, resources/, routes/, ... when present).</summary>
    public static string ShortPath(string file)
    {
        var normalized = file.Replace('\\', '/');
        foreach (var marker in new[] { "/app/", "/resources/", "/routes/", "/database/", "/config/", "/tests/", "/vendor/", "/bootstrap/" })
        {
            var at = normalized.LastIndexOf(marker, StringComparison.Ordinal);
            if (at >= 0)
            {
                return normalized[(at + 1)..];
            }
        }
        return string.Join("/", normalized.Split('/', StringSplitOptions.RemoveEmptyEntries).TakeLast(3));
    }

    private static string Time(DateTimeOffset date) => date.ToLocalTime().ToString("HH:mm:ss", CultureInfo.InvariantCulture);

    public static string Escape(string s) =>
        s.Replace("&", "&amp;").Replace("<", "&lt;").Replace(">", "&gt;").Replace("\"", "&quot;");
}
