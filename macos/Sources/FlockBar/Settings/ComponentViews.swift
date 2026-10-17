import AppKit
import FlockKit
import SwiftUI

// Building blocks of the service details: cards, the empty "install" state, a component (Valkey)
// with its background job, and its log.

extension View {
    /// A grouped box like the ones in System Settings.
    func card(padding: CGFloat = 12) -> some View {
        self
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color(nsColor: .separatorColor).opacity(0.6)))
    }
}

/// The name of a service with its icon and a status line, at the top of its detail.
struct DetailHeader: View {
    let title: String
    let symbol: String
    let color: Color
    let status: String
    var running: Bool?

    var body: some View {
        HStack(spacing: 10) {
            SettingsIcon(symbol: symbol, color: color, size: 34)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.title3.weight(.semibold))
                HStack(spacing: 5) {
                    if let running {
                        StatusDot(on: running)
                    }
                    Text(status).font(.callout).foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
    }
}

/// A section title inside a detail, with optional trailing content.
struct DetailSectionTitle<Trailing: View>: View {
    let title: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.subheadline.weight(.semibold))
            Spacer()
            trailing.font(.caption).foregroundStyle(.secondary)
        }
    }
}

extension DetailSectionTitle where Trailing == EmptyView {
    init(_ title: String) {
        self.title = title
        trailing = EmptyView()
    }
}

/// "Last checked 3 hours ago · Check for Updates", with the check's error behind a warning icon.
struct CheckForUpdatesLine: View {
    let checkedAt: Date?
    let error: String
    let disabled: Bool
    let check: () async -> Void
    private let checkingState = State<Bool>(initialValue: false)

    var body: some View {
        HStack(spacing: 5) {
            if !error.isEmpty {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    .help("The last check failed: \(error)")
            }
            if let checkedAt {
                Text("Last checked \(checkedAt, format: .relative(presentation: .named))")
            } else {
                Text("Not checked yet")
            }
            Text("·")
            if checkingState.wrappedValue {
                ProgressView().controlSize(.mini)
                Text("Checking…")
            } else {
                Button("Check for Updates") {
                    checkingState.wrappedValue = true
                    Task {
                        await check()
                        checkingState.wrappedValue = false
                    }
                }
                .buttonStyle(.link)
                .disabled(disabled)
            }
        }
        .lineLimit(1)
    }
}

enum Formats {
    static func bytes(_ count: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: count, countStyle: .file)
    }

    /// "Nov 2030".
    static func monthYear(_ date: Date) -> String {
        date.formatted(.dateTime.month(.abbreviated).year().locale(Locale.current))
    }

    /// "4:05" or "1:02:03".
    static func elapsed(since start: Date, now: Date) -> String {
        let seconds = max(0, Int(now.timeIntervalSince(start)))
        let (h, m, s) = (seconds / 3600, seconds / 60 % 60, seconds % 60)
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }
}

extension ServiceComponent {
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

    /// The tooltip of Install/Update.
    var installHelp: String {
        let target = latest.isEmpty ? "" : " \(latest)"
        let how = method == .build ? "Builds\(target) from source" : "Downloads\(target)"
        let time = estimate.isEmpty ? "" : " (\(estimate))"
        return isInstalled ? "\(how)\(time), then replaces \(installed)." : "\(how)\(time), then installs it."
    }
}

extension ComponentJob {
    /// "Building Redis 9.1.3…"
    func title(_ component: ServiceComponent) -> String {
        let target = component.latest.isEmpty ? component.name : "\(component.name) \(component.latest)"
        return switch phase {
        case .downloading: "Downloading \(target)…"
        case .building: "Building \(target)…"
        case .installing: "Installing \(target)…"
        case .unknown: action == .update ? "Updating \(component.name)…" : "Installing \(component.name)…"
        }
    }
}

/// An install that runs in the background: phase, progress, elapsed time, the log and Cancel.
struct ComponentJobProgress: View {
    let title: String
    /// What a build usually takes ("about 1 minute"); empty for downloads.
    let buildEstimate: String
    let job: ComponentJob
    let showLog: () -> Void
    let cancel: () -> Void

    init(title: String, buildEstimate: String, job: ComponentJob, showLog: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.title = title
        self.buildEstimate = buildEstimate
        self.job = job
        self.showLog = showLog
        self.cancel = cancel
    }

