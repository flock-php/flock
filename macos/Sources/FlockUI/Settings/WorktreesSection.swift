import FlockKit
import SwiftUI

/// A site's git worktrees in its details: each opens its own details, and New Worktree… creates one.
struct SiteWorktreesSection: View {
    @Environment(AppModel.self) private var model
    let site: Status.Site
    let worktrees: [Status.Site]
    let select: (Status.Site) -> Void

    private let creatingState = State<Bool>(initialValue: false)
    /// What Create asked for, once clicked: the sheet then follows it.
    private let requestState = State<NewWorktree?>(initialValue: nil)

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Worktrees").font(.subheadline.weight(.semibold))
            ForEach(worktrees) { worktree in
                Button { select(worktree) } label: {
                    HStack(spacing: 6) {
                        Image(systemName: "arrow.triangle.branch").foregroundStyle(.secondary)
                        Text(worktree.worktreeLabel)
                        Text(worktree.domain).foregroundStyle(.secondary)
                        Spacer(minLength: 0)
                        WorktreeState(worktree: worktree)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .font(.callout)
                .help("Show \(worktree.worktreeLabel)")
            }
            HStack(spacing: 8) {
                Button("New Worktree…") { creatingState.wrappedValue = true }
                Text(worktrees.isEmpty ? "Work on another branch at its own address, e.g. feature.\(site.domain)." : "")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .sheet(isPresented: creatingState.projectedValue, onDismiss: { requestState.wrappedValue = nil }) {
            if let request = requestState.wrappedValue {
                WorktreeJobView(title: "New Worktree of \(site.name)") { [name = site.name] client in
                    try await client.createWorktree(site: name, branch: request.branch, from: request.from, db: request.db).path
                } close: { worktree in
                    creatingState.wrappedValue = false
                    if let worktree { select(worktree) }
                }
            } else {
                NewWorktreeSheet(site: site) { branch, from, db in
                    requestState.wrappedValue = NewWorktree(branch: branch, from: from, db: db)
                } cancel: {
                    creatingState.wrappedValue = false
                }
            }
        }
    }
}

/// What New Worktree… asks for.
private struct NewWorktree: Sendable {
    let branch: String
    let from: String
    let db: DatabaseMode?
}

/// A selected worktree: its address and folder, the site whose settings it uses, its database and
/// its setup, instead of a site's settings.
struct WorktreeDetail: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    let worktree: Status.Site
    let selectParent: () -> Void

    /// A database switch waiting for confirmation: the worktree's own database is dropped or kept.
    private let switchingState = State<DatabaseMode?>(initialValue: nil)
    private var switching: DatabaseMode? {
        get { switchingState.wrappedValue }
        nonmutating set { switchingState.wrappedValue = newValue }
    }
    private let removingState = State<Bool>(initialValue: false)
    /// The job a sheet follows: a setup run again or a database switch.
    private let jobState = State<WorktreeJob?>(initialValue: nil)
    /// Why removing failed, offering to remove it anyway.
    private let removeErrorState = State<String?>(initialValue: nil)
    private var removeError: String? {
        get { removeErrorState.wrappedValue }
        nonmutating set { removeErrorState.wrappedValue = newValue }
    }

    private var setup: WorktreeSetup? { worktree.worktree }
    private var parent: String { worktree.parent ?? "" }
    private var ownDatabase: String { setup?.database ?? "" }

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            header
            section("Worktree") {
                field("Branch") { Text(worktree.branch.isEmpty ? "Detached HEAD" : worktree.branch).textSelection(.enabled) }
                field("Site") {
                    Button(parent, action: selectParent).buttonStyle(.link)
                }
                field("Versions") { Text("PHP \(worktree.php)" + (worktree.node.map { " · Node.js \($0)" } ?? "")) }
                Text("A worktree uses \(parent)'s settings: its PHP and Node.js versions, HTTPS and document root. Change them on \(parent).")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            section("Database") {
                field("Database") {
                    Picker("Database", selection: Binding(get: { currentMode }, set: { mode in
                        if mode != currentMode { switching = mode }
                    })) {
                        ForEach(DatabaseMode.allCases) { Text($0.title).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                    .disabled(setup?.busy ?? false)
                }
                Text(ownDatabase.isEmpty ? "It uses \(parent)'s database: migrations you run in the worktree change it too."
                                         : "Its own database: \(ownDatabase).")
                    .font(.caption).foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            section("Setup") {
                setupState
                HStack(spacing: 8) {
                    Button(setup == nil ? "Set Up" : "Run Setup Again") { runSetup() }
                        .disabled(setup?.busy ?? false)
                        .help("Copies what's missing from \(parent), points .env at the worktree, runs the installs and the project's hooks")
                    if let log = setup?.log, !log.isEmpty {
                        Button("Show Log") {
                            model.logs.requested = .service(log)
                            openWindow.activating(WindowID.logs)
                        }
                    }
                }
            }
        }
        .sheet(item: jobState.projectedValue) { job in
            WorktreeJobView(title: job.title, start: job.start) { _ in jobState.wrappedValue = nil }
        }
        .confirmationDialog(switchTitle, isPresented: Binding(get: { switching != nil }, set: { if !$0 { switching = nil } }),
                            presenting: switching) { mode in
            if ownDatabase.isEmpty {
                Button("Switch") { switchDatabase(to: mode) }
            } else {
                Button("Switch and Drop \(ownDatabase)", role: .destructive) { switchDatabase(to: mode) }
            }
            Button("Cancel", role: .cancel) {}
        } message: { mode in
            Text(switchMessage(mode))
        }
        .confirmationDialog("Remove \(worktree.worktreeLabel)?", isPresented: removingState.projectedValue) {
            Button("Remove", role: .destructive) { remove(force: false) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Flock runs its before_delete hook and deletes \(worktree.path)\(ownDatabase.isEmpty ? "" : " and its database \(ownDatabase)"). \(worktree.branch.isEmpty ? "Commits" : "The branch \(worktree.branch)") stays.")
        }
        .alert("Couldn't remove \(worktree.worktreeLabel)", isPresented: Binding(get: { removeError != nil }, set: { if !$0 { removeError = nil } })) {
            Button("Remove Anyway", role: .destructive) { remove(force: true) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(removeError ?? "")
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(worktree.worktreeLabel).font(.title3.weight(.semibold))
                    Badge(text: "Worktree of \(parent)", color: .secondary)
                }
                if let text = worktree.url, let url = URL(string: text) {
                    Link(text, destination: url).font(.callout)
                }
                Text(worktree.path).font(.caption).foregroundStyle(.tertiary)
                    .lineLimit(1).truncationMode(.middle)
                    .help(worktree.path)
            }
            Spacer(minLength: 8)
            HStack(spacing: 8) {
                Button { model.open(worktree) } label: { Image(systemName: "safari") }
                    .help("Open \(worktree.url ?? worktree.domain)")
                    .disabled(worktree.url == nil)
                Button { reveal(worktree.path) } label: { Image(systemName: "folder") }
                    .help("Show in Finder")
                Button { removingState.wrappedValue = true } label: { Image(systemName: "minus.circle") }
                    .help("Remove worktree")
                    .disabled(setup?.busy ?? false)
            }
            .buttonStyle(.borderless)
        }
    }

    @ViewBuilder private var setupState: some View {
        switch setup?.state {
        case .queued, .running:
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text(setup?.step.isEmpty == false ? "\(setup!.step.prefix(1).uppercased())\(setup!.step.dropFirst())…" : "Setting up…")
            }
            .font(.callout)
        case .failed:
            Label("Setup failed: \(setup?.error ?? "see its log")", systemImage: "exclamationmark.triangle.fill")
                .font(.callout).foregroundStyle(.orange)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        case .ready:
            Label("Set up", systemImage: "checkmark.circle").font(.callout).foregroundStyle(.secondary)
        case .unknown, nil:
            Text("Flock hasn't set this worktree up: it existed before Flock could, or flock.yml turns setting up off.")
                .font(.callout).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    /// The mode it has: the one asked for, else what its database says.
    private var currentMode: DatabaseMode {
        setup?.dbMode ?? (ownDatabase.isEmpty ? .shared : .copy)
    }

    private var switchTitle: String {
        guard let mode = switching else { return "" }
        switch mode {
        case .shared: return "Use \(parent)'s database?"
        case .copy: return "Give \(worktree.worktreeLabel) a copy of \(parent)'s database?"
        case .fresh: return "Give \(worktree.worktreeLabel) a new, empty database?"
        }
    }

    private func switchMessage(_ mode: DatabaseMode) -> String {
        let new = switch mode {
        case .shared: "It will use \(parent)'s database."
        case .copy: "Flock copies \(parent)'s database for it and runs the migrations."
        case .fresh: "Flock creates an empty database for it and runs the migrations."
        }
        return ownDatabase.isEmpty ? new : "\(new) Its database \(ownDatabase) is dropped."
    }

    private func switchDatabase(to mode: DatabaseMode) {
        let path = worktree.path
        jobState.wrappedValue = WorktreeJob(title: "Switching \(worktree.worktreeLabel)'s Database") { client in
            _ = try await client.switchWorktreeDatabase(path: path, to: mode)
            return path
        }
    }

    private func runSetup() {
        let path = worktree.path
        jobState.wrappedValue = WorktreeJob(title: "Setting Up \(worktree.worktreeLabel)") { client in
            _ = try await client.setupWorktree(path: path)
            return path
        }
    }

    /// Removes it; when git refuses (changes in it, a failing hook), offers to remove it anyway.
    private func remove(force: Bool) {
        let path = worktree.path
        Task {
            do {
                try await model.client.removeWorktree(path: path, force: force)
                await model.settings.perform { _ in }
            } catch {
                removeError = error.localizedDescription
            }
        }
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.subheadline.weight(.semibold))
            content()
        }
    }

    private func field<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label).foregroundStyle(.secondary)
                .frame(width: 80, alignment: .leading)
            content()
            Spacer(minLength: 0)
        }
    }
}

