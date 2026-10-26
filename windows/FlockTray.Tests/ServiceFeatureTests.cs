using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.Tests;

public sealed class ServiceFeatureTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-services-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;

    public ServiceFeatureTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
    }

    public void Dispose()
    {
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task PortFailureDoesNotRunAnotherMutationAndRefreshesTheService()
    {
        await using var service = new ServicesService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.FailPatch = true;

        var outcome = await service.SetPortAsync("postgres", 5433);

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal(1, handler.Requests.Count(request => request == "PATCH /v1/services/postgres"));
        Assert.Equal(2, handler.Requests.Count(request => request == "GET /v1/services"));
        Assert.Contains("port unavailable", service.Error);
    }

    [Fact]
    public async Task AReadStartedBeforeAServiceWriteCannotPublishAfterTheWrite()
    {
        await using var service = new ServicesService(client, new OperationRunner());
        handler.BlockFirstServiceRead = true;
        var stale = service.RefreshAsync();
        await handler.FirstServiceReadStarted.Task;

        var write = service.SetPortAsync("postgres", 5433);
        await write;
        handler.ReleaseFirstServiceRead.TrySetResult();
        await stale;

        Assert.Equal(5433, service.Current.Single().Port);
    }

    [Fact]
    public async Task FirstPostgresInstallStopsAfterARejectedPortPrerequisite()
    {
        await using var service = new PostgresService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.FailPatch = true;

        var outcome = await service.InstallFirstAsync("18", 5433, 5432);

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Contains("port unavailable", service.Error);
        Assert.DoesNotContain("POST /v1/postgres/versions/18/install", handler.Requests);
        Assert.Equal(2, handler.Requests.Count(request => request == "GET /v1/postgres/versions"));
    }

    [Fact]
    public async Task PostgresMutationLaneRejectsASecondWriteBeforeItsDelegateRuns()
    {
        await using var service = new PostgresService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockPostgres = true;

        var first = service.InstallAsync("18");
        await handler.PostgresWriteStarted.Task;
        var second = await service.UpdateAsync("18");

        Assert.Equal(OperationOutcome.Rejected, second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/postgres/versions/18/install"));
        handler.ReleasePostgresWrite.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
    }

    [Fact]
    public async Task CatalogLanesAllowDifferentServicesButConflictWithinOneService()
    {
        await using var service = new CatalogFeatureService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockCatalog = true;

        var mysql = service.InstallAsync("mysql", "8.4");
        await handler.MySqlWriteStarted.Task;
        var redis = service.InstallAsync("redis", "7");
        await handler.RedisWriteStarted.Task;
        var duplicate = await service.ActivateAsync("mysql", "9");

        Assert.Equal(OperationOutcome.Rejected, duplicate);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/mysql/install"));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/catalog/redis/install"));
        handler.ReleaseMySqlWrite.TrySetResult();
        handler.ReleaseRedisWrite.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await mysql);
        Assert.Equal(OperationOutcome.Succeeded, await redis);
    }

    [Fact]
    public async Task CatalogDraftSurvivesAJobSnapshotUpdate()
    {
        await using var service = new CatalogFeatureService(client, new OperationRunner());
        await service.RefreshAsync();
        var row = new CatalogServiceViewModel(service, service.Current!["mysql"]!);
        row.DraftLine = "9";
        row.DraftPort = 3307;

        row.Update(new Flock.Kit.CatalogService
        {
            Id = "mysql",
            Name = "MySQL",
            Port = 3306,
            Lines = [new CatalogLine { Id = "8.4", Label = "8.4", Installed = false }],
            Job = new CatalogJob { State = "running", Action = "install" },
        });

        Assert.Equal("9", row.DraftLine);
        Assert.Equal(3307, row.DraftPort);
    }

    private sealed class Handler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];
        public bool FailPatch { get; set; }
        public bool BlockFirstServiceRead { get; set; }
        public bool BlockPostgres { get; set; }
        public bool BlockCatalog { get; set; }
        public TaskCompletionSource PostgresWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleasePostgresWrite { get; } = NewSignal();
        public TaskCompletionSource MySqlWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseMySqlWrite { get; } = NewSignal();
        public TaskCompletionSource RedisWriteStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseRedisWrite { get; } = NewSignal();
        public TaskCompletionSource FirstServiceReadStarted { get; } = NewSignal();
        public TaskCompletionSource ReleaseFirstServiceRead { get; } = NewSignal();
        private bool firstServiceRead = true;

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token)
        {
            var path = request.RequestUri!.AbsolutePath;
            Requests.Add($"{request.Method} {path}");
            if (FailPatch && request.Method == HttpMethod.Patch)
            {
                PortChanged = true;
                return Response(HttpStatusCode.Conflict, "port unavailable");
            }
            if (request.Method == HttpMethod.Patch)
            {
                PortChanged = true;
            }
            if (BlockFirstServiceRead && path == "/v1/services" && firstServiceRead)
            {
                firstServiceRead = false;
                FirstServiceReadStarted.TrySetResult();
                await ReleaseFirstServiceRead.Task;
            }
            if (BlockPostgres && path.EndsWith("/install", StringComparison.Ordinal) && path.Contains("/postgres/", StringComparison.Ordinal))
            {
                PostgresWriteStarted.TrySetResult();
                await ReleasePostgresWrite.Task.WaitAsync(token);
            }
            if (BlockCatalog && path.EndsWith("/install", StringComparison.Ordinal) && path.Contains("/catalog/", StringComparison.Ordinal))
            {
                if (path.Contains("/mysql/", StringComparison.Ordinal))
                {
                    MySqlWriteStarted.TrySetResult();
                    await ReleaseMySqlWrite.Task.WaitAsync(token);
                }
                else if (path.Contains("/redis/", StringComparison.Ordinal))
                {
                    RedisWriteStarted.TrySetResult();
                    await ReleaseRedisWrite.Task.WaitAsync(token);
                }
            }
            return path switch
            {
                "/v1/services" => Response(HttpStatusCode.OK, $"[{{\"id\":\"postgres\",\"name\":\"PostgreSQL\",\"category\":\"Database\",\"installed\":false,\"port\":{(PortChanged ? 5433 : 5432)}}} ]"),
                "/v1/postgres/versions" => Response(HttpStatusCode.OK, "{\"port\":5432,\"majors\":[{\"major\":\"18\",\"latest\":\"18.1\"}]}"),
                "/v1/catalog" => Response(HttpStatusCode.OK, "{\"services\":[{\"id\":\"mysql\",\"name\":\"MySQL\",\"port\":3306,\"default_port\":3306,\"lines\":[{\"id\":\"8.4\",\"label\":\"8.4\"},{\"id\":\"9\",\"label\":\"9\"}]},{\"id\":\"redis\",\"name\":\"Redis\",\"port\":6379,\"lines\":[{\"id\":\"7\",\"label\":\"7\"}]}]}"),
                _ when path.StartsWith("/v1/services/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "[]"),
                _ when path.StartsWith("/v1/postgres/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "{}"),
                _ when path.StartsWith("/v1/catalog/", StringComparison.Ordinal) => Response(HttpStatusCode.OK, "{\"services\":[]}"),
                _ => Response(HttpStatusCode.NotFound, "missing"),
            };
        }

        private static TaskCompletionSource NewSignal() => new(TaskCreationOptions.RunContinuationsAsynchronously);
        private bool PortChanged { get; set; }

        private static HttpResponseMessage Response(HttpStatusCode status, string body) => new(status)
        {
            Content = new StringContent(status == HttpStatusCode.OK ? body : "{\"error\":\"" + body + "\"}"),
        };
    }
}