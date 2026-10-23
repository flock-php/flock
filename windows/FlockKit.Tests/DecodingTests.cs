namespace Flock.Kit.Tests;

public class DecodingTests
{
    [Fact]
    public void ParsesGoTimesWithAndWithoutFractions()
    {
        var whole = FlockJson.ParseDate("2026-10-13T13:40:07+02:00")!.Value;
        var nanos = FlockJson.ParseDate("2026-10-13T13:40:07.740103123+02:00")!.Value;
        var utc = FlockJson.ParseDate("2026-10-13T11:40:07.5Z")!.Value;

        Assert.True(Math.Abs((nanos - whole).TotalSeconds - 0.740103123) < 0.000001);
        Assert.Equal(0.5, (utc - whole).TotalSeconds);
        Assert.Null(FlockJson.ParseDate("yesterday"));
    }

    [Fact]
    public void DecodesAFullStatus()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"0893ae2","channel":"beta","pid":91994,"started_at":"2026-10-13T14:10:01.123456+02:00",
             "dns":{"tld":"test","addr":"127.0.0.1:53535"},"http_port":80,"https_port":443,
             "mail":"127.0.0.1:2525","postgres":"18.6 on 127.0.0.1:5432",
             "extras":[{"id":"redis","name":"Redis","version":"7.2.16","port":6379,"enabled":true,"up":true},{"id":"mysql"}],
             "xdebug":true,"xdebug_breakpoints":true,"dumps":false,"mail_count":3,"dump_count":12,
             "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":true,"parked":false,"url":"https://watchtower.test","debugging":true},
                      {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"7.4","secured":false,"parked":true,"problem":"PHP 7.4 is not installed"}],
             "processes":[{"name":"php8.5-fpm","up":true,"restarts":0},{"name":"postgres","up":false,"restarts":3,"last_error":"exit status 1: could not bind"}],
             "warnings":["postgres: port 5432 is used by postgres (pid 1)"]}
            """);

        Assert.Equal(443, status.HttpsPort);
        Assert.Equal("beta", status.Channel);
        Assert.Null(status.Valkey);
        Assert.Equal(["redis", "mysql"], status.Extras.Select(e => e.Id));
        Assert.True(status.Extras[0].Up);
        Assert.Equal(6379, status.Extras[0].Port);
        Assert.Equal("mysql", status.Extras[1].Name);
        Assert.True(status.Extras[1].Enabled);
        Assert.False(status.Extras[1].Up);
        Assert.Equal(3, status.MailCount);
        Assert.Equal(12, status.DumpCount);
        Assert.Equal("https://watchtower.test", status.Sites[0].Url);
        Assert.True(status.XdebugBreakpoints);
        Assert.True(status.Sites[0].Debugging);
        Assert.False(status.Sites[1].Debugging);
        Assert.Null(status.Sites[1].Url);
        Assert.NotNull(status.Sites[1].Problem);
        Assert.Equal("exit status 1: could not bind", status.Processes[1].LastError);
        Assert.True(status.IsDegraded);
    }

    [Fact]
    public void DecodesWhereASiteVersionComesFrom()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","secured":false,"parked":false,
                       "php":"8.3","php_source":"isolated","php_pinned":"8.4","php_file":"C:\\code\\shop\\.flockrc",
                       "node":"24","node_source":"project","node_pinned":"24","node_file":"C:\\code\\shop\\.flockrc","node_shadowed":".nvmrc: 20"},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.5","secured":false,"parked":true}]}
            """);

        // Daemons before the channel was in the status: stable.
        Assert.Equal("stable", status.Channel);

        var shop = status.Sites[0];
        Assert.Equal((Status.VersionSource.Isolated, "8.4"), (shop.PhpSource, shop.PhpPinned));
        Assert.True(shop.PhpPinnedHere);
        Assert.Equal((Status.VersionSource.Project, ".nvmrc: 20"), (shop.NodeSource, shop.NodeShadowed));
        Assert.Equal(Status.VersionSource.Default, status.Sites[1].PhpSource);
        Assert.False(status.Sites[1].PhpPinnedHere);
    }

    [Fact]
    public void DecodesTheDocumentRoot()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
             "sites":[{"name":"zoo","domain":"zoo.test","path":"C:\\code\\zoo","php":"8.5","root":".","root_source":"default",
                       "root_pinned":"","root_file":"","root_state":"not_found","root_suggestions":["src/www"],"notice":"No document root found"},
                      {"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.5","root":"web","root_source":"isolated",
                       "root_pinned":"public","root_file":"C:\\code\\shop\\.flockrc"},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.5"},
                      {"name":"cms","domain":"cms.test","path":"C:\\code\\cms","php":"8.5","root":"src/www","root_source":"isolated","root_state":"empty"}]}
            """);

        var zoo = status.Sites[0];
        Assert.Equal((Status.DocumentRootState.NotFound, "No document root found"), (zoo.RootState, zoo.Notice));
        Assert.Equal(["src/www"], zoo.RootSuggestions);
        Assert.False(zoo.RootPinnedHere);
        var shop = status.Sites[1];
        Assert.Equal(("web", Status.VersionSource.Isolated, "public", ""), (shop.Root, shop.RootSource, shop.RootPinned, shop.RootState));
        Assert.True(shop.RootPinnedHere);
        // A daemon from before document roots: the project folder, detected.
        var old = status.Sites[2];
        Assert.Equal((".", Status.VersionSource.Default, "", null), (old.Root, old.RootSource, old.RootState, old.Notice));
        Assert.Empty(old.RootSuggestions);
        Assert.Equal((Status.DocumentRootState.Empty, null), (status.Sites[3].RootState, status.Sites[3].Notice));
    }

    [Theory]
    [InlineData(@"C:\code\zoo\src\www", "src/www")]
    [InlineData(@"c:\Code\Zoo\src\www\", "src/www")]
    [InlineData(@"C:\code\zoo", ".")]
    [InlineData(@"C:\code\zoo-2\www", null)]
    [InlineData(@"C:\code", null)]
    public void TurnsAChosenFolderIntoADocumentRoot(string folder, string? root)
    {
        var site = new Status.Site { Path = @"C:\code\zoo" };
        Assert.Equal(root, site.RootFor(folder));
    }

    [Fact]
    public void DecodesExtraDomainsAndTheWildcardState()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "dns":{"tld":"test","addr":"127.0.0.1:53","wildcard":false,"wildcard_problem":"your organization manages DNS rules (Group Policy), so Windows ignores Flock's rule for .test","max_depth":4},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"app","domain":"app.test","path":"C:\\code\\app","php":"8.5","secured":true,"parked":false,"url":"https://app.test",
                       "domains":[{"entry":"tenant1","host":"tenant1.app.test","alias":false,"url":"https://tenant1.app.test"},
                                  {"entry":"promo.test","host":"promo.test","alias":true,"problem":"promo.test is already served by promo"}]},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.5","secured":false,"parked":true,"domains":null}]}
            """);

        Assert.False(status.Dns.Wildcard);
        Assert.Contains("Group Policy", status.Dns.WildcardProblem);
        Assert.Equal(4, status.Dns.MaxDepth);
        var app = status.Sites[0];
        Assert.Equal(("tenant1", "tenant1.app.test", false), (app.Domains[0].Entry, app.Domains[0].Host, app.Domains[0].Alias));
        Assert.Equal("https://tenant1.app.test", app.Domains[0].Url);
        Assert.True(app.Domains[1].Alias);
        Assert.Null(app.Domains[1].Url);
        Assert.NotNull(app.Domains[1].Problem);
        Assert.Empty(status.Sites[1].Domains);
    }

    [Fact]
    public void DecodesWhatRunsASite()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.4","secured":false,"parked":false,
                       "server":"frankenphp","worker":"octane","octane_available":true,
                       "frankenphp":{"state":"running","version":"1.13.0","php_version":"8.4.14","port":21000,"progress":0,"log":"frankenphp-shop"}},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.4","secured":false,"parked":false,
                       "server":"frankenphp","worker":"","octane_available":false,
                       "frankenphp":{"state":"installing","port":21001,"progress":-1,"problem":"FrankenPHP for PHP 8.4 is downloading","log":"frankenphp-blog"}},
                      {"name":"legacy","domain":"legacy.test","path":"C:\\code\\legacy","php":"8.1","secured":false,"parked":true,"server":"fastcgi","worker":"","octane_available":false},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.3","secured":false,"parked":true}]}
            """);

        var shop = status.Sites[0];
        Assert.True(shop.IsFrankenPhp);
        Assert.Equal((Status.SiteServer.Octane, true), (shop.Worker, shop.OctaneAvailable));
        Assert.True(shop.FrankenPhp!.IsRunning);
        Assert.Equal(("1.13.0", "8.4.14", 21000, "frankenphp-shop"), (shop.FrankenPhp.Version, shop.FrankenPhp.PhpVersion, shop.FrankenPhp.Port, shop.FrankenPhp.Log));
        var blog = status.Sites[1].FrankenPhp!;
        Assert.True(blog.IsInstalling);
        Assert.False(blog.HasProgress);
        Assert.Empty(blog.Version);
        Assert.NotEmpty(blog.Problem);
        Assert.False(status.Sites[2].IsFrankenPhp);
        Assert.Null(status.Sites[2].FrankenPhp);
        // An older daemon sends none of these fields.
        Assert.Equal((Status.SiteServer.FastCgi, "", false), (status.Sites[3].Server, status.Sites[3].Worker, status.Sites[3].OctaneAvailable));
        Assert.Null(status.Sites[3].FrankenPhp);
    }

    [Fact]
    public void DecodesASitesShare()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-19T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.4","secured":true,"parked":false,
                       "share":{"site":"shop","state":"running","url":"https://quiet-river-lamp.trycloudflare.com","allow":["*@acme.com"],
                                "started_at":"2026-10-19T14:10:01Z","expires_at":"2026-10-19T22:10:05Z","built":true,"can_rebuild":true,
                                "rebuilding":false,"log":"share-shop","qr":"iVBORw0KGgo="}},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.4","secured":false,"parked":false,
                       "share":{"site":"blog","state":"failed","allow":null,"started_at":"2026-10-19T14:10:01Z","built":false,
                                "can_rebuild":false,"rebuilding":false,"error":"cloudflared exited","log":"share-blog"}},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.3","secured":false,"parked":true}]}
            """);

        var shop = status.Sites[0];
        Assert.True(shop.IsShared);
        Assert.Equal(("https://quiet-river-lamp.trycloudflare.com", "share-shop"), (shop.Share!.Url, shop.Share.Log));
        Assert.Equal(["*@acme.com"], shop.Share.Allow);
        Assert.Equal(new DateTimeOffset(2026, 10, 19, 22, 10, 5, TimeSpan.Zero), shop.Share.ExpiresAt);
        Assert.True(shop.Share.CanRebuild);
        Assert.Equal("iVBORw0KGgo=", shop.Share.Qr);
        var blog = status.Sites[1];
        Assert.False(blog.IsShared);
        Assert.True(blog.Share!.IsFailed);
        Assert.Empty(blog.Share.Allow);
        Assert.Null(blog.Share.ExpiresAt);
        Assert.Equal("cloudflared exited", blog.Share.Error);
        Assert.Null(status.Sites[2].Share);
    }

    [Fact]
    public void DecodesTheFrankenPhpBuilds()
    {
        var state = FlockJson.Decode<FrankenPhpState>("""
            {"available":true,"checked_at":"2026-10-13T14:10:01.5+02:00","check_error":"",
             "builds":[{"php":"8.4","installed":"1.13.0","php_version":"8.4.14","latest":"8.4.26","update":true,"size":41943040,"prerelease":false,"yanked":"crashes on start","job":null},
                       {"php":"8.3","installed":"","php_version":"","latest":"8.3.36RC1","update":false,"size":40000000,"prerelease":true,
                        "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"2026-10-13T14:11:00Z"}},
                       {"php":"8.2","installed":"","php_version":"","latest":"","update":false,"size":0,
                        "job":{"action":"install","state":"failed","phase":"downloading","progress":-1,"error":"checksum mismatch","started_at":"2026-10-13T14:11:00Z"}}]}
            """);

        Assert.True(state.Available);
        Assert.NotNull(state.CheckedAt);
        Assert.True(state.HasRunningJob);
        Assert.True(state.Builds[0].IsInstalled);
        Assert.Equal(("8.4.26", true, 41943040L), (state.Builds[0].Latest, state.Builds[0].Update, state.Builds[0].Size));
        Assert.Equal((false, "crashes on start"), (state.Builds[0].Prerelease, state.Builds[0].Yanked));
        Assert.Equal((true, ""), (state.Builds[1].Prerelease, state.Builds[1].Yanked));
        Assert.Null(state.Builds[0].Job);
        Assert.Equal((0.42, "Downloading…"), (state.Builds[1].Job!.Progress, state.Builds[1].Job!.PhaseText));
        Assert.True(state.Builds[2].Job!.IsFailed);
        Assert.Equal("checksum mismatch", state.Builds[2].Job!.Error);

        var unavailable = FlockJson.Decode<FrankenPhpState>("""{"available":false,"checked_at":null,"check_error":"offline","builds":null}""");
        Assert.False(unavailable.Available);
        Assert.Null(unavailable.CheckedAt);
        Assert.Empty(unavailable.Builds);
        Assert.False(unavailable.HasRunningJob);
    }

    [Fact]
    public void DecodesAMinimalStatusWithOmittedFields()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","xdebug":false,"dumps":false,
             "mail_count":0,"dump_count":0,"sites":null,"processes":[]}
            """);

        Assert.Null(status.Postgres);
        Assert.Null(status.Valkey);
        Assert.Empty(status.Extras);
        Assert.Empty(status.Sites);
        Assert.Empty(status.Warnings);
        Assert.False(status.IsDegraded);
        // Both defaults are set.
        Assert.Null(status.Defaults);
        // A daemon from before runtime capture: everything captured, nothing unsupported.
        Assert.Empty(status.CaptureOff);
        Assert.Empty(status.CaptureUnsupported);
        Assert.False(status.DumpsInPage);
        Assert.Equal(0, status.EventCount);
    }

    [Fact]
    public void DecodesTheCaptureSettings()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","dumps":true,"capture_off":["view","job"],"dumps_in_page":true,"event_count":42,
             "capture_unsupported":["8.1"],"sites":[],"processes":[]}
            """);

        Assert.Equal(["view", "job"], status.CaptureOff);
        Assert.True(status.DumpsInPage);
        Assert.Equal(42, status.EventCount);
        Assert.Equal(["8.1"], status.CaptureUnsupported);

        var config = FlockJson.Decode<FlockConfig>("""{"tld":"test","dumps":true,"capture_off":null,"dumps_in_page":true}""");
        Assert.Empty(config.CaptureOff);
        Assert.True(config.DumpsInPage);
    }

    [Fact]
    public void DecodesEveryEventKindAndIgnoresUnknownOnes()
    {
        var events = FlockJson.Decode<List<RuntimeEvent>>("""
            [{"id":"6","kind":"request","time":"2026-10-18T09:40:07.9Z","site":"shop","group":"g1",
              "request":{"method":"GET","uri":"https://shop.test/spike"},"status_code":200,"duration_ms":31.2},
             {"id":"5","kind":"job","time":"2026-10-18T09:40:07.8Z","group":"g2","command_line":"artisan queue:work",
              "job":"App\\Jobs\\SpikeJob","queue":"default","connection":"database","state":"failed","exception":"RuntimeException: boom","duration_ms":5.5},
             {"id":"4","kind":"view","time":"2026-10-18T09:40:07.7Z","group":"g1","view":"spike","path":"/code/shop/resources/views/spike.blade.php"},
             {"id":"3","kind":"log","time":"2026-10-18T09:40:07.6Z","group":"g1","level":"warning","message":"spike request","context":{"count":0}},
             {"id":"2","kind":"query","time":"2026-10-18T09:40:07.5Z","group":"g1","sql":"select * from users where id > ?",
              "bindings":[0,"a",null],"connection":"sqlite","duration_ms":0.42,
              "source":{"file":"/code/shop/routes/web.php","line":12,"name":"web.php"},"some_future_field":{"x":1}},
             {"id":"1","kind":"mail","time":"2026-10-18T09:40:07.4Z","group":"g1"}]
            """);

        Assert.Equal((200, 31.2), (events[0].StatusCode, events[0].DurationMs));
        Assert.Equal("GET https://shop.test/spike", events[0].Origin);
        Assert.Equal(("App\\Jobs\\SpikeJob", "failed", "RuntimeException: boom"), (events[1].Job, events[1].State, events[1].Exception));
        Assert.Equal("artisan queue:work", events[1].Origin);
        Assert.Equal("spike", events[2].View);
        Assert.Equal(("warning", "spike request"), (events[3].Level, events[3].Message));
        Assert.Contains("\"count\": 0", events[3].ContextText);
        Assert.Equal("[0, \"a\", null]", events[4].BindingsText);
        Assert.Equal(("sqlite", 0.42, 12), (events[4].Connection, events[4].DurationMs, events[4].Source?.Line));
        Assert.Null(events[4].StatusCode);
        Assert.False(events[5].IsKnown);
        Assert.True(events[4].IsKnown);
    }

    [Fact]
    public void EmptyLogContextShowsNothing()
    {
        var events = FlockJson.Decode<List<RuntimeEvent>>("""
            [{"id":"1","kind":"log","time":"2026-10-18T09:40:07Z","context":{}},{"id":"2","kind":"log","time":"2026-10-18T09:40:07Z"}]
            """);

        Assert.Equal("", events[0].ContextText);
        Assert.Equal("", events[1].ContextText);
        Assert.Equal("", events[1].BindingsText);
    }

    [Fact]
    public void DecodesTheFirstRunInstallOfTheDefaults()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "defaults":{"php":{"version":"8.5","state":"failed","error":"download failed"},"node":{"state":"waiting"}}}
            """);
        var defaults = status.Defaults!;
        Assert.Equal(("8.5", Status.DefaultState.Failed, "download failed"), (defaults.Php.Version, defaults.Php.State, defaults.Php.Error));
        Assert.True(defaults.Php.IsFailed);
        Assert.False(defaults.Php.IsPending);
        Assert.Equal(("", Status.DefaultState.Waiting, ""), (defaults.Node.Version, defaults.Node.State, defaults.Node.Error));
        Assert.True(defaults.Node.IsPending);
        Assert.False(defaults.Node.IsInstalling);

        var installing = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "defaults":{"php":{"version":"8.5","state":"done"},"node":{"version":"24","state":"installing"}}}
            """).Defaults!;
        Assert.False(installing.Php.IsPending);
        Assert.True(installing.Node.IsInstalling);
        Assert.Equal("24", installing.Node.Version);
    }

    [Fact]
    public void DecodesMailListAndDetail()
    {
        var list = FlockJson.Decode<List<MailSummary>>("""
            [{"id":"1791891607740103000-000001","inbox":"watchtower","from":"App <app@watchtower.test>","to":["you@example.com"],
              "subject":"Flock mail check ✓","date":"2026-10-13T13:40:07+02:00","size":321,"path":"/x/1.eml"}]
            """);
        Assert.Equal("Flock mail check ✓", list[0].Subject);

        var detail = FlockJson.Decode<MailDetail>("""
            {"id":"1","inbox":"default","from":"a@b.test","to":["c@d.test"],"cc":null,"subject":"Hi","date":"2026-10-13T13:40:07.1Z",
             "html":"<p>Hi</p>","text":"Hi","headers":[{"name":"Subject","value":"Hi"}],
             "attachments":[{"index":0,"filename":"invoice.pdf","content_type":"application/pdf","size":2048}]}
            """);
        Assert.Empty(detail.Cc);
        Assert.Equal("application/pdf", detail.Attachments[0].ContentType);
    }

    [Fact]
    public void DecodesDumpsWithOptionalContext()
    {
        var dumps = FlockJson.Decode<List<Dump>>("""
            [{"id":"2","time":"2026-10-13T14:00:00.999999999+02:00","site":"watchtower.test",
              "source":{"file":"/code/watchtower/app/Http/Controllers/HomeController.php","line":42,"name":"HomeController.php"},
              "request":{"method":"GET","uri":"https://watchtower.test/"},"text":"array:1 [...]","html":"<pre class=sf-dump>…</pre>"},
             {"id":"1","time":"2026-10-13T13:59:00Z","site":"","command_line":"artisan tinker","text":"1","html":"<pre>1</pre>"},
             {"id":"0","time":"2026-10-13T13:58:00Z","site":"","source":{"file":"C:\\code\\shop\\app\\Models\\Cart.php","line":7,"name":"Cart.php"},"text":"","html":""}]
            """);

        Assert.Equal("Controllers/HomeController.php:42", dumps[0].ShortSource);
        Assert.Equal("GET", dumps[0].Request?.Method);
        Assert.Null(dumps[1].Source);
        Assert.Equal("artisan tinker", dumps[1].CommandLine);
        Assert.Equal("Models/Cart.php:7", dumps[2].ShortSource);
    }

    [Fact]
    public void DecodesLogs()
    {
        var index = FlockJson.Decode<LogIndex>("""{"services":["daemon","web"],"sites":["watchtower"]}""");
        Assert.Equal(["watchtower"], index.Sites);

        var entries = FlockJson.Decode<List<LogEntry>>("""
            [{"time":"2026-10-13T12:00:00+00:00","env":"local","level":"ERROR","message":"Boom","details":"#0 stack"},
             {"time":"2026-10-13T11:00:00Z","env":"local","level":"info","message":"Hello"}]
            """);
        Assert.Equal("error", entries[0].Level);
        Assert.Empty(entries[1].Details);
    }

    [Fact]
    public void DecodesTheApiConfig()
    {
        var config = FlockJson.Decode<ApiConfig>("""{"url":"http://127.0.0.1:53531","token":"abc"}""");
        Assert.Equal(53531, new Uri(config.Url).Port);
        Assert.Equal("abc", config.Token);
    }

    [Fact]
    public void DecodesTheAiClients()
    {
        var state = FlockJson.Decode<McpState>("""
            {"clients":[{"id":"claude-code","name":"Claude Code","state":"registered","config":"C:\\Users\\ann\\.claude.json"},
                        {"id":"vscode","name":"VS Code","state":"available","problem":"Flock can't read mcp.json — add Flock to it by hand"},
                        {"id":"codex","name":"Codex","state":"missing"}],
             "command":"C:\\Users\\ann\\AppData\\Roaming\\Flock\\bin\\flock.exe","args":["mcp"],"snippet":"{}"}
            """);
        Assert.Equal(["claude-code", "vscode", "codex"], state.Clients.Select(c => c.Id));
        Assert.Equal([McpClientState.Registered, McpClientState.Available, McpClientState.Missing], state.Clients.Select(c => c.State));
        Assert.Contains("by hand", state.Clients[1].Problem);
        Assert.Equal(["mcp"], state.Args);
        Assert.EndsWith("flock.exe", state.Command);
    }

    [Fact]
    public void DecodesTheBoostStateOfSites()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[{"name":"a","boost":"missing"},{"name":"b"}]}
            """);
        Assert.Equal(Status.BoostState.Missing, status.Sites[0].Boost);
        Assert.Equal("", status.Sites[1].Boost);
    }
}