/// How a worktree's setup goes, small: a spinner while it runs, a warning when it failed.
struct WorktreeState: View {
    let worktree: Status.Site

    var body: some View {
        switch worktree.worktree?.state {
        case .queued, .running:
            ProgressView().controlSize(.mini).help(worktree.worktree?.step ?? "Setting up")
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
                .help("Setup failed: \(worktree.worktree?.error ?? "")")
        default:
            if let database = worktree.worktree?.database, !database.isEmpty {
                Image(systemName: "cylinder").font(.caption).foregroundStyle(.secondary)
                    .help("Its own database: \(database)")
            }
        }
    }
}

/// Asks for a new worktree's branch (empty: Flock names it), where a new branch starts (the site's
/// branches, its current one first) and its database.
private struct NewWorktreeSheet: View {
    @Environment(AppModel.self) private var model
    let site: Status.Site
    let create: (String, String, DatabaseMode?) -> Void
    let cancel: () -> Void
    private let branchState = State<String>(initialValue: "")
    /// Where a new branch starts: a local or remote branch; "" for the current commit.
    private let fromState = State<String>(initialValue: "")
    private let databaseState = State<DatabaseMode?>(initialValue: nil)
    private let branchesState = State<Branches?>(initialValue: nil)

    private var branch: String { branchState.wrappedValue.trimmingCharacters(in: .whitespaces) }
    private var branches: Branches { branchesState.wrappedValue ?? Branches() }
    /// The branch typed in exists: it's checked out as it is.
    private var exists: Bool { branches.local.contains(branch) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New Worktree of \(site.name)").font(.headline)
            Form {
                TextField("Branch", text: branchState.projectedValue, prompt: Text("a name like brave-otter"))
                Picker("Start from", selection: fromState.projectedValue) {
                    if branches.current.isEmpty {
                        Text("The current commit").tag("")
                    }
                    ForEach(branches.ordered, id: \.self) { name in
                        Text(label(name)).tag(name)
                    }
                    if !branches.remote.isEmpty {
                        Divider()
                        ForEach(branches.remote, id: \.self) { Text($0).tag($0) }
                    }
                }
                .disabled(exists || branchesState.wrappedValue == nil)
                Picker("Database", selection: databaseState.projectedValue) {
                    Text("Project default").tag(DatabaseMode?.none)
                    ForEach(DatabaseMode.allCases) { Text($0.title).tag(DatabaseMode?.some($0)) }
                }
            }
            Text(explanation)
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("Create") { create(branch, exists ? "" : fromState.wrappedValue, databaseState.wrappedValue) }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 440)
        .task {
            let loaded = (try? await model.client.siteBranches(site.name)) ?? Branches()
            branchesState.wrappedValue = loaded
            fromState.wrappedValue = loaded.current
        }
    }

    private func label(_ name: String) -> String {
        switch name {
        case branches.current: "\(name) (current)"
        case branches.default: "\(name) (default)"
        default: name
        }
    }

    private var explanation: String {
        let address = branch.isEmpty ? "its own address" : "\(slug).\(site.domain)"
        if exists {
            return "\(branch) exists, so the worktree checks it out. Flock creates it next to \(site.name), sets it up and serves it at \(address)."
        }
        let name = branch.isEmpty ? "a new branch with a name of its own" : "the new branch \(branch)"
        return "Flock creates the worktree next to \(site.name) on \(name), sets it up and serves it at \(address)."
    }

    /// The name it's served under, as Flock makes it from the branch (config.WorktreeSlug).
    private var slug: String {
        var out = ""
        for c in branch.lowercased() {
            if c.isASCII && (c.isLetter || c.isNumber || c == "-") {
                out.append(c)
            } else if c == "/" || c == "_" || c == "." {
                out.append("-")
            }
        }
        while out.contains("--") { out = out.replacingOccurrences(of: "--", with: "-") }
        return out.trimmingCharacters(in: CharacterSet(charactersIn: "-"))
    }
}

