//! The shared suffix picker (spec 2): the domain menu, the custom field and the daemon's live
//! verdict on their draft. Tasks 3-5 embed the three widgets next to their own Apply button:
//! General's global domain switch, a site's Domain row and the add-alias dialog.
//!
//! The threading contract: the picker never touches the network itself and never blocks the UI
//! thread. It calls the host's blocking `check` closure (normally `move |draft| Client.check_tld(&draft)`,
//! a `ureq` call) on a `gio::spawn_blocking` thread once 300 ms after the draft settled — the
//! panes' own pattern for daemon calls (services.rs `run_services`, general.rs's beta switch) —
//! and applies the answer back on the main context. Every draft change bumps a sequence counter,
//! and a verdict that no longer matches it updates neither the label nor the draft nor the host's
//! hook (Review Focus 2).
//!
//! The macOS app (Settings/SuffixPicker.swift) is the behavioral spec for the states, the preset
//! order and the feedback copy; the draft's tri-state and the selection commands mirror the
//! Windows control's `DraftNormalized`, `SelectedTag` and `Select`
//! (windows/Flock.Tray/Controls/SuffixPickerControl.xaml.cs).

// The General pane (plan 3d task 3) builds pickers and calls draft/can_apply/set_changed; the
// selection API the sites dialogs use (set_selected, set_custom, set_selected_inherit,
// is_inherit) waits for task 4, and the dead_code lint can't see unit-test uses (same doctrine
// as the allows in api.rs — drop when task 4 builds them).
#![allow(dead_code)]

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::sync::Arc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Error, TldCheck};

/// The domains the menu offers, as the daemon stores them: no leading dot, alphabetical — the
/// macOS order (`SuffixPicker.presets`); Windows keeps its legacy order as-is.
pub const PRESETS: [&str; 4] = ["example", "internal", "loc", "test"];

/// The picked row's tag while it stands for following the global suffix (macOS SuffixValue.inherit,
/// Windows InheritTag).
pub const INHERIT: &str = "inherit";

/// The picked row's tag while the custom field holds the domain (macOS SuffixValue.custom,
/// Windows CustomTag).
pub const CUSTOM: &str = "custom";

/// The check waits this long after every draft change (macOS .task(id:), Windows DebounceMilliseconds).
const DEBOUNCE: Duration = Duration::from_millis(300);

/// The feedback line's color classes: `error` as in `versions::detail`, `warning` and `success` as
/// on rows and labels across the panes; notes sit on `dim-label`.
const COLORS: [&str; 4] = ["error", "warning", "success", "dim-label"];

/// What the picker is drafting.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Draft {
    /// The row follows the global suffix (picked and allowed); there is nothing to check.
    Inherit,
    /// A domain the daemon accepted; holds its `normalized` (no leading dot) answer, never the
    /// raw text — argv takes only normalized values (plan 3d Global Constraints).
    Custom(String),
}

/// The menu's rows in selection order, as (tag, label): the optional leading Inherit row follows
/// the current domain, then the presets, then Custom… (macOS's Picker, Windows's Rebuild).
fn entries(current: &str, allow_inherit: bool) -> Vec<(String, String)> {
    let mut rows = Vec::with_capacity(PRESETS.len() + 2);
    if allow_inherit {
        rows.push((INHERIT.to_owned(), format!("Inherit (.{current})")));
    }
    for preset in PRESETS {
        rows.push((preset.to_owned(), format!(".{preset}")));
    }
    rows.push((CUSTOM.to_owned(), "Custom…".to_owned()));
    rows
}

/// The domain to check and apply: the picked preset, else the trimmed custom text, else nothing
/// (an Inherit row or an emptied field); the RAW text goes to the check, the daemon normalizes it
/// (macOS candidate(), Windows Candidate()).
fn candidate(tag: Option<&str>, custom: &str) -> String {
    match tag {
        None | Some(INHERIT) => String::new(),
        Some(CUSTOM) => custom.trim().to_owned(),
        Some(preset) => preset.to_owned(),
    }
}

