import AppKit
import FlockKit
import SwiftUI

/// Grouped list of services with a detail column, like Herd's Services pane.
struct ServicesPane: View {
    @Environment(AppModel.self) private var model
    private let selectionState = State<Service.ID?>(initialValue: nil)
    private var selection: Service.ID? {
        get { selectionState.wrappedValue }
        nonmutating set { selectionState.wrappedValue = newValue }
    }
    private let editingState = State<Service?>(initialValue: nil)
    private var editing: Service? {
        get { editingState.wrappedValue }
        nonmutating set { editingState.wrappedValue = newValue }
    }

    private static let categoryOrder = ["Core", "Database", "Cache & Queue", "Mail", "Debugging"]

    var body: some View {
        let store = model.settings
        let services = store.services
        let selected = services.first { $0.id == selection } ?? services.first

        VStack(alignment: .leading, spacing: 0) {
            PaneHeader(title: "Services", subtitle: "Databases, caches and the tools around your sites. Everything listens on 127.0.0.1 only.")

            HStack(spacing: 0) {
                List(selection: Binding<Service.ID?>(get: { selected?.id }, set: { selection = $0 })) {
                    ForEach(categories(of: services), id: \.self) { category in
                        Section(category) {
                            ForEach(services.filter { $0.category == category }) { service in
                                ServiceRow(service: service, busy: store.isBusy) {
                                    editing = service
                                } toggle: {
                                    let id = service.id
                                    Task {
                                        await store.performOnServices {
                                            service.running ? try await $0.stopService(id) : try await $0.startService(id)
                                        }
                                    }
                                }
                                .tag(service.id)
                            }
                        }
                    }
                }
                .listStyle(.inset)
                .frame(minWidth: 330)

                Divider()

                Group {
                    if let selected {
                        ServiceDetail(service: selected)
                    } else {
                        ContentUnavailableView("No services", systemImage: "server.rack")
                    }
                }
                .frame(width: 270)
                .frame(maxHeight: .infinity, alignment: .top)
            }
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.separator))
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
        }
        .sheet(item: editingState.projectedValue) { service in
            PortSheet(service: service) { port in
                let id = service.id
                Task { await store.performOnServices { try await $0.setServicePort(id, port: port) } }
            }
        }
    }

    private func categories(of services: [Service]) -> [String] {
        let present = Set(services.map(\.category))
        let ordered = Self.categoryOrder.filter(present.contains)
        return ordered + present.subtracting(ordered).sorted()
    }
}

private struct ServiceRow: View {
    let service: Service
    let busy: Bool
    let settings: () -> Void
    let toggle: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            StatusDot(on: service.running)
            VStack(alignment: .leading, spacing: 1) {
                Text(service.name).fontWeight(.medium)
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if service.installed {
                if service.hasConfigurablePort {
                    Button("Settings", action: settings)
                        .controlSize(.small)
                }
                if service.stoppable {
                    Button(service.running ? "Stop" : "Start", action: toggle)
                        .controlSize(.small)
                        .disabled(busy)
                }
            } else {
                Text("Not installed").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 3)
    }

    private var detail: String {
        var parts: [String] = []
        if !service.version.isEmpty {
            parts.append(service.version)
        }
        if service.port > 0 {
            parts.append("Port \(service.port)")
        }
        if !service.running {
            parts.append("Stopped")
        }
        return parts.joined(separator: " · ")
    }
}

private extension Service {
    /// The web server and DNS ports are tied to certificates and /etc/resolver; `flock install` changes them.
    var hasConfigurablePort: Bool { port > 0 && id != "web" && id != "dns" }
}

private struct ServiceDetail: View {
    let service: Service

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 8) {
                    StatusDot(on: service.running)
                    Text(service.name).font(.headline)
                    Spacer()
                }

                if service.isDatabase, let url = URL(string: service.url),
                   let tablePlus = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.tinyapp.TablePlus") {
                    row("TablePlus") {
                        Button("Open") {
                            NSWorkspace.shared.open([url], withApplicationAt: tablePlus, configuration: NSWorkspace.OpenConfiguration())
                        }
                    }
                }

                if !service.env.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Environment Variables").font(.subheadline.weight(.medium))
                        CodeBlock(text: service.env)
                    }
                }

                if !service.log.isEmpty {
                    row("Logs") {
                        Button("Open") {
                            let file = FlockPaths.root.appendingPathComponent("log/\(service.log).log")
                            NSWorkspace.shared.open(file)
                        }
                    }
                }

                if !service.url.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Connection").font(.subheadline.weight(.medium))
                        CodeBlock(text: service.url)
                    }
                }
            }
            .padding(14)
        }
    }

    private func row(_ title: String, @ViewBuilder trailing: () -> some View) -> some View {
        HStack {
            Text(title).font(.subheadline.weight(.medium))
            Spacer()
            trailing()
        }
    }
}

/// Changes a service's port.
private struct PortSheet: View {
    let service: Service
    let save: (Int) -> Void
    @Environment(\.dismiss) private var dismiss
    private let portState: State<String>

    init(service: Service, save: @escaping (Int) -> Void) {
        self.service = service
        self.save = save
        portState = State(initialValue: String(service.port))
    }

    private var port: Int? {
        Int(portState.wrappedValue).flatMap { (1024...65535).contains($0) ? $0 : nil }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("\(service.name) Settings").font(.headline)
            Form {
                TextField("Port", text: portState.projectedValue)
            }
            Text("Ports 1024–65535. Update the port in your projects' .env files too.")
                .font(.caption).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Save") {
                    if let port {
                        save(port)
                    }
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(port == nil || port == service.port)
            }
        }
        .padding(20)
        .frame(width: 340)
    }
}
