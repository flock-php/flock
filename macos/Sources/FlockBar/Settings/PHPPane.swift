import AppKit
import FlockKit
import SwiftUI

struct PHPPane: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let store = model.settings

        VStack(spacing: 0) {
            PaneHeader(title: "PHP", subtitle: "Every installed version runs its own PHP-FPM pool. Sites use the default unless pinned to a version.")
            Form {
                if let config = store.config {
                    Section("Installed versions") {
                        if config.php.isEmpty {
                            Text("No PHP installed yet.").foregroundStyle(.secondary)
                        }
                        ForEach(config.php) { php in
                            HStack(spacing: 10) {
                                SettingsIcon(symbol: "chevron.left.forwardslash.chevron.right", color: .indigo, size: 26)
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text("PHP \(php.version)").fontWeight(.medium)
                                        if php.version == config.defaultPHP {
                                            Badge(text: "Default")
                                        }
                                    }
                                    Text(php.fullVersion + (php.xdebugAvailable ? " · Xdebug available" : ""))
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Button("php.ini") {
                                    NSWorkspace.shared.open(URL(fileURLWithPath: php.ini))
                                }
                                .disabled(php.ini.isEmpty)
                                .help(php.ini)
                                if php.version != config.defaultPHP {
                                    Button("Make Default") {
                                        Task { await store.perform { _ = try await $0.updateSettings(SettingsChange(defaultPHP: php.version)) } }
                                    }
                                }
                            }
                            .padding(.vertical, 2)
                        }
                    }

                    Section("Install another version") {
                        Text("Build PHP with `build/php/build.sh <version>`, then run `flock php install --from build/php/out/<version>`.")
                            .font(.callout).foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
    }
}
