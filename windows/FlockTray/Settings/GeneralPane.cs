using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed class GeneralPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    private string? selectedParked;

    public override string Title => "General";
    public override string Subtitle => "The Flock daemon runs your sites, PHP and services in the background.";

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

        if (Store.Config is { } config)
        {
            var network = new List<UIElement>
            {
                Ui.Setting("Domain", Model.Status?.Dns is { Wildcard: true }
                        ? "Every name, any subdomain included, is resolved by Flock's DNS server. Site domains are also in the hosts file."
                        : $"Only site domains and the domains added to sites resolve (the hosts file){(Model.Status?.Dns.WildcardProblem is { Length: > 0 } p ? $": {p}" : "")}.",
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
                Ui.Setting("Folder", null, Ui.Row(12, path, Ui.Button("Show in Explorer", () => Native.Reveal(config.Root))))));
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
                Store.ShowError($"Could not change the startup setting: {e.Message}");
            }
        };
        page.Children.Add(Ui.Section("Startup",
            Ui.Setting("Open Flock at sign-in", "Shows the Flock icon in the notification area when you sign in. The daemon itself always starts at sign-in.", startup)));

        if (Store.Config is { } channelConfig)
        {
            var experimental = channelConfig.Channel == "experimental";
            var beta = new ToggleSwitch { IsOn = channelConfig.Channel == "beta", IsEnabled = !experimental };
            beta.Toggled += async (_, _) => await Perform(c => c.UpdateSettingsAsync(new SettingsChange(Channel: beta.IsOn ? "beta" : "stable")));
            var updates = new List<UIElement>
            {
                Ui.Setting("Beta versions", "Get new PHP versions and services earlier, including pre-releases. They may not work yet.", beta),
            };
            if (experimental)
            {
                updates.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution),
                    Ui.Caption("Experimental channel: packages that aren't released yet are offered. `flock channel stable` switches back.", Ui.Caution)));
            }
            page.Children.Add(Ui.Section("Updates", [.. updates]));
        }

        if (Store.Config is { } cfg)
        {
            page.Children.Add(Parked(cfg));
        }
        return page;
    }

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
                await Perform(c => c.UnparkAsync(path));
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
                await Perform(async c =>
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
