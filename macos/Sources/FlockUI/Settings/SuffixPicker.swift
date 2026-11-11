import FlockKit
import Observation
import SwiftUI

/// The picker tags that aren't suffixes: shared by the general and the per-site pickers.
enum SuffixValue {
    /// The row follows the global suffix.
    static let inherit = "inherit"
    /// The custom field holds the suffix.
    static let custom = "custom"
}

/// The drafted suffix, the daemon's live verdict and the rules around them; one instance per
/// picker (General's global picker, a site's Domain row, the add-domain sheet).
@MainActor
@Observable
final class SuffixCheckModel {
    /// The domain picked in the menu; nil keeps the selection on the domain in the config.
    var draft: String?
    /// The text in the custom field, shown when Custom… is picked.
    var custom = ""
    /// The daemon's verdict on the drafted domain; nil while nothing has been checked.
    var check: TLDCheck?
    /// Why the last check couldn't be made (the daemon didn't answer); the last verdict stays.
    var checkNote: String?

    /// The add-domain sheet starts on Inherit; the others fall back to the config's domain.
    init(draft: String? = nil) {
        self.draft = draft
    }

    /// The domain to check and apply: the picked preset, else the trimmed custom text.
    func candidate(current: String) -> String {
        draft ?? custom.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Apply is enabled only on a checked, acceptable domain the config doesn't already have.
    func canApply(current: String) -> Bool {
        check.map { $0.ok && $0.normalized != current } ?? false
    }

    /// What the menu shows: the draft, else the preset of the current domain, else Custom….
    func selection(current: String) -> String {
        if let draft { return draft }
        return custom.isEmpty && SuffixPicker.presets.contains(current) ? current : SuffixValue.custom
    }

    /// A menu choice: a preset becomes the draft; Custom… clears the draft and prefills the field.
    func pick(_ tag: String, current: String) {
        if tag == SuffixValue.custom {
            draft = nil
            custom = current
        } else {
            draft = tag
        }
    }

    /// A domain the menu doesn't offer sits in the custom field to be edited.
    func prefill(current: String) {
        if draft == nil, custom.isEmpty, !SuffixPicker.presets.contains(current) {
            custom = current
        }
    }

    /// Back to a clean picker: the applied domain is the config's, so the drafts and verdicts go.
    func reset() {
        draft = nil
        custom = ""
        check = nil
        checkNote = nil
    }

    /// The 300 ms debounce: checks the drafted suffix once typing settles. The picker restarts
    /// it on every change (`.task(id:)`), so a newer candidate cancels this run and late
    /// answers are dropped.
    func runCheck(store: SettingsStore, current: String) async {
        let candidate = candidate(current: current)
        guard !candidate.isEmpty else {
            // Nothing drafted (an emptied field): no verdict to show.
            check = nil
            checkNote = nil
            return
        }
        try? await Task.sleep(for: .milliseconds(300))
        if Task.isCancelled { return }
        switch await store.checkTLD(candidate) {
        case .success(let verdict):
            // A newer candidate cancelled this task; drop the late answer.
            if Task.isCancelled { return }
            check = verdict
            checkNote = nil
        case .failure:
            if Task.isCancelled { return }
            checkNote = check == nil
                ? "Couldn’t check with the daemon; Apply stays disabled until the daemon answers."
                : "Couldn’t check with the daemon; the verdict shown is the last one."
        }
    }
}

/// The shared suffix picker: the domain menu and the custom field, with the live check running
/// on the draft. The caller adds its own Apply/Add button next to it; the verdict lines are
/// `SuffixFeedback`.
struct SuffixPicker: View {
    let store: SettingsStore
    let suffixModel: SuffixCheckModel
    /// The suffix in the config today; the selection falls back to it and Apply ignores it.
    let current: String
    /// Adds the leading Inherit row (the row follows `current`'s role as the global suffix).
    var allowInherit = false

    /// The domains the menu offers; the config can hold any other one, which the custom field edits.
    static let presets = ["test", "loc", "local"]

    var body: some View {
        @Bindable var model = suffixModel
        VStack(alignment: .trailing, spacing: 8) {
            Picker("Domain", selection: Binding(
                get: { model.selection(current: current) },
                set: { model.pick($0, current: current) }
            )) {
                if allowInherit {
                    Text("Inherit (.\(current))").tag(SuffixValue.inherit)
                }
                Text(".test").tag("test")
                Text(".loc").tag("loc")
                Text(".local").tag("local")
                Text("Custom…").tag(SuffixValue.custom)
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .fixedSize()
            if model.selection(current: current) == SuffixValue.custom {
                TextField("domain", text: $model.custom)
                    .labelsHidden()
                    .frame(width: 160)
                    .multilineTextAlignment(.trailing)
            }
        }
        .task(id: suffixModel.candidate(current: current)) {
            if allowInherit, suffixModel.candidate(current: current) == SuffixValue.inherit {
                // The row follows the global suffix; there is no suffix to check.
                suffixModel.check = nil
                suffixModel.checkNote = nil
                return
            }
            await suffixModel.runCheck(store: store, current: current)
        }
        // A domain the menu doesn't offer sits in the custom field to be edited.
        .onChange(of: current, initial: true) { _, new in
            model.prefill(current: new)
        }
    }
}

/// The picker's verdict lines for a section footer: the normalized suffix, warnings, errors, and
/// why a check couldn't be made.
struct SuffixFeedback: View {
    let suffixModel: SuffixCheckModel
    let candidate: String

    var body: some View {
        Group {
            if let check = suffixModel.check {
                if check.ok, check.normalized != candidate {
                    Text("→ .\(check.normalized)").font(.caption).foregroundStyle(.green)
                }
                if !check.warning.isEmpty {
                    Text(check.warning).font(.caption).foregroundStyle(.orange)
                }
                if !check.error.isEmpty {
                    Text(check.error).font(.caption).foregroundStyle(.red)
                }
            }
            if let checkNote = suffixModel.checkNote {
                Text(checkNote).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}
