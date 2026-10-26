using Flock.Tray.Infrastructure;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Features.Dumps;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Features.Mail;
using Flock.Tray.Features.Node;
using Flock.Tray.Features.Php;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Features.Services;
using Flock.Tray.Features.Sites;
using Flock.Tray.Features.Update;
using Flock.Tray.ViewModels;
using Flock.Kit;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray;

/// <summary>
/// The only composition root. It owns the host, its application-lifetime signal, and the one
/// completion task used to stop and dispose the application.
/// </summary>
public sealed class AppHost : IAsyncDisposable
{
    private readonly IHost host;
    private readonly ApplicationRuntime runtime;
    private readonly FileLogSink logSink;
    private readonly Func<Task>? releaseUi;
    private readonly bool hasUi;
    private readonly TaskCompletionSource stopping = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly object lifecycleGate = new();
    private Task? startTask;
    private Task? supervisor;
    private Task? updateInstallation;
    private Task? quitTask;

    private AppHost(IHost host, FileLogSink logSink, Func<Task>? releaseUi, bool hasUi)
    {
        this.host = host;
        this.logSink = logSink;
        this.releaseUi = releaseUi;
        this.hasUi = hasUi;
        runtime = host.Services.GetRequiredService<ApplicationRuntime>();
        host.Services.GetRequiredService<IHostApplicationLifetime>().ApplicationStopping.Register(() => stopping.TrySetResult());
    }
    public RuntimeService RuntimeService => host.Services.GetRequiredService<RuntimeService>();

    public Flock.Kit.FlockClient Client => host.Services.GetRequiredService<Flock.Kit.FlockClient>();

    public ApplicationRuntime Runtime => runtime;

    public T? GetService<T>() where T : class => host.Services.GetService<T>();

    public static AppHost Create(DispatcherQueue? dispatcher = null, Func<Task>? releaseUi = null)
        => Create(dispatcher, releaseUi, new AppOptions());

