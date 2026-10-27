import FlockKit
import SwiftUI

/// A site's git worktrees in its details: each opens its own details, and New Worktree… creates one.
struct SiteWorktreesSection: View {
    @Environment(AppModel.self) private var model
    let site: Status.Site
    let worktrees: [Status.Site]
    let select: (Status.Site) -> Void

    private let creatingState = State<Bool>(initialValue: false)

    var body: some View {
        let store = model.settings
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
        .sheet(isPresented: creatingState.projectedValue) {
            NewWorktreeSheet(site: site) { branch, from, db in
                creatingState.wrappedValue = false
                Task { await store.perform { _ = try await $0.createWorktree(site: site.name, branch: branch, from: from, db: db) } }
            } cancel: {
                creatingState.wrappedValue = false
            }
        }
    }
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
        return mode == .shared ? "Use \(parent)'s database?" : "Give \(worktree.worktreeLabel) \(mode.title.lowercased()) database?"
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
        Task { await model.settings.perform { [path = worktree.path] in _ = try await $0.switchWorktreeDatabase(path: path, to: mode) } }
    }

    private func runSetup() {
        Task { await model.settings.perform { [path = worktree.path] in _ = try await $0.setupWorktree(path: path) } }
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

/// Asks for a new worktree's branch, where a new branch starts, and its database.
private struct NewWorktreeSheet: View {
    let site: Status.Site
    let create: (String, String, DatabaseMode?) -> Void
    let cancel: () -> Void
    private let branchState = State<String>(initialValue: "")
    private let fromState = State<String>(initialValue: "")
    private let databaseState = State<DatabaseMode?>(initialValue: nil)

    private var branch: String { branchState.wrappedValue.trimmingCharacters(in: .whitespaces) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New Worktree of \(site.name)").font(.headline)
            Form {
                TextField("Branch", text: branchState.projectedValue, prompt: Text("feature/checkout"))
                TextField("Start from", text: fromState.projectedValue, prompt: Text("the current commit"))
                Picker("Database", selection: databaseState.projectedValue) {
                    Text("Project default").tag(DatabaseMode?.none)
                    ForEach(DatabaseMode.allCases) { Text($0.title).tag(DatabaseMode?.some($0)) }
                }
            }
            Text("Flock creates the worktree next to \(site.name) on this branch (a branch that exists is checked out), sets it up and serves it at \(branch.isEmpty ? "<branch>" : slug).\(site.domain).")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { cancel() }
                    .keyboardShortcut(.cancelAction)
                Button("Create") { create(branch, fromState.wrappedValue.trimmingCharacters(in: .whitespaces), databaseState.wrappedValue) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(branch.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 440)
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
