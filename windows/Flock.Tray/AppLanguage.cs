using System.Globalization;
using Flock.Kit;

namespace Flock.Tray;

/// <summary>
/// The UI language, chosen once at startup from the Language preference (Settings → General): a
/// language changes only with a restart, since text is set when each window is built.
/// </summary>
internal static class AppLanguage
{
    /// <summary>The preference the running app applied ("system", "en", "cs", "es").</summary>
    public static string Preference { get; private set; } = Localization.System;

    /// <summary>The language the running app shows ("en", "cs", "es").</summary>
    public static string Current { get; private set; } = "en";

    /// <summary>
    /// Sets the UI culture of this thread and of every thread started later, before any window exists.
    /// WinUI's own text (a text box's context menu) keeps following the Windows display language:
    /// Microsoft.Windows.Globalization.ApplicationLanguages.PrimaryLanguageOverride would change it, but for
    /// unpackaged apps it is only documented for languages the app declares, and reported to throw.
    /// </summary>
    public static void Apply(string preference)
    {
        Preference = preference;
        // Windows' preferred UI languages in order (Win32: WinRT's GlobalizationPreferences.Languages, the
        // full Settings → Language list, isn't safe before App.Run sets up the COM wrappers), then the
        // display language .NET started with.
        Current = Localization.Resolve(preference, [.. Native.PreferredUiLanguages(), CultureInfo.CurrentUICulture.Name]);
        var culture = CultureInfo.GetCultureInfo(Current);
        CultureInfo.DefaultThreadCurrentUICulture = culture;
        CultureInfo.CurrentUICulture = culture;
    }
}
