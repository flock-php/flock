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
/// version line and port and install it, follow the install, update, switch lines or uninstall.
struct CatalogDetail: View {
    @Environment(AppModel.self) private var model
    let service: Service

    private let logState = State<Bool>(initialValue: false)
    private let switchState = State<CatalogLine?>(initialValue: nil)
    private let uninstallState = State<Bool>(initialValue: false)

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
                    if entry.isInstalled {
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
            JobLogSheet(title: "\(service.name) \(entry?.method == .build ? "build" : "install") log",
                        running: { store.catalog?[service.id]?.job?.isRunning ?? false },
                        load: { await store.catalogLog(service.id) })
        }
        .alert(item: switchState.projectedValue) { line in
            switchAlert(line, entry: entry)
        }
        .confirmationDialog("Uninstall \(service.name)?", isPresented: uninstallState.projectedValue) {
            Button("Uninstall, Keep Data") { uninstall(deleteData: false) }
            Button("Uninstall and Delete Data", role: .destructive) { uninstall(deleteData: true) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(service.name) stops and your sites can't use it until you install it again. Keep its data to pick up where you left off when you reinstall.")
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
                        uninstallState.wrappedValue = true
                    } showLog: {
                        logState.wrappedValue = true
                    }
                }
            }
            .card(padding: 0)

            Text(entry.lines.count > 1
                 ? "One version runs at a time. Each version keeps its own data; updates within a version keep it."
                 : "Updates keep your data.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private func jobView(_ entry: CatalogService, _ job: CatalogJob) -> some View {
        if job.isRunning {
            ComponentJobProgress(title: job.title(entry), buildEstimate: entry.method == .build ? entry.estimate : "", job: job.job) {
                logState.wrappedValue = true
            } cancel: {
                let id = entry.id
                Task { await model.settings.performOnCatalog(id) { try await $0.cancelCatalogJob(id) } }
            }
        } else if job.isFailed {
            ComponentJobFailure(name: entry.name, isBuild: entry.method == .build, job: job.job) {
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

    private func uninstall(deleteData: Bool) {
        let id = service.id
        Task { await model.settings.performOnCatalog(id) { try await $0.uninstallCatalogService(id, deleteData: deleteData) } }
    }

    private func switchAlert(_ target: CatalogLine, entry: CatalogService?) -> Alert {
        let name = service.name
        let current = entry?.lines.first { $0.installed }?.label ?? entry?.line ?? ""
        let own = target.hasData
            ? "\(name) \(target.label) uses its own data from before."
            : "\(name) \(target.label) starts with empty data."
        let message = "\(name) \(current) is replaced; its data stays but isn't used until you switch back. \(own)"
        return Alert(
            title: Text("Switch to \(name) \(target.label)?"),
            message: Text(message),
            primaryButton: .default(Text("Switch")) { install(line: target.id) },
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
        case .building: "Building \(target)…"
        case .installing: "Installing \(target)…"
        case .unknown: job.action == .update ? "Updating \(entry.name)…" : "Installing \(entry.name)…"
        }
    }
}

extension CatalogService {
    /// How installing works and takes, for a footnote.
    var methodText: String {
        switch method {
        case .build:
            "Builds from source\(estimate.isEmpty ? "" : " — \(estimate)"); needs the Xcode Command Line Tools."
        case .download:
            estimate.isEmpty ? "Downloads a ready-made build." : "Downloads a ready-made build (\(estimate))."
        case .unknown:
            estimate
        }
    }
}

/// One line of an installed catalog service: the running one with update and uninstall, the
/// others to switch to.
private struct CatalogLineRow: View {
    let entry: CatalogService
    let line: CatalogLine
    let locked: Bool
    let update: () -> Void
    let switchTo: () -> Void
    let uninstall: () -> Void
    let showLog: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: line.installed ? "checkmark.circle.fill" : "circle")
                .font(.system(size: 15))
                .foregroundStyle(line.installed ? Color.green : Color.secondary.opacity(0.5))
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 1) {
                Text("\(entry.name) \(line.label)").font(.body.weight(line.installed ? .medium : .regular))
                Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                if line.installed, !entry.yanked.isEmpty {
                    Label(entry.update ? "Withdrawn: \(entry.yanked) — update to replace it" : "Withdrawn: \(entry.yanked)", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption).foregroundStyle(.orange)
                        .lineLimit(2)
                        .help("This build was withdrawn. Flock replaces it as soon as a fixed build is published.")
                }
            }
            Spacer(minLength: 8)
            if line.installed {
                if entry.update, !entry.latest.isEmpty {
                    Button("Update to \(entry.latest)", action: update)
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .disabled(locked)
                        .help("\(entry.method == .build ? "Builds" : "Downloads") \(entry.latest) and replaces \(entry.installed); your data stays.")
                }
                Menu {
                    Button(entry.update ? "Update to \(entry.latest)" : "Reinstall \(entry.installed)", action: update)
                    Button("Show Last Log", action: showLog)
                    Divider()
                    Button("Uninstall…", role: .destructive, action: uninstall)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")
                .disabled(locked)
            } else {
                Button("Switch…", action: switchTo)
                    .controlSize(.small)
                    .disabled(locked)
                    .help(line.latest.isEmpty ? "Installs the newest \(line.label) instead" : "Installs \(line.latest) instead of \(entry.installed)")
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private var detail: String {
        if line.installed {
            return entry.update ? "\(entry.installed) · \(entry.latest) is available" : entry.installed
        }
        var parts = [line.latest.isEmpty ? "Not installed" : "\(line.latest) available"]
        if line.hasData {
            parts.append("has data")
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
                ComponentJobProgress(title: job.title(entry), buildEstimate: entry.method == .build ? entry.estimate : "", job: job.job, showLog: showLog) {
                    perform { try await $0.cancelCatalogJob(entry.id) }
                }
                .multilineTextAlignment(.leading)
                .padding(.top, 6)
            } else if let job, job.isFailed {
                ComponentJobFailure(name: entry.name, isBuild: entry.method == .build, job: job.job, showLog: showLog) {
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
    private var port: Int? {
        Int(portText).flatMap { (1024...65535).contains($0) ? $0 : nil }
    }

    private var form: some View {
        VStack(spacing: 12) {
            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
                if entry.lines.count > 1 {
                    GridRow {
                        Text("Version").foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                        Picker("Version", selection: Binding(get: { selectedLine ?? "" }, set: { lineState.wrappedValue = $0 })) {
                            ForEach(entry.lines) { line in
                                Text(line.latest.isEmpty ? line.label : "\(line.label) (\(line.latest))").tag(line.id)
                            }
                        }
                        .labelsHidden()
                        .fixedSize()
                    }
                }
                GridRow {
                    Text("Port").foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                    TextField("Port", text: Binding(get: { portText }, set: { portState.wrappedValue = $0 }))
                        .labelsHidden()
                        .monospacedDigit()
                        .frame(width: 80)
                }
            }
            if entry.port != entry.defaultPort, portState.wrappedValue == nil {
                Text("Port \(String(entry.defaultPort)) is taken, so Flock suggests \(String(entry.port)).")
                    .font(.caption).foregroundStyle(.secondary)
            } else if port == nil {
                Text("Pick a port between 1024 and 65535.")
                    .font(.caption).foregroundStyle(.orange)
            }
            HStack(spacing: 8) {
                Button {
                    let line = selectedLine
                    let port = self.port
                    perform { try await $0.installCatalogService(entry.id, line: line, port: port) }
                } label: {
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
        let line = entry.lines.first { $0.id == selectedLine }
        guard let latest = line?.latest, !latest.isEmpty else { return "Install \(entry.name)" }
        return "Install \(entry.name) \(latest)"
    }

    private func perform(_ change: @escaping @Sendable (FlockClient) async throws -> ServiceCatalog) {
        let id = entry.id
        Task { await model.settings.performOnCatalog(id, change) }
    }
}
