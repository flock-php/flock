using System.Diagnostics;
using System.Text.Json;
using Flock.Kit;

namespace Flock.Tray;

/// <summary>
/// Shared state for the tray panel and all windows. Polls the daemon's status every two seconds.
/// Everything runs on the UI thread (async continuations come back to it), so events can update UI directly.
/// </summary>
public sealed class AppModel
{
    public FlockClient Client { get; } = new();

    public Status? Status { get; private set; }
    /// <summary>Why the status can't be read (the daemon isn't running); the next refresh replaces it.</summary>
    public string? Error { get; private set; }
    /// <summary>
    /// Why the last restart or Xdebug/dumps toggle failed. Status refreshes keep it; it goes when
    /// dismissed or when the next of those actions succeeds.
    /// </summary>
    public string? ActionError { get; private set; }
    public bool IsRestarting { get; private set; }

    public MailStore Mail { get; }
    public DumpStore Dumps { get; }
    public LogStore Logs { get; }
    public SettingsStore Settings { get; }

    /// <summary>Raised whenever Status, Error, ActionError or IsRestarting change.</summary>
    public event Action? Changed;

    public AppModel()
    {
        Mail = new MailStore(Client);
        Dumps = new DumpStore(Client);
        Logs = new LogStore(Client);
        Settings = new SettingsStore(Client) { OnChange = RefreshAsync };
    }

    public bool IsRunning => Status is not null;
    public bool IsDegraded => Status?.IsDegraded ?? false;

    /// <summary>Flock isn't set up for this user yet, and this app can do it (see Installation).</summary>
    public bool NeedsSetup { get; set; }

    /// <summary>A flock://install link waiting for the user to confirm it in Settings → Services.</summary>
    public InstallLink? PendingInstall { get; set; }

    /// <summary>A flock://secure link waiting for the user to confirm it in Settings → Sites.</summary>
    public SecureLink? PendingSecure { get; set; }

    /// <summary>The app is replacing the installed CLI with its bundled one.</summary>
    public bool IsUpdating { get; private set; }

    /// <summary>"Running · v1.2", "Needs attention · v1.2", "Not running" (or "Not set up", "Updating…").</summary>
    public string Headline => IsUpdating ? "Updating…"
        : Status is { } s ? $"{(s.IsDegraded ? "Needs attention" : "Running")} · v{s.Version}"
        : NeedsSetup ? "Not set up" : "Not running";

    /// <summary>
    /// Runs the bundled `flock install` after the app was updated. It restarts the daemon; Windows
    /// asks for permission only if the hosts helper changed.
    /// </summary>
    public async Task UpdateInstallationAsync()
    {
        IsUpdating = true;
        Changed?.Invoke();
        var result = await Installation.InstallAsync();
        IsUpdating = false;
        if (result.Ok)
        {
            Installation.RecordInstalled();
        }
        else
        {
            App.Log(new InvalidOperationException("flock install (update) failed:\n" + result.Output));
            Native.ShowError("Flock could not update", $"{result.Output}\n\nYou can run `flock install` in a terminal, or reinstall Flock.");
        }
        Changed?.Invoke();
        await RefreshAsync();
    }

    public async void StartPolling()
    {
        while (true)
        {
            await RefreshAsync();
            await Task.Delay(TimeSpan.FromSeconds(2));
        }
    }

    public async Task RefreshAsync()
    {
        try
        {
            Apply(await Client.StatusAsync());
        }
        catch (FlockException e)
        {
            var changed = Status is not null || Error != e.Message;
            Status = null;
            Error = e.Message;
            if (changed)
            {
                Changed?.Invoke();
            }
        }
    }

    private void Apply(Status fresh, bool force = false)
    {
        var previous = Status;
        var changed = force || !Json.Same(previous, fresh) || Error is not null;
        Status = fresh;
        Error = null;
        _ = Settings.RefreshIfVisibleAsync();

        // Lists in open windows follow the counters in the status.
        if (previous?.MailCount != fresh.MailCount)
        {
            _ = Mail.RefreshIfVisibleAsync();
        }
        if (previous?.DumpCount != fresh.DumpCount)
        {
            _ = Dumps.RefreshIfVisibleAsync();
        }
        if (changed)
        {
            Changed?.Invoke();
        }
    }

