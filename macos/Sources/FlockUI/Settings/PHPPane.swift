import AppKit
import FlockKit
import SwiftUI

/// PHP versions from the binaries manifest: install, update and uninstall them, with download progress.
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
                            Text("PHP-FPM")
                            Spacer()
                            checkedLine(catalog)
                        }
                    } footer: {
                        if !catalog.checkError.isEmpty {
                            Label(catalog.checkError, systemImage: "exclamationmark.triangle.fill")
                                .font(.caption).foregroundStyle(.orange)
                        }
                    }

                    if let frankenPHP = store.frankenPHP, frankenPHP.available {
                        Section {
                            ForEach(frankenPHP.builds) { build in
                                FrankenPHPBuildRow(build: build)
                            }
                        } header: {
                            Text("FrankenPHP")
                        } footer: {
                            if !frankenPHP.checkError.isEmpty {
                                Label(frankenPHP.checkError, systemImage: "exclamationmark.triangle.fill")
                                    .font(.caption).foregroundStyle(.orange)
                            }
                        }
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
            PHPInstallLogSheet(minor: target.minor)
        }
        // A flock://install/php link (AppModel.handle): nothing installs until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingInstall = nil } }),
               presenting: linkRequest) { request in
            if let install = request.confirmation {
                Button("Install") {
                    let minor = install.line
                    Task { await store.performOnPHP { try await $0.installPHP(minor) } }
                }
                Button("Cancel", role: .cancel) {}
            } else {
                Button("OK") {}
            }
        } message: { request in
            Text(request.message)
        }
        .task {
            // Poll quickly while something downloads, slowly otherwise.
            while !Task.isCancelled {
                await store.refreshPHP()
                await store.refreshFrankenPHP()
                let active = (store.php?.hasActiveJob ?? false) || (store.frankenPHP?.hasRunningJob ?? false)
                try? await Task.sleep(for: .seconds(active ? 2 : 30))
            }
        }
    }

    /// What a pending flock://install link for PHP asks for, once the versions are loaded.
    private var linkRequest: InstallLink.Resolution? {
        guard let link = model.pendingInstall, link.service == "php" else { return nil }
        return link.resolve(php: model.settings.php)
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
                    if release.prerelease {
                        Badge(text: "Pre-release", color: .orange).help("PHP \(release.latest) isn’t released yet — it may not work. Offered on the beta channel.")
                    }
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
        if release.update && release.latest != release.installed {
            return "\(release.installed) → \(release.latest)"
        }
        return release.isInstalled ? release.installed : release.latest
    }

    /// The status line under the name: an install in progress, the error of a failed one, a
    /// withdrawn build, or the state.
    @ViewBuilder
    private var detail: some View {
        if let job = release.job, job.isRunning {
            HStack(spacing: 6) {
                if let progress = job.progress, job.phase != .installing {
                    ProgressView(value: progress).frame(width: 120)
                } else {
                    ProgressView().controlSize(.mini)
                }
                Text(Self.jobText(job))
            }
            .font(.caption).foregroundStyle(.secondary)
        } else if let job = release.job, job.isFailed {
            Text(job.error.isEmpty ? "The download failed." : job.error)
                .font(.caption).foregroundStyle(.red)
                .lineLimit(2)
                .textSelection(.enabled)
                .help(job.error)
        } else if !release.yanked.isEmpty {
            Label(release.update ? "Withdrawn: \(release.yanked) — update to replace it" : "Withdrawn: \(release.yanked)", systemImage: "exclamationmark.triangle.fill")
                .font(.caption).foregroundStyle(.orange)
                .lineLimit(2)
                .help("This build was withdrawn. Flock replaces it as soon as a fixed build is published.")
        } else {
            Text(statusText).font(.caption).foregroundStyle(release.update ? Color.blue : Color.secondary)
        }
    }

    private var statusText: String {
        switch release.status {
        case .installed: "Up to date"
        case .update: release.latest == release.installed ? "A new build is available" : "Update available"
        case .available: "Not installed"
        case .unknown: ""
        }
    }

    /// "Downloading… 42%", "Downloading…" (size unknown) or "Installing…".
    private static func jobText(_ job: ComponentJob) -> String {
        if job.phase == .installing {
            return "Installing…"
        }
        if let progress = job.progress {
            return "Downloading… \(Int((progress * 100).rounded()))%"
        }
        return "Downloading…"
    }

    private var sizeHelp: String {
        release.size > 0 ? "Downloads PHP \(release.latest) (\(release.size / 1_000_000) MB)" : ""
    }

    @ViewBuilder
    private var actions: some View {
        let store = model.settings

        if let job = release.job, job.isRunning {
            Button("Show Log", action: showLog).buttonStyle(.link)
            Button("Cancel") {
                run { try await $0.cancelPHPInstall(release.minor) }
            }
        } else if let job = release.job, job.isFailed {
            Button("Show Log", action: showLog).buttonStyle(.link)
            Button {
                run { try await $0.installPHP(release.minor) }
            } label: {
                Label("Retry", systemImage: "arrow.clockwise")
            }
            Button("Dismiss") {
                run { try await $0.dismissPHPJob(release.minor) }
            }
        } else if release.isInstalled {
            if release.update {
                Button {
                    run { try await $0.installPHP(release.minor) }
                } label: {
                    Label(release.latest == release.installed ? "Update" : "Update to \(release.latest)", systemImage: "arrow.down.circle")
                }
                .buttonStyle(.borderedProminent)
                .help(sizeHelp)
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
                Button("Show Last Install Log", action: showLog)
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
        } else {
            Button {
                run { try await $0.installPHP(release.minor) }
            } label: {
                Label("Install", systemImage: "arrow.down.circle")
            }
            .help(sizeHelp)
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

/// The FrankenPHP build for one PHP minor: what is installed, a download in progress or a failed
/// one, and the actions that fit.
private struct FrankenPHPBuildRow: View {
    @Environment(AppModel.self) private var model
    let build: FrankenPHPBuild

    /// A request of this row is waiting for the daemon; its buttons stay disabled until it answers.
    private let busyState = State<Bool>(initialValue: false)
    private var busy: Bool {
        get { busyState.wrappedValue }
        nonmutating set { busyState.wrappedValue = newValue }
    }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            SettingsIcon(symbol: "bolt.fill", color: build.isInstalled ? .purple : .gray, size: 26)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text("PHP \(build.php)").font(.body.weight(.semibold))
                    if build.prerelease {
                        Badge(text: "Pre-release", color: .orange).help("PHP \(build.latest) isn’t released yet — it may not work. Offered on the beta channel.")
                    }
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
    }

    private var installedText: String {
        guard build.isInstalled else { return "Not installed · downloads by itself when a site uses it" }
        return build.phpVersion.isEmpty ? "FrankenPHP \(build.installed)" : "FrankenPHP \(build.installed) (PHP \(build.phpVersion))"
    }

    /// The line under the name: a download in progress, the error of a failed one, or what is installed.
    @ViewBuilder
    private var detail: some View {
        if let job = build.job, job.isRunning {
            HStack(spacing: 6) {
                if let progress = job.progress, job.phase != .installing {
                    ProgressView(value: progress).frame(width: 120)
                } else {
                    ProgressView().controlSize(.mini)
                }
                Text(Self.jobText(job))
            }
            .font(.caption).foregroundStyle(.secondary)
        } else if let job = build.job, job.isFailed {
            Text(job.error.isEmpty ? "The download failed." : job.error)
                .font(.caption).foregroundStyle(.red)
                .lineLimit(2)
                .textSelection(.enabled)
                .help(job.error)
        } else if !build.yanked.isEmpty {
            Label(build.update ? "Withdrawn: \(build.yanked) — update to replace it" : "Withdrawn: \(build.yanked)", systemImage: "exclamationmark.triangle.fill")
                .font(.caption).foregroundStyle(.orange)
                .lineLimit(2)
                .help("This build was withdrawn. Flock replaces it as soon as a fixed build is published.")
        } else {
            Text(installedText).font(.caption).foregroundStyle(.secondary)
        }
    }

    /// "Downloading… 42%", "Downloading…" (size unknown) or "Installing…".
    private static func jobText(_ job: ComponentJob) -> String {
        if job.phase == .installing {
            return "Installing…"
        }
        if let progress = job.progress {
            return "Downloading… \(Int((progress * 100).rounded()))%"
        }
        return "Downloading…"
    }

    @ViewBuilder
    private var actions: some View {
        if build.job?.isRunning == true {
            Button("Cancel") {
                run { try await $0.cancelFrankenPHPJob(build.php) }
            }
        } else if build.job?.isFailed == true {
            Button {
                run { try await $0.installFrankenPHP(build.php) }
            } label: {
                Label("Retry", systemImage: "arrow.clockwise")
            }
            Button("Dismiss") {
                run { try await $0.dismissFrankenPHPJob(build.php) }
            }
        } else {
            // Installing ahead makes switching a site to FrankenPHP immediate (else PHP-FPM serves it meanwhile).
            if !build.isInstalled, !build.latest.isEmpty {
                Button {
                    run { try await $0.installFrankenPHP(build.php) }
                } label: {
                    Label("Install", systemImage: "arrow.down.circle")
                }
                .help(build.size > 0 ? "Downloads FrankenPHP for PHP \(build.latest) (\(build.size / 1_000_000) MB)" : "Downloads FrankenPHP for PHP \(build.latest)")
            }
            if build.update {
                Button {
                    run { try await $0.installFrankenPHP(build.php) }
                } label: {
                    Label(build.latest == build.phpVersion ? "Update" : "Update to PHP \(build.latest)", systemImage: "arrow.down.circle")
                }
                .buttonStyle(.borderedProminent)
                .help(build.size > 0 ? "Downloads FrankenPHP for PHP \(build.latest) (\(build.size / 1_000_000) MB)" : "")
            }
            if build.isInstalled {
                Button("Remove") {
                    run { try await $0.uninstallFrankenPHP(build.php) }
                }
            }
        }
    }
}

extension FrankenPHPBuildRow {
    /// Runs a FrankenPHP action for this row, with its buttons disabled until the daemon answers.
    fileprivate func run(_ action: @escaping @Sendable (FlockClient) async throws -> FrankenPHPState) {
        busy = true
        Task {
            await model.settings.performOnFrankenPHP(action)
            busy = false
        }
    }
}

/// The tail of a version's install log, refreshed while the sheet is open.
private struct PHPInstallLogSheet: View {
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
                Text("PHP \(minor) install log").font(.headline)
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
                    Text(text.isEmpty ? "No install log yet." : text)
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
                let log = await model.settings.phpInstallLog(minor)
                let lines = log.split(separator: "\n", omittingEmptySubsequences: false)
                let tail = lines.suffix(400).joined(separator: "\n")
                if tail != text {
                    text = tail
                }
                let active = model.settings.php?.versions.first { $0.minor == minor }?.job?.isRunning ?? false
                if !active, !text.isEmpty {
                    break
                }
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }
}
