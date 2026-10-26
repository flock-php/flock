using Flock.Kit;
using Flock.Tray.ViewModels;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed class GeneralPane(SettingsWindow window, RuntimeService model) : Pane(window, model)
{
    private readonly GeneralViewModel viewModel = new(model);
    private string? selectedParked;

    public override string Title => "General";
    public override string Subtitle => "The Flock daemon runs your sites, PHP and services in the background.";

    public override Task ActivateAsync(CancellationToken hidden) => viewModel.RefreshAsync(hidden);

    public override UIElement Build()
    {
        var page = new StackPanel();

        var restart = Ui.Button(Model.IsRestarting ? "Restarting…" : "Restart Daemon", () => _ = Model.RestartAsync());
        restart.IsEnabled = !Model.IsRestarting;
        var daemon = new List<UIElement>();
        if (Model.Status is { } status)
        {
            daemon.Add(Ui.Setting("Status", null, Ui.Row(8, Ui.StatusDot(!status.IsDegraded), Ui.Text(status.IsDegraded ? "Needs attention" : "Running", wrap: false))));
            daemon.Add(Ui.Setting("Version", null, Ui.Text(status.Version, foreground: Ui.SecondaryText, wrap: false)));
            daemon.Add(Ui.Setting("Process", null, Ui.Text($"pid {status.Pid}", foreground: Ui.SecondaryText, wrap: false)));
        }
        daemon.Add(Ui.Setting("Restart", "Restarts the daemon and every service it runs.", restart));
        page.Children.Add(Ui.Section("Daemon", [.. daemon]));

        if (viewModel.Config is { } config)
        {
            var network = new List<UIElement>
            {
                Ui.Setting("Domain", Model.Status?.Dns is { Wildcard: true }
                        ? "Every name, any subdomain included, is resolved by Flock's DNS server. Site domains are also in the hosts file."
                        : $"Only site domains and the domains added to sites resolve (the hosts file)" +
                          $"{
                            (Model.Status?.Dns?.WildcardProblem is { Length: > 0 } p
                                ? $": {p}"
                                : string.Empty)
                        }.",
                    Value($"*.{config.Tld}")),
            };
            // Sites are served on 80 and 443; other ports are only set by editing config.json.
            if (config.Ports.Http != 80 || config.Ports.Https != 443)
            {
                network.Add(Footnote($"Sites use ports {config.Ports.Http} (HTTP) and {config.Ports.Https} (HTTPS), set in config.json, so their addresses include the port."));
            }
            page.Children.Add(Ui.Section("Network", [.. network]));

            var path = Ui.Text(config.Root, foreground: Ui.SecondaryText, wrap: false);
            path.MaxWidth = 420;
            page.Children.Add(Ui.Section("Data",
                Ui.Setting("Folder", null, Ui.Row(12, path, Ui.Button("Show in Explorer", () => Model.Reveal(config.Root))))));
        }

        var startup = new ToggleSwitch { IsOn = Native.StartsAtSignIn };
        startup.Toggled += (_, _) =>
        {
            try
            {
                Native.StartsAtSignIn = startup.IsOn;
            }
            catch (Exception e) when (e is UnauthorizedAccessException or System.Security.SecurityException or IOException)
            {
                Model.ShowRuntimeError($"Could not change the startup setting: {e.Message}");
            }
        };
        page.Children.Add(Ui.Section("Startup",
            Ui.Setting("Open Flock at sign-in", "Shows the Flock icon in the notification area when you sign in. The daemon itself always starts at sign-in.", startup)));

        var updates = new List<UIElement> { AppUpdate() };
        if (viewModel.Config is { } channelConfig)
        {
            var experimental = channelConfig.Channel == "experimental";
            var beta = new ToggleSwitch { IsOn = channelConfig.Channel == "beta", IsEnabled = !experimental };
            beta.Toggled += async (_, _) =>
            {
                await viewModel.SetChannelAsync(beta.IsOn ? "beta" : "stable");
                await Model.UpdateService.CheckAsync(userInitiated: false);
            };
            updates.Add(Ui.Setting("Beta versions", "Get new versions of Flock, PHP and services earlier, including pre-releases. They may not work yet.", beta));
            if (experimental)
            {
                updates.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution),
                    Ui.Caption("Experimental channel: packages that aren't released yet are offered. `flock channel stable` switches back.", Ui.Caution)));
            }
        }
        page.Children.Add(Ui.Section("Updates", [.. updates]));

        if (viewModel.Config is { } cfg)
        {
            page.Children.Add(Parked(cfg));
        }
        return page;
    }

    /// <summary>The app's version and its update: what the last check found, a download in progress, or why it failed.</summary>
    private Border AppUpdate()
    {
        var updater = Model.UpdateService;
        if (!updater.IsEnabled || Features.Update.UpdateService.Current is not { } current)
        {
            return Ui.Setting("Flock", "Updates need a version such as 1.0.0. Set InformationalVersion when building to test updates.", null);
        }
        var idle = !updater.IsBusy;
        var summary = updater.Summary;
        var description = summary.Length > 0 ? $"Version {current} · {summary}" : $"Version {current}";
        var button = updater.Available is { } update
            ? Ui.Button($"Update to {update.Version}", () => _ = updater.InstallAsync(), accent: true)
            : Ui.Button("Check for Updates", () => _ = updater.CheckAsync(userInitiated: true));
        button.IsEnabled = idle;
        var control = idle ? (UIElement)button : Ui.Row(12, new ProgressRing { Width = 16, Height = 16 }, button);
        if (updater.Available is { } available && (available.Notes.Length > 0 || available.NotesUrl is not null) && idle)
        {
            control = Ui.Row(12, Ui.Link("Release Notes", async () =>
            {
                if (available.Notes.Length > 0)
                {
                    await ReleaseNotesAsync(available);
                }
                else if (available.NotesUrl is { } url)
                {
                    Model.Open(url);
                }
            }), button);
        }
        return Ui.Setting("Flock", description, control);
    }

    /// <summary>An update's release notes, like Sparkle's update window on macOS: install it from here, or read it on GitHub.</summary>
    private async Task ReleaseNotesAsync(UpdateFeed.Item update)
    {
        var notes = new HtmlView(allowScripts: false, blockRemote: false, whiteBackground: false, shell: Model.Shell) { Width = 500, Height = 380 };
        var load = notes.LoadAsync(ReleaseNotesPage(update.Notes));
        var dialog = new ContentDialog
        {
            Title = $"Flock {update.Version}",
            Content = notes,
            PrimaryButtonText = $"Update to {update.Version}",
            SecondaryButtonText = update.NotesUrl is null ? "" : "View on GitHub",
            CloseButtonText = "Close",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.Content.XamlRoot,
        };
        // Wide enough for the notes; the default caps the dialog at 548 pixels.
        dialog.Resources["ContentDialogMaxWidth"] = 600.0;
        ContentDialogResult result;
        try
        {
            result = await dialog.ShowAsync();
        }
        finally
        {
            await load;
            await notes.ReleaseAsync();
        }
        switch (result)
        {
            case ContentDialogResult.Primary:
                await Model.UpdateService.InstallAsync();
                break;
            case ContentDialogResult.Secondary:
                Model.Open(update.NotesUrl!);
                break;
        }
    }

    /// <summary>The feed's notes (GitHub-rendered markdown) as a page that matches the app in both themes.</summary>
    private static string ReleaseNotesPage(string notes) => $$"""
        <!doctype html>
        <html>
        <head>
        <meta charset="utf-8">
        <style>
        :root { color-scheme: light dark; color: #1b1b1b; }
        @media (prefers-color-scheme: dark) { :root { color: #ffffff; } a { color: #99ebff; } }
        html, body { background: transparent; }
        body { margin: 0; font: 14px/1.5 "Segoe UI Variable Text", "Segoe UI", sans-serif; }
        a { color: #005fb8; text-decoration: none; }
        a:hover { text-decoration: underline; }
        h1, h2, h3 { font-weight: 600; margin: 1em 0 .4em; }
        h1 { font-size: 20px; } h2 { font-size: 17px; } h3 { font-size: 15px; }
        body > :first-child { margin-top: 0; }
        ul, ol { padding-left: 1.4em; }
        li { margin: .2em 0; }
        code { font: 12px Consolas, monospace; padding: .1em .3em; border-radius: 3px; background: rgba(128, 128, 128, .18); }
        img { max-width: 100%; }
        </style>
        </head>
        <body>{{notes}}</body>
        </html>
        """;

    private StackPanel Parked(FlockConfig config)
    {
        var list = new ListView { SelectionMode = ListViewSelectionMode.Single, MinHeight = 60 };
        foreach (var path in config.Parked)
        {
            list.Items.Add(Ui.Row(10, Ui.Icon(Ui.GlyphFolder, 14), Ui.Text(path, wrap: false)));
        }
        list.SelectedIndex = selectedParked is null ? -1 : config.Parked.IndexOf(selectedParked);

        var remove = Ui.IconButton(Ui.GlyphRemove, "Remove the selected project folder", async () =>
        {
            if (selectedParked is { } path
                && await Window.Confirm($"Remove {path} from project folders?", "Its folders stop being served as sites. Nothing is deleted from disk.", "Remove", destructive: true))
            {
                selectedParked = null;
                await PerformAsync(c => c.UnparkAsync(path));
            }
        });
        remove.IsEnabled = selectedParked is not null;
        list.SelectionChanged += (_, _) =>
        {
            selectedParked = list.SelectedIndex >= 0 && list.SelectedIndex < config.Parked.Count ? config.Parked[list.SelectedIndex] : null;
            remove.IsEnabled = selectedParked is not null;
        };
        var add = Ui.IconButton(Ui.GlyphAdd, "Add project folders…", async () =>
        {
            var paths = await Window.PickFolders("Add Project Folders");
            if (paths.Count > 0)
            {
                await PerformAsync(async c =>
                {
                    foreach (var path in paths)
                    {
                        await c.ParkAsync(path);
                    }
                });
            }
        });

        var body = Ui.Column(4, list, Ui.Row(0, add, remove));
        if (config.Parked.Count == 0)
        {
            body.Children.Insert(0, Ui.Caption("No project folders"));
        }
        return Ui.Section("Project folders",
            Ui.Card(body, new Thickness(8)),
            Footnote($"Every folder inside a project folder is served as <folder>.{config.Tld} — the same as `flock park`."));
    }

    private static TextBlock Value(string text) => Ui.Text(text, foreground: Ui.SecondaryText, wrap: false);

    public static TextBlock Footnote(string text)
    {
        var note = Ui.Caption(text);
        note.Margin = new Thickness(2, 6, 0, 0);
        return note;
    }
}
