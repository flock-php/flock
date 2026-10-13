import AppKit
import FlockKit
import SwiftUI

struct SitesPane: View {
    @Environment(AppModel.self) private var model
    private let selectionState = State<Status.Site.ID?>(initialValue: nil)

    var body: some View {
        let store = model.settings
        let sites = model.status?.sites ?? []
        let versions = store.config?.php.map(\.version) ?? []
        let defaultPHP = store.config?.defaultPHP ?? ""

        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .bottom) {
                PaneHeader(title: "Sites", subtitle: "Linked folders and everything inside parked folders.")
                Button("Link Folder…") {
                    for url in SettingsStore.chooseDirectories(prompt: "Link", multiple: false) {
                        Task { await store.perform { _ = try await $0.linkSite(path: url.path) } }
                    }
                }
                .padding(.trailing, 20)
                .padding(.bottom, 6)
            }

            Table(sites, selection: selectionState.projectedValue) {
                TableColumn("Site") { site in
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 4) {
                            Text(site.name).fontWeight(.medium)
                            if site.parked {
                                Badge(text: "Parked", color: .secondary)
                            }
                        }
                        if let problem = site.problem {
                            Label(problem, systemImage: "exclamationmark.triangle.fill")
                                .font(.caption).foregroundStyle(.orange)
                        } else {
                            Text(site.path).font(.caption).foregroundStyle(.secondary)
                                .lineLimit(1).truncationMode(.middle)
                        }
                    }
                    .padding(.vertical, 2)
                }
                .width(min: 170, ideal: 230)

                TableColumn("PHP") { site in
                    Picker("PHP", selection: Binding(
                        get: { site.php },
                        set: { php in
                            let pin = php == defaultPHP ? "" : php
                            Task { await store.perform { _ = try await $0.updateSite(site.name, SiteChange(php: pin)) } }
                        }
                    )) {
                        ForEach(versions.contains(site.php) ? versions : versions + [site.php], id: \.self) { version in
                            Text(version == defaultPHP ? "\(version) (default)" : version).tag(version)
                        }
                    }
                    .labelsHidden()
                    .fixedSize()
                }
                .width(110)

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
                        if !site.parked {
                            Button {
                                Task { await store.perform { _ = try await $0.unlinkSite(site.name) } }
                            } label: { Image(systemName: "minus.circle") }
                                .help("Unlink")
                        }
                    }
                    .buttonStyle(.borderless)
                }
                .width(100)
            }
            .tableStyle(.inset)
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
            .overlay {
                if sites.isEmpty {
                    ContentUnavailableView("No sites", systemImage: "globe",
                                           description: Text("Link a folder or park a folder in General."))
                }
            }
        }
    }
}
