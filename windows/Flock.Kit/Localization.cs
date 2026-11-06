using System.Globalization;
using System.Resources;

namespace Flock.Kit;

/// <summary>
/// The app's languages and the formatting its translated strings need: English (the source and
/// fallback), Czech and Spanish. Strings come from Strings.resx and its .cs/.es satellites, looked up
/// in CultureInfo.CurrentUICulture, which the app sets from the Language preference at startup.
/// </summary>
public static class Localization
{
    /// <summary>The Language preference that follows the system's preferred languages.</summary>
    public const string System = "system";

    /// <summary>The languages the app is translated to, English first (the fallback).</summary>
    public static IReadOnlyList<string> Languages { get; } = ["en", "cs", "es"];

    /// <summary>
    /// The language to show: the preferred one when it is translated, else the first of the system's
    /// preferred languages (most preferred first, e.g. "de-DE", "cs-CZ") that is, else English.
    /// </summary>
    public static string Resolve(string? preference, IEnumerable<string> systemLanguages)
    {
        if (preference is not null && Languages.Contains(preference))
        {
            return preference;
        }
        foreach (var language in systemLanguages)
        {
            var primary = language.Split('-', '_')[0].ToLowerInvariant();
            if (Languages.Contains(primary))
            {
                return primary;
            }
        }
        return "en";
    }

    /// <summary>A translated format string filled in with the user's number and date formats.</summary>
    public static string Format(string format, params object?[] args) => string.Format(CultureInfo.CurrentCulture, format, args);

    /// <summary>
    /// A count in a sentence ("3 sites", "před 3 minutami"): the resource <c>key_one</c>, <c>key_few</c>
    /// (Czech) or <c>key_other</c> for the count in the current UI language, with the count as {0} and
    /// args from {1} on. A missing <c>_few</c> falls back to <c>_other</c>.
    /// </summary>
    public static string Plural(ResourceManager resources, string key, long count, params object?[] args)
    {
        var culture = CultureInfo.CurrentUICulture;
        var format = resources.GetString($"{key}_{PluralCategory(count, culture)}", culture)
                     ?? resources.GetString(key + "_other", culture)
                     ?? throw new MissingManifestResourceException($"No plural resource {key}_other.");
        return string.Format(CultureInfo.CurrentCulture, format, [count, .. args]);
    }

    /// <summary>
    /// The CLDR plural category of a whole number: "one" or "other" in English and Spanish; in Czech
    /// also "few" for 2–4 (1 web, 2 weby, 5 webů).
    /// </summary>
    public static string PluralCategory(long count, CultureInfo culture)
    {
        if (culture.TwoLetterISOLanguageName == "cs")
        {
            return count switch
            {
                1 => "one",
                >= 2 and <= 4 => "few",
                _ => "other",
            };
        }
        return count == 1 ? "one" : "other";
    }
}
