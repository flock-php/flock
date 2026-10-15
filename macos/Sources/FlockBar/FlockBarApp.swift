import AppKit
import SwiftUI

/// Note on `State`: with only the Command Line Tools installed, `@State` cannot be used —
/// the macOS SDK expands it as a macro whose plugin ships with Xcode only. The property
/// wrapper type itself works, so views declare `State` values explicitly instead.
struct FlockBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let modelState = State<AppModel>(initialValue: AppModel())
    private var model: AppModel {
        get { modelState.wrappedValue }
        nonmutating set { modelState.wrappedValue = newValue }
    }

    var body: some Scene {
        MenuBarExtra {
            PanelView()
                .environment(model)
        } label: {
            MenuBarLabel(mode: model.logoMark, needsSetup: model.needsSetup)
        }
        .menuBarExtraStyle(.window)

        Window("Mail", id: WindowID.mail) {
            MailWindow()
                .environment(model)
        }
        .defaultSize(width: 980, height: 640)

        Window("Dumps", id: WindowID.dumps) {
            DumpsWindow()
                .environment(model)
        }
        .defaultSize(width: 900, height: 600)

        Window("Logs", id: WindowID.logs) {
            LogsWindow()
                .environment(model)
        }
        .defaultSize(width: 980, height: 640)

        Window("Flock Settings", id: WindowID.settings) {
            SettingsWindow()
                .environment(model)
        }
        .defaultSize(width: 860, height: 560)
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentMinSize)

        Window("Set Up Flock", id: WindowID.setup) {
            SetupWindow()
                .environment(model)
        }
        .windowResizability(.contentSize)
        .defaultPosition(.center)
    }
}

/// Opens the main window when the app is started by hand (Finder, Spotlight, Launchpad), also
/// while it already runs. The panel belongs to the menu bar icon; the login item stays quiet.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    static let reopen = Notification.Name("dev.flock.reopen")

    /// Whether this launch is the login item's, read from the launch's Apple event.
    private(set) static var launchedAtLogin = false

    func applicationWillFinishLaunching(_ notification: Notification) {
        let event = NSAppleEventManager.shared().currentAppleEvent
        Self.launchedAtLogin = event?.eventID == kAEOpenApplication
            && event?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        NotificationCenter.default.post(name: Self.reopen, object: nil)
        return false
    }
}

/// The menu bar icon. It is the only view that exists at launch, so it also opens the
/// window requested by `--snapshot`, or the main window when started by hand.
private struct MenuBarLabel: View {
    let mode: LogoMark.Mode
    let needsSetup: Bool
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        LogoMark(mode: mode)
            .task {
                if let target = Snapshot.target {
                    openWindow.activating(target)
                } else if !AppDelegate.launchedAtLogin {
                    openMain()
                }
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.reopen)) { _ in
                openMain()
            }
    }

    /// The setup until Flock is set up, then Settings.
    private func openMain() {
        openWindow.activating(needsSetup ? WindowID.setup : WindowID.settings)
    }
}

enum WindowID {
    static let mail = "mail"
    static let dumps = "dumps"
    static let logs = "logs"
    static let settings = "settings"
    static let setup = "setup"
}

extension OpenWindowAction {
    /// Agent apps (no Dock icon) don't come to the front on their own when a window opens.
    @MainActor
    func activating(_ id: String) {
        callAsFunction(id: id)
        NSApp.activate()
    }
}
