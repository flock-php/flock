import FlockKit
import Foundation

/// Fixture data for `--snapshot settings --pane services --fixture <name>` renders of the
/// Services pane (a development aid; nothing is sent to the daemon):
/// `pg-empty`, `pg-first-install`, `pg-installed`, `pg-busy`, `pg-building`, `pg-failed`,
/// `valkey-empty`, `valkey-building`, `valkey-failed`, `valkey-installed`, and for the catalog
/// `catalog-empty`, `catalog-port`, `catalog-installing`, `catalog-failed`, `catalog-installed`.
@MainActor
enum SnapshotFixtures {
    static let log = """
    checking for gcc... clang
    checking whether the C compiler works... yes
    configuring PostgreSQL 18.0 (client only)
    make -C src/interfaces/libpq all
    clang -Wall -O2 -c -o fe-connect.o fe-connect.c
    clang -Wall -O2 -c -o fe-exec.o fe-exec.c
    make -C src/bin/psql all
    clang -Wall -O2 -c -o command.o command.c
    """

    static func load(into store: SettingsStore) {
        let name = Snapshot.fixture ?? ""
        let pgInstalled = !["pg-empty", "pg-first-install", "valkey-empty", "valkey-building", "valkey-failed"].contains(name)
        let valkeyInstalled = ["valkey-installed", "pg-installed", "pg-busy"].contains(name)
        let mysql = ["catalog-port", "catalog-failed", "catalog-installed"].contains(name)
        store.loadFixture(services: services(postgres: pgInstalled, valkey: valkeyInstalled, mysql: mysql),
                          postgres: decode(postgres(name)),
                          components: decode(components(name)),
                          catalog: decode(catalog(name, mysql: mysql)))
    }

    private static func decode<T: Decodable>(_ json: String) -> T? {
        do {
            return try FlockJSON.decoder().decode(T.self, from: Data(json.utf8))
        } catch {
            FileHandle.standardError.write(Data("fixture: \(error)\n".utf8))
            return nil
        }
    }

    private static func services(postgres: Bool, valkey: Bool, mysql: Bool) -> [Service] {
        decode("""
        [{"id":"web","name":"Web server (Caddy)","category":"Core","port":443,"installed":true,"enabled":true,"running":true,"log":"web"},
         {"id":"dns","name":"DNS (*.test)","category":"Core","port":53535,"installed":true,"enabled":true,"running":true},
         {"id":"postgres","name":"PostgreSQL","category":"Database","version":"\(postgres ? "18.6.0" : "")","port":5432,
          "installed":\(postgres),"enabled":true,"running":\(postgres),"stoppable":true,"log":"postgres",
          "env":"DB_CONNECTION=pgsql\\nDB_HOST=127.0.0.1\\nDB_PORT=5432\\nDB_DATABASE=laravel\\nDB_USERNAME=postgres\\nDB_PASSWORD=",
          "url":"postgres://postgres@127.0.0.1:5432"},
         {"id":"mysql","name":"MySQL","category":"Database","available":true,"version":"\(mysql ? "8.4.11" : "")","port":3306,
          "installed":\(mysql),"enabled":true,"running":\(mysql),"stoppable":\(mysql),"log":"mysql",
          "env":"DB_CONNECTION=mysql\\nDB_HOST=127.0.0.1\\nDB_PORT=3306\\nDB_DATABASE=laravel\\nDB_USERNAME=root\\nDB_PASSWORD=",
          "url":"mysql://root@127.0.0.1:3306"},
         {"id":"mariadb","name":"MariaDB","category":"Database","available":true,"port":3306,"installed":false},
         {"id":"mongodb","name":"MongoDB","category":"Database","available":true,"port":27017,"installed":false},
         {"id":"valkey","name":"Valkey","category":"Cache & Queue","available":true,"version":"\(valkey ? "9.1.2" : "")","port":6379,
          "installed":\(valkey),"enabled":true,"running":\(valkey),"stoppable":true,"log":"valkey",
          "env":"REDIS_CLIENT=phpredis\\nREDIS_HOST=127.0.0.1\\nREDIS_PASSWORD=null\\nREDIS_PORT=6379","url":"redis://127.0.0.1:6379"},
         {"id":"redis","name":"Redis","category":"Cache & Queue","available":true,"port":6379,"installed":false},
         {"id":"meilisearch","name":"Meilisearch","category":"Search","available":true,"port":7700,"installed":false},
         {"id":"typesense","name":"Typesense","category":"Search","available":true,"port":8108,"installed":false},
         {"id":"rustfs","name":"RustFS","category":"Storage","available":false,"unavailable":"RustFS has no build for Intel Macs.","port":9000,"installed":false},
         {"id":"mail","name":"Mail (SMTP)","category":"Mail","port":2525,"installed":true,"enabled":true,"running":true},
         {"id":"dumps","name":"Dump server","category":"Debugging","port":9912,"installed":true,"enabled":false,"running":false,"stoppable":true}]
        """) ?? []
    }

