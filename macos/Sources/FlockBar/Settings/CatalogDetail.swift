import AppKit
import FlockKit
import SwiftUI

/// The icon of a service in the Services pane.
struct ServiceStyle {
    let symbol: String
    let color: Color

    static func of(_ id: String) -> ServiceStyle {
        switch id {
        case "postgres": ServiceStyle(symbol: "cylinder.split.1x2.fill", color: .blue)
        case "mysql": ServiceStyle(symbol: "cylinder.fill", color: .orange)
        case "mariadb": ServiceStyle(symbol: "cylinder.fill", color: .brown)
        case "mongodb": ServiceStyle(symbol: "leaf.fill", color: .green)
        case "valkey": ServiceStyle(symbol: "square.stack.3d.up.fill", color: .indigo)
        case "redis": ServiceStyle(symbol: "square.stack.3d.up.fill", color: .red)
        case "meilisearch": ServiceStyle(symbol: "magnifyingglass", color: .pink)
        case "typesense": ServiceStyle(symbol: "text.magnifyingglass", color: .purple)
        case "rustfs": ServiceStyle(symbol: "externaldrive.fill", color: .teal)
        default: ServiceStyle(symbol: "server.rack", color: .gray)
        }
    }
}

/// What a detail shows for a service that doesn't run on this platform.
struct UnavailableNotice: View {
    let service: Service

    var body: some View {
        let style = ServiceStyle.of(service.id)
        InstallInvitation(symbol: style.symbol, color: style.color.opacity(0.5),
                          title: "Not available on macOS",
                          message: service.unavailable.isEmpty ? "Flock can't run \(service.name) on this Mac." : service.unavailable) {} footer: {}
    }
}

/// A catalog service (MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense, RustFS): pick a
/// version line and port and install it, follow the install; like PostgreSQL's majors, lines
/// install side by side and one runs: install more, switch, update or uninstall them.
struct CatalogDetail: View {
    @Environment(AppModel.self) private var model
    let service: Service

    private let logState = State<Bool>(initialValue: false)
    private let switchState = State<CatalogLine?>(initialValue: nil)
    private let uninstallState = State<CatalogLine?>(initialValue: nil)

