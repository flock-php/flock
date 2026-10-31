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
                        Button(model.isRestarting ? "Restarting…" : "Restart Daemon") {
                            Task { await model.restart() }
                        }
                        .disabled(model.isRestarting)
                    }
                }

                if let config = store.config {
                    Section {
                        LabeledContent {
                            Text("*.\(config.tld)")
                        } label: {
                            Text("Domain")
                            Text(domainExplanation(config.tld))
                        }
                    } header: {
                        Text("Network")
                    } footer: {
                        // Sites are served on 80 and 443; other ports are only set by editing config.json.
                        if config.ports.http != 80 || config.ports.https != 443 {
                            Text("Sites use ports \(String(config.ports.http)) (HTTP) and \(String(config.ports.https)) (HTTPS), set in config.json, so their addresses include the port.")
                                .font(.caption).foregroundStyle(.secondary)
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
}
