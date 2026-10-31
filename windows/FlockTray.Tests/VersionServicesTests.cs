using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Node;
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
    public async Task FrankenPhpCheckUsesItsCheckEndpointAndRefreshesTheCatalog()
    {
        await using var service = new FrankenPhpService(client, new OperationRunner());
        handler.FrankenPhpBody = "{\"available\":true,\"builds\":[{\"php\":\"8.4\",\"latest\":\"8.4.14\"}]}";
        await service.RefreshAsync();
        handler.Requests.Clear();
        handler.FrankenPhpBody = "{\"available\":true,\"builds\":[{\"php\":\"8.4\",\"latest\":\"8.4.15\"}]}";

        Assert.Equal(OperationOutcome.Succeeded, await service.CheckAsync());

        Assert.Equal(["POST /v1/frankenphp/check", "GET /v1/frankenphp"], handler.Requests);
        Assert.Equal("8.4.15", service.Current!.Builds[0].Latest);
    }

    [Fact]
    public async Task PhpVersionActionsFollowTheReleaseState()
    {
        await using var service = new PhpService(client, new OperationRunner());

        var available = new PhpVersionViewModel(service, new PhpRelease { Minor = "8.4" });
        var installed = new PhpVersionViewModel(service, new PhpRelease { Minor = "8.4", Installed = "8.4.1", Update = true });
        var running = new PhpVersionViewModel(service, new PhpRelease { Minor = "8.4", Job = new ComponentJob { State = "running", Phase = "downloading", Progress = 0.42 } });
        var failed = new PhpVersionViewModel(service, new PhpRelease { Minor = "8.4", Job = new ComponentJob { State = "failed" } });

        Assert.True(available.CanInstall);
        Assert.False(available.CanUpdate);
        Assert.True(installed.CanUpdate);
        Assert.True(installed.CanMakeDefault);
        Assert.True(installed.CanUninstall);
        Assert.True(installed.ShowPrimaryUpdate);
        Assert.False(installed.ShowPrimaryUninstall);
        Assert.True(installed.HasMoreActions);
        Assert.True(running.HasRunningJob);
        Assert.True(running.ShowPrimaryCancel);
        Assert.False(running.CanInstall);
        Assert.Equal(0.42, running.JobProgress);
        Assert.False(running.IsJobProgressIndeterminate);
        Assert.Equal("Downloading… 42%", running.JobProgressText);
        Assert.True(failed.HasFailedJob);
        Assert.True(failed.ShowPrimaryRetry);
        Assert.False(failed.CanInstall);
    }

    [Fact]
    public async Task PhpVersionRowsProjectCatalogStateAndProtectTheDefaultRuntime()
    {
        await using var service = new PhpService(client, new OperationRunner());

        var defaultRow = new PhpVersionViewModel(service, new PhpRelease
        {
            Minor = "8.4", Installed = "8.4.1", Latest = "8.4.2", IsDefault = true, Status = PhpStatus.Update,
            Update = true, Prerelease = true, Xdebug = true, Yanked = "bad build",
            Job = new ComponentJob { State = "failed", Error = "download failed" },
        });
        var nonDefaultRow = new PhpVersionViewModel(service, new PhpRelease
        {
            Minor = "8.3", Installed = "8.3.9", Latest = "8.3.9", Status = PhpStatus.Installed,
        });
        var availableRow = new PhpVersionViewModel(service, new PhpRelease
        {
            Minor = "8.2", Latest = "8.2.30", Status = PhpStatus.Available,
        });

        Assert.Equal("PHP 8.4", defaultRow.DisplayName);
        Assert.Equal("8.4.1 → 8.4.2", defaultRow.VersionText);
        Assert.Equal("Update available", defaultRow.StatusText);
        Assert.True(defaultRow.IsPrerelease);
        Assert.True(defaultRow.HasXdebug);
        Assert.Equal("Withdrawn: bad build — update to replace it", defaultRow.WithdrawnText);
        Assert.Equal("download failed", defaultRow.JobErrorText);
        Assert.False(defaultRow.CanUninstall);
        Assert.False(defaultRow.ShowPrimaryUninstall);
        Assert.True(nonDefaultRow.CanUninstall);
        Assert.Equal("Up to date", nonDefaultRow.StatusText);
        Assert.Equal("Not installed", availableRow.StatusText);
    }

    [Fact]
    public async Task NodeVersionActionsRequireAnInstalledVersionForDefaultAndUpdates()
    {
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var service = new NodeService(client, new OperationRunner(), configuration);

        var available = new NodeVersionViewModel(service, new NodeRelease { Major = "22" });
        var installed = new NodeVersionViewModel(service, new NodeRelease { Major = "22", Installed = "22.1.0", Update = true, NpmLatest = "10.9.0" });

        Assert.True(available.CanInstall);
        Assert.False(available.CanMakeDefault);
        Assert.False(available.CanUpdateNpm);
        Assert.True(installed.CanMakeDefault);
        Assert.True(installed.CanUpdateNpm);
        Assert.True(installed.ShowPrimaryUpdate);
        Assert.False(installed.ShowPrimaryUninstall);
        Assert.True(installed.HasMoreActions);
    }

    [Fact]
    public async Task NodeVersionRowsProjectReleaseAndNpmStateWithoutDefaultUninstall()
    {
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var service = new NodeService(client, new OperationRunner(), configuration);

        var defaultRow = new NodeVersionViewModel(service, new NodeRelease
        {
            Major = "22", Installed = "22.1.0", Latest = "22.2.0", IsDefault = true, Update = true,
            Npm = "10.9.0", NpmLatest = "10.9.1",
        });
        var nonDefaultRow = new NodeVersionViewModel(service, new NodeRelease
        {
            Major = "20", Installed = "20.1.0", Latest = "20.1.0", Npm = "10.5.0",
        });

        Assert.Equal("Node 22", defaultRow.DisplayName);
        Assert.Equal("22.1.0 → 22.2.0", defaultRow.VersionText);
        Assert.Equal("Update available", defaultRow.StatusText);
        Assert.Equal("npm 10.9.0", defaultRow.NpmText);
        Assert.Equal("npm 10.9.1 available", defaultRow.NpmLatestText);
        Assert.False(defaultRow.CanUninstall);
        Assert.False(defaultRow.ShowPrimaryUninstall);
        Assert.True(nonDefaultRow.CanUninstall);
        Assert.Equal("Up to date", nonDefaultRow.StatusText);
        Assert.Equal("", nonDefaultRow.NpmLatestText);
    }

    [Fact]
    public async Task NodeRowBusyWhileTheFirstRunInstallsItAsTheDefault()
    {
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var service = new NodeService(client, new OperationRunner(), configuration);
        string? installing = "22";

        var row = new NodeVersionViewModel(service, new NodeRelease { Major = "22", Latest = "22.1.0" }, () => installing);

        Assert.True(row.IsWorking);
        Assert.Equal("Downloading Node.js 22.1.0…", row.JobProgressText);
        Assert.False(row.ShowPrimaryInstall);
        Assert.False(row.InstallCommand.CanExecute(null));

        installing = null;
        row.Update(row.Value);

        Assert.False(row.IsWorking);
        Assert.True(row.ShowPrimaryInstall);
        Assert.True(row.InstallCommand.CanExecute(null));
    }

    [Fact]
    public async Task FrankenPhpRowsNameThePhpVersionAndOfferTheLogOnceInstalled()
    {
        await using var service = new FrankenPhpService(client, new OperationRunner());

        var available = new FrankenPhpVersionViewModel(service, new FrankenPhpBuild { Php = "8.4", Latest = "8.4.5" });
        var installed = new FrankenPhpVersionViewModel(service, new FrankenPhpBuild { Php = "8.4", Installed = "1.9.0", PhpVersion = "8.4.5", Latest = "8.4.5" });

        Assert.Equal("PHP 8.4", available.DisplayName);
        Assert.Equal("Not installed · downloads by itself when a site uses it", available.DetailText);
        Assert.False(available.HasMoreActions);
        Assert.Equal("FrankenPHP 1.9.0 (PHP 8.4.5)", installed.DetailText);
        Assert.True(installed.HasMoreActions);
    }

    [Fact]
    public async Task NodeVersionRowsShowAnIndeterminateProgressIndicatorDuringInstallation()
    {
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var service = new NodeService(client, new OperationRunner(), configuration);
        handler.NodeVersionsBody = "{\"versions\":[{\"major\":\"22\",\"latest\":\"22.1.0\"}]}";
        await service.RefreshAsync();
        var row = new NodeVersionViewModel(service, service.Current!.Versions[0]);
        handler.BlockWrites = true;

        var install = service.InstallAsync("22");
        await handler.WriteStarted.Task;

        Assert.True(row.HasRunningJob);
        Assert.True(row.IsJobProgressIndeterminate);
        Assert.Equal("Downloading and installing…", row.JobProgressText);

        handler.ReleaseWrite.SetResult();
        await install;
    }

    [Fact]
    public async Task VersionPaneRowsFormatCheckTimeAndCheckErrors()
    {
        var checkedAt = DateTimeOffset.Now.AddMinutes(-3);
        handler.PhpVersionsBody = $"{{\"checked_at\":\"{checkedAt:O}\",\"check_error\":\"PHP manifest unavailable\",\"versions\":[]}}";
        handler.NodeVersionsBody = $"{{\"checked_at\":\"{checkedAt:O}\",\"check_error\":\"Node manifest unavailable\",\"versions\":[]}}";
        await using var php = new PhpService(client, new OperationRunner());
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var node = new NodeService(client, new OperationRunner(), configuration);
        var phpRows = new PhpPaneViewModel(php);
        var nodeRows = new NodePaneViewModel(node);

        await phpRows.RefreshAsync();
        await nodeRows.RefreshAsync();

        Assert.StartsWith("Last checked ", phpRows.CheckedText);
        Assert.Equal("Couldn't check for updates: PHP manifest unavailable", phpRows.CheckErrorText);
        Assert.StartsWith("Last checked ", nodeRows.CheckedText);
        Assert.Equal("Couldn't check for updates: Node manifest unavailable", nodeRows.CheckErrorText);
        phpRows.Dispose();
        nodeRows.Dispose();
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

    [Fact]
    public async Task EmptyNodeCatalogIsExposedByThePaneViewModel()
    {
        await using var configuration = new ConfigurationService(client, new OperationRunner());
        await using var service = new NodeService(client, new OperationRunner(), configuration);
        var rows = new NodePaneViewModel(service);

        await service.RefreshAsync();

        Assert.True(rows.IsEmpty);
        rows.Dispose();
    }

    private sealed class Handler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];
        public bool BlockWrites { get; set; }
        public HttpStatusCode Status { get; set; } = HttpStatusCode.OK;
        public string PhpVersionsBody { get; set; } = "{\"versions\":[{\"minor\":\"8.4\",\"latest\":\"8.4.1\",\"installed\":\"8.4.1\",\"default\":true,\"status\":\"installed\"}]}";
        public string NodeVersionsBody { get; set; } = "{\"versions\":[]}";
        public string FrankenPhpBody { get; set; } = "{\"available\":true,\"builds\":[]}";
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
            return path == "/v1/node/versions"
                ? Response(HttpStatusCode.OK, NodeVersionsBody)
                : path == "/v1/php/versions"
                ? Response(HttpStatusCode.OK, PhpVersionsBody)
                : Response(HttpStatusCode.OK, FrankenPhpBody);
        }

        private static HttpResponseMessage Response(HttpStatusCode status, string body) => new(status)
        {
            Content = new StringContent(status == HttpStatusCode.OK ? body : "{\"error\":\"" + body + "\"}")
        };
    }
}
