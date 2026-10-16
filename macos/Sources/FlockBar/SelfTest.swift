import Foundation
import FlockKit

/// Exercises every endpoint the app uses and reports decoding problems.
enum SelfTest {
    static func run() -> Int32 {
        let semaphore = DispatchSemaphore(value: 0)
        let result = Result()

        Task.detached {
            let client = FlockClient()

            func check(_ name: String, _ body: () async throws -> String) async {
                do {
                    print("OK    \(name): \(try await body())")
                } catch {
                    result.failures += 1
                    print("FAIL  \(name): \(error)")
                }
            }

            await check("GET /v1/status") {
                let s = try await client.status()
                return "v\(s.version), \(s.sites.count) sites, \(s.processes.count) processes, mail \(s.mailCount), dumps \(s.dumpCount)"
            }
            await check("POST /v1/settings (no-op)") {
                let s = try await client.status()
                let after = try await client.updateSettings(xdebug: s.xdebug, dumps: s.dumps)
                return "xdebug \(after.xdebug), dumps \(after.dumps)"
            }
            await check("GET /v1/mail") {
                let list = try await client.mail()
                if let first = list.first {
                    let detail = try await client.mail(id: first.id)
                    let raw = try await client.rawMail(id: first.id)
                    var result = "\(list.count) messages; first: html \(detail.html.count)B, text \(detail.text.count)B, \(detail.attachments.count) attachments, raw \(raw.count)B"
                    if detail.html.contains("cid:") {
                        result += " — WARNING: html still references cid: images"
                    }
                    if let attachment = detail.attachments.first {
                        let data = try await client.attachment(mailID: first.id, index: attachment.index)
                        result += ", attachment \(data.count)B"
                    }
                    return result
                }
                return "0 messages"
            }
            await check("GET /v1/dumps") {
                let dumps = try await client.dumps()
                guard let first = dumps.first else { return "0 dumps" }
                let origin = first.request.map { "\($0.method) \($0.uri)" } ?? first.commandLine ?? "?"
                return "\(dumps.count) dumps; newest from \(first.shortSource ?? "unknown source") via \(origin), html \(first.html.count)B, text \(first.text.count)B"
            }
            await check("GET /v1/logs") {
                let index = try await client.logs()
                var result = "services \(index.services), sites \(index.sites.count)"
                if let service = index.services.first {
                    result += "; \(service): \(try await client.serviceLog(service, lines: 5).count)B"
                }
                if let site = index.sites.first {
                    result += "; \(site): \(try await client.siteLog(site, limit: 20).count) entries"
                }
                return result
            }

            await check("GET /v1/config") {
                let c = try await client.config()
                return "tld \(c.tld), php \(c.php.map(\.fullVersion)) (default \(c.defaultPHP)), node \(c.node) (default \(c.defaultNode)), parked \(c.parked.count), smtp :\(c.ports.smtp)"
            }
            await check("GET /v1/services") {
                let services = try await client.services()
                return services.map { "\($0.id)[\($0.category)]\($0.running ? "↑" : "↓")\($0.port > 0 ? ":\($0.port)" : "")" }.joined(separator: " ")
            }

            await check("GET /v1/php/versions") {
                let catalog = try await client.phpVersions()
                let rows = catalog.versions.map { "\($0.minor):\($0.status.rawValue)\($0.job.map { "[\($0.state.rawValue)]" } ?? "")" }
                return "checked \(catalog.checkedAt.map { "\($0)" } ?? "never"), \(rows.joined(separator: " "))"
            }

            semaphore.signal()
        }

        semaphore.wait()
        let failed = result.failures
        print(failed == 0 ? "selftest passed" : "selftest: \(failed) failure(s)")
        return failed == 0 ? 0 : 1
    }
}

/// Only mutated by the single detached task, and read after it signalled completion.
private final class Result: @unchecked Sendable {
    var failures = 0
}