/// The tri-state draft (Windows DraftNormalized), pinned by Review Focus 2: a verdict only stands
/// for the candidate it ran on, so a verdict for an older draft — one that dropped in late or one
/// the newer draft simply hasn't re-checked yet — can never make a draft.
fn resolve_draft(tag: Option<&str>, custom: &str, checked: Option<(&str, &TldCheck)>) -> Option<Draft> {
    if tag == Some(INHERIT) {
        return Some(Draft::Inherit);
    }
    let want = candidate(tag, custom);
    if want.is_empty() {
        return None;
    }
    match checked {
        Some((for_candidate, verdict)) if for_candidate == want && verdict.ok => Some(Draft::Custom(verdict.normalized.clone())),
        _ => None,
    }
}

/// The state the widget's signals move: a cloneable handle, so the host builds one, hands out
/// `row()`, `entry()` and `verdict()` to its group and reads the draft whenever the hook rings.
struct Inner {
    combo: adw::ComboRow,
    entry: adw::EntryRow,
    verdict: gtk::Label,
    /// The suffix in the config today (no leading dot): the Inherit row's label and what Apply
    /// compares against.
    current: String,
    allow_inherit: bool,
    /// The picked row's tags, parallel to the ComboRow's model (labels differ from tags).
    tags: Vec<String>,
    /// The host's blocking verdict call, run under `gio::spawn_blocking` (see the module docs).
    check: Arc<dyn Fn(String) -> Result<TldCheck, Error> + Send + Sync>,
    /// Bumped on every draft change; pending and in-flight checks drop once it moved on.
    sequence: Cell<u64>,
    /// The last verdict and the candidate it ran on; it keeps standing during the next debounce
    /// (macOS "the verdict shown is the last one"), and `resolve_draft` binds it to its candidate.
    checked: RefCell<Option<(String, TldCheck)>>,
    /// Why the last check couldn't run (macOS checkNote); cleared by the next answer.
    note: RefCell<Option<String>>,
    /// The host's Apply-gating hook (Windows Changed).
    changed: RefCell<Option<Rc<dyn Fn()>>>,
}

impl Inner {
    fn index_of(&self, tag: &str) -> Option<u32> {
        self.tags.iter().position(|t| t == tag).map(|index| index as u32)
    }

    /// The picked row's tag; None only with an empty menu, which `entries` never builds.
    fn tag(&self) -> Option<&str> {
        self.tags.get(self.combo.selected() as usize).map(String::as_str)
    }

    /// A selection or a keystroke: reveal the field, settle the verdict state, restart the
    /// debounce and ring the host.
    fn on_change(self: &Rc<Self>) {
        self.entry.set_visible(self.tag() == Some(CUSTOM));
        let draft = candidate(self.tag(), &self.entry.text());
        // A newer draft wins over any check still waiting or in flight (the sequence stale-drop).
        self.sequence.set(self.sequence.get() + 1);
        let sequence = self.sequence.get();
        if draft.is_empty() {
            // Nothing drafted (Inherit, or an emptied field): no verdict to show.
            *self.checked.borrow_mut() = None;
            *self.note.borrow_mut() = None;
            self.draw();
            self.raise_changed();
            return;
        }
        let (this, check) = (self.clone(), self.check.clone());
        glib::timeout_add_local_once(DEBOUNCE, move || {
            if this.sequence.get() != sequence {
                return; // superseded while it waited
            }
            glib::spawn_future_local(async move {
                // The blocking daemon call leaves the UI thread; its answer comes back here.
                let for_candidate = draft.clone();
                let result = gio::spawn_blocking(move || check(draft)).await.expect("tld check request");
                if this.sequence.get() != sequence {
                    return; // the stale branch updates label, draft and hook never at all
                }
                *this.note.borrow_mut() = None;
                match result {
                    Ok(verdict) => *this.checked.borrow_mut() = Some((for_candidate, verdict)),
                    Err(_) => {
                        // The daemon didn't answer; the last verdict stays in place (macOS checkNote).
                        *this.note.borrow_mut() = Some(
                            match &*this.checked.borrow() {
                                Some(_) => "Couldn’t check with the daemon; the verdict shown is the last one.",
                                None => "Couldn’t check with the daemon; Apply stays disabled until the daemon answers.",
                            }
                            .to_owned(),
                        );
                    }
                }
                this.draw();
                this.raise_changed();
            });
        });
    }

