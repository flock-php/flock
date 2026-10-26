//! Dumps: capturing dump() output, queries, logs, views and jobs, and where files open.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;

use super::common::{PaneView, group, open_row, report, switch_row};
use super::{Data, Pane};
use crate::api::{EVENT_KINDS, SettingsChange, Status};
use crate::model::AppModel;
use crate::prefs::{self, EDITORS};

pub struct DumpsPane {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    shown: RefCell<Option<Status>>,
}

impl DumpsPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        let view = PaneView::new(
            "Send dump() and dd() output, queries, logs, views and jobs to Flock instead of the page or terminal — no changes to your projects.",
        );
        Self { view, model, banner, shown: RefCell::default() }
    }
}

impl Pane for DumpsPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let Some(status) = data.status.clone() else { return };
        if self.shown.borrow().as_ref() == Some(&status) {
            return;
        }
        self.shown.replace(Some(status.clone()));
        let capturing = status.dumps;
        let off = status.capture_off.clone().unwrap_or_default();

        let main = group("", "Turn it off to see dumps in the page again.");
        let (model, banner) = (self.model.clone(), self.banner.clone());
        main.add(&switch_row("Capture", "", capturing, move |on| {
            let banner = banner.clone();
            model.change_settings(SettingsChange { dumps: Some(on), ..Default::default() }, move |e| report(&banner, e));
        }));
        main.add(&open_row("Captured", &format!("{} dumps, {} events", status.dump_count, status.event_count), "Open Dumps", "app.dumps"));

        let unsupported = status.capture_unsupported.clone().unwrap_or_default();
        let mut footer = "dd() always shows in the page too, as it ends the request.".to_owned();
        if !unsupported.is_empty() {
            let (verb, builds) = if unsupported.len() == 1 { ("captures", "its build has") } else { ("capture", "their builds have") };
            footer.push_str(&format!(" PHP {} only {verb} dumps: {builds} no agent extension yet.", unsupported.join(", ")));
        }
        let kinds = group("While capturing", &footer);
        for (kind, title) in EVENT_KINDS {
            let this_off = off.clone();
            let (model, banner) = (self.model.clone(), self.banner.clone());
            let row = switch_row(title, "", !off.iter().any(|k| k == kind), move |on| {
                let mut list: Vec<String> = this_off.iter().filter(|k| *k != kind).cloned().collect();
                if !on {
                    list.push(kind.to_owned());
                }
                let banner = banner.clone();
                model.change_settings(SettingsChange { capture_off: Some(list), ..Default::default() }, move |e| report(&banner, e));
            });
            kinds.add(&row);
        }
        let (model, banner) = (self.model.clone(), self.banner.clone());
        kinds.add(&switch_row("Show dump() output in the page too", "", status.dumps_in_page, move |on| {
            let banner = banner.clone();
            model.change_settings(SettingsChange { dumps_in_page: Some(on), ..Default::default() }, move |e| report(&banner, e));
        }));
        kinds.set_sensitive(capturing);

        let sources = group("Sources", "");
        let names = gtk::StringList::new(&EDITORS.map(|(_, name)| name));
        let editor = adw::ComboRow::builder().title("Open files in").model(&names).build();
        let current = prefs::load().editor;
        editor.set_selected(EDITORS.iter().position(|(id, _)| *id == current).unwrap_or(0) as u32);
        editor.connect_selected_notify(|row| {
            let mut p = prefs::load();
            p.editor = EDITORS[row.selected() as usize].0.to_owned();
            let _ = prefs::save(&p);
        });
        sources.add(&editor);

        self.view.set_groups(&[main.upcast(), kinds.upcast(), sources.upcast()]);
    }
}
