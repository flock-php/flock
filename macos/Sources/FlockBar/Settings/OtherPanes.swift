import AppKit
import FlockKit
import SwiftUI

struct MailPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        let store = model.settings
        let mail = store.services.first { $0.id == "mail" }

        VStack(spacing: 0) {
            PaneHeader(title: "Mail", subtitle: "Flock catches every email your apps send — nothing leaves your Mac.")
            Form {
                Section("SMTP server") {
                    LabeledContent("Host", value: "127.0.0.1")
                    LabeledContent("Port", value: String(store.config?.ports.smtp ?? mail?.port ?? 0))
                    LabeledContent("Status") {
                        HStack(spacing: 6) {
                            StatusDot(on: mail?.running ?? false)
                            Text(mail?.running == true ? "Running" : "Stopped")
                        }
                    }
                }
                Section {
                    CodeBlock(text: mail?.env ?? "")
                } header: {
                    Text("Environment Variables")
                } footer: {
                    Text("MAIL_USERNAME picks the inbox, so every project gets its own.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    LabeledContent("Caught emails") {
                        HStack {
                            Text(String(model.status?.mailCount ?? 0))
                            Button("Open Mail") { openWindow.activating(WindowID.mail) }
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

struct DumpsPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    @AppStorage("dumps.editor") private var editorID = Editor.phpstorm.rawValue

    var body: some View {
        let capturing = model.status?.dumps == true

        VStack(spacing: 0) {
            PaneHeader(title: "Dumps", subtitle: "Send dump() and dd() output to Flock instead of the page or terminal — no changes to your projects.")
            Form {
                Section {
                    Toggle("Capture dumps", isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on, from: .settings) } }))
                    LabeledContent("Captured") {
                        HStack {
                            Text(String(model.status?.dumpCount ?? 0))
                            Button("Open Dumps") { openWindow.activating(WindowID.dumps) }
                        }
                    }
                } footer: {
                    Text("Works by setting VAR_DUMPER_FORMAT=server for PHP; turn it off to see dumps in the page again.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section("Sources") {
                    Picker("Open files in", selection: $editorID) {
                        ForEach(Editor.allCases) { Text($0.name).tag($0.rawValue) }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

struct DebuggerPane: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let enabled = model.status?.xdebug == true
        let versions = model.settings.config?.php ?? []

        VStack(spacing: 0) {
            PaneHeader(title: "Debugger", subtitle: "Step through requests and commands with Xdebug.")
            Form {
                Section {
                    Toggle("Load Xdebug", isOn: Binding(get: { enabled }, set: { on in Task { await model.setXdebug(on, from: .settings) } }))
                } footer: {
                    Text("Loading Xdebug slows every request down, so keep it off when you don't need it.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section("Connect your IDE") {
                    LabeledContent("Client port", value: "9003")
                    LabeledContent("Mode", value: "debug, develop")
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Start a session")
                        Text("Use the Xdebug browser extension, add `?XDEBUG_TRIGGER=1` to the URL, or run `XDEBUG_TRIGGER=1 php artisan …`.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Section("Availability") {
                    ForEach(versions) { php in
                        LabeledContent("PHP \(php.version)") {
                            if php.xdebugAvailable {
                                Label("Available", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                            } else {
                                Label("Not in this build", systemImage: "xmark.circle").foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}

struct AboutPane: View {
    @Environment(AppModel.self) private var model
    private let uninstallingState = State<Bool>(initialValue: false)

    var body: some View {
        VStack(spacing: 14) {
            Spacer()
            SettingsIcon(symbol: "bird.fill", color: .accentColor, size: 72)
            Text("Flock").font(.largeTitle.weight(.semibold))
            Text("A native local development environment for Laravel.")
                .foregroundStyle(.secondary)
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                GridRow {
                    Text("App").foregroundStyle(.secondary)
                    Text(appVersion)
                }
                GridRow {
                    Text("Daemon").foregroundStyle(.secondary)
                    Text(model.status?.version ?? "not running")
                }
            }
            .font(.callout)
            HStack {
                Button("Data Folder") { reveal(FlockPaths.root.path) }
                Button("Laravel Docs") { NSWorkspace.shared.open(URL(string: "https://laravel.com/docs")!) }
            }
            Spacer()
            VStack(spacing: 6) {
                Button("Uninstall Flock…") { uninstallingState.wrappedValue = true }
                    .disabled(model.installation.uninstaller == nil)
                Text("Stops Flock and removes it from this Mac. Your sites, databases and settings are kept.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            .padding(.bottom, 20)
        }
        .frame(maxWidth: .infinity)
        .sheet(isPresented: uninstallingState.projectedValue) {
            UninstallSheet()
        }
    }

    private var appVersion: String {
        let info = Bundle.main.infoDictionary
        return info?["CFBundleShortVersionString"] as? String ?? info?["CFBundleVersion"] as? String ?? "dev"
    }
}

/// Asks whether to uninstall (and whether to delete the user's data too), runs
/// `flock uninstall --keep-app` with its output, then removes the app and quits.
private struct UninstallSheet: View {
    enum Stage { case confirm, running, done, failed }

    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    private let stageState = State<Stage>(initialValue: .confirm)
    private var stage: Stage {
        get { stageState.wrappedValue }
        nonmutating set { stageState.wrappedValue = newValue }
    }
    private let purgeState = State<Bool>(initialValue: false)
    private let outputState = State<String>(initialValue: "")
    private var output: String {
        get { outputState.wrappedValue }
        nonmutating set { outputState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            switch stage {
            case .confirm:
                Text("Uninstall Flock?").font(.headline)
                Text("Flock stops and is removed from your PATH, together with its PHP and Node.js versions, certificates, logs and caches. macOS asks for your password to remove the *.test DNS setup and Flock's trusted certificate. Then this app moves itself to the Trash.")
                    .fixedSize(horizontal: false, vertical: true)
                Text("Your sites' settings and your PostgreSQL and Redis databases stay in \(FlockPaths.root.path), so reinstalling picks up where you left off — unless you also delete them:")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Toggle("Also delete my sites' settings and databases", isOn: purgeState.projectedValue)
                    .toggleStyle(.checkbox)
                HStack {
                    Spacer()
                    Button("Cancel", role: .cancel) { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button("Uninstall", role: .destructive) { Task { await run() } }
                }

            case .running:
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Uninstalling Flock…").font(.headline)
                }
                log

            case .done:
                Text("Flock was uninstalled").font(.headline)
                Text(model.installedAppURL == nil
                    ? "This copy of the app isn't in an Applications folder, so it stays where it is; delete it yourself. Open a new terminal: Flock is no longer on its PATH."
                    : "Quitting moves Flock.app to the Trash. Open a new terminal: Flock is no longer on its PATH.")
                    .fixedSize(horizontal: false, vertical: true)
                log
                HStack {
                    Spacer()
                    Button("Quit Flock") { model.removeAppAndQuit() }
                        .keyboardShortcut(.defaultAction)
                }

            case .failed:
                Text("Uninstalling didn't finish").font(.headline)
                Text(output.contains("administrator permission was declined")
                    ? "The password prompt was cancelled, so the DNS setup and the certificate are still in place."
                    : "Something went wrong. The output below says what; you can try again.")
                    .fixedSize(horizontal: false, vertical: true)
                log
                HStack {
                    Spacer()
                    Button("Close") { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button("Try Again") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .padding(20)
        .frame(width: 520)
        .interactiveDismissDisabled(stage == .running)
    }

    private func run() async {
        output = ""
        stage = .running
        let result = await model.uninstall(purge: purgeState.wrappedValue) { line in
            Task { @MainActor in
                if stage == .running {
                    output += line + "\n"
                }
            }
        }
        output = result.output
        stage = result.ok ? .done : .failed
    }

    private var log: some View {
        ScrollView([.vertical, .horizontal]) {
            Text(output.isEmpty ? " " : output)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
        }
        .defaultScrollAnchor(.bottom)
        .frame(height: 180)
        .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
        .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
    }
}