    /// Draws the feedback line (macOS SuffixFeedback, Windows SetCheck, in a single label): the
    /// daemon's error, its warning, the normalized domain when the daemon changed it, and why a
    /// check couldn't run — one color, the one that matters most, on `error`/`warning`/`success`/
    /// `dim-label`; nothing to say hides the line.
    fn draw(&self) {
        let checked = self.checked.borrow();
        let note = self.note.borrow();
        let mut parts: Vec<String> = Vec::new();
        let mut color = "";
        if let Some((for_candidate, verdict)) = &*checked {
            if verdict.ok {
                if !verdict.normalized.is_empty() && verdict.normalized != *for_candidate {
                    parts.push(format!("→ .{}", verdict.normalized));
                    color = "success";
                }
                if !verdict.warning.is_empty() {
                    parts.push(verdict.warning.clone());
                    color = "warning";
                }
            } else if !verdict.error.is_empty() {
                parts.push(verdict.error.clone());
                color = "error";
            }
        }
        if let Some(message) = &*note {
            parts.push(message.clone());
            color = "dim-label";
        }
        let text = parts.join(" · ");
        for class in COLORS {
            self.verdict.remove_css_class(class);
        }
        self.verdict.set_label(&text);
        if !color.is_empty() {
            self.verdict.add_css_class(color);
        }
        self.verdict.set_visible(!text.is_empty());
    }

    fn raise_changed(&self) {
        // Cloned out of the RefCell first: a hook may read the picker, just never set another one.
        let changed = self.changed.borrow().clone();
        if let Some(changed) = changed {
            changed();
        }
    }
}

/// The shared suffix picker: a combo of Inherit (where allowed) plus the preset domains plus
/// Custom…, an entry the Custom… row reveals, and a feedback line with the daemon's live verdict
/// on the draft. A plain struct of cheap clones over one set of widgets; build it, add `row()`,
/// `entry()` and `verdict()` to the caller's group, and gate the caller's Apply on `can_apply`
/// and `draft`, refreshed by `set_changed`.
#[derive(Clone)]
pub struct SuffixPicker {
    inner: Rc<Inner>,
}

impl SuffixPicker {
    /// A picker over the domain `current` (as the daemon stores it: no leading dot — the Inherit
    /// row is labelled with it and Apply compares against it), adding the leading Inherit row iff
    /// `allow_inherit`. The check is a constructor argument, not a setter: the picker cannot reach
    /// a verdict state at all without it, so there is no half-wired window. The closure is called
    /// on a `gio::spawn_blocking` thread with the raw draft and returns the daemon's answer; it
    /// must be a blocking call like `move |draft| Client.check_tld(&draft)` (never a UI-thread
    /// hop — see the module docs for the contract).
    ///
    /// Seeding follows macOS selection()/prefill() and Windows SeedTag: an inherit-capable picker
    /// starts on Inherit; otherwise the picker opens on the preset of `current`, and a domain the
    /// menu doesn't offer sits in the Custom… field to be edited.
    pub fn new(current: &str, allow_inherit: bool, check: impl Fn(String) -> Result<TldCheck, Error> + Send + Sync + 'static) -> Self {
        let rows = entries(current, allow_inherit);
        let labels: Vec<&str> = rows.iter().map(|(_, label)| label.as_str()).collect();
        let model = gtk::StringList::new(&labels);
        let inner = Rc::new(Inner {
            combo: adw::ComboRow::builder().model(&model).build(),
            entry: adw::EntryRow::builder().title("domain").build(),
            verdict: gtk::Label::builder().wrap(true).xalign(0.0).visible(false).build(),
            current: current.to_owned(),
            allow_inherit,
            tags: rows.into_iter().map(|(tag, _)| tag).collect(),
            check: Arc::new(check),
            sequence: Cell::new(0),
            checked: RefCell::default(),
            note: RefCell::default(),
            changed: RefCell::default(),
        });
        if let Some(index) = inner.allow_inherit.then(|| inner.index_of(INHERIT)).flatten() {
            inner.combo.set_selected(index);
        } else if let Some(index) = inner.index_of(&inner.current) {
            inner.combo.set_selected(index);
        } else {
            inner.entry.set_text(&inner.current);
            if let Some(index) = inner.index_of(CUSTOM) {
                inner.combo.set_selected(index);
            }
        }
        // Wired after the seed so it runs once, below, with everything in place.
        let (picked, typed) = (inner.clone(), inner.clone());
        inner.combo.connect_selected_notify(move |_| {
            // macOS pick(): Custom… starts the field on the current domain, to be edited.
            if picked.tag() == Some(CUSTOM) {
                picked.entry.set_text(&picked.current);
            }
            picked.on_change();
        });
        inner.entry.connect_changed(move |_| typed.on_change());
        inner.on_change();
        Self { inner }
    }

