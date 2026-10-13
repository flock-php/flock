import FlockKit
import SwiftUI

struct NodePane: View {
    @Environment(AppModel.self) private var model
    private let versionState = State<String>(initialValue: "")
    private var version: String {
        get { versionState.wrappedValue }
        nonmutating set { versionState.wrappedValue = newValue }
    }
    private let installingState = State<Bool>(initialValue: false)
    private var installing: Bool {
        get { installingState.wrappedValue }
        nonmutating set { installingState.wrappedValue = newValue }
    }
    private let resultState = State<String?>(initialValue: nil)
    private var result: String? {
        get { resultState.wrappedValue }
        nonmutating set { resultState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "Node", subtitle: "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.")
            Form {
                if let config = store.config {
                    Section("Default") {
                        Picker("Default version", selection: Binding(
                            get: { config.defaultNode },
                            set: { major in Task { await store.perform { _ = try await $0.updateSettings(SettingsChange(defaultNode: major)) } } }
                        )) {
                            ForEach(majors(config), id: \.self) { major in
                                Text("Node \(major)").tag(major)
                            }
                        }
                    }

                    Section("Installed versions") {
                        if config.node.isEmpty {
                            Text("No Node.js installed yet.").foregroundStyle(.secondary)
                        }
                        ForEach(config.node, id: \.self) { full in
                            HStack(spacing: 10) {
                                SettingsIcon(symbol: "hexagon.fill", color: .green, size: 26)
                                Text("Node \(full)").fontWeight(.medium)
                                if full.hasPrefix(config.defaultNode + ".") {
                                    Badge(text: "Default")
                                }
                            }
                            .padding(.vertical, 2)
                        }
                    }

                    Section {
                        HStack {
                            TextField("Version", text: versionState.projectedValue, prompt: Text("e.g. 20 or 20.19.5"))
                                .labelsHidden()
                                .frame(maxWidth: 220)
                                .onSubmit(install)
                            Button("Install", action: install)
                                .disabled(installing || version.trimmingCharacters(in: .whitespaces).isEmpty)
                            if installing {
                                ProgressView().controlSize(.small)
                                Text("Downloading…").font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                        }
                        if let result {
                            Text(result).font(.caption).foregroundStyle(.secondary)
                        }
                    } header: {
                        Text("Install a version")
                    } footer: {
                        Text("Official builds from nodejs.org, verified against their checksums.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }

    /// Installed majors plus the configured default, so the picker always has a match.
    private func majors(_ config: FlockConfig) -> [String] {
        let majors = config.nodeMajors
        return majors.contains(config.defaultNode) || config.defaultNode.isEmpty ? majors : [config.defaultNode] + majors
    }

    private func install() {
        let requested = version.trimmingCharacters(in: .whitespaces)
        guard !requested.isEmpty, !installing else { return }
        installing = true
        result = nil
        Task {
            do {
                let installed = try await model.client.installNode(requested)
                result = "Installed Node \(installed)."
                version = ""
            } catch {
                model.settings.error = error.localizedDescription
            }
            installing = false
            await model.settings.refresh()
        }
    }
}
