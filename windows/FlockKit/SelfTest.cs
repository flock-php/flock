namespace Flock.Kit;

/// <summary>Exercises selected daemon endpoints used by the app and reports problems.</summary>
public static class SelfTest
{
    public static async Task<int> RunAsync(FlockClient client, TextWriter output)
    {
        var failures = 0;

        async Task Check(string name, Func<Task<string>> body)
        {
            try
            {
                output.WriteLine($"OK    {name}: {await body()}");
            }
            catch (Exception e)
            {
                failures++;
                output.WriteLine($"FAIL  {name}: {e.Message}");
            }
        }

        await Check("GET /v1/status", async () =>
        {
            var s = await client.StatusAsync();
            return $"v{s.Version}, {s.Sites.Count} sites, {s.Processes.Count} processes, mail {s.MailCount}, dumps {s.DumpCount}, tld {s.Dns.Tld}";
        });
        await Check("POST /v1/settings (no-op)", async () =>
        {
            var s = await client.StatusAsync();
            var after = await client.UpdateSettingsAsync(new SettingsChange(Xdebug: s.Xdebug, Dumps: s.Dumps));
            return $"xdebug {after.Xdebug}, dumps {after.Dumps}";
        });
        await Check("GET /v1/mail", async () =>
        {
            var list = await client.MailAsync();
            if (list.Count == 0)
            {
                return "0 messages";
            }
            var detail = await client.MailAsync(list[0].Id);
            var raw = await client.RawMailAsync(list[0].Id);
            var result = $"{list.Count} messages; first: html {detail.Html.Length}B, text {detail.Text.Length}B, {detail.Attachments.Count} attachments, raw {raw.Length}B";
            if (detail.Attachments.Count > 0)
            {
                var data = await client.AttachmentAsync(list[0].Id, detail.Attachments[0].Index);
                result += $", attachment {data.Length}B";
            }
            return result;
        });
        await Check("GET /v1/dumps", async () =>
        {
            var dumps = await client.DumpsAsync();
            return dumps.Count == 0 ? "0 dumps" : $"{dumps.Count} dumps; newest from {dumps[0].ShortSource ?? "unknown source"} via {dumps[0].Origin}";
        });
        await Check("GET /v1/logs", async () =>
        {
            var index = await client.LogsAsync();
            var result = $"services [{string.Join(", ", index.Services)}], sites {index.Sites.Count}";
            if (index.Services.Count > 0)
            {
                result += $"; {index.Services[0]}: {(await client.ServiceLogAsync(index.Services[0], 5)).Length}B";
            }
            if (index.Sites.Count > 0)
            {
                result += $"; {index.Sites[0]}: {(await client.SiteLogAsync(index.Sites[0], 20)).Count} entries";
            }
            return result;
        });
        await Check("GET /v1/config", async () =>
        {
            var c = await client.ConfigAsync();
            return $"tld {c.Tld}, php [{string.Join(", ", c.Php.Select(p => p.FullVersion))}] (default {c.DefaultPhp}), node [{string.Join(", ", c.Node)}] (default {c.DefaultNode}), parked {c.Parked.Count}, smtp :{c.Ports.Smtp}";
        });
        await Check("GET /v1/services", async () =>
        {
            var services = await client.ServicesAsync();
            return string.Join(" ", services.Select(s => $"{s.Id}[{s.Category}]{(s.Running ? "↑" : "↓")}{(s.Port > 0 ? $":{s.Port}" : "")}"));
        });
        await Check("GET /v1/php/versions", async () =>
        {
            var catalog = await client.PhpVersionsAsync();
            var rows = catalog.Versions.Select(v => $"{v.Minor}:{v.Status}{(v.Job is null ? "" : $"[{v.Job.State}]")}");
            return $"checked {catalog.CheckedAt?.ToString() ?? "never"}, {string.Join(" ", rows)}";
        });
        await Check("GET /v1/frankenphp", async () =>
        {
            var state = await client.FrankenPhpAsync();
            var rows = state.Builds.Select(b => $"{b.Php}:{(b.IsInstalled ? b.Installed : "-")}{(b.Job is null ? "" : $"[{b.Job.State}]")}");
            return $"available {state.Available}, {string.Join(" ", rows)}";
        });

        output.WriteLine(failures == 0 ? "selftest passed" : $"selftest: {failures} failure(s)");
        return failures == 0 ? 0 : 1;
    }
}