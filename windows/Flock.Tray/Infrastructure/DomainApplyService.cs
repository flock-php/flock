using Flock.Kit;

namespace Flock.Tray.Infrastructure;

/// <summary>
/// Runs the bundled CLI for suffix and domain mutations (`flock tld …`, `flock site tld …`,
/// `flock site host …`, `flock install`): the CLI owns the logic and the elevation, the app only
/// guards that a bundled CLI exists and translates the output (the macOS applyDomain doctrine,
/// minus streaming — CliProcessRunner aggregates, so the caller shows the Output itself).
/// </summary>
public sealed class DomainApplyService(CliProcessRunner processRunner)
{
    /// <summary>What the CLI prints when the user answers the elevation prompt with "No".</summary>
    public const string DeclinedSentinel = DomainApply.DeclinedSentinel;

    public bool HasBundledCli => Installation.BundledFlock is not null;

    /// <summary>
    /// Runs the app's own flock.exe with the arguments. Ok=false when this build has no bundled CLI
    /// (a development build — the Output tells the user to run the command in a terminal) or when
    /// the CLI failed. Declined singles out the declined elevation, where the config change is done
    /// but the machine-wide part isn't: the caller offers `flock install` as the finish step.
    /// </summary>
    public async Task<(bool Ok, bool Declined, string Output)> RunAsync(IReadOnlyList<string> arguments, CancellationToken cancellationToken = default)
    {
        if (Installation.BundledFlock is not { } bundled)
        {
            return (false, false, "This build of Flock has no bundled CLI — run the command in a terminal.");
        }
        var result = await processRunner.RunAsync(bundled, arguments, cancellationToken: cancellationToken);
        return (result.Succeeded, DomainApply.IsDeclined(result.Output), result.Output);
    }
}
