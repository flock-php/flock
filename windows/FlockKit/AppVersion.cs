using System.Text.RegularExpressions;

namespace Flock.Kit;

/// <summary>
/// A released version of the app: "1.1.0", or a beta of it, "1.1.0-beta.2". A beta sorts below its
/// own release and above the release before it. Anything else (dev, git describe output) is a
/// development build, which the updater leaves alone.
/// </summary>
public sealed partial record AppVersion(int Major, int Minor, int Patch, int? Beta = null) : IComparable<AppVersion>
{
    [GeneratedRegex(@"^(\d+)\.(\d+)\.(\d+)(?:-beta\.(\d+))?$")]
    private static partial Regex Pattern();

    /// <summary>The version, or null when text isn't a released version.</summary>
    public static AppVersion? Parse(string? text)
    {
        var match = Pattern().Match(text?.Trim() ?? "");
        if (!match.Success
            || !int.TryParse(match.Groups[1].Value, out var major)
            || !int.TryParse(match.Groups[2].Value, out var minor)
            || !int.TryParse(match.Groups[3].Value, out var patch))
        {
            return null;
        }
        int? beta = null;
        if (match.Groups[4].Success)
        {
            if (!int.TryParse(match.Groups[4].Value, out var n))
            {
                return null;
            }
            beta = n;
        }
        return new AppVersion(major, minor, patch, beta);
    }

    /// <summary>The app's version from its informational version ("1.1.0+3f2a…" when the SDK appends the commit).</summary>
    public static AppVersion? FromInformational(string? informational) => Parse(informational?.Split('+')[0]);

    public bool IsBeta => Beta is not null;

    public int CompareTo(AppVersion? other)
    {
        if (other is null)
        {
            return 1;
        }
        var order = (Major, Minor, Patch).CompareTo((other.Major, other.Minor, other.Patch));
        if (order != 0)
        {
            return order;
        }
        // The release itself is newer than every beta of it, including beta.int.MaxValue.
        if (Beta is null)
        {
            return other.Beta is null ? 0 : 1;
        }
        return other.Beta is null ? -1 : Beta.Value.CompareTo(other.Beta.Value);
    }

    public static bool operator >(AppVersion a, AppVersion b) => a.CompareTo(b) > 0;
    public static bool operator <(AppVersion a, AppVersion b) => a.CompareTo(b) < 0;
    public static bool operator >=(AppVersion a, AppVersion b) => a.CompareTo(b) >= 0;
    public static bool operator <=(AppVersion a, AppVersion b) => a.CompareTo(b) <= 0;

    public override string ToString() => Beta is { } n ? $"{Major}.{Minor}.{Patch}-beta.{n}" : $"{Major}.{Minor}.{Patch}";
}