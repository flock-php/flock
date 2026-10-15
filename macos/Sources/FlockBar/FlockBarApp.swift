import AppKit
import FlockKit
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
            MenuBarLabel(model: model)
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

    static let openLink = Notification.Name("dev.flock.openLink")

    /// The flock://install link macOS handed over last, until the menu bar label takes it. Kept
    /// here because a link that starts the app can arrive before any view listens.
    private(set) static var pendingLink: InstallLink?

    static func takePendingLink() -> InstallLink? {
        defer { pendingLink = nil }
        return pendingLink
    }

    /// flock:// links (CFBundleURLTypes in build-app.sh), e.g. from the website's install buttons.
    func application(_ application: NSApplication, open urls: [URL]) {
        guard let link = urls.lazy.compactMap(InstallLink.init(url:)).first else { return }
        Self.pendingLink = link
        NotificationCenter.default.post(name: Self.openLink, object: nil)
    }
}

/// The menu bar icon. It is the only view that exists at launch, so it also opens the
/// window requested by `--snapshot`, the main window when started by hand, and Settings →
/// Services for a flock://install link.
private struct MenuBarLabel: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        LogoMark(mode: model.logoMark)
            .task {
                if let target = Snapshot.target {
                    openWindow.activating(target)
                } else if let link = AppDelegate.takePendingLink() {
                    open(link)
                } else if !AppDelegate.launchedAtLogin {
                    openMain()
                }
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.reopen)) { _ in
                openMain()
            }
            .onReceive(NotificationCenter.default.publisher(for: AppDelegate.openLink)) { _ in
                if let link = AppDelegate.takePendingLink() {
                    open(link)
                }
            }
    }

    /// The setup until Flock is set up, then Settings.
    private func openMain() {
        openWindow.activating(model.needsSetup ? WindowID.setup : WindowID.settings)
    }

    /// Before the setup the link waits: Services asks about it once Flock is set up.
    private func open(_ link: InstallLink) {
        model.handle(link)
        openMain()
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
