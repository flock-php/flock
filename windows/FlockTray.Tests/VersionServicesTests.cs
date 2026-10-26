using System.Net;
using Flock.Kit;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Php;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class VersionServicesTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-versions-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;

    public VersionServicesTests()
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
    public async Task PhpMutationUsesItsMinorLaneAndPublishesTheFollowUpRead()
    {
        await using var service = new PhpService(client, new OperationRunner());

        await service.RefreshAsync();
        Assert.Equal(OperationOutcome.Succeeded, await service.InstallAsync("8.4"));
        Assert.Equal(["GET /v1/php/versions", "POST /v1/php/versions/8.4/install", "GET /v1/php/versions"], handler.Requests);
        Assert.Equal("8.4.1", service.Current!.Versions[0].Installed);
    }

    [Fact]
    public async Task DuplicatePhpMutationIsRejectedBeforeTheSecondWrite()
    {
        await using var service = new PhpService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockWrites = true;

        var first = service.InstallAsync("8.4");
        await handler.WriteStarted.Task;
        var second = await service.InstallAsync("8.4");

        Assert.Equal(OperationOutcome.Rejected, second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/php/versions/8.4/install"));
        handler.ReleaseWrite.SetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
    }

    [Fact]
    public async Task DifferentPhpMinorsCanRunAtTheSameTime()
    {
        await using var service = new PhpService(client, new OperationRunner());
        await service.RefreshAsync();
        handler.BlockWrites = true;

        var first = service.InstallAsync("8.4");
        await handler.WriteStarted.Task;
        var second = service.InstallAsync("8.3");
        handler.ReleaseWrite.SetResult();

        Assert.Equal(OperationOutcome.Succeeded, await first);
        Assert.Equal(OperationOutcome.Succeeded, await second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/php/versions/8.4/install"));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/php/versions/8.3/install"));
    }

    [Fact]
    public async Task FrankenPhpOnlyTreatsMissingEndpointsAsUnsupported()
    {
        await using var service = new FrankenPhpService(client, new OperationRunner());
        handler.Status = HttpStatusCode.Unauthorized;
        await service.RefreshAsync();
        Assert.Equal("unauthorized", service.Error);

        handler.Status = HttpStatusCode.NotFound;
        await service.RefreshAsync();
        Assert.Null(service.Current);
        Assert.Null(service.Error);
    }

    [Fact]
    public async Task JobLogKeepsPhpTailAndDisablesCopyForMissingLogs()
    {
        var loaded = new JobLogViewModel(_ => Task.FromResult("one\ntwo\nthree\n"), () => false, tailLines: 2);
        await loaded.StartAsync();
        Assert.Equal(JobLogState.Loaded, loaded.State);
        Assert.Equal("two\nthree\n", loaded.Text);
        Assert.True(loaded.CanCopy);

        var missing = new JobLogViewModel(_ => Task.FromException<string>(new FlockServerException(404, "missing")), () => false);
        await missing.StartAsync();
        Assert.Equal(JobLogState.Missing, missing.State);
        Assert.Equal("No log yet.", missing.DisplayText);
        Assert.False(missing.CanCopy);
    }

    private sealed class Handler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];
        public bool BlockWrites { get; set; }
        public HttpStatusCode Status { get; set; } = HttpStatusCode.OK;
        public TaskCompletionSource WriteStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseWrite { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token)
        {
            var path = request.RequestUri!.AbsolutePath;
            Requests.Add($"{request.Method} {path}");
            if (path == "/v1/frankenphp" && Status != HttpStatusCode.OK)
            {
                return Response(Status, Status == HttpStatusCode.Unauthorized ? "unauthorized" : "missing");
            }
            if (request.Method != HttpMethod.Get && BlockWrites)
            {
                WriteStarted.TrySetResult();
                await ReleaseWrite.Task.WaitAsync(token);
            }
            if (path == "/v1/php/versions/8.4/install" || path == "/v1/php/versions/8.3/install")
            {
                return Response(HttpStatusCode.OK, "{}");
            }
            return path == "/v1/php/versions"
                ? Response(HttpStatusCode.OK, "{\"versions\":[{\"minor\":\"8.4\",\"latest\":\"8.4.1\",\"installed\":\"8.4.1\",\"default\":true,\"status\":\"installed\"}]}")
                : Response(HttpStatusCode.OK, "{\"available\":true,\"builds\":[]}");
        }

        private static HttpResponseMessage Response(HttpStatusCode status, string body) => new(status)
        {
            Content = new StringContent(status == HttpStatusCode.OK ? body : "{\"error\":\"" + body + "\"}")
        };
    }
}