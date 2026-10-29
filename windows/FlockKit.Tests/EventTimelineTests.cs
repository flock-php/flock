namespace Flock.Kit.Tests;

public class EventTimelineTests
{
    // Request g1 (dump, query, log, view, request) and an older queue run g0 (job), newest first.
    private const string DumpSample = """
        [{"id":"d1","time":"2026-10-18T09:40:07.55Z","site":"shop","group":"g1",
          "source":{"file":"/code/shop/routes/web.php","line":16,"name":"web.php"},
          "request":{"method":"GET","uri":"https://shop.test/spike"},"text":"dumped","html":"<pre class=sf-dump>dumped</pre>"}]
        """;

    private const string EventSample = """
        [{"id":"e5","kind":"request","time":"2026-10-18T09:40:07.9Z","site":"shop","group":"g1",
          "request":{"method":"GET","uri":"https://shop.test/spike"},"status_code":404,"duration_ms":1500},
         {"id":"e4","kind":"view","time":"2026-10-18T09:40:07.7Z","site":"shop","group":"g1",
          "request":{"method":"GET","uri":"https://shop.test/spike"},"view":"spike-partial","path":"/code/shop/resources/views/spike-partial.blade.php"},
         {"id":"e3","kind":"log","time":"2026-10-18T09:40:07.6Z","site":"shop","group":"g1",
          "request":{"method":"GET","uri":"https://shop.test/spike"},"level":"error","message":"<boom>","context":{"user":7}},
         {"id":"e2","kind":"query","time":"2026-10-18T09:40:07.5Z","site":"shop","group":"g1",
          "request":{"method":"GET","uri":"https://shop.test/spike"},"sql":"select * from users where id > ?","bindings":[0],
          "connection":"sqlite","duration_ms":0.42,"source":{"file":"/code/shop/routes/web.php","line":12,"name":"web.php"}},
         {"id":"e1","kind":"job","time":"2026-10-18T09:39:00Z","site":"","group":"g0","command_line":"artisan queue:work",
          "job":"App\\Jobs\\SpikeJob","queue":"default","state":"failed","exception":"RuntimeException: nope","duration_ms":5.5},
         {"id":"e0","kind":"mail","time":"2026-10-18T09:41:00Z","site":"","group":"g9","command_line":"from the future"}]
        """;

    private static List<Dump> Dumps() => FlockJson.Decode<List<Dump>>(DumpSample);

    private static List<RuntimeEvent> Events() => FlockJson.Decode<List<RuntimeEvent>>(EventSample);

    [Fact]
    public void LatestGroupSpansDumpsAndEventsAndSkipsUnknownKinds()
    {
        // The unknown "mail" event is the newest, but isn't rendered, so it doesn't pick the group.
        var (dumps, events) = DumpTimeline.LatestGroup(Dumps(), Events());
        Assert.Equal(["d1"], dumps.Select(d => d.Id));
        Assert.Equal(["e5", "e4", "e3", "e2"], events.Select(e => e.Id));

        // Without dumps the newest event's group wins.
        var onlyEvents = DumpTimeline.LatestGroup([], Events().Where(e => e.Group == "g0").ToList());
        Assert.Equal(["e1"], onlyEvents.Events.Select(e => e.Id));
        var none = DumpTimeline.LatestGroup([], []);
        Assert.Empty(none.Dumps);
        Assert.Empty(none.Events);
    }

    [Fact]
    public void SearchMatchesWhatEventsShow()
    {
        Assert.Equal(["e2"], DumpTimeline.Filter(Events(), "FROM USERS").Select(e => e.Id));
        Assert.Equal(["e3"], DumpTimeline.Filter(Events(), "boom").Select(e => e.Id));
        Assert.Equal(["e4"], DumpTimeline.Filter(Events(), "spike-partial").Select(e => e.Id));
        Assert.Equal(["e1"], DumpTimeline.Filter(Events(), "nope").Select(e => e.Id));
        Assert.Equal(["e1", "e0"], DumpTimeline.Filter(Events(), "artisan queue").Concat(DumpTimeline.Filter(Events(), "future")).Select(e => e.Id));
        Assert.Equal(6, DumpTimeline.Filter(Events(), "").Count);
    }

    [Fact]
    public void HtmlInterleavesEventsWithDumpsInOrder()
    {
        var html = DumpTimeline.Html(Dumps(), Events(), "<script>HEADER</script>", Editor.VsCode);

        // The request's entries in the order they happened: query, dump, log, view.
        int At(string s) => html.IndexOf(s, StringComparison.Ordinal);
        Assert.True(At("select * from users") < At(">dumped<"));
        Assert.True(At(">dumped<") < At("&lt;boom&gt;"));
        Assert.True(At("&lt;boom&gt;") < At(">spike-partial<"));
        // The newer request comes before the older queue run.
        Assert.True(At("https://shop.test/spike") < At("artisan queue:work"));

        Assert.Contains("<div class=\"bindings\">[0]</div>", html);
        Assert.Contains("<span class=\"duration\">0.42 ms</span>", html);
        Assert.Contains("vscode://file/code/shop/routes/web.php:12", html);
        Assert.Contains("<span class=\"kind level error\">error</span>", html);
        Assert.Contains("&quot;user&quot;: 7", html);
        Assert.Contains("vscode://file/code/shop/resources/views/spike-partial.blade.php", html);
        Assert.Contains("<span class=\"state failed\">failed</span>", html);
        Assert.Contains("RuntimeException: nope", html);

        // The request event shows in the group header (status and duration), not as a card.
        Assert.Contains("<span class=\"status s4xx\">404</span><span class=\"duration\">1.5 s</span>", html);
        Assert.DoesNotContain("event request", html);
        // Unknown kinds are left out, and every group shows once.
        Assert.DoesNotContain("from the future", html);
        Assert.Equal(2, html.Split("<section class=\"group\">").Length - 1);
        Assert.Equal(2, html.Split("HEADER").Length);
    }

    [Fact]
    public void EntriesOfOneRequestStayTogetherWhenRequestsOverlap()
    {
        var events = FlockJson.Decode<List<RuntimeEvent>>("""
            [{"id":"3","kind":"query","time":"2026-10-18T09:40:03Z","group":"a","request":{"method":"GET","uri":"https://a.test/"},"sql":"a2"},
             {"id":"2","kind":"query","time":"2026-10-18T09:40:02Z","group":"b","request":{"method":"POST","uri":"https://b.test/"},"sql":"b1"},
             {"id":"1","kind":"query","time":"2026-10-18T09:40:01Z","group":"a","request":{"method":"GET","uri":"https://a.test/"},"sql":"a1"}]
            """);
        var html = DumpTimeline.Html([], events, "", Editor.None);

        Assert.Equal(2, html.Split("<section class=\"group\">").Length - 1);
        int At(string s) => html.IndexOf(s, StringComparison.Ordinal);
        Assert.True(At(">a1<") < At(">a2<"));
        Assert.True(At(">a2<") < At(">b1<"));
        Assert.Contains("<span class=\"verb post\">POST</span>", html);
    }

    [Fact]
    public void Durations()
    {
        Assert.Equal("0.42 ms", DumpTimeline.Duration(0.42));
        Assert.Equal("31.2 ms", DumpTimeline.Duration(31.24));
        Assert.Equal("1.5 s", DumpTimeline.Duration(1500));
    }
}
