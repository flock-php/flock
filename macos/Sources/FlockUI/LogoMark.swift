import AppKit
import SwiftUI

/// Flock's logomark, from the template images that build-app.sh puts in the app bundle
/// (rendered by design/icongen). Template images take the foreground style, and in the menu bar
/// macOS colors them for its appearance. Without the bundle (`swift run`), an SF Symbol bird.
struct LogoMark: View {
    enum Mode {
        case running, stopped, warning

        /// The menu bar's 18-point image, or a 128-pixel one to scale down inside windows.
        func resource(large: Bool) -> String {
            let suffix = switch self {
            case .running: ""
            case .stopped: "Stopped"
            case .warning: "Warning"
            }
            return (large ? "LogoMark" : "MenuBar") + suffix
        }

        var fallback: String {
            switch self {
            case .running: "bird.fill"
            case .stopped: "bird"
            case .warning: "exclamationmark.triangle.fill"
            }
        }
    }

    var mode: Mode = .running
    /// Points; nil keeps the image's own 18-point size (the menu bar's).
    var size: CGFloat? = nil

    var body: some View {
        if let size, let image = Self.image(mode.resource(large: true)) {
            Image(nsImage: image).resizable().frame(width: size, height: size)
        } else if size == nil, let image = Self.image(mode.resource(large: false)) {
            Image(nsImage: image)
        } else {
            Image(systemName: mode.fallback)
                .font(size.map { .system(size: $0 * 0.8) } ?? .body)
        }
    }

    @MainActor private static var cache: [String: NSImage] = [:]

    @MainActor private static func image(_ resource: String) -> NSImage? {
        if let image = cache[resource] {
            return image
        }
        guard let image = Bundle.main.image(forResource: resource) else {
            return nil
        }
        image.isTemplate = true
        cache[resource] = image
        return image
    }
}

extension Color {
    /// The brand's logomark color: teal on light backgrounds, mint on dark (as the Windows app's
    /// notification area icon).
    static let flockMark = Color(nsColor: NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            ? NSColor(srgbRed: 0xEF / 255, green: 0xFC / 255, blue: 0xFD / 255, alpha: 1)
            : NSColor(srgbRed: 0x0D / 255, green: 0x7A / 255, blue: 0x8C / 255, alpha: 1)
    })

    /// Ember, the brand's color for what needs attention now.
    static let flockEmber = Color(.sRGB, red: 0xE8 / 255, green: 0x79 / 255, blue: 0x2B / 255)
}

/// The app icon, as Finder shows it (the tile is 80% of it: Apple's icon grid leaves room for the shadow).
struct AppIconView: View {
    var size: CGFloat

    var body: some View {
        Image(nsImage: NSApp.applicationIconImage)
            .resizable()
            .frame(width: size, height: size)
    }
}
