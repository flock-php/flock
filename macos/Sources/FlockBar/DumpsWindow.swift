import AppKit
import FlockKit
import SwiftUI

struct DumpsWindow: View {
    @Environment(AppModel.self) private var model
    private let selectionState = State<Dump.ID?>(initialValue: nil)
    private var selection: Dump.ID? {
        get { selectionState.wrappedValue }
        nonmutating set { selectionState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.dumps

        NavigationSplitView {
            List(store.dumps, selection: selectionState.projectedValue) { dump in
                DumpRow(dump: dump)
            }
            .overlay {
                if store.dumps.isEmpty {
                    ContentUnavailableView("No dumps", systemImage: "ladybug",
                                           description: Text(model.status?.dumps == true
                                               ? "dump() and dd() output will show up here."
                                               : "Turn on “Capture dumps” in the menu bar to collect dump() and dd() output."))
                }
            }
            .navigationSplitViewColumnWidth(min: 260, ideal: 320)
        } detail: {
            if let dump = store.dumps.first(where: { $0.id == selection }) {
                DumpDetail(dump: dump)
            } else {
                ContentUnavailableView("No dump selected", systemImage: "ladybug")
            }
        }
        .toolbar {
            ToolbarItemGroup {
                Button("Refresh", systemImage: "arrow.clockwise") {
                    Task { await store.refresh() }
                }
                Button("Clear", systemImage: "trash") {
                    selection = nil
                    Task { await store.clear() }
                }
                .disabled(store.dumps.isEmpty)
            }
        }
        .safeAreaInset(edge: .bottom) {
            ErrorBanner(message: store.error)
        }
        .task {
            store.isVisible = true
            await store.refresh()
            if selection == nil {
                selection = store.dumps.first?.id
            }
        }
        .onDisappear { store.isVisible = false }
    }
}

private struct DumpRow: View {
    let dump: Dump

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(dump.shortSource ?? "dump").font(.callout.monospaced()).lineLimit(1)
                Spacer()
                Text(dump.time, format: .dateTime.hour().minute().second())
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Text(origin)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .padding(.vertical, 3)
    }

    private var origin: String {
        if let request = dump.request {
            return "\(request.method) \(request.uri)"
        }
        if let command = dump.commandLine, !command.isEmpty {
            return command
        }
        return dump.site
    }
}

private struct DumpDetail: View {
    let dump: Dump

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    if let source = dump.source {
                        Text("\(source.file):\(source.line)").font(.callout.monospaced()).textSelection(.enabled)
                    }
                    Text(dump.time.formatted(date: .abbreviated, time: .standard))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("Copy as Text", systemImage: "doc.on.doc") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(dump.text, forType: .string)
                }
            }
            .padding(12)
            Divider()
            WebView(html: page, allowsJavaScript: true, blocksRemoteContent: true)
        }
    }

    /// Symfony's dumper uses a dark theme; give the page the same background so it fits in both modes.
    private var page: String {
        """
        <!doctype html><html><head><meta charset="utf-8">
        <style>html,body{margin:0;padding:0;background:#18171B;} pre.sf-dump{margin:0!important;padding:12px!important;}</style>
        </head><body>\(dump.html)</body></html>
        """
    }
}