    public Task SetXdebugAsync(bool on) => UpdateAsync(new SettingsChange(Xdebug: on));

    public Task SetXdebugBreakpointsAsync(bool on) => UpdateAsync(new SettingsChange(XdebugBreakpoints: on));

    public Task SetDumpsAsync(bool on) => UpdateAsync(new SettingsChange(Dumps: on));

    private async Task UpdateAsync(SettingsChange change)
    {
        try
        {
            var fresh = await Client.UpdateSettingsAsync(change);
            ActionError = null;
            Apply(fresh, force: true);
        }
        catch (FlockException e)
        {
            ActionError = e.Message;
            Changed?.Invoke();
        }
    }

    public void DismissError()
    {
        if (ActionError is not null)
        {
            ActionError = null;
            Changed?.Invoke();
        }
    }

    /// <summary>Runs `flock restart`, which also starts a stopped daemon through its scheduled task.</summary>
    public async Task RestartAsync()
    {
        IsRestarting = true;
        Changed?.Invoke();
        try
        {
            var binary = FlockPaths.FlockBinary;
            ActionError = await Task.Run(() => RunFlock(binary, "restart"));
        }
        finally
        {
            IsRestarting = false;
            Changed?.Invoke();
        }
        await RefreshAsync();
    }

    /// <summary>Runs the flock CLI without a console window; returns its output when it fails.</summary>
    private static string? RunFlock(string binary, params string[] args)
    {
        if (!File.Exists(binary))
        {
            return $"Flock is not installed for this user ({binary} is missing) — run `flock install` once.";
        }
        var start = new ProcessStartInfo(binary)
        {
            CreateNoWindow = true,
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var arg in args)
        {
            start.ArgumentList.Add(arg);
        }
        try
        {
            using var process = Process.Start(start)!;
            var stdout = process.StandardOutput.ReadToEndAsync();
            var stderr = process.StandardError.ReadToEnd();
            process.WaitForExit();
            return process.ExitCode == 0 ? null : (stdout.Result + stderr).Trim();
        }
        catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            return $"Could not run {binary}: {e.Message}";
        }
    }

    public static void Open(Status.Site site)
    {
        if (site.Url is { Length: > 0 } url)
        {
            Native.Open(url);
        }
    }
}

/// <summary>Compares API values by their JSON, so unchanged data doesn't rebuild lists mid-interaction.</summary>
public static class Json
{
    public static bool Same<T>(T? a, T? b) =>
        JsonSerializer.Serialize(a, FlockJson.Options) == JsonSerializer.Serialize(b, FlockJson.Options);
}

/// <summary>Base of the stores: a Changed event and the last error.</summary>
public abstract class Store
{
    public string? Error { get; set; }

    public event Action? Changed;

    protected void Notify() => Changed?.Invoke();

    protected async Task Guard(Func<Task> action)
    {
        try
        {
            await action();
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
    }
}

/// <summary>Caught mail for the Mail window.</summary>
public sealed class MailStore(FlockClient client) : Store
{
    public List<MailSummary> Messages { get; private set; } = [];
    public bool IsVisible { get; set; }

    public Task RefreshAsync() => Guard(async () =>
    {
        var fresh = await client.MailAsync();
        if (!Json.Same(fresh, Messages))
        {
            Messages = fresh;
        }
    });

    public async Task RefreshIfVisibleAsync()
    {
        if (IsVisible)
        {
            await RefreshAsync();
        }
    }

    public Task<MailDetail> DetailAsync(string id) => client.MailAsync(id);

    public Task<string> RawAsync(string id) => client.RawMailAsync(id);

    public Task<byte[]> AttachmentAsync(string id, int index) => client.AttachmentAsync(id, index);

    public Task ClearAsync() => Guard(async () =>
    {
        await client.ClearMailAsync();
        Messages = [];
    });
}

/// <summary>Captured dump()/dd() output for the Dumps window.</summary>
public sealed class DumpStore(FlockClient client) : Store
{
    public List<Dump> Dumps { get; private set; } = [];
    public string Header { get; private set; } = "";
    public bool IsVisible { get; set; }

