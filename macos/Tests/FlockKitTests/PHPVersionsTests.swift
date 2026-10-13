import Foundation
import Testing
@testable import FlockKit

@Test func decodesPHPCatalog() throws {
    let catalog = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data("""
    {"auto_build": true, "checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
     "versions": [
      {"minor":"8.5","latest":"8.5.11","installed":"8.5.11","default":true,"supported":true,
       "status":"installed","update_ready":false,"xdebug":true,"job":null},
      {"minor":"8.4","latest":"8.4.27","installed":"8.4.26","default":false,"supported":true,
       "status":"update","update_ready":true,"xdebug":true,"job":null},
      {"minor":"8.3","latest":"8.3.35","installed":"","default":false,"supported":true,
       "status":"likely","update_ready":false,"xdebug":false,
       "job":{"state":"running","install":true,"auto":false,"step":"Compiling ext/intl …","full_version":"",
              "started_at":"2026-10-13T15:41:00+02:00","finished_at":null,"error":""}},
      {"minor":"7.4","latest":"7.4.33","installed":"","default":false,"supported":false,
       "status":"unsupported","update_ready":false,"xdebug":false,"note":"Too old for current compilers.","job":null},
      {"minor":"9.0","latest":"9.0.0","status":"something-new"}
     ]}
    """.utf8))

    #expect(catalog.autoBuild)
    #expect(catalog.checkedAt != nil)
    #expect(catalog.versions.map(\.minor) == ["8.5", "8.4", "8.3", "7.4", "9.0"])
    #expect(catalog.versions[0].isDefault && catalog.versions[0].isInstalled && catalog.versions[0].job == nil)
    #expect(catalog.versions[1].status == .update && catalog.versions[1].updateReady)
    #expect(!catalog.versions[3].supported && catalog.versions[3].status == .unsupported)
    #expect(catalog.versions[3].note == "Too old for current compilers." && catalog.versions[0].note.isEmpty)
    #expect(catalog.versions[4].status == .unknown && catalog.versions[4].supported)

    let job = try #require(catalog.versions[2].job)
    #expect(job.state == .running && job.install && !job.auto && job.isActive)
    #expect(job.startedAt != nil && job.finishedAt == nil)
    #expect(catalog.hasActiveJob)
}

@Test func decodesCatalogWithoutCheckAndFinishedJob() throws {
    let catalog = try FlockJSON.decoder().decode(PHPCatalog.self, from: Data("""
    {"auto_build": false, "checked_at": null, "check_error": "php.net unreachable",
     "versions": [{"minor":"8.5","latest":"8.5.12","installed":"8.5.11","default":true,"supported":true,
       "status":"failed","update_ready":false,"xdebug":true,
       "job":{"state":"failed","install":true,"auto":true,"step":"","full_version":"8.5.12",
              "started_at":"2026-10-13T15:41:00Z","finished_at":"2026-10-13T15:47:12.123456789Z","error":"make: *** [ext/intl] Error 1"}}]}
    """.utf8))

    #expect(!catalog.autoBuild && catalog.checkedAt == nil && catalog.checkError == "php.net unreachable")
    #expect(!catalog.hasActiveJob)
    let job = try #require(catalog.versions[0].job)
    #expect(job.state == .failed && job.auto && job.finishedAt != nil && job.error.contains("intl"))
}

@Test func settingsChangeEncodesPHPAutoBuild() throws {
    let json = String(decoding: try JSONEncoder().encode(SettingsChange(phpAutoBuild: false)), as: UTF8.self)
    #expect(json == #"{"php_auto_build":false}"#)
}

@Test func configDecodesPHPAutoBuildWithDefault() throws {
    let base = #""tld":"test","default_php":"8.5","default_node":"22","root":"/r","ports":{"http":80}"#
    let off = try FlockJSON.decoder().decode(FlockConfig.self, from: Data("{\(base),\"php_auto_build\":false}".utf8))
    let missing = try FlockJSON.decoder().decode(FlockConfig.self, from: Data("{\(base)}".utf8))
    #expect(!off.phpAutoBuild)
    #expect(missing.phpAutoBuild)
}