/// Follows a worktree's job (its creation and setup, a setup run again, a database switch) in a
/// sheet: the step it's at and its log as it grows, then Ready with Open in Browser, or the error
/// with Run Setup Again. Hide leaves it running; the list shows it, and a failure is notified.
struct WorktreeJobView: View {
    @Environment(AppModel.self) private var model
    let title: String
    /// Starts the job and returns the worktree's folder.
    let start: @Sendable (FlockClient) async throws -> String
    /// Closes the sheet; with the worktree when it's ready, so it can be shown.
    let close: (Status.Site?) -> Void

    private enum Phase: Equatable {
        case starting
        case running(path: String, step: String)
        case ready(path: String)
        case failed(path: String?, error: String)
    }

    private let phaseState = State<Phase>(initialValue: .starting)
    private var phase: Phase {
        get { phaseState.wrappedValue }
        nonmutating set { phaseState.wrappedValue = newValue }
    }
    private let logState = State<String>(initialValue: "")
    /// Bumped to start the job again (Run Setup Again).
    private let runState = State<Int>(initialValue: 0)

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title).font(.headline)
            headline
            ScrollViewReader { proxy in
                ScrollView {
                    Text(logState.wrappedValue.isEmpty ? " " : logState.wrappedValue)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)
                    Color.clear.frame(height: 1).id("end")
                }
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: logState.wrappedValue) { proxy.scrollTo("end", anchor: .bottom) }
            }
            .frame(height: 260)
            buttons
        }
        .padding(20)
        .frame(width: 600)
        .task(id: runState.wrappedValue) { await run() }
        .onDisappear {
            if let path = path { model.worktreeNotifier.unfollow(path) }
        }
    }

    private var path: String? {
        switch phase {
        case .starting: nil
        case let .running(path, _), let .ready(path): path
        case let .failed(path, _): path
        }
    }

    private var worktree: Status.Site? {
        guard let path else { return nil }
        return model.status?.sites.first { $0.isWorktree && $0.path == path }
    }

    @ViewBuilder private var headline: some View {
        switch phase {
        case .starting:
            HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Starting…") }
        case let .running(_, step):
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text(step.isEmpty ? "Setting up…" : "\(step.prefix(1).uppercased())\(step.dropFirst())…")
            }
        case .ready:
            Label("Ready\(worktree?.url.map { " at \($0)" } ?? "")", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
        case let .failed(_, error):
            Label(error, systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder private var buttons: some View {
        HStack {
            Spacer()
            switch phase {
            case .starting, .running:
                Button("Hide") { close(nil) }
                    .keyboardShortcut(.cancelAction)
                    .help("The setup goes on; Flock tells you if it fails")
            case .ready:
                if let worktree, worktree.url != nil {
                    Button("Open in Browser") { model.open(worktree) }
                }
                Button("Close") { close(worktree) }
                    .keyboardShortcut(.defaultAction)
            case let .failed(path, _):
                if let path {
                    Button("Run Setup Again") { restart(path) }
                }
                Button("Close") { close(nil) }
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    /// The job's restart: its setup again, whatever started it.
    private let restartState = State<String?>(initialValue: nil)

    private func restart(_ path: String) {
        restartState.wrappedValue = path
        logState.wrappedValue = ""
        phase = .starting
        runState.wrappedValue += 1
    }

    private func run() async {
        let client = model.client
        let path: String
        do {
            if let again = restartState.wrappedValue {
                _ = try await client.setupWorktree(path: again)
                path = again
            } else {
                path = try await start(client)
            }
        } catch {
            phase = .failed(path: nil, error: error.localizedDescription)
            return
        }
        model.worktreeNotifier.follow(path)
        phase = .running(path: path, step: "")
        while !Task.isCancelled {
            if let setup = try? await client.worktreeSetup(path: path) {
                if !setup.log.isEmpty, let log = try? await client.serviceLog(setup.log, lines: 400) {
                    logState.wrappedValue = log
                }
                switch setup.state {
                case .ready:
                    phase = .ready(path: path)
                    return
                case .failed:
                    phase = .failed(path: path, error: "Setup failed: \(setup.error ?? "see the log")")
                    return
                default:
                    phase = .running(path: path, step: setup.step)
                }
            }
            try? await Task.sleep(for: .milliseconds(500))
        }
    }
}

/// A job of a worktree a sheet follows (see WorktreeJobView).
private struct WorktreeJob: Identifiable {
    let id = UUID()
    let title: String
    let start: @Sendable (FlockClient) async throws -> String
}
