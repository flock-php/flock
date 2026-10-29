import AppKit
@testable import FlockUI

/// The shots for the website's docs: each opens a window on the demo data, waits for it to settle
/// and saves it as {id}-macos-{theme}.png. The ids are the docs' screenshot:… placeholders and the
/// same as in windows/FlockScreenshots/Screenshots.cs.
@MainActor
final class Screenshots {
    /// One screenshot and how to show it.
    struct Shot {
        let id: String
        let show: @MainActor (Screenshots) async throws -> NSWindow
    }

    static let all: [Shot] = [
        Shot(id: "settings-general-project-folders") { try await $0.settingsAtEnd(.general) },
        Shot(id: "settings-sites") { try await $0.site("shop") },
        Shot(id: "settings-sites-https") { try await $0.site("acme") },
        Shot(id: "settings-sites-add-domain") { try await $0.addDomain("acme", typing: "admin") },
        Shot(id: "settings-sites-pin") { try await $0.pinMenu("shop") },
        Shot(id: "settings-php") { await $0.settings(.php) },
        Shot(id: "settings-node") { await $0.settings(.node) },
        Shot(id: "settings-services-postgres") { await $0.service("postgres") },
        Shot(id: "settings-services-install") { await $0.service("mariadb") },
        Shot(id: "settings-debugger") { await $0.settings(.debugger) },
        Shot(id: "logs-window") { await $0.log(site: "acme") },
        Shot(id: "mail-window") { try await $0.mail() },
        Shot(id: "dumps-window") { await $0.dumps() },
        Shot(id: "panel-running") { await $0.panel() },
    ]

    let options: Options
    let theme: String
    private(set) var failures = 0
    private var model: AppModel!
    private var opened: [NSWindow] = []

    init(options: Options, theme: String) {
        self.options = options
        self.theme = theme
    }

    func run() async {
        model = AppModel()
        let shots = Self.all.filter { options.only?.contains($0.id) ?? true }
        if let unknown = options.only?.subtracting(Self.all.map(\.id)), !unknown.isEmpty {
            FileHandle.standardError.write(Data("unknown shots: \(unknown.sorted().joined(separator: ", "))\n".utf8))
            failures += unknown.count
        }
        for shot in shots {
            let file = options.out.appendingPathComponent("\(shot.id)-macos-\(theme).png")
            do {
                await model.refresh()
                let window = try await shot.show(self)
                // Panes load their data, lists lay out, web views render.
                try await Task.sleep(for: .seconds(1.5))
                try await Capture.save(window, to: file)
                print(file.path)
            } catch {
                failures += 1
                FileHandle.standardError.write(Data("\(shot.id) (\(theme)) failed: \(error)\n".utf8))
            }
            close()
        }
        NSApp.stop(nil)
        // stop() takes effect after the next event.
        NSApp.postEvent(NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0,
                                           windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!, atStart: false)
    }

    private func close() {
        for window in opened {
            if let sheet = window.attachedSheet {
                window.endSheet(sheet)
            }
            window.close()
        }
        opened = []
        UI.closeMenus()
    }

    private func open(_ window: NSWindow) -> NSWindow {
        opened.append(window)
        return window
    }

    // MARK: Shots

    func settings(_ pane: SettingsPane) async -> NSWindow {
        model.showSettings(pane)
        let window = open(Windows.settings(model))
        await UI.settle()
        return window
    }

    /// A Settings pane scrolled to its last section. The window is a little shorter than usual, so
    /// the section above starts at the top instead of showing its last pixels.
    func settingsAtEnd(_ pane: SettingsPane, height: CGFloat = 530) async throws -> NSWindow {
        let window = await settings(pane)
        window.setContentSize(NSSize(width: window.contentView?.frame.width ?? 860, height: height))
        await UI.settle()
        try UI.scrollToEnd(window)
        await UI.settle()
        return window
    }

    func site(_ name: String) async throws -> NSWindow {
        let window = await settings(.sites)
        // The list shows the sites in the status's order.
        let sites = model.status?.sites ?? []
        guard let index = sites.firstIndex(where: { $0.name == name }) else {
            throw CaptureError("no site \(name)")
        }
        try UI.selectRow(in: window, index: index, of: sites.count)
        await UI.settle()
        return window
    }

    /// The Add Domain alert of a site, with a domain typed in.
    func addDomain(_ site: String, typing domain: String) async throws -> NSWindow {
        let window = try await self.site(site)
        try UI.click(in: window, text: "Add Domain…")
        try await UI.type(domain, inSheetOf: window)
        return window
    }

    /// A site's pin menu, open.
    func pinMenu(_ site: String) async throws -> NSWindow {
        let window = try await self.site(site)
        try await UI.openMenu(in: window, containing: "to Project…")
        return window
    }

    func service(_ id: String) async -> NSWindow {
        model.showSettings(.services, service: id)
        return await settings(.services)
    }

    func log(site: String) async -> NSWindow {
        model.logs.requested = .site(site)
        let window = open(Windows.logs(model))
        await UI.settle()
        return window
    }

    func mail() async throws -> NSWindow {
        let window = open(Windows.mail(model))
        await UI.settle()
        try UI.selectRow(in: window, index: 0)
        return window
    }

    func dumps() async -> NSWindow {
        let window = open(Windows.dumps(model))
        await UI.settle()
        return window
    }

    func panel() async -> NSWindow {
        let window = open(Windows.panel(model))
        await UI.settle()
        return window
    }
}
