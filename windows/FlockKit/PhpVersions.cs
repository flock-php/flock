using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/php/versions — every PHP minor the channel's manifest offers, plus anything installed,
/// with install state and install jobs.
/// </summary>
public sealed class PhpCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<PhpRelease> Versions { get; init; } = [];

    /// <summary>Whether any install runs, so the UI polls faster.</summary>
    [JsonIgnore]
    public bool HasActiveJob => Versions.Any(v => v.Job?.IsRunning == true);
}

/// <summary>Values of PhpRelease.Status.</summary>
public static class PhpStatus
{
    public const string Installed = "installed";
    public const string Update = "update";
    public const string Available = "available";
}

/// <summary>One PHP minor version (e.g. "8.4") and what can be done with it.</summary>
public sealed class PhpRelease
{
    public string Minor { get; init; } = "";
    /// <summary>The newest release in the channel, e.g. "8.4.26"; empty when the channel has none.</summary>
    public string Latest { get; init; } = "";
    public string Installed { get; init; } = "";
    [JsonPropertyName("default")]
    public bool IsDefault { get; init; }
    /// <summary>One of PhpStatus; anything else is unknown.</summary>
    public string Status { get; init; } = "unknown";
    /// <summary>Latest (or a newer build of it) can be installed.</summary>
    public bool Update { get; init; }
    /// <summary>Download size of Latest in bytes (with the extension pack).</summary>
    public long Size { get; init; }
    /// <summary>Latest is a pre-release (beta channel).</summary>
    public bool Prerelease { get; init; }
    /// <summary>
    /// Why the installed build was withdrawn; empty when it wasn't. Flock replaces it by itself as
    /// soon as a good build is published.
    /// </summary>
    public string Yanked { get; init; } = "";
    public bool Xdebug { get; init; }
    /// <summary>An install or update, running or failed (the same shape as a component's).</summary>
    public ComponentJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;
}