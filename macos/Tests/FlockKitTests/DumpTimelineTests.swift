import Foundation
import Testing
@testable import FlockKit

private func dumps(_ json: String) throws -> [Dump] {
    try FlockJSON.decoder().decode([Dump].self, from: Data(json.utf8))
}

private let sample = """
[
 {"id":"4","time":"2026-10-13T12:00:04Z","site":"shop.test","group":"req-2","label":null,
  "source":{"file":"/code/shop/app/Http/Controllers/CartController.php","line":42,"name":"CartController.php"},
  "request":{"method":"GET","uri":"https://shop.test/cart"},"command_line":null,"text":"second","html":"<pre class=sf-dump>second</pre>"},
 {"id":"3","time":"2026-10-13T12:00:03Z","site":"shop.test","group":"req-2","label":"cart",
  "source":{"file":"/code/shop/app/Models/Cart.php","line":7,"name":"Cart.php"},
  "request":{"method":"GET","uri":"https://shop.test/cart"},"command_line":null,"text":"first","html":"<pre class=sf-dump>first</pre>"},
 {"id":"1","time":"2026-10-13T11:59:00Z","site":"","group":"cli-1","source":null,"request":null,
  "command_line":"artisan tinker","text":"<b>old</b>","html":"<pre class=sf-dump>old</pre>"}
]
"""

@Test func latestGroupKeepsOnlyTheNewestRequest() throws {
    let all = try dumps(sample)
    #expect(DumpTimeline.latestGroup(of: all).map(\.id) == ["4", "3"])
    #expect(DumpTimeline.latestGroup(of: [Dump]()).isEmpty && DumpTimeline.latestGroup(of: [TimelineItem]()).isEmpty)
}

@Test func searchMatchesValueSourceAndOrigin() throws {
    let all = try dumps(sample)
    #expect(DumpTimeline.filter(all, query: "tinker").map(\.id) == ["1"])
    #expect(DumpTimeline.filter(all, query: "CART.php").map(\.id) == ["3"])
    #expect(DumpTimeline.filter(all, query: " ").count == 3)
}

@Test func htmlShowsRequestsNewestFirstAndDumpsInOrder() throws {
    let html = DumpTimeline.html(dumps: try dumps(sample), header: "<script>HEADER</script>", editor: .phpstorm)

    #expect(html.contains("<span class=\"verb get\">GET</span>"))
    let request = try #require(html.range(of: "https://shop.test/cart"))
    let cli = try #require(html.range(of: "artisan tinker"))
    #expect(request.lowerBound < cli.lowerBound)

    let first = try #require(html.range(of: ">first<"))
    let second = try #require(html.range(of: ">second<"))
    #expect(first.lowerBound < second.lowerBound)

    #expect(html.contains("phpstorm://open?file=/code/shop/app/Models/Cart.php&amp;line=7"))
    #expect(html.contains(">app/Models/Cart.php:7<"))
    #expect(html.components(separatedBy: "HEADER").count == 2)
}

@Test func editorURLs() {
    #expect(Editor.vscode.url(file: "/a b/c.php", line: 3)?.absoluteString == "vscode://file/a%20b/c.php:3")
    #expect(Editor.phpstorm.url(file: "/x.blade.php", line: 0)?.absoluteString == "phpstorm://open?file=/x.blade.php")
    #expect(Editor.none.url(file: "/x.php", line: 1) == nil)
}

// MARK: Events

private func events(_ json: String) throws -> [Event] {
    try FlockJSON.eventDecoder().decode([Event].self, from: Data(json.utf8))
}

private let eventSample = """
[
 {"id":"e6","kind":"request","time":"2026-10-13T12:00:05Z","site":"shop.test","group":"req-2",
  "request":{"method":"GET","uri":"https://shop.test/cart"},"command_line":null,"status_code":500,"duration_ms":31.2},
 {"id":"e5","kind":"job","time":"2026-10-13T12:00:03.5Z","site":"shop.test","group":"req-2","request":{"method":"GET","uri":"https://shop.test/cart"},
  "job":"App\\\\Jobs\\\\SendInvoice","queue":"default","connection":"sync","state":"failed","exception":"RuntimeException: boom","duration_ms":5.5},
 {"id":"e4","kind":"view","time":"2026-10-13T12:00:03.2Z","site":"shop.test","group":"req-2","request":{"method":"GET","uri":"https://shop.test/cart"},
  "view":"cart.show","path":"/code/shop/resources/views/cart/show.blade.php"},
 {"id":"e3","kind":"log","time":"2026-10-13T12:00:03.1Z","site":"shop.test","group":"req-2","request":{"method":"GET","uri":"https://shop.test/cart"},
  "level":"warning","message":"cart is <empty>","context":{"user_id":7,"tags":["a","b"]}},
 {"id":"e2","kind":"query","time":"2026-10-13T12:00:02Z","site":"shop.test","group":"req-2","request":{"method":"GET","uri":"https://shop.test/cart"},
  "source":{"file":"/code/shop/app/Models/Cart.php","line":9,"name":"Cart.php"},
  "sql":"select * from carts where user_id = ? and note = ? and paid = ?","bindings":[7,"it's",null],"connection":"sqlite","duration_ms":0.42},
 {"id":"e1","kind":"cache","time":"2026-10-13T12:00:01Z","group":"req-2","key":"x"},
 {"id":"e0","kind":"log","time":"2026-10-13T11:59:30Z","group":"cli-2","command_line":"artisan queue:work","level":"info","message":"idle","context":[]}
]
"""