    private static func time(_ secondsAgo: TimeInterval) -> String {
        Date().addingTimeInterval(-secondsAgo).formatted(.iso8601)
    }

    private static func major(_ major: String, installed: String = "", latest: String, update: Bool = false, active: Bool = false,
                              data: Int64 = 0, eol: String, busy: String = "") -> String {
        """
        {"major":"\(major)","installed":"\(installed)","latest":"\(latest)","update":\(update),"active":\(active),
         "has_data":\(data > 0),"data_size":\(data),"eol":"\(eol)","busy":"\(busy)"}
        """
    }

    private static func postgres(_ name: String) -> String {
        let empty = name == "pg-empty" || name == "pg-first-install" || name.hasPrefix("valkey-") && name != "valkey-installed"
        let majors: [String]
        if empty {
            majors = [
                major("18", latest: "18.7.0", eol: "2030-11-14", busy: name == "pg-first-install" ? "installing" : ""),
                major("17", latest: "17.9.0", eol: "2029-11-08"),
                major("16", latest: "16.13.0", data: 12_400_000, eol: "2028-11-09"),
                major("15", latest: "15.17.0", eol: "2027-11-11"),
                major("14", latest: "14.22.0", eol: "2026-11-12"),
            ]
        } else {
            majors = [
                major("18", installed: "18.6.0", latest: "18.7.0", update: true, active: true, data: 48_234_496, eol: "2030-11-14"),
                major("17", installed: "17.9.0", latest: "17.9.0", data: 210_000_000, eol: "2029-11-08", busy: name == "pg-busy" ? "switching" : ""),
                major("16", latest: "16.13.0", data: 12_400_000, eol: "2028-11-09"),
                major("15", latest: "15.17.0", eol: "2027-11-11"),
                major("14", installed: "14.21.0", latest: "14.22.0", update: true, data: 9_100_000, eol: "2026-11-12"),
            ]
        }
        return """
        {"active":"\(empty ? "" : "18")","running":\(!empty && name != "pg-busy"),"enabled":true,"port":5432,
         "checked_at":"\(time(3 * 3600))","check_error":"\(name == "pg-failed" ? "maven.org: connection timed out" : "")",
         "majors":[\(majors.joined(separator: ","))]}
        """
    }

    private static func components(_ name: String) -> String {
        let clientJob: String
        switch name {
        case "pg-building", "pg-busy":
            clientJob = #"{"action":"install","state":"running","phase":"building","progress":-1,"error":"","started_at":"\#(time(194))"}"#
        case "pg-failed":
            clientJob = #"{"action":"install","state":"failed","phase":"building","progress":-1,"error":"configure: error: readline library not found. Use --without-readline to disable it.","started_at":"\#(time(400))"}"#
        default:
            clientJob = "null"
        }
        let clientInstalled = name == "pg-installed" || name == "valkey-installed" ? "18.0" : ""

        let redisJob: String
        switch name {
        case "valkey-building":
            redisJob = #"{"action":"install","state":"running","phase":"building","progress":-1,"error":"","started_at":"\#(time(71))"}"#
        case "valkey-failed":
            redisJob = #"{"action":"install","state":"failed","phase":"building","progress":-1,"error":"xcrun: error: invalid active developer path (/Library/Developer/CommandLineTools), missing xcrun","started_at":"\#(time(30))"}"#
        default:
            redisJob = "null"
        }
        let redisInstalled = ["valkey-installed", "pg-installed", "pg-busy"].contains(name)

        return """
        {"checked_at":"\(time(3 * 3600))","check_error":"",
         "components":[
          {"id":"valkey","name":"Valkey","description":"","installed":"\(redisInstalled ? "9.1.2" : "")","latest":"9.1.3",
           "update":\(redisInstalled),"method":"build","estimate":"about 2 minutes","job":\(redisJob)},
          {"id":"postgres-client","name":"PostgreSQL command-line tools",
           "description":"psql, pg_dump, pg_restore, pg_dumpall, createdb, dropdb",
           "installed":"\(clientInstalled)","latest":"18.0","update":false,"method":"build","estimate":"about 5 minutes","job":\(clientJob)}
         ]}
        """
    }

