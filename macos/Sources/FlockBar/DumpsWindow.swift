import AppKit
import FlockKit
import SwiftUI

/// A terminal-like timeline: newest request on top, dumps one after another, no sidebar.
struct DumpsWindow: View {
    @Environment(AppModel.self) private var model
    @AppStorage("dumps.latestOnly") private var latestOnly = true
    @AppStorage("dumps.editor") private var editorID = Editor.phpstorm.rawValue

    private let queryState = State<String>(initialValue: "")
    private var query: String {
        get { queryState.wrappedValue }
        nonmutating set { queryState.wrappedValue = newValue }
    }

    private var editor: Editor { Editor(rawValue: editorID) ?? .phpstorm }

    var body: some View {
        let store = model.dumps
        let capturing = model.status?.dumps == true
        let visible = DumpTimeline.filter(latestOnly ? DumpTimeline.latestGroup(of: store.dumps) : store.dumps, query: query)

        Group {
            if visible.isEmpty {
                ContentUnavailableView {
                    Label(store.dumps.isEmpty ? "No dumps yet" : "No matching dumps", systemImage: "ladybug")
                } description: {
                    Text(capturing
                        ? "dump() and dd() output from your sites and artisan commands appears here."
                        : "Capture is off — turn it on with the antenna button to collect dump() and dd() output here.")
                } actions: {
                    if !capturing {
                        Button("Start Capturing") { Task { await model.setDumps(true) } }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Color(red: 0.094, green: 0.09, blue: 0.106))
                .environment(\.colorScheme, .dark)
            } else {
                WebView(html: DumpTimeline.html(dumps: visible, header: store.header, editor: editor),
                        allowsJavaScript: true, blocksRemoteContent: true)
            }
        }
        .searchable(text: queryState.projectedValue, placement: .toolbar, prompt: "Search dumps")
        .toolbar {
            ToolbarItemGroup {
                Toggle(isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on) } })) {
                    Label(capturing ? "Capturing" : "Capture Dumps", systemImage: "antenna.radiowaves.left.and.right")
                }
                .help(capturing ? "Stop intercepting dump() and dd()" : "Intercept dump() and dd()")
                .symbolEffect(.pulse, isActive: capturing)
                .disabled(model.status == nil)

                Toggle(isOn: $latestOnly) {
                    Label("Latest Request Only", systemImage: "clock.arrow.circlepath")
                }
                .help(latestOnly ? "Showing the latest request only — click to show the full history" : "Showing all requests")

                Menu {
                    Picker("Open Sources In", selection: $editorID) {
                        ForEach(Editor.allCases) { Text($0.name).tag($0.rawValue) }
                    }
                    .pickerStyle(.inline)
                } label: {
                    Label("Editor", systemImage: "chevron.left.forwardslash.chevron.right")
                }
                .help("Editor used when clicking a dump's file and line")

                Button("Clear", systemImage: "trash") {
                    Task { await store.clear() }
                }
                .disabled(store.dumps.isEmpty)
            }
        }
        .navigationTitle(visible.isEmpty ? "Dumps" : "Dumps (\(visible.count))")
        .safeAreaInset(edge: .bottom) {
            ErrorBanner(message: store.error)
        }
        .task { await Snapshot.captureIfRequested(WindowID.dumps) }
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }
}
