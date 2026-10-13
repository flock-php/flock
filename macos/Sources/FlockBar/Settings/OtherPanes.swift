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
                    Toggle("Capture dumps", isOn: Binding(get: { capturing }, set: { on in Task { await model.setDumps(on) } }))
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
                    Toggle("Load Xdebug", isOn: Binding(get: { enabled }, set: { on in Task { await model.setXdebug(on) } }))
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
        }
        .frame(maxWidth: .infinity)
    }

    private var appVersion: String {
        let info = Bundle.main.infoDictionary
        return info?["CFBundleShortVersionString"] as? String ?? info?["CFBundleVersion"] as? String ?? "dev"
    }
}
