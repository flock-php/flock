import AppKit
import FlockKit
import SwiftUI

/// The menu bar panel: status, services, sites, toggles and window shortcuts.
struct PanelView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header

            if let version = model.updater.availableVersion {
                updateRow(version)
            }

            if let message = model.actionError {
                errorBanner(message)
            }

            // Before the setup is done, a daemon that answers is the setup's (it may still fail).
            if let status = model.status, !model.needsSetup {
                if !status.warnings.isEmpty {
                    warnings(status.warnings)
                }
                Divider()
                services(status)
                Divider()
                sites(status.sites)
                Divider()
                toggles(status)
                Divider()
                shortcuts(status)
            } else {
                notRunning
            }

            Divider()
            footer
        }
        .padding(14)
        .frame(width: 340)
        .onAppear { model.refreshSetup() }
    }

    /// Opens a window and closes the panel, which otherwise stays on top of it.
    private func open(_ id: String) {
        closePanel()
        openWindow.activating(id)
    }

    /// MenuBarExtra has no API to close its panel; while it is used, it is the key window.
    private func closePanel() {
        if let panel = NSApp.keyWindow as? NSPanel {
            panel.close()
        }
    }

    // MARK: Sections

    private var header: some View {
        HStack(spacing: 8) {
            LogoMark(mode: model.logoMark, size: 24)
                // The stopped image is already dimmed.
                .foregroundStyle(model.isRunning && model.isDegraded ? Color.flockEmber : Color.flockMark)
            VStack(alignment: .leading, spacing: 1) {
                Text("Flock").font(.headline)
                Text(model.headline).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if model.isRestarting || model.isUpdating {
                ProgressView().controlSize(.small)
            }
        }
    }

    /// A newer version of Flock; Update opens Sparkle's window to install it.
    private func updateRow(_ version: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.down.circle.fill").foregroundStyle(Color.accentColor)
            Text("Flock \(version) is available").font(.callout)
            Spacer(minLength: 0)
            Button("Update") {
                closePanel()
                model.updater.checkForUpdates()
            }
            .controlSize(.small)
        }
        .padding(8)
        .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
    }

    /// The last failed action (restart, a toggle) until it is dismissed or an action succeeds.
    private func errorBanner(_ message: String) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(.red)
            Text(message)
                .font(.caption)
                .textSelection(.enabled)
                .lineLimit(8)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button {
                model.actionError = nil
            } label: {
                Image(systemName: "xmark").font(.caption2.weight(.bold))
            }
            .buttonStyle(.borderless)
            .help("Dismiss")
        }
        .padding(8)
        .background(.red.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
    }

    private func warnings(_ warnings: [String]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(warnings, id: \.self) { warning in
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func services(_ status: Status) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            SectionTitle("Services")
            ServiceRow(name: "Web", detail: "http :\(status.httpPort) · https :\(status.httpsPort)", up: true)
            if status.dns.wildcard {
                ServiceRow(name: "DNS", detail: "*.\(status.dns.tld)", up: true)
            } else if !status.dns.tld.isEmpty {
                ServiceRow(name: "DNS", detail: status.dns.wildcardProblem.isEmpty ? "*.\(status.dns.tld) doesn't resolve" : status.dns.wildcardProblem, up: false)
                    .help(status.dns.wildcardProblem)
            }
            ServiceRow(name: "Mail", detail: status.mail.isEmpty ? "stopped" : "smtp \(status.mail)", up: !status.mail.isEmpty)
            if let postgres = status.postgres {
                ServiceRow(name: "PostgreSQL", detail: postgres, up: status.processes.first { $0.name == "postgres" }?.up ?? false)
            }
            if let valkey = status.valkey {
                ServiceRow(name: "Valkey", detail: valkey, up: status.processes.first { $0.name == "valkey" }?.up ?? false)
            }
            ForEach(status.extras) { extra in
                ServiceRow(name: extra.name, detail: extra.enabled ? "\(extra.version) on 127.0.0.1:\(extra.port)" : "stopped", up: extra.up)
            }
            notInstalled(status)
            ForEach(status.processes.filter { $0.name.hasPrefix("php") }) { process in
                ServiceRow(name: process.name, detail: processDetail(process), up: process.up)
            }
            ForEach(status.processes.filter { !$0.up && !$0.name.hasPrefix("php") }) { process in
                Text("\(process.name): \(process.lastError ?? "down")")
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }
        }
    }

    /// Databases and caches are installed on request, from Settings → Services.
    @ViewBuilder
    private func notInstalled(_ status: Status) -> some View {
        if status.postgres == nil, status.valkey == nil, status.extras.isEmpty {
            HStack(spacing: 6) {
                Circle().fill(Color.secondary.opacity(0.4)).frame(width: 7, height: 7)
                Text("No database or cache installed")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Install…") {
                    model.showSettings(.services, service: "postgres")
                    open(WindowID.settings)
                }
                .buttonStyle(.link)
                .font(.caption)
                .help("Opens Settings → Services")
            }
        }
    }

    private func processDetail(_ process: Status.Process) -> String {
        if !process.up {
            return process.lastError ?? "down"
        }
        return process.restarts > 0 ? "up (restarted \(process.restarts)×)" : "up"
    }

    private func sites(_ sites: [Status.Site]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            SectionTitle("Sites")
            if sites.isEmpty {
                Text("No sites yet — run `flock link` or `flock park`.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                SiteList(sites: sites) { model.open($0) }
            }
        }
    }

    private func toggles(_ status: Status) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Toggle(isOn: Binding(get: { status.xdebug }, set: { on in Task { await model.setXdebug(on) } })) {
                Label("Xdebug", systemImage: "ladybug")
            }
            Toggle(isOn: Binding(get: { status.dumps }, set: { on in Task { await model.setDumps(on) } })) {
                Label("Capture dumps & queries", systemImage: "tray.and.arrow.down")
            }
        }
        .toggleStyle(.switch)
        .controlSize(.small)
    }

    private func shortcuts(_ status: Status) -> some View {
        HStack(spacing: 8) {
            ShortcutButton(title: "Mail", symbol: "envelope", count: status.mailCount) {
                open(WindowID.mail)
            }
            ShortcutButton(title: "Dumps", symbol: "ladybug.fill", count: status.dumpCount) {
                open(WindowID.dumps)
            }
            ShortcutButton(title: "Logs", symbol: "doc.text.magnifyingglass", count: nil) {
                open(WindowID.logs)
            }
        }
    }

    @ViewBuilder
    private var notRunning: some View {
        if model.needsSetup {
            VStack(alignment: .leading, spacing: 8) {
                Text("Flock isn't set up on this Mac yet. Setup installs the daemon, puts php, composer and node on your PATH, and makes *.test sites work with trusted HTTPS.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Set Up Flock…") {
                    open(WindowID.setup)
                }
                .buttonStyle(.borderedProminent)
            }
        } else {
            VStack(alignment: .leading, spacing: 8) {
                Text(model.isUpdating ? "Updating Flock…" : model.error ?? "Flock is not running")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Start Flock") {
                    Task { await model.restart() }
                }
                .disabled(model.isRestarting || model.isUpdating)
            }
        }
    }

    private var footer: some View {
        HStack {
            // Restart and Settings need the daemon; before the setup, Set Up Flock is the way on.
            if !model.needsSetup {
                Button("Restart") {
                    Task { await model.restart() }
                }
                .disabled(model.isRestarting || model.isUpdating || !model.isRunning)
                Button("Settings…") {
                    open(WindowID.settings)
                }
                .keyboardShortcut(",")
            }
            Spacer()
            Button("Quit Flock Bar") {
                NSApp.terminate(nil)
            }
        }
        .buttonStyle(.borderless)
        .font(.callout)
    }
}