    init(component: ServiceComponent, job: ComponentJob, showLog: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.init(title: job.title(component), buildEstimate: component.method == .build ? component.estimate : "",
                  job: job, showLog: showLog, cancel: cancel)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.callout.weight(.medium))
                Spacer()
                if let started = job.startedAt {
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        Text(Formats.elapsed(since: started, now: context.date))
                            .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
            }
            if let progress = job.progress {
                ProgressView(value: progress)
            } else {
                ProgressView().progressViewStyle(.linear)
            }
            HStack(spacing: 10) {
                Text(!buildEstimate.isEmpty
                     ? "Usually \(buildEstimate). It keeps going when you close this window."
                     : "It keeps going when you close this window.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 4)
                Button("Show Log", action: showLog).buttonStyle(.link).font(.caption)
                Button("Cancel", action: cancel).controlSize(.small)
            }
        }
    }
}

/// A failed install: what went wrong, the log, Try Again and Dismiss.
struct ComponentJobFailure: View {
    /// "Redis", "PostgreSQL tools".
    let name: String
    let isBuild: Bool
    let job: ComponentJob
    let showLog: () -> Void
    let retry: () -> Void
    let dismiss: () -> Void

    init(name: String, isBuild: Bool, job: ComponentJob, showLog: @escaping () -> Void, retry: @escaping () -> Void, dismiss: @escaping () -> Void) {
        self.name = name
        self.isBuild = isBuild
        self.job = job
        self.showLog = showLog
        self.retry = retry
        self.dismiss = dismiss
    }

    init(component: ServiceComponent, job: ComponentJob, showLog: @escaping () -> Void, retry: @escaping () -> Void, dismiss: @escaping () -> Void) {
        self.init(name: component.name, isBuild: component.method == .build, job: job, showLog: showLog, retry: retry, dismiss: dismiss)
    }

    /// The build failed because Apple's Command Line Tools are missing.
    private var needsDeveloperTools: Bool {
        guard isBuild else { return false }
        let error = job.error.lowercased()
        return ["xcrun", "xcode-select", "commandlinetools", "command line tools"].contains { error.contains($0) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(.red)
                VStack(alignment: .leading, spacing: 3) {
                    Text(job.action == .update ? "Couldn't update \(name)" : "Couldn't install \(name)")
                        .font(.callout.weight(.semibold))
                    Text(job.error.isEmpty ? "The \(isBuild ? "build" : "install") stopped with an error. The log has the details." : job.error)
                        .font(.caption).foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .lineLimit(4)
                        .fixedSize(horizontal: false, vertical: true)
                        .help(job.error)
                }
            }
            HStack(spacing: 8) {
                Button("Show Log", action: showLog).buttonStyle(.link).font(.caption)
                Spacer()
                if needsDeveloperTools {
                    Button("Install Xcode Tools…") {
                        // Opens Apple's installer for the Command Line Tools.
                        _ = try? Process.run(URL(fileURLWithPath: "/usr/bin/xcode-select"), arguments: ["--install"])
                    }
                    .controlSize(.small)
                    .help("Builds need Apple's Command Line Tools. Install them, then try again.")
                }
                Button("Dismiss", action: dismiss).controlSize(.small)
                Button("Try Again", action: retry).controlSize(.small).buttonStyle(.borderedProminent)
            }
        }
        .padding(10)
        .background(Color.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

/// A component in a card: its name, version and state, with every action that fits.
/// Used for an installed Valkey.
struct ComponentPanel: View {
    @Environment(AppModel.self) private var model
    let component: ServiceComponent
    let title: String
    let subtitle: String
    let symbol: String
    /// The text of the uninstall confirmation.
    let uninstallMessage: String
    /// What an update keeps, for the update strip ("Your data stays.").
    var updateNote = ""

    private let logState = State<Bool>(initialValue: false)
    private let confirmUninstallState = State<Bool>(initialValue: false)
    /// A request of this panel is waiting for the daemon.
    private let requestingState = State<Bool>(initialValue: false)

    var body: some View {
        let job = component.job
        let requesting = requestingState.wrappedValue

        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 10) {
                Image(systemName: symbol)
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(.secondary)
                    .frame(width: 22)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).font(.body.weight(.medium))
                    if !subtitle.isEmpty {
                        Text(subtitle).font(.caption).foregroundStyle(.secondary)
                            .lineLimit(2).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 8)
                if requesting {
                    ProgressView().controlSize(.small)
                }
                if job?.isRunning != true {
                    trailing(requesting: requesting)
                }
            }

            if let job, job.isRunning {
                ComponentJobProgress(component: component, job: job) {
                    logState.wrappedValue = true
                } cancel: {
                    run { try await $0.cancelComponentJob(component.id) }
                }
                .disabled(requesting)
            } else if let job, job.isFailed {
                ComponentJobFailure(component: component, job: job) {
                    logState.wrappedValue = true
                } retry: {
                    run { try await $0.installComponent(component.id) }
                } dismiss: {
                    run { try await $0.dismissComponentJob(component.id) }
                }
                .disabled(requesting)
            } else if !component.isInstalled {
                Text(component.methodText)
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else if component.update, !component.latest.isEmpty {
                Divider()
                UpdateStrip(title: "\(component.name) \(component.latest) is available",
                            note: [component.method == .build ? "Builds from source\(component.estimate.isEmpty ? "" : ", \(component.estimate)")." : "", updateNote]
                                .filter { !$0.isEmpty }.joined(separator: " "),
                            help: component.installHelp, disabled: requesting) {
                    run { try await $0.installComponent(component.id) }
                }
            }
        }
        .card()
        .task {
            if Snapshot.sheet == "log", component.job != nil {
                logState.wrappedValue = true
            }
        }
        .sheet(isPresented: logState.projectedValue) {
            ComponentLogSheet(component: component)
        }
        .confirmationDialog("Uninstall \(component.name)?", isPresented: confirmUninstallState.projectedValue) {
            Button("Uninstall", role: .destructive) {
                run { try await $0.uninstallComponent(component.id) }
            }
        } message: {
            Text(uninstallMessage)
        }
    }

