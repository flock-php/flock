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
    public string? Error { get; private set; }
    public bool IsRestarting { get; private set; }

    public MailStore Mail { get; }
    public DumpStore Dumps { get; }
    public LogStore Logs { get; }
    public SettingsStore Settings { get; }

    /// <summary>Raised whenever Status, Error or IsRestarting change.</summary>
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

    /// <summary>"Running · v1.2", "Needs attention · v1.2" or "Not running".</summary>
    public string Headline => Status is { } s ? $"{(s.IsDegraded ? "Needs attention" : "Running")} · v{s.Version}" : "Not running";

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

    private void Apply(Status fresh)
    {
        var previous = Status;
        var changed = !Json.Same(previous, fresh) || Error is not null;
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

    public Task SetDumpsAsync(bool on) => UpdateAsync(new SettingsChange(Dumps: on));

    private async Task UpdateAsync(SettingsChange change)
    {
        try
        {
            Apply(await Client.UpdateSettingsAsync(change));
        }
        catch (FlockException e)
        {
            Error = e.Message;
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
            var failure = await Task.Run(() => RunFlock(binary, "restart"));
            if (failure is not null)
            {
                Error = failure;
            }
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
                Entries = await client.SiteLogAsync(site.Name);
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

    public async Task<string> PhpBuildLogAsync(string minor)
    {
        try
        {
            return await client.PhpBuildLogAsync(minor);
        }
        catch (FlockException)
        {
            return "";
        }
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
