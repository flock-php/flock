import AppKit
import WebKit

/// Development aid: `FlockBar --snapshot <mail|dumps|logs> <file.png>` opens that window,
/// (optionally `--appearance light|dark`, `--pane`, `--service`, `--fixture`) renders it into a PNG from inside the app (no screen recording permission needed) and exits.
@MainActor
enum Snapshot {
    static let target: String? = argument(after: "--snapshot", offset: 1)
    static let path: String? = argument(after: "--snapshot", offset: 2)
    /// `--pane <name>` picks the Settings pane to show.
    static let pane: String? = argument(after: "--pane", offset: 1)
    /// `--service <id>` picks the service in Settings → Services.
    static let service: String? = argument(after: "--service", offset: 1)
    /// `--fixture <name>` shows fixture data (see SnapshotFixtures) instead of the daemon's, and
    /// changes nothing on the daemon.
    static let fixture: String? = argument(after: "--fixture", offset: 1)
    static var isFixture: Bool { fixture != nil }
    /// `--sheet <name>` opens a sheet of the pane (e.g. `uninstall`, `log`); it is saved next to the PNG as `-sheet.png`.
    static let sheet: String? = argument(after: "--sheet", offset: 1)

    private static func argument(after flag: String, offset: Int) -> String? {
        let args = CommandLine.arguments
        guard let i = args.firstIndex(of: flag), i + offset < args.count else { return nil }
        return args[i + offset]
    }

    static func isTarget(_ id: String) -> Bool { target == id }

    static func captureIfRequested(_ id: String) async {
        guard target == id, let path else { return }
        if let name = argument(after: "--appearance", offset: 1) {
            NSApp.appearance = NSAppearance(named: name == "dark" ? .darkAqua : .aqua)
        }
        if let size = argument(after: "--size", offset: 1)?.split(separator: "x").compactMap({ Double($0) }), size.count == 2 {
            try? await Task.sleep(for: .seconds(1))
            NSApp.windows.first { $0.isVisible && $0.identifier?.rawValue.contains(id) == true }?.setContentSize(NSSize(width: size[0], height: size[1]))
        }
        try? await Task.sleep(for: .seconds(4))

        guard let window = NSApp.windows.first(where: { $0.isVisible && $0.identifier?.rawValue.contains(id) == true })
            ?? NSApp.windows.first(where: { $0.isVisible && $0.contentView != nil && $0.title.lowercased() == id }),
            let frameView = window.contentView?.superview
        else {
            FileHandle.standardError.write(Data("snapshot: window \(id) not found\n".utf8))
            exit(1)
        }

        let bounds = frameView.bounds
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        try? await Task.sleep(for: .milliseconds(300))
        guard let rep = frameView.bitmapImageRepForCachingDisplay(in: bounds) else { exit(1) }
        frameView.cacheDisplay(in: bounds, to: rep)

        let image = NSImage(size: bounds.size)
        image.addRepresentation(rep)

        // WKWebView content is not part of cacheDisplay; draw its own snapshots on top.
        for webView in webViews(in: frameView) {
            guard let shot = try? await webView.takeSnapshot(configuration: nil) else { continue }
            let rect = webView.convert(webView.bounds, to: frameView)
            image.lockFocus()
            shot.draw(in: rect)
            image.unlockFocus()
        }

        if let sheet = window.attachedSheet, let sheetView = sheet.contentView?.superview,
           let sheetRep = sheetView.bitmapImageRepForCachingDisplay(in: sheetView.bounds) {
            sheetView.cacheDisplay(in: sheetView.bounds, to: sheetRep)
            let sheetPath = (path as NSString).deletingPathExtension + "-sheet.png"
            try? sheetRep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: sheetPath))
        }

        guard let tiff = image.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:])
        else { exit(1) }
        try? png.write(to: URL(fileURLWithPath: path))
        exit(0)
    }

    private static func webViews(in view: NSView) -> [WKWebView] {
        if let web = view as? WKWebView { return [web] }
        return view.subviews.flatMap(webViews(in:))
    }
}
