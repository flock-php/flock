import FlockKit
import SwiftUI

/// The panes of the Settings window, in sidebar order.
enum SettingsPane: String, CaseIterable, Identifiable {
    case general, sites, php, node, services, mail, dumps, debugger, about

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
        case .about: .secondary
        }
    }
}

/// Herd-style settings: a sidebar of panes and the selected pane on the right.
/// A plain HStack instead of NavigationSplitView, which lets the toolbar background
/// bleed under its floating sidebar on current macOS.
struct SettingsWindow: View {
    @Environment(AppModel.self) private var model
    private let paneState = State<SettingsPane>(initialValue: Snapshot.pane.flatMap(SettingsPane.init(rawValue:)) ?? .general)
    private var pane: SettingsPane {
        get { paneState.wrappedValue }
        nonmutating set { paneState.wrappedValue = newValue }
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
                SettingsError(message: store.error) { store.error = nil }
            }
            .background(Color(nsColor: .windowBackgroundColor))
        }
        .ignoresSafeArea(.container, edges: .top)
        .frame(minWidth: 760, minHeight: 480)
        .task { await Snapshot.captureIfRequested(WindowID.settings) }
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }

    @ViewBuilder
    private var content: some View {
        if model.status == nil, pane != .about {
            ContentUnavailableView {
                Label("Flock is not running", systemImage: "bird")
            } description: {
                Text("Start the daemon to change its settings.")
            } actions: {
                Button("Start Flock") { Task { await model.restart() } }
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
            case .about: AboutPane()
            }
        }
    }
}
