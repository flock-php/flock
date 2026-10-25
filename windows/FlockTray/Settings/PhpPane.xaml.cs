using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using CommunityToolkit.Mvvm.ComponentModel;
using System.Collections.ObjectModel;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

internal sealed partial class PhpPane : UserControl, ISettingsPane
{
    private readonly SettingsWindow window;
    private readonly RuntimeService model;
    private readonly PhpPaneViewModel phpRows;
    private readonly FrankenPhpPaneViewModel frankenRows;

    public PhpPane(SettingsWindow window, RuntimeService model)
    {
        this.window = window;
        this.model = model;
        phpRows = new PhpPaneViewModel(model.Php);
        frankenRows = new FrankenPhpPaneViewModel(model.FrankenPhp);
        InitializeComponent();
        DataContext = new PhpBindingViewModel(phpRows, frankenRows);
    }

    public string Title => "PHP";
    public string Subtitle => "Every installed version runs its own pool of PHP workers. Sites use the default unless pinned to a version.";
    public UIElement Root => this;
    public bool ScrollsItself => false;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        while (!hidden.IsCancellationRequested)
        {
            await Task.WhenAll(phpRows.RefreshAsync(hidden), frankenRows.RefreshAsync(hidden));
            ConfirmPendingLink(hidden);
            var active = (phpRows.Current?.HasActiveJob ?? false) || (frankenRows.Current?.HasRunningJob ?? false);
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(active ? 2 : 30), model.TimeProvider, hidden);
            }
            catch (TaskCanceledException)
            {
                return;
            }
        }
    }

    public Task ReleaseAsync()
    {
        phpRows.Dispose();
        frankenRows.Dispose();
        return Task.CompletedTask;
    }

    public void Hidden() { }

    private void CheckPhp(object sender, RoutedEventArgs args) => _ = phpRows.CheckAsync();
    private void CheckFranken(object sender, RoutedEventArgs args) => _ = frankenRows.CheckAsync();

    private void ConfirmPendingLink(CancellationToken token)
    {
        if (model.PendingInstall is not { Service: InstallLink.Php } link || link.ResolvePhp(phpRows.Current) is not { } request)
        {
            return;
        }
        model.PendingInstall = null;
        window.DispatcherQueue.TryEnqueue(async () =>
        {
            if (request.Target is not { } target)
            {
                await Ui.Inform(window.XamlRoot, request.Title, request.Message);
                return;
            }
            if (await window.Confirm(request.Title, request.Message, "Install"))
            {
                await model.Php.InstallAsync(target.Line, token);
            }
        });
    }

    private void OpenPhpActions(object sender, RoutedEventArgs args)
    {
        if (sender is Button button && Row(button) is { } row)
        {
            var menu = new MenuFlyout();
            if (row.HasRunningJob)
            {
                menu.Items.Add(Item("Show log", () => ShowPhpLog(row)));
                menu.Items.Add(Item("Cancel", () => _ = row.CancelCommand.ExecuteAsync(null)));
            }
            else if (row.HasFailedJob)
            {
                menu.Items.Add(Item("Show log", () => ShowPhpLog(row)));
                menu.Items.Add(Item("Dismiss", () => _ = row.DismissCommand.ExecuteAsync(null)));
            }
            else if (row.HasJob)
            {
                menu.Items.Add(Item("Show log", () => ShowPhpLog(row)));
            }
            else
            {
                if (row.CanMakeDefault) menu.Items.Add(Item("Make default", () => MakePhpDefault(row)));
                if (row.CanUninstall && row.HasUpdate) menu.Items.Add(Item("Uninstall…", () => _ = UninstallPhpAsync(row)));
            }
            button.Flyout = menu;
            menu.ShowAt(button);
        }
    }

    private void MakePhpDefault(PhpVersionViewModel row) => _ = RunAsync(row.Minor, () => model.Configuration.SetDefaultPhpAsync(row.Minor));

    private void UninstallPhp(object sender, RoutedEventArgs args)
    {
        if (Row(sender) is { } row)
        {
            _ = UninstallPhpAsync(row);
        }
    }

    private async Task UninstallPhpAsync(PhpVersionViewModel row)
    {
        if (!await window.Confirm($"Uninstall PHP {row.Minor}?",
            $"Its php.ini is removed too. Sites pinned to PHP {row.Minor} stop working until you pick another version.", "Uninstall"))
        {
            return;
        }
        await RunAsync(row.Minor, () => model.Php.UninstallAsync(row.Minor));
    }

    private void ShowPhpLog(PhpVersionViewModel row)
    {
        _ = JobLogDialog.ShowAsync(window, $"PHP {row.Minor} log", token => model.Php.ReadLogAsync(row.Minor, token),
            () => model.Php.Current?.Versions.FirstOrDefault(v => v.Minor == row.Minor)?.Job?.IsRunning == true, 400);
    }

    private void OpenFrankenPhpActions(object sender, RoutedEventArgs args)
    {
        if (sender is Button button && button.DataContext is FrankenPhpVersionViewModel row)
        {
            var menu = new MenuFlyout();
            if (row.HasRunningJob)
            {
                menu.Items.Add(Item("Show log", () => ShowFrankenLog(row)));
                menu.Items.Add(Item("Cancel", () => _ = row.CancelCommand.ExecuteAsync(null)));
            }
            else if (row.HasFailedJob)
            {
                menu.Items.Add(Item("Show log", () => ShowFrankenLog(row)));
                menu.Items.Add(Item("Dismiss", () => _ = row.DismissCommand.ExecuteAsync(null)));
            }
            else if (row.HasJob)
            {
                menu.Items.Add(Item("Show log", () => ShowFrankenLog(row)));
            }
            else
            {
                if (row.CanUninstall && row.HasUpdate) menu.Items.Add(Item("Uninstall", () => _ = row.UninstallCommand.ExecuteAsync(null)));
            }
            button.Flyout = menu;
            menu.ShowAt(button);
        }
    }

    private void ShowFrankenLog(FrankenPhpVersionViewModel row) => _ = JobLogDialog.ShowAsync(window, $"FrankenPHP {row.Php} log", token => model.FrankenPhp.ReadLogAsync(row.Php, token),
        () => model.FrankenPhp.Current?.Builds.FirstOrDefault(v => v.Php == row.Php)?.Job?.IsRunning == true);

    private static MenuFlyoutItem Item(string text, Action action)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => action();
        return item;
    }

    private PhpVersionViewModel? Row(object sender) => (sender as FrameworkElement)?.DataContext as PhpVersionViewModel;

    private async Task RunAsync(string key, Func<Task> action)
    {
        try
        {
            await action();
        }
        catch (OperationCanceledException)
        {
        }
    }

    private sealed class PhpBindingViewModel : ObservableObject
    {
        private readonly PhpPaneViewModel php;
        private readonly FrankenPhpPaneViewModel franken;

        public PhpBindingViewModel(PhpPaneViewModel php, FrankenPhpPaneViewModel franken)
        {
            this.php = php;
            this.franken = franken;
            php.PropertyChanged += Changed;
            franken.PropertyChanged += Changed;
        }

        public ObservableCollection<PhpVersionViewModel> Versions => php.Versions;
        public ObservableCollection<FrankenPhpVersionViewModel> Builds => franken.Builds;
        public PhpCatalog? Current => php.Current;
        public FrankenPhpState? FrankenCurrent => franken.Current;
        public bool IsChecking => php.IsChecking;
        public bool IsFrankenChecking => franken.IsChecking;
        public Visibility FrankenPhpVisibility => franken.Current?.Available == true ? Visibility.Visible : Visibility.Collapsed;

        private void Changed(object? sender, System.ComponentModel.PropertyChangedEventArgs args)
        {
            if (ReferenceEquals(sender, franken))
            {
                OnPropertyChanged(nameof(Builds));
                OnPropertyChanged(nameof(FrankenCurrent));
                OnPropertyChanged(nameof(IsFrankenChecking));
                OnPropertyChanged(nameof(FrankenPhpVisibility));
                return;
            }

            OnPropertyChanged(args.PropertyName);
        }
    }
}
