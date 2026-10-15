import FlockKit
import SwiftUI

/// First start after installing: explains what setting Flock up does, runs the bundled
/// `flock install` (two password prompts) and shows its output. The Windows app's SetupWindow.
struct SetupWindow: View {
    enum Stage { case intro, running, done, failed }

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
    private let declinedState = State<Bool>(initialValue: false)
    private var declined: Bool {
        get { declinedState.wrappedValue }
        nonmutating set { declinedState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            switch stage {
            case .intro:
                header("Welcome to Flock", "Flock runs your Laravel sites, PHP, mail and databases on this Mac. Setting it up takes a moment:")
                Form {
                    item("Runs in the background", "The Flock daemon serves your sites and services, and starts whenever you log in.", symbol: "server.rack")
                    item("php, composer and node in your terminal", "Flock's versions come first on your PATH. Open a new terminal after setup.", symbol: "terminal")
                    item("*.test sites with trusted HTTPS", "macOS asks for your password twice: once to resolve *.test through Flock (/etc/resolver), once to trust Flock's certificate.", symbol: "lock.fill")
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

            case .running:
                header("Setting up Flock…", "macOS asks for your password in a moment, and then once more.")
                ProgressView().progressViewStyle(.linear).padding(.horizontal, 32).padding(.bottom, 12)
                log

            case .done:
                header("Flock is ready", nil)
                Form {
                    Section("Next steps") {
                        item("Open a new terminal", "Terminals that were already open still have the old PATH.", symbol: "terminal")
                        item("Link a project", "In a Laravel project, run `flock link`: the site is then at http://<folder>.test. `flock secure` adds HTTPS.", symbol: "globe")
                        item("Install PHP", "Settings → PHP installs versions; running `php` in a terminal offers to install a missing one.", symbol: "chevron.left.forwardslash.chevron.right")
                    }
                }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                buttons {
                    Button("Open Settings") {
                        dismissWindow(id: WindowID.setup)
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
        .task { await Snapshot.captureIfRequested(WindowID.setup) }
    }

    private func run() async {
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

    private func item(_ title: String, _ text: String, symbol: String) -> some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                Text(text).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        } icon: {
            Image(systemName: symbol).foregroundStyle(Color.accentColor).frame(width: 22)
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
