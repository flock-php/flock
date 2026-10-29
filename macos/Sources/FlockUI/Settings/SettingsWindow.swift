import FlockKit
import SwiftUI

/// The panes of the Settings window, in sidebar order.
enum SettingsPane: String, CaseIterable, Identifiable {
    case general, sites, php, node, services, mail, dumps, debugger, ai, about

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: "General"
        case .sites: "Sites"
        case .php: "PHP"
        case .node: "Node"
        case .services: "Services"
        case .mail: "Mail"
        case .dumps: "Dumps"
        case .debugger: "Debugger"
        case .ai: "AI"
        case .about: "About"
        }
    }

    var symbol: String {
        switch self {
        case .general: "gearshape.fill"
        case .sites: "globe"
        case .php: "chevron.left.forwardslash.chevron.right"
        case .node: "hexagon.fill"
        case .services: "server.rack"
        case .mail: "envelope.fill"
        case .dumps: "ladybug.fill"
        case .debugger: "ant.fill"
        case .ai: "sparkles"
        case .about: "info"
        }
    }

    var color: Color {
        switch self {
        case .general: .gray
        case .sites: .blue
        case .php: .indigo
        case .node: .green
        case .services: .orange
        case .mail: .cyan
        case .dumps: .pink
        case .debugger: .red
        case .ai: .purple
        case .about: .secondary
        }
    }
}

/// Settings: a sidebar of panes and the selected pane on the right.
/// A plain HStack instead of NavigationSplitView, which lets the toolbar background
/// bleed under its floating sidebar on current macOS.
struct SettingsWindow: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    private var pane: SettingsPane {
        get { model.settingsPane }
        nonmutating set { model.settingsPane = newValue }
    }

    var body: some View {
        let store = model.settings

        HStack(spacing: 0) {
            List(selection: Binding<SettingsPane?>(get: { pane }, set: { if let new = $0 { pane = new } })) {
                ForEach(SettingsPane.allCases) { item in
                    Label {
                        Text(item.title)
                    } icon: {
                        SettingsIcon(symbol: item.symbol, color: item.color)
                    }
                    .tag(item)
                    .padding(.vertical, 2)
                }
            }
            .listStyle(.sidebar)
            .frame(width: 200)
            .safeAreaPadding(.top, 38) // clear the traffic lights

            Divider()

            VStack(spacing: 0) {
                content
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                // The daemon's buttons here (Start, Restart) report through the model; "not running"
                // is already what the pane shows.
                SettingsError(message: (store.error == model.error ? nil : store.error) ?? model.actionError) {
                    store.error = nil
                    model.actionError = nil
                }
            }
            .background(Color(nsColor: .windowBackgroundColor))
        }
        .ignoresSafeArea(.container, edges: .top)
        .frame(minWidth: 810, minHeight: 480)
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }

    @ViewBuilder
    private var content: some View {
        if model.status == nil, pane != .about {
            if model.needsSetup {
                ContentUnavailableView {
                    Label { Text("Flock isn't set up yet") } icon: { LogoMark(size: 44) }
                } description: {
                    Text("Set Flock up to run your sites and change its settings.")
                } actions: {
                    Button("Set Up Flock…") { openWindow.activating(WindowID.setup) }
                }
            } else {
                ContentUnavailableView {
                    Label { Text("Flock is not running") } icon: { LogoMark(size: 44) }
                } description: {
                    Text(model.error ?? "Start the daemon to change its settings.")
                } actions: {
                    Button("Start Flock") { Task { await model.restart() } }
                        .disabled(model.isRestarting)
                }
            }
        } else {
            switch pane {
            case .general: GeneralPane()
            case .sites: SitesPane()
            case .php: PHPPane()
            case .node: NodePane()
            case .services: ServicesPane()
            case .mail: MailPane()
            case .dumps: DumpsPane()
            case .debugger: DebuggerPane()
            case .ai: AIPane()
            case .about: AboutPane()
            }
        }
    }
}
