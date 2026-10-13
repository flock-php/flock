import Foundation
import Testing
@testable import FlockKit

@Test func decodesNodeCatalog() throws {
    let catalog = try FlockJSON.decoder().decode(NodeCatalog.self, from: Data("""
    {"checked_at": "2026-10-13T15:40:00+02:00", "check_error": "",
     "versions": [
      {"major":"24","latest":"24.11.0","codename":"Krypton","support":"lts","end_of_life":"2028-04-30",
       "installed":"","default":false,"update":false,"npm":"","npm_latest":""},
      {"major":"22","latest":"22.21.0","codename":"Jod","support":"maintenance","end_of_life":"2027-04-30",
       "installed":"22.20.0","default":true,"update":true,"npm":"10.9.2","npm_latest":"12.2.0"},
      {"major":"14","latest":"14.21.3","support":"something-new","installed":"14.21.3"}
     ]}
    """.utf8))

    #expect(catalog.checkedAt != nil)
    #expect(catalog.versions.map(\.major) == ["24", "22", "14"])
    #expect(catalog.versions[0].codename == "Krypton" && catalog.versions[0].support == .lts && !catalog.versions[0].isInstalled)
    #expect(catalog.versions[0].endOfLife == "2028-04-30")
    #expect(catalog.versions[1].isDefault && catalog.versions[1].update && catalog.versions[1].installed == "22.20.0")
    #expect(catalog.versions[1].npm == "10.9.2" && catalog.versions[1].npmLatest == "12.2.0")
    #expect(catalog.versions[2].support == .unknown && catalog.versions[2].npm.isEmpty)
}

@Test func decodesNodeCatalogWhenNodejsOrgIsUnreachable() throws {
    let catalog = try FlockJSON.decoder().decode(NodeCatalog.self, from: Data("""
    {"checked_at": null, "check_error": "dial tcp: timeout", "versions": []}
    """.utf8))
    #expect(catalog.checkedAt == nil && catalog.checkError == "dial tcp: timeout" && catalog.versions.isEmpty)
}