    @ViewBuilder
    private func trailing(requesting: Bool) -> some View {
        if component.isInstalled {
            HStack(spacing: 6) {
                Text(component.installed).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                    .help(component.update ? "\(component.latest) is available" : "Up to date")
                Menu {
                    if component.update {
                        Button("Update to \(component.latest)") {
                            run { try await $0.installComponent(component.id) }
                        }
                    } else {
                        Button("Reinstall \(component.installed)") {
                            run { try await $0.installComponent(component.id) }
                        }
                    }
                    Button("Show Last Log") { logState.wrappedValue = true }
                    Divider()
                    Button("Uninstall…", role: .destructive) { confirmUninstallState.wrappedValue = true }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")
                .disabled(requesting)
            }
        } else if component.job?.isFailed != true {
            Button("Install") {
                run { try await $0.installComponent(component.id) }
            }
            .controlSize(.small)
            .help(component.installHelp)
            .disabled(requesting)
        }
    }

    private func run(_ action: @escaping @Sendable (FlockClient) async throws -> ComponentCatalog) {
        requestingState.wrappedValue = true
        Task {
            await model.settings.performOnComponents(action)
            requestingState.wrappedValue = false
        }
    }
}

/// "X is available", what updating means, and the Update button.
struct UpdateStrip: View {
    let title: String
    let note: String
    let help: String
    var busy = false
    var disabled = false
    let update: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "arrow.up.circle.fill").foregroundStyle(Color.accentColor).font(.title3)
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.callout.weight(.medium))
                if !note.isEmpty {
                    Text(note).font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 6)
            if busy {
                ProgressView().controlSize(.small)
            } else {
                Button("Update", action: update)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(disabled)
                    .help(help)
            }
        }
    }
}

/// The tail of a component job's output, refreshed while the sheet is open.
struct ComponentLogSheet: View {
    @Environment(AppModel.self) private var model
    let component: ServiceComponent

    var body: some View {
        JobLogSheet(title: "\(component.name) \(component.method == .build ? "build" : "install") log",
                    running: { model.settings.components?[component.id]?.job?.isRunning ?? false },
                    load: { await model.settings.componentLog(component.id) })
    }
}

/// The tail of an install's output, refreshed while it runs and the sheet is open.
struct JobLogSheet: View {
    @Environment(\.dismiss) private var dismiss
    let title: String
    let running: () -> Bool
    let load: () async -> String
    private let textState = State<String>(initialValue: "")

    var body: some View {
        let text = textState.wrappedValue

        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Text(title).font(.headline)
                if running() {
                    ProgressView().controlSize(.small)
                }
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
                    Text(text.isEmpty ? "No log yet." : text)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(text.isEmpty ? .secondary : .primary)
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
                let log = await load()
                if log != textState.wrappedValue {
                    textState.wrappedValue = log
                }
                if !running(), !log.isEmpty {
                    break
                }
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }
}

/// What a detail shows before something is installed: what it is, and one obvious button.
struct InstallInvitation<Actions: View, Footer: View>: View {
    let symbol: String
    let color: Color
    let title: String
    let message: String
    @ViewBuilder var actions: Actions
    @ViewBuilder var footer: Footer

