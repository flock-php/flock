import Foundation
import Testing
@testable import FlockKit

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try FlockJSON.decoder().decode(T.self, from: Data(json.utf8))
}

@Test func parsesGoTimesWithAndWithoutFractions() throws {
    let whole = try #require(FlockJSON.parseDate("2026-10-13T13:40:07+02:00"))
    let nanos = try #require(FlockJSON.parseDate("2026-10-13T13:40:07.740103123+02:00"))
    let utc = try #require(FlockJSON.parseDate("2026-10-13T11:40:07.5Z"))

    #expect(abs(nanos.timeIntervalSince(whole) - 0.740103123) < 0.000001)
    #expect(utc.timeIntervalSince(whole) == 0.5)
    #expect(FlockJSON.parseDate("yesterday") == nil)
}

@Test func decodesAFullStatus() throws {
    let status = try decode(Status.self, """
    {"version":"0893ae2","pid":91994,"started_at":"2026-10-13T14:10:01.123456+02:00",
     "dns":{"tld":"test","addr":"127.0.0.1:53535"},"http_port":80,"https_port":443,
     "mail":"127.0.0.1:2525","postgres":"18.6.0 on 127.0.0.1:5432","valkey":"9.1.2 on 127.0.0.1:6379",
     "extras":[{"id":"mysql","name":"MySQL","version":"8.4.6","port":3306,"enabled":true,"up":true},{"id":"redis"}],
     "xdebug":true,"dumps":false,"mail_count":3,"dump_count":12,
     "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":true,"parked":false,"url":"https://watchtower.test"},
              {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"7.4","secured":false,"parked":true,"problem":"PHP 7.4 is not installed"}],
     "processes":[{"name":"php8.5-fpm","up":true,"restarts":0},{"name":"postgres","up":false,"restarts":3,"last_error":"exit status 1: could not bind"}],
     "warnings":["postgres: port 5432 is used by postgres (pid 1)"]}
    """)

    #expect(status.httpsPort == 443)
    #expect(status.valkey == "9.1.2 on 127.0.0.1:6379")
    #expect(status.extras.map(\.id) == ["mysql", "redis"] && status.extras[0].up && status.extras[0].port == 3306)
    #expect(status.extras[1].name == "redis" && !status.extras[1].up && status.extras[1].enabled)
    #expect(status.mailCount == 3 && status.dumpCount == 12)
    #expect(status.sites[0].url == "https://watchtower.test")
    #expect(status.sites[1].url == nil && status.sites[1].problem != nil)
    #expect(status.processes[1].lastError == "exit status 1: could not bind")
    #expect(status.isDegraded)
}

@Test func decodesExtraDomainsAndTheWildcardState() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
     "dns":{"tld":"test","addr":"127.0.0.1:53535","wildcard":true,"max_depth":4},
     "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"app","domain":"app.test","path":"/code/app","php":"8.5","secured":true,"parked":false,"url":"https://app.test",
               "domains":[{"entry":"tenant1","host":"tenant1.app.test","alias":false,"url":"https://tenant1.app.test"},
                          {"entry":"promo.test","host":"promo.test","alias":true,"problem":"promo.test is already served by promo"}]},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.5","secured":false,"parked":true,"domains":null}]}
    """)

    #expect(status.dns.wildcard && status.dns.wildcardProblem.isEmpty && status.dns.maxDepth == 4)
    let app = status.sites[0]
    #expect(app.domains.map(\.host) == ["tenant1.app.test", "promo.test"])
    #expect(app.domains[0].url == "https://tenant1.app.test" && !app.domains[0].alias)
    #expect(app.domains[1].alias && app.domains[1].url == nil && app.domains[1].problem != nil)
    #expect(status.sites[1].domains.isEmpty)
}

@Test func decodesAMinimalStatusWithOmittedFields() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
     "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","xdebug":false,"dumps":false,
     "mail_count":0,"dump_count":0,"sites":null,"processes":[]}
    """)

    #expect(status.postgres == nil && status.valkey == nil && status.extras.isEmpty)
    #expect(status.sites.isEmpty && status.warnings.isEmpty)
    #expect(!status.isDegraded)
}

