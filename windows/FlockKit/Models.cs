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
    /// <summary>"9.1.2 on 127.0.0.1:6379" when Valkey is installed (macOS only).</summary>
    public string? Valkey { get; init; }
    /// <summary>The installed catalog services (MySQL, Redis, ...).</summary>
    public List<ExtraService> Extras { get; init; } = [];
    /// <summary>Xdebug detection: requests that ask for Xdebug go to a PHP pool that loads it.</summary>
    public bool Xdebug { get; init; }
    /// <summary>Sites with PhpStorm breakpoints send every request to Xdebug.</summary>
    public bool XdebugBreakpoints { get; init; }
    public bool Dumps { get; init; }
    public int MailCount { get; init; }
    public int DumpCount { get; init; }
    public List<Site> Sites { get; init; } = [];
    public List<Process> Processes { get; init; } = [];
    public List<string> Warnings { get; init; } = [];

    /// <summary>An installed catalog service, for the tray panel.</summary>
    public sealed class ExtraService
    {
        private readonly string? name;

        public string Id { get; init; } = "";
        public string Name { get => name ?? Id; init => name = value; }
        public string Version { get; init; } = "";
        public int Port { get; init; }
        public bool Enabled { get; init; } = true;
        public bool Up { get; init; }
    }

    /// <summary>Something needs attention: a warning, or a supervised process that is down.</summary>
    [JsonIgnore]
    public bool IsDegraded => Warnings.Count > 0 || Processes.Any(p => !p.Up);

    public sealed class DnsStatus
    {
        public string Tld { get; init; } = "";
        /// <summary>The DNS server's address; empty when it isn't running.</summary>
        public string Addr { get; init; } = "";
        /// <summary>
        /// Every name under the TLD resolves (any subdomain). When false (e.g. a company DNS policy
        /// overrides Flock's rule), only site domains and extra domains resolve, through the hosts file.
        /// </summary>
        public bool Wildcard { get; init; }
        public string WildcardProblem { get; init; } = "";
        /// <summary>Subdomain levels below a site (and its aliases) that are served.</summary>
        public int MaxDepth { get; init; }
    }

    /// <summary>One of a site's extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test").</summary>
    public sealed class SiteDomain
    {
        /// <summary>As stored, and as the API takes it for removal.</summary>
        public string Entry { get; init; } = "";
        /// <summary>The full name, e.g. tenant1.app.test.</summary>
        public string Host { get; init; } = "";
        public bool Alias { get; init; }
        public string? Url { get; init; }
        /// <summary>Why it isn't served, e.g. another site uses it.</summary>
        public string? Problem { get; init; }
    }

    public sealed class Site
    {
        public string Name { get; init; } = "";
        public string Domain { get; init; } = "";
        public string Path { get; init; } = "";
        /// <summary>The PHP version the site uses; PhpSource says why (see VersionSource).</summary>
        public string Php { get; init; } = "";
        public string PhpSource { get; init; } = VersionSource.Default;
        /// <summary>What the project pins (in PhpFile), also when an isolation wins over it; empty when nothing is pinned.</summary>
        public string PhpPinned { get; init; } = "";
        public string PhpFile { get; init; } = "";
        /// <summary>The Node.js version (usually a major) the site uses; NodeSource says why.</summary>
        public string? Node { get; init; }
        public string NodeSource { get; init; } = VersionSource.Default;
        public string NodePinned { get; init; } = "";
        public string NodeFile { get; init; } = "";
        /// <summary>A .nvmrc / .node-version that .flockrc's node= wins over, e.g. ".nvmrc: 20".</summary>
        public string NodeShadowed { get; init; } = "";
        public bool Secured { get; init; }
        public bool Parked { get; init; }
        public string? Url { get; init; }
        public string? Problem { get; init; }
        public List<SiteDomain> Domains { get; init; } = [];
        /// <summary>PhpStorm breakpoints send every request of the site to Xdebug.</summary>
        public bool Debugging { get; init; }

        /// <summary>The project file pinning PHP sits in the site's own folder (so it can be unpinned here).</summary>
        [System.Text.Json.Serialization.JsonIgnore]
        public bool PhpPinnedHere => PhpFile.Length > 0 && SameDir(PhpFile, Path);

        [System.Text.Json.Serialization.JsonIgnore]
        public bool NodePinnedHere => NodeFile.Length > 0 && SameDir(NodeFile, Path);

        // Splits by hand rather than with Path.GetDirectoryName, which ignores '\\' off Windows (tests run anywhere).
        private static bool SameDir(string file, string dir) =>
            string.Equals(file[..Math.Max(0, file.LastIndexOfAny(['\\', '/']))].TrimEnd('\\', '/'), dir.TrimEnd('\\', '/'), StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>Where a site's PHP or Node.js version comes from, in this order.</summary>
    public static class VersionSource
    {
        /// <summary>Set for the site in Flock, on this machine only (flock isolate).</summary>
        public const string Isolated = "isolated";
        /// <summary>Pinned in the project: .flockrc, or for Node .nvmrc / .node-version (flock pin).</summary>
        public const string Project = "project";
        public const string Default = "default";
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