    var body: some View {
        VStack(spacing: 12) {
            SettingsIcon(symbol: symbol, color: color, size: 52)
                .shadow(color: color.opacity(0.25), radius: 8, y: 3)
            VStack(spacing: 4) {
                Text(title).font(.title3.weight(.semibold))
                Text(message)
                    .font(.callout).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            actions
                .padding(.top, 2)
            footer
                .font(.caption).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 22)
        .padding(.horizontal, 18)
        .card(padding: 0)
    }
}

/// Valkey: install it (a build), follow the build, update or uninstall it.
struct ValkeyDetail: View {
    @Environment(AppModel.self) private var model
    let service: Service
    private let logState = State<Bool>(initialValue: false)
    private let requestingState = State<Bool>(initialValue: false)

    var body: some View {
        let store = model.settings
        let component = store.components?[ServiceComponent.valkey]
        let style = ServiceStyle.of(service.id)

        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                DetailHeader(title: service.name, symbol: style.symbol, color: style.color,
                             status: status(component), running: service.installed ? service.running : nil)

                if !service.available {
                    UnavailableNotice(service: service)
                } else if let component {
                    if component.isInstalled {
                        VStack(alignment: .leading, spacing: 8) {
                            DetailSectionTitle(title: "Version") {
                                if let catalog = store.components {
                                    CheckForUpdatesLine(checkedAt: catalog.checkedAt, error: catalog.checkError, disabled: false) {
                                        await store.checkForUpdates()
                                    }
                                }
                            }
                            ComponentPanel(component: component, title: "Valkey",
                                           subtitle: "The open-source Redis; works with every Redis client.",
                                           symbol: "shippingbox",
                                           uninstallMessage: "Valkey stops and your sites can't use it until you install it again. Its data stays.",
                                           updateNote: "Your data stays.")
                        }
                        Divider()
                        ServiceInfo(service: service)
                    } else {
                        notInstalled(component)
                    }
                } else if store.components == nil {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 30)
                } else {
                    // A daemon without component installs: the CLI still works.
                    InstallInvitation(symbol: style.symbol, color: style.color, title: "Valkey isn't installed",
                                      message: "Run `flock valkey install` in Terminal to install it.") {} footer: {}
                }
            }
            .padding(16)
        }
        .sheet(isPresented: logState.projectedValue) {
            if let component {
                ComponentLogSheet(component: component)
            }
        }
    }

    private func status(_ component: ServiceComponent?) -> String {
        if let job = component?.job, job.isRunning {
            return job.action == .update ? "Updating…" : "Installing…"
        }
        guard service.installed else { return service.available ? "Not installed" : "Not available" }
        var parts = [service.running ? "Running" : "Stopped"]
        if service.port > 0 {
            parts.append("Port \(service.port)")
        }
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private func notInstalled(_ component: ServiceComponent) -> some View {
        let requesting = requestingState.wrappedValue
        let job = component.job
        let style = ServiceStyle.of(service.id)
        InstallInvitation(symbol: style.symbol, color: style.color,
                          title: job?.isRunning == true ? "Installing Valkey" : "Install Valkey",
                          message: "The open-source Redis: a fast in-memory store for your cache, queues and sessions, on port \(service.port). Works with every Redis client.") {
            if let job, job.isRunning {
                ComponentJobProgress(component: component, job: job) {
                    logState.wrappedValue = true
                } cancel: {
                    run { try await $0.cancelComponentJob(component.id) }
                }
                .disabled(requesting)
                .multilineTextAlignment(.leading)
                .padding(.top, 6)
            } else if let job, job.isFailed {
                ComponentJobFailure(component: component, job: job) {
                    logState.wrappedValue = true
                } retry: {
                    run { try await $0.installComponent(component.id) }
                } dismiss: {
                    run { try await $0.dismissComponentJob(component.id) }
                }
                .disabled(requesting)
                .multilineTextAlignment(.leading)
            } else {
                HStack(spacing: 8) {
                    Button {
                        run { try await $0.installComponent(component.id) }
                    } label: {
                        Text(component.latest.isEmpty ? "Install Valkey" : "Install Valkey \(component.latest)")
                            .padding(.horizontal, 6)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .help(component.installHelp)
                    .disabled(requesting)
                    if requesting {
                        ProgressView().controlSize(.small)
                    }
                }
            }
        } footer: {
            if job?.isRunning != true {
                Text(component.methodText)
            }
        }
    }

    private func run(_ action: @escaping @Sendable (FlockClient) async throws -> ComponentCatalog) {
        requestingState.wrappedValue = true
        Task {
            await model.settings.performOnComponents(action)
            requestingState.wrappedValue = false
        }
    }
}
