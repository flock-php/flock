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

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Requests.Add(request);
            Bodies.Add(request.Content is null ? "" : await request.Content.ReadAsStringAsync(cancellationToken));
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
}