    internal static AppHost Create(DispatcherQueue? dispatcher, Func<Task>? releaseUi, AppOptions options)
    {
        var builder = Host.CreateApplicationBuilder();
        builder.Logging.ClearProviders();
        builder.ConfigureContainer(new DefaultServiceProviderFactory(new ServiceProviderOptions
        {
            ValidateScopes = true,
            ValidateOnBuild = true,
        }), _ => { });
        builder.Services.Configure<HostOptions>(options => options.ShutdownTimeout = Timeout.InfiniteTimeSpan);
        var logSink = new FileLogSink();
        builder.Services.AddSingleton(logSink);
        builder.Services.AddKeyedSingleton("daemon", new HttpClient(new SocketsHttpHandler { UseProxy = false, PooledConnectionLifetime = TimeSpan.FromMinutes(2) })
        {
            Timeout = Timeout.InfiniteTimeSpan,
        });
        var updates = new HttpClient(new SocketsHttpHandler { PooledConnectionLifetime = TimeSpan.FromMinutes(2) })
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
        updates.DefaultRequestHeaders.UserAgent.ParseAdd($"Flock/{UpdateService.Current?.ToString() ?? "dev"}");
        builder.Services.AddKeyedSingleton("updates", updates);
        builder.Services.AddSingleton<Flock.Kit.FlockClient>(services =>
            new Flock.Kit.FlockClient(services.GetRequiredKeyedService<HttpClient>("daemon")));
        builder.Services.AddSingleton<Microsoft.Extensions.Logging.ILoggerProvider, FileLogProvider>();
        builder.Services.AddSingleton<ITrayNativeApi, Win32TrayNativeApi>();
        builder.Services.AddSingleton<IShellLauncher, ShellLauncher>();
        if (dispatcher is not null)
        {
            builder.Services.AddSingleton<CliProcessRunner>();
            builder.Services.AddSingleton<PreferencesService>(services => new PreferencesService(
                path: options.PreferencesPath, dispatcher: services.GetService<IUiDispatcher>(), log: error => logSink.TryWrite($"{DateTimeOffset.Now:O} preferences: {error}")));
            builder.Services.AddSingleton<InstallationService>();
            builder.Services.AddSingleton<OperationRunner>();
            builder.Services.AddSingleton<IUpdateInstallationMetadata, UpdateInstallationMetadata>();
            builder.Services.AddSingleton<IUpdateFileCleanup, UpdateFileCleanup>();
            builder.Services.AddSingleton<IAuthenticodeVerifier, WindowsAuthenticodeVerifier>();
            builder.Services.AddSingleton<IUpdateFileVerifier, UpdateFileVerifier>();
            builder.Services.AddSingleton<IUpdateProcessLauncher, UpdateProcessLauncher>();
            builder.Services.AddSingleton<MailService>();
            builder.Services.AddSingleton<DumpsService>();
            builder.Services.AddScoped<MailDetailViewModel>();
            builder.Services.AddScoped<MailViewModel>();
            builder.Services.AddScoped<DumpsViewModel>();
            builder.Services.AddScoped<PanelViewModel>();
            builder.Services.AddScoped<IMailPlatform, MailPlatform>();
            builder.Services.AddSingleton<IUiDispatcher>(new UiDispatcher(dispatcher));
            builder.Services.AddSingleton<ConfigurationService>();
            builder.Services.AddSingleton<PhpService>();
            builder.Services.AddSingleton<FrankenPhpService>();
            builder.Services.AddSingleton<NodeService>();
            builder.Services.AddSingleton<ServicesService>();
            builder.Services.AddSingleton<PostgresService>();
            builder.Services.AddSingleton<CatalogFeatureService>();
            builder.Services.AddSingleton<McpService>();
            builder.Services.AddSingleton<SitesService>();
            builder.Services.AddSingleton<DeepLinkCoordinator>();
            builder.Services.AddSingleton<IWindowFactory, WindowFactory>();
            builder.Services.AddScoped<LogsViewModel>();
            builder.Services.AddSingleton<WindowManager>();
            builder.Services.AddSingleton<ITrayIconFactory>(services => new TrayIconFactory(
                services.GetRequiredService<ITrayNativeApi>(), services.GetRequiredService<IUiDispatcher>(),
                services.GetRequiredService<Microsoft.Extensions.Logging.ILogger<TrayService>>(), options.Identity.WindowClass));
            builder.Services.AddSingleton<IPanelWindowFactory, PanelWindowFactory>();
            builder.Services.AddSingleton<StatusPollingService>(services => new StatusPollingService(
                services.GetRequiredService<Flock.Kit.FlockClient>(),
                _ => services.GetRequiredService<ApplicationRuntime>().RequestQuit(),
                services.GetService<IUiDispatcher>()));
            if (options.EnableBackgroundServices)
            {
                builder.Services.AddSingleton<IHostedService>(services => services.GetRequiredService<StatusPollingService>());
            }
            builder.Services.AddSingleton<UpdateService>(services =>
            {
                return new UpdateService(
                    services.GetRequiredKeyedService<HttpClient>("updates"),
                    services.GetRequiredService<TimeProvider>(),
                    services.GetRequiredService<IUpdateInstallationMetadata>(),
                    services.GetRequiredService<IUpdateProcessLauncher>(),
                    services.GetRequiredService<IUpdateFileVerifier>(),
                    services.GetRequiredService<IUpdateFileCleanup>(),
                    services.GetRequiredService<OperationRunner>(),
                    services.GetRequiredService<IShutdownRequest>(),
                    services.GetRequiredService<Microsoft.Extensions.Logging.ILogger<UpdateService>>(),
                    () => services.GetRequiredService<PreferencesService>().Snapshot.Channel,
                    services.GetRequiredService<IUiDispatcher>());
            });
            if (options.EnableBackgroundServices)
            {
                builder.Services.AddSingleton<IHostedService>(services => new UpdateScheduleService(
                    services.GetRequiredService<UpdateService>(), services.GetRequiredService<TimeProvider>()));
            }
            builder.Services.AddSingleton<RuntimeService>(services => new RuntimeService(
                services.GetRequiredService<Flock.Kit.FlockClient>(),
                services.GetRequiredService<UpdateService>(),
                services.GetRequiredService<InstallationService>(),
                services.GetRequiredService<CliProcessRunner>(),
                    services.GetRequiredService<StatusPollingService>(),
                     services.GetRequiredService<OperationRunner>(),
                     services.GetRequiredService<MailService>(),
                     services.GetRequiredService<PreferencesService>(),
                     services.GetRequiredService<ConfigurationService>(),
                     services.GetRequiredService<PhpService>(),
                     services.GetRequiredService<FrankenPhpService>(),
                     services.GetRequiredService<NodeService>(),
                     services.GetRequiredService<ServicesService>(),
                     services.GetRequiredService<PostgresService>(),
                      services.GetRequiredService<CatalogFeatureService>(),
                      services.GetRequiredService<McpService>(),
                     services.GetRequiredService<DumpsService>(),
                     services.GetService<IUiDispatcher>(),
                     services.GetRequiredService<SitesService>(),
                     shell: services.GetRequiredService<IShellLauncher>()));
        }
        builder.Services.AddSingleton<TimeProvider>(TimeProvider.System);
        builder.Services.AddSingleton<ApplicationRuntime>(services =>
            new ApplicationRuntime(() => services.GetRequiredService<IHostApplicationLifetime>().StopApplication()));
        builder.Services.AddSingleton<IShutdownRequest>(services => services.GetRequiredService<ApplicationRuntime>());
        return new AppHost(builder.Build(), logSink, releaseUi, dispatcher is not null);
    }

