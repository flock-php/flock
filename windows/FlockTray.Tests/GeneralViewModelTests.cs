using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;

namespace Flock.Tray.Tests;

public sealed class GeneralViewModelTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-general-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly ConfigurationService configuration;
    private readonly GeneralViewModel viewModel;

    public GeneralViewModelTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        configuration = new ConfigurationService(client, new OperationRunner());
        viewModel = new GeneralViewModel(configuration);
    }

    public void Dispose()
    {
        viewModel.Dispose();
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task ChannelCommandUsesConfigurationOwnershipAndRefreshesAfterTheWrite()
    {
        await viewModel.RefreshAsync();
        await viewModel.SetChannelCommand.ExecuteAsync("beta");

        Assert.Equal("beta", viewModel.Config!.Channel);
        Assert.Equal(["GET /v1/config", "POST /v1/settings", "GET /v1/config"], handler.Requests);
        Assert.Null(viewModel.Error);
    }

    [Fact]
    public async Task FailedWriteRemainsVisibleAfterItsFollowUpRead()
    {
        handler.FailWrites = true;

        await viewModel.SetChannelAsync("beta");

        Assert.Equal("settings failed", viewModel.Error);
        Assert.Equal(["POST /v1/settings", "GET /v1/config"], handler.Requests);
    }

    [Fact]
    public async Task BindingProjectionsFollowTheLatestConfiguration()
    {
        await viewModel.RefreshAsync();

        Assert.Equal("*.test", viewModel.DomainValue);
        Assert.Equal("Every folder inside a project folder is served as <folder>.test — the same as `flock park`.", viewModel.ProjectFoldersDescription);
        Assert.Equal("C:\\Flock", viewModel.Root);
        Assert.Equal(["C:\\Projects"], viewModel.Config!.Parked);
        Assert.Equal(Visibility.Visible, viewModel.PortsVisibility);
    }

    private sealed class Handler : HttpMessageHandler
    {
        public bool FailWrites { get; set; }
        public List<string> Requests { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            Requests.Add($"{request.Method} {path}");
            if (FailWrites && request.Method != HttpMethod.Get)
            {
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.ServiceUnavailable)
                {
                    Content = new StringContent("{\"error\":\"settings failed\"}"),
                });
            }
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent("{\"channel\":\"beta\",\"tld\":\"test\",\"root\":\"C:\\\\Flock\",\"parked\":[\"C:\\\\Projects\"],\"ports\":{\"http\":8080,\"https\":8443}}"),
            });
        }
    }
}
