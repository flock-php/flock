using System.Globalization;
using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/postgres/versions — the PostgreSQL majors Flock offers (18 down to 14), what is installed
/// of each, and which one is active. One PostgreSQL runs at a time, on the PostgreSQL port; every
/// major keeps its own data directory, so switching leaves the other's databases untouched.
/// </summary>
public sealed class PostgresVersions
{
    /// <summary>The major selected to run; empty when none is installed.</summary>
    public string Active { get; init; } = "";
    public bool Running { get; init; }
    /// <summary>Not switched off by the user.</summary>
    public bool Enabled { get; init; }
    public int Port { get; init; }
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    /// <summary>Always 18, 17, 16, 15, 14 (newest first).</summary>
    public List<PostgresMajor> Majors { get; init; } = [];

    /// <summary>The active major's entry, when one is active.</summary>
    [JsonIgnore]
    public PostgresMajor? ActiveMajor => Majors.FirstOrDefault(m => m.Major == Active && Active.Length > 0);

    [JsonIgnore]
    public bool AnyInstalled => Majors.Any(m => m.IsInstalled);

    /// <summary>Some major has an operation in flight (maybe started by another window or the CLI), so the UI polls.</summary>
    [JsonIgnore]
    public bool AnyBusy => Majors.Any(m => m.IsBusy);

    [JsonIgnore]
    public bool AnyUpdate => Majors.Any(m => m.Update);
}

/// <summary>Values of PostgresMajor.Busy; anything else is an operation this app doesn't know yet.</summary>
public static class PostgresBusy
{
    public const string Installing = "installing";
    public const string Updating = "updating";
    public const string Switching = "switching";
    public const string Uninstalling = "uninstalling";
}

/// <summary>How close a version is to its upstream end of life.</summary>
public enum EndOfLife { Unknown, Supported, Soon, Past }

/// <summary>One PostgreSQL major (e.g. "18") and what is installed of it.</summary>
public sealed class PostgresMajor
{
    public string Major { get; init; } = "";
    /// <summary>The installed minor, e.g. "18.6.0"; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>The newest release for this platform; empty when unknown (never checked, offline).</summary>
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    public bool Active { get; init; }
    /// <summary>A data directory (cluster) of this major exists — also after uninstalling it.</summary>
    public bool HasData { get; init; }
    public long DataSize { get; init; }
    /// <summary>Upstream end-of-life date (yyyy-MM-dd); empty when unknown.</summary>
    public string Eol { get; init; } = "";
    /// <summary>One of PostgresBusy, or empty when idle.</summary>
    public string Busy { get; init; } = "";

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;

    [JsonIgnore]
    public bool IsBusy => Busy.Length > 0;

    /// <summary>"Installing…", "Switching…"; a generic text for operations this app doesn't know.</summary>
    [JsonIgnore]
    public string BusyText => Busy switch
    {
        PostgresBusy.Installing => "Installing…",
        PostgresBusy.Updating => "Updating…",
        PostgresBusy.Switching => "Switching…",
        PostgresBusy.Uninstalling => "Uninstalling…",
        "" => "",
        _ => "Working…",
    };

    [JsonIgnore]
    public DateOnly? EolDate =>
        DateOnly.TryParseExact(Eol, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out var date) ? date : null;

    /// <summary>Past its end of life, or within about six months of it.</summary>
    public EndOfLife EndOfLifeState(DateOnly today) => EolDate switch
    {
        null => EndOfLife.Unknown,
        { } date when date <= today => EndOfLife.Past,
        { } date when date <= today.AddMonths(6) => EndOfLife.Soon,
        _ => EndOfLife.Supported,
    };
}

/// <summary>
/// GET /v1/components — tools Flock installs on request: the PostgreSQL command-line tools (and Valkey on macOS).
/// Installs run as jobs in the daemon; a finished job disappears, a failed one stays until dismissed.
/// </summary>
public sealed class ComponentCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<Component> Components { get; init; } = [];

    /// <summary>Some job is running, so the UI polls.</summary>
    [JsonIgnore]
    public bool HasRunningJob => Components.Any(c => c.Job?.IsRunning == true);

    public Component? this[string id] => Components.FirstOrDefault(c => c.Id == id);
}

/// <summary>Values of Component.Id.</summary>
public static class ComponentId
{
    public const string Valkey = "valkey";
    public const string PostgresClient = "postgres-client";
}

/// <summary>Values of Component.Method.</summary>
public static class ComponentMethod
{
    /// <summary>Compiled from source (macOS).</summary>
    public const string Build = "build";
    /// <summary>A prebuilt download (Windows).</summary>
    public const string Download = "download";
}

