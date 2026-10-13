using System.Text.Json.Serialization;

namespace Flock.Kit;

// Wire models of the daemon's API. Fields the daemon may omit keep their defaults;
// Go's nil slices arrive as null and are read as empty lists (see FlockJson).

/// <summary>Contents of api.json, written by the daemon.</summary>
public sealed class ApiConfig
{
    public string Url { get; init; } = "";
    public string Token { get; init; } = "";
}

/// <summary>GET /v1/status</summary>
public sealed class Status
{
    public string Version { get; init; } = "";
    public int Pid { get; init; }
    public DateTimeOffset StartedAt { get; init; }
    public DnsStatus Dns { get; init; } = new();
    public int HttpPort { get; init; }
    public int HttpsPort { get; init; }
    public string Mail { get; init; } = "";
    public string? Postgres { get; init; }
    public string? Redis { get; init; }
    public bool Xdebug { get; init; }
    public bool Dumps { get; init; }
    public int MailCount { get; init; }
    public int DumpCount { get; init; }
    public List<Site> Sites { get; init; } = [];
    public List<Process> Processes { get; init; } = [];
    public List<string> Warnings { get; init; } = [];

    /// <summary>Something needs attention: a warning, or a supervised process that is down.</summary>
    [JsonIgnore]
    public bool IsDegraded => Warnings.Count > 0 || Processes.Any(p => !p.Up);

    public sealed class DnsStatus
    {
        public string Tld { get; init; } = "";
        /// <summary>The DNS server's address; empty on Windows, where the hosts file resolves sites.</summary>
        public string Addr { get; init; } = "";
    }

    public sealed class Site
    {
        public string Name { get; init; } = "";
        public string Domain { get; init; } = "";
        public string Path { get; init; } = "";
        public string Php { get; init; } = "";
        public string? Node { get; init; }
        public bool Secured { get; init; }
        public bool Parked { get; init; }
        public string? Url { get; init; }
        public string? Problem { get; init; }
    }

    public sealed class Process
    {
        public string Name { get; init; } = "";
        public bool Up { get; init; }
        public int Restarts { get; init; }
        public string? LastError { get; init; }
    }
}

/// <summary>An entry of GET /v1/mail.</summary>
public sealed class MailSummary
{
    public string Id { get; init; } = "";
    public string Inbox { get; init; } = "";
    public string From { get; init; } = "";
    public List<string> To { get; init; } = [];
    public string Subject { get; init; } = "";
    public DateTimeOffset Date { get; init; }
    public int Size { get; init; }
}

/// <summary>GET /v1/mail/{id}</summary>
public sealed class MailDetail
{
    public string Id { get; init; } = "";
    public string Inbox { get; init; } = "";
    public string From { get; init; } = "";
    public List<string> To { get; init; } = [];
    public List<string> Cc { get; init; } = [];
    public string Subject { get; init; } = "";
    public DateTimeOffset Date { get; init; }
    public string Html { get; init; } = "";
    public string Text { get; init; } = "";
    public List<Header> Headers { get; init; } = [];
    public List<Attachment> Attachments { get; init; } = [];

    public sealed class Header
    {
        public string Name { get; init; } = "";
        public string Value { get; init; } = "";
    }

    public sealed class Attachment
    {
        public int Index { get; init; }
        public string Filename { get; init; } = "";
        public string ContentType { get; init; } = "";
        public long Size { get; init; }
    }
}

/// <summary>An entry of GET /v1/dumps.</summary>
public sealed class Dump
{
    public string Id { get; init; } = "";
    public DateTimeOffset Time { get; init; }
    public string Site { get; init; } = "";
    public DumpSource? Source { get; init; }
    public DumpRequest? Request { get; init; }
    public string? CommandLine { get; init; }
    /// <summary>Shared by every dump of one HTTP request or CLI run.</summary>
    public string Group { get; init; } = "";
    public string? Label { get; init; }
    public string Text { get; init; } = "";
    /// <summary>Dump body only; render it after the shared header from FlockClient.DumpHeaderAsync.</summary>
    public string Html { get; set; } = "";

    /// <summary>"Controllers/HomeController.php:42" — the last path components keep rows short.</summary>
    [JsonIgnore]
    public string? ShortSource
    {
        get
        {
            if (Source is null || Source.File.Length == 0)
            {
                return null;
            }
            var parts = string.Join("/", Source.File.Split('/', '\\').TakeLast(2));
            return Source.Line > 0 ? $"{parts}:{Source.Line}" : parts;
        }
    }

    /// <summary>"GET https://app.test/x" for requests, the command line for CLI runs.</summary>
    [JsonIgnore]
    public string Origin
    {
        get
        {
            if (Request is { Uri.Length: > 0 })
            {
                return $"{Request.Method} {Request.Uri}";
            }
            return CommandLine ?? "";
        }
    }
}

public sealed class DumpSource
{
    public string File { get; init; } = "";
    public int Line { get; init; }
    public string Name { get; init; } = "";
}

public sealed class DumpRequest
{
    public string Method { get; init; } = "";
    public string Uri { get; init; } = "";
}

/// <summary>GET /v1/logs</summary>
public sealed class LogIndex
{
    public List<string> Services { get; init; } = [];
    public List<string> Sites { get; init; } = [];
}

/// <summary>An entry of GET /v1/logs/sites/{name}: one parsed Laravel log record.</summary>
public sealed class LogEntry
{
    private readonly string level = "info";

    public DateTimeOffset Time { get; init; }
    public string Env { get; init; } = "";
    public string Level { get => level; init => level = value.ToLowerInvariant(); }
    public string Message { get; init; } = "";
    public string Details { get; init; } = "";
}
