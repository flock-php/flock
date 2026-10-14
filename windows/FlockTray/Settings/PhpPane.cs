using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// PHP versions from php.net: install, update, download again and uninstall them, with progress.
/// On Windows a "build" downloads the official windows.php.net build (plus Xdebug) — the daemon's
/// API and the macOS app call it building because there it compiles.
/// </summary>
internal sealed class PhpPane(SettingsWindow window, AppModel model) : Pane(window, model)
{
    // Versions with a request in flight: their buttons are disabled until the daemon answers.
    private readonly HashSet<string> busy = [];
    private bool checking;

    public override string Title => "PHP";
    public override string Subtitle => "Every installed version runs its own pool of PHP workers. Sites use the default unless pinned to a version.";

    public override void Shown(CancellationToken hidden)
    {
        _ = Poll(hidden);
    }

    /// <summary>Polls quickly while something downloads, slowly otherwise.</summary>
    private async Task Poll(CancellationToken hidden)
    {
        while (!hidden.IsCancellationRequested)
        {
            await Store.RefreshPhpAsync();
            var active = Store.Php?.HasActiveJob ?? false;
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(active ? 2 : 30), hidden);
            }
            catch (TaskCanceledException)
            {
                return;
            }
        }
    }

    public override UIElement Build()
    {
        if (Store.Php is not { } catalog)
        {
            return Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Text("Loading PHP versions…", foreground: Ui.SecondaryText));
        }

        var page = new StackPanel();

        var checkedText = catalog.CheckedAt is { } at ? $"Checked {Ui.Relative(at)}" : "Not checked yet";
        UIElement check = checking
            ? new ProgressRing { Width = 14, Height = 14 }
            : Ui.Link("Check now", async () =>
            {
                checking = true;
                Rerender();
                await Store.PerformOnPhpAsync(c => c.CheckPhpReleasesAsync());
                checking = false;
                Rerender();
            });
        var header = Ui.Spread(Ui.Text("Versions", "BodyStrongTextBlockStyle"), Ui.Row(6, Ui.Caption(checkedText, wrap: false), check));
        header.Margin = new Thickness(1, 0, 0, 6);
        var versions = new StackPanel { Spacing = 4, Margin = new Thickness(0, 0, 0, 24) };
        versions.Children.Add(header);
        foreach (var release in catalog.Versions)
        {
            versions.Children.Add(Row(release));
        }
        if (catalog.CheckError.Length > 0)
        {
            versions.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption(catalog.CheckError, Ui.Caution)));
        }
        page.Children.Add(versions);

        var auto = new ToggleSwitch { IsOn = catalog.AutoBuild };
        auto.Toggled += async (_, _) =>
        {
            await Perform(c => c.UpdateSettingsAsync(new SettingsChange(PhpAutoBuild: auto.IsOn)));
            await Store.RefreshPhpAsync();
        };
        page.Children.Add(Ui.Section("Background downloads",
            Ui.Setting("Download updates and new PHP versions automatically",
                "Patch updates for installed versions and new PHP versions are downloaded from windows.php.net in the background (about 30 MB each), so installing them is instant. Pre-releases are not shown.",
                auto)));
        return page;
    }

    private Border Row(PhpRelease release)
    {
        var title = Ui.Row(8, Ui.Text($"PHP {release.Minor}", "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(VersionText(release), wrap: false));
        if (release.IsDefault)
        {
            title.Children.Add(Ui.Badge("Default"));
        }
        if (!release.Supported)
        {
            title.Children.Add(Ui.Badge("End of life", Ui.Tone.Caution));
        }
        if (release.IsInstalled && release.Xdebug)
        {
            title.Children.Add(Ui.Badge("Xdebug", Ui.Tone.Critical));
        }

        var icon = Ui.Icon(Ui.GlyphCode, 20, release.IsInstalled ? Ui.Accent : Ui.TertiaryText);
        var info = Ui.Row(14, icon, Ui.Column(2, title, Detail(release)));
        return Ui.Card(Ui.Spread(info, Actions(release)));
    }

    private static string VersionText(PhpRelease release) => release.Status switch
    {
        PhpStatus.Update => $"{release.Installed} → {release.Latest}",
        _ => release.IsInstalled ? release.Installed : release.Latest,
    };

    /// <summary>The status line under the name.</summary>
    private static UIElement Detail(PhpRelease release)
    {
        if (release.Job is { IsActive: true } job)
        {
            var step = job.State == "queued" ? "Waiting for another download…" : job.Step.Length == 0 ? "Downloading…" : job.Step;
            var text = Ui.Caption(step + (job.Auto ? " · in the background" : ""), wrap: false);
            text.MaxWidth = 380;
            return Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, text);
        }
        if (release.Status == PhpStatus.Failed || release.Job?.State == "failed")
        {
            var error = Ui.Caption(release.Job is { Error.Length: > 0 } j ? j.Error : "The last download failed.", Ui.Critical);
            error.MaxLines = 2;
            return error;
        }
        if (release.Status == PhpStatus.Unsupported && release.Note.Length > 0)
        {
            var note = Ui.Caption(release.Note);
            note.MaxLines = 2;
            ToolTipService.SetToolTip(note, release.Note);
            return note;
        }
        var (status, color) = release.Status switch
        {
            PhpStatus.Installed => ("Up to date", Ui.SecondaryText),
            PhpStatus.Update => (release.UpdateReady ? "Update downloaded — installs instantly" : "Update available", Ui.Accent),
            PhpStatus.Ready => ("Downloaded and ready to install", Ui.Success),
            PhpStatus.Likely => ("Not installed", Ui.SecondaryText),
            PhpStatus.Unsupported => ("No Windows build", Ui.SecondaryText),
            _ => ("", Ui.SecondaryText),
        };
        return Ui.Caption(status, color);
    }

    private StackPanel Actions(PhpRelease release)
    {
        var actions = Ui.Row(8);
        if (release.Job is { IsActive: true })
        {
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(release.Minor)));
            actions.Children.Add(Ui.Button("Cancel", () => _ = Run(release.Minor, () => Store.PerformOnPhpAsync(c => c.CancelPhpBuildAsync(release.Minor)))));
            return Busy(release, actions);
        }

        switch (release.Status)
        {
            case PhpStatus.Installed or PhpStatus.Update:
                if (release.Status == PhpStatus.Update)
                {
                    actions.Children.Add(Ui.Button($"Update to {release.Latest}", () => _ = Run(release.Minor, () => Store.PerformOnPhpAsync(c => c.InstallPhpAsync(release.Minor))),
                        accent: true, glyph: release.UpdateReady ? Ui.GlyphLightning : Ui.GlyphDownload,
                        tooltip: release.UpdateReady ? "Already downloaded — installs instantly" : $"Downloads PHP {release.Latest}, then installs it"));
                }
                if (!release.IsDefault)
                {
                    actions.Children.Add(Ui.Button("Make Default", () => _ = Run(release.Minor, () => Perform(c => c.UpdateSettingsAsync(new SettingsChange(DefaultPhp: release.Minor))))));
                }
                actions.Children.Add(More(release));
                break;

            case PhpStatus.Unsupported:
                var unavailable = new Button { Content = "Not available", IsEnabled = false };
                ToolTipService.SetToolTip(unavailable, release.Note.Length > 0 ? release.Note : $"There is no Windows build of PHP {release.Minor} yet. Flock checks again automatically.");
                actions.Children.Add(unavailable);
                break;

            default:
                if (release.Status == PhpStatus.Failed)
                {
                    actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(release.Minor)));
                }
                actions.Children.Add(Ui.Button(release.Status == PhpStatus.Failed ? "Retry" : "Install",
                    () => _ = Run(release.Minor, () => Store.PerformOnPhpAsync(c => c.InstallPhpAsync(release.Minor))),
                    glyph: release.Status == PhpStatus.Ready ? Ui.GlyphLightning : Ui.GlyphDownload,
                    tooltip: release.Status == PhpStatus.Ready ? "Already downloaded — installs instantly" : $"Downloads PHP {release.Latest} from windows.php.net, then installs it"));
                break;
        }
        return Busy(release, actions);
    }

    /// <summary>A row's actions, disabled with a progress ring while its request runs.</summary>
    private StackPanel Busy(PhpRelease release, StackPanel actions)
    {
        if (busy.Contains(release.Minor))
        {
            Ui.SetEnabled(actions, false);
            if (release.Job is not { IsActive: true }) // a running download shows its own ring
            {
                actions.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
        }
        return actions;
    }

    private async Task Run(string minor, Func<Task> request)
    {
        busy.Add(minor);
        Rerender();
        try
        {
            await request();
        }
        finally
        {
            busy.Remove(minor);
            Rerender();
        }
    }

    private Button More(PhpRelease release)
    {
        var menu = new MenuFlyout();
        var ini = Store.Config?.Php.FirstOrDefault(p => p.Version == release.Minor)?.Ini;
        if (ini is { Length: > 0 })
        {
            menu.Items.Add(Item("Open php.ini", () => Native.Open(ini)));
            menu.Items.Add(Item("Show php.ini in Explorer", () => Native.Reveal(ini)));
        }
        menu.Items.Add(Item("Download Again", () => _ = Run(release.Minor, () => Store.PerformOnPhpAsync(c => c.BuildPhpAsync(release.Minor)))));
        menu.Items.Add(Item("Show Last Log", () => _ = ShowLog(release.Minor)));
        menu.Items.Add(new MenuFlyoutSeparator());
        var uninstall = Item("Uninstall…", async () =>
        {
            if (await Window.Confirm($"Uninstall PHP {release.Minor}?",
                    $"Its php.ini is removed too. Sites pinned to PHP {release.Minor} stop working until you pick another version.", "Uninstall"))
            {
                await Run(release.Minor, () => Store.PerformOnPhpAsync(c => c.UninstallPhpAsync(release.Minor)));
            }
        });
        uninstall.IsEnabled = !release.IsDefault;
        menu.Items.Add(uninstall);

        var button = Ui.IconButton(Ui.GlyphMore, "More", () => { });
        button.Flyout = menu;
        return button;
    }

    private static MenuFlyoutItem Item(string text, Action click)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => click();
        return item;
    }

    /// <summary>The tail of a version's log, refreshed while the dialog is open and a job runs.</summary>
    private async Task ShowLog(string minor)
    {
        var text = new TextBlock { FontFamily = Ui.Mono, FontSize = 12, IsTextSelectionEnabled = true, Text = "Loading…" };
        var scroller = new ScrollViewer
        {
            Content = text,
            Width = 680,
            Height = 380,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        var dialog = new ContentDialog
        {
            Title = $"PHP {minor} log",
            Content = scroller,
            PrimaryButtonText = "Copy",
            CloseButtonText = "Done",
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 760.0;
        // Nothing to copy until the log has loaded ("Loading…", "No log yet." are placeholders).
        dialog.IsPrimaryButtonEnabled = false;
        dialog.PrimaryButtonClick += (_, e) =>
        {
            e.Cancel = true; // copying keeps the dialog open
            Ui.Copy(text.Text);
        };

        using var closed = new CancellationTokenSource();
        dialog.Closed += (_, _) => closed.Cancel();
        _ = Follow();
        await dialog.ShowAsync();

        async Task Follow()
        {
            while (!closed.IsCancellationRequested)
            {
                var log = await Store.PhpBuildLogAsync(minor);
                var tail = string.Join("\n", log.Split('\n').TakeLast(400));
                var shown = tail.Length == 0 ? "No log yet." : tail;
                if (shown != text.Text)
                {
                    text.Text = shown;
                    dialog.IsPrimaryButtonEnabled = tail.Length > 0;
                    scroller.UpdateLayout();
                    scroller.ChangeView(null, scroller.ScrollableHeight, null, disableAnimation: true);
                }
                var active = Store.Php?.Versions.FirstOrDefault(v => v.Minor == minor)?.Job?.IsActive ?? false;
                if (!active && tail.Length > 0)
                {
                    return;
                }
                try
                {
                    await Task.Delay(TimeSpan.FromSeconds(2), closed.Token);
                }
                catch (TaskCanceledException)
                {
                    return;
                }
            }
        }
    }
}