@Test func decodesMailListAndDetail() throws {
    let list = try decode([MailSummary].self, """
    [{"id":"1791891607740103000-000001","inbox":"watchtower","from":"App <app@watchtower.test>","to":["you@example.com"],
      "subject":"Flock mail check ✓","date":"2026-10-13T13:40:07+02:00","size":321,"path":"/x/1.eml"}]
    """)
    #expect(list.first?.subject == "Flock mail check ✓")

    let detail = try decode(MailDetail.self, """
    {"id":"1","inbox":"default","from":"a@b.test","to":["c@d.test"],"cc":null,"subject":"Hi","date":"2026-10-13T13:40:07.1Z",
     "html":"<p>Hi</p>","text":"Hi","headers":[{"name":"Subject","value":"Hi"}],
     "attachments":[{"index":0,"filename":"invoice.pdf","content_type":"application/pdf","size":2048}]}
    """)
    #expect(detail.cc.isEmpty)
    #expect(detail.attachments.first?.contentType == "application/pdf")
}

@Test func decodesDumpsWithOptionalContext() throws {
    let dumps = try decode([Dump].self, """
    [{"id":"2","time":"2026-10-13T14:00:00.999999999+02:00","site":"watchtower.test",
      "source":{"file":"/code/watchtower/app/Http/Controllers/HomeController.php","line":42,"name":"HomeController.php"},
      "request":{"method":"GET","uri":"https://watchtower.test/"},"text":"array:1 [...]","html":"<pre class=sf-dump>…</pre>"},
     {"id":"1","time":"2026-10-13T13:59:00Z","site":"","command_line":"artisan tinker","text":"1","html":"<pre>1</pre>"}]
    """)

    #expect(dumps[0].shortSource == "Controllers/HomeController.php:42")
    #expect(dumps[0].request?.method == "GET")
    #expect(dumps[1].source == nil && dumps[1].commandLine == "artisan tinker")
}

@Test func decodesLogs() throws {
    let index = try decode(LogIndex.self, #"{"services":["daemon","web"],"sites":["watchtower"]}"#)
    #expect(index.sites == ["watchtower"])

    let entries = try decode([LogEntry].self, """
    [{"time":"2026-10-13T12:00:00+00:00","env":"local","level":"ERROR","message":"Boom","details":"#0 stack"},
     {"time":"2026-10-13T11:00:00Z","env":"local","level":"info","message":"Hello"}]
    """)
    #expect(entries[0].level == "error")
    #expect(entries[1].details.isEmpty)
}

@Test func decodesTheAPIConfig() throws {
    let config = try JSONDecoder().decode(APIConfig.self, from: Data(#"{"url":"http://127.0.0.1:53531","token":"abc"}"#.utf8))
    #expect(config.url.port == 53531)
}

@Test func decodesWhereASiteVersionComesFrom() throws {
    let status = try decode(Status.self, """
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
     "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
     "sites":[{"name":"shop","domain":"shop.test","path":"/code/shop","secured":false,"parked":false,
               "php":"8.3","php_source":"isolated","php_pinned":"8.4","php_file":"/code/shop/.flockrc",
               "node":"24","node_source":"project","node_pinned":"24","node_file":"/code/shop/.flockrc","node_shadowed":".nvmrc: 20"},
              {"name":"blog","domain":"blog.test","path":"/code/blog","php":"8.5","secured":false,"parked":true}]}
    """)

    let shop = status.sites[0]
    #expect(shop.phpSource == .isolated && shop.phpPinned == "8.4" && shop.phpPinnedHere)
    #expect(shop.nodeSource == .project && shop.nodeShadowed == ".nvmrc: 20" && shop.nodePinnedHere)
    #expect(status.sites[1].phpSource == .default && !status.sites[1].phpPinnedHere)
}
