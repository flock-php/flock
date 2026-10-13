import AppKit
import SwiftUI

/// Note on `State`: with only the Command Line Tools installed, `@State` cannot be used —
/// the macOS SDK expands it as a macro whose plugin ships with Xcode only. The property
/// wrapper type itself works, so views declare `State` values explicitly instead.
struct FlockBarApp: App {
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
            MenuBarLabel(symbol: model.menuBarSymbol)
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
    }
}

/// The menu bar icon. It is the only view that exists at launch, so it also opens the
/// window requested by `--snapshot`.
private struct MenuBarLabel: View {
    let symbol: String
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Image(systemName: symbol)
            .task {
                if let target = Snapshot.target {
                    openWindow.activating(target)
                }
            }
    }
}

enum WindowID {
    static let mail = "mail"
    static let dumps = "dumps"
    static let logs = "logs"
}

extension OpenWindowAction {
    /// Agent apps (no Dock icon) don't come to the front on their own when a window opens.
    @MainActor
    func activating(_ id: String) {
        callAsFunction(id: id)
        NSApp.activate()
    }
}
