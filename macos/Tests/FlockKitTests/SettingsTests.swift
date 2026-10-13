import Foundation
import Testing
@testable import FlockKit

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try FlockJSON.decoder().decode(T.self, from: Data(json.utf8))
}

@Test func decodesConfig() throws {
    let config = try decode(FlockConfig.self, """
    {"tld":"test","default_php":"8.5","default_node":"22","xdebug":false,"dumps":true,
     "root":"/Users/x/Library/Application Support/Flock",
     "ports":{"http":80,"https":443,"dns":53535,"smtp":2525,"postgres":5432,"redis":6379,"dumps":9912,"api":53531},
     "parked":["/Users/x/Developer"],
     "php":[{"version":"8.5","full_version":"8.5.11","ini":"/f/php/8.5/php.ini","xdebug_available":true},
            {"version":"8.4","full_version":"8.4.26","ini":"/f/php/8.4/php.ini","xdebug_available":false}],
     "node":["22.23.3","22.1.0","20.19.5"]}
    """)

    #expect(config.defaultPHP == "8.5")
    #expect(config.defaultNode == "22")
    #expect(config.dumps)
    #expect(config.ports.smtp == 2525)
    #expect(config.ports.api == 53531)
    #expect(config.php.map(\.fullVersion) == ["8.5.11", "8.4.26"])
    #expect(config.php[0].xdebugAvailable && !config.php[1].xdebugAvailable)
    #expect(config.nodeMajors == ["22", "20"])
}

@Test func decodesConfigWithNullsAndMissingPorts() throws {
    let config = try decode(FlockConfig.self, """
    {"tld":"test","default_php":"8.5","default_node":"22","root":"/r","ports":{"http":80},"parked":null,"php":null,"node":null}
    """)
    #expect(config.parked.isEmpty && config.php.isEmpty && config.node.isEmpty)
    #expect(config.ports.https == 0)
}

@Test func decodesServices() throws {
    let services = try decode([Service].self, """
    [{"id":"postgres","name":"PostgreSQL","category":"Database","version":"18.6.0","port":5432,"installed":true,
      "enabled":true,"running":true,"stoppable":true,
      "env":"DB_CONNECTION=pgsql\\nDB_HOST=127.0.0.1","log":"postgres","url":"postgres://postgres@127.0.0.1:5432"},
     {"id":"web","name":"Web Server","category":"Core","version":"","port":443,"installed":true,
      "enabled":true,"running":true,"stoppable":false,"env":"","log":"web","url":""}]
    """)

    #expect(services.count == 2)
    #expect(services[0].isDatabase)
    #expect(services[0].env.contains("DB_HOST=127.0.0.1"))
    #expect(!services[1].stoppable && !services[1].isDatabase)
}

@Test func siteDecodesOptionalNode() throws {
    let site = try decode(Status.Site.self, """
    {"name":"shop","domain":"shop.test","path":"/c/shop","php":"8.5","node":"22","secured":true,"parked":false}
    """)
    #expect(site.node == "22")
}

@Test func encodesOnlyChangedSettings() throws {
    let encoder = JSONEncoder()
    encoder.outputFormatting = .sortedKeys
    let settings = String(decoding: try encoder.encode(SettingsChange(defaultPHP: "8.4")), as: UTF8.self)
    #expect(settings == #"{"default_php":"8.4"}"#)

    let site = String(decoding: try encoder.encode(SiteChange(php: "")), as: UTF8.self)
    #expect(site == #"{"php":""}"#)
}