    public Task StartAsync()
    {
        lock (this)
        {
            if (host.Services.GetService<RuntimeService>() is not null)
            {
                _ = RuntimeService;
            }
            return startTask ??= StartCoreAsync();
        }
    }

    private async Task StartCoreAsync()
    {
        if (host.Services.GetService<PreferencesService>() is { } preferences)
        {
            await preferences.LoadAsync();
        }
        await host.StartAsync();
        if (hasUi)
        {
            supervisor ??= SuperviseAsync();
        }
    }

    /// <summary>Starts the update installation once and keeps the task owned by AppHost.</summary>
    public void StartUpdateInstallation()
    {
        if (host.Services.GetService<RuntimeService>() is { } runtimeService)
        {
            updateInstallation ??= runtimeService.UpdateInstallationAsync();
        }
    }

    private async Task SuperviseAsync()
    {
        await stopping.Task;
        await QuitAsync();
    }

    /// <summary>Signals shutdown immediately and returns the single owner completion task.</summary>
    public Task QuitAsync()
    {
        runtime.RequestQuit();
        lock (lifecycleGate)
        {
            return quitTask ??= runtime.QuitAsync(QuitCoreAsync);
        }
    }

    private async Task QuitCoreAsync()
    {
        Exception? failure = null;

        async Task Attempt(Func<Task> action)
        {
            try
            {
                await action();
            }
            catch (Exception error)
            {
                failure ??= error;
            }
        }

        if (startTask is not null)
        {
            await Attempt(() => startTask);
        }
        if (updateInstallation is not null)
        {
            await Attempt(() => updateInstallation);
        }
        if (host.Services.GetService<InstallationService>() is { } installation)
        {
            await Attempt(installation.DrainAsync);
        }
        await Attempt(() => host.StopAsync(CancellationToken.None));
        if (host.Services.GetService<OperationRunner>() is { } operations)
        {
            await Attempt(() => operations.DrainAsync());
        }
        if (releaseUi is not null)
        {
            await Attempt(releaseUi);
        }
        await Attempt(async () => await ((IAsyncDisposable)host).DisposeAsync());
        await Attempt(logSink.DrainAsync);
        await Attempt(async () => await logSink.DisposeAsync());
        if (failure is not null)
        {
            throw failure;
        }
    }

    public ValueTask DisposeAsync() => new(QuitAsync());
}
