using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class AiViewModelTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-ai-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly McpService service;
    private readonly AiViewModel viewModel;

    public AiViewModelTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        service = new McpService(client, new OperationRunner());
        viewModel = new AiViewModel(service);
    }

    public void Dispose()
    {
        viewModel.Dispose();
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task RefreshKeepsRowsAndPublishesSnippetAndDescriptions()
    {
        await viewModel.RefreshAsync();
        var row = Assert.Single(viewModel.Clients);

        handler.Registered = true;
        await service.RegisterAsync("cursor");

        Assert.Same(row, Assert.Single(viewModel.Clients));
        Assert.True(row.IsRegistered);
        Assert.Equal("snippet", viewModel.Snippet);
        Assert.Contains("Flock is added", row.Description);
    }

    [Fact]
    public async Task FailedMutationKeepsTheActionErrorAfterItsFollowUpRead()
    {
        await viewModel.RefreshAsync();
        handler.FailWrites = true;

        var outcome = await service.RegisterAsync("cursor");

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal("MCP write failed", viewModel.Error);
        Assert.Equal("Installed", Assert.Single(viewModel.Clients).Description);
    }

    [Fact]
    public async Task MissingClientHasNoMutationCommand()
    {
        handler.State = McpClientState.Missing;
        await viewModel.RefreshAsync();

        var row = Assert.Single(viewModel.Clients);
        Assert.False(row.IsAvailable);
        Assert.False(row.IsRegistered);
        Assert.False(row.RegisterCommand.CanExecute(null));
        Assert.False(row.UnregisterCommand.CanExecute(null));
    }

    [Fact]
    public async Task EmptyAndProblemStatesStayRepresented()
    {
        handler.Empty = true;
        await viewModel.RefreshAsync();
        Assert.Empty(viewModel.Clients);
        Assert.Equal("", viewModel.Snippet);

        handler.Empty = false;
        handler.Problem = "config could not be read";
        await viewModel.RefreshAsync();
        Assert.Contains(handler.Problem, Assert.Single(viewModel.Clients).Description);
    }

    private sealed class Handler : HttpMessageHandler
    {
        public bool Registered { get; set; }
        public bool FailWrites { get; set; }
        public string State { get; set; } = McpClientState.Available;
        public bool Empty { get; set; }
        public string Problem { get; set; } = "";

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var state = request.Method == HttpMethod.Get ? (Registered ? McpClientState.Registered : State) : McpClientState.Registered;
            if (FailWrites && request.Method != HttpMethod.Get)
            {
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.ServiceUnavailable)
                {
                    Content = new StringContent("{\"error\":\"MCP write failed\"}"),
                });
            }
            var clients = Empty ? "[]" : $"[{{\"id\":\"cursor\",\"name\":\"Cursor\",\"state\":\"{state}\",\"problem\":\"{Problem}\"}}]";
            var snippet = Empty ? "" : "snippet";
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent($"{{\"clients\":{clients},\"snippet\":\"{snippet}\"}}"),
            });
        }
    }
}