import AppKit
import FlockKit
import SwiftUI

/// The sites in a list, and the selected one's details beside it, as in Services: its address and
/// domains, its PHP and Node.js versions and what serves it. Each version picker offers the
/// project's pinned version (or the default) first, then the installed versions, which isolate the
/// site on this machine; the pin menu pins a version into the project or removes it again.
struct SitesPane: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    private let selectionState = State<Status.Site.ID?>(initialValue: nil)
    private let confirmState = State<ConfirmAction?>(initialValue: nil)
    private var confirm: ConfirmAction? {
        get { confirmState.wrappedValue }
        nonmutating set { confirmState.wrappedValue = newValue }
    }
    /// The site a domain is being added to, and the name typed so far.
    private let addingState = State<String?>(initialValue: nil)
    private var adding: String? {
        get { addingState.wrappedValue }
        nonmutating set { addingState.wrappedValue = newValue }
    }
    private let newDomainState = State<String>(initialValue: "")
    /// The site whose PHP extensions are shown.
    private let extensionsState = State<Status.Site?>(initialValue: nil)
    /// The site whose FrankenPHP is restarting.
    private let restartingState = State<String?>(initialValue: nil)
    private var restarting: String? {
        get { restartingState.wrappedValue }
        nonmutating set { restartingState.wrappedValue = newValue }
    }

    var body: some View {
        let store = model.settings
        let sites = model.status?.sites ?? []
        let phpVersions = store.config?.php.map(\.version) ?? []
        let nodeMajors = store.config?.nodeMajors ?? []

        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .bottom) {
                PaneHeader(title: "Sites", subtitle: "Sites you added one by one, plus every folder inside your project folders.")
                Button("Add Site…") {
                    for url in SettingsStore.chooseDirectories(prompt: "Add", multiple: false) {
                        Task { await store.perform { _ = try await $0.linkSite(path: url.path) } }
                    }
                }
                .padding(.trailing, 20)
                .padding(.bottom, 6)
            }

            if sites.isEmpty {
                ContentUnavailableView("No sites", systemImage: "globe",
                                       description: Text("Add a single site here, or add a project folder in General."))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                // The sites in a list, the selected one's details beside it, as in Services.
                let selected = sites.first { $0.id == selectionState.wrappedValue } ?? sites[0]
                HStack(spacing: 0) {
                    List(selection: Binding<Status.Site.ID?>(get: { selected.id }, set: { selectionState.wrappedValue = $0 })) {
                        ForEach(sites) { site in
                            siteRow(site).tag(site.id)
                        }
                    }
                    .listStyle(.inset)
                    .frame(width: 230)

                    Divider()

                    ScrollView {
                        detail(selected, phpVersions: phpVersions, nodeMajors: nodeMajors)
                            .padding(16)
                    }
                    // Another site's details start at the top.
                    .id(selected.id)
                    .frame(minWidth: 340, maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                    .background(Color(nsColor: .controlBackgroundColor).opacity(0.5))
                }
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.separator))
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .padding(.horizontal, 20)
                .padding(.vertical, 12)
            }
        }
        .sheet(item: extensionsState.projectedValue) { site in
            SiteExtensionsSheet(site: site)
        }
        .confirmationDialog(confirm?.title ?? "", isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } }), presenting: confirm) { action in
            Button(action.button, role: action.destructive ? .destructive : nil) {
                Task { await store.perform { _ = try await action.run($0) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: { action in
            Text(action.message)
        }
        .alert("Add a domain to \(adding ?? "")", isPresented: Binding(get: { adding != nil }, set: { if !$0 { adding = nil } })) {
            TextField("tenant1, a.b or \(adding ?? "app")-2.\(model.status?.dns.tld ?? "test")", text: newDomainState.projectedValue)
            Button("Add") {
                let name = adding ?? ""
                let domain = newDomainState.wrappedValue.trimmingCharacters(in: .whitespaces)
                newDomainState.wrappedValue = ""
                if !name.isEmpty, !domain.isEmpty {
                    Task { await store.perform { _ = try await $0.addDomain(site: name, domain: domain) } }
                }
            }
            Button("Cancel", role: .cancel) { newDomainState.wrappedValue = "" }
        } message: {
            Text(domainHelp)
        }
        // A flock://secure link (AppModel.handle): nothing changes until the user confirms here.
        .alert(linkRequest?.title ?? "", isPresented: Binding(get: { linkRequest != nil }, set: { if !$0 { model.pendingSecure = nil } }),
               presenting: linkRequest) { request in
            if let name = request.confirmation {
                Button("Secure") {
                    Task { await store.perform { _ = try await $0.updateSite(name, SiteChange(secured: true)) } }
                }
                Button("Cancel", role: .cancel) {}
            } else {
                Button("OK") {}
            }
        } message: { request in
            Text(request.message)
        }
    }

    /// What a pending flock://secure link asks for, once the sites are loaded.
    private var linkRequest: SecureLink.Resolution? {
        model.pendingSecure?.resolve(sites: model.status?.sites)
    }

    /// What adding a domain does on this machine.
    private var domainHelp: String {
        let dns = model.status?.dns
        let depth = dns?.maxDepth ?? 4
        if dns?.wildcard ?? true {
            return "Every subdomain up to \(depth) levels below the site already works. Add a full name like \(adding ?? "app")-2.\(dns?.tld ?? "test") for an alias, or a subdomain (tenant1, a.b) so it is listed here and also resolves on Windows machines without wildcard DNS."
        }
        return "Wildcard subdomains don't work here (\(dns?.wildcardProblem ?? "")), so add each subdomain you use (tenant1, a.b). A full name adds an alias."
    }

    /// A site in the list: its name and address, a lock when it is secured, a warning when something is wrong.
    private func siteRow(_ site: Status.Site) -> some View {
        HStack(spacing: 6) {
            VStack(alignment: .leading, spacing: 1) {
                Text(site.name).fontWeight(.medium).lineLimit(1)
                Text(site.domain).font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 4)
            if let problem = problem(site) {
                Image(systemName: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
                    .help(problem)
            }
            if site.secured {
                Image(systemName: "lock.fill").font(.caption).foregroundStyle(.secondary)
                    .help("Served over HTTPS")
            }
        }
        .padding(.vertical, 2)
        .help(site.path)
    }

    /// What a site's list row warns about: the site, one of its domains, or its FrankenPHP.
    private func problem(_ site: Status.Site) -> String? {
        if let problem = site.problem {
            return problem
        }
        if let domain = site.domains.first(where: { $0.problem != nil }) {
            return "\(domain.host): \(domain.problem ?? "")"
        }
        if site.server == .frankenphp, let franken = site.frankenPHP, franken.state == .failed || franken.state == .unavailable,
           !franken.problem.isEmpty {
            return franken.problem
        }
        return nil
    }

    /// The selected site: its address and actions, then Site, Versions and Server sections of labelled settings.
    private func detail(_ site: Status.Site, phpVersions: [String], nodeMajors: [String]) -> some View {
        let store = model.settings
        return VStack(alignment: .leading, spacing: 20) {
            HStack(alignment: .top, spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(site.name).font(.title3.weight(.semibold))
                        if site.parked {
                            Badge(text: "Project folder", color: .secondary)
                        }
                    }
                    if let text = site.url, let url = URL(string: text) {
                        Link(text, destination: url).font(.callout)
                    }
                    Text(site.path).font(.caption).foregroundStyle(.tertiary)
                        .lineLimit(1).truncationMode(.middle)
                        .help(site.path)
                }
                Spacer(minLength: 8)
                HStack(spacing: 8) {
                    Button { model.open(site) } label: { Image(systemName: "safari") }
                        .help("Open \(site.url ?? site.domain)")
                        .disabled(site.url == nil)
                    Button { reveal(site.path) } label: { Image(systemName: "folder") }
                        .help("Show in Finder")
                    Button { extensionsState.wrappedValue = site } label: { Image(systemName: "puzzlepiece.extension") }
                        .help("PHP extensions")
                    pinMenu(site)
                    if !site.parked {
                        Button {
                            confirm = ConfirmAction(
                                title: "Remove \(site.name)?", button: "Remove", destructive: true,
                                message: "Flock stops serving \(site.domain). The project in \(site.path) is not touched.",
                                run: { [name = site.name] client in try await client.unlinkSite(name) })
                        } label: { Image(systemName: "minus.circle") }
                            .help("Remove site")
                    }
                }
                .buttonStyle(.borderless)
            }
            if let problem = site.problem {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout).foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("Site") {
                field("HTTPS") {
                    Toggle("HTTPS", isOn: Binding(
                        get: { site.secured },
                        set: { on in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(secured: on)) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                field("Domains") {
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(site.domains) { domain in
                            HStack(spacing: 4) {
                                if let problem = domain.problem {
                                    Label("\(domain.host): \(problem)", systemImage: "exclamationmark.triangle.fill")
                                        .foregroundStyle(.orange)
                                        .fixedSize(horizontal: false, vertical: true)
                                } else if let url = domain.url.flatMap(URL.init(string:)) {
                                    Link(domain.alias ? "\(domain.host) (alias)" : domain.host, destination: url)
                                } else {
                                    Text(domain.host)
                                }
                                Button {
                                    confirm = ConfirmAction(
                                        title: "Remove \(domain.host)?", button: "Remove", destructive: true,
                                        message: "\(domain.host) is no longer served by \(site.name).",
                                        run: { [name = site.name, entry = domain.entry] client in try await client.removeDomain(site: name, domain: entry) })
                                } label: { Image(systemName: "minus.circle") }
                                    .buttonStyle(.borderless)
                                    .help("Remove \(domain.host)")
                            }
                        }
                        Button("Add Domain…") { adding = site.name }
                            .buttonStyle(.link)
                            .help("Extra domains: subdomains and aliases")
                    }
                    .font(.callout)
                }
                if let dns = model.status?.dns, !dns.wildcard, !dns.wildcardProblem.isEmpty {
                    Text("Subdomains only work when you add them here: \(dns.wildcardProblem).")
                        .font(.caption).foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            section("Versions") {
                field("PHP") {
                    versionPicker(label: "PHP", current: site.php, source: site.phpSource, pinned: site.phpPinned, file: site.phpFile,
                                  defaultVersion: store.config?.defaultPHP ?? "", installed: phpVersions) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: version)) } }
                    }
                }
                field("Node.js") {
                    versionPicker(label: "Node", current: major(site.node ?? ""), source: site.nodeSource, pinned: major(site.nodePinned), file: site.nodeFile,
                                  defaultVersion: store.config?.defaultNode ?? "", installed: nodeMajors) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: version)) } }
                    }
                }
                hints(site)
                Text("A version picked here applies on this machine only. To set it for everyone working on the project, pin it (\(Image(systemName: "pin"))): PHP goes into .flockrc, Node.js into .nvmrc.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            section("Server") {
                serverFields(site)
            }

            if site.boost == .missing || site.boost == .outdated {
                section("Laravel Boost") {
                    boost(site)
                }
            }
        }
    }

    /// A section of the details: a heading and its settings.
    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.subheadline.weight(.semibold))
            content()
        }
    }

    /// A labelled setting: the label in a fixed column, so the controls line up.
    private func field<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label).foregroundStyle(.secondary)
                .frame(width: 80, alignment: .leading)
            content()
            Spacer(minLength: 0)
        }
    }

    /// The project's pinned version ("Use .nvmrc (Node 20)") or the default first, which follow the
    /// project; then the installed versions, which isolate the site. The tag "" means "follow".
    private func versionPicker(label: String, current: String, source: Status.VersionSource, pinned: String, file: String,
                               defaultVersion: String, installed: [String], change: @escaping (String) -> Void) -> some View {
        let follow = pinned.isEmpty ? "Default (\(label) \(defaultVersion))" : "Use \(fileName(file)) (\(label) \(pinned))"
        let versions = source == .isolated && !current.isEmpty && !installed.contains(current) ? installed + [current] : installed
        let help: String = switch source {
        case .isolated: "\(label) \(current) is set for this site on this machine (isolated)."
        case .project: "The project pins \(label) \(pinned) in \(fileName(file))."
        case .default: "The site follows Flock's default \(label) version."
        }
        return Picker(label, selection: Binding(
            get: { source == .isolated ? current : "" },
            set: { change($0) }
        )) {
            Text(follow).tag("")
            Divider()
            ForEach(versions, id: \.self) { version in
                Text("\(label) \(version)").tag(version)
            }
        }
        .labelsHidden()
        .fixedSize()
        .help(help)
    }

    /// What runs the site's PHP (PHP-FPM or FrankenPHP); on FrankenPHP also its mode (classic or
    /// Laravel Octane), its state, Restart and its log.
    @ViewBuilder
    private func serverFields(_ site: Status.Site) -> some View {
        let store = model.settings
        field("Runs on") {
            Picker("Server", selection: Binding(
                get: { site.server },
                set: { server in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(server: server.rawValue)) } } }
            )) {
                Text("PHP-FPM").tag(Status.Server.fastcgi)
                Text("FrankenPHP").tag(Status.Server.frankenphp)
                    .disabled(!site.supportsFrankenPHP && site.server != .frankenphp)
            }
            .labelsHidden()
            .fixedSize()
        }

        if site.server == .frankenphp {
            field("Mode") {
                Picker("Mode", selection: Binding(
                    get: { site.worker },
                    set: { worker in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(worker: worker.rawValue)) } } }
                )) {
                    Text("Classic").tag(Status.Worker.classic)
                    Text("Laravel Octane").tag(Status.Worker.octane)
                        .disabled(!site.octaneAvailable && site.worker != .octane)
                }
                .labelsHidden()
                .fixedSize()
                .help(site.octaneAvailable ? "" : "Install Laravel Octane in the project (composer require laravel/octane) to use worker mode.")
            }

            frankenPHPState(site)

            HStack(spacing: 8) {
                Button("Restart") {
                    restarting = site.name
                    Task {
                        await store.perform { _ = try await $0.restartSite(site.name) }
                        restarting = nil
                    }
                }
                .disabled(site.frankenPHP?.state != .running || restarting == site.name)
                .help("Restart FrankenPHP, e.g. so Octane's workers load changed code")
                Button("Show Log") {
                    model.logs.requested = .service(frankenPHPLog(site))
                    openWindow.activating(WindowID.logs)
                }
                if restarting == site.name {
                    ProgressView().controlSize(.mini)
                }
            }
            .controlSize(.small)
        }

        Text(site.server == .frankenphp && !site.octaneAvailable
             ? "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM. For worker mode, install Laravel Octane in the project (composer require laravel/octane)."
             : "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM.")
            .font(.caption).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// The FrankenPHP process: running, downloading its build, or why PHP-FPM serves the site.
    @ViewBuilder
    private func frankenPHPState(_ site: Status.Site) -> some View {
        if let franken = site.frankenPHP {
            switch franken.state {
            case .running:
                Text(runningText(site, franken))
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            case .installing:
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        if let progress = franken.progress {
                            ProgressView(value: progress).frame(width: 60)
                        } else {
                            ProgressView().controlSize(.mini)
                        }
                        Text(downloadingText(site, franken))
                    }
                    Text("PHP-FPM serves the site until it's ready.")
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            case .failed, .unavailable:
                VStack(alignment: .leading, spacing: 2) {
                    if !franken.problem.isEmpty {
                        Label(franken.problem, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .textSelection(.enabled)
                    }
                    Text("PHP-FPM serves the site meanwhile.")
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            case .unknown:
                EmptyView()
            }
        }
    }

    /// "Running FrankenPHP 1.13.0 with PHP 8.4.14 · worker mode".
    private func runningText(_ site: Status.Site, _ franken: Status.SiteFrankenPHP) -> String {
        let text = "Running FrankenPHP \(franken.version) with PHP \(franken.phpVersion)"
        return site.worker == .octane ? text + " · worker mode" : text
    }

    /// "Downloading FrankenPHP for PHP 8.4… 42%"; without a percentage while the size is unknown.
    private func downloadingText(_ site: Status.Site, _ franken: Status.SiteFrankenPHP) -> String {
        let text = "Downloading FrankenPHP for PHP \(site.php)…"
        guard let progress = franken.progress else { return text }
        return "\(text) \(Int((progress * 100).rounded()))%"
    }

    /// The name of the site's FrankenPHP log, a service log in the Logs window.
    private func frankenPHPLog(_ site: Status.Site) -> String {
        if let log = site.frankenPHP?.log, !log.isEmpty {
            return log
        }
        return "frankenphp-\(site.name)"
    }

    /// Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.
    @ViewBuilder
    private func hints(_ site: Status.Site) -> some View {
        let store = model.settings
        if site.phpSource == .isolated, !site.phpPinned.isEmpty, site.phpPinned != site.php {
            HStack(spacing: 4) {
                Text("PHP \(site.php) is isolated here; the project pins \(site.phpPinned) in \(fileName(site.phpFile)).")
                Button("Use \(fileName(site.phpFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if site.nodeSource == .isolated, !site.nodePinned.isEmpty, major(site.nodePinned) != major(site.node ?? "") {
            HStack(spacing: 4) {
                Text("Node \(site.node ?? "") is isolated here; the project pins \(site.nodePinned) in \(fileName(site.nodeFile)).")
                Button("Use \(fileName(site.nodeFile))") {
                    Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: "")) } }
                }
                .buttonStyle(.link)
            }
            .font(.caption).foregroundStyle(.orange)
        }
        if !site.nodeShadowed.isEmpty {
            Text(".flockrc's Node \(site.nodePinned) wins over \(site.nodeShadowed).")
                .font(.caption).foregroundStyle(.orange)
        }
    }

    /// The project uses Laravel Boost, and Flock's guideline is missing from it or outdated.
    private func boost(_ site: Status.Site) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(site.boost == .missing
                 ? "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
                 : "Flock's guideline in the project's Boost guidelines is outdated.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(site.boost == .missing ? "Add Flock to its guidelines…" : "Update it…") {
                confirm = ConfirmAction(
                    title: site.boost == .missing ? "Add Flock to \(site.name)'s Boost guidelines?" : "Update Flock's guideline in \(site.name)?",
                    button: site.boost == .missing ? "Add" : "Update",
                    message: "Writes .ai/guidelines/flock.md in \(site.path) and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. Commit it with the rest of .ai/guidelines. This changes files in your project.",
                    run: { [name = site.name] client in try await client.addBoostGuideline(site: name) })
            }
            .buttonStyle(.link)
        }
        .font(.caption)
    }

    private func pinMenu(_ site: Status.Site) -> some View {
        let nodeMajor = major(site.node ?? "")
        let nodeFile = site.nodePinnedHere ? fileName(site.nodeFile) : ".nvmrc"
        let canPinPHP = !(site.phpSource == .project && site.phpPinnedHere)
        let canPinNode = !nodeMajor.isEmpty && !(site.nodeSource == .project && site.nodePinnedHere)

        return Menu {
            if canPinPHP {
                Button("Pin PHP \(site.php) to Project…") {
                    confirm = ConfirmAction(
                        title: "Pin PHP \(site.php) to \(site.name)?", button: "Pin",
                        message: "Writes php=\(site.php) to .flockrc in \(site.path). Commit it, and everyone using Flock on this project gets PHP \(site.php). This changes a file in your project.",
                        run: { [name = site.name, php = site.php] client in try await client.pin(site: name, tool: "php", version: php) })
                }
            }
            if canPinNode {
                Button("Pin Node \(nodeMajor) to Project…") {
                    confirm = ConfirmAction(
                        title: "Pin Node \(nodeMajor) to \(site.name)?", button: "Pin",
                        message: "Writes \(nodeMajor) to \(nodeFile) in \(site.path). Commit it, and Flock, nvm and other Node tools use Node \(nodeMajor) for this project. This changes a file in your project.",
                        run: { [name = site.name] client in try await client.pin(site: name, tool: "node", version: nodeMajor) })
                }
            }
            if site.phpPinnedHere || site.nodePinnedHere {
                Divider()
            }
            if site.phpPinnedHere {
                Button("Unpin PHP (\(fileName(site.phpFile)))…") {
                    confirm = ConfirmAction(
                        title: "Unpin PHP from \(site.name)?", button: "Unpin",
                        message: "Removes the PHP version from \(site.phpFile). The site then uses Flock's default PHP. This changes files in your project.",
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "php") })
                }
            }
            if site.nodePinnedHere {
                Button("Unpin Node (\(fileName(site.nodeFile)))…") {
                    confirm = ConfirmAction(
                        title: "Unpin Node from \(site.name)?", button: "Unpin",
                        message: "Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in \(site.path). The site then uses Flock's default Node.js. This changes files in your project.",
                        run: { [name = site.name] client in try await client.unpin(site: name, tool: "node") })
                }
            }
        } label: {
            Image(systemName: "pin")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Pin versions to the project")
        .disabled(!canPinPHP && !canPinNode && !site.phpPinnedHere && !site.nodePinnedHere)
    }

    private func fileName(_ path: String) -> String { URL(fileURLWithPath: path).lastPathComponent }

    /// "22.11.0" → "22"; pins and isolations are majors.
    private func major(_ version: String) -> String { String(version.split(separator: ".").first ?? "") }
}

/// A change waiting for confirmation: a pin or unpin (it changes files in the project), or a removal.
private struct ConfirmAction: Identifiable, Sendable {
    let title: String
    let button: String
    var destructive = false
    let message: String
    let run: @Sendable (FlockClient) async throws -> Status
    var id: String { title }
}
