import FlockKit
import ServiceManagement
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
    private let launchAtLoginState = State<Bool>(initialValue: SMAppService.mainApp.status == .enabled)
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
                        LabeledContent("Domain", value: "*.\(config.tld)")
                        LabeledContent("HTTP port", value: String(config.ports.http))
                        LabeledContent("HTTPS port", value: String(config.ports.https))
                        LabeledContent("DNS port", value: String(config.ports.dns))
                    } header: {
                        Text("Network")
                    } footer: {
                        Text("Changing the domain or these ports needs `flock install` (it updates DNS and certificates).")
                            .font(.caption).foregroundStyle(.secondary)
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
                    Toggle("Open Flock’s menu bar app at login", isOn: Binding(get: { launchAtLogin }, set: setLaunchAtLogin))
                    if let loginError {
                        Text(loginError).font(.caption).foregroundStyle(.orange)
                    }
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
                                Text("No parked folders").foregroundStyle(.secondary)
                            }
                        }
                        HStack(spacing: 0) {
                            Button {
                                for url in SettingsStore.chooseDirectories(prompt: "Park", multiple: true) {
                                    Task { await store.perform { _ = try await $0.park(url.path) } }
                                }
                            } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                            Button {
                                guard let path = selectedParked else { return }
                                selectedParked = nil
                                Task { await store.perform { _ = try await $0.forget(path) } }
                            } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                            .disabled(selectedParked == nil)
                            Spacer()
                        }
                        .buttonStyle(.borderless)
                    } header: {
                        Text("Parked folders")
                    } footer: {
                        Text("Every folder inside a parked folder is served as <folder>.\(config.tld).")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }

    private func setLaunchAtLogin(_ on: Bool) {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            loginError = SMAppService.mainApp.status == .requiresApproval
                ? "Allow Flock in System Settings → General → Login Items."
                : nil
        } catch {
            loginError = "Could not change the login item: \(error.localizedDescription)"
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }
}
