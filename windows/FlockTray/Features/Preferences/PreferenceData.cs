namespace Flock.Tray.Features.Preferences;

/// <summary>Persisted tray preferences. This type has no file or UI side effects.</summary>
public sealed record PreferenceData
{
    public string Editor { get; init; } = "PhpStorm";
    public bool DumpsLatestOnly { get; init; } = true;
    public string InstalledFlock { get; init; } = "";
    public string Channel { get; init; } = "stable";
}