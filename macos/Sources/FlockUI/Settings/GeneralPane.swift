import AppKit
import FlockKit
import SwiftUI

struct GeneralPane: View {
    @Environment(AppModel.self) private var model
    private let loginErrorState = State<String?>(initialValue: nil)
    private var loginError: String? {
        get { loginErrorState.wrappedValue }
        nonmutating set { loginErrorState.wrappedValue = newValue }
    }
    private let selectedParkedState = State<String?>(initialValue: nil)
    private var selectedParked: String? {
        get { selectedParkedState.wrappedValue }
        nonmutating set { selectedParkedState.wrappedValue = newValue }
    }
    /// The project folder waiting for the removal to be confirmed.
    private let confirmUnparkState = State<String?>(initialValue: nil)
    private var confirmUnpark: String? {
        get { confirmUnparkState.wrappedValue }
        nonmutating set { confirmUnparkState.wrappedValue = newValue }
    }
    private let launchAtLoginState = State<Bool>(initialValue: LoginItem.isEnabled)
    private var launchAtLogin: Bool {
        get { launchAtLoginState.wrappedValue }
        nonmutating set { launchAtLoginState.wrappedValue = newValue }
    }
    /// The domain picked in the menu; nil keeps the selection on the domain in the config.
    private let draftTLDState = State<String?>(initialValue: nil)
    private var draftTLD: String? {
        get { draftTLDState.wrappedValue }
        nonmutating set { draftTLDState.wrappedValue = newValue }
    }
    /// The text in the custom field, shown when Custom… is picked.
    private let customState = State<String>(initialValue: "")
    private var custom: String {
        get { customState.wrappedValue }
        nonmutating set { customState.wrappedValue = newValue }
    }
    /// The daemon's verdict on the drafted domain; nil while nothing has been checked.
    private let checkState = State<TLDCheck?>(initialValue: nil)
    private var check: TLDCheck? {
        get { checkState.wrappedValue }
        nonmutating set { checkState.wrappedValue = newValue }
    }
    /// Why the last check couldn't be made (the daemon didn't answer); the last verdict stays.
    private let checkNoteState = State<String?>(initialValue: nil)
    private var checkNote: String? {
        get { checkNoteState.wrappedValue }
        nonmutating set { checkNoteState.wrappedValue = newValue }
    }
    /// The bundled CLI's `flock tld` is running.
    private let applyingState = State<Bool>(initialValue: false)
    private var applying: Bool {
        get { applyingState.wrappedValue }
        nonmutating set { applyingState.wrappedValue = newValue }
    }
    /// The domain the sheet is switching to; nil once the sheet is closed.
    private let applyTLDState = State<String?>(initialValue: nil)
    private var applyTLD: String? {
        get { applyTLDState.wrappedValue }
        nonmutating set { applyTLDState.wrappedValue = newValue }
    }
    /// The `flock tld` output as it streams in.
    private let applyLinesState = State<[String]>(initialValue: [])
    private var applyLines: [String] {
        get { applyLinesState.wrappedValue }
        nonmutating set { applyLinesState.wrappedValue = newValue }
    }
    /// The switch stopped at a declined password prompt.
    private let applyDeclinedState = State<Bool>(initialValue: false)
    private var applyDeclined: Bool {
        get { applyDeclinedState.wrappedValue }
        nonmutating set { applyDeclinedState.wrappedValue = newValue }
    }
    private let showApplySheetState = State<Bool>(initialValue: false)
    private var showApplySheet: Bool {
        get { showApplySheetState.wrappedValue }
        nonmutating set { showApplySheetState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "General", subtitle: "The Flock daemon runs your sites, PHP and services in the background.")
            Form {
                Section("Daemon") {
                    if let status = model.status {
                        LabeledContent("Status") {
                            HStack(spacing: 6) {
                                StatusDot(on: !status.isDegraded)
                                Text(status.isDegraded ? "Needs attention" : "Running")
                            }
                        }
                        LabeledContent("Version", value: status.version)
                        LabeledContent("Process", value: "pid \(status.pid)")
                    }
                    LabeledContent("") {
                        Button {
                            Task { await model.restart() }
                        } label: {
                            BusyLabel(title: "Restart Daemon", busyTitle: "Restarting…", isBusy: model.isRestarting)
                        }
                        .disabled(model.isRestarting)
                    }
                }

                if let config = store.config {
                    Section {
                        LabeledContent {
                            let canApply = check.map { $0.ok && $0.normalized != config.tld } ?? false
                            VStack(alignment: .trailing, spacing: 8) {
                                Picker("Domain", selection: Binding(
                                    get: { domainSelection(config.tld) },
                                    set: { pickDomain($0, current: config.tld) }
                                )) {
                                    Text(".test").tag("test")
                                    Text(".loc").tag("loc")
                                    Text(".local").tag("local")
                                    Text("Custom…").tag(Self.customTag)
                                }
                                .pickerStyle(.menu)
                                .labelsHidden()
                                .fixedSize()
                                if domainSelection(config.tld) == Self.customTag {
                                    TextField("domain", text: customState.projectedValue)
                                        .labelsHidden()
                                        .frame(width: 160)
                                        .multilineTextAlignment(.trailing)
                                }
                                Button {
                                    if let check {
                                        Task { await applyDomain(check.normalized) }
                                    }
                                } label: {
                                    BusyLabel(title: "Apply", busyTitle: "Applying…", isBusy: applying)
                                }
                                .disabled(!canApply || applying)
                            }
                        } label: {
                            Text("Domain")
                            Text(domainExplanation(config.tld))
                        }
                        .task(id: candidate) {
                            let candidate = candidate
                            guard !candidate.isEmpty else {
                                // Nothing drafted (an emptied field): no verdict to show.
                                check = nil
                                checkNote = nil
                                return
                            }
                            try? await Task.sleep(for: .milliseconds(300))
                            if Task.isCancelled { return }
                            switch await store.checkTLD(candidate) {
                            case .success(let verdict):
                                // A newer candidate cancelled this task; drop the late answer.
                                if Task.isCancelled { return }
                                check = verdict
                                checkNote = nil
                            case .failure:
                                if Task.isCancelled { return }
                                checkNote = "Couldn’t check with the daemon; the verdict shown is the last one."
                            }
                        }
                    } header: {
                        Text("Network")
                    } footer: {
                        VStack(alignment: .leading, spacing: 4) {
                            if let check {
                                if check.ok, check.normalized != candidate {
                                    Text("→ .\(check.normalized)").font(.caption).foregroundStyle(.green)
                                }
                                if !check.warning.isEmpty {
                                    Text(check.warning).font(.caption).foregroundStyle(.orange)
                                }
                                if !check.error.isEmpty {
                                    Text(check.error).font(.caption).foregroundStyle(.red)
                                }
                            }
                            if let checkNote {
                                Text(checkNote).font(.caption).foregroundStyle(.secondary)
                            }
                            // Sites are served on 80 and 443; other ports are only set by editing config.json.
                            if config.ports.http != 80 || config.ports.https != 443 {
                                Text("Sites use ports \(String(config.ports.http)) (HTTP) and \(String(config.ports.https)) (HTTPS), set in config.json, so their addresses include the port.")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                    // A domain the menu doesn't offer sits in the custom field to be edited.
                    .onChange(of: config.tld, initial: true) { _, tld in
                        if draftTLD == nil, custom.isEmpty, !Self.domainPresets.contains(tld) {
                            custom = tld
                        }
                    }

                    Section("Data") {
                        LabeledContent("Folder") {
                            HStack {
                                Text(config.root).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                                Button("Show in Finder") { reveal(config.root) }
                            }
                        }
                    }
                }

                Section("Startup") {
                    Toggle("Open Flock’s menu bar app at login", isOn: Binding(get: { launchAtLogin }, set: { setLaunchAtLogin($0) }))
                    if let loginError {
                        Text(loginError).font(.caption).foregroundStyle(.orange)
                    }
                }

                Section {
                    appUpdates
                    if let config = store.config {
                        Toggle(isOn: Binding(
                            get: { config.channel == "beta" },
                            set: { on in Task { await store.perform { _ = try await $0.updateSettings(SettingsChange(channel: on ? "beta" : "stable")) } } }
                        )) {
                            Text("Beta versions")
                            Text("Get new versions of Flock, PHP and services earlier, including pre-releases. They may not work yet.")
                        }
                        .disabled(config.channel == "experimental")
                        if config.channel == "experimental" {
                            Label("Experimental channel: packages that aren’t released yet are offered. `flock channel stable` switches back.", systemImage: "flask.fill")
                                .font(.caption).foregroundStyle(.orange)
                        }
                    }
                } header: {
                    Text("Updates")
                }

                if let config = store.config {
                    Section {
                        List(selection: selectedParkedState.projectedValue) {
                            ForEach(config.parked, id: \.self) { path in
                                Label(path, systemImage: "folder").tag(path)
                            }
                        }
                        .frame(minHeight: 80)
                        .overlay {
                            if config.parked.isEmpty {
                                Text("No project folders").foregroundStyle(.secondary)
                            }
                        }
                        HStack(spacing: 0) {
                            Button {
                                for url in SettingsStore.chooseDirectories(prompt: "Add", multiple: true) {
                                    Task { await store.perform { _ = try await $0.park(url.path) } }
                                }
                            } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                            Button {
                                confirmUnpark = selectedParked
                            } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                            .disabled(selectedParked == nil)
                            Spacer()
                        }
                        .buttonStyle(.borderless)
                    } header: {
                        Text("Project folders")
                    } footer: {
                        Text("Every folder inside a project folder is served as <folder>.\(config.tld) — the same as `flock park`.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .confirmationDialog("Remove \(confirmUnpark.map { URL(fileURLWithPath: $0).lastPathComponent } ?? "") from project folders?",
                            isPresented: Binding(get: { confirmUnpark != nil }, set: { if !$0 { confirmUnpark = nil } }),
                            presenting: confirmUnpark) { path in
            Button("Remove", role: .destructive) {
                if selectedParked == path {
                    selectedParked = nil
                }
                Task { await store.perform { _ = try await $0.unpark(path) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: { path in
            Text("The sites in \(path) are no longer served. The folder and its projects are not touched.")
        }
        .sheet(isPresented: showApplySheetState.projectedValue) {
            if let applyTLD {
                ApplyTLDSheet(tld: applyTLD, lines: applyLines, running: applying, declined: applyDeclined)
            }
        }
    }

    /// The app's version, what the last check found, and the button to check or update.
    private var appUpdates: some View {
        let updater = model.updater
        return LabeledContent {
            HStack(spacing: 8) {
                Text(updateState(updater)).foregroundStyle(.secondary)
                if updater.isEnabled {
                    Button(updater.availableVersion.map { "Update to \($0)" } ?? "Check for Updates") {
                        updater.checkForUpdates()
                    }
                    .disabled(updater.isChecking)
                }
            }
        } label: {
            Text("Flock")
            Text("Version \(Updater.appVersion)")
        }
    }

    private func updateState(_ updater: Updater) -> String {
        if !updater.isEnabled {
            return "Updates are off in development builds"
        }
        if updater.isChecking {
            return "Checking…"
        }
        if let version = updater.availableVersion {
            return "\(version) is available"
        }
        return updater.upToDate ? "Up to date" : ""
    }

    /// How names under the TLD resolve: Flock's DNS server through /etc/resolver, which has no
    /// fallback on macOS (Windows also writes site domains to the hosts file).
    private func domainExplanation(_ tld: String) -> String {
        guard let dns = model.status?.dns else { return "" }
        if dns.wildcard {
            return "Every name, any subdomain included, is resolved by Flock's DNS server (/etc/resolver/\(tld))."
        }
        let problem = dns.wildcardProblem.isEmpty ? "Flock's DNS server isn't running" : dns.wildcardProblem
        return "*.\(tld) doesn't resolve: \(problem). macOS has no hosts-file fallback, so sites don't open until it does; `flock install` sets /etc/resolver/\(tld) up again."
    }

    private func setLaunchAtLogin(_ on: Bool) {
        loginError = LoginItem.set(on)
        launchAtLogin = LoginItem.isEnabled
    }

    // MARK: The domain picker

    /// The domains the menu offers; the config can hold any other one, which the custom field edits.
    private static let domainPresets = ["test", "loc", "local"]
    private static let customTag = "custom"

    /// What the menu shows: the draft, else the preset of the current domain, else Custom….
    private func domainSelection(_ tld: String) -> String {
        if let draftTLD { return draftTLD }
        return custom.isEmpty && Self.domainPresets.contains(tld) ? tld : Self.customTag
    }

    /// A menu choice: a preset becomes the draft; Custom… clears the draft and prefills the field.
    private func pickDomain(_ tag: String, current: String) {
        if tag == Self.customTag {
            draftTLD = nil
            custom = current
        } else {
            draftTLD = tag
        }
    }

    /// The domain to check and apply: the picked preset, else the trimmed custom text.
    private var candidate: String {
        draftTLD ?? custom.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Runs the bundled CLI's `flock tld`, streaming its output in the sheet — the same runner as
    /// the setup's install, password dialog and all. On success the refresh moves every row.
    private func applyDomain(_ tld: String) async {
        let store = model.settings
        applying = true
        defer { applying = false }
        applyTLD = tld
        applyLines = []
        applyDeclined = false
        showApplySheet = true
        guard let binary = model.installation.bundledFlock else {
            applyLines = ["This build of Flock has no bundled CLI — run `flock tld \(tld)` in a terminal."]
            return
        }
        let result = await Installation.run(binary, ["tld", tld]) { line in
            // Lines arrive from the pipe's thread; late ones lose to the final output below.
            Task { @MainActor in
                if applying {
                    applyLines.append(line)
                }
            }
        }
        applyLines = result.output.split(separator: "\n").map(String.init)
        applyDeclined = result.permissionDeclined
        if result.ok {
            showApplySheet = false
            applyTLD = nil
            draftTLD = nil
            custom = ""
            check = nil
            checkNote = nil
            await store.refresh()
        }
    }
}

/// The Apply sheet: the `flock tld` output as it streams, like the setup window's install log.
/// The CLI's own text is the story; the sheet only adds the declined-password footer.
private struct ApplyTLDSheet: View {
    let tld: String
    let lines: [String]
    let running: Bool
    let declined: Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(running ? "Switching to .\(tld)…" : "Switching to .\(tld) didn’t finish").font(.headline)
            if running {
                ProgressView().progressViewStyle(.linear)
            }
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                            Text(line)
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        Color.clear.frame(height: 1).id("end")
                    }
                    .padding(10)
                }
                .frame(height: 200)
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: lines) { proxy.scrollTo("end", anchor: .bottom) }
            }
            if declined {
                Text("You declined the password. Flock’s config is on .\(tld); finish the switch by running `flock tld \(tld)` in a terminal.")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Close") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(running)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}
