using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

namespace Flock.Kit;

public class FlockException(string message) : Exception(message);

/// <summary>Nothing answers on the daemon's API.</summary>
public sealed class FlockNotRunningException() : FlockException("Flock is not running");

/// <summary>The daemon answered with an error.</summary>
public sealed class FlockServerException(int status, string message)
    : FlockException(message.Length == 0 ? $"Flock answered with HTTP {status}" : message)
{
    public int StatusCode { get; } = status;
}

/// <summary>Talks to the daemon's local HTTP API (the loopback listener described by api.json).</summary>
public sealed class FlockClient : IDisposable
{
    private readonly string configPath;
    // Installing, building or uninstalling PHP waits for the daemon's work (the Go CLI allows 2 min too).
    private static readonly TimeSpan PhpChangeTimeout = TimeSpan.FromMinutes(2);
    // Installing or updating PostgreSQL downloads a server build and waits for it (and initdb).
    private static readonly TimeSpan PostgresDownloadTimeout = TimeSpan.FromMinutes(10);
    // Switching a site to FrankenPHP or restarting it waits for the process to come up (~20 s at most).
    private static readonly TimeSpan ServerSwitchTimeout = TimeSpan.FromMinutes(1);
    // Registering with Claude Code runs its CLI.
    private static readonly TimeSpan McpChangeTimeout = TimeSpan.FromSeconds(30);
    // Adding Flock's Boost guideline runs `php artisan boost:update`, which boots the app.
    private static readonly TimeSpan BoostTimeout = TimeSpan.FromMinutes(2);

    private readonly HttpClient http;
    private readonly SemaphoreSlim configLock = new(1, 1);
    private ApiConfig? config;

    public FlockClient(string? configPath = null, HttpMessageHandler? handler = null)
    {
        this.configPath = configPath ?? FlockPaths.ApiJson;
        // Never send localhost traffic through a proxy; timeouts are per request.
        http = new HttpClient(handler ?? new SocketsHttpHandler { UseProxy = false }) { Timeout = Timeout.InfiniteTimeSpan };
    }

    public void Dispose() => http.Dispose();

    // Endpoints

    public Task<Status> StatusAsync() => Get<Status>("/v1/status");

    /// <summary>Changes only the non-null settings and returns the new status.</summary>
    public Task<Status> UpdateSettingsAsync(SettingsChange change) => Send<Status>(HttpMethod.Post, "/v1/settings", change);

    public Task<FlockConfig> ConfigAsync() => Get<FlockConfig>("/v1/config");

    public Task<List<Service>> ServicesAsync() => Get<List<Service>>("/v1/services");

    public Task<List<Service>> StartServiceAsync(string id) => Send<List<Service>>(HttpMethod.Post, $"/v1/services/{Escape(id)}/start");

    public Task<List<Service>> StopServiceAsync(string id) => Send<List<Service>>(HttpMethod.Post, $"/v1/services/{Escape(id)}/stop");

    public Task<List<Service>> SetServicePortAsync(string id, int port) =>
        Send<List<Service>>(HttpMethod.Patch, $"/v1/services/{Escape(id)}", new { port });

    /// <summary>Links a directory as a site (the name defaults to the directory name on the daemon side).</summary>
    public Task<Status> LinkSiteAsync(string path, string? name = null, string? php = null, bool secured = false) =>
        Send<Status>(HttpMethod.Post, "/v1/sites", new { path, name, php, secured });

    /// <summary>Switching the server or worker mode reloads the web server and may start FrankenPHP, so it gets longer.</summary>
    public Task<Status> UpdateSiteAsync(string name, SiteChange change) =>
        Send<Status>(HttpMethod.Patch, $"/v1/sites/{Escape(name)}", change,
            timeout: change.Server is not null || change.Worker is not null ? ServerSwitchTimeout : null);

