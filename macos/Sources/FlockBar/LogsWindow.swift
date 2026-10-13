import FlockKit
import SwiftUI

struct LogsWindow: View {
    @Environment(AppModel.self) private var model
    private let selectionState = State<LogStore.Source?>(initialValue: nil)
    private var selection: LogStore.Source? {
        get { selectionState.wrappedValue }
        nonmutating set { selectionState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.logs

        NavigationSplitView {
            List(selection: selectionState.projectedValue) {
                Section("Sites") {
                    ForEach(store.index.sites, id: \.self) { site in
                        Label(site, systemImage: "globe").tag(LogStore.Source.site(site))
                    }
                }
                Section("Services") {
                    ForEach(store.index.services, id: \.self) { service in
                        Label(service, systemImage: "gearshape").tag(LogStore.Source.service(service))
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 200, ideal: 240)
        } detail: {
            switch selection {
            case let .site(name):
                SiteLogView(entries: store.entries, site: name)
            case .service:
                MonospacedText(store.serviceText.isEmpty ? "(empty)" : store.serviceText)
            case nil:
                ContentUnavailableView("Pick a log", systemImage: "doc.text.magnifyingglass")
            }
        }
        .toolbar {
            ToolbarItemGroup {
                Button("Refresh", systemImage: "arrow.clockwise") {
                    Task { await reload() }
                }
                if case let .site(name) = selection {
                    Button("Clear", systemImage: "trash") {
                        Task { await store.clear(site: name) }
                    }
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            ErrorBanner(message: store.error)
        }
        .task {
            await store.loadIndex()
        }
        .task(id: selection) {
            await reload()
        }
    }

    private func reload() async {
        if let selection {
            await model.logs.load(selection)
        }
    }
}

private struct SiteLogView: View {
    let entries: [LogEntry]
    let site: String

    var body: some View {
        if entries.isEmpty {
            ContentUnavailableView("No log entries", systemImage: "checkmark.circle",
                                   description: Text("storage/logs of \(site) is empty."))
        } else {
            List(entries) { entry in
                LogEntryRow(entry: entry)
            }
        }
    }
}

private struct LogEntryRow: View {
    let entry: LogEntry
    private let expandedState = State<Bool>(initialValue: false)
    private var expanded: Bool {
        get { expandedState.wrappedValue }
        nonmutating set { expandedState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(entry.level.uppercased())
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(levelColor, in: RoundedRectangle(cornerRadius: 4))
                Text(entry.time, format: .dateTime.year().month().day().hour().minute().second())
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                if !entry.env.isEmpty {
                    Text(entry.env).font(.caption).foregroundStyle(.secondary)
                }
            }
            Text(entry.message)
                .font(.callout)
                .lineLimit(expanded ? nil : 3)
                .textSelection(.enabled)
            if !entry.details.isEmpty {
                DisclosureGroup("Details", isExpanded: expandedState.projectedValue) {
                    Text(entry.details)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .font(.caption)
            }
        }
        .padding(.vertical, 4)
    }

    private var levelColor: Color {
        switch entry.level {
        case "debug": .gray
        case "info": .blue
        case "notice": .teal
        case "warning": .orange
        default: .red
        }
    }
}
