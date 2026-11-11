using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     A PHP site's Laravel Reverb server: turned on here, Flock runs php artisan reverb:start and
///     serves it at wss://&lt;domain&gt;/app. Shows its state, what the app's .env needs, Restart and
///     its output.
/// </summary>
internal sealed partial class SitesPane
{
    // Sites whose Reverb restart is in flight.
    private readonly HashSet<string> restartingReverb = [];

    private StackPanel Reverb(Status.Site site, SiteRowViewModel? row = null)
    {
        var reverb = site.Reverb;
        var toggle = new ToggleSwitch { IsOn = reverb is not null, MinWidth = 0, IsEnabled = site.ReverbAvailable || reverb is not null };
        Ui.AutomationName(toggle, "Run Reverb");
        toggle.Toggled += (_, _) =>
        {
            // Rerendered afterwards, so a refused change snaps back.
            var on = toggle.IsOn;
            Start(site.Name, () => UpdateSiteAsync(site.Name, new SiteChange(Reverb: on), null));
        };
        var section = Section("Reverb", Field("Run Reverb", toggle));
        var scheme = (row?.Secured ?? site.Secured) ? "wss" : "ws";
        if (reverb is null)
        {
            section.Children.Add(Ui.Caption(site.ReverbAvailable
                ? $"Flock runs php artisan reverb:start for the site. Browsers reach it at {scheme}://{site.Domain}/app, with no port or certificate of its own."
                : "Install Laravel Reverb in the project (php artisan install:broadcasting --reverb) to run it.", Ui.TertiaryText));
            return section;
        }

        switch (reverb.State)
        {
            case Status.ReverbState.Running:
                section.Children.Add(ReverbState(Ui.Tone.Success, $"Running at {scheme}://{site.Domain}/app"));
                break;
            case Status.ReverbState.Failed:
                section.Children.Add(ReverbState(Ui.Tone.Critical, reverb.Problem.Length > 0 ? reverb.Problem : "Failed"));
                break;
            default:
                section.Children.Add(Warning(reverb.Problem.Length > 0 ? reverb.Problem : "Reverb can't run."));
                break;
        }

        if (reverb.Env.Count > 0)
        {
            section.Children.Add(Ui.Column(6, Ui.Caption("The app's .env needs:"), Ui.CodeBlock(string.Join("\n", reverb.Env))));
        }

        var busy = restartingReverb.Contains(site.Name);
        var restart = Ui.Button("Restart", () => Start(site.Name, () => RestartReverb(site.Name)), tooltip: "Restart Reverb, e.g. so it loads changed code");
        restart.IsEnabled = reverb.IsRunning && !busy;
        var buttons = Ui.Row(8, restart);
        if (busy)
        {
            buttons.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
        }
        section.Children.Add(buttons);
        if (reverb.Log.Length > 0)
        {
            Output(section, reverb.Log, reverb.IsRunning);
        }
        section.Children.Add(Ui.Caption(
            "WebSocket connections under /app and Laravel's broadcasts under /apps go to Reverb; every other request stays the app's. Flock doesn't change the project's .env. Rebuild the front end after changing it.",
            Ui.TertiaryText));
        return section;
    }

    /// <summary>A state dot with its text, which wraps, so a long domain or problem fits the smallest window.</summary>
    private static Grid ReverbState(Ui.Tone tone, string text)
    {
        var dot = Ui.StateDot(tone);
        dot.VerticalAlignment = VerticalAlignment.Top;
        dot.Margin = new Thickness(0, 6, 0, 0);
        var label = Ui.Text(text, selectable: true);
        var line = new Grid { ColumnSpacing = 6 };
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        Grid.SetColumn(label, 1);
        line.Children.Add(dot);
        line.Children.Add(label);
        return line;
    }

    private async Task RestartReverb(string site)
    {
        restartingReverb.Add(site);
        Rerender();
        try
        {
            await Model.Sites.RestartReverbAsync(site);
        }
        finally
        {
            restartingReverb.Remove(site);
            Rerender();
        }
    }
}
