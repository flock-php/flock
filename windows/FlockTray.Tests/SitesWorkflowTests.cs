using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class SitesWorkflowTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-sites-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly SitesService service;

    public SitesWorkflowTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        service = new SitesService(client, new OperationRunner());
        service.OnChange = () => client.StatusAsync().ContinueWith(_ => { }, TaskScheduler.Default);
    }
    public void Dispose()
    {
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task FailedWriteStillRunsAuthoritativeStatusRefreshAndKeepsActionError()
    {
        handler.FailUpdate = true;

        var outcome = await service.UpdateAsync("site", new SiteChange(Secured: true));

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal("write failed", service.ErrorFor("site"));
        Assert.Equal(1, handler.StatusReads);
    }

    [Fact]
    public async Task ANewExtensionSelectionRetiresTheOlderReport()
    {
        var old = service.SiteExtensionsAsync("old");
        await handler.OldExtensionStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        var current = service.SiteExtensionsAsync("current");

        var fresh = await current;
        handler.ReleaseOldExtension.TrySetResult();
        var retired = await old;

        Assert.Equal("current", fresh.Report?.Php);
        Assert.Null(retired.Report);
        Assert.Null(retired.Error);
    }

    [Theory]
    [InlineData("start", false)]
    [InlineData("rebuild", false)]
    [InlineData("stop", false)]
    [InlineData("start", true)]
    [InlineData("rebuild", true)]
    [InlineData("stop", true)]
    public async Task ShareActionsRefreshStatusAndKeepSiteScopedFailures(string action, bool fail)
    {
        handler.FailShare = fail;

        var outcome = await ShareAsync(action);

        Assert.Equal(fail ? OperationOutcome.Failed : OperationOutcome.Succeeded, outcome);
        Assert.Equal(fail ? "share failed" : null, service.ErrorFor("site"));
        Assert.Null(service.ErrorFor("other"));
        Assert.Equal(1, handler.ShareWrites);
        Assert.Equal(1, handler.StatusReads);
    }

    [Fact]
    public async Task ShareAndSiteChangesUseTheSameMutationGate()
    {
        handler.HoldShare = true;
        var started = service.StartShareAsync("site", [], false);
        await handler.ShareStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        try
        {
            Assert.True(service.IsRunning("site"));
            Assert.Equal(OperationOutcome.Rejected, await service.StopShareAsync("site"));
            Assert.Equal(OperationOutcome.Rejected, await service.UpdateAsync("site", new SiteChange(Root: "public")));
            Assert.Equal(1, handler.ShareWrites);
            Assert.Equal(0, handler.StatusReads);
        }
        finally
        {
            handler.ReleaseShare.TrySetResult();
            await started;
        }
        Assert.False(service.IsRunning("site"));
        Assert.Equal(1, handler.StatusReads);
    }

    [Theory]
    [InlineData("start")]
    [InlineData("rebuild")]
    [InlineData("stop")]
    public async Task ShareCancellationReachesTransportAndReleasesMutationGate(string action)
    {
        handler.HoldShare = true;
        using var cancellation = new CancellationTokenSource();
        var operation = ShareAsync(action, cancellation.Token);
        await handler.ShareStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));

        cancellation.Cancel();

        Assert.Equal(OperationOutcome.Canceled, await operation.WaitAsync(TimeSpan.FromSeconds(5)));
        Assert.False(service.IsRunning("site"));
        Assert.Null(service.ErrorFor("site"));
        Assert.Equal(1, handler.ShareWrites);
    }

    private Task<OperationOutcome> ShareAsync(string action, CancellationToken token = default) => action switch
    {
        "start" => service.StartShareAsync("site", ["colleague@example.com"], true, token),
        "rebuild" => service.RebuildShareAsync("site", token),
        "stop" => service.StopShareAsync("site", token),
        _ => throw new ArgumentOutOfRangeException(nameof(action)),
    };

    [Fact]
    public async Task EquivalentExtensionReadsJoinAndBothReceiveTheReport()
    {
        var first = service.SiteExtensionsAsync("old");
        await handler.OldExtensionStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        var second = service.SiteExtensionsAsync("old");
        handler.ReleaseOldExtension.TrySetResult();

        var results = await Task.WhenAll(first, second);

        Assert.All(results, result => Assert.Equal("old", result.Report?.Php));
    }

    [Fact]
    public void StatusRefreshKeepsTheSelectedRowAndUncommittedDraft()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("site", "8.3")] });
        var retained = Assert.Single(model.Sites);
        retained.SetDraftPhp("8.4");

        model.Apply(new Status { Sites = [Site("site", "8.3"), Site("other", "8.2")] });

        Assert.Same(retained, model.Sites[0]);
        Assert.Equal("8.4", retained.Php);
    }

    private static Status.Site Site(string name, string php) => new() { Name = name, Domain = name + ".test", Php = php };

    private sealed class Handler : HttpMessageHandler
    {
        public bool FailUpdate { get; set; }
        public bool FailShare { get; set; }
        public bool HoldShare { get; set; }
        public int ShareWrites { get; private set; }
        public TaskCompletionSource ShareStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseShare { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public int StatusReads { get; private set; }
        public TaskCompletionSource OldExtensionStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseOldExtension { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            if (path.StartsWith("/v1/shares/", StringComparison.Ordinal))
            {
                ShareWrites++;
                ShareStarted.TrySetResult();
                if (HoldShare)
                {
                    await ReleaseShare.Task.WaitAsync(cancellationToken);
                }
                return FailShare
                    ? Json("{\"error\":\"share failed\"}", HttpStatusCode.BadRequest)
                    : Json(request.Method == HttpMethod.Delete ? "[]" : "{\"site\":\"site\",\"state\":\"building\"}");
            }
            if (path == "/v1/status")
            {
                StatusReads++;
                return Json("{\"sites\":[{\"name\":\"site\",\"domain\":\"site.test\",\"php\":\"8.3\"}]}");
            }
            if (path == "/v1/sites/site" && FailUpdate)
            {
                return Json("{\"error\":\"write failed\"}", HttpStatusCode.BadRequest);
            }
            if (path.EndsWith("/extensions", StringComparison.Ordinal))
            {
                var site = path.Split('/')[3];
                if (site == "old")
                {
                    OldExtensionStarted.TrySetResult();
                    await ReleaseOldExtension.Task;
                }
                return Json($"{{\"php\":\"{site}\",\"extensions\":[]}}");
            }
            return Json("{}");
        }

        private static HttpResponseMessage Json(string body, HttpStatusCode status = HttpStatusCode.OK) => new(status)
        {
            Content = new StringContent(body),
        };
    }
}