    /// The domain menu. Embed it in the caller's group (it has no title of its own, like the
    /// macOS picker).
    pub fn row(&self) -> adw::ComboRow {
        self.inner.combo.clone()
    }

    /// The field a custom domain is typed in, a sibling of `row()` for the same group; the picker
    /// shows it only while Custom… is picked (hidden widgets hold no place in a PreferencesGroup).
    pub fn entry(&self) -> adw::EntryRow {
        self.inner.entry.clone()
    }

    /// The feedback line to place under the two rows: `→ .normalized`, the daemon's warning or
    /// error, or why a check couldn't run — colored through css classes, hidden when empty.
    pub fn verdict(&self) -> gtk::Label {
        self.inner.verdict.clone()
    }

    /// The draft to apply (Windows DraftNormalized, in tri-state): `None` while nothing is
    /// checkable yet (an emptied field, a pending check, or a draft no accepted verdict stands
    /// for), `Some(Inherit)` while the Inherit row is picked, and `Some(Custom(normalized))` once
    /// the daemon accepted exactly this draft.
    pub fn draft(&self) -> Option<Draft> {
        let checked = self.inner.checked.borrow();
        resolve_draft(
            self.inner.tag(),
            &self.inner.entry.text(),
            checked.as_ref().map(|(for_candidate, verdict)| (for_candidate.as_str(), verdict)),
        )
    }

    /// macOS canApply / Windows CanApply for the suffix drafts: true only on a draft the daemon
    /// checked and that differs from the domain in the config today (the bare-suffix equality).
    /// With Inherit picked this is false by design — an `--inherit` run has no verdict to wait
    /// for, so the host owns that rule (Task 4: enabled iff the site has an explicit `site_tld`).
    pub fn can_apply(&self) -> bool {
        matches!(self.draft(), Some(Draft::Custom(normalized)) if normalized != self.inner.current)
    }

    /// True while the draft sits on the Inherit row (Windows IsInherit): there is no suffix to
    /// check, and the alias auto-install chain of Task 4 reads the draft through this.
    pub fn is_inherit(&self) -> bool {
        self.draft() == Some(Draft::Inherit)
    }

    /// The host's Apply-gating hook (Windows Changed): raised when the selection, the field or a
    /// current verdict moved — never from a dropped stale one. Read `can_apply` and `draft` from it.
    pub fn set_changed(&self, f: impl Fn() + 'static) {
        *self.inner.changed.borrow_mut() = Some(Rc::new(f));
    }

    /// Pick the Inherit row (Windows Select(InheritTag)): Task 4's add-alias dialog starts there.
    /// A no-op when the picker was built without inherit.
    pub fn set_selected_inherit(&self) {
        self.set_selected(INHERIT);
    }

