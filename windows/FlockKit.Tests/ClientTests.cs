using System.Net;

namespace Flock.Kit.Tests;

public class ClientTests : IDisposable
{
    private readonly string dir = Directory.CreateTempSubdirectory("flock-client-").FullName;

    public void Dispose() => Directory.Delete(dir, recursive: true);

    private sealed class Handler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        public List<HttpRequestMessage> Requests { get; } = [];
        public List<string> Bodies { get; } = [];
        public List<TimeSpan> Timeouts { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Requests.Add(request);
            Bodies.Add(request.Content is null ? "" : await request.Content.ReadAsStringAsync(cancellationToken));
            Timeouts.Add(request.Options.TryGetValue(FlockClient.TimeoutOption, out var timeout) ? timeout : TimeSpan.Zero);
            return respond(request);
        }
    }

    private string WriteApiJson(string url, string token)
    {
        var path = Path.Combine(dir, "api.json");
        File.WriteAllText(path, $$"""{"url":"{{url}}","token":"{{token}}"}""");
        return path;
    }

    [Fact]
    public async Task SendsTheBearerTokenAndDecodesNullListsAsEmpty()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("null") });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:53531", "secret"), handler);

        Assert.Empty(await client.MailAsync());
        Assert.Equal("Bearer secret", handler.Requests[0].Headers.Authorization?.ToString());
        Assert.Equal("http://127.0.0.1:53531/v1/mail", handler.Requests[0].RequestUri?.ToString());
    }

    [Fact]
    public async Task RereadsApiJsonAfterUnauthorized()
    {
        var path = WriteApiJson("http://127.0.0.1:1", "old");
        var handler = new Handler(r => r.Headers.Authorization?.Parameter == "new"
            ? new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("""{"services":["web"],"sites":null}""") }
            : new HttpResponseMessage(HttpStatusCode.Unauthorized) { Content = new StringContent("""{"error":"missing or invalid token"}""") });
        using var client = new FlockClient(path, handler);

        // The daemon restarted with a new token after the client cached the old one.
        await Assert.ThrowsAsync<FlockServerException>(async () =>
        {
            File.WriteAllText(path, """{"url":"http://127.0.0.1:1","token":"old"}""");
            await client.LogsAsync();
        });
        File.WriteAllText(path, """{"url":"http://127.0.0.1:1","token":"new"}""");
        var index = await client.LogsAsync();
        Assert.Equal(["web"], index.Services);
    }

    [Fact]
    public async Task ReportsServerErrorsWithTheDaemonsMessage()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.Conflict) { Content = new StringContent("""{"error":"PHP 8.5 is the default version"}""") });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        var error = await Assert.ThrowsAsync<FlockServerException>(() => client.UninstallPhpAsync("8.5"));
        Assert.Equal(409, error.StatusCode);
        Assert.Equal("PHP 8.5 is the default version", error.Message);
        Assert.Equal(HttpMethod.Delete, handler.Requests[0].Method);
    }

    [Fact]
    public async Task IsNotRunningWithoutApiJsonOrListener()
    {
        using var missing = new FlockClient(Path.Combine(dir, "nope.json"));
        await Assert.ThrowsAsync<FlockNotRunningException>(() => missing.StatusAsync());

        // Port 9 (discard) on loopback has no listener: the connection is refused.
        using var refused = new FlockClient(WriteApiJson("http://127.0.0.1:9", "t"));
        await Assert.ThrowsAsync<FlockNotRunningException>(() => refused.StatusAsync());
    }

    [Fact]
    public async Task SendsOnlyGivenFieldsWhenLinkingSites()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"""),
        });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.LinkSiteAsync(@"C:\code\shop");
        Assert.Equal("""{"path":"C:\\code\\shop","secured":false}""", handler.Bodies[0]);
    }

    [Fact]
    public async Task GivesSlowEndpointsLongerThanTheDefault()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.StatusAsync();
        // The daemon may ask nodejs.org first (~30 s on a cold cache).
        await client.NodeVersionsAsync();
        // Changing PHP versions waits for the daemon's work, like the Go CLI (2 min).
        await client.InstallPhpAsync("8.4");
        await client.CancelPhpInstallAsync("8.4");
        await client.UninstallPhpAsync("8.4");
        await client.PhpVersionsAsync();

        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromSeconds(5)],
            handler.Timeouts);
    }

    [Fact]
    public async Task CallsThePostgresVersionEndpoints()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("""{"active":"18","majors":[]}""") });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.PostgresVersionsAsync();
        await client.CheckPostgresReleasesAsync();
        await client.InstallPostgresAsync("17");
        await client.UpdatePostgresAsync("18");
        await client.ActivatePostgresAsync("17");
        await client.UninstallPostgresAsync("16");
        var answer = await client.UninstallPostgresAsync("15", deleteData: true);

        Assert.Equal("18", answer.Active);
        Assert.Equal(
            [
                "GET /v1/postgres/versions", "POST /v1/postgres/check", "POST /v1/postgres/versions/17/install",
                "POST /v1/postgres/versions/18/update", "POST /v1/postgres/versions/17/activate",
                "DELETE /v1/postgres/versions/16", "DELETE /v1/postgres/versions/15?delete_data=1",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        // Installs and updates download a server and wait for it; switching and uninstalling stop one.
        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromMinutes(10), TimeSpan.FromMinutes(10), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2)],
            handler.Timeouts);
    }

    [Fact]
    public async Task CallsTheCatalogEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/log") ? "==> Downloading\n" : """{"services":[{"id":"mysql"}]}"""),
        });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.CatalogAsync();
        await client.CheckCatalogAsync();
        var started = await client.InstallCatalogServiceAsync("mysql", "8.4", 3307);
        Assert.NotNull(started["mysql"]);
        await client.InstallCatalogServiceAsync("redis");
        await client.CancelCatalogJobAsync("mysql");
        await client.DismissCatalogJobAsync("mysql");
        Assert.Equal("==> Downloading\n", await client.CatalogLogAsync("mysql"));
        await client.UninstallCatalogServiceAsync("mysql");
        await client.UninstallCatalogServiceAsync("mongodb", deleteData: true);

        Assert.Equal(
            [
                "GET /v1/catalog", "POST /v1/catalog/check", "POST /v1/catalog/mysql/install", "POST /v1/catalog/redis/install",
                "POST /v1/catalog/mysql/cancel", "DELETE /v1/catalog/mysql/job", "GET /v1/catalog/mysql/log",
                "DELETE /v1/catalog/mysql", "DELETE /v1/catalog/mongodb?delete_data=1",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Contains("\"line\":\"8.4\"", handler.Bodies[2]);
        Assert.Contains("\"port\":3307", handler.Bodies[2]);
        Assert.Contains("\"port\":0", handler.Bodies[3]);
        Assert.Equal(TimeSpan.FromMinutes(1), handler.Timeouts[^1]);
    }

    [Fact]
    public async Task CallsTheFrankenPhpEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/log") ? "downloading…\n" : """{"available":true,"builds":[{"php":"8.4"}]}"""),
        });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        var state = await client.FrankenPhpAsync();
        Assert.Equal("8.4", state.Builds[0].Php);
        await client.CheckFrankenPhpAsync();
        await client.InstallFrankenPhpAsync("8.4");
        await client.CancelFrankenPhpJobAsync("8.4");
        await client.DismissFrankenPhpJobAsync("8.4");
        Assert.Equal("downloading…\n", await client.FrankenPhpLogAsync("8.4"));
        await client.UninstallFrankenPhpAsync("8.4");

        Assert.Equal(
            [
                "GET /v1/frankenphp", "POST /v1/frankenphp/check", "POST /v1/frankenphp/8.4/install",
                "POST /v1/frankenphp/8.4/cancel", "DELETE /v1/frankenphp/8.4/job", "GET /v1/frankenphp/8.4/log",
                "DELETE /v1/frankenphp/8.4",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        // Installs return at once (the job runs in the daemon); uninstalling may stop a process first.
        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60)],
            handler.Timeouts);
    }

    [Fact]
    public async Task SwitchesAndRestartsASitesServer()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"""),
        });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.UpdateSiteAsync("shop", new SiteChange(Server: Status.SiteServer.FrankenPhp));
        await client.UpdateSiteAsync("shop", new SiteChange(Worker: ""));
        await client.UpdateSiteAsync("shop", new SiteChange(Secured: true));
        await client.RestartSiteAsync("shop");

        Assert.Equal(
            ["PATCH /v1/sites/shop", "PATCH /v1/sites/shop", "PATCH /v1/sites/shop", "POST /v1/sites/shop/restart"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(["""{"server":"frankenphp"}""", """{"worker":""}""", """{"secured":true}""", ""], handler.Bodies);
        // Switching servers and restarting wait for FrankenPHP to come up.
        Assert.Equal([TimeSpan.FromMinutes(1), TimeSpan.FromMinutes(1), TimeSpan.FromSeconds(5), TimeSpan.FromMinutes(1)], handler.Timeouts);
    }

    [Fact]
    public async Task ReportsAMissingCatalogLogAsAServerError()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.NotFound) { Content = new StringContent("""{"error":"no log for redis"}""") });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        var error = await Assert.ThrowsAsync<FlockServerException>(() => client.CatalogLogAsync("redis"));
        Assert.Equal(404, error.StatusCode);
    }

    [Fact]
    public async Task CallsTheAiClientAndBoostEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/boost")
                ? """{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[]}"""
                : """{"clients":[{"id":"cursor","name":"Cursor","state":"registered"}],"command":"flock.exe","args":["mcp"],"snippet":"{}"}"""),
        });
        using var client = new FlockClient(WriteApiJson("http://127.0.0.1:1", "t"), handler);

        await client.McpAsync();
        var added = await client.RegisterMcpAsync("cursor");
        Assert.Equal(McpClientState.Registered, added.Clients[0].State);
        await client.UnregisterMcpAsync("cursor");
        await client.AddBoostGuidelineAsync("my app");

        Assert.Equal(
            ["GET /v1/mcp", "POST /v1/mcp/clients/cursor", "DELETE /v1/mcp/clients/cursor", "POST /v1/sites/my%20app/boost"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(TimeSpan.FromSeconds(30), handler.Timeouts[1]);
        Assert.Equal(TimeSpan.FromMinutes(2), handler.Timeouts[^1]);
    }
}