    var body: some View {
        let store = model.settings
        let entry = store.catalog?[service.id]
        let style = ServiceStyle.of(service.id)

        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                DetailHeader(title: service.name, symbol: style.symbol, color: style.color,
                             status: status(entry), running: service.installed ? service.running : nil)

                if !service.available {
                    UnavailableNotice(service: service)
                } else if let entry {
                    if entry.lines.contains(where: \.installed) {
                        versions(entry)
                        Divider()
                        ServiceInfo(service: service)
                    } else {
                        CatalogInstallCard(entry: entry, busy: busy) {
                            logState.wrappedValue = true
                        }
                    }
                } else if store.catalog == nil {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 30)
                } else {
                    InstallInvitation(symbol: style.symbol, color: style.color, title: "\(service.name) isn't offered",
                                      message: "This Flock daemon doesn't know \(service.name). Update Flock, then try again.") {} footer: {}
                }
            }
            .padding(16)
        }
        .sheet(isPresented: logState.projectedValue) {
            JobLogSheet(title: "\(service.name) install log",
                        running: { store.catalog?[service.id]?.job?.isRunning ?? false },
                        load: { await store.catalogLog(service.id) })
        }
        .alert(item: switchState.projectedValue) { line in
            switchAlert(line, entry: entry)
        }
        .confirmationDialog(uninstallState.wrappedValue.map { "Uninstall \(service.name) \($0.label)?" } ?? "",
                            isPresented: Binding(get: { uninstallState.wrappedValue != nil }, set: { if !$0 { uninstallState.wrappedValue = nil } }),
                            presenting: uninstallState.wrappedValue) { line in
            if line.hasData {
                Button("Uninstall, Keep Data") { uninstall(line, deleteData: false) }
                Button("Uninstall and Delete Data", role: .destructive) { uninstall(line, deleteData: true) }
            } else {
                Button("Uninstall", role: .destructive) { uninstall(line, deleteData: false) }
            }
            Button("Cancel", role: .cancel) {}
        } message: { line in
            Text(uninstallMessage(line, entry: entry))
        }
    }

    /// A request for this service is waiting for the daemon.
    private var busy: Bool { model.settings.catalogPending.contains(service.id) }

    private func status(_ entry: CatalogService?) -> String {
        if let job = entry?.job, job.isRunning {
            return job.job.action == .update ? "Updating…" : "Installing…"
        }
        guard service.installed else { return service.available ? "Not installed" : "Not available" }
        var parts = [service.running ? "Running" : (service.enabled ? "Not running" : "Stopped")]
        if service.port > 0 {
            parts.append("Port \(service.port)")
        }
        return parts.joined(separator: " · ")
    }

    // MARK: Installed

    private func versions(_ entry: CatalogService) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DetailSectionTitle(title: "Versions") {
                if let catalog = model.settings.catalog {
                    CheckForUpdatesLine(checkedAt: catalog.checkedAt, error: catalog.checkError, disabled: busy) {
                        await model.settings.checkForUpdates()
                    }
                }
            }

            VStack(spacing: 0) {
                if let job = entry.job {
                    jobView(entry, job)
                        .padding(12)
                    Divider()
                }
                ForEach(Array(entry.lines.enumerated()), id: \.element.id) { index, line in
                    if index > 0 {
                        Divider().padding(.leading, 46)
                    }
                    CatalogLineRow(entry: entry, line: line, locked: busy || entry.job?.isRunning == true) {
                        install(line: line.id)
                    } switchTo: {
                        switchState.wrappedValue = line
                    } uninstall: {
                        uninstallState.wrappedValue = line
                    } showLog: {
                        logState.wrappedValue = true
                    }
                }
            }
            .card(padding: 0)

            Text(entry.lines.count > 1
                 ? "Installed versions stay side by side. One runs at a time; each keeps its own data, and updates within a version keep it."
                 : "Updates keep your data.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private func jobView(_ entry: CatalogService, _ job: CatalogJob) -> some View {
        if job.isRunning {
            ComponentJobProgress(title: job.title(entry), job: job.job) {
                logState.wrappedValue = true
            } cancel: {
                let id = entry.id
                Task { await model.settings.performOnCatalog(id) { try await $0.cancelCatalogJob(id) } }
            }
        } else if job.isFailed {
            ComponentJobFailure(name: entry.name, job: job.job) {
                logState.wrappedValue = true
            } retry: {
                install(line: job.line.isEmpty ? nil : job.line)
            } dismiss: {
                let id = entry.id
                Task { await model.settings.performOnCatalog(id) { try await $0.dismissCatalogJob(id) } }
            }
        }
    }

    private func install(line: String?) {
        let id = service.id
        Task { await model.settings.performOnCatalog(id) { try await $0.installCatalogService(id, line: line) } }
    }

    private func uninstall(_ line: CatalogLine, deleteData: Bool) {
        let id = service.id
        let target = line.id
        Task { await model.settings.performOnCatalog(id) { try await $0.uninstallCatalogService(id, line: target, deleteData: deleteData) } }
    }

    private func uninstallMessage(_ line: CatalogLine, entry: CatalogService?) -> String {
        let name = service.name
        var message = ""
        if line.active {
            if let next = entry?.lines.first(where: { $0.installed && $0.id != line.id }) {
                message = "\(name) \(line.label) stops and \(name) \(next.label) becomes the active version. "
            } else {
                message = "\(name) \(line.label) stops; your sites can't use \(name) until you install a version again. "
            }
        }
        return message + (line.hasData
            ? "Keep its data to pick up where you left off when you install \(line.label) again."
            : "It holds no data.")
    }

    private func switchAlert(_ target: CatalogLine, entry: CatalogService?) -> Alert {
        let name = service.name
        let current = entry?.line ?? ""
        let own = target.hasData
            ? "\(name) \(target.label) uses its own data."
            : "\(name) \(target.label) starts with empty data."
        let message = current.isEmpty ? own : "\(name) \(current) stops; its data stays but isn't used until you switch back. \(own)"
        let id = service.id
        return Alert(
            title: Text("Switch to \(name) \(target.label)?"),
            message: Text(message),
            primaryButton: .default(Text("Switch")) {
                Task { await model.settings.performOnCatalog(id) { try await $0.activateCatalogService(id, line: target.id) } }
            },
            secondaryButton: .cancel()
        )
    }
}

