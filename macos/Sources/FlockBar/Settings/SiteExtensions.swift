import FlockKit
import SwiftUI

/// A site's PHP extensions: what composer.json and the locked packages require, what a Laravel
/// app's configuration shows it uses without requiring it, and whether Flock's PHP has each.
struct SiteExtensionsSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Status.Site

    private let reportState = State<ExtensionReport?>(initialValue: nil)
    private var report: ExtensionReport? {
        get { reportState.wrappedValue }
        nonmutating set { reportState.wrappedValue = newValue }
    }
    private let errorState = State<String?>(initialValue: nil)
    private var error: String? {
        get { errorState.wrappedValue }
        nonmutating set { errorState.wrappedValue = newValue }
    }
    /// The extension being added to composer.json.
    private let addingState = State<String?>(initialValue: nil)
    private var adding: String? {
        get { addingState.wrappedValue }
        nonmutating set { addingState.wrappedValue = newValue }
    }
    private let confirmState = State<PHPExtension?>(initialValue: nil)

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("PHP extensions of \(site.name)").font(.headline)
                    Text(subtitle).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }

            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.red)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let report {
                if report.extensions.isEmpty {
                    Text("The project requires no PHP extensions.").foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, minHeight: 80)
                } else {
                    List(report.extensions) { ext in
                        row(ext)
                    }
                    .listStyle(.inset)
                }
            } else if error == nil {
                ProgressView().frame(maxWidth: .infinity, minHeight: 80)
            }

            Text("Requiring an extension in composer.json makes composer install refuse a server without it, instead of the app failing there.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(width: 560, height: 420)
        .task { await load() }
        .confirmationDialog("Require ext-\(confirmState.wrappedValue?.name ?? "") in composer.json?",
                            isPresented: Binding(get: { confirmState.wrappedValue != nil }, set: { if !$0 { confirmState.wrappedValue = nil } }),
                            presenting: confirmState.wrappedValue) { ext in
            Button("Require") { add(ext) }
            Button("Cancel", role: .cancel) {}
        } message: { ext in
            Text("Runs composer require ext-\(ext.name):* in \(site.path), which changes composer.json and composer.lock. Commit them with the project.")
        }
    }

    private var subtitle: String {
        guard let report else { return site.path }
        return report.laravel ? "PHP \(report.php) · Laravel app" : "PHP \(report.php)"
    }

    private func row(_ ext: PHPExtension) -> some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: icon(ext))
                .foregroundStyle(ext.available ? (ext.declared ? Color.green : Color.orange) : Color.red)
                .frame(width: 16)
            VStack(alignment: .leading, spacing: 2) {
                Text(ext.name).font(.body.monospaced())
                Text(detail(ext)).font(.caption).foregroundStyle(.secondary)
                    .lineLimit(2)
                    .help(detail(ext))
            }
            Spacer(minLength: 8)
            if adding == ext.name {
                ProgressView().controlSize(.small)
            } else if !ext.declared {
                Button("Add to composer.json") { confirmState.wrappedValue = ext }
                    .disabled(adding != nil)
            }
        }
        .padding(.vertical, 2)
    }

    private func icon(_ ext: PHPExtension) -> String {
        if !ext.available { return "xmark.circle.fill" }
        return ext.declared ? "checkmark.circle.fill" : "exclamationmark.circle.fill"
    }

    private func detail(_ ext: PHPExtension) -> String {
        var text = ext.declared ? "Required by \(ext.requiredBy.joined(separator: ", "))" : "Not required, but used: \(ext.hint)"
        if !ext.available {
            text += " — Flock's PHP \(report?.php ?? "") doesn't include it"
        }
        return text
    }

    private func load() async {
        switch await model.settings.siteExtensions(site.name) {
        case let .success(fresh):
            report = fresh
            error = nil
        case let .failure(failure): error = failure.localizedDescription
        }
    }

    private func add(_ ext: PHPExtension) {
        adding = ext.name
        Task {
            switch await model.settings.requireExtension(site: site.name, name: ext.name) {
            case let .success(fresh):
                report = fresh
                error = nil
            case let .failure(failure): error = failure.localizedDescription
            }
            adding = nil
        }
    }
}