    public Task RefreshAsync() => Guard(async () =>
    {
        var fresh = await client.DumpsAsync();
        // The header only exists once the dump server has run; fetch it when first needed.
        if (Header.Length == 0 && fresh.Count > 0)
        {
            try
            {
                Header = await client.DumpHeaderAsync();
            }
            catch (FlockException)
            {
            }
        }
        if (!Json.Same(fresh, Dumps))
        {
            Dumps = fresh;
        }
    });

    public async Task RefreshIfVisibleAsync()
    {
        if (IsVisible)
        {
            await RefreshAsync();
        }
    }

    public Task ClearAsync() => Guard(async () =>
    {
        await client.ClearDumpsAsync();
        Dumps = [];
    });
}

/// <summary>Site (Laravel) and service logs for the Logs window.</summary>
public sealed class LogStore(FlockClient client) : Store
{
    public abstract record Source(string Name);
    public sealed record SiteSource(string Name) : Source(Name);
    public sealed record ServiceSource(string Name) : Source(Name);

    public LogIndex Index { get; private set; } = new();
    public List<LogEntry> Entries { get; private set; } = [];
    public string ServiceText { get; private set; } = "";

    public Task LoadIndexAsync() => Guard(async () => Index = await client.LogsAsync());

    public Task LoadAsync(Source source) => Guard(async () =>
    {
        switch (source)
        {
            case SiteSource site:
                // Unchanged entries keep their list, so an auto-refresh doesn't rebuild the view.
                var fresh = await client.SiteLogAsync(site.Name);
                if (!Json.Same(fresh, Entries))
                {
                    Entries = fresh;
                }
                break;
            case ServiceSource service:
                ServiceText = await client.ServiceLogAsync(service.Name);
                break;
        }
    });

    public Task ClearAsync(string site) => Guard(async () =>
    {
        await client.ClearSiteLogAsync(site);
        Entries = [];
    });
}

/// <summary>Configuration, services and PHP versions for the Settings window.</summary>
public sealed class SettingsStore(FlockClient client) : Store
{
    /// <summary>Lets the tray status catch up after a change.</summary>
    public Func<Task>? OnChange { get; init; }

    public FlockConfig? Config { get; private set; }
    public List<Service> Services { get; private set; } = [];
    /// <summary>PHP versions with install state; loaded by the PHP pane.</summary>
    public PhpCatalog? Php { get; private set; }
    public bool IsBusy { get; private set; }
    public bool IsVisible { get; set; }

    public FlockClient Client => client;

