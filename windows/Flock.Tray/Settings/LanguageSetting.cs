using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Settings;

/// <summary>
/// The Language setting: Settings → General, and the first-run setup, where Settings isn't available
/// yet. A new language applies after Restart Flock (App.Relaunch).
/// </summary>
internal static class LanguageSetting
{
    /// <summary>The Language preference of each item of the picker, in order.</summary>
    private static readonly string[] Choices = [Localization.System, "en", "cs", "es"];

    public static StackPanel Create(PreferencesService preferences)
    {
        // The languages' own names, the same in every language.
        var picker = new ComboBox { MinWidth = 200, Items = { Strings.General_LanguageSystem, "English", "Čeština", "Español" } };
        picker.SelectedIndex = Math.Max(0, Array.IndexOf(Choices, preferences.Snapshot.Language));
        Ui.AutomationName(picker, Strings.General_AppLanguage);

        Button? restart = null;
        restart = Ui.Button(Strings.General_RestartFlock, () =>
        {
            // Disabled while this instance quits; enabled again only when the new one couldn't start.
            restart!.IsEnabled = false;
            restart.IsEnabled = !App.Current.Relaunch();
        }, true);
        var note = Ui.Setting(Strings.General_LanguageRestartNote, null, restart);

        void UpdateNote() =>
            note.Visibility = preferences.Snapshot.Language != AppLanguage.Preference ? Visibility.Visible : Visibility.Collapsed;

        picker.SelectionChanged += async (_, _) =>
        {
            if (picker.SelectedIndex < 0)
            {
                return;
            }
            var language = Choices[picker.SelectedIndex];
            try
            {
                await preferences.UpdateAsync(value => value with { Language = language });
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException or OperationCanceledException)
            {
                // PreferencesService reports a failed save (Error), which Settings shows.
            }
            UpdateNote();
        };
        UpdateNote();
        return Ui.Section(Strings.General_Language, Ui.Setting(Strings.General_AppLanguage, Strings.General_AppLanguageDescription, picker), note);
    }
}
