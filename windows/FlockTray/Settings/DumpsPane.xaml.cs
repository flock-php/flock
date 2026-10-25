using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed partial class DumpsPane : UserControl, ISettingsPane
{
    private readonly DumpsViewModel viewModel;
    private bool loaded;

    public DumpsPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new DumpsViewModel(model);
        DataContext = viewModel;
        foreach (var editor in EditorInfo.All)
        {
            Editor.Items.Add(editor.Name());
        }
        Editor.SelectedIndex = EditorInfo.All.ToList().IndexOf(EditorInfo.Parse(model.Preferences.Snapshot.Editor));
        loaded = true;
    }

    public string Title => "Dumps";
    public string Subtitle => "Send dump() and dd() output to Flock instead of the page or terminal — no changes to your projects.";
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden) => Task.CompletedTask;
    public void Hidden() { }
    public Task ReleaseAsync()
    {
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private async void CaptureToggled(object sender, RoutedEventArgs args)
    {
        if (sender is ToggleSwitch toggle && loaded && toggle.IsOn != viewModel.Capturing)
        {
            await viewModel.SetDumpsCommand.ExecuteAsync(toggle.IsOn);
        }
    }

    private void OpenDumps(object sender, RoutedEventArgs args) => App.Current.Open(WindowKind.Dumps);

    private async void EditorChanged(object sender, SelectionChangedEventArgs args)
    {
        if (!loaded || Editor.SelectedIndex < 0)
        {
            return;
        }
        try
        {
            await viewModel.SetEditorAsync(EditorInfo.All[Editor.SelectedIndex]);
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
        }
    }
}
