import Foundation
import Testing
@testable import FlockKit

@Test func parsesInstallLinks() throws {
    let link = { (string: String) in URL(string: string).flatMap(InstallLink.init(url:)) }

    #expect(link("flock://install/mysql/8.4") == InstallLink(service: "mysql", line: "8.4"))
    #expect(link("flock://install/postgres/18") == InstallLink(service: "postgres", line: "18"))
    #expect(link("flock://install/meilisearch") == InstallLink(service: "meilisearch"))
    #expect(link("FLOCK://INSTALL/MySQL/8.4/") == InstallLink(service: "mysql", line: "8.4"))

    #expect(link("https://install/mysql/8.4") == nil)
    #expect(link("flock://uninstall/mysql") == nil)
    #expect(link("flock://install") == nil)
    #expect(link("flock://install/mysql/8.4/extra") == nil)
    #expect(link("flock://install/my%20sql") == nil)
    #expect(link("flock://install/mysql/8.4%0A") == nil)
    #expect(link("flock://install/\(String(repeating: "a", count: 33))") == nil)
}

private func fixtures() throws -> ([Service], ServiceCatalog, PostgresVersions) {
    let decoder = FlockJSON.decoder()
    let services = try decoder.decode([Service].self, from: Data("""
    [{"id":"postgres","name":"PostgreSQL","available":true},
     {"id":"mysql","name":"MySQL","available":true},
     {"id":"redis","name":"Redis","available":true},
     {"id":"mariadb","name":"MariaDB","available":true},
     {"id":"typesense","name":"Typesense","available":false,"unavailable":"Typesense has no Windows build."}]
    """.utf8))
    let catalog = try decoder.decode(ServiceCatalog.self, from: Data("""
    {"services":[
      {"id":"mysql","name":"MySQL","installed":"","lines":[
        {"id":"9.7","label":"9.7 LTS","latest":"9.7.1"},{"id":"8.4","label":"8.4 LTS","latest":"8.4.7"}]},
      {"id":"redis","name":"Redis","installed":"8.4.1","line":"8.4","lines":[
        {"id":"8.10","label":"8.10"},{"id":"8.4","label":"8.4","installed":true}]}
    ]}
    """.utf8))
    let postgres = try decoder.decode(PostgresVersions.self, from: Data("""
    {"majors":[{"major":"18","latest":"18.7.0"},{"major":"17","installed":"17.4.0"}]}
    """.utf8))
    return (services, catalog, postgres)
}

@Test func resolvesInstallLinksAgainstWhatFlockOffers() throws {
    let (services, catalog, postgres) = try fixtures()
    let resolve = { (link: InstallLink) in link.resolve(services: services, catalog: catalog, postgres: postgres) }

    let mysql = try #require(resolve(InstallLink(service: "mysql", line: "8.4"))?.confirmation)
    #expect(mysql == InstallLink.Install(service: "mysql", name: "MySQL", line: "8.4", label: "8.4 LTS", version: "8.4.7"))
    #expect(resolve(InstallLink(service: "mysql"))?.confirmation?.line == "9.7")
    #expect(resolve(InstallLink(service: "postgres"))?.confirmation == InstallLink.Install(service: "postgres", name: "PostgreSQL", line: "18", label: "18", version: "18.7.0"))

    #expect(resolve(InstallLink(service: "postgres", line: "17")) == .installed(name: "PostgreSQL 17"))
    #expect(resolve(InstallLink(service: "redis", line: "8.4")) == .installed(name: "Redis 8.4"))
    #expect(resolve(InstallLink(service: "redis", line: "8.10")) == .otherInstalled(name: "Redis", installed: "8.4", requested: "8.10"))
    #expect(resolve(InstallLink(service: "typesense")) == .unavailable(name: "Typesense", reason: "Typesense has no Windows build."))
    #expect(resolve(InstallLink(service: "mysql", line: "5.7")) == .unknownVersion(name: "MySQL", line: "5.7"))
    #expect(resolve(InstallLink(service: "postgres", line: "9")) == .unknownVersion(name: "PostgreSQL", line: "9"))
    #expect(resolve(InstallLink(service: "oracle")) == .unknown(name: "oracle"))
    // In the services list, but this daemon's catalog doesn't know it.
    #expect(resolve(InstallLink(service: "mariadb")) == .unknown(name: "MariaDB"))
}

@Test func waitsForTheVersionsBeforeResolving() throws {
    let (services, catalog, postgres) = try fixtures()
    #expect(InstallLink(service: "mysql").resolve(services: [], catalog: catalog, postgres: postgres) == nil)
    #expect(InstallLink(service: "mysql").resolve(services: services, catalog: nil, postgres: postgres) == nil)
    #expect(InstallLink(service: "postgres").resolve(services: services, catalog: catalog, postgres: nil) == nil)
}
