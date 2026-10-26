using Microsoft.Extensions.Logging;

namespace Flock.Tray.Infrastructure;

/// <summary>Forwards structured logger records to the application-owned file sink.</summary>
public sealed class FileLogProvider(FileLogSink sink) : ILoggerProvider
{
    public ILogger CreateLogger(string categoryName) => new Logger(categoryName, sink);

    public void Dispose()
    {
    }

    private sealed class Logger(string category, FileLogSink sink) : ILogger
    {
        public IDisposable BeginScope<TState>(TState state) where TState : notnull => NullScope.Instance;

        public bool IsEnabled(LogLevel logLevel) => logLevel >= LogLevel.Information;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            if (!IsEnabled(logLevel))
            {
                return;
            }
            var message = Redact(formatter(state, exception));
            sink.TryWrite($"{DateTimeOffset.Now:O} {logLevel} {category}: {message}{(exception is null ? "" : $"\n{Redact(exception.ToString())}")}");
        }

        private static string Redact(string message) => message.Replace("Bearer ", "Bearer [redacted]", StringComparison.OrdinalIgnoreCase);

        private sealed class NullScope : IDisposable
        {
            public static readonly NullScope Instance = new();
            public void Dispose() { }
        }
    }
}