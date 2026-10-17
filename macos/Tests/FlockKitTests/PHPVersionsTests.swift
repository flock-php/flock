import Foundation
import Testing
@testable import FlockKit

@Test func decodesPHPCatalog() throws {
    let catalog = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data("""
    {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
     "versions": [
      {"minor":"8.5","latest":"8.5.11","installed":"8.5.11","status":"installed","update":false,"size":64575439,
       "prerelease":false,"yanked":"","default":true,"xdebug":true,"job":null},
      {"minor":"8.4","latest":"8.4.27","installed":"8.4.26","status":"update","update":true,"size":63000000,
       "prerelease":false,"yanked":"crashes on start","default":false,"xdebug":true,"job":null},
      {"minor":"8.3","latest":"8.3.35","installed":"","status":"available","update":false,"size":62000000,
       "prerelease":false,"yanked":"","default":false,"xdebug":false,
       "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"",
              "started_at":"2026-10-13T15:41:00+02:00"}},
      {"minor":"8.6","latest":"8.6.0RC2","installed":"","status":"available","prerelease":true},
      {"minor":"9.0","latest":"9.0.0","status":"something-new"}
     ]}
    """.utf8))

    #expect(catalog.checkedAt != nil)
    #expect(catalog.versions.map(\.minor) == ["8.5", "8.4", "8.3", "8.6", "9.0"])
    #expect(catalog.versions[0].isDefault && catalog.versions[0].isInstalled && catalog.versions[0].job == nil && catalog.versions[0].size == 64575439)
    #expect(catalog.versions[1].status == .update && catalog.versions[1].update && catalog.versions[1].yanked == "crashes on start")
    #expect(catalog.versions[3].prerelease && catalog.versions[3].yanked.isEmpty && !catalog.versions[3].update)
    #expect(catalog.versions[4].status == .unknown)

    let job = try #require(catalog.versions[2].job)
    #expect(job.isRunning && job.phase == .downloading && job.progress == 0.42)
    #expect(catalog.hasActiveJob)
}

@Test func decodesCatalogWithoutCheckAndFailedJob() throws {
    let catalog = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data("""
    {"checked_at": null, "check_error": "the package manifest's signature doesn't verify",
     "versions": [{"minor":"8.5","latest":"8.5.12","installed":"8.5.11","status":"update","update":true,"default":true,
       "job":{"action":"update","state":"failed","phase":"downloading","progress":-1,"error":"checksum mismatch",
              "started_at":"2026-10-13T15:41:00Z"}}]}
    """.utf8))

    #expect(catalog.checkedAt == nil && catalog.checkError.contains("signature"))
    #expect(!catalog.hasActiveJob)
    let job = try #require(catalog.versions[0].job)
    #expect(job.isFailed && job.action == .update && job.error == "checksum mismatch")
}

@Test func settingsChangeEncodesTheChannel() throws {
    let json = String(decoding: try JSONEncoder().encode(SettingsChange(channel: "beta")), as: UTF8.self)
    #expect(json == #"{"channel":"beta"}"#)
}

@Test func configDecodesTheChannelWithDefault() throws {
    let base = #""tld":"test","default_php":"8.5","default_node":"22","root":"/r","ports":{"http":80}"#
    let beta = try FlockJSON.decoder().decode(FlockConfig.self, from: Data("{\(base),\"channel\":\"beta\"}".utf8))
    let missing = try FlockJSON.decoder().decode(FlockConfig.self, from: Data("{\(base)}".utf8))
    #expect(beta.channel == "beta")
    #expect(missing.channel == "stable")
}

@Test func decodesSiteExtensions() throws {
    let report = try FlockJSON.decoder().decode(ExtensionReport.self, from: Data("""
    {"php":"8.4","laravel":true,"extensions":[
      {"name":"intl","required_by":["composer.json","mongodb/mongodb"],"available":true},
      {"name":"pdo_pgsql","required_by":[],"hint":"DB_CONNECTION=pgsql in .env","available":true},
      {"name":"swoole","required_by":["composer.json"],"available":false}]}
    """.utf8))

    #expect(report.php == "8.4" && report.laravel && report.extensions.count == 3)
    #expect(report.extensions[0].declared && report.extensions[0].requiredBy == ["composer.json", "mongodb/mongodb"])
    #expect(!report.extensions[1].declared && report.extensions[1].hint == "DB_CONNECTION=pgsql in .env")
    #expect(!report.extensions[2].available)
}
