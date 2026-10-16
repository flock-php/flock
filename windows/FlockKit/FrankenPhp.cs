using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/frankenphp — the FrankenPHP builds, one per PHP minor, that sites can run on instead of
/// php-cgi. A build downloads when a site first needs it; installs run as jobs like the components'.
/// </summary>
public sealed class FrankenPhpState
{
    /// <summary>FrankenPHP is published for this platform.</summary>
    public bool Available { get; init; }
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<FrankenPhpBuild> Builds { get; init; } = [];

    /// <summary>Some install runs, so the UI polls.</summary>
    [JsonIgnore]
    public bool HasRunningJob => Builds.Any(b => b.Job?.IsRunning == true);
}

/// <summary>The FrankenPHP build for one PHP minor.</summary>
public sealed class FrankenPhpBuild
{
    /// <summary>The PHP minor, e.g. "8.4".</summary>
    public string Php { get; init; } = "";
    /// <summary>The installed FrankenPHP version; empty when not installed.</summary>
    public string Installed { get; init; } = "";
    /// <summary>The embedded PHP of the installed build, e.g. "8.4.14".</summary>
    public string PhpVersion { get; init; } = "";
    /// <summary>The newest FrankenPHP; empty when unknown.</summary>
    public string Latest { get; init; } = "";
    public bool Update { get; init; }
    /// <summary>Download size of Latest in bytes.</summary>
    public long Size { get; init; }
    /// <summary>An install or update, running or failed (the same shape as a component's).</summary>
    public ComponentJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;
}
