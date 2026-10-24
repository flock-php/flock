//! Debugger: Xdebug detection, PhpStorm breakpoints and connecting an IDE.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use gtk::glib;

use super::common::{PaneView, group, info_row, report, switch_row};
use super::{Data, Pane};
use crate::api::SettingsChange;
use crate::model::AppModel;

pub struct DebuggerPane {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    shown: RefCell<Option<Data>>,
}

impl DebuggerPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self { view: PaneView::new("Step through requests and commands with Xdebug."), model, banner, shown: RefCell::default() }
    }

    fn toggle(&self, title: &str, active: bool, change: fn(bool) -> SettingsChange) -> adw::SwitchRow {
        let (model, banner) = (self.model.clone(), self.banner.clone());
        switch_row(title, "", active, move |on| {
            let banner = banner.clone();
            model.change_settings(change(on), move |e| report(&banner, e));
        })
    }
}

impl Pane for DebuggerPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let Some(status) = &data.status else { return };
        if self.shown.borrow().as_ref() == Some(data) {
            return;
        }
        self.shown.replace(Some(data.clone()));

        let detect = group(
            "",
            "Xdebug loads only for requests that ask for it, so everything else stays fast. While a project has breakpoints in PhpStorm, every request to its site is debugged.",
        );
        detect.add(&self.toggle("Detect Xdebug requests", status.xdebug, |on| SettingsChange { xdebug: Some(on), ..Default::default() }));
        let breakpoints = self.toggle("Detect PhpStorm breakpoints", status.xdebug_breakpoints, |on| SettingsChange {
            xdebug_breakpoints: Some(on),
            ..Default::default()
        });
        breakpoints.set_sensitive(status.xdebug);
        detect.add(&breakpoints);
        let mut groups = vec![detect.upcast()];

        let debugging: Vec<_> = status.sites.iter().filter(|s| s.debugging).collect();
        if status.xdebug && status.xdebug_breakpoints && !debugging.is_empty() {
            let now = group("Debugging now", "");
            for site in debugging {
                let row = adw::ActionRow::builder().title(glib::markup_escape_text(&site.domain)).css_classes(["warning"]).build();
                row.add_prefix(&gtk::Image::from_icon_name("applications-engineering-symbolic"));
                now.add(&row);
            }
            groups.push(now.upcast());
        }

        let ide = group("Connect your IDE", "");
        ide.add(&info_row("Client port", "9003"));
        ide.add(&info_row("Mode", "debug, develop"));
        let start = adw::ActionRow::builder()
            .title("Start a session")
            .subtitle("Set a breakpoint in PhpStorm and listen for debug connections, use the Xdebug browser extension, or add ?XDEBUG_TRIGGER=1 to the URL. On the command line, run `flock debug artisan …`.")
            .subtitle_selectable(true)
            .build();
        ide.add(&start);
        groups.push(ide.upcast());

        let availability = group("Availability", "");
        for php in data.config.as_ref().and_then(|c| c.php.clone()).unwrap_or_default() {
            let row = info_row(&format!("PHP {}", php.version), if php.xdebug_available { "Available" } else { "Not in this build" });
            availability.add(&row);
        }
        groups.push(availability.upcast());
        self.view.set_groups(&groups);
    }
}
