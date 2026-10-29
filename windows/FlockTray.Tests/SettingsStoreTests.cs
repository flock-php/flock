using System.Net;
using System.Text.Json;
using Microsoft.Extensions.Time.Testing;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Features.Node;
using Flock.Tray.Features.Php;
using Flock.Tray.Features.Services;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.Tests;

public sealed class FeatureServiceErrorTests : IAsyncLifetime
{
    private readonly string dir = Directory.CreateTempSubdirectory("flock-feature-errors-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly ConfigurationService configuration;
    private readonly ServicesService services;
    private readonly SitesService sites;
    private readonly PhpService php;
    private readonly FrankenPhpService frankenPhp;
    private readonly NodeService node;
    private readonly CatalogFeatureService catalog;
    private readonly PostgresService postgres;
    private readonly McpService mcp;
    private readonly IAsyncDisposable[] owners;

    public FeatureServiceErrorTests()
    {
        var path = Path.Combine(dir, "api.json");
        File.WriteAllText(path, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, path, new FakeTimeProvider());
        var runner = new OperationRunner();
        configuration = new ConfigurationService(client, runner);
        services = new ServicesService(client, runner);
        sites = new SitesService(client, runner);
        php = new PhpService(client, runner);
        frankenPhp = new FrankenPhpService(client, runner);
        node = new NodeService(client, runner, configuration);
        catalog = new CatalogFeatureService(client, runner);
        postgres = new PostgresService(client, runner);
        mcp = new McpService(client, runner);
        owners = [configuration, services, sites, php, frankenPhp, node, catalog, postgres, mcp];
    }

    public Task InitializeAsync() => Task.CompletedTask;

    public async Task DisposeAsync()
    {
        foreach (var owner in owners)
        {
            await owner.DisposeAsync();
        }
        client.Dispose();
        http.Dispose();
        Directory.Delete(dir, recursive: true);
    }

    public enum ResourceKind { Site, Config, Services, Php, FrankenPhp, Mcp, Node, Catalog, Postgres }

    public enum ReadKind { Config, Services, Php, FrankenPhp, Mcp, Node, Catalog, Postgres }

    private sealed class Handler : HttpMessageHandler
    {
        public bool FailReads { get; set; }
        public Dictionary<string, HttpStatusCode> Failures { get; } = [];
        public Dictionary<string, string> ErrorMessages { get; } = [];
        public string? BlockPath { get; set; }
        public TaskCompletionSource BlockStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseBlocked { get; } = NewSignal();
        public Func<HttpRequestMessage, Task>? BeforeRespond { get; set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (BeforeRespond is not null)
            {
                await BeforeRespond(request);
            }
            var path = request.RequestUri!.AbsolutePath;
            if (BlockPath == path)
            {
                BlockStarted.TrySetResult();
                await ReleaseBlocked.Task.WaitAsync(cancellationToken);
            }
            var status = Failures.GetValueOrDefault(path,
                FailReads && request.Method == HttpMethod.Get ? HttpStatusCode.ServiceUnavailable : HttpStatusCode.OK);
            return new HttpResponseMessage(status)
            {
                Content = new StringContent(status != HttpStatusCode.OK
                    ? JsonSerializer.Serialize(new { error = ErrorMessages.GetValueOrDefault(path, "refresh failed") })
                    : path == "/v1/services" || path.StartsWith("/v1/services/", StringComparison.Ordinal) ? "[]" : "{}"),
            };
        }

        public static TaskCompletionSource NewSignal() => new(TaskCreationOptions.RunContinuationsAsynchronously);
    }

    private Task<OperationOutcome> Perform(ResourceKind kind, bool fail)
    {
        var path = ActionPath(kind);
        if (fail)
        {
            handler.Failures[path] = HttpStatusCode.ServiceUnavailable;
            handler.ErrorMessages[path] = "action failed";
        }
        else
        {
            handler.Failures.Remove(path);
            handler.ErrorMessages.Remove(path);
        }
        return kind switch
        {
            ResourceKind.Site => sites.UpdateAsync("site", new SiteChange()),
            ResourceKind.Config => configuration.SetChannelAsync("stable"),
            ResourceKind.Services => services.SetPortAsync("postgres", 5433),
            ResourceKind.Php => php.InstallAsync("8.4"),
            ResourceKind.FrankenPhp => frankenPhp.InstallAsync("8.4"),
            ResourceKind.Mcp => mcp.RegisterAsync("cursor"),
            ResourceKind.Node => node.InstallAsync("20"),
            ResourceKind.Catalog => catalog.InstallAsync("mysql", "8.4"),
            ResourceKind.Postgres => postgres.InstallAsync("18"),
            _ => throw new ArgumentOutOfRangeException(nameof(kind)),
        };
    }

    private Task Refresh(ReadKind kind) => kind switch
    {
        ReadKind.Config => configuration.RefreshAsync(),
        ReadKind.Services => services.RefreshAsync(),
        ReadKind.Php => php.RefreshAsync(),
        ReadKind.FrankenPhp => frankenPhp.RefreshAsync(),
        ReadKind.Mcp => mcp.RefreshAsync(),
        ReadKind.Node => node.RefreshAsync(),
        ReadKind.Catalog => catalog.RefreshAsync(),
        ReadKind.Postgres => postgres.RefreshAsync(),
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private Task Refresh(ResourceKind kind) => kind == ResourceKind.Site ? Task.CompletedTask : Refresh((ReadKind)Enum.Parse(typeof(ReadKind), kind.ToString()));

    private string? Error(ResourceKind kind) => kind switch
    {
        ResourceKind.Site => sites.Error,
        ResourceKind.Config => configuration.Error,
        ResourceKind.Services => services.Error,
        ResourceKind.Php => php.Error,
        ResourceKind.FrankenPhp => frankenPhp.Error,
        ResourceKind.Mcp => mcp.Error,
        ResourceKind.Node => node.Error,
        ResourceKind.Catalog => catalog.Error,
        ResourceKind.Postgres => postgres.Error,
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private void DismissError(ResourceKind kind)
    {
        switch (kind)
        {
            case ResourceKind.Site: sites.DismissError(); break;
            case ResourceKind.Config: configuration.DismissError(); break;
            case ResourceKind.Services: services.DismissError(); break;
            case ResourceKind.Php: php.DismissError(); break;
            case ResourceKind.FrankenPhp: frankenPhp.DismissError(); break;
            case ResourceKind.Mcp: mcp.DismissError(); break;
            case ResourceKind.Node: node.DismissError(); break;
            case ResourceKind.Catalog: catalog.DismissError(); break;
            case ResourceKind.Postgres: postgres.DismissError(); break;
            default: throw new ArgumentOutOfRangeException(nameof(kind));
        }
    }

    private void Subscribe(ResourceKind kind, Action callback)
    {
        switch (kind)
        {
            case ResourceKind.Site: sites.Changed += callback; break;
            case ResourceKind.Config: configuration.Changed += callback; break;
            case ResourceKind.Services: services.Changed += callback; break;
            case ResourceKind.Php: php.Changed += callback; break;
            case ResourceKind.FrankenPhp: frankenPhp.Changed += callback; break;
            case ResourceKind.Mcp: mcp.Changed += callback; break;
            case ResourceKind.Node: node.Changed += callback; break;
            case ResourceKind.Catalog: catalog.Changed += callback; break;
            case ResourceKind.Postgres: postgres.Changed += callback; break;
            default: throw new ArgumentOutOfRangeException(nameof(kind));
        }
    }

    private bool IsBusy(ResourceKind kind) => kind switch
    {
        ResourceKind.Site => sites.IsRunning("site"),
        ResourceKind.Config => configuration.IsRunning,
        ResourceKind.Services => services.IsRunning("postgres"),
        ResourceKind.Php => php.IsRunning("8.4"),
        ResourceKind.FrankenPhp => frankenPhp.IsRunning("8.4"),
        ResourceKind.Mcp => mcp.IsRunning("cursor"),
        ResourceKind.Node => node.IsRunning("20"),
        ResourceKind.Catalog => catalog.IsRunning("mysql"),
        ResourceKind.Postgres => postgres.IsRunning,
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private bool HasSnapshot(ResourceKind kind) => kind switch
    {
        ResourceKind.Site => true,
        ResourceKind.Config => configuration.Current is not null,
        ResourceKind.Services => services.HasSnapshot,
        ResourceKind.Php => php.Current is not null,
        ResourceKind.FrankenPhp => frankenPhp.Current is not null,
        ResourceKind.Mcp => mcp.Current is not null,
        ResourceKind.Node => node.Current is not null,
        ResourceKind.Catalog => catalog.HasSnapshot,
        ResourceKind.Postgres => postgres.HasSnapshot,
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private static string PathFor(ReadKind kind) => kind switch
    {
        ReadKind.Config => "/v1/config",
        ReadKind.Services => "/v1/services",
        ReadKind.Php => "/v1/php/versions",
        ReadKind.FrankenPhp => "/v1/frankenphp",
        ReadKind.Mcp => "/v1/mcp",
        ReadKind.Node => "/v1/node/versions",
        ReadKind.Catalog => "/v1/catalog",
        ReadKind.Postgres => "/v1/postgres/versions",
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private static string ActionPath(ResourceKind kind) => kind switch
    {
        ResourceKind.Site => "/v1/sites/site",
        ResourceKind.Config => "/v1/settings",
        ResourceKind.Services => "/v1/services/postgres",
        ResourceKind.Php => "/v1/php/versions/8.4/install",
        ResourceKind.FrankenPhp => "/v1/frankenphp/8.4/install",
        ResourceKind.Mcp => "/v1/mcp/clients/cursor",
        ResourceKind.Node => "/v1/node/versions/20/install",
        ResourceKind.Catalog => "/v1/catalog/mysql/install",
        ResourceKind.Postgres => "/v1/postgres/versions/18/install",
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    [Theory]
    [InlineData(ResourceKind.Site)]
    [InlineData(ResourceKind.Config)]
    [InlineData(ResourceKind.Services)]
    [InlineData(ResourceKind.Php)]
    [InlineData(ResourceKind.FrankenPhp)]
    [InlineData(ResourceKind.Mcp)]
    [InlineData(ResourceKind.Node)]
    [InlineData(ResourceKind.Catalog)]
    [InlineData(ResourceKind.Postgres)]
    public async Task FailedActionsSurviveRefreshesUntilASuccessfulAction(ResourceKind kind)
    {
        await Perform(kind, fail: true);
        Assert.Equal("action failed", Error(kind));
        Assert.False(IsBusy(kind));

        await Refresh(kind);
        await Refresh(kind);
        Assert.Equal("action failed", Error(kind));
        Assert.True(HasSnapshot(kind));

        await Perform(kind, fail: false);
        Assert.Null(Error(kind));
    }

    [Fact]
    public async Task RefreshFailuresDoNotReplaceActionFailures()
    {
        handler.FailReads = true;
        await Perform(ResourceKind.Config, fail: true);
        Assert.Equal("action failed", Error(ResourceKind.Config));

        handler.FailReads = false;
        await Refresh(ResourceKind.Config);
        Assert.Equal("action failed", Error(ResourceKind.Config));

        DismissError(ResourceKind.Config);
        Assert.Null(Error(ResourceKind.Config));
    }

    [Fact]
    public async Task SuccessfulActionDoesNotClearARefreshFailure()
    {
        handler.FailReads = true;
        await Perform(ResourceKind.Config, fail: false);

        Assert.Equal("refresh failed", Error(ResourceKind.Config));
    }

    [Fact]
    public async Task SuccessfulRefreshClearsReadErrorsAndNotifiesEvenWhenDataIsUnchanged()
    {
        await Refresh(ResourceKind.Config);
        handler.Failures[PathFor(ReadKind.Config)] = HttpStatusCode.ServiceUnavailable;
        await Refresh(ResourceKind.Config);
        Assert.Equal("refresh failed", Error(ResourceKind.Config));

        var notifications = 0;
        Subscribe(ResourceKind.Config, () => notifications++);
        handler.Failures.Clear();
        await Refresh(ResourceKind.Config);
        Assert.Null(Error(ResourceKind.Config));
        Assert.Equal(1, notifications);
    }

    [Fact]
    public async Task UnchangedRefreshDoesNotNotifyForAPersistentActionError()
    {
        await Refresh(ResourceKind.Config);
        await Perform(ResourceKind.Config, fail: true);
        var notifications = 0;
        Subscribe(ResourceKind.Config, () => notifications++);

        await Refresh(ResourceKind.Config);
        Assert.Equal("action failed", Error(ResourceKind.Config));
        Assert.Equal(0, notifications);

        DismissError(ResourceKind.Config);
        Assert.Null(Error(ResourceKind.Config));
        Assert.Equal(1, notifications);
    }

    [Theory]
    [InlineData(ReadKind.Config)]
    [InlineData(ReadKind.Services)]
    [InlineData(ReadKind.Php)]
    [InlineData(ReadKind.FrankenPhp)]
    [InlineData(ReadKind.Mcp)]
    [InlineData(ReadKind.Node)]
    [InlineData(ReadKind.Catalog)]
    [InlineData(ReadKind.Postgres)]
    public async Task EachReadClearsOnlyItsOwnFailureEvenWhenDataIsUnchanged(ReadKind kind)
    {
        await Refresh(kind);
        handler.Failures[PathFor(kind)] = HttpStatusCode.ServiceUnavailable;
        await Refresh(kind);
        Assert.Equal("refresh failed", Error((ResourceKind)Enum.Parse(typeof(ResourceKind), kind.ToString())));

        var other = kind == ReadKind.Config ? ReadKind.Services : ReadKind.Config;
        await Refresh(other);
        var resource = (ResourceKind)Enum.Parse(typeof(ResourceKind), kind.ToString());
        Assert.Equal("refresh failed", Error(resource));

        var notifications = 0;
        Subscribe(resource, () => notifications++);
        handler.Failures.Clear();
        await Refresh(kind);
        Assert.Null(Error(resource));
        Assert.Equal(1, notifications);
    }

    [Fact]
    public async Task ConfigFailureDoesNotPreventServicesFromUpdating()
    {
        handler.Failures[PathFor(ReadKind.Config)] = HttpStatusCode.ServiceUnavailable;
        await Refresh(ResourceKind.Config);
        Assert.Equal("refresh failed", Error(ResourceKind.Config));

        var notifications = 0;
        Subscribe(ResourceKind.Services, () => notifications++);
        await Perform(ResourceKind.Services, fail: false);
        Assert.True(services.HasSnapshot);
        Assert.Empty(services.Current);
        Assert.True(notifications > 0);
        Assert.Equal("refresh failed", Error(ResourceKind.Config));
    }

    [Fact]
    public async Task ConcurrentReadFailuresRecoverIndependently()
    {
        handler.Failures[PathFor(ReadKind.Php)] = HttpStatusCode.ServiceUnavailable;
        handler.Failures[PathFor(ReadKind.Node)] = HttpStatusCode.ServiceUnavailable;
        await Task.WhenAll(Refresh(ReadKind.Php), Refresh(ReadKind.Node));
        Assert.Equal("refresh failed", php.Error);
        Assert.Equal("refresh failed", node.Error);

        handler.Failures.Clear();
        await Refresh(ReadKind.Php);
        Assert.Null(php.Error);
        Assert.Equal("refresh failed", node.Error);
        await Refresh(ReadKind.Node);
        Assert.Null(node.Error);
    }

    [Theory]
    [InlineData(HttpStatusCode.NotFound)]
    [InlineData(HttpStatusCode.NotImplemented)]
    public async Task UnsupportedFrankenPhpClearsOnlyItsOwnFailure(HttpStatusCode status)
    {
        await Refresh(ReadKind.FrankenPhp);
        Assert.NotNull(frankenPhp.Current);
        handler.Failures[PathFor(ReadKind.FrankenPhp)] = HttpStatusCode.ServiceUnavailable;
        await Refresh(ReadKind.FrankenPhp);
        Assert.Equal("refresh failed", frankenPhp.Error);

        handler.Failures[PathFor(ReadKind.FrankenPhp)] = status;
        await Refresh(ReadKind.FrankenPhp);
        Assert.Null(frankenPhp.Current);
        Assert.Null(frankenPhp.Error);

        handler.Failures[PathFor(ReadKind.Php)] = HttpStatusCode.ServiceUnavailable;
        await Refresh(ReadKind.Php);
        await Refresh(ReadKind.FrankenPhp);
        Assert.Equal("refresh failed", php.Error);
        Assert.Null(frankenPhp.Error);
    }

    [Fact]
    public async Task InFlightReadCannotClearANewerActionFailure()
    {
        var started = Handler.NewSignal();
        var finish = Handler.NewSignal();
        handler.BeforeRespond = async request =>
        {
            if (request.Method == HttpMethod.Get && request.RequestUri!.AbsolutePath == PathFor(ReadKind.Config))
            {
                started.TrySetResult();
                await finish.Task;
            }
        };
        var refresh = Refresh(ResourceKind.Config);
        await started.Task.WaitAsync(TimeSpan.FromSeconds(5));

        var action = Perform(ResourceKind.Config, fail: true);
        finish.SetResult();
        await Task.WhenAll(refresh, action);

        Assert.Equal("action failed", Error(ResourceKind.Config));
    }

    [Fact]
    public async Task DismissalClearsAllCurrentFailuresButNotFutureFailures()
    {
        handler.FailReads = true;
        await Task.WhenAll(Refresh(ReadKind.Php), Refresh(ReadKind.Node));
        DismissError(ResourceKind.Php);
        DismissError(ResourceKind.Node);
        Assert.Null(php.Error);
        Assert.Null(node.Error);

        await Refresh(ReadKind.Php);
        Assert.Equal("refresh failed", php.Error);
    }

    [Fact]
    public async Task ReadFailuresHaveStablePriorityAndRemainAfterAnActionSucceeds()
    {
        handler.Failures[PathFor(ReadKind.Config)] = HttpStatusCode.ServiceUnavailable;
        handler.ErrorMessages[PathFor(ReadKind.Config)] = "config read failed";
        await Refresh(ReadKind.Config);
        Assert.Equal("config read failed", configuration.Error);

        await Perform(ResourceKind.Config, fail: true);
        Assert.Equal("action failed", configuration.Error);
        await Perform(ResourceKind.Config, fail: false);
        Assert.Equal("config read failed", configuration.Error);

        handler.Failures.Clear();
        handler.ErrorMessages.Clear();
        await Refresh(ReadKind.Config);
        Assert.Null(configuration.Error);
    }

    [Fact]
    public async Task ServiceActionReturnsItsOwnOutcomeNotTheDisplayedError()
    {
        handler.Failures[PathFor(ReadKind.Services)] = HttpStatusCode.ServiceUnavailable;
        Assert.Equal(OperationOutcome.Succeeded, await Perform(ResourceKind.Services, fail: false));
        Assert.Equal("refresh failed", services.Error);

        handler.Failures.Remove(PathFor(ReadKind.Services));
        Assert.Equal(OperationOutcome.Failed, await Perform(ResourceKind.Services, fail: true));
        Assert.Equal("action failed", services.Error);
    }

    [Theory]
    [InlineData(ResourceKind.Site)]
    [InlineData(ResourceKind.Config)]
    [InlineData(ResourceKind.Services)]
    [InlineData(ResourceKind.Php)]
    [InlineData(ResourceKind.FrankenPhp)]
    [InlineData(ResourceKind.Mcp)]
    [InlineData(ResourceKind.Node)]
    [InlineData(ResourceKind.Catalog)]
    [InlineData(ResourceKind.Postgres)]
    public async Task ActionsKeepTheirBusyBehaviorWhilePending(ResourceKind kind)
    {
        handler.BlockPath = ActionPath(kind);
        var action = Perform(kind, fail: false);
        await handler.BlockStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        Assert.True(IsBusy(kind));
        Assert.False(action.IsCompleted);

        handler.ReleaseBlocked.SetResult();
        await action;
        Assert.False(IsBusy(kind));
    }

    [Fact]
    public async Task RepeatedUnchangedReadFailuresDoNotNotify()
    {
        handler.FailReads = true;
        await Refresh(ResourceKind.Config);
        var notifications = 0;
        Subscribe(ResourceKind.Config, () => notifications++);

        await Refresh(ResourceKind.Config);
        Assert.Equal("refresh failed", configuration.Error);
        Assert.Equal(0, notifications);
    }
}
