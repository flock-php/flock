namespace Flock.Kit;

/// <summary>
/// What the bundled CLI's output means for the app. Suffix and host mutations run as `flock.exe`
/// commands; when their elevation prompt is answered "No", the CLI says so in its output and the
/// app offers to finish later with `flock install` — the same doctrine macOS Installation pins.
/// </summary>
public static class DomainApply
{
    /// <summary>The sentence the CLI prints when the user declines the elevation prompt.</summary>
    public const string DeclinedSentinel = "administrator permission was declined";

    /// <summary>True when the output reports a declined elevation prompt (case-sensitive, as on macOS).</summary>
    public static bool IsDeclined(string output) => output.Contains(DeclinedSentinel, StringComparison.Ordinal);
}
