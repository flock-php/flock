namespace Flock.Kit;

/// <summary>The minimum level the Logs window shows ("Warning and above").</summary>
public enum LogLevelFilter { All, Info, Warning, Error }

/// <summary>Search and level filtering of a site's Laravel log, as in the Logs window.</summary>
public static class LogFilter
{
    // Monolog's levels, lowest first, as the daemon's laravellog parses them (lowercased).
    private static readonly string[] Levels = ["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"];

    public static readonly IReadOnlyList<(LogLevelFilter Filter, string Name)> Choices =
    [
        (LogLevelFilter.All, "All levels"),
        (LogLevelFilter.Error, "Error and above"),
        (LogLevelFilter.Warning, "Warning and above"),
        (LogLevelFilter.Info, "Info and above"),
    ];

    /// <summary>A level's rank; levels Monolog doesn't know rank highest, so no filter hides them.</summary>
    public static int Rank(string level)
    {
        var index = Array.IndexOf(Levels, level.ToLowerInvariant());
        return index < 0 ? int.MaxValue : index;
    }

    private static int Minimum(LogLevelFilter filter) => filter switch
    {
        LogLevelFilter.Info => Rank("info"),
        LogLevelFilter.Warning => Rank("warning"),
        LogLevelFilter.Error => Rank("error"),
        _ => 0,
    };

    /// <summary>Entries at or above the level whose message or context contain the query (ignoring case).</summary>
    public static List<LogEntry> Filter(IEnumerable<LogEntry> entries, string query, LogLevelFilter level)
    {
        var terms = query.Trim();
        var minimum = Minimum(level);
        return entries.Where(e => Rank(e.Level) >= minimum
            && (terms.Length == 0
                || e.Message.Contains(terms, StringComparison.OrdinalIgnoreCase)
                || e.Details.Contains(terms, StringComparison.OrdinalIgnoreCase))).ToList();
    }

    /// <summary>The lines of a service log that contain the query (ignoring case).</summary>
    public static string FilterLines(string text, string query)
    {
        var terms = query.Trim();
        return terms.Length == 0
            ? text
            : string.Join("\n", text.Split('\n').Where(line => line.Contains(terms, StringComparison.OrdinalIgnoreCase)));
    }
}