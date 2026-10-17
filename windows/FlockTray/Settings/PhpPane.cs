using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>
/// PHP versions from the binaries manifest: install, update and uninstall them, with download
/// progress. A version is the official windows.php.net build plus Flock's extension pack.
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
            await Task.WhenAll(Store.RefreshPhpAsync(), Store.RefreshFrankenPhpAsync());
            var active = (Store.Php?.HasActiveJob ?? false) || (Store.FrankenPhp?.HasRunningJob ?? false);
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
        // Named after the server, like the FrankenPHP section below and a site's server picker.
        var header = Ui.Spread(Ui.Text("PHP-CGI", "BodyStrongTextBlockStyle"), Ui.Row(6, Ui.Caption(checkedText, wrap: false), check));
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
        if (Store.FrankenPhp is { Available: true } franken)
        {
            page.Children.Add(FrankenPhp(franken));
        }
        return page;
    }

    private Border Row(PhpRelease release)
    {
        var title = Ui.Row(8, Ui.Text($"PHP {release.Minor}", "BodyStrongTextBlockStyle", wrap: false), Ui.Caption(VersionText(release), wrap: false));
        if (release.IsDefault)
        {
            title.Children.Add(Ui.Badge("Default"));
        }
        if (release.Prerelease)
        {
            var badge = Ui.Badge("Pre-release", Ui.Tone.Caution);
            ToolTipService.SetToolTip(badge, $"PHP {release.Latest} isn't released yet — it may not work. Offered on the beta channel.");
            title.Children.Add(badge);
        }
        if (release.IsInstalled && release.Xdebug)
        {
            title.Children.Add(Ui.Badge("Xdebug", Ui.Tone.Critical));
        }

        var icon = Ui.Icon(Ui.GlyphCode, 20, release.IsInstalled ? Ui.Accent : Ui.TertiaryText);
        var info = Ui.Row(14, icon, Ui.Column(2, title, Detail(release)));
        return Ui.Card(Ui.Spread(info, Actions(release)));
    }

    // FrankenPHP

    /// <summary>The FrankenPHP builds, one per PHP minor; each downloads when a site first runs on it.</summary>
    private StackPanel FrankenPhp(FrankenPhpState state)
    {
        var section = new StackPanel { Spacing = 4, Margin = new Thickness(0, 0, 0, 24) };
        var header = Ui.Text("FrankenPHP", "BodyStrongTextBlockStyle");
        header.Margin = new Thickness(1, 0, 0, 6);
        section.Children.Add(header);
        foreach (var build in state.Builds)
        {
            section.Children.Add(FrankenPhpRow(build));
        }
        if (state.CheckError.Length > 0)
        {
            var error = Ui.Caption(state.CheckError, Ui.Caution);
            error.MaxLines = 2;
            ToolTipService.SetToolTip(error, state.CheckError);
            section.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), error));
        }
        return section;
    }

    private Border FrankenPhpRow(FrankenPhpBuild build)
    {
        var key = "frankenphp:" + build.Php;
        var minor = build.Php;
        var actions = Ui.Row(8);
        UIElement detail;

        if (build.Job is { IsRunning: true } job)
        {
            var text = job.Phase == "downloading" && job.HasProgress ? $"{job.PhaseText} {Math.Round(job.Progress * 100)}%" : job.PhaseText;
            detail = Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(text, wrap: false));
            actions.Children.Add(Ui.Button("Cancel", () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.CancelFrankenPhpJobAsync(minor)))));
        }
        else if (build.Job is { IsFailed: true } failed)
        {
            var error = Ui.Caption(failed.Error.Length > 0 ? failed.Error : "The download failed.", Ui.Critical);
            error.MaxLines = 2;
            error.MaxWidth = 380;
            ToolTipService.SetToolTip(error, failed.Error);
            detail = error;
            actions.Children.Add(Ui.Button("Retry", () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.InstallFrankenPhpAsync(minor))), glyph: Ui.GlyphRefresh));
            actions.Children.Add(Ui.Button("Dismiss", () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.DismissFrankenPhpJobAsync(minor)))));
        }
        else
        {
            if (build.Yanked.Length > 0)
            {
                var withdrawn = Ui.Caption(build.Update ? $"Withdrawn: {build.Yanked} — update to replace it" : $"Withdrawn: {build.Yanked}", Ui.Caution);
                withdrawn.MaxLines = 2;
                withdrawn.MaxWidth = 380;
                ToolTipService.SetToolTip(withdrawn, "This build was withdrawn. Flock replaces it as soon as a fixed build is published.");
                detail = Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn);
            }
            else
            {
                detail = Ui.Caption(build.IsInstalled ? $"FrankenPHP {build.Installed} (PHP {build.PhpVersion})" : "Not installed · downloads by itself when a site uses it", wrap: false);
            }
            // Installing ahead makes switching a site to FrankenPHP immediate (else PHP-CGI serves it meanwhile).
            if (!build.IsInstalled && build.Latest.Length > 0)
            {
                actions.Children.Add(Ui.Button("Install", () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.InstallFrankenPhpAsync(minor))),
                    glyph: Ui.GlyphDownload, tooltip: build.Size > 0 ? $"Downloads FrankenPHP for PHP {build.Latest} ({build.Size / 1_000_000} MB)" : $"Downloads FrankenPHP for PHP {build.Latest}"));
            }
            if (build.Update)
            {
                var label = build.Latest == build.PhpVersion ? "Update" : $"Update to PHP {build.Latest}";
                actions.Children.Add(Ui.Button(label, () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.InstallFrankenPhpAsync(minor))),
                    accent: true, glyph: Ui.GlyphDownload, tooltip: build.Size > 0 ? $"Downloads FrankenPHP for PHP {build.Latest} ({build.Size / 1_000_000} MB)" : null));
            }
            if (build.IsInstalled)
            {
                actions.Children.Add(Ui.Button("Remove", () => _ = Run(key, () => Store.PerformOnFrankenPhpAsync(c => c.UninstallFrankenPhpAsync(minor)))));
            }
        }

        if (busy.Contains(key))
        {
            Ui.SetEnabled(actions, false);
            if (build.Job is not { IsRunning: true }) // a running download shows its own ring
            {
                actions.Children.Insert(0, new ProgressRing { Width = 16, Height = 16 });
            }
        }
        var icon = Ui.Icon(Ui.GlyphServer, 20, build.IsInstalled ? Ui.Accent : Ui.TertiaryText);
        var title = Ui.Row(8, Ui.Text($"PHP {build.Php}", "BodyStrongTextBlockStyle", wrap: false));
        if (build.Prerelease)
        {
            var badge = Ui.Badge("Pre-release", Ui.Tone.Caution);
            ToolTipService.SetToolTip(badge, $"PHP {build.Latest} isn't released yet — it may not work. Offered on the beta channel.");
            title.Children.Add(badge);
        }
        var info = Ui.Row(14, icon, Ui.Column(2, title, detail));
        return Ui.Card(Ui.Spread(info, actions));
    }

    private static string VersionText(PhpRelease release) =>
        release.Update && release.Latest != release.Installed ? $"{release.Installed} → {release.Latest}" : release.IsInstalled ? release.Installed : release.Latest;

    /// <summary>The status line under the name: an install in progress, the error of a failed one, a withdrawn build, or the state.</summary>
    private static UIElement Detail(PhpRelease release)
    {
        if (release.Job is { IsRunning: true } job)
        {
            var text = job.Phase == "downloading" && job.HasProgress ? $"{job.PhaseText} {Math.Round(job.Progress * 100)}%" : job.PhaseText;
            return Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(text, wrap: false));
        }
        if (release.Job is { IsFailed: true } failed)
        {
            var error = Ui.Caption(failed.Error.Length > 0 ? failed.Error : "The download failed.", Ui.Critical);
            error.MaxLines = 2;
            error.MaxWidth = 380;
            ToolTipService.SetToolTip(error, failed.Error);
            return error;
        }
        if (release.Yanked.Length > 0)
        {
            var withdrawn = Ui.Caption(release.Update ? $"Withdrawn: {release.Yanked} — update to replace it" : $"Withdrawn: {release.Yanked}", Ui.Caution);
            withdrawn.MaxLines = 2;
            withdrawn.MaxWidth = 380;
            ToolTipService.SetToolTip(withdrawn, "This build was withdrawn. Flock replaces it as soon as a fixed build is published.");
            return Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn);
        }
        var (status, color) = release.Status switch
        {
            PhpStatus.Installed => ("Up to date", Ui.SecondaryText),
            PhpStatus.Update => (release.Latest == release.Installed ? "A new build is available" : "Update available", Ui.Accent),
            PhpStatus.Available => ("Not installed", Ui.SecondaryText),
            _ => ("", Ui.SecondaryText),
        };
        return Ui.Caption(status, color);
    }

    private StackPanel Actions(PhpRelease release)
    {
        var minor = release.Minor;
        var actions = Ui.Row(8);
        var size = release.Size > 0 ? $"Downloads PHP {release.Latest} ({release.Size / 1_000_000} MB)" : null;
        if (release.Job is { IsRunning: true })
        {
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(minor)));
            actions.Children.Add(Ui.Button("Cancel", () => _ = Run(minor, () => Store.PerformOnPhpAsync(c => c.CancelPhpInstallAsync(minor)))));
        }
        else if (release.Job is { IsFailed: true })
        {
            actions.Children.Add(Ui.Link("Show Log", () => _ = ShowLog(minor)));
            actions.Children.Add(Ui.Button("Retry", () => _ = Run(minor, () => Store.PerformOnPhpAsync(c => c.InstallPhpAsync(minor))), glyph: Ui.GlyphRefresh));
            actions.Children.Add(Ui.Button("Dismiss", () => _ = Run(minor, () => Store.PerformOnPhpAsync(c => c.DismissPhpJobAsync(minor)))));
        }
        else if (release.IsInstalled)
        {
            if (release.Update)
            {
                var label = release.Latest == release.Installed ? "Update" : $"Update to {release.Latest}";
                actions.Children.Add(Ui.Button(label, () => _ = Run(minor, () => Store.PerformOnPhpAsync(c => c.InstallPhpAsync(minor))),
                    accent: true, glyph: Ui.GlyphDownload, tooltip: size));
            }
            if (!release.IsDefault)
            {
                actions.Children.Add(Ui.Button("Make Default", () => _ = Run(minor, () => Perform(c => c.UpdateSettingsAsync(new SettingsChange(DefaultPhp: minor))))));
            }
            actions.Children.Add(More(release));
        }
        else
        {
            actions.Children.Add(Ui.Button("Install", () => _ = Run(minor, () => Store.PerformOnPhpAsync(c => c.InstallPhpAsync(minor))), glyph: Ui.GlyphDownload, tooltip: size));
        }

        if (busy.Contains(minor))
        {
            Ui.SetEnabled(actions, false);
            if (release.Job is not { IsRunning: true }) // a running download shows its own ring
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
        menu.Items.Add(Item("Show Last Install Log", () => _ = ShowLog(release.Minor)));
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
                var log = await Store.PhpInstallLogAsync(minor);
                var tail = string.Join("\n", log.Split('\n').TakeLast(400));
                var shown = tail.Length == 0 ? "No log yet." : tail;
                if (shown != text.Text)
                {
                    text.Text = shown;
                    dialog.IsPrimaryButtonEnabled = tail.Length > 0;
                    scroller.UpdateLayout();
                    scroller.ChangeView(null, scroller.ScrollableHeight, null, disableAnimation: true);
                }
                var active = Store.Php?.Versions.FirstOrDefault(v => v.Minor == minor)?.Job?.IsRunning ?? false;
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
