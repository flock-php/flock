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
            {"version":"0893ae2","pid":91994,"started_at":"2026-10-13T14:10:01.123456+02:00",
             "dns":{"tld":"test","addr":"127.0.0.1:53535"},"http_port":80,"https_port":443,
             "mail":"127.0.0.1:2525","postgres":"18.6.0 on 127.0.0.1:5432","redis":"Valkey 9.1.2 on 127.0.0.1:6379",
             "xdebug":true,"dumps":false,"mail_count":3,"dump_count":12,
             "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":true,"parked":false,"url":"https://watchtower.test"},
                      {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"7.4","secured":false,"parked":true,"problem":"PHP 7.4 is not installed"}],
             "processes":[{"name":"php8.5-fpm","up":true,"restarts":0},{"name":"postgres","up":false,"restarts":3,"last_error":"exit status 1: could not bind"}],
             "warnings":["postgres: port 5432 is used by postgres (pid 1)"]}
            """);

        Assert.Equal(443, status.HttpsPort);
        Assert.Equal(3, status.MailCount);
        Assert.Equal(12, status.DumpCount);
        Assert.Equal("https://watchtower.test", status.Sites[0].Url);
        Assert.Null(status.Sites[1].Url);
        Assert.NotNull(status.Sites[1].Problem);
        Assert.Equal("exit status 1: could not bind", status.Processes[1].LastError);
        Assert.True(status.IsDegraded);
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
        Assert.Null(status.Redis);
        Assert.Empty(status.Sites);
        Assert.Empty(status.Warnings);
        Assert.False(status.IsDegraded);
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
}
