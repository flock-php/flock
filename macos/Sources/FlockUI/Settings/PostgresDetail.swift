import AppKit
import FlockKit
import SwiftUI

/// PostgreSQL: install a major, see the one that runs, switch between installed majors (each
/// keeps its own databases), update and uninstall, and pick each major's extensions. The
/// command-line tools come with every major.
struct PostgresDetail: View {
    @Environment(AppModel.self) private var model
    let service: Service

    private let switchState = State<PostgresMajor?>(initialValue: nil)
    private let uninstallState = State<PostgresMajor?>(initialValue: nil)
    private let extensionsState = State<PostgresMajor?>(initialValue: nil)
    private let installWithState = State<PostgresMajor?>(initialValue: nil)

    var body: some View {
        let store = model.settings

        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let versions = store.postgres {
                    DetailHeader(title: "PostgreSQL", symbol: "cylinder.split.1x2.fill", color: .blue,
                                 status: headerStatus(versions), running: versions.hasInstalled ? versions.running : nil)

                    if versions.hasInstalled {
                        if let active = versions.activeMajor {
                            ActiveServerCard(versions: versions, major: active, service: service, locked: locked)
                        }
                        versionList(versions)
                    } else {
                        EmptyPostgres(versions: versions, locked: locked)
                    }

                    commandLineTools

                    if versions.hasInstalled {
                        Divider()
                        ServiceInfo(service: service)
                    }
                } else {
                    DetailHeader(title: "PostgreSQL", symbol: "cylinder.split.1x2.fill", color: .blue, status: "Loading…")
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 30)
                }
            }
            .padding(16)
        }
        .alert(item: switchState.projectedValue) { target in
            switchAlert(target)
        }
        .sheet(item: uninstallState.projectedValue) { major in
            UninstallPostgresSheet(major: major, versions: store.postgres) { deleteData in
                let id = major.major
                Task { await store.performOnPostgres(id, .uninstalling) { try await $0.uninstallPostgres(id, deleteData: deleteData) } }
            }
        }
        .sheet(item: extensionsState.projectedValue) { major in
            PostgresExtensionsSheet(major: major.major)
        }
        .alert("Also install the extensions of PostgreSQL \(store.postgres?.active ?? "")?",
               isPresented: Binding(get: { installWithState.wrappedValue != nil }, set: { if !$0 { installWithState.wrappedValue = nil } }),
               presenting: installWithState.wrappedValue) { target in
            let id = target.major
            let extensions = store.postgres?.activeMajor?.extensions ?? []
            Button("Install with Extensions") {
                Task { await store.performOnPostgres(id, .installing) { try await $0.installPostgres(id, extensions: extensions) } }
            }
            .keyboardShortcut(.defaultAction)
            Button("PostgreSQL Only") {
                Task { await store.performOnPostgres(id, .installing) { try await $0.installPostgres(id) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: { target in
            let names = PostgresExtensions.titles(store.postgres?.activeMajor?.extensions ?? [])
            Text("\(names) for PostgreSQL \(target.major), where they're available for it. You can change this later under Extensions….")
        }
    }

    /// Installs another major; when the active one has extensions, asks whether it gets them too.
    private func install(_ major: PostgresMajor) {
        if model.settings.postgres?.activeMajor?.extensions.isEmpty == false {
            installWithState.wrappedValue = major
            return
        }
        let id = major.major
        Task { await model.settings.performOnPostgres(id, .installing) { try await $0.installPostgres(id) } }
    }

    /// Something runs on a major: other PostgreSQL actions wait.
    private var locked: Bool {
        model.settings.postgres?.isBusy == true || !model.settings.postgresPending.isEmpty
    }

    private func headerStatus(_ versions: PostgresVersions) -> String {
        if let busy = versions.majors.first(where: { model.settings.busy($0) != .idle }) {
            return model.settings.busy(busy).describe(busy.major)
        }
        guard let active = versions.activeMajor else {
            return versions.hasInstalled ? "No version selected" : "Not installed"
        }
        let state = versions.running ? "Running" : (versions.enabled ? "Not running" : "Stopped")
        return "\(state) · PostgreSQL \(active.major) on port \(versions.port)"
    }

    // MARK: Versions

    private func versionList(_ versions: PostgresVersions) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DetailSectionTitle(title: "Versions") {
                CheckForUpdatesLine(checkedAt: versions.checkedAt, error: versions.checkError, disabled: locked) {
                    await model.settings.checkForUpdates()
                }
            }

            VStack(spacing: 0) {
                ForEach(Array(versions.majors.enumerated()), id: \.element.id) { index, major in
                    if index > 0 {
                        Divider().padding(.leading, 46)
                    }
                    PostgresMajorRow(major: major, busy: model.settings.busy(major), locked: locked,
                                     activeMajor: versions.active) {
                        switchState.wrappedValue = major
                    } uninstall: {
                        uninstallState.wrappedValue = major
                    } extensions: {
                        extensionsState.wrappedValue = major
                    } install: {
                        install(major)
                    }
                }
            }
            .card(padding: 0)

            if !versions.checkError.isEmpty {
                Label("Couldn't check for new releases: \(versions.checkError)", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(2)
                    .help(versions.checkError)
            }
            Text("Installed versions stay side by side. One runs at a time; each keeps its own databases, and updates within a version keep them.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func switchAlert(_ target: PostgresMajor) -> Alert {
        let current = model.settings.postgres?.active ?? ""
        let own = target.hasData
            ? "PostgreSQL \(target.major) uses its own databases."
            : "PostgreSQL \(target.major) starts with a new, empty set of databases."
        let message = current.isEmpty
            ? own
            : "PostgreSQL \(current) stops; its databases stay but aren't available until you switch back. \(own)"
        let id = target.major
        return Alert(
            title: Text("Switch to PostgreSQL \(target.major)?"),
            message: Text(message),
            primaryButton: .default(Text("Switch")) {
                Task { await model.settings.performOnPostgres(id, .switching) { try await $0.activatePostgres(id) } }
            },
            secondaryButton: .cancel()
        )
    }

    // MARK: Command-line tools

    private var commandLineTools: some View {
        Label("psql, pg_dump and the other command-line tools come with PostgreSQL. In Terminal, Flock uses those of the newest installed version, which work with every version above and with remote servers.",
              systemImage: "terminal")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }
}

extension PostgresMajor.Busy {
    /// "Installing PostgreSQL 17…"
    func describe(_ major: String) -> String {
        switch self {
        case .idle: ""
        case .installing: "Installing PostgreSQL \(major)…"
        case .updating: "Updating PostgreSQL \(major)…"
        case .switching: "Switching to PostgreSQL \(major)…"
        case .uninstalling: "Uninstalling PostgreSQL \(major)…"
        case let .other(raw): "\(raw.prefix(1).uppercased())\(raw.dropFirst()) PostgreSQL \(major)…"
        }
    }

    /// "Installing…", for a row.
    var short: String {
        switch self {
        case .idle: ""
        case .installing: "Installing…"
        case .updating: "Updating…"
        case .switching: "Switching…"
        case .uninstalling: "Uninstalling…"
        case let .other(raw): "\(raw.prefix(1).uppercased())\(raw.dropFirst())…"
        }
    }
}

/// The version that runs: state, version, port, data size and its update.
private struct ActiveServerCard: View {
    @Environment(AppModel.self) private var model
    let versions: PostgresVersions
    let major: PostgresMajor
    let service: Service
    let locked: Bool

    var body: some View {
        let busy = model.settings.busy(major)

        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text("PostgreSQL \(major.major)").font(.title2.weight(.semibold))
                Text(major.installed).font(.title3.monospacedDigit()).foregroundStyle(.secondary)
                Spacer()
                statePill(busy)
            }

            fact("Databases", major.hasData ? Formats.bytes(major.dataSize) : "None yet")

            if !major.yanked.isEmpty {
                Label(major.update ? "This build was withdrawn: \(major.yanked) — update to replace it" : "This build was withdrawn: \(major.yanked)",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .help("Flock replaces it as soon as a fixed build is published.")
            }

            if major.update, !major.latest.isEmpty {
                Divider()
                UpdateStrip(title: major.latest == major.installed ? "A new build of PostgreSQL \(major.latest) is available" : "PostgreSQL \(major.latest) is available",
                            note: "Your databases stay; PostgreSQL restarts briefly.",
                            help: "Installs \(major.latest) and restarts PostgreSQL \(major.major). Your databases stay.",
                            busy: busy == .updating, disabled: locked) {
                    let id = major.major
                    Task { await model.settings.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                }
            }
        }
        .card(padding: 14)
    }

    @ViewBuilder
    private func statePill(_ busy: PostgresMajor.Busy) -> some View {
        let switching = versions.majors.contains { model.settings.busy($0) == .switching }
        if busy != .idle || switching {
            HStack(spacing: 5) {
                ProgressView().controlSize(.mini)
                Text(switching ? "Switching…" : busy.short)
            }
            .font(.caption.weight(.medium)).foregroundStyle(.secondary)
        } else {
            Badge(text: "Active")
        }
    }

    private func fact(_ title: String, _ value: String) -> some View {
        HStack(spacing: 6) {
            Text(title).foregroundStyle(.secondary)
            Text(value).monospacedDigit()
        }
        .font(.callout)
    }
}

/// One major in the version list.
private struct PostgresMajorRow: View {
    @Environment(AppModel.self) private var model
    let major: PostgresMajor
    let busy: PostgresMajor.Busy
    let locked: Bool
    let activeMajor: String
    let switchTo: () -> Void
    let uninstall: () -> Void
    let extensions: () -> Void
    let install: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Text(major.major)
                .font(.system(.title3, design: .rounded).weight(.semibold).monospacedDigit())
                .foregroundStyle(major.isInstalled ? .primary : .tertiary)
                .frame(width: 26, alignment: .center)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    if major.isInstalled {
                        Text(major.installed).monospacedDigit()
                        if major.update, !major.latest.isEmpty {
                            Text(major.latest == major.installed ? "→ new build" : "→ \(major.latest)").monospacedDigit().foregroundStyle(Color.accentColor)
                                .help(major.latest == major.installed ? "A new build of PostgreSQL \(major.latest) is available" : "PostgreSQL \(major.latest) is available")
                        }
                        if !major.yanked.isEmpty {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                                .help("This build was withdrawn: \(major.yanked). Flock replaces it as soon as a fixed build is published.")
                        }
                    } else {
                        Text("Not installed").foregroundStyle(.secondary)
                    }
                    if major.prerelease {
                        Badge(text: "Pre-release", color: .orange)
                            .help("PostgreSQL \(major.latest) isn’t released yet — it may not work. Offered on the beta channel.")
                    }
                    if major.active, major.isInstalled {
                        Badge(text: "Active")
                    }
                }
                .font(.callout)
                caption
            }

            Spacer(minLength: 6)

            if busy != .idle {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text(busy.short).font(.caption).foregroundStyle(.secondary)
                }
            } else {
                actions
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private var caption: some View {
        if major.isInstalled && !major.extensions.isEmpty {
            Text(PostgresExtensions.titles(major.extensions))
                .font(.caption)
                .lineLimit(1)
                .foregroundStyle(.secondary)
                .help("Extensions installed for PostgreSQL \(major.major)")
        } else if !major.isInstalled && major.hasData {
            Text("\(Formats.bytes(major.dataSize)) kept")
                .font(.caption)
                .lineLimit(1)
                .foregroundStyle(.tertiary)
                .help("The databases of PostgreSQL \(major.major) are kept. Install it again to use them.")
        }
    }

    @ViewBuilder
    private var actions: some View {
        let store = model.settings
        if !major.isInstalled {
            Button("Install", action: install)
            .controlSize(.small)
            .disabled(locked)
            .help(major.latest.isEmpty ? "Downloads the newest PostgreSQL \(major.major)" : "Downloads PostgreSQL \(major.latest)")
        } else {
            HStack(spacing: 4) {
                // The active major's update is in the card above.
                if major.update, !major.latest.isEmpty, !major.active {
                    // "Update": the row shows the version it goes to ("17.10 → 17.11").
                    Button("Update") {
                        let id = major.major
                        Task { await store.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(locked)
                    .help("Installs \(major.latest) in place of \(major.installed). Your databases stay.")
                }
                if !major.active {
                    Button("Switch…", action: switchTo)
                        .controlSize(.small)
                        .disabled(locked)
                        .help("Stops PostgreSQL \(activeMajor) and runs \(major.major) with its own databases")
                }
                // An icon, like ⋯: with Update and Switch, a third labelled button doesn't fit.
                Button(action: extensions) {
                    Image(systemName: "puzzlepiece.extension")
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("Extensions")
                .help("Extensions of PostgreSQL \(major.major): PostGIS, pgRouting, pgvector, TimescaleDB and your own")
                Menu {
                    if major.update, !major.latest.isEmpty {
                        Button("Update to \(major.latest)") {
                            let id = major.major
                            Task { await store.performOnPostgres(id, .updating) { try await $0.updatePostgres(id) } }
                        }
                        .help("Your databases stay")
                    }
                    if !major.active {
                        Button("Switch to PostgreSQL \(major.major)", action: switchTo)
                    }
                    if major.update || !major.active {
                        Divider()
                    }
                    Button("Uninstall PostgreSQL \(major.major)…", role: .destructive, action: uninstall)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")
                .disabled(locked)
            }
        }
    }
}

/// The first PostgreSQL: the version and port, and Install, as for the catalog services.
private struct EmptyPostgres: View {
    @Environment(AppModel.self) private var model
    let versions: PostgresVersions
    let locked: Bool

    private let majorState = State<String?>(initialValue: nil)
    private let portState = State<String?>(initialValue: nil)

    var body: some View {
        let store = model.settings
        let installing = versions.majors.first { store.busy($0) != .idle }
        let kept = versions.majors.filter(\.hasData)

        if let installing {
            InstallInvitation(symbol: "cylinder.split.1x2.fill", color: .blue, title: "Installing PostgreSQL \(installing.major)",
                              message: "Downloading PostgreSQL \(installing.latest.isEmpty ? installing.major : installing.latest) and creating its first databases. This takes about a minute.") {
                ProgressView().progressViewStyle(.linear).frame(maxWidth: 260)
            } footer: {
                Text("It keeps going when you close this window.")
            }
        } else {
            InstallInvitation(symbol: "cylinder.split.1x2.fill", color: .blue, title: "Install PostgreSQL",
                              message: "The database for your Laravel apps. You can add other versions later; one runs at a time, and each keeps its own databases.") {
                ServiceInstallForm(
                    name: "PostgreSQL",
                    options: versions.majors.map { ServiceInstallForm.Option(id: $0.major, label: optionLabel($0), latest: $0.latest) },
                    version: Binding(get: { selectedMajor }, set: { majorState.wrappedValue = $0 }),
                    portText: Binding(get: { portText }, set: { portState.wrappedValue = $0 }),
                    note: nil,
                    busy: locked,
                    install: install
                )
            } footer: {
                if !kept.isEmpty {
                    Text("Databases of PostgreSQL \(kept.map(\.major).joined(separator: ", ")) are still here — install that version to use them again.")
                }
            }
        }
    }

    private var selectedMajor: String? { majorState.wrappedValue ?? versions.majors.first?.major }

    private var portText: String { portState.wrappedValue ?? String(versions.port) }

    /// "17 (17.6)", or "17 (17.6) — your databases" when its databases are kept.
    private func optionLabel(_ major: PostgresMajor) -> String {
        let label = major.latest.isEmpty ? major.major : "\(major.major) (\(major.latest))"
        return major.hasData ? label + " — your databases" : label
    }

    /// The port belongs to PostgreSQL, not to a version, so it is set before the install.
    private func install() {
        guard let major = selectedMajor, let port = ServiceInstallForm.port(portText) else { return }
        let store = model.settings
        let current = versions.port
        Task {
            if port != current {
                await store.performOnServices { try await $0.setServicePort("postgres", port: port) }
                guard store.error == nil else { return }
            }
            await store.performOnPostgres(major, .installing) { try await $0.installPostgres(major) }
        }
    }
}

/// Confirms uninstalling a major, optionally with its databases.
private struct UninstallPostgresSheet: View {
    let major: PostgresMajor
    let versions: PostgresVersions?
    let uninstall: (_ deleteData: Bool) -> Void
    @Environment(\.dismiss) private var dismiss
    private let deleteDataState = State<Bool>(initialValue: false)

    var body: some View {
        let deleteData = deleteDataState.wrappedValue

        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .top, spacing: 12) {
                SettingsIcon(symbol: "cylinder.split.1x2.fill", color: .blue, size: 40)
                VStack(alignment: .leading, spacing: 6) {
                    Text("Uninstall PostgreSQL \(major.major)?").font(.headline)
                    Text(message)
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            if major.hasData {
                VStack(alignment: .leading, spacing: 4) {
                    Toggle("Also delete its databases (\(Formats.bytes(major.dataSize)))", isOn: deleteDataState.projectedValue)
                        .toggleStyle(.checkbox)
                    Text(deleteData ? "Every database of PostgreSQL \(major.major) is deleted for good."
                                    : "Its databases stay, so installing \(major.major) again brings them back.")
                        .font(.caption)
                        .foregroundStyle(deleteData ? AnyShapeStyle(Color.red) : AnyShapeStyle(.secondary))
                        .padding(.leading, 20)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.leading, 52)
            }

            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(deleteData ? "Uninstall and Delete" : "Uninstall", role: .destructive) {
                    uninstall(deleteData)
                    dismiss()
                }
                // Deleting databases needs a deliberate click, not Return.
                .keyboardShortcut(deleteData ? nil : .defaultAction)
                .tint(deleteData ? .red : nil)
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private var message: String {
        guard major.active else {
            return "Removes the PostgreSQL \(major.installed) server."
        }
        let next = versions?.majors.first { $0.isInstalled && $0.major != major.major }
        if let next {
            return "PostgreSQL \(major.major) stops and Flock switches to PostgreSQL \(next.major) with its own databases."
        }
        return "PostgreSQL \(major.major) stops, and your sites can't use PostgreSQL until you install a version again."
    }
}
