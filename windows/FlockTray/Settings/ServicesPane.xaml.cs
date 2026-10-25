using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

internal sealed partial class ServicesPane : UserControl, ISettingsPane
{
    private readonly DeepLinkCoordinator deepLinks;
    private readonly ServicesPaneViewModel rows;
    private CancellationToken activeToken;
    private CancellationTokenSource? installCancellation;
    private Task? installConfirmation;
    private bool installConfirmationQueued;
    private Task? stoppingInstallConfirmation;

    public ServicesPane(SettingsWindow window, RuntimeService model, DeepLinkCoordinator deepLinks)
    {
        Window = window;
        Model = model;
        this.deepLinks = deepLinks;
        rows = new ServicesPaneViewModel(model.Services, model.Postgres, model.Catalog, model.ReleaseActions);
        InitializeComponent();
        DataContext = rows;
        model.Changed += ModelChanged;
        rows.SelectedServiceChanged += SelectedServiceChanged;
        rows.NotifySelectedServiceChanged();
    }

    private SettingsWindow Window { get; }
    private RuntimeService Model { get; }

    public string Title => "Services";
    public string Subtitle => "Databases, caches, search and the tools around your sites. Everything listens on 127.0.0.1 only.";
    public UIElement Root => this;
    public bool ScrollsItself => true;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        activeToken = hidden;
        while (!hidden.IsCancellationRequested)
        {
            await PollAsync(hidden);
        }
    }

    public async Task ReleaseAsync()
    {
        await StopInstallConfirmationAsync();
        Model.Changed -= ModelChanged;
        rows.SelectedServiceChanged -= SelectedServiceChanged;
        rows.Dispose();
        nap?.Cancel();
    }

    public async void Hidden()
    {
        nap?.Cancel();
        try
        {
            await StopInstallConfirmationAsync();
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    public void Select(string id)
    {
        if (id != rows.SelectedId)
        {
            DetailScroller.ChangeView(null, 0, null, true);
        }
        rows.SelectedId = id;
    }

    private void FilterChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        rows.ShowAll = sender.SelectedItem == Filter.Items[1];
    }

    private void ModelChanged()
    {
        if (Model.PendingInstall is not null)
        {
            ConfirmPendingLink();
        }
    }

    private void ServiceSelected(object sender, RoutedEventArgs args)
    {
        if ((sender as FrameworkElement)?.DataContext is ServiceViewModel service)
        {
            Select(service.Id);
        }
    }

    private void SelectedServiceChanged(ServiceViewModel? service)
    {
        Filter.SelectedItem = Filter.Items[rows.ShowAll ? 1 : 0];
        if (service is null)
        {
            DetailHost.Content = Ui.EmptyState(Ui.GlyphServer, "No services");
            return;
        }
        DetailHost.Content = Detail(service.Value);
        ConfirmPendingLink();
    }

    private Border Detail(Service service)
    {
        var panel = new StackPanel { Spacing = 14 };
        panel.Children.Add(Ui.Row(8, Ui.StatusDot(service.Running), Ui.Text(service.Name, "SubtitleTextBlockStyle")));
        var card = Ui.Card(panel, new Thickness(16));
        card.VerticalAlignment = VerticalAlignment.Top;
        if (!service.Available)
        {
            panel.Children.Add(Hero(Ui.GlyphServer, "Not available on Windows",
                service.Unavailable.Length > 0 ? service.Unavailable : $"Flock can't run {service.Name} on Windows."));
            return card;
        }
        if (service.Problem.Length > 0 && !(service.Id == "dns" && !service.Running))
        {
            panel.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), Ui.Caption(service.Problem, Ui.Caution)));
        }
        if (service.Installed)
        {
            var actions = Ui.Row(8);
            if (service.HasConfigurablePort)
            {
                actions.Children.Add(Ui.Button("Port…", () => _ = EditPort(service)));
            }
            if (service.Stoppable)
            {
                actions.Children.Add(Ui.Button(service.Running ? "Stop" : "Start", () => _ = ToggleServiceAsync(service)));
            }
            panel.Children.Add(actions);
        }
        if (service.Id == "web")
        {
            var tld = Model.Status?.Dns.Tld is { Length: > 0 } value ? value : "test";
            panel.Children.Add(Ui.Caption(service.Running
                ? $"Caddy serves every site over HTTPS (https://app.{tld}) with certificates from Flock's local certificate authority, which `flock install` trusts."
                : "Sites don't open while the web server is down. Its log may tell why; `flock restart` starts it again."));
        }
        if (service.Id == "dns")
        {
            var tld = Model.Status?.Dns.Tld is { Length: > 0 } value ? value : "test";
            panel.Children.Add(Ui.Caption(service.Running
                ? $"Windows sends every name under .{tld} here, so any subdomain of a site works without adding it."
                : "Wildcard subdomains don't resolve while the DNS server is down. Site domains still work through the hosts file."));
        }
        if (service.Id == "hosts")
        {
            panel.Children.Add(Ui.Caption(service.Installed
                ? "The Flock hosts helper writes every site's domain and added domains into the hosts file."
                : "The hosts helper is not installed, so site domains use Flock's DNS rule instead."));
        }
        if (service.Id == "postgres")
        {
            PostgresDetail(panel);
        }
        if (service.IsOnRequest)
        {
            CatalogDetail(panel, service);
        }
        if (service.Installed || !IsInstallable(service))
        {
            if (service.IsDatabase && TablePlus() is { } tablePlus)
            {
                panel.Children.Add(Ui.Spread(Ui.Text("TablePlus", "BodyStrongTextBlockStyle"), Ui.Button("Open", () => Model.OpenConnection(tablePlus, service.Url))));
            }
            if (service.Env.Length > 0)
            {
                panel.Children.Add(Ui.Column(6, Ui.Text("Environment variables", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Env)));
            }
            if (service.Log.Length > 0)
            {
                panel.Children.Add(Ui.Spread(Ui.Text("Logs", "BodyStrongTextBlockStyle"), Ui.Button("Open", () => Model.Open(FlockPaths.LogFile(service.Log)))));
            }
            if (service.Url.Length > 0)
            {
                panel.Children.Add(Ui.Column(6, Ui.Text("Connection", "BodyStrongTextBlockStyle"), Ui.CodeBlock(service.Url)));
            }
        }
        return card;
    }

    private async Task ToggleServiceAsync(Service service)
    {
        if (service.Running && !await Window.Confirm($"Stop {service.Name}?", "Apps that use it can't connect until you start it again.", "Stop", true))
        {
            return;
        }
        if (service.Id == "postgres")
        {
            if (service.Running) await Model.Postgres.StopAsync();
            else await Model.Postgres.StartAsync();
        }
        else
        {
            if (service.Running) await Model.Services.StopAsync(service.Id);
            else await Model.Services.StartAsync(service.Id);
        }
    }

    private static string? TablePlus()
    {
        foreach (var root in new[] { Environment.SpecialFolder.ProgramFiles, Environment.SpecialFolder.LocalApplicationData })
        {
            var path = Path.Combine(Environment.GetFolderPath(root), "TablePlus", "TablePlus.exe");
            if (File.Exists(path)) return path;
        }
        return null;
    }

    private void ConfirmPendingLink()
    {
        if (Model.PendingInstall is null || installConfirmationQueued)
        {
            return;
        }
        installConfirmationQueued = true;
        installCancellation?.Dispose();
        installCancellation = CancellationTokenSource.CreateLinkedTokenSource(activeToken);
        installConfirmation = ConfirmPendingLinkAsync(installCancellation.Token);
    }

    private async Task ConfirmPendingLinkAsync(CancellationToken token)
    {
        try
        {
            var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            if (!Window.DispatcherQueue.TryEnqueue(async () =>
            {
                try
                {
                    var request = await deepLinks.ConsumeInstallAsync(token);
                    if (request is null || token.IsCancellationRequested)
                    {
                        completion.TrySetResult();
                        return;
                    }
                    if (request.Target is not { } target)
                    {
                        if (!token.IsCancellationRequested)
                        {
                            await Ui.Inform(Window.XamlRoot, request.Title, request.Message);
                        }
                        completion.TrySetResult();
                        return;
                    }
                    if (token.IsCancellationRequested || !await Window.Confirm(request.Title, request.Message, "Install") || token.IsCancellationRequested)
                    {
                        completion.TrySetResult();
                        return;
                    }
                    await Model.ReleaseActions.InstallDeepLinkAsync(target.Service, target.Line);
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
        finally
        {
            installConfirmationQueued = false;
        }
    }

    private async Task StopInstallConfirmationAsync()
    {
        if (stoppingInstallConfirmation is { } stopping)
        {
            await stopping;
            return;
        }
        stoppingInstallConfirmation = StopInstallConfirmationCoreAsync();
        try
        {
            await stoppingInstallConfirmation;
        }
        finally
        {
            stoppingInstallConfirmation = null;
        }
    }

    private async Task StopInstallConfirmationCoreAsync()
    {
        var cancellation = installCancellation;
        var task = installConfirmation;
        if (cancellation is null || task is null)
        {
            return;
        }
        cancellation.Cancel();
        try
        {
            await task;
        }
        catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
        {
        }
        finally
        {
            installConfirmation = null;
            installCancellation = null;
            cancellation.Dispose();
        }
    }

    private async Task EditPort(Service service)
    {
        var box = new NumberBox
            { Header = "Port", Value = service.Port, Minimum = 1024, Maximum = 65535, SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact };
        var dialog = new ContentDialog
        {
            Title = $"{service.Name} port", Content = Ui.Column(12, box, Ui.Caption("Ports 1024–65535. Update the port in your projects' .env files too.")),
            PrimaryButtonText = "Save", CloseButtonText = "Cancel", DefaultButton = ContentDialogButton.Primary, XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary || double.IsNaN(box.Value))
        {
            return;
        }
        var port = (int)box.Value;
        if (port != service.Port && port is >= 1024 and <= 65535)
        {
            if (service.Id == "postgres") await Model.Postgres.SetPortAsync(port);
            else await Model.Services.SetPortAsync(service.Id, port);
        }
    }
}
