using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/node/versions — every Node.js major the package manifest offers (16 and newer, one
/// release each), plus anything installed, with install state.
/// </summary>
public sealed class NodeCatalog
{
    public DateTimeOffset? CheckedAt { get; init; }
    public string CheckError { get; init; } = "";
    public List<NodeRelease> Versions { get; init; } = [];
}

/// <summary>Values of NodeRelease.Support.</summary>
public static class NodeSupport
{
    public const string Current = "current";
    public const string Lts = "lts";
    public const string Maintenance = "maintenance";
    public const string EndOfLife = "eol";
}

/// <summary>One Node.js major line (e.g. "22") and what is installed of it.</summary>
public sealed class NodeRelease
{
    public string Major { get; init; } = "";
    /// <summary>The newest published release of the line, e.g. "22.21.0".</summary>
    public string Latest { get; init; } = "";
    /// <summary>LTS name such as "Jod"; empty for lines that aren't LTS.</summary>
    public string Codename { get; init; } = "";
    /// <summary>One of NodeSupport; empty when the schedule couldn't be loaded.</summary>
    public string Support { get; init; } = "";
    public string EndOfLife { get; init; } = "";
    /// <summary>The newest installed release of the line; empty when none is.</summary>
    public string Installed { get; init; } = "";
    [JsonPropertyName("default")]
    public bool IsDefault { get; init; }
    /// <summary>A newer release than the installed one exists.</summary>
    public bool Update { get; init; }
    /// <summary>The npm the installed version uses (bundled, or updated); empty when none is installed.</summary>
    public string Npm { get; init; } = "";
    /// <summary>The newest npm that supports the installed version, when it is newer than Npm.</summary>
    public string NpmLatest { get; init; } = "";

    [JsonIgnore]
    public bool IsInstalled => Installed.Length > 0;
}