@Test func decodesEveryEventKindLeniently() throws {
    let all = try events(eventSample)
    #expect(all.map(\.kind) == [.request, .job, .view, .log, .query, nil, .log])

    let query = all[4]
    #expect(query.sql.hasPrefix("select") && query.bindings == [.number(7), .string("it's"), .null])
    #expect(query.durationMs == 0.42 && query.connection == "sqlite" && query.source?.line == 9)

    // Context keys stay as the app wrote them; PHP's [] for an empty context is no context.
    #expect(all[3].context["user_id"] == .number(7))
    #expect(all[6].context.isEmpty && all[6].commandLine == "artisan queue:work" && all[6].request == nil)

    #expect(all[1].state == "failed" && all[1].exception == "RuntimeException: boom")
    #expect(all[0].statusCode == 500 && all[0].durationMs == 31.2)
}

@Test func timelineMergesDumpsAndEventsByTime() throws {
    let items = DumpTimeline.merge(dumps: try dumps(sample), events: try events(eventSample).filter { $0.kind != nil })
    #expect(items.map(\.id).prefix(5) == ["event-e6", "dump-4", "event-e5", "event-e4", "event-e3"])

    // The latest request has its dumps and events; the CLI run's are left out.
    let latest = DumpTimeline.latestGroup(of: items)
    #expect(latest.count == 7 && latest.allSatisfy { $0.group == "req-2" })

    #expect(DumpTimeline.filter(items, query: "carts where").map(\.id) == ["event-e2"])
    #expect(DumpTimeline.filter(items, query: "SendInvoice").map(\.id) == ["event-e5"])
    #expect(DumpTimeline.filter(items, query: "user_id").map(\.id) == ["event-e3", "event-e2"])
}

@Test func htmlRendersEachEventKind() throws {
    let items = DumpTimeline.merge(dumps: try dumps(sample), events: try events(eventSample).filter { $0.kind != nil })
    let html = DumpTimeline.html(items: items, header: "", editor: .vscode)

    // The finished request is the group's header, not a row.
    #expect(html.contains("<span class=\"status s5xx\">500</span><span class=\"duration\">31 ms</span>"))
    #expect(!html.contains("event request"))

    #expect(html.contains("select * from carts where user_id = 7 and note = 'it''s' and paid = NULL"))
    #expect(html.contains("vscode://file/code/shop/app/Models/Cart.php:9"))
    #expect(html.contains("0.42 ms"))
    #expect(html.contains("<span class=\"kind level-warning\">WARNING</span>"))
    #expect(html.contains("cart is &lt;empty&gt;"))
    #expect(html.contains("&quot;user_id&quot;: 7"))
    #expect(html.contains(">cart.show<") && html.contains(">resources/views/cart/show.blade.php<"))
    #expect(html.contains("<span class=\"state failed\">failed</span>") && html.contains("RuntimeException: boom"))

    // Chronological within the request, dumps and events interleaved.
    let order = ["carts where", ">first<", "cart is", "cart.show", "SendInvoice", ">second<"].map { html.range(of: $0)!.lowerBound }
    #expect(order == order.sorted())

    // One section per request or CLI run.
    #expect(html.components(separatedBy: "<section class=\"group\">").count == 4)
    #expect(html.contains("<span class=\"verb cli\">CLI</span><span class=\"what\">artisan queue:work</span>"))
}

@Test func sqlInterpolationFallsBackWhenBindingsDontLineUp() {
    #expect(DumpTimeline.interpolate(sql: "select ?", bindings: []) == "select ?")
    #expect(DumpTimeline.interpolate(sql: "select ? from t where a = ?", bindings: [.string("x")]) == "select ? from t where a = ?  -- 'x'")
    #expect(DumpTimeline.duration(1500) == "1.50 s" && DumpTimeline.duration(12.4) == "12 ms" && DumpTimeline.duration(0.4) == "0.40 ms")
}

@Test func concurrentRequestsKeepTheirEntriesTogether() throws {
    let all = try events("""
    [{"id":"3","kind":"log","time":"2026-10-13T12:00:03Z","group":"a","request":{"method":"GET","uri":"https://x.test/a"},"level":"info","message":"a-late"},
     {"id":"2","kind":"log","time":"2026-10-13T12:00:02Z","group":"b","request":{"method":"POST","uri":"https://x.test/b"},"level":"info","message":"b-only"},
     {"id":"1","kind":"log","time":"2026-10-13T12:00:01Z","group":"a","request":{"method":"GET","uri":"https://x.test/a"},"level":"info","message":"a-early"}]
    """)
    let html = DumpTimeline.html(items: DumpTimeline.merge(dumps: [], events: all), header: "", editor: .none)
    #expect(html.components(separatedBy: "<section class=\"group\">").count == 3)
    let order = ["a-early", "a-late", "b-only"].map { html.range(of: $0)!.lowerBound }
    #expect(order == order.sorted())
}
