import SwiftUI

/// The streaming sheet for a bundled-CLI run: the CLI's output as it arrives, like the setup
/// window's install log. The CLI's own text is the story; the sheet only adds the running/failed
/// headline and the declined-password footer the caller supplies.
struct ProvisionSheet: View {
    /// The headline while the run is in flight, e.g. "Switching to .test…".
    let title: String
    /// The headline once the run ended short, e.g. "Switching to .test didn’t finish".
    let failedTitle: String
    /// The output as it streams in.
    let lines: [String]
    let running: Bool
    /// The run stopped at a declined password prompt.
    let declined: Bool
    /// The footer shown when the password was declined.
    let footer: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(running ? title : failedTitle).font(.headline)
            if running {
                ProgressView().progressViewStyle(.linear)
            }
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                            Text(line)
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        Color.clear.frame(height: 1).id("end")
                    }
                    .padding(10)
                }
                .frame(height: 200)
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .onChange(of: lines) { proxy.scrollTo("end", anchor: .bottom) }
            }
            if declined {
                Text(footer)
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Close") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(running)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}
