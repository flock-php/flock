import AppKit
import FlockKit
import SwiftUI

/// PHP versions from php.net: install, update, rebuild and uninstall them, with build progress.
struct PHPPane: View {
    @Environment(AppModel.self) private var model
    private let logMinorState = State<String?>(initialValue: nil)
    private var logMinor: String? {
        get { logMinorState.wrappedValue }
        nonmutating set { logMinorState.wrappedValue = newValue }
    }
    private let checkingState = State<Bool>(initialValue: false)
    private var checking: Bool {
        get { checkingState.wrappedValue }
        nonmutating set { checkingState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "PHP", subtitle: "Every installed version runs its own PHP-FPM pool. Sites use the default unless pinned to a version.")
            Form {
                if let catalog = store.php {
                    Section {
                        ForEach(catalog.versions) { release in
                            PHPReleaseRow(release: release, ini: ini(for: release), showLog: { logMinor = release.minor })
                        }
                    } header: {
                        HStack {
                            Text("Versions")
                            Spacer()
                            checkedLine(catalog)
                        }
                    } footer: {
                        if !catalog.checkError.isEmpty {
                            Label(catalog.checkError, systemImage: "exclamationmark.triangle.fill")
                                .font(.caption).foregroundStyle(.orange)
                        }
                    }

                    Section {
                        Toggle("Build updates and new PHP versions automatically", isOn: Binding(
                            get: { catalog.autoBuild },
                            set: { on in Task { await store.perform { _ = try await $0.updateSettings(SettingsChange(phpAutoBuild: on)) }; await store.refreshPHP() } }
                        ))
                    } header: {
                        Text("Background builds")
                    } footer: {
                        Text("Patch updates for installed versions and newly released PHP versions are built in the background (low priority), so installing them is instant. Each build takes ~5–10 minutes and uses the Xcode Command Line Tools. Pre-releases are not shown.")
                            .font(.caption).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                } else {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("Loading PHP versions…").foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .sheet(item: Binding(get: { logMinor.map(LogTarget.init) }, set: { logMinor = $0?.minor })) { target in
            PHPBuildLogSheet(minor: target.minor)
        }
        .task {
            // Poll quickly while something builds, slowly otherwise.
            while !Task.isCancelled {
                await store.refreshPHP()
                let active = store.php?.hasActiveJob ?? false
                try? await Task.sleep(for: .seconds(active ? 2 : 30))
            }
        }
    }

    private func ini(for release: PHPRelease) -> String? {
        model.settings.config?.php.first { $0.version == release.minor }?.ini
    }

    @ViewBuilder
    private func checkedLine(_ catalog: PHPCatalog) -> some View {
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
                        await model.settings.performOnPHP { try await $0.checkPHPReleases() }
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

private struct LogTarget: Identifiable {
    let minor: String
    var id: String { minor }
}

/// One PHP minor: name, version, badges, state and the actions that fit it.
private struct PHPReleaseRow: View {
    @Environment(AppModel.self) private var model
    let release: PHPRelease
    let ini: String?
    let showLog: () -> Void

    private let confirmUninstallState = State<Bool>(initialValue: false)
    private var confirmUninstall: Bool {
        get { confirmUninstallState.wrappedValue }
        nonmutating set { confirmUninstallState.wrappedValue = newValue }
    }
    /// A request of this row is waiting for the daemon; its buttons stay disabled until it answers.
    private let busyState = State<Bool>(initialValue: false)
    private var busy: Bool {
        get { busyState.wrappedValue }
        nonmutating set { busyState.wrappedValue = newValue }
    }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            SettingsIcon(symbol: "chevron.left.forwardslash.chevron.right", color: release.isInstalled ? .indigo : .gray, size: 26)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text("PHP \(release.minor)").font(.body.weight(.semibold))
                    Text(versionText).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                    if release.isDefault { Badge(text: "Default") }
                    if !release.supported { Badge(text: "End of life", color: .orange) }
                    if release.isInstalled && release.xdebug { Badge(text: "Xdebug", color: .red) }
                }
                detail
            }

            Spacer(minLength: 12)
            if busy {
                ProgressView().controlSize(.small)
            }
            actions
                .disabled(busy)
        }
        .padding(.vertical, 3)
        .confirmationDialog("Uninstall PHP \(release.minor)?", isPresented: confirmUninstallState.projectedValue) {
            Button("Uninstall", role: .destructive) {
                run { try await $0.uninstallPHP(release.minor) }
            }
        } message: {
            Text("Its php.ini is removed too. Sites pinned to PHP \(release.minor) stop working until you pick another version.")
        }
    }

    private var versionText: String {
        switch release.status {
        case .update: "\(release.installed) → \(release.latest)"
        default: release.isInstalled ? release.installed : release.latest
        }
    }

    /// The status line under the name.
    @ViewBuilder
    private var detail: some View {
        if let job = release.job, job.isActive {
            HStack(spacing: 6) {
                ProgressView().controlSize(.mini)
                Text(job.state == .queued ? "Waiting for another build…" : (job.step.isEmpty ? "Building…" : job.step))
                    .lineLimit(1).truncationMode(.middle)
                if job.auto {
                    Text("· Background build").foregroundStyle(.tertiary)
                }
            }
            .font(.caption).foregroundStyle(.secondary)
        } else if release.status == .failed || release.job?.state == .failed {
            Text(release.job?.error.isEmpty == false ? release.job!.error : "The last build failed.")
                .font(.caption).foregroundStyle(.red)
                .lineLimit(2)
        } else if release.status == .unsupported, !release.note.isEmpty {
            Text(release.note)
                .font(.caption).foregroundStyle(.secondary)
                .lineLimit(2)
                .help(release.note)
        } else {
            Text(statusText).font(.caption).foregroundStyle(statusColor)
        }
    }

    private var statusText: String {
        switch release.status {
        case .installed: "Up to date"
        case .update: release.updateReady ? "Update ready to install" : "Update available"
        case .ready: "Built and ready to install"
        case .likely: "Not installed"
        case .unsupported: "Not yet buildable"
        case .failed: "The last build failed"
        case .unknown: ""
        }
    }

    private var statusColor: Color {
        switch release.status {
        case .update: .blue
        case .ready: .green
        case .unsupported: .secondary
        case .failed: .red
        default: .secondary
        }
    }

    @ViewBuilder
    private var actions: some View {
        let store = model.settings

        if let job = release.job, job.isActive {
            Button("Show Log", action: showLog).buttonStyle(.link)
            Button("Cancel") {
                run { try await $0.cancelPHPBuild(release.minor) }
            }
        } else {
            switch release.status {
            case .installed, .update:
                if release.status == .update {
                    Button {
                        run { try await $0.installPHP(release.minor) }
                    } label: {
                        Label("Update to \(release.latest)", systemImage: release.updateReady ? "bolt.fill" : "arrow.down.circle")
                    }
                    .buttonStyle(.borderedProminent)
                    .help(release.updateReady ? "Already built — installs instantly" : "Builds \(release.latest) (5–10 minutes), then installs it")
                }
                if !release.isDefault {
                    Button("Make Default") {
                        busy = true
                        Task {
                            await store.perform { _ = try await $0.updateSettings(SettingsChange(defaultPHP: release.minor)) }
                            await store.refreshPHP()
                            busy = false
                        }
                    }
                }
                Menu {
                    if let ini, !ini.isEmpty {
                        Button("Open php.ini") { NSWorkspace.shared.open(URL(fileURLWithPath: ini)) }
                    }
                    Button("Rebuild") {
                        run { try await $0.buildPHP(release.minor) }
                    }
                    Button("Show Last Build Log", action: showLog)
                    Divider()
                    Button("Uninstall…", role: .destructive) { confirmUninstall = true }
                        .disabled(release.isDefault)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")

            case .ready, .likely, .failed, .unknown:
                if release.status == .failed {
                    Button("Show Log", action: showLog).buttonStyle(.link)
                }
                Button {
                    run { try await $0.installPHP(release.minor) }
                } label: {
                    Label(release.status == .failed ? "Retry" : "Install",
                          systemImage: release.status == .ready ? "bolt.fill" : "arrow.down.circle")
                }
                .help(release.status == .ready ? "Already built — installs instantly" : "Builds PHP \(release.latest) (5–10 minutes), then installs it")

            case .unsupported:
                // With a note the version is permanently out of reach; without one the tooling just lags behind.
                Button(release.note.isEmpty ? "Not yet buildable" : "Not buildable") {}
                    .disabled(true)
                    .help(release.note.isEmpty ? "The build tooling doesn't support PHP \(release.minor) yet. Flock checks again automatically." : release.note)
            }
        }
    }
}

extension PHPReleaseRow {
    /// Runs a PHP action for this row, with its buttons disabled until the daemon answers.
    fileprivate func run(_ action: @escaping @Sendable (FlockClient) async throws -> PHPCatalog) {
        busy = true
        Task {
            await model.settings.performOnPHP(action)
            busy = false
        }
    }
}

/// The tail of a version's build log, refreshed while the sheet is open.
private struct PHPBuildLogSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let minor: String

    private let textState = State<String>(initialValue: "")
    private var text: String {
        get { textState.wrappedValue }
        nonmutating set { textState.wrappedValue = newValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("PHP \(minor) build log").font(.headline)
                Spacer()
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(text, forType: .string)
                }
                .disabled(text.isEmpty)
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }

            ScrollViewReader { proxy in
                ScrollView {
                    Text(text.isEmpty ? "No build log yet." : text)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(10)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: text) { proxy.scrollTo("end", anchor: .bottom) }
            }
        }
        .padding(16)
        .frame(width: 720, height: 460)
        .task {
            while !Task.isCancelled {
                let log = await model.settings.phpBuildLog(minor)
                let lines = log.split(separator: "\n", omittingEmptySubsequences: false)
                let tail = lines.suffix(400).joined(separator: "\n")
                if tail != text {
                    text = tail
                }
                let active = model.settings.php?.versions.first { $0.minor == minor }?.job?.isActive ?? false
                if !active, !text.isEmpty {
                    break
                }
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }
}
