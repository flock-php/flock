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
            page.Children.Add(Ui.Section("Network",
                Ui.Setting("Domain", Model.Status?.Dns.Addr.Length > 0
                        ? "Resolved by Flock's DNS server."
                        : "Each site is added to the Windows hosts file by the Flock hosts helper service.",
                    Value($"*.{config.Tld}")),
                Ui.Setting("HTTP port", null, Value(config.Ports.Http.ToString(System.Globalization.CultureInfo.InvariantCulture))),
                Ui.Setting("HTTPS port", null, Value(config.Ports.Https.ToString(System.Globalization.CultureInfo.InvariantCulture))),
                Footnote("Changing the domain or these ports needs `flock install` (it updates the hosts helper and certificates).")));

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

        var remove = Ui.IconButton(Ui.GlyphRemove, "Remove the selected folder", () =>
        {
            if (selectedParked is { } path)
            {
                selectedParked = null;
                _ = Perform(c => c.ForgetAsync(path));
            }
        });
        remove.IsEnabled = selectedParked is not null;
        list.SelectionChanged += (_, _) =>
        {
            selectedParked = list.SelectedIndex >= 0 && list.SelectedIndex < config.Parked.Count ? config.Parked[list.SelectedIndex] : null;
            remove.IsEnabled = selectedParked is not null;
        };
        var add = Ui.IconButton(Ui.GlyphAdd, "Park a folder…", async () =>
        {
            if (await Window.PickFolder() is { } path)
            {
                await Perform(c => c.ParkAsync(path));
            }
        });

        var body = Ui.Column(4, list, Ui.Row(0, add, remove));
        if (config.Parked.Count == 0)
        {
            body.Children.Insert(0, Ui.Caption("No parked folders"));
        }
        return Ui.Section("Parked folders",
            Ui.Card(body, new Thickness(8)),
            Footnote($"Every folder inside a parked folder is served as <folder>.{config.Tld}."));
    }

    private static TextBlock Value(string text) => Ui.Text(text, foreground: Ui.SecondaryText, wrap: false);

    public static TextBlock Footnote(string text)
    {
        var note = Ui.Caption(text);
        note.Margin = new Thickness(2, 6, 0, 0);
        return note;
    }
}