    /// <summary>Restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.</summary>
    public Task<Status> RestartSiteAsync(string name) => Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(name)}/restart", timeout: ServerSwitchTimeout);

    public Task<Status> UnlinkSiteAsync(string name) => Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(name)}");

    /// <summary>Adds an extra domain: a subdomain ("tenant1", "a.b") or an alias ("my-app-2.test").</summary>
    public Task<Status> AddDomainAsync(string site, string domain) =>
        Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/domains", new { domain });

    /// <summary>Removes an extra domain, given as SiteDomain.Entry (or its full name).</summary>
    public Task<Status> RemoveDomainAsync(string site, string domain) =>
        Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(site)}/domains/{Escape(domain)}");

    /// <summary>
    /// Writes a version ("php" or "node"; empty version: the one the site uses now) into the
    /// site's project files and lifts the site's isolation for that tool.
    /// </summary>
    public Task<Status> PinAsync(string site, string tool, string version = "") =>
        Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/pin", new { tool, version });

    /// <summary>Removes a tool's version from the site's project files.</summary>
    public Task<Status> UnpinAsync(string site, string tool) => Send<Status>(HttpMethod.Delete, $"/v1/sites/{Escape(site)}/pin/{Escape(tool)}");

    /// <summary>
    /// Writes Flock's guideline into the site's Laravel Boost guidelines (.ai\guidelines\flock.md) and
    /// runs `php artisan boost:update`, so the agents' guideline files include it.
    /// </summary>
    public Task<Status> AddBoostGuidelineAsync(string site) => Send<Status>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/boost", timeout: BoostTimeout);

    public Task<ExtensionReport> SiteExtensionsAsync(string site) => Get<ExtensionReport>($"/v1/sites/{Escape(site)}/extensions");

    /// <summary>Adds the extension to the site's composer.json (ext-NAME) with Composer (up to 5 minutes).</summary>
    public Task<ExtensionReport> RequireExtensionAsync(string site, string name) => Send<ExtensionReport>(HttpMethod.Post, $"/v1/sites/{Escape(site)}/extensions/{Escape(name)}/require", timeout: TimeSpan.FromMinutes(5));

    /// <summary>The AI clients Flock's MCP server can be registered with.</summary>
    public Task<McpState> McpAsync() => Get<McpState>("/v1/mcp");

    /// <summary>Registers Flock's MCP server with a client ("claude-code", "cursor", "vscode", "windsurf", "codex").</summary>
    public Task<McpState> RegisterMcpAsync(string client) => Send<McpState>(HttpMethod.Post, $"/v1/mcp/clients/{Escape(client)}", timeout: McpChangeTimeout);

    /// <summary>Removes Flock's MCP server from a client; nothing else in its configuration changes.</summary>
    public Task<McpState> UnregisterMcpAsync(string client) => Send<McpState>(HttpMethod.Delete, $"/v1/mcp/clients/{Escape(client)}", timeout: McpChangeTimeout);

    public Task<Status> ParkAsync(string path) => Send<Status>(HttpMethod.Post, "/v1/parked", new { path });

    public Task<Status> UnparkAsync(string path) => Send<Status>(HttpMethod.Delete, "/v1/parked", new { path });

    /// <summary>Downloads and installs a Node.js version ("20", "20.19" or exact); this can take a minute.</summary>
    public async Task<string> InstallNodeAsync(string version)
    {
        var body = await Raw(HttpMethod.Post, "/v1/node/install", new { version }, timeout: TimeSpan.FromMinutes(5));
        return JsonDocument.Parse(body).RootElement.GetProperty("version").GetString() ?? version;
    }

    // Node.js versions

    /// <summary>The Node.js lines; on a cold cache the daemon asks nodejs.org first, which can take ~30 s.</summary>
    public Task<NodeCatalog> NodeVersionsAsync() => Send<NodeCatalog>(HttpMethod.Get, "/v1/node/versions", timeout: TimeSpan.FromMinutes(1));

    /// <summary>Installs the newest release of a major line, or updates it; a download of about 30 MB.</summary>
    public Task<NodeCatalog> InstallNodeMajorAsync(string major) =>
        Send<NodeCatalog>(HttpMethod.Post, $"/v1/node/versions/{Escape(major)}/install", timeout: TimeSpan.FromMinutes(10));

    /// <summary>Updates a line's npm to the newest version that supports it; its global packages stay.</summary>
    public Task<NodeCatalog> UpdateNpmAsync(string major) =>
        Send<NodeCatalog>(HttpMethod.Post, $"/v1/node/versions/{Escape(major)}/npm", timeout: TimeSpan.FromMinutes(10));

    public Task<NodeCatalog> UninstallNodeAsync(string major) => Send<NodeCatalog>(HttpMethod.Delete, $"/v1/node/versions/{Escape(major)}");

    /// <summary>Asks the daemon to check nodejs.org for new releases now.</summary>
    public Task<NodeCatalog> CheckNodeReleasesAsync() => Send<NodeCatalog>(HttpMethod.Post, "/v1/node/check", timeout: TimeSpan.FromMinutes(1));

    // PHP versions

    public Task<PhpCatalog> PhpVersionsAsync() => Get<PhpCatalog>("/v1/php/versions");

    /// <summary>Starts installing (or updating to) the channel's newest release of a version; follow it with PhpVersionsAsync.</summary>
    public Task<PhpCatalog> InstallPhpAsync(string minor) => Send<PhpCatalog>(HttpMethod.Post, $"/v1/php/versions/{Escape(minor)}/install", timeout: PhpChangeTimeout);

    public Task<PhpCatalog> CancelPhpInstallAsync(string minor) => Send<PhpCatalog>(HttpMethod.Post, $"/v1/php/versions/{Escape(minor)}/cancel", timeout: PhpChangeTimeout);

    /// <summary>Forgets a failed install.</summary>
    public Task<PhpCatalog> DismissPhpJobAsync(string minor) => Send<PhpCatalog>(HttpMethod.Delete, $"/v1/php/versions/{Escape(minor)}/job");

    public Task<PhpCatalog> UninstallPhpAsync(string minor) => Send<PhpCatalog>(HttpMethod.Delete, $"/v1/php/versions/{Escape(minor)}", timeout: PhpChangeTimeout);

    /// <summary>Asks the daemon to fetch the binaries manifest now.</summary>
    public Task<PhpCatalog> CheckPhpReleasesAsync() => Send<PhpCatalog>(HttpMethod.Post, "/v1/php/check", timeout: TimeSpan.FromMinutes(1));

    public async Task<string> PhpInstallLogAsync(string minor) => await Text(HttpMethod.Get, $"/v1/php/versions/{Escape(minor)}/log");

    // FrankenPHP builds

    /// <summary>The builds per PHP minor; 501 (FlockServerException) when FrankenPHP isn't supported here.</summary>
    public Task<FrankenPhpState> FrankenPhpAsync() => Get<FrankenPhpState>("/v1/frankenphp");

    /// <summary>Asks the daemon to fetch the list of builds now.</summary>
    public Task<FrankenPhpState> CheckFrankenPhpAsync() => Send<FrankenPhpState>(HttpMethod.Post, "/v1/frankenphp/check", timeout: TimeSpan.FromMinutes(1));

    /// <summary>Starts installing, updating or retrying a minor's build in the background; poll FrankenPhpAsync.</summary>
    public Task<FrankenPhpState> InstallFrankenPhpAsync(string minor) => Send<FrankenPhpState>(HttpMethod.Post, $"/v1/frankenphp/{Escape(minor)}/install");

    public Task<FrankenPhpState> CancelFrankenPhpJobAsync(string minor) => Send<FrankenPhpState>(HttpMethod.Post, $"/v1/frankenphp/{Escape(minor)}/cancel");

    /// <summary>Forgets a failed install.</summary>
    public Task<FrankenPhpState> DismissFrankenPhpJobAsync(string minor) => Send<FrankenPhpState>(HttpMethod.Delete, $"/v1/frankenphp/{Escape(minor)}/job");

    /// <summary>Removes a minor's build; 409 (FlockServerException) while a site runs on it.</summary>
    public Task<FrankenPhpState> UninstallFrankenPhpAsync(string minor) =>
        Send<FrankenPhpState>(HttpMethod.Delete, $"/v1/frankenphp/{Escape(minor)}", timeout: TimeSpan.FromMinutes(1));

    /// <summary>The end of the current or last install's output; 404 (FlockServerException) when there is none.</summary>
    public Task<string> FrankenPhpLogAsync(string minor) => Text(HttpMethod.Get, $"/v1/frankenphp/{Escape(minor)}/log");

    // PostgreSQL versions

    /// <summary>Cached by the daemon (it checks Maven Central in the background), so it answers at once.</summary>
    public Task<PostgresVersions> PostgresVersionsAsync() => Get<PostgresVersions>("/v1/postgres/versions");

    /// <summary>Asks the daemon to look for new PostgreSQL releases now.</summary>
    public Task<PostgresVersions> CheckPostgresReleasesAsync() =>
        Send<PostgresVersions>(HttpMethod.Post, "/v1/postgres/check", timeout: TimeSpan.FromMinutes(1));

    /// <summary>Downloads and installs a major's newest release; the first one installed becomes active and starts.</summary>
    public Task<PostgresVersions> InstallPostgresAsync(string major) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/install", timeout: PostgresDownloadTimeout);

    /// <summary>Installs a major's newest release over the installed one; its databases stay, a running server restarts.</summary>
    public Task<PostgresVersions> UpdatePostgresAsync(string major) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/update", timeout: PostgresDownloadTimeout);

    /// <summary>Stops the active major and starts this one with its own databases.</summary>
    public Task<PostgresVersions> ActivatePostgresAsync(string major) =>
        Send<PostgresVersions>(HttpMethod.Post, $"/v1/postgres/versions/{Escape(major)}/activate", timeout: TimeSpan.FromMinutes(2));

    /// <summary>Removes a major's binaries; its data directory goes too only when asked.</summary>
    public Task<PostgresVersions> UninstallPostgresAsync(string major, bool deleteData = false) =>
        Send<PostgresVersions>(HttpMethod.Delete, $"/v1/postgres/versions/{Escape(major)}" + (deleteData ? "?delete_data=1" : ""), timeout: TimeSpan.FromMinutes(2));

    // Catalog (MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS)

    public Task<ServiceCatalog> CatalogAsync() => Get<ServiceCatalog>("/v1/catalog");

    public Task<ServiceCatalog> CheckCatalogAsync() =>
        Send<ServiceCatalog>(HttpMethod.Post, "/v1/catalog/check", timeout: TimeSpan.FromMinutes(1));

    /// <summary>
    /// Starts installing the newest release of a line on a port in the background (null: the
    /// installed line or the newest, and the configured or suggested port); poll CatalogAsync.
    /// </summary>
    public Task<ServiceCatalog> InstallCatalogServiceAsync(string id, string? line = null, int? port = null) =>
        Send<ServiceCatalog>(HttpMethod.Post, $"/v1/catalog/{Escape(id)}/install", new { line = line ?? "", port = port ?? 0 });

    public Task<ServiceCatalog> CancelCatalogJobAsync(string id) => Send<ServiceCatalog>(HttpMethod.Post, $"/v1/catalog/{Escape(id)}/cancel");

    /// <summary>Forgets a failed install.</summary>
    public Task<ServiceCatalog> DismissCatalogJobAsync(string id) => Send<ServiceCatalog>(HttpMethod.Delete, $"/v1/catalog/{Escape(id)}/job");

    /// <summary>The end of the current or last install's output; 404 (FlockServerException) when there is none.</summary>
    public Task<string> CatalogLogAsync(string id) => Text(HttpMethod.Get, $"/v1/catalog/{Escape(id)}/log");

    /// <summary>Stops and removes a catalog service; its data goes too only when asked.</summary>
    public Task<ServiceCatalog> UninstallCatalogServiceAsync(string id, bool deleteData = false) =>
        Send<ServiceCatalog>(HttpMethod.Delete, $"/v1/catalog/{Escape(id)}" + (deleteData ? "?delete_data=1" : ""), timeout: TimeSpan.FromMinutes(1));

    // Components (the PostgreSQL command-line tools; Valkey on macOS)

    public Task<ComponentCatalog> ComponentsAsync() => Get<ComponentCatalog>("/v1/components");

    public Task<ComponentCatalog> CheckComponentsAsync() =>
        Send<ComponentCatalog>(HttpMethod.Post, "/v1/components/check", timeout: TimeSpan.FromMinutes(1));

    /// <summary>Starts installing (or updating) in the background; poll ComponentsAsync for the job.</summary>
    public Task<ComponentCatalog> InstallComponentAsync(string id) => Send<ComponentCatalog>(HttpMethod.Post, $"/v1/components/{Escape(id)}/install");

    public Task<ComponentCatalog> CancelComponentJobAsync(string id) => Send<ComponentCatalog>(HttpMethod.Post, $"/v1/components/{Escape(id)}/cancel");

    /// <summary>Forgets a failed job.</summary>
    public Task<ComponentCatalog> DismissComponentJobAsync(string id) => Send<ComponentCatalog>(HttpMethod.Delete, $"/v1/components/{Escape(id)}/job");

    /// <summary>The end of the current or last job's output; 404 (FlockServerException) when there is none.</summary>
    public Task<string> ComponentLogAsync(string id) => Text(HttpMethod.Get, $"/v1/components/{Escape(id)}/log");

    /// <summary>Uninstalls (a service stops first; its data stays).</summary>
    public Task<ComponentCatalog> UninstallComponentAsync(string id) =>
        Send<ComponentCatalog>(HttpMethod.Delete, $"/v1/components/{Escape(id)}", timeout: TimeSpan.FromSeconds(30));

    // Mail

    public Task<List<MailSummary>> MailAsync() => Get<List<MailSummary>>("/v1/mail");

    public Task<MailDetail> MailAsync(string id) => Get<MailDetail>($"/v1/mail/{Escape(id)}");

    public Task<string> RawMailAsync(string id) => Text(HttpMethod.Get, $"/v1/mail/{Escape(id)}/raw");

    public Task<byte[]> AttachmentAsync(string mailId, int index) => Raw(HttpMethod.Get, $"/v1/mail/{Escape(mailId)}/attachments/{index}");

    public async Task ClearMailAsync(string? inbox = null) =>
        await Raw(HttpMethod.Delete, "/v1/mail" + (inbox is null ? "" : "?inbox=" + Uri.EscapeDataString(inbox)));

    // Dumps

    public Task<List<Dump>> DumpsAsync() => Get<List<Dump>>("/v1/dumps");

    /// <summary>Symfony's dump &lt;script&gt;/&lt;style&gt; header, shared by all dump bodies.</summary>
    public Task<string> DumpHeaderAsync() => Text(HttpMethod.Get, "/v1/dumps/header");

    public async Task ClearDumpsAsync() => await Raw(HttpMethod.Delete, "/v1/dumps");

    // Logs

    public Task<LogIndex> LogsAsync() => Get<LogIndex>("/v1/logs");

    public Task<string> ServiceLogAsync(string name, int lines = 300) =>
        Text(HttpMethod.Get, $"/v1/logs/services/{Escape(name)}?lines={lines}");

    public Task<List<LogEntry>> SiteLogAsync(string name, int limit = 200) =>
        Get<List<LogEntry>>($"/v1/logs/sites/{Escape(name)}?limit={limit}");

    public async Task ClearSiteLogAsync(string name) => await Raw(HttpMethod.Delete, $"/v1/logs/sites/{Escape(name)}");

    // Transport

    /// <summary>The time a request is given, recorded in its options (for diagnostics and tests).</summary>
    public static readonly HttpRequestOptionsKey<TimeSpan> TimeoutOption = new("Flock.Timeout");

    private static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(5);

    private Task<T> Get<T>(string path) => Send<T>(HttpMethod.Get, path);

    private async Task<T> Send<T>(HttpMethod method, string path, object? body = null, TimeSpan? timeout = null)
    {
        var data = await Raw(method, path, body, timeout);
        // Go encodes nil slices as null; an empty list is what every caller wants then.
        if (data.Length == 0 || data.AsSpan().SequenceEqual("null"u8))
        {
            data = typeof(T).IsGenericType && typeof(T).GetGenericTypeDefinition() == typeof(List<>) ? "[]"u8.ToArray() : data;
        }
        try
        {
            return JsonSerializer.Deserialize<T>(data, FlockJson.Options) ?? throw new FlockException($"empty answer from {path}");
        }
        catch (JsonException e)
        {
            throw new FlockException($"Unexpected answer from {path}: {e.Message}");
        }
    }

    private async Task<string> Text(HttpMethod method, string path) => Encoding.UTF8.GetString(await Raw(method, path));

    /// <summary>
    /// Sends a request; on a connection failure it re-reads api.json once and retries, because a
    /// restarted daemon listens on a new port with a new token.
    /// </summary>
    private async Task<byte[]> Raw(HttpMethod method, string path, object? body = null, TimeSpan? timeout = null)
    {
        try
        {
            return await Attempt(method, path, body, timeout, await LoadConfig(force: false));
        }
        catch (HttpRequestException)
        {
            try
            {
                return await Attempt(method, path, body, timeout, await LoadConfig(force: true));
            }
            catch (HttpRequestException)
            {
                throw new FlockNotRunningException();
            }
        }
        catch (FlockServerException e) when (e.StatusCode == (int)HttpStatusCode.Unauthorized)
        {
            return await Attempt(method, path, body, timeout, await LoadConfig(force: true));
        }
    }

    private async Task<byte[]> Attempt(HttpMethod method, string path, object? body, TimeSpan? timeout, ApiConfig api)
    {
        using var request = new HttpRequestMessage(method, api.Url.TrimEnd('/') + path);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", api.Token);
        if (body is not null)
        {
            request.Content = new StringContent(JsonSerializer.Serialize(body, FlockJson.Options), Encoding.UTF8, "application/json");
        }

        var limit = timeout ?? DefaultTimeout;
        request.Options.Set(TimeoutOption, limit);
        using var cts = new CancellationTokenSource(limit);
        HttpResponseMessage response;
        try
        {
            response = await http.SendAsync(request, cts.Token);
        }
        catch (TaskCanceledException) when (cts.IsCancellationRequested)
        {
            throw new FlockException($"Flock did not answer {method} {path} in time");
        }

        using (response)
        {
            var data = await response.Content.ReadAsByteArrayAsync(CancellationToken.None);
            if (!response.IsSuccessStatusCode)
            {
                throw new FlockServerException((int)response.StatusCode, ErrorMessage(data));
            }
            return data;
        }
    }

    private static string ErrorMessage(byte[] data)
    {
        try
        {
            using var doc = JsonDocument.Parse(data);
            if (doc.RootElement.ValueKind == JsonValueKind.Object && doc.RootElement.TryGetProperty("error", out var error))
            {
                return error.GetString() ?? "";
            }
        }
        catch (JsonException)
        {
        }
        var text = Encoding.UTF8.GetString(data);
        return text.Length > 500 ? text[..500] : text;
    }

    private async Task<ApiConfig> LoadConfig(bool force)
    {
        await configLock.WaitAsync();
        try
        {
            if (config is not null && !force)
            {
                return config;
            }
            try
            {
                var loaded = FlockJson.Decode<ApiConfig>(await File.ReadAllTextAsync(configPath));
                if (loaded.Url.Length == 0)
                {
                    throw new FlockNotRunningException();
                }
                return config = loaded;
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or JsonException)
            {
                throw new FlockNotRunningException();
            }
        }
        finally
        {
            configLock.Release();
        }
    }

    private static string Escape(string segment) => Uri.EscapeDataString(segment);
}
