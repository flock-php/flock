import AppKit
@testable import FlockUI
import SwiftUI

/// The app's windows, built like FlockBarApp's scenes but as plain AppKit windows the
/// screenshots can reach: the same views, sizes and window styles.
@MainActor
enum Windows {
    static func settings(_ model: AppModel) -> NSWindow {
        window(SettingsWindow(), model, title: "Flock Settings", size: NSSize(width: 860, height: 560), hiddenTitleBar: true)
    }

    static func mail(_ model: AppModel) -> NSWindow {
        window(MailWindow(), model, title: "Mail", size: NSSize(width: 980, height: 640))
    }

    static func dumps(_ model: AppModel) -> NSWindow {
        window(DumpsWindow(), model, title: "Dumps", size: NSSize(width: 900, height: 600))
    }

    static func logs(_ model: AppModel) -> NSWindow {
        window(LogsWindow(), model, title: "Logs", size: NSSize(width: 980, height: 640))
    }

    /// The menu bar panel (MenuBarExtra's window), on a solid background instead of its material.
    static func panel(_ model: AppModel) -> NSWindow {
        let controller = NSHostingController(rootView: PanelView()
            .environment(model)
            .background(Color(nsColor: .windowBackgroundColor)))
        controller.sizingOptions = [.preferredContentSize]
        let panel = ShotPanel(contentRect: NSRect(x: 0, y: 0, width: 340, height: 400), styleMask: [.borderless], backing: .buffered, defer: false)
        panel.contentViewController = controller
        panel.backgroundColor = .windowBackgroundColor
        panel.isReleasedWhenClosed = false
        show(panel)
        return panel
    }

    private static func window<Content: View>(_ content: Content, _ model: AppModel, title: String, size: NSSize, hiddenTitleBar: Bool = false) -> NSWindow {
        let controller = NSHostingController(rootView: content.environment(model))
        // The toolbar, search field and title come from the view, as in a SwiftUI scene.
        controller.sceneBridgingOptions = [.toolbars, .title]
        controller.sizingOptions = [.minSize]
        let window = ShotWindow(contentViewController: controller)
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
        window.title = title
        window.toolbarStyle = .unified
        window.isReleasedWhenClosed = false
        if hiddenTitleBar {
            window.styleMask.insert(.fullSizeContentView)
            window.titlebarAppearsTransparent = true
            window.titleVisibility = .hidden
        }
        window.setContentSize(size)
        show(window)
        return window
    }

    private static func show(_ window: NSWindow) {
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
}

/// Draws as the key window whichever app is in front, so selections and controls have their
/// active look on every machine.
final class ShotWindow: NSWindow {
    override var isKeyWindow: Bool { true }
    override var isMainWindow: Bool { true }
    override var canBecomeKey: Bool { true }
}

final class ShotPanel: NSPanel {
    override var isKeyWindow: Bool { true }
    override var canBecomeKey: Bool { true }
}
