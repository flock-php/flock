using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed partial class AiPane : UserControl, ISettingsPane
{
    private readonly AiViewModel viewModel;

    public AiPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new AiViewModel(model.Mcp);
        DataContext = viewModel;
    }

    public string Title => "AI";
    public string Subtitle => "Let AI agents like Claude Code and Cursor work with Flock: your sites' URLs and PHP versions, logs, dumps and mail, and installing versions and services.";
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden) => viewModel.RefreshAsync(hidden);
    public void Hidden() { }
    public Task ReleaseAsync()
    {
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private void CopySnippet(object sender, RoutedEventArgs args) => Ui.Copy(viewModel.Snippet);
}

internal sealed class BooleanVisibilityConverter : Microsoft.UI.Xaml.Data.IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language) => value is true ? Visibility.Visible : Visibility.Collapsed;
    public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
}
