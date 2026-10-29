using System.Globalization;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.RegularExpressions;

namespace Flock.Tray;

/// <summary>
/// Answers the app's API calls from fixture files, on a loopback port it announces in
/// FLOCK_HOME\api.json, so FlockClient works unchanged. GET /v1/php/versions reads
/// php_versions.json (.json, .txt or .html). "{{now}}" and "{{now-3h}}" (s, m, h, d) in a fixture
/// become times relative to now, so "3 hours ago" stays true. Changes (POST, PATCH, DELETE) are
/// refused: a shot shows state.
/// </summary>
internal sealed partial class FakeDaemon : IDisposable
{
    private readonly string fixtures;
    private readonly HttpListener listener = new();

    public FakeDaemon(string fixtures, string home)
    {
        this.fixtures = fixtures;
        var port = FreePort();
        listener.Prefixes.Add($"http://localhost:{port}/");
        listener.Start();
        File.WriteAllText(Path.Combine(home, "api.json"), $$"""{"url":"http://localhost:{{port}}","token":"screenshots"}""");
        _ = ServeAsync();
    }

    public void Dispose() => listener.Close();

    private static int FreePort()
    {
        var probe = new TcpListener(IPAddress.Loopback, 0);
        probe.Start();
        var port = ((IPEndPoint)probe.LocalEndpoint).Port;
        probe.Stop();
        return port;
    }

    private async Task ServeAsync()
    {
        while (listener.IsListening)
        {
            HttpListenerContext context;
            try
            {
                context = await listener.GetContextAsync();
            }
            catch (Exception e) when (e is HttpListenerException or ObjectDisposedException)
            {
                return;
            }
            Answer(context);
        }
    }

    private void Answer(HttpListenerContext context)
    {
        var path = context.Request.Url?.AbsolutePath ?? "";
        var (status, type, body) = context.Request.HttpMethod != "GET"
            ? (501, "application/json", $$"""{"error":"screenshots don't change anything ({{context.Request.HttpMethod}} {{path}})"}""")
            : Fixture(path) is { } fixture
                ? (200, fixture.Type, fixture.Body)
                : (404, "application/json", $$"""{"error":"no fixture for {{path}}"}""");
        if (status != 200)
        {
            Console.Error.WriteLine($"  fake daemon: {status} {context.Request.HttpMethod} {path}");
        }
        var data = Encoding.UTF8.GetBytes(body);
        context.Response.StatusCode = status;
        context.Response.ContentType = type;
        context.Response.ContentLength64 = data.Length;
        context.Response.OutputStream.Write(data);
        context.Response.Close();
    }

    private (string Type, string Body)? Fixture(string path)
    {
        var name = Uri.UnescapeDataString(path.TrimStart('/'))
            .Replace("v1/", "", StringComparison.Ordinal)
            .Replace('/', '_');
        foreach (var (extension, type) in new[] { (".json", "application/json"), (".txt", "text/plain"), (".html", "text/html") })
        {
            var file = Path.Combine(fixtures, name + extension);
            if (File.Exists(file))
            {
                return (type, Times().Replace(File.ReadAllText(file), Time));
            }
        }
        return null;
    }

    private static string Time(Match match)
    {
        var amount = match.Groups["amount"].Success ? int.Parse(match.Groups["amount"].Value, CultureInfo.InvariantCulture) : 0;
        var ago = match.Groups["unit"].Value switch
        {
            "s" => TimeSpan.FromSeconds(amount),
            "m" => TimeSpan.FromMinutes(amount),
            "h" => TimeSpan.FromHours(amount),
            "d" => TimeSpan.FromDays(amount),
            _ => TimeSpan.Zero,
        };
        return (DateTimeOffset.Now - ago).ToString("yyyy-MM-dd'T'HH:mm:ssK", CultureInfo.InvariantCulture);
    }

    [GeneratedRegex(@"\{\{now(?:-(?<amount>\d+)(?<unit>[smhd]))?\}\}")]
    private static partial Regex Times();
}