private struct SectionTitle: View {
    let title: String

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Text(title.uppercased())
            .font(.caption2.weight(.semibold))
            .foregroundStyle(.secondary)
    }
}

private struct ServiceRow: View {
    let name: String
    let detail: String
    let up: Bool

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(up ? Color.green : Color.red)
                .frame(width: 7, height: 7)
            Text(name).font(.callout)
            Spacer()
            Text(detail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }
}

/// The panel sizes itself to its content's ideal size, and a ScrollView has none of its own, so
/// the list is as tall as its rows and scrolls past 240pt.
private struct SiteList: View {
    let sites: [Status.Site]
    let open: (Status.Site) -> Void

    private let contentHeightState = State<CGFloat>(initialValue: 0)
    private var contentHeight: CGFloat {
        get { contentHeightState.wrappedValue }
        nonmutating set { contentHeightState.wrappedValue = newValue }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(sites) { site in
                    SiteRow(site: site) { open(site) }
                }
            }
        }
        .onScrollGeometryChange(for: CGFloat.self, of: \.contentSize.height) { _, height in
            contentHeight = height
        }
        .frame(height: min(contentHeight, 240))
    }
}

private struct SiteRow: View {
    let site: Status.Site
    let open: () -> Void

    private let isHoveredState = State<Bool>(initialValue: false)
    private var isHovered: Bool {
        get { isHoveredState.wrappedValue }
        nonmutating set { isHoveredState.wrappedValue = newValue }
    }

    var body: some View {
        Button(action: open) {
            HStack(spacing: 6) {
                Image(systemName: site.secured ? "lock.fill" : "globe")
                    .font(.caption)
                    .foregroundStyle(site.secured ? Color.green : Color.secondary)
                    .frame(width: 14)
                VStack(alignment: .leading, spacing: 1) {
                    Text(site.domain).font(.callout)
                    if let problem = site.problem {
                        Text(problem).font(.caption2).foregroundStyle(.red)
                    } else if let notice = site.notice {
                        Label(notice, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption2).foregroundStyle(.orange)
                    }
                }
                Spacer()
                if site.debugging {
                    Label("Debugging", systemImage: "ladybug.fill")
                        .labelStyle(.iconOnly)
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
                if site.shared {
                    Badge(text: "Shared", color: .green)
                        .help(site.share?.url ?? "")
                }
                if site.server == .frankenphp {
                    Badge(text: site.worker == .octane ? "Octane" : "FrankenPHP", color: .secondary)
                }
                Text("PHP \(site.php)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 3)
            .padding(.horizontal, 6)
            .background(isHovered ? Color.primary.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(site.url == nil)
        .onHover { isHovered = $0 }
        .help(help)
    }

    /// The row's tooltip: where a site without a document root is fixed, else its address.
    private var help: String {
        if site.rootState == .notFound {
            return "No public/, www/, web/ or webroot/ with an index.php in \(site.path). Choose the document root in Settings → Sites."
        }
        return site.url ?? site.problem ?? site.path
    }
}

private struct ShortcutButton: View {
    let title: String
    let symbol: String
    let count: Int?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 3) {
                Image(systemName: symbol).font(.title3)
                HStack(spacing: 3) {
                    Text(title)
                    if let count, count > 0 {
                        Text("\(count)")
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 5)
                            .background(Color.accentColor.opacity(0.25), in: Capsule())
                    }
                }
                .font(.caption)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
        }
        .buttonStyle(.bordered)
    }
}
