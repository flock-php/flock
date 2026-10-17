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
    #expect(DumpTimeline.latestGroup(of: []).isEmpty)
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
