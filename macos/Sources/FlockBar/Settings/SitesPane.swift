import AppKit
import FlockKit
import SwiftUI

/// Sites with their PHP and Node.js versions. Each picker offers the project's pinned version (or
/// the default) first, then the installed versions, which isolate the site on this machine; the
/// "…" menu pins a version into the project or removes it again.
struct SitesPane: View {
    @Environment(AppModel.self) private var model
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

            if let dns = model.status?.dns, !dns.wildcard, !dns.wildcardProblem.isEmpty, !sites.isEmpty {
                Label("Subdomains only work when you add them to a site (\(Image(systemName: "network")) → Add Domain…): \(dns.wildcardProblem).",
                      systemImage: "info.circle.fill")
                    .font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 24)
                    .padding(.top, 8)
            }

            Table(sites, selection: selectionState.projectedValue) {
                TableColumn("Site") { site in
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 4) {
                            Text(site.name).fontWeight(.medium)
                            if site.parked {
                                Badge(text: "Project folder", color: .secondary)
                            }
                        }
                        if let problem = site.problem {
                            Label(problem, systemImage: "exclamationmark.triangle.fill")
                                .font(.caption).foregroundStyle(.orange)
                        } else {
                            Text(site.url ?? site.domain).font(.caption).foregroundStyle(.secondary)
                                .lineLimit(1).truncationMode(.middle)
                            Text(site.path).font(.caption).foregroundStyle(.tertiary)
                                .lineLimit(1).truncationMode(.middle)
                                .help(site.path)
                        }
                        ForEach(site.domains) { domain in
                            if let problem = domain.problem {
                                Label("\(domain.host): \(problem)", systemImage: "exclamationmark.triangle.fill")
                                    .font(.caption).foregroundStyle(.orange)
                            } else if let url = domain.url.flatMap(URL.init(string:)) {
                                Link(domain.alias ? "\(domain.host) (alias)" : domain.host, destination: url)
                                    .font(.caption)
                            }
                        }
                        hints(site)
                    }
                    .padding(.vertical, 2)
                }
                .width(min: 200, ideal: 280)

                TableColumn("PHP") { site in
                    versionPicker(label: "PHP", current: site.php, source: site.phpSource, pinned: site.phpPinned, file: site.phpFile,
                                  defaultVersion: store.config?.defaultPHP ?? "", installed: phpVersions) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: version)) } }
                    }
                }
                .width(min: 150, ideal: 180)

                TableColumn("Node") { site in
                    versionPicker(label: "Node", current: major(site.node ?? ""), source: site.nodeSource, pinned: major(site.nodePinned), file: site.nodeFile,
                                  defaultVersion: store.config?.defaultNode ?? "", installed: nodeMajors) { version in
                        Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(node: version)) } }
                    }
                }
                .width(min: 150, ideal: 180)

                TableColumn("HTTPS") { site in
                    Toggle("HTTPS", isOn: Binding(
                        get: { site.secured },
                        set: { on in Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(secured: on)) } } }
                    ))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .controlSize(.small)
                }
                .width(60)

                TableColumn("") { site in
                    HStack(spacing: 8) {
                        Button { model.open(site) } label: { Image(systemName: "safari") }
                            .help("Open \(site.url ?? site.domain)")
                            .disabled(site.url == nil)
                        Button { reveal(site.path) } label: { Image(systemName: "folder") }
                            .help("Show in Finder")
                        domainMenu(site)
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
                .width(150)
            }
            .tableStyle(.inset)
            .padding(.horizontal, 20)
            .padding(.top, 12)
            .overlay {
                if sites.isEmpty {
                    ContentUnavailableView("No sites", systemImage: "globe",
                                           description: Text("Add a single site here, or add a project folder in General."))
                }
            }

            Text("A version picked here applies on this machine only. To set it for everyone working on a project, pin it (…): PHP goes into .flockrc, Node.js into .nvmrc. Projects that pin a version use it automatically.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, 24)
                .padding(.vertical, 10)
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

    private func domainMenu(_ site: Status.Site) -> some View {
        Menu {
            Button("Add Domain…") { adding = site.name }
            if !site.domains.isEmpty {
                Divider()
                ForEach(site.domains) { domain in
                    Button("Remove \(domain.host)…") {
                        confirm = ConfirmAction(
                            title: "Remove \(domain.host)?", button: "Remove", destructive: true,
                            message: "\(domain.host) is no longer served by \(site.name).",
                            run: { [name = site.name, entry = domain.entry] client in try await client.removeDomain(site: name, domain: entry) })
                    }
                }
            }
        } label: {
            Image(systemName: "network")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Extra domains: subdomains and aliases")
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
