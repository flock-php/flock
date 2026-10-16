import Foundation
import Testing
@testable import FlockKit

@Test func parsesSecureLinks() throws {
    let link = { (string: String) in URL(string: string).flatMap(SecureLink.init(url:)) }

    #expect(link("flock://secure/watchtower") == SecureLink(site: "watchtower"))
    #expect(link("FLOCK://SECURE/My-App/") == SecureLink(site: "my-app"))

    #expect(link("https://secure/watchtower") == nil)
    #expect(link("flock://unsecure/watchtower") == nil)
    #expect(link("flock://secure") == nil)
    #expect(link("flock://secure/watchtower/extra") == nil)
    #expect(link("flock://secure/watch%20tower") == nil)
    #expect(link("flock://secure/watchtower%0A") == nil)
    #expect(link("flock://secure/\(String(repeating: "a", count: 64))") == nil)
}

@Test func tellsFlockLinksApart() throws {
    let link = { (string: String) in URL(string: string).flatMap(FlockLink.init(url:)) }

    #expect(link("flock://secure/watchtower") == .secure(SecureLink(site: "watchtower")))
    #expect(link("flock://install/mysql") == .install(InstallLink(service: "mysql")))
    #expect(link("flock://uninstall/mysql") == nil)
}

@Test func resolvesSecureLinksAgainstTheLinkedSites() throws {
    let status = try FlockJSON.decoder().decode(Status.self, from: Data("""
    {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
     "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":false,"parked":false},
              {"name":"safe","domain":"safe.test","path":"/code/safe","php":"8.5","secured":true,"parked":false}]}
    """.utf8))

    #expect(SecureLink(site: "watchtower").resolve(sites: nil) == nil)
    #expect(SecureLink(site: "watchtower").resolve(sites: status.sites) == .secure(name: "watchtower", domain: "watchtower.test"))
    #expect(SecureLink(site: "watchtower").resolve(sites: status.sites)?.confirmation == "watchtower")
    #expect(SecureLink(site: "safe").resolve(sites: status.sites) == .secured(name: "safe", domain: "safe.test"))
    #expect(SecureLink(site: "safe").resolve(sites: status.sites)?.confirmation == nil)
    #expect(SecureLink(site: "nope").resolve(sites: status.sites) == .unknown(name: "nope"))
}
