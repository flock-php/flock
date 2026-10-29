import AppKit
import Foundation

/// `swift run FlockScreenshots --out <dir> [--only id,id] [--theme light|dark]` renders
/// Screenshots.all with the demo data in tools/screenshots/fixtures into
/// <dir>/{id}-macos-{light|dark}.png. Each theme runs in a process of its own, started with the
/// same language, locale, accent colour and scroll bars on every machine.
///
/// `FlockScreenshots --serve` only runs the fake daemon, for trying the fixtures with the real app:
/// `FLOCK_HOME=<the folder it prints> swift run FlockBar` (or `FlockBar --selftest`).
if CommandLine.arguments.dropFirst() == ["--serve"] {
    let home = try FakeDaemon.scratchHome()
    let daemon = try FakeDaemon(fixtures: FakeDaemon.fixtures, home: home)
    print("FLOCK_HOME=\(home.path)")
    fflush(stdout)
    withExtendedLifetime(daemon) { dispatchMain() }
}
guard let options = Options(CommandLine.arguments.dropFirst()) else {
    FileHandle.standardError.write(Data("usage: FlockScreenshots --out <dir> [--only id,id] [--theme light|dark]\n".utf8))
    exit(2)
}
guard let theme = options.theme, Options.isRelaunched else {
    exit(Options.relaunch(themes: options.theme.map { [$0] } ?? ["light", "dark"]))
}

// FlockPaths and with it api.json point into a scratch folder, so the app talks to the fake daemon only.
let home = try FakeDaemon.scratchHome()
setenv("FLOCK_HOME", home.path, 1)
// The app's preferences (filters, toggles) start from their defaults, not from an earlier run.
UserDefaults.standard.removePersistentDomain(forName: ProcessInfo.processInfo.processName)

let daemon = try FakeDaemon(fixtures: FakeDaemon.fixtures, home: home)

let failures = MainActor.assumeIsolated {
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)
    app.appearance = NSAppearance(named: theme == "dark" ? .darkAqua : .aqua)
    let screenshots = Screenshots(options: options, theme: theme)
    Task { await screenshots.run() }
    app.run()
    return screenshots.failures
}

daemon.stop()
UserDefaults.standard.removePersistentDomain(forName: ProcessInfo.processInfo.processName)
try? FileManager.default.removeItem(at: home)
exit(Int32(failures))

struct Options {
    var out: URL
    var theme: String?
    var only: Set<String>?

    /// Nil when the arguments make no sense. Ignores the -Apple… defaults relaunch adds.
    init?(_ args: ArraySlice<String>) {
        var out: URL?
        var args = Array(args)
        while let i = args.firstIndex(where: { $0.hasPrefix("-Apple") }), i + 1 < args.count {
            args.removeSubrange(i...i + 1)
        }
        guard args.count % 2 == 0 else { return nil }
        for i in stride(from: 0, to: args.count, by: 2) {
            switch (args[i], args[i + 1]) {
            case let ("--out", value): out = URL(fileURLWithPath: value).standardizedFileURL
            case let ("--theme", value) where value == "light" || value == "dark": theme = value
            case let ("--only", value): only = Set(value.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) })
            default: return nil
            }
        }
        guard let out else { return nil }
        self.out = out
    }

    /// This Mac's settings that change how the windows look, overridden for one process: the
    /// argument domain comes before the user's defaults.
    private static let defaults = [
        "-AppleLanguages", "(en-US)",
        "-AppleLocale", "en_US",
        // Blue, the default accent and highlight colour.
        "-AppleAccentColor", "4",
        "-AppleHighlightColor", "0.698039 0.843137 1.000000 Blue",
        "-AppleShowScrollBars", "WhenScrolling",
    ]

    static var isRelaunched: Bool { CommandLine.arguments.contains(defaults[0]) }

    /// Runs this program once per theme with the same arguments and the defaults above.
    static func relaunch(themes: [String]) -> Int32 {
        var arguments = Array(CommandLine.arguments.dropFirst())
        if let i = arguments.firstIndex(of: "--theme"), i + 1 < arguments.count {
            arguments.removeSubrange(i...i + 1)
        }
        var failures: Int32 = 0
        for theme in themes {
            let process = Process()
            process.executableURL = Bundle.main.executableURL
            process.arguments = arguments + ["--theme", theme] + defaults
            do {
                try process.run()
                process.waitUntilExit()
                failures += process.terminationStatus
            } catch {
                FileHandle.standardError.write(Data("could not start \(process.executableURL?.path ?? ""): \(error)\n".utf8))
                failures += 1
            }
        }
        return failures
    }
}
