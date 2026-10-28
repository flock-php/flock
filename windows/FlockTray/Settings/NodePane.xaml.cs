using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

internal sealed partial class NodePane : UserControl, ISettingsPane
{
    private readonly RuntimeService model;
    private readonly NodePaneViewModel rows;
    private readonly SettingsWindow window;
    private CancellationToken activeToken;
    private bool active;
    private Task pendingConfirmation = Task.CompletedTask;

    public NodePane(SettingsWindow window, RuntimeService model)
    {
        this.window = window;
        this.model = model;
        rows = new NodePaneViewModel(model.Node);
        InitializeComponent();
        DataContext = rows;
        model.Changed += ModelChanged;
    }

    public string Title => "Node";
    public string Subtitle => "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.";
    public UIElement Root => this;
    public bool ScrollsItself => false;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        active = true;
        activeToken = hidden;
        while (!hidden.IsCancellationRequested)
        {
            await rows.RefreshAsync(hidden);
            ConfirmPendingLink(hidden);
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(30), model.TimeProvider, hidden);
            }
            catch (OperationCanceledException) when (hidden.IsCancellationRequested)
            {
                return;
            }
        }
    }

    public Task ReleaseAsync()
    {
        active = false;
        model.Changed -= ModelChanged;
        rows.Dispose();
        return Task.CompletedTask;
    }

    public void Hidden() => active = false;

    private void ModelChanged()
    {
        if (active)
        {
            ConfirmPendingLink(activeToken);
        }
    }

    private void CheckNode(object sender, RoutedEventArgs args)
    {
        _ = rows.CheckAsync();
    }

    private void OpenNodeActions(object sender, RoutedEventArgs args)
    {
        if (sender is not Button button || button.DataContext is not NodeVersionViewModel row)
        {
            return;
        }

        var menu = new MenuFlyout();
        if (row.CanUpdateNpm) menu.Items.Add(Item("Update npm", () => _ = row.UpdateNpmCommand.ExecuteAsync(null)));
        if (row.CanMakeDefault) menu.Items.Add(Item("Make default", () => _ = row.SetDefaultCommand.ExecuteAsync(null)));
        if (row.CanUninstall && row.HasUpdate) menu.Items.Add(Item("Uninstall…", () => _ = UninstallNodeAsync(row)));
        button.Flyout = menu;
        menu.ShowAt(button);
    }

    private void UninstallNode(object sender, RoutedEventArgs args)
    {
        if ((sender as FrameworkElement)?.DataContext is NodeVersionViewModel row)
        {
            _ = UninstallNodeAsync(row);
        }
    }

    private async Task UninstallNodeAsync(NodeVersionViewModel row)
    {
        if (!await window.Confirm($"Uninstall Node {row.Major}?",
                $"Its global npm packages are removed too. Projects and sites that use Node {row.Major} fall back to the default version until you install it again.",
                "Uninstall"))
        {
            return;
        }
        await row.UninstallCommand.ExecuteAsync(null);
    }

    private static MenuFlyoutItem Item(string text, Action action)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => action();
        return item;
    }

    private void ConfirmPendingLink(CancellationToken token)
    {
        if (token.IsCancellationRequested || !pendingConfirmation.IsCompleted)
        {
            return;
        }
        var installing = (model.Node.Current?.Versions ?? []).Where(release => model.Node.IsRunning(release.Major)).Select(release => release.Major).ToList();
        if (model.PendingInstall is not { Service: InstallLink.Node } link || link.ResolveNode(rows.Current, installing) is not { } request)
        {
            return;
        }
        if (!ReferenceEquals(model.PendingInstall, link))
        {
            return;
        }
        model.PendingInstall = null;
        pendingConfirmation = ConfirmPendingLinkAsync(request, token);
    }

    private async Task ConfirmPendingLinkAsync(InstallLinkResolution request, CancellationToken token)
    {
        try
        {
            var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            if (!window.DispatcherQueue.TryEnqueue(async () =>
            {
                try
                {
                    if (request.Target is not { } target)
                    {
                        if (!token.IsCancellationRequested)
                        {
                            await Ui.Inform(window.XamlRoot, request.Title, request.Message);
                        }
                        completion.TrySetResult();
                        return;
                    }
                    if (token.IsCancellationRequested || !await window.Confirm(request.Title, request.Message, "Install") || token.IsCancellationRequested)
                    {
                        completion.TrySetResult();
                        return;
                    }
                    App.Current.StartAcceptedInstall(cancellation => model.Node.InstallAsync(target.Line, cancellation));
                    completion.TrySetResult();
                }
                catch (OperationCanceledException) when (token.IsCancellationRequested)
                {
                    completion.TrySetCanceled(token);
                }
                catch (Exception error)
                {
                    App.Log(error);
                    completion.TrySetResult();
                }
            }))
            {
                return;
            }
            using var cancellation = token.Register(() => completion.TrySetCanceled(token));
            await completion.Task;
        }
        catch (OperationCanceledException) when (token.IsCancellationRequested)
        {
        }
    }
}
