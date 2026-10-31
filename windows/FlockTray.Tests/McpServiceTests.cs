using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Tests;

public sealed class McpServiceTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-mcp-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly McpService service;

    public McpServiceTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        service = new McpService(client, new OperationRunner());
    }

    public void Dispose()
    {
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task RegisterUsesAClientLaneAndPublishesTheFollowUpGet()
    {
        await service.RefreshAsync();
        var outcome = await service.RegisterAsync("cursor");

        Assert.Equal(OperationOutcome.Succeeded, outcome);
        Assert.Equal(["GET /v1/mcp", "POST /v1/mcp/clients/cursor", "GET /v1/mcp"], handler.Requests);
        Assert.Equal(McpClientState.Registered, service.Current!.Clients[0].State);
    }

    [Fact]
    public async Task DuplicateClientMutationIsRejectedBeforeAnotherPost()
    {
        await service.RefreshAsync();
        handler.BlockWrites = true;
        var first = service.RegisterAsync("cursor");
        await handler.WriteStarted.Task;

        var second = await service.RegisterAsync("cursor");

        Assert.Equal(OperationOutcome.Rejected, second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/mcp/clients/cursor"));
        handler.ReleaseWrite.SetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
    }

    [Fact]
    public async Task APreWriteActivationResponseCannotOverwriteTheFollowUpRead()
    {
        handler.BlockFirstRead = true;
        var activation = service.RefreshAsync();
        await handler.ReadStarted.Task;
        handler.Registered = true;

        var mutation = service.RegisterAsync("cursor");
        handler.ReleaseRead.SetResult();

        await activation;
        Assert.Equal(OperationOutcome.Succeeded, await mutation);
        Assert.Equal(McpClientState.Registered, service.Current!.Clients[0].State);
    }

    [Fact]
    public async Task DifferentClientLanesCanRunAtTheSameTime()
    {
        await service.RefreshAsync();
        handler.BlockWrites = true;
        var first = service.RegisterAsync("cursor");
        await handler.WriteStarted.Task;
        var second = service.RegisterAsync("vscode");
        handler.ReleaseWrite.SetResult();

        Assert.Equal(OperationOutcome.Succeeded, await first);
        Assert.Equal(OperationOutcome.Succeeded, await second);
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/mcp/clients/cursor"));
        Assert.Equal(1, handler.Requests.Count(request => request == "POST /v1/mcp/clients/vscode"));
    }

    private sealed class Handler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];
        public bool Registered { get; set; }
        public bool BlockWrites { get; set; }
        public bool BlockFirstRead { get; set; }
        private bool firstReadBlocked;
        public TaskCompletionSource WriteStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseWrite { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReadStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseRead { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            Requests.Add($"{request.Method} {path}");
            if (BlockFirstRead && request.Method == HttpMethod.Get && !firstReadBlocked)
            {
                firstReadBlocked = true;
                ReadStarted.TrySetResult();
                return WaitForReadAsync(cancellationToken);
            }
            if (BlockWrites && request.Method != HttpMethod.Get)
            {
                WriteStarted.TrySetResult();
                return WaitForWriteAsync(cancellationToken);
            }
            var registered = Registered || Requests.Count > 1;
            var json = $"{{\"clients\":[{{\"id\":\"cursor\",\"name\":\"Cursor\",\"state\":\"{(registered ? "registered" : "available")}\"}}],\"snippet\":\"snippet\"}}";
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(json) });
        }

        private async Task<HttpResponseMessage> WaitForWriteAsync(CancellationToken cancellationToken)
        {
            await ReleaseWrite.Task.WaitAsync(cancellationToken);
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent("{\"clients\":[{\"id\":\"cursor\",\"name\":\"Cursor\",\"state\":\"registered\"}],\"snippet\":\"snippet\"}"),
            };
        }

        private async Task<HttpResponseMessage> WaitForReadAsync(CancellationToken cancellationToken)
        {
            await ReleaseRead.Task.WaitAsync(cancellationToken);
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent("{\"clients\":[{\"id\":\"cursor\",\"name\":\"Cursor\",\"state\":\"available\"}],\"snippet\":\"snippet\"}"),
            };
        }
    }
}