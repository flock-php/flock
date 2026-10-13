import FlockKit
import SwiftUI

/// Node.js lines from nodejs.org, like the PHP pane: install, update, make default and uninstall
/// them, and update their npm. Exact versions a project pins (.nvmrc) are offered by `node` in
/// the terminal instead.
struct NodePane: View {
    @Environment(AppModel.self) private var model
    private let checkingState = State<Bool>(initialValue: false)
    private var checking: Bool {
        get { checkingState.wrappedValue }
        nonmutating set { checkingState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "Node", subtitle: "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.")
            Form {
                if let catalog = store.node {
                    Section {
                        if catalog.versions.isEmpty {
                            Text("No Node.js versions to show yet.").foregroundStyle(.secondary)
                        }
                        ForEach(catalog.versions) { release in
                            NodeReleaseRow(release: release)
                        }
                    } header: {
                        HStack {
                            Text("Versions")
                            Spacer()
                            checkedLine(catalog)
                        }
                    } footer: {
                        VStack(alignment: .leading, spacing: 6) {
                            if !catalog.checkError.isEmpty {
                                Label("Couldn't reach nodejs.org: \(catalog.checkError)", systemImage: "exclamationmark.triangle.fill")
                                    .foregroundStyle(.orange)
                            }
                            Text("Official builds from nodejs.org, verified against their checksums. A project that pins an exact version in .nvmrc gets it offered when you run node, npm or npx there. Global packages (npm install -g) belong to a Node line and survive its updates; those of the default version are on your PATH.")
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        .font(.caption)
                    }
                } else {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("Loading Node.js versions…").foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .task { await store.refreshNode() }
    }

    @ViewBuilder
    private func checkedLine(_ catalog: NodeCatalog) -> some View {
        HStack(spacing: 6) {
            if let checked = catalog.checkedAt {
                Text("Checked \(checked, format: .relative(presentation: .named))")
            } else {
                Text("Not checked yet")
            }
            Text("·")
            if checking {
                ProgressView().controlSize(.mini)
            } else {
                Button("Check now") {
                    checking = true
                    Task {
                        await model.settings.performOnNode { try await $0.checkNodeReleases() }
                        checking = false
                    }
                }
                .buttonStyle(.link)
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .textCase(nil)
    }
}

/// One Node.js line: name, version, badges, npm, state and the actions that fit it.
private struct NodeReleaseRow: View {
    @Environment(AppModel.self) private var model
    let release: NodeRelease

    // What the row is doing while the daemon works (installs answer when they're done).
    private let busyState = State<String?>(initialValue: nil)
    private var busy: String? {
        get { busyState.wrappedValue }
        nonmutating set { busyState.wrappedValue = newValue }
    }
    private let confirmUninstallState = State<Bool>(initialValue: false)
    private var confirmUninstall: Bool {
        get { confirmUninstallState.wrappedValue }
        nonmutating set { confirmUninstallState.wrappedValue = newValue }
    }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            SettingsIcon(symbol: "hexagon.fill", color: release.isInstalled ? .green : .gray, size: 26)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text("Node \(release.major)").font(.body.weight(.semibold))
                    Text(versionText).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                    if release.isDefault { Badge(text: "Default") }
                    supportBadge
                }
                detail
            }

            Spacer(minLength: 12)
            actions
        }
        .padding(.vertical, 3)
        .confirmationDialog("Uninstall Node \(release.major)?", isPresented: confirmUninstallState.projectedValue) {
            Button("Uninstall", role: .destructive) {
                run("Uninstalling…") { try await $0.uninstallNode(release.major) }
            }
        } message: {
            Text("Its global npm packages are removed too. Projects and sites that use Node \(release.major) fall back to the default version until you install it again.")
        }
    }

    private var versionText: String {
        release.update ? "\(release.installed) → \(release.latest)" : (release.isInstalled ? release.installed : release.latest)
    }

    @ViewBuilder
    private var supportBadge: some View {
        let until = release.endOfLife.isEmpty ? "" : " until \(release.endOfLife)"
        switch release.support {
        case .lts:
            Badge(text: release.codename.isEmpty ? "LTS" : "LTS · \(release.codename)").help("Long-term support\(until)")
        case .maintenance:
            Badge(text: "Maintenance", color: .secondary).help("Security fixes only\(until)")
        case .current:
            Badge(text: "Current", color: .secondary).help("The newest line, without long-term support\(until)")
        case .eol:
            Badge(text: "End of life", color: .orange).help(release.endOfLife.isEmpty ? "No more updates" : "No more updates since \(release.endOfLife)")
        case .unknown:
            EmptyView()
        }
    }

    /// The status line under the name.
    @ViewBuilder
    private var detail: some View {
        if let busy {
            HStack(spacing: 6) {
                ProgressView().controlSize(.mini)
                Text(busy)
            }
            .font(.caption).foregroundStyle(.secondary)
        } else {
            HStack(spacing: 0) {
                Text(release.update ? "Update available" : (release.isInstalled ? "Up to date" : "Not installed"))
                    .foregroundStyle(release.update ? Color.blue : Color.secondary)
                if !release.npm.isEmpty {
                    Text(" · npm \(release.npm)").foregroundStyle(.secondary)
                    if !release.npmLatest.isEmpty {
                        Text(" (npm \(release.npmLatest) available)").foregroundStyle(.blue)
                    }
                }
            }
            .font(.caption)
        }
    }

    @ViewBuilder
    private var actions: some View {
        if busy != nil {
            EmptyView()
        } else if !release.isInstalled {
            Button {
                run("Downloading Node.js \(release.latest)…") { try await $0.installNodeMajor(release.major) }
            } label: {
                Label("Install", systemImage: "arrow.down.circle")
            }
            .help("Downloads Node.js \(release.latest) from nodejs.org")
        } else {
            if release.update {
                Button {
                    run("Downloading Node.js \(release.latest)…") { try await $0.installNodeMajor(release.major) }
                } label: {
                    Label("Update to \(release.latest)", systemImage: "arrow.down.circle")
                }
                .buttonStyle(.borderedProminent)
                .help("Downloads Node.js \(release.latest), then removes \(release.installed). Global packages stay.")
            }
            if !release.npmLatest.isEmpty {
                Button("Update npm") {
                    run("Updating npm to \(release.npmLatest)…") { try await $0.updateNpm(release.major) }
                }
                .help("Installs npm \(release.npmLatest), the newest npm that supports Node.js \(release.installed), for Node \(release.major)")
            }
            if !release.isDefault {
                Button("Make Default") {
                    Task {
                        await model.settings.perform { _ = try await $0.updateSettings(SettingsChange(defaultNode: release.major)) }
                        await model.settings.refreshNode()
                    }
                }
            }
            Menu {
                Button("Uninstall…", role: .destructive) { confirmUninstall = true }
                    .disabled(release.isDefault)
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("More")
        }
    }

    private func run(_ step: String, _ action: @escaping @Sendable (FlockClient) async throws -> NodeCatalog) {
        busy = step
        Task {
            await model.settings.performOnNode(action)
            busy = nil
        }
    }
}
