import AppKit
import FlockKit
import SwiftUI

/// A terminal-like timeline: newest request on top, its dumps, queries, logs, views and jobs one
/// after another, no sidebar.
struct DumpsWindow: View {
    @Environment(AppModel.self) private var model
    @AppStorage("dumps.latestOnly") private var latestOnly = true
    @AppStorage("dumps.editor") private var editorID = Editor.phpstorm.rawValue

    private let queryState = State<String>(initialValue: "")
    private var query: String {
        get { queryState.wrappedValue }
        nonmutating set { queryState.wrappedValue = newValue }
    }

    private let confirmClearState = State<Bool>(initialValue: false)
    private var confirmClear: Bool {
        get { confirmClearState.wrappedValue }
        nonmutating set { confirmClearState.wrappedValue = newValue }
    }

    private var editor: Editor { Editor(rawValue: editorID) ?? .phpstorm }

    var body: some View {
        let store = model.dumps
        let capturing = model.status?.dumps == true
        let all = DumpTimeline.merge(dumps: store.dumps, events: store.events)
        let visible = DumpTimeline.filter(latestOnly ? DumpTimeline.latestGroup(of: all) : all, query: query)

        Group {
            if visible.isEmpty {
                ContentUnavailableView {
                    Label(all.isEmpty ? "Nothing captured yet" : "Nothing matches", systemImage: "ladybug")
                } description: {
                    Text(capturing
                        ? "dump() and dd() output, queries, logs, views and jobs from your sites and artisan commands appear here."
                        : "Capture is off — turn it on with the antenna button to collect dumps, queries, logs, views and jobs here.")
                } actions: {
                    if !capturing {
                        Button("Start Capturing") { Task { await model.setDumps(true, from: .dumps) } }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                WebView(html: DumpTimeline.html(items: visible, header: store.header, editor: editor),
                        allowsJavaScript: true, blocksRemoteContent: true)
            }
        }
        .searchable(text: queryState.projectedValue, placement: .toolbar, prompt: "Search dumps, queries, logs")
        .toolbar {
            ToolbarItemGroup {
                Toggle(isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on, from: .dumps) } })) {
                    Label(capturing ? "Capturing" : "Capture Dumps", systemImage: "antenna.radiowaves.left.and.right")
                }
                .help(capturing ? "Stop capturing dumps, queries, logs, views and jobs" : "Capture dumps, queries, logs, views and jobs")
                .symbolEffect(.pulse, isActive: capturing)
                .disabled(model.status == nil)

                captureMenu

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
                    confirmClear = true
                }
                .disabled(all.isEmpty)
            }
        }
        .navigationTitle(visible.isEmpty ? "Dumps" : "Dumps (\(visible.count))")
        .confirmationDialog("Clear everything captured?", isPresented: confirmClearState.projectedValue) {
            Button("Clear", role: .destructive) {
                Task { await store.clear() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Every captured dump, query, log, view and job is deleted, from all requests and commands.")
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 0) {
                if capturing, let versions = model.status?.captureUnsupported, !versions.isEmpty {
                    DumpsOnlyNote(versions: versions)
                }
                ErrorBanner(message: store.error)
            }
        }
        .task {
            store.isVisible = true
            await store.refresh()
        }
        .onDisappear { store.isVisible = false }
    }

    /// What is captured besides dumps, and whether dump() also shows in the page.
    private var captureMenu: some View {
        let off = Set(model.status?.captureOff ?? [])
        return Menu {
            Section("Capture") {
                ForEach(Event.Kind.allCases) { kind in
                    Toggle(kind.title, isOn: Binding(get: { !off.contains(kind.rawValue) },
                                                     set: { on in Task { await model.setCapture(kind, on) } }))
                }
            }
            Divider()
            Toggle("Show dump() in Page Too", isOn: Binding(get: { model.status?.dumpsInPage == true },
                                                         set: { on in Task { await model.setDumpsInPage(on) } }))
        } label: {
            Label("Capture Options", systemImage: "line.3.horizontal.decrease.circle")
        }
        .help("What is captured besides dumps — dd() always shows in the page too")
        .disabled(model.status == nil)
    }
}

/// PHP versions whose build lacks Flock's agent extension only capture dumps.
struct DumpsOnlyNote: View {
    let versions: [String]

    var body: some View {
        Label("PHP \(ListFormatter.localizedString(byJoining: versions)) only \(versions.count == 1 ? "captures" : "capture") dumps — \(versions.count == 1 ? "its build has" : "their builds have") no agent extension yet, so queries, logs, views and jobs are missing there.",
              systemImage: "info.circle")
            .font(.callout)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(8)
            .background(.bar)
    }
}
