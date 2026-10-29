using System.Text.Json.Serialization;

namespace Flock.Kit;

/// <summary>
/// GET /v1/sites/{name}/extensions — the PHP extensions a site requires (ext-* in composer.json and
/// its locked packages), the ones its Laravel configuration shows it uses without requiring them,
/// and whether Flock's PHP has each.
/// </summary>
public sealed class ExtensionReport
{
    public string Php { get; init; } = "";
    /// <summary>Hints are looked for (Laravel apps only).</summary>
    public bool Laravel { get; init; }
    public List<PhpExtension> Extensions { get; init; } = [];
}

/// <summary>One extension a project needs.</summary>
public sealed class PhpExtension
{
    /// <summary>Composer's name without "ext-", e.g. "intl".</summary>
    public string Name { get; init; } = "";
    /// <summary>"composer.json" and/or the packages that require it.</summary>
    public List<string> RequiredBy { get; init; } = [];
    /// <summary>Why the project seems to use it although nothing requires it; empty when something does.</summary>
    public string Hint { get; init; } = "";
    /// <summary>Flock's PHP has it.</summary>
    public bool Available { get; init; }

    [JsonIgnore]
    public bool Declared => RequiredBy.Count > 0;
}