    public async Task RefreshAsync()
    {
        try
        {
            var config = client.ConfigAsync();
            var services = client.ServicesAsync();
            var changed = false;
            if (!Json.Same(await config, Config))
            {
                Config = config.Result;
                changed = true;
            }
            if (!Json.Same(await services, Services))
            {
                Services = services.Result;
                changed = true;
                // A service changed (installed, started, another version): the Services pane's details follow.
                if (WatchesServices)
                {
                    _ = RefreshServiceVersionsAsync();
                }
            }
            if (changed || Error is not null)
            {
                Error = null;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    public async Task RefreshIfVisibleAsync()
    {
        if (IsVisible)
        {
            await RefreshAsync();
        }
    }

    /// <summary>Runs a change against the daemon, then reloads everything that may depend on it.</summary>
    public async Task PerformAsync(Func<FlockClient, Task> change)
    {
        await Busy(async () =>
        {
            try
            {
                await change(client);
                Error = null;
            }
            catch (FlockException e)
            {
                Error = e.Message;
            }
        });
        await RefreshAsync();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>Changes services and adopts the list the daemon returns.</summary>
    public async Task PerformOnServicesAsync(Func<FlockClient, Task<List<Service>>> change)
    {
        await Busy(async () =>
        {
            try
            {
                Services = await change(client);
                Error = null;
            }
            catch (FlockException e)
            {
                Error = e.Message;
            }
        });
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>Reloads the PHP catalog (not part of Refresh; only the PHP pane needs it).</summary>
    public async Task RefreshPhpAsync()
    {
        try
        {
            var fresh = await client.PhpVersionsAsync();
            if (!Json.Same(fresh, Php))
            {
                Php = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>Runs a PHP action and adopts the catalog it returns; installs also change config and status.</summary>
    public async Task PerformOnPhpAsync(Func<FlockClient, Task<PhpCatalog>> change)
    {
        await Busy(async () =>
        {
            try
            {
                Php = await change(client);
                Error = null;
            }
            catch (FlockException e)
            {
                Error = e.Message;
            }
        });
        await RefreshAsync();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>FrankenPHP builds per PHP minor; loaded by the PHP pane. Null when the daemon doesn't offer FrankenPHP.</summary>
    public FrankenPhpState? FrankenPhp { get; private set; }

    public async Task RefreshFrankenPhpAsync()
    {
        try
        {
            var fresh = await client.FrankenPhpAsync();
            if (!Json.Same(fresh, FrankenPhp))
            {
                FrankenPhp = fresh;
                Notify();
            }
        }
        catch (FlockServerException e) when (e.StatusCode is 404 or 501)
        {
            // An older daemon, or a platform without FrankenPHP: the PHP pane leaves the section out.
            if (FrankenPhp is not null)
            {
                FrankenPhp = null;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>Runs a FrankenPHP action (installs return at once and continue as jobs) and adopts the state it returns.</summary>
    public async Task PerformOnFrankenPhpAsync(Func<FlockClient, Task<FrankenPhpState>> change)
    {
        try
        {
            FrankenPhp = await change(client);
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>The AI clients Flock's MCP server can be registered with; loaded by the AI pane.</summary>
    public McpState? Mcp { get; private set; }

    public async Task RefreshMcpAsync()
    {
        try
        {
            var fresh = await client.McpAsync();
            if (!Json.Same(fresh, Mcp))
            {
                Mcp = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>Registers or removes Flock with an AI client and adopts the list the daemon returns.</summary>
    public async Task PerformOnMcpAsync(Func<FlockClient, Task<McpState>> change)
    {
        var failed = false;
        await Busy(async () =>
        {
            try
            {
                Mcp = await change(client);
                Error = null;
            }
            catch (FlockException e)
            {
                Error = e.Message;
                failed = true;
            }
        });
        if (failed)
        {
            await RefreshMcpAsync();
        }
    }

    /// <summary>Node.js lines with install state; loaded by the Node pane.</summary>
    public NodeCatalog? Node { get; private set; }

    public async Task RefreshNodeAsync()
    {
        try
        {
            var fresh = await client.NodeVersionsAsync();
            if (!Json.Same(fresh, Node))
            {
                Node = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>Runs a Node.js action and adopts the catalog it returns.</summary>
    public async Task PerformOnNodeAsync(Func<FlockClient, Task<NodeCatalog>> change)
    {
        try
        {
            Node = await change(client);
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
        await RefreshAsync();
    }

    /// <summary>PostgreSQL majors with install state; loaded by the Services pane.</summary>
    public PostgresVersions? Postgres { get; private set; }
    /// <summary>The PostgreSQL command-line tools with their install job; loaded by the Services pane.</summary>
    public ComponentCatalog? Components { get; private set; }
    /// <summary>MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS; loaded by the Services pane.</summary>
    public ServiceCatalog? Catalog { get; private set; }
    /// <summary>The Services pane is shown, so service changes also reload Postgres and Components.</summary>
    public bool WatchesServices { get; set; }

    public Task RefreshServiceVersionsAsync() => Task.WhenAll(RefreshPostgresAsync(), RefreshComponentsAsync(), RefreshCatalogAsync());

    public async Task RefreshCatalogAsync()
    {
        try
        {
            var fresh = await client.CatalogAsync();
            if (!Json.Same(fresh, Catalog))
            {
                Catalog = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>Runs a catalog action (they return at once; installs continue as jobs) and adopts the catalog.</summary>
    public async Task PerformOnCatalogAsync(Func<FlockClient, Task<ServiceCatalog>> change)
    {
        try
        {
            Catalog = await change(client);
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
        await RefreshAsync();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>The tail of a catalog install's output; empty when there is none (yet).</summary>
    public async Task<string> CatalogLogAsync(string id)
    {
        try
        {
            return await client.CatalogLogAsync(id);
        }
        catch (FlockException)
        {
            return "";
        }
    }

    public async Task RefreshPostgresAsync()
    {
        try
        {
            var fresh = await client.PostgresVersionsAsync();
            if (!Json.Same(fresh, Postgres))
            {
                Postgres = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    public async Task RefreshComponentsAsync()
    {
        try
        {
            var fresh = await client.ComponentsAsync();
            if (!Json.Same(fresh, Components))
            {
                Components = fresh;
                Notify();
            }
        }
        catch (FlockException e)
        {
            Error = e.Message;
            Notify();
        }
    }

    /// <summary>
    /// Runs a PostgreSQL action and adopts the versions it returns, then lets the service list and
    /// the tray catch up. Not Busy(): an install takes minutes and shouldn't freeze other services.
    /// </summary>
    public async Task PerformOnPostgresAsync(Func<FlockClient, Task<PostgresVersions>> change)
    {
        try
        {
            Postgres = await change(client);
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
        await RefreshAsync();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>Runs a component action (they return at once; installs continue as jobs) and adopts the catalog.</summary>
    public async Task PerformOnComponentsAsync(Func<FlockClient, Task<ComponentCatalog>> change)
    {
        try
        {
            Components = await change(client);
            Error = null;
        }
        catch (FlockException e)
        {
            Error = e.Message;
        }
        Notify();
        await RefreshAsync();
        if (OnChange is not null)
        {
            await OnChange();
        }
    }

    /// <summary>The tail of a component job's output; empty when there is none (yet).</summary>
    public async Task<string> ComponentLogAsync(string id)
    {
        try
        {
            return await client.ComponentLogAsync(id);
        }
        catch (FlockException)
        {
            return "";
        }
    }

    public async Task<string> PhpInstallLogAsync(string minor)
    {
        try
        {
            return await client.PhpInstallLogAsync(minor);
        }
        catch (FlockException)
        {
            return "";
        }
    }

    /// <summary>The PHP extensions a site needs, or why the daemon couldn't tell.</summary>
    public async Task<(ExtensionReport? Report, string? Error)> SiteExtensionsAsync(string site)
    {
        try
        {
            return (await client.SiteExtensionsAsync(site), null);
        }
        catch (FlockException e)
        {
            return (null, e.Message);
        }
    }

    /// <summary>Adds the extension to a site's composer.json, then refreshes the sites (a problem may be gone).</summary>
    public async Task<(ExtensionReport? Report, string? Error)> RequireExtensionAsync(string site, string name)
    {
        (ExtensionReport?, string?) result;
        try
        {
            result = (await client.RequireExtensionAsync(site, name), null);
        }
        catch (FlockException e)
        {
            result = (null, e.Message);
        }
        if (OnChange is not null)
        {
            await OnChange();
        }
        return result;
    }

    public void DismissError()
    {
        Error = null;
        Notify();
    }

    public void ShowError(string message)
    {
        Error = message;
        Notify();
    }

    private async Task Busy(Func<Task> work)
    {
        IsBusy = true;
        Notify();
        try
        {
            await work();
        }
        finally
        {
            IsBusy = false;
            Notify();
        }
    }
}

/// <summary>Per-user app preferences (the macOS app's @AppStorage), in %LocalAppData%\Flock\tray.json.</summary>
public sealed class Preferences
{
    public string Editor { get; set; } = nameof(Kit.Editor.PhpStorm);
    public bool DumpsLatestOnly { get; set; } = true;
    /// <summary>SHA-256 of the flock.exe this app last installed (see Installation.NeedsUpdate).</summary>
    public string InstalledFlock { get; set; } = "";

    private static string FilePath => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Flock", "tray.json");

    public static Preferences Shared { get; } = Load();

    public Editor EditorChoice
    {
        get => EditorInfo.Parse(Editor);
        set
        {
            Editor = value.ToString();
            Save();
        }
    }

    public event Action? Changed;

    private static Preferences Load()
    {
        try
        {
            return JsonSerializer.Deserialize<Preferences>(File.ReadAllText(FilePath)) ?? new Preferences();
        }
        catch (Exception e) when (e is IOException or JsonException or UnauthorizedAccessException)
        {
            return new Preferences();
        }
    }

    public void Save()
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
            File.WriteAllText(FilePath, JsonSerializer.Serialize(this));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
        }
        Changed?.Invoke();
    }
}