    private static func catalog(_ name: String, mysql: Bool) -> String {
        let mysqlJob = name == "catalog-installing"
            ? #"{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"\#(time(38))","version":"9.7.2","line":"9.7"}"#
            : "null"
        let mariadbJob = name == "catalog-failed"
            ? #"{"action":"install","state":"failed","phase":"building","progress":-1,"error":"the build failed (exit status 2) — see the build log","started_at":"\#(time(400))","version":"11.8.9","line":"11.8"}"#
            : "null"
        let update = name == "catalog-installed"
        return """
        {"checked_at":"\(time(2 * 3600))","check_error":"",
         "services":[
          {"id":"mysql","name":"MySQL","category":"Database","description":"The world's most popular open-source database. User root, no password.",
           "available":true,"method":"download","estimate":"about 170 MB download","installed":"\(mysql ? "8.4.11" : "")","line":"\(mysql ? "8.4" : "")",
           "latest":"\(mysql ? (update ? "8.4.12" : "8.4.11") : "")","update":\(mysql && update),"port":3306,"default_port":3306,
           "lines":[{"id":"9.7","label":"9.7 LTS","latest":"9.7.2","installed":false,"has_data":\(update)},
                    {"id":"8.4","label":"8.4 LTS","latest":"\(update ? "8.4.12" : "8.4.11")","installed":\(mysql),"has_data":\(mysql)},
                    {"id":"8.0","label":"8.0 (end of life)","latest":"8.0.46","installed":false,"has_data":false}],
           "job":\(mysqlJob)},
          {"id":"mariadb","name":"MariaDB","category":"Database","description":"The community fork of MySQL, compatible with its clients. User root, no password.",
           "available":true,"method":"build","estimate":"about 5 minutes","installed":"","port":\(mysql ? 3307 : 3306),"default_port":3306,
           "lines":[{"id":"12.3","label":"12.3 LTS","latest":"12.3.3"},{"id":"11.8","label":"11.8 LTS","latest":"11.8.9"},
                    {"id":"11.4","label":"11.4 LTS","latest":"11.4.13"},{"id":"10.11","label":"10.11 LTS","latest":"10.11.19"}],
           "job":\(mariadbJob)},
          {"id":"mongodb","name":"MongoDB","category":"Database","available":true,"method":"download","estimate":"about 80 MB download",
           "port":27017,"default_port":27017,"lines":[{"id":"9.0","label":"9.0","latest":"9.0.4"}],"job":null},
          {"id":"redis","name":"Redis","category":"Cache & Queue","description":"The in-memory store for your cache, queues and sessions, from Redis Ltd.",
           "available":true,"method":"build","estimate":"about 1 minute","port":6380,"default_port":6379,
           "lines":[{"id":"8.10","label":"8.10","latest":"8.10.2"},{"id":"7.4","label":"7.4","latest":"7.4.11"}],"job":null},
          {"id":"meilisearch","name":"Meilisearch","category":"Search","available":true,"method":"download","estimate":"about 350 MB download",
           "port":7700,"default_port":7700,"lines":[{"id":"1","label":"1.x","latest":"1.55.0"}],"job":null},
          {"id":"typesense","name":"Typesense","category":"Search","available":true,"method":"download","estimate":"about 40 MB download",
           "port":8108,"default_port":8108,"lines":[{"id":"30","label":"30","latest":"30.2"}],"job":null},
          {"id":"rustfs","name":"RustFS","category":"Storage","available":false,"unavailable":"RustFS has no build for Intel Macs.","lines":[],"job":null}
         ]}
        """
    }
}
