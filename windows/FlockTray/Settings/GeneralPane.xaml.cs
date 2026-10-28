using System.ComponentModel;
using System.Security;
using Flock.Kit;
using Flock.Tray.Features.Update;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

internal sealed partial class GeneralPane : UserControl, ISettingsPane
{
    private readonly RuntimeService model;
    private readonly GeneralViewModel viewModel;
    private readonly SettingsWindow window;
    private readonly bool loaded;
    private string? selectedParked;

    public GeneralPane(SettingsWindow window, RuntimeService model)
    {
        this.window = window;
        this.model = model;
        InitializeComponent();
        viewModel = new GeneralViewModel(model);
        DataContext = viewModel;
        Startup.IsOn = Native.StartsAtSignIn;
        model.UpdateService.Changed += UpdateChanged;
        viewModel.PropertyChanged += ViewModelChanged;
        loaded = true;
        UpdateChanged();
        UpdateParked();
    }

    public string Title => "General";
    public string Subtitle => "The Flock daemon runs your sites, PHP and services in the background.";
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden)
    {
        Startup.IsOn = Native.StartsAtSignIn;
        return viewModel.RefreshAsync(hidden);
    }
    public void Hidden() { }

    public Task ReleaseAsync()
    {
        model.UpdateService.Changed -= UpdateChanged;
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (args.PropertyName is nameof(GeneralViewModel.Config))
        {
            UpdateParked();
        }
        RestartButton.IsEnabled = viewModel.RestartCommand.CanExecute(null);
    }

    private void UpdateParked()
    {
        var paths = viewModel.Config?.Parked ?? [];
        NoParked.Visibility = paths.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        if (selectedParked is not null)
        {
            Parked.SelectedIndex = paths.IndexOf(selectedParked);
            if (Parked.SelectedIndex < 0)
            {
                selectedParked = null;
            }
        }
        RemoveParked.IsEnabled = selectedParked is not null;
    }

    private void UpdateChanged()
    {
        UpdateHost.Children.Clear();
        var updater = model.UpdateService;
        if (!updater.IsEnabled || UpdateService.Current is not { } current)
        {
            UpdateHost.Children.Add(Ui.Setting("Flock", "Updates need a version such as 1.0.0. Set InformationalVersion when building to test updates.", null));
            return;
        }
        var idle = !updater.IsBusy;
        var description = updater.Summary.Length > 0 ? $"Version {current} · {updater.Summary}" : $"Version {current}";
        var button = updater.Available is { } update
            ? Ui.Button($"Update to {update.Version}", () => _ = updater.InstallAsync(), true)
            : Ui.Button("Check for Updates", () => _ = updater.CheckAsync(userInitiated: true));
        button.IsEnabled = idle;
        UIElement control = idle ? button : Ui.Row(12, new ProgressRing { Width = 16, Height = 16 }, button);
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
                    model.Open(url);
                }
            }), button);
        }
        UpdateHost.Children.Add(Ui.Setting("Flock", description, control));
    }

    private async Task ReleaseNotesAsync(UpdateFeed.Item update)
    {
        var notes = new HtmlView(false, false, false, model.Shell) { Width = 500, Height = 380 };
        var load = notes.LoadAsync(ReleaseNotesPage(update.Notes));
        var dialog = new ContentDialog
        {
            Title = $"Flock {update.Version}",
            Content = notes,
            PrimaryButtonText = $"Update to {update.Version}",
            SecondaryButtonText = update.NotesUrl is null ? "" : "View on GitHub",
            CloseButtonText = "Close",
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = XamlRoot
        };
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
        if (result == ContentDialogResult.Primary)
        {
            await model.UpdateService.InstallAsync();
        }
        else if (result == ContentDialogResult.Secondary)
        {
            model.Open(update.NotesUrl!);
        }
    }

    private static string ReleaseNotesPage(string notes)
    {
        return $$"""
                 <!doctype html><html><head><meta charset="utf-8"><style>
                 :root { color-scheme: light dark; color: #1b1b1b; } @media (prefers-color-scheme: dark) { :root { color: #ffffff; } a { color: #99ebff; } }
                 html, body { background: transparent; } body { margin: 0; font: 14px/1.5 "Segoe UI Variable Text", "Segoe UI", sans-serif; }
                 a { color: #005fb8; text-decoration: none; } a:hover { text-decoration: underline; } h1, h2, h3 { font-weight: 600; margin: 1em 0 .4em; }
                 h1 { font-size: 20px; } h2 { font-size: 17px; } h3 { font-size: 15px; } body > :first-child { margin-top: 0; }
                 ul, ol { padding-left: 1.4em; } li { margin: .2em 0; } code { font: 12px Consolas, monospace; padding: .1em .3em; border-radius: 3px; background: rgba(128, 128, 128, .18); } img { max-width: 100%; }
                 </style></head><body>{{notes}}</body></html>
                 """;
    }

    private void RestartClicked(object sender, RoutedEventArgs args)
    {
        _ = viewModel.RestartCommand.ExecuteAsync(null);
    }

    private void StartupToggled(object sender, RoutedEventArgs args)
    {
        if (!loaded || sender is not ToggleSwitch toggle || toggle.IsOn == Native.StartsAtSignIn)
        {
            return;
        }
        try
        {
            Native.StartsAtSignIn = toggle.IsOn;
        }
        catch (Exception e) when (e is UnauthorizedAccessException or SecurityException or IOException)
        {
            model.ShowRuntimeError($"Could not change the startup setting: {e.Message}");
        }
    }

    private async void BetaToggled(object sender, RoutedEventArgs args)
    {
        if (loaded && Beta.IsOn != viewModel.IsBeta)
        {
            await viewModel.SetChannelCommand.ExecuteAsync(Beta.IsOn ? "beta" : "stable");
            await model.UpdateService.CheckAsync(userInitiated: false);
        }
    }

    private void ShowRoot(object sender, RoutedEventArgs args)
    {
        model.Reveal(viewModel.Config?.Root ?? "");
    }

    private void ParkedSelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        selectedParked = Parked.SelectedItem as string;
        RemoveParked.IsEnabled = selectedParked is not null;
    }

    private async void AddParkedClicked(object sender, RoutedEventArgs args)
    {
        var paths = await window.PickFolders("Add Project Folders");
        if (paths.Count > 0)
        {
            await PerformAsync(async client =>
            {
                foreach (var path in paths)
                {
                    await client.ParkAsync(path);
                }
            });
        }
    }

    private async void RemoveParkedClicked(object sender, RoutedEventArgs args)
    {
        if (selectedParked is { } path && await window.Confirm($"Remove {path} from project folders?",
                "Its folders stop being served as sites. Nothing is deleted from disk.", "Remove", true))
        {
            selectedParked = null;
            await PerformAsync(client => client.UnparkAsync(path));
        }
    }

    private async Task PerformAsync(Func<FlockClient, Task> change)
    {
        await change(model.Client);
        await model.Configuration.RefreshAsync();
    }

    public static TextBlock Footnote(string text)
    {
        var note = Ui.Caption(text);
        note.Margin = new Thickness(2, 6, 0, 0);
        return note;
    }
}
