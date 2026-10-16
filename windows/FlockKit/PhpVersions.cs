using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/php/versions — every PHP minor Flock knows about (php.net releases from 7.4 up, plus
/// anything installed), with install state and background jobs. On Windows a "build" downloads
/// the official windows.php.net build instead of compiling.
/// </summary>
public sealed class PhpCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<PhpRelease> Versions { get; init; } = [];

    /// <summary>Whether any job is queued or running, so the UI polls faster.</summary>
    [JsonIgnore]
    public bool HasActiveJob => Versions.Any(v => v.Job?.IsActive == true);
}

/// <summary>Values of PhpRelease.Status.</summary>
public static class PhpStatus
{
    public const string Installed = "installed";
    public const string Update = "update";
    public const string Ready = "ready";
    public const string Likely = "likely";
    public const string Unsupported = "unsupported";
    public const string Failed = "failed";
}

/// <summary>One PHP minor version (e.g. "8.4") and what can be done with it.</summary>
public sealed class PhpRelease
{
    public string Minor { get; init; } = "";
    public string Latest { get; init; } = "";
    public string Installed { get; init; } = "";
    [JsonPropertyName("default")]
    public bool IsDefault { get; init; }
    /// <summary>Still maintained upstream; false means end of life.</summary>
    public bool Supported { get; init; } = true;
    /// <summary>One of PhpStatus; anything else is unknown.</summary>
    public string Status { get; init; } = "unknown";
    /// <summary>The update is already downloaded/built, so installing it is instant.</summary>
    public bool UpdateReady { get; init; }
    public bool Xdebug { get; init; }
    /// <summary>Why a version is unsupported, in plain words; empty otherwise.</summary>
    public string Note { get; init; } = "";
    public PhpBuildJob? Job { get; init; }

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;
}

/// <summary>A build (or, on Windows, a download) of one PHP version, queued or run by the daemon.</summary>
public sealed class PhpBuildJob
{
    public string State { get; init; } = "unknown";
    /// <summary>Installs the result when the job succeeds.</summary>
    public bool Install { get; init; }
    /// <summary>Started by Flock in the background rather than by the user.</summary>
    public bool Auto { get; init; }
    /// <summary>The latest progress line.</summary>
    public string Step { get; init; } = "";
    public string FullVersion { get; init; } = "";
    public DateTimeOffset? StartedAt { get; init; }
    public DateTimeOffset? FinishedAt { get; init; }
    public string Error { get; init; } = "";

    [JsonIgnore]
    public bool IsActive => State is "queued" or "running";
}
