import FlockKit
import SwiftUI

/// First start after installing: explains what setting Flock up does, makes sure no other local
/// environment (Herd, Valet, ...) holds Flock's ports, runs the bundled `flock install` (two
/// password prompts) and shows its output. The Windows app's SetupWindow.
struct SetupWindow: View {
    enum Stage { case intro, checking, conflicts, running, done, failed }

    @Environment(AppModel.self) private var model
    @Environment(\.dismissWindow) private var dismissWindow
    @Environment(\.openWindow) private var openWindow

    private let stageState = State<Stage>(initialValue: .intro)
    private var stage: Stage {
        get { stageState.wrappedValue }
        nonmutating set { stageState.wrappedValue = newValue }
    }
    private let outputState = State<String>(initialValue: "")
    private var output: String {
        get { outputState.wrappedValue }
        nonmutating set { outputState.wrappedValue = newValue }
    }
    /// The programs to quit before Flock can be set up, by app.
    private let conflictsState = State<[Installation.ConflictingApp]>(initialValue: [])
    private var conflicts: [Installation.ConflictingApp] {
        get { conflictsState.wrappedValue }
        nonmutating set { conflictsState.wrappedValue = newValue }
    }
    private let declinedState = State<Bool>(initialValue: false)
    private var declined: Bool {
        get { declinedState.wrappedValue }
        nonmutating set { declinedState.wrappedValue = newValue }
    }
    /// The first run's installs as this window last saw them in the status.
    private let seenDefaultsState = State<Status.Defaults?>(initialValue: nil)
    private var seenDefaults: Status.Defaults? {
        get { seenDefaultsState.wrappedValue }
        nonmutating set { seenDefaultsState.wrappedValue = newValue }
    }
    /// Applied once setup succeeds; Settings → General changes it later.
    private let launchAtLoginState = State<Bool>(initialValue: true)
    private var launchAtLogin: Bool {
        get { launchAtLoginState.wrappedValue }
        nonmutating set { launchAtLoginState.wrappedValue = newValue }
    }
    private let loginProblemState = State<String?>(initialValue: nil)
    private var loginProblem: String? {
        get { loginProblemState.wrappedValue }
        nonmutating set { loginProblemState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            switch stage {
            case .intro:
                header("Welcome to Flock", "Flock runs your Laravel sites, PHP, mail and databases on this Mac. Setting it up takes a moment:")
                Form {
                    Section {
                        item("Runs in the background", "The Flock daemon serves your sites and services, and starts whenever you log in.", symbol: "server.rack")
                        item("php, composer and node in your terminal", "Flock's versions come first on your PATH. Open a new terminal after setup.", symbol: "terminal")
                        item("*.test sites with trusted HTTPS", "macOS asks for your password twice: once to resolve *.test through Flock (/etc/resolver), once to trust Flock's certificate.", symbol: "lock.fill")
                    }
                    Section {
                        Toggle("Open Flock’s menu bar app at login", isOn: Binding(get: { launchAtLogin }, set: { launchAtLogin = $0 }))
                            .toggleStyle(.checkbox)
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                buttons {
                    Button("Not now") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("Set Up Flock") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }

            case .checking:
                header("Checking Flock's ports…", nil)
                ProgressView().progressViewStyle(.linear).padding(.horizontal, 32).padding(.bottom, 28)

            case .conflicts:
                header("Quit other local environments first", "Flock needs these ports and *.\(model.status?.dns.tld ?? "test") for itself, but other programs use them. Running both would leave neither working.")
                Form {
                    Section {
                        ForEach(conflicts) { app in
                            item(app.title, app.detail, symbol: "exclamationmark.triangle.fill", color: .orange)
                        }
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                buttons {
                    Button("Not now") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("Check Again") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }

            case .running:
                header("Setting up Flock…", "macOS asks for your password in a moment, and then once more.")
                ProgressView().progressViewStyle(.linear).padding(.horizontal, 32).padding(.bottom, 12)
                log

            case .done:
                header("Flock is ready", nil)
                Form {
                    Section("Next steps") {
                        if let loginProblem {
                            item("Open Flock at login", loginProblem, symbol: "exclamationmark.triangle")
                        }
                        runtimes
                        item("Open a new terminal", "Terminals that were already open still have the old PATH.", symbol: "terminal")
                        item("Link a project", "In a Laravel project, run `flock link`: the site is then at http://<folder>.test. `flock secure` adds HTTPS.", symbol: "globe")
                        item("PHP and Node.js","Flock installs the newest PHP and Node.js LTS as the defaults. Settings → PHP and Node install other versions.", symbol: "chevron.left.forwardslash.chevron.right")
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                // The status stops listing the installs once both are done; the row keeps their versions.
                .onChange(of: model.status?.defaults, initial: true) { _, new in
                    if let new {
                        seenDefaults = new
                    }
                }
                buttons {
                    Button("Open Settings") {
                        dismissWindow(id: WindowID.setup)
                        showSettingsPane()
                        openWindow.activating(WindowID.settings)
                    }
                    Button("Done") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.defaultAction)
                }

            case .failed:
                header("Setup didn't finish", declined
                    ? "The password prompt was cancelled. Flock runs, but *.test sites won't resolve and HTTPS isn't trusted until you allow it."
                    : "Something went wrong. The output below says what; you can try again.")
                log
                buttons {
                    Button("Close") { dismissWindow(id: WindowID.setup) }
                        .keyboardShortcut(.cancelAction)
                    Button("Try Again") { Task { await run() } }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .frame(width: 560)
        .fixedSize(horizontal: false, vertical: true)
    }

    private func run() async {
        stage = .checking
        conflicts = Installation.group(await model.conflicts())
        guard conflicts.isEmpty else {
            stage = .conflicts
            return
        }

        output = ""
        stage = .running
        let result = await model.install { line in
            // Lines arrive from the pipe's thread; late ones lose to the final output below.
            Task { @MainActor in
                if stage == .running {
                    output += line + "\n"
                }
            }
        }
        output = result.output
        declined = result.permissionDeclined
        if result.ok && launchAtLogin != LoginItem.isEnabled {
            loginProblem = LoginItem.set(launchAtLogin)
        }
        stage = result.ok ? .done : .failed
    }

    private func header(_ title: String, _ text: String?) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            AppIconView(size: 60)
            Text(title).font(.title.weight(.semibold))
            if let text {
                Text(text).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 32)
        .padding(.top, 28)
        .padding(.bottom, 12)
    }

    /// One of the first run's installs, which become the default PHP and Node.js.
    private struct Runtime {
        let name: String
        /// The Settings pane that installs it again.
        let pane: String
        let install: Status.DefaultInstall

        /// "PHP 8.5", or "PHP" while the version isn't known yet.
        var label: String { install.version.isEmpty ? name : "\(name) \(install.version)" }
    }

    /// The first run's installs of the newest PHP and Node.js LTS (Status.defaults): in progress,
    /// waiting for a download, failed, or done. Shown only when this setup installs them; once the
    /// status stops listing them, both are done.
    @ViewBuilder
    private var runtimes: some View {
        if let defaults = model.status?.defaults ?? seenDefaults.map(Self.finished) {
            let all = [Runtime(name: "PHP", pane: "PHP", install: defaults.php), Runtime(name: "Node.js", pane: "Node", install: defaults.node)]
            let pending = all.filter { $0.install.isPending }
            ForEach(all.filter { $0.install.state == .failed }, id: \.name) { runtime in
                item("\(runtime.label) couldn't be installed",
                     (runtime.install.error.isEmpty ? "" : "\(runtime.install.error). ") + "Try again in Settings → \(runtime.pane).",
                     symbol: "exclamationmark.triangle.fill", color: .orange)
            }
            if pending.contains(where: { $0.install.state == .installing }) {
                row("Installing \(Self.names(pending))…", "Flock makes \(pending.count > 1 ? "them" : "it") the default for your sites and terminal.") {
                    ProgressView().controlSize(.small)
                }
            } else if !pending.isEmpty {
                row("Waiting to install \(Self.names(pending))", "\(Self.names(pending)) \(pending.count > 1 ? "are" : "is") installed as soon as Flock can download \(pending.count > 1 ? "them" : "it").") {
                    ProgressView().controlSize(.small)
                }
            }
            if all.allSatisfy({ $0.install.state == .done }) {
                item("\(Self.names(all)) are installed", "They're the defaults for your sites and terminal.", symbol: "checkmark.circle.fill", color: .green)
            }
        }
    }

    /// The installs as last seen, once the status no longer lists them: both defaults are set.
    private static func finished(_ seen: Status.Defaults) -> Status.Defaults {
        Status.Defaults(php: Status.DefaultInstall(version: seen.php.version), node: Status.DefaultInstall(version: seen.node.version))
    }

    /// The pane "Open Settings" shows: the one a link waited for during the setup, else the pane to
    /// retry a failed default in, else the one Settings showed last.
    private func showSettingsPane() {
        if let link = model.pendingInstall {
            model.handle(.install(link))
        } else if model.pendingSecure != nil {
            model.showSettings(.sites)
        } else if let defaults = model.status?.defaults, defaults.php.state == .failed {
            model.showSettings(.php)
        } else if model.status?.defaults?.node.state == .failed {
            model.showSettings(.node)
        }
    }

    /// "PHP 8.5 and Node.js 24".
    private static func names(_ runtimes: [Runtime]) -> String {
        runtimes.map(\.label).joined(separator: " and ")
    }

    private func item(_ title: String, _ text: String, symbol: String, color: Color = .accentColor) -> some View {
        row(title, text) {
            Image(systemName: symbol).foregroundStyle(color)
        }
    }

    private func row(_ title: String, _ text: String, @ViewBuilder icon: () -> some View) -> some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                Text(text).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        } icon: {
            icon().frame(width: 22)
        }
        .padding(.vertical, 2)
    }

    private var log: some View {
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                VStack(alignment: .leading, spacing: 0) {
                    Text(output.isEmpty ? " " : output)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(10)
            }
            .frame(height: 220)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
            .padding(.horizontal, 32)
            .padding(.bottom, stage == .running ? 28 : 0)
            .onChange(of: output) { proxy.scrollTo("end", anchor: .bottom) }
        }
    }

    private func buttons(@ViewBuilder _ content: () -> some View) -> some View {
        HStack {
            Spacer()
            content()
        }
        .padding(.horizontal, 32)
        .padding(.top, 8)
        .padding(.bottom, 24)
    }
}
