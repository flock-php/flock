import AppKit
import WebKit

/// Saves a window as a PNG from inside the app, so no screen recording permission is needed: the
/// window's frame view draws itself into a bitmap at 2x whatever the display is, WKWebViews (which
/// don't draw that way) add their own snapshots, and an attached sheet or an open menu, windows
/// of their own, are drawn on top where they are. The PNG records the scale as 192 dpi.
@MainActor
enum Capture {
    static let scale: CGFloat = 2

    static func save(_ window: NSWindow, to file: URL) async throws {
        guard let frameView = window.contentView?.superview ?? window.contentView else {
            throw CaptureError("the window has no content")
        }
        let size = frameView.bounds.size
        let rep = try bitmap(size)
        // The corners are left square: the website rounds the frame.
        draw(into: rep) {
            window.backgroundColor.setFill()
            NSRect(origin: .zero, size: size).fill()
        }
        frameView.cacheDisplay(in: frameView.bounds, to: rep)

        for webView in webViews(in: frameView) where !webView.isHiddenOrHasHiddenAncestor {
            let configuration = WKSnapshotConfiguration()
            configuration.afterScreenUpdates = true
            // The part of the page that is on screen. A web view under a header or toolbar starts
            // above its bounds' origin (obscured insets); that part is the other views'.
            var visible = webView.visibleRect
            let obscured = max(0, -visible.minY)
            visible.origin.y += obscured
            visible.size.height -= obscured
            guard visible.height > 0 else { continue }
            configuration.rect = visible
            // At the PNG's scale, not the display's (a CI runner's is 1x).
            configuration.snapshotWidth = NSNumber(value: visible.width * scale / (window.backingScaleFactor))
            let image = try await webView.takeSnapshot(configuration: configuration)
            // In the window's coordinates, which are frameView's too.
            let rect = webView.convert(visible, to: nil)
            draw(into: rep) { image.draw(in: rect) }
        }

        // Sheets (alerts too) and menus: windows in front of this one, in their order on screen.
        // (Menus aren't in orderedWindows.)
        let others = NSApp.windows.filter { $0 !== window && $0.isVisible && overlaps($0, window) }
            .sorted { $0.level.rawValue < $1.level.rawValue }
        for other in others {
            guard let view = other.contentView?.superview ?? other.contentView else { continue }
            let image = try bitmap(view.bounds.size)
            view.cacheDisplay(in: view.bounds, to: image)
            var rect = NSRect(origin: NSPoint(x: other.frame.minX - window.frame.minX, y: other.frame.minY - window.frame.minY), size: view.bounds.size)
            // A menu that opens past the window's edge is moved in, as the PNG holds the window only.
            let margin: CGFloat = 8
            rect.origin.x = max(margin, min(rect.minX, size.width - rect.width - margin))
            rect.origin.y = max(margin, min(rect.minY, size.height - rect.height - margin))
            // Their rounded corners and shadow, which the window server draws.
            let shape = NSBezierPath(roundedRect: rect, xRadius: cornerRadius(of: other), yRadius: cornerRadius(of: other))
            draw(into: rep) {
                let shadow = NSShadow()
                shadow.shadowColor = .black.withAlphaComponent(0.3)
                shadow.shadowBlurRadius = 24
                shadow.shadowOffset = NSSize(width: 0, height: -8)
                NSGraphicsContext.saveGraphicsState()
                shadow.set()
                other.backgroundColor.setFill()
                shape.fill()
                NSGraphicsContext.restoreGraphicsState()
                shape.addClip()
                image.draw(in: rect)
            }
        }

        guard let png = rep.representation(using: .png, properties: [:]) else {
            throw CaptureError("PNG encoding failed")
        }
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try PNG.withResolution(png, dpi: 96 * scale).write(to: file)
    }

    /// A sheet or menu of this window: in front of it and within (or over) its frame.
    private static func overlaps(_ other: NSWindow, _ window: NSWindow) -> Bool {
        other.sheetParent === window || other.level.rawValue >= NSWindow.Level.popUpMenu.rawValue && other.frame.intersects(window.frame)
    }

    /// The window's corner radius (AppKit's own, not public); 0 when it doesn't say.
    private static func cornerRadius(of window: NSWindow) -> CGFloat {
        guard window.responds(to: NSSelectorFromString("_cornerRadius")) else { return 0 }
        return (window.value(forKey: "_cornerRadius") as? NSNumber).map { CGFloat($0.doubleValue) } ?? 0
    }

    private static func bitmap(_ size: NSSize) throws -> NSBitmapImageRep {
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int((size.width * scale).rounded()),
                                         pixelsHigh: Int((size.height * scale).rounded()), bitsPerSample: 8,
                                         samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .calibratedRGB,
                                         bytesPerRow: 0, bitsPerPixel: 0) else {
            throw CaptureError("no bitmap for \(size)")
        }
        rep.size = size
        return rep
    }

    private static func draw(into rep: NSBitmapImageRep, _ body: () -> Void) {
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
        body()
        NSGraphicsContext.restoreGraphicsState()
    }

    private static func webViews(in view: NSView) -> [WKWebView] {
        if let web = view as? WKWebView { return [web] }
        return view.subviews.flatMap(webViews(in:))
    }
}

struct CaptureError: LocalizedError {
    let errorDescription: String?

    init(_ message: String) {
        errorDescription = message
    }
}

/// PNG chunks: the resolution (pHYs) the website reads the scale from.
enum PNG {
    /// The PNG with a pHYs chunk of that many dots per inch, right after IHDR (replacing any).
    /// AppKit's eXIf chunk goes: it says 72 dpi per point (144 at 2x), and some readers prefer it.
    static func withResolution(_ png: Data, dpi: CGFloat) -> Data {
        let bytes = [UInt8](png)
        var chunks: [[UInt8]] = []
        var offset = 8
        while offset + 12 <= bytes.count {
            let length = Int(UInt32(bigEndian: bytes[offset..<offset + 4].withUnsafeBytes { $0.loadUnaligned(as: UInt32.self) }))
            let type = String(decoding: bytes[offset + 4..<offset + 8], as: UTF8.self)
            let end = offset + 12 + length
            if type != "pHYs", type != "eXIf" {
                chunks.append(Array(bytes[offset..<end]))
            }
            offset = end
        }
        let perMetre = UInt32((dpi / 0.0254).rounded())
        var phys = Array("pHYs".utf8)
        phys += perMetre.bigEndianBytes + perMetre.bigEndianBytes + [1]
        let chunk = UInt32(9).bigEndianBytes + phys + crc32(phys).bigEndianBytes
        chunks.insert(chunk, at: 1)
        return Data(bytes[0..<8] + chunks.joined())
    }

    private static func crc32(_ bytes: [UInt8]) -> UInt32 {
        var crc: UInt32 = 0xFFFF_FFFF
        for byte in bytes {
            crc ^= UInt32(byte)
            for _ in 0..<8 {
                crc = crc & 1 == 1 ? (crc >> 1) ^ 0xEDB8_8320 : crc >> 1
            }
        }
        return ~crc
    }
}

private extension UInt32 {
    var bigEndianBytes: [UInt8] { [UInt8(self >> 24), UInt8(self >> 16 & 0xFF), UInt8(self >> 8 & 0xFF), UInt8(self & 0xFF)] }
}
