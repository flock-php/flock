namespace Flock.Kit.Tests;

public class LogFilterTests
{
    private static readonly List<LogEntry> Entries =
    [
        new() { Level = "debug", Message = "Cache warmed" },
        new() { Level = "INFO", Message = "User logged in", Details = """{"id":42}""" },
        new() { Level = "warning", Message = "Slow query" },
        new() { Level = "error", Message = "Division by zero", Details = "#0 /app/routes/web.php(5)" },
        new() { Level = "critical", Message = "Disk full" },
        new() { Level = "custom", Message = "Something else" },
    ];

    private static List<string> Messages(string query, LogLevelFilter level) =>
        LogFilter.Filter(Entries, query, level).Select(e => e.Message).ToList();

    [Fact]
    public void FiltersByMinimumLevel()
    {
        Assert.Equal(6, Messages("", LogLevelFilter.All).Count);
        Assert.Equal(["User logged in", "Slow query", "Division by zero", "Disk full", "Something else"], Messages("", LogLevelFilter.Info));
        Assert.Equal(["Slow query", "Division by zero", "Disk full", "Something else"], Messages("", LogLevelFilter.Warning));
        // Levels Monolog doesn't know are never hidden.
        Assert.Equal(["Division by zero", "Disk full", "Something else"], Messages("", LogLevelFilter.Error));
    }

    [Fact]
    public void SearchesMessageAndContextIgnoringCase()
    {
        Assert.Equal(["Division by zero"], Messages("WEB.PHP", LogLevelFilter.All));
        Assert.Equal(["User logged in"], Messages(" \"id\":42 ", LogLevelFilter.All));
        Assert.Equal(["Slow query"], Messages("slow", LogLevelFilter.Warning));
        Assert.Empty(Messages("slow", LogLevelFilter.Error));
    }

    [Fact]
    public void FiltersServiceLogLines()
    {
        Assert.Equal("b: Error\nd: error", LogFilter.FilterLines("a: ok\nb: Error\nc: ok\nd: error", "ERROR"));
        Assert.Equal("a\nb", LogFilter.FilterLines("a\nb", " "));
    }
}