    /// Start the picker on a row: [`INHERIT`], one of [`PRESETS`], or [`CUSTOM`]; unknown tags do
    /// nothing (Windows Select). Picking Custom… puts the current domain in the field, as on macOS.
    pub fn set_selected(&self, tag: &str) {
        if let Some(index) = self.inner.index_of(tag) {
            self.inner.combo.set_selected(index);
        }
    }

    /// Pick Custom… and put `text` in the field: Task 4 seeds a site's explicit `site_tld`, which
    /// the menu may not offer, the way the seed does for the domain in the config.
    pub fn set_custom(&self, text: &str) {
        self.set_selected(CUSTOM);
        self.inner.entry.set_text(text);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn verdict(ok: bool, normalized: &str, warning: &str, error: &str) -> TldCheck {
        TldCheck { ok, normalized: normalized.to_owned(), warning: warning.to_owned(), error: error.to_owned() }
    }

    fn tags(current: &str, allow_inherit: bool) -> Vec<String> {
        entries(current, allow_inherit).into_iter().map(|(tag, _)| tag).collect()
    }

    fn labels(rows: &[(String, String)]) -> Vec<&str> {
        rows.iter().map(|(_, label)| label.as_str()).collect()
    }

    #[test]
    fn entries_are_alphabetical_like_macos_with_inherit_first_and_custom_last() {
        let rows = entries("test", true);
        assert_eq!(labels(&rows), ["Inherit (.test)", ".example", ".internal", ".loc", ".test", "Custom…"]);
        assert_eq!(tags("test", false), ["example", "internal", "loc", "test", "custom"]);
        assert_eq!(entries("test", true)[0].0, INHERIT);
        assert_eq!(entries("test", true).last().unwrap().0, CUSTOM);
    }

    #[test]
    fn the_inherit_row_follows_the_current_domain() {
        assert_eq!(entries("loc", true)[0].1, "Inherit (.loc)");
    }

    #[test]
    fn the_candidate_is_the_raw_text_of_a_suffix_draft_only() {
        assert_eq!(candidate(None, "junk"), "");
        assert_eq!(candidate(Some(INHERIT), "junk"), "");
        assert_eq!(candidate(Some("test"), ""), "test");
        // Trimmed but otherwise raw: the daemon normalizes, the app doesn't.
        assert_eq!(candidate(Some(CUSTOM), "  .Test  "), ".Test");
        assert_eq!(candidate(Some(CUSTOM), "   "), "");
    }

    #[test]
    fn inherit_is_always_a_draft_and_needs_no_check() {
        assert_eq!(resolve_draft(Some(INHERIT), "", None), Some(Draft::Inherit));
        let checked = ("test", &verdict(true, "test", "", ""));
        assert_eq!(resolve_draft(Some(INHERIT), "", Some(checked)), Some(Draft::Inherit));
    }

    #[test]
    fn a_draft_needs_an_accepted_verdict_for_exactly_the_current_candidate() {
        let ok = verdict(true, "test", "", "");
        assert_eq!(resolve_draft(Some("test"), "", Some(("test", &ok))), Some(Draft::Custom("test".to_owned())));
        assert_eq!(resolve_draft(Some(CUSTOM), "te st", Some(("te st", &ok))), Some(Draft::Custom("test".to_owned())));
        // Pending, failed-to-arrive, empty or rejected: no draft.
        assert_eq!(resolve_draft(Some("test"), "", None), None);
        assert_eq!(resolve_draft(Some(CUSTOM), "x", None), None);
        assert_eq!(resolve_draft(Some(CUSTOM), "  ", Some(("x", &ok))), None);
        assert_eq!(resolve_draft(Some(CUSTOM), "x", Some(("x", &verdict(false, "", "", "nope")))), None);
    }

    #[test]
    fn a_verdict_for_an_older_draft_never_makes_one() {
        // Review Focus 2: the draft moved on, its check hasn't come back (or came back late).
        let old = verdict(true, "loc", "", "");
        assert_eq!(resolve_draft(Some("test"), "", Some(("loc", &old))), None);
        assert_eq!(resolve_draft(Some(CUSTOM), "test", Some(("loc", &old))), None);
    }
}