extension CatalogJob {
    /// "Downloading MySQL 8.4.7…"
    func title(_ entry: CatalogService) -> String {
        let target = version.isEmpty ? entry.name : "\(entry.name) \(version)"
        return switch job.phase {
        case .downloading: "Downloading \(target)…"
        case .installing: "Installing \(target)…"
        case .unknown: job.action == .update ? "Updating \(entry.name)…" : "Installing \(entry.name)…"
        }
    }
}

extension CatalogService {
    /// What installing takes, for a footnote.
    var methodText: String {
        estimate.isEmpty ? "Downloads a ready-made build." : "Downloads a ready-made build (\(estimate))."
    }
}

/// One line of a catalog service, like a PostgreSQL major: install it next to the others, or
/// update, switch to and uninstall an installed one.
private struct CatalogLineRow: View {
    let entry: CatalogService
    let line: CatalogLine
    let locked: Bool
    /// Installs the line, or updates (reinstalls) it when it is installed.
    let install: () -> Void
    let switchTo: () -> Void
    let uninstall: () -> Void
    let showLog: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: line.active ? "checkmark.circle.fill" : line.installed ? "checkmark.circle" : "circle")
                .font(.system(size: 15))
                .foregroundStyle(line.active ? Color.green : line.installed ? Color.secondary : Color.secondary.opacity(0.5))
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text("\(entry.name) \(line.label)").font(.body.weight(line.installed ? .medium : .regular))
                    if line.active {
                        Badge(text: "Active")
                    }
                }
                Text(detail).font(.caption).foregroundStyle(line.update ? AnyShapeStyle(Color.accentColor) : AnyShapeStyle(.secondary)).lineLimit(1)
                if !line.yanked.isEmpty {
                    Label(line.update ? "Withdrawn: \(line.yanked) — update to replace it" : "Withdrawn: \(line.yanked)", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.orange)
                        .lineLimit(2)
                        .help("This build was withdrawn. Flock replaces it as soon as a fixed build is published.")
                }
            }
            Spacer(minLength: 8)
            if !line.installed {
                Button("Install", action: install)
                    .controlSize(.small)
                    .disabled(locked)
                    .help(installHelp)
            } else {
                if line.update, !line.latest.isEmpty {
                    Button("Update to \(line.latest)", action: install)
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .disabled(locked)
                        .help("Downloads \(line.latest) and replaces \(line.version); your data stays.")
                }
                if !line.active {
                    Button("Switch…", action: switchTo)
                        .controlSize(.small)
                        .disabled(locked)
                        .help(entry.isInstalled ? "Stops \(entry.name) \(entry.line) and runs \(line.label) with its own data" : "Runs \(entry.name) \(line.label)")
                }
                Menu {
                    if !line.update {
                        Button("Reinstall \(line.version)", action: install)
                    }
                    Button("Show Last Log", action: showLog)
                    Divider()
                    Button("Uninstall \(entry.name) \(line.label)…", role: .destructive, action: uninstall)
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
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private var installHelp: String {
        var help = line.latest.isEmpty ? "Downloads the newest \(entry.name) \(line.label)." : "Downloads \(entry.name) \(line.latest)."
        if entry.isInstalled {
            help += " \(entry.name) \(entry.line) keeps running until you switch."
        }
        if line.hasData {
            help += " Its data is still here and comes back."
        }
        return help
    }

    private var detail: String {
        var parts: [String] = []
        if line.installed {
            parts.append(line.version)
            parts.append(line.update ? (line.latest == line.version ? "new build available" : "\(line.latest) available") : "up to date")
        } else {
            parts.append(line.latest.isEmpty ? "Not installed" : "Not installed · \(line.latest) available")
        }
        if line.hasData {
            parts.append(line.installed ? "has data" : "data kept")
        }
        return parts.joined(separator: " · ")
    }
}

/// Installing a catalog service: what it is, the version line and port, and Install — or the
/// running or failed install.
private struct CatalogInstallCard: View {
    @Environment(AppModel.self) private var model
    let entry: CatalogService
    let busy: Bool
    let showLog: () -> Void

    private let lineState = State<String?>(initialValue: nil)
    private let portState = State<String?>(initialValue: nil)

    var body: some View {
        let style = ServiceStyle.of(entry.id)
        let job = entry.job

        InstallInvitation(symbol: style.symbol, color: style.color,
                          title: job?.isRunning == true ? "Installing \(entry.name)" : "Install \(entry.name)",
                          message: entry.description.isEmpty ? "Runs on 127.0.0.1 for your sites." : entry.description) {
            if let job, job.isRunning {
                ComponentJobProgress(title: job.title(entry), job: job.job, showLog: showLog) {
                    perform { try await $0.cancelCatalogJob(entry.id) }
                }
                .multilineTextAlignment(.leading)
                .padding(.top, 6)
            } else if let job, job.isFailed {
                ComponentJobFailure(name: entry.name, job: job.job, showLog: showLog) {
                    let line = job.line.isEmpty ? selectedLine : job.line
                    let port = self.port
                    perform { try await $0.installCatalogService(entry.id, line: line, port: port) }
                } dismiss: {
                    perform { try await $0.dismissCatalogJob(entry.id) }
                }
                .multilineTextAlignment(.leading)
            } else {
                form
            }
        } footer: {
            if job?.isRunning != true {
                Text(entry.methodText)
            }
        }
    }

    private var selectedLine: String? { lineState.wrappedValue ?? entry.preferredLine?.id }

    private var portText: String { portState.wrappedValue ?? String(entry.port) }

    /// The port typed in, when it is valid.
    private var port: Int? { ServiceInstallForm.port(portText) }

    private var form: some View {
        ServiceInstallForm(
            name: entry.name,
            options: entry.lines.map { ServiceInstallForm.Option(id: $0.id, label: $0.latest.isEmpty ? $0.label : "\($0.label) (\($0.latest))", latest: $0.latest) },
            version: Binding(get: { selectedLine }, set: { lineState.wrappedValue = $0 }),
            portText: Binding(get: { portText }, set: { portState.wrappedValue = $0 }),
            note: entry.port != entry.defaultPort && portState.wrappedValue == nil
                ? "Port \(String(entry.defaultPort)) is taken, so Flock suggests \(String(entry.port))." : nil,
            busy: busy
        ) {
            let line = selectedLine
            let port = self.port
            perform { try await $0.installCatalogService(entry.id, line: line, port: port) }
        }
    }

    private func perform(_ change: @escaping @Sendable (FlockClient) async throws -> ServiceCatalog) {
        let id = entry.id
        Task { await model.settings.performOnCatalog(id, change) }
    }
}

/// Installing a service that isn't installed yet, the same for PostgreSQL and the catalog
/// services: the version (when there is a choice) and the port, then Install.
struct ServiceInstallForm: View {
    struct Option: Identifiable {
        let id: String
        let label: String
        /// The release Install downloads; empty when unknown.
        let latest: String
    }

    let name: String
    let options: [Option]
    @Binding var version: String?
    @Binding var portText: String
    /// Why the suggested port isn't the usual one.
    let note: String?
    let busy: Bool
    let install: () -> Void

    /// A port typed in, when it is valid.
    static func port(_ text: String) -> Int? {
        Int(text).flatMap { (1024...65535).contains($0) ? $0 : nil }
    }

    var body: some View {
        let port = Self.port(portText)

        VStack(spacing: 12) {
            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
                if options.count > 1 {
                    GridRow {
                        Text("Version").foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                        Picker("Version", selection: Binding(get: { version ?? "" }, set: { version = $0 })) {
                            ForEach(options) { option in
                                Text(option.label).tag(option.id)
                            }
                        }
                        .labelsHidden()
                        .fixedSize()
                    }
                }
                GridRow {
                    Text("Port").foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                    TextField("Port", text: $portText)
                        .labelsHidden()
                        .monospacedDigit()
                        .frame(width: 80)
                }
            }
            if port == nil {
                Text("Pick a port between 1024 and 65535.")
                    .font(.caption).foregroundStyle(.orange)
            } else if let note {
                Text(note).font(.caption).foregroundStyle(.secondary)
            }
            HStack(spacing: 8) {
                Button(action: install) {
                    Text(installLabel).padding(.horizontal, 6)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(busy || port == nil)
                if busy {
                    ProgressView().controlSize(.small)
                }
            }
        }
    }

    private var installLabel: String {
        guard let latest = options.first(where: { $0.id == version })?.latest, !latest.isEmpty else { return "Install \(name)" }
        return "Install \(name) \(latest)"
    }
}