public sealed class Component
{
    public string Id { get; init; } = "";
    public string Name { get; init; } = "";
    public string Description { get; init; } = "";
    /// <summary>The installed version; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>Empty when unknown.</summary>
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    /// <summary>One of ComponentMethod.</summary>
    public string Method { get; init; } = "";
    /// <summary>A hint such as "12 MB download" or "about 2 minutes"; may be empty.</summary>
    public string Estimate { get; init; } = "";
    public ComponentJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;
}

/// <summary>An install or update of a component, running or failed.</summary>
public sealed class ComponentJob
{
    /// <summary>"install" or "update".</summary>
    public string Action { get; init; } = "";
    /// <summary>"running" or "failed"; anything else counts as neither.</summary>
    public string State { get; init; } = "";
    /// <summary>"downloading", "building" or "installing"; see PhaseText.</summary>
    public string Phase { get; init; } = "";
    /// <summary>0…1, or -1 when the job can't tell (builds).</summary>
    public double Progress { get; init; } = -1;
    public string Error { get; init; } = "";
    public DateTimeOffset? StartedAt { get; init; }

    [JsonIgnore]
    public bool IsRunning => State == "running";

    [JsonIgnore]
    public bool IsFailed => State == "failed";

    [JsonIgnore]
    public bool HasProgress => Progress is >= 0 and <= 1;

    /// <summary>"Downloading…", "Building…", "Installing…"; a generic text for phases this app doesn't know.</summary>
    [JsonIgnore]
    public string PhaseText => Phase switch
    {
        "downloading" => "Downloading…",
        "building" => "Building…",
        "installing" => "Installing…",
        _ => Action == "update" ? "Updating…" : "Working…",
    };
}

/// <summary>
/// GET /v1/catalog — the services Flock installs on request besides PostgreSQL: MySQL, MariaDB,
/// Redis, MongoDB, Meilisearch, Typesense, RustFS. Each runs one version (of a line the user
/// picks) on its own port; installs run as jobs like the components'.
/// </summary>
public sealed class ServiceCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<CatalogService> Services { get; init; } = [];

    /// <summary>Some install runs, so the UI polls.</summary>
    [JsonIgnore]
    public bool HasRunningJob => Services.Any(s => s.Job?.IsRunning == true);

    public CatalogService? this[string id] => Services.FirstOrDefault(s => s.Id == id);
}

public sealed class CatalogService
{
    private readonly string? name;

    public string Id { get; init; } = "";
    public string Name { get => name ?? Id; init => name = value; }
    public string Category { get; init; } = "Other";
    public string Description { get; init; } = "";
    public bool Available { get; init; } = true;
    /// <summary>Why it isn't available on this platform.</summary>
    public string Unavailable { get; init; } = "";
    /// <summary>One of ComponentMethod; empty when unavailable.</summary>
    public string Method { get; init; } = "";
    public string Estimate { get; init; } = "";
    /// <summary>The installed version; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>The line of the installed version ("8.4").</summary>
    public string Line { get; init; } = "";
    /// <summary>The newest release of Line; empty when unknown.</summary>
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    /// <summary>The configured port, or the one Flock suggests when not installed.</summary>
    public int Port { get; init; }
    public int DefaultPort { get; init; }
    public List<CatalogLine> Lines { get; init; } = [];
    public CatalogJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;

    /// <summary>The installed line, else the newest.</summary>
    [JsonIgnore]
    public CatalogLine? PreferredLine => Lines.FirstOrDefault(l => l.Installed) ?? Lines.FirstOrDefault();
}

/// <summary>A release line of a catalog service ("8.4 LTS").</summary>
public sealed class CatalogLine
{
    private readonly string? label;

    public string Id { get; init; } = "";
    public string Label { get => label ?? Id; init => label = value; }
    public string Latest { get; init; } = "";
    public bool Installed { get; init; }
    /// <summary>The line has data (each line keeps its own).</summary>
    public bool HasData { get; init; }
}

/// <summary>An install of a catalog service, running or failed.</summary>
public sealed class CatalogJob
{
    public string Action { get; init; } = "";
    public string State { get; init; } = "";
    public string Phase { get; init; } = "";
    public double Progress { get; init; } = -1;
    public string Error { get; init; } = "";
    public DateTimeOffset? StartedAt { get; init; }
    /// <summary>What it installs; empty until the newest release is known.</summary>
    public string Version { get; init; } = "";
    public string Line { get; init; } = "";

    [JsonIgnore]
    public bool IsRunning => State == "running";

    [JsonIgnore]
    public bool IsFailed => State == "failed";

    [JsonIgnore]
    public bool HasProgress => Progress is >= 0 and <= 1;

    [JsonIgnore]
    public string PhaseText => Phase switch
    {
        "downloading" => "Downloading…",
        "building" => "Building…",
        "installing" => "Installing…",
        _ => Action == "update" ? "Updating…" : "Working…",
    };
}
