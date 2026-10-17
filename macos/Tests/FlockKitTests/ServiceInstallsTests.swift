import Foundation
import Testing
@testable import FlockKit

@Test func decodesPostgresVersions() throws {
    let versions = try FlockJSON.decoder().decode(PostgresVersions.self, from: Data("""
    {"active":"18","running":true,"enabled":true,"port":5432,
     "checked_at":"2026-10-14T08:00:00Z","check_error":"",
     "majors":[
      {"major":"18","installed":"18.6","latest":"18.7","update":true,"yanked":"crashes on start","active":true,
       "has_data":true,"data_size":48234496,"eol":"2030-11-14","busy":""},
      {"major":"17","installed":"17.4","latest":"17.4","update":false,"prerelease":true,"active":false,
       "has_data":true,"data_size":1024,"eol":"2029-11-08","busy":"switching"},
      {"major":"16","installed":"","latest":"16.10.0","update":false,"active":false,
       "has_data":true,"data_size":2048,"eol":"2028-11-09","busy":"defragmenting"},
      {"major":"15","installed":"","latest":"","update":false,"active":false,
       "has_data":false,"data_size":0,"eol":"","busy":""},
      {"major":"14"}
     ]}
    """.utf8))

    #expect(versions.active == "18" && versions.running && versions.enabled && versions.port == 5432)
    #expect(versions.checkedAt == FlockJSON.parseDate("2026-10-14T08:00:00Z"))
    #expect(versions.majors.map(\.major) == ["18", "17", "16", "15", "14"])
    #expect(versions.activeMajor?.installed == "18.6")
    #expect(versions.hasInstalled && versions.hasUpdate && versions.isBusy)

    let pg18 = versions.majors[0]
    #expect(pg18.update && pg18.active && pg18.hasData && pg18.dataSize == 48_234_496 && pg18.busy == .idle && !pg18.isBusy)
    #expect(pg18.yanked == "crashes on start" && !pg18.prerelease && versions.majors[1].prerelease && versions.majors[1].yanked.isEmpty)
    #expect(versions.majors[1].busy == .switching)
    // A state this app doesn't know yet is still busy.
    #expect(versions.majors[2].busy == .other("defragmenting") && versions.majors[2].isBusy)
    #expect(!versions.majors[2].isInstalled && versions.majors[2].hasData)
    let pg14 = versions.majors[4]
    #expect(!pg14.isInstalled && pg14.latest.isEmpty && pg14.eolDate == nil && pg14.busy == .idle)
}

@Test func decodesPostgresVersionsNeverCheckedAndEmpty() throws {
    let versions = try FlockJSON.decoder().decode(PostgresVersions.self, from: Data("""
    {"active":"","running":false,"enabled":true,"port":5432,"checked_at":null,"check_error":"offline","majors":null}
    """.utf8))
    #expect(versions.checkedAt == nil && versions.checkError == "offline")
    #expect(versions.majors.isEmpty && versions.activeMajor == nil && !versions.hasInstalled && !versions.isBusy)
}

@Test func tellsWhereAPostgresMajorStandsUpstream() throws {
    let major = { (eol: String) in
        try FlockJSON.decoder().decode(PostgresMajor.self, from: Data(#"{"major":"14","eol":"\#(eol)"}"#.utf8))
    }
    let now = try #require(FlockJSON.parseDate("2026-10-14T08:00:00Z"))
    let ended = try major("2026-11-12")
    #expect(ended.support(now: try #require(FlockJSON.parseDate("2026-11-13T00:00:00Z"))) == .ended(try #require(ended.eolDate)))
    #expect(try major("2026-11-12").support(now: now) == .endingSoon(try #require(major("2026-11-12").eolDate)))
    if case .supported = try major("2030-11-14").support(now: now) {} else { Issue.record("2030 is supported") }
    #expect(try major("").support(now: now) == .unknown)
    #expect(try major("soon").support(now: now) == .unknown)
}

@Test func decodesTheServiceCatalog() throws {
    let catalog = try FlockJSON.decoder().decode(ServiceCatalog.self, from: Data("""
    {"checked_at":"2026-10-14T08:00:00Z","check_error":"Typesense: offline",
     "services":[
      {"id":"mysql","name":"MySQL","category":"Database","description":"The most popular database","available":true,"unavailable":"",
       "estimate":"200 MB download","installed":"8.4.6","line":"8.4","latest":"8.4.7","update":true,
       "port":3306,"default_port":3306,
       "lines":[{"id":"9.4","label":"9.4 Innovation","latest":"9.4.0","installed":false,"has_data":false},
                {"id":"8.4","label":"8.4 LTS","latest":"8.4.7","installed":true,"has_data":true}],
       "job":null},
      {"id":"mariadb","name":"MariaDB","category":"Database","available":true,"estimate":"about 90 MB download",
       "installed":"","port":3307,"default_port":3306,"lines":[{"id":"11.8","label":"11.8 LTS"}],
       "job":{"action":"install","state":"running","phase":"downloading","progress":-1,"error":"","started_at":"2026-10-14T08:01:00Z",
              "version":"11.8.3","line":"11.8"}},
      {"id":"typesense","name":"Typesense","category":"Search","available":false,"unavailable":"Typesense has no Windows build.",
       "lines":null},
      {"id":"future"}
     ]}
    """.utf8))

    #expect(catalog.checkedAt != nil && catalog.checkError == "Typesense: offline")
    #expect(catalog.services.map(\.id) == ["mysql", "mariadb", "typesense", "future"])
    #expect(catalog.hasRunningJob)

    let mysql = try #require(catalog["mysql"])
    #expect(mysql.isInstalled && mysql.update && mysql.line == "8.4" && mysql.latest == "8.4.7")
    #expect(mysql.lines.map(\.label) == ["9.4 Innovation", "8.4 LTS"] && mysql.preferredLine?.id == "8.4")
    #expect(mysql.lines[1].hasData && mysql.lines[1].installed && !mysql.lines[0].hasData)

    let mariadb = try #require(catalog["mariadb"])
    #expect(!mariadb.isInstalled && mariadb.port == 3307 && mariadb.defaultPort == 3306 && mariadb.preferredLine?.id == "11.8")
    let job = try #require(mariadb.job)
    #expect(job.isRunning && job.version == "11.8.3" && job.line == "11.8" && job.job.phase == .downloading && job.job.startedAt != nil)
    #expect(mariadb.lines[0].latest.isEmpty && !mariadb.lines[0].installed)

    let typesense = try #require(catalog["typesense"])
    #expect(!typesense.available && typesense.unavailable.hasPrefix("Typesense") && typesense.lines.isEmpty)

    let future = try #require(catalog["future"])
    #expect(future.name == "future" && future.available && future.category == "Other" && future.preferredLine == nil)
}
