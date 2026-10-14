import Foundation
import Testing
@testable import FlockKit

@Suite struct InstallationTests {
    private let dir: URL

    init() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("flock-installation-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    private func file(_ name: String, _ contents: String) throws -> URL {
        let url = dir.appendingPathComponent(name)
        try Data(contents.utf8).write(to: url)
        return url
    }

    @Test func needsSetupOnlyWithABundledCLI() throws {
        let missing = dir.appendingPathComponent("missing")
        let bundled = try file("bundled", "v2")
        #expect(Installation(bundledFlock: bundled, installedFlock: missing, launchAgent: missing).needsSetup)
        #expect(!Installation(bundledFlock: nil, installedFlock: missing, launchAgent: missing).needsSetup)

        let installed = try file("installed", "v2")
        let agent = try file("agent.plist", "")
        let setUp = Installation(bundledFlock: bundled, installedFlock: installed, launchAgent: agent)
        #expect(setUp.isSetUp && !setUp.needsSetup)
        // The CLI alone isn't enough: without the LaunchAgent nothing starts the daemon.
        #expect(Installation(bundledFlock: bundled, installedFlock: installed, launchAgent: missing).needsSetup)
    }

    @Test func updatesOnlyWhatThisAppInstalled() throws {
        let bundled = try file("bundled", "v2")
        let installed = try file("installed", "v1")
        let agent = try file("agent.plist", "")
        let installation = Installation(bundledFlock: bundled, installedFlock: installed, launchAgent: agent)
        let v1 = Installation.hash(installed)

        #expect(installation.needsUpdate(recorded: nil))
        #expect(installation.needsUpdate(recorded: v1))
        // Installed by hand from another build since: left alone.
        #expect(!installation.needsUpdate(recorded: Installation.hash(bundled) + "x"))

        try Data("v2".utf8).write(to: installed)
        #expect(!installation.needsUpdate(recorded: v1))
        #expect(!Installation(bundledFlock: nil, installedFlock: installed, launchAgent: agent).needsUpdate(recorded: nil))
    }

    @Test func runStreamsOutputAndReportsFailures() async throws {
        let script = try file("flock", "#!/bin/sh\necho \"✓ first $1\"\necho second >&2\nprintf 'no newline'\nexit 3\n")
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)

        let lines = Lines()
        let result = await Installation.run(script, ["install"]) { lines.add($0) }
        #expect(!result.ok)
        #expect(result.output == "✓ first install\nsecond\nno newline")
        #expect(lines.all == ["✓ first install", "second", "no newline"])

        let declined = Installation.Result(ok: false, output: "Error: privileged setup: administrator permission was declined")
        #expect(declined.permissionDeclined)
    }
}

private final class Lines: @unchecked Sendable {
    private let lock = NSLock()
    private var lines: [String] = []
    func add(_ line: String) { lock.withLock { lines.append(line) } }
    var all: [String] { lock.withLock { lines } }
}
