//! General: the daemon, the domain, the data folder, opening at login, updates and the project
//! folders.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, group, info_row, report, status_dot, switch_row};
use super::{Data, Pane};
use crate::api::{Client, Config, SettingsChange, Status};
use crate::autostart;
use crate::model::AppModel;

pub struct GeneralPane {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    shown: RefCell<Option<(Status, Option<Config>, bool)>>,
}

impl GeneralPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self {
            view: PaneView::new("The Flock daemon runs your sites, PHP and services in the background."),
            model,
            banner,
            shown: RefCell::default(),
        }
    }

    fn daemon(&self, status: &Status) -> adw::PreferencesGroup {
        let group = group("Daemon", "");
        let state = adw::ActionRow::builder().title("Status").build();
        let degraded = status.is_degraded();
        let value = gtk::Box::builder().spacing(6).build();
        value.append(&status_dot(!degraded));
        value.append(&gtk::Label::new(Some(if degraded { "Needs attention" } else { "Running" })));
        state.add_suffix(&value);
        group.add(&state);
        // What "Needs attention" is about.
        let down = status.processes.iter().filter(|p| !p.up).map(|p| format!("{}: {}", p.name, p.last_error.as_deref().unwrap_or("down")));
        for warning in status.warnings.iter().cloned().chain(down) {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&warning)).css_classes(["warning"]).build();
            row.add_prefix(&gtk::Image::from_icon_name("dialog-warning-symbolic"));
            group.add(&row);
        }
        group.add(&info_row("Version", &status.version));
        group.add(&info_row("Process", &format!("pid {}", status.pid)));

        let restarting = self.model.is_restarting();
        let restart = gtk::Button::builder()
            .label(if restarting { "Restarting…" } else { "Restart Daemon" })
            .valign(gtk::Align::Center)
            .sensitive(!restarting)
            .build();
        let model = self.model.clone();
        restart.connect_clicked(move |_| model.restart());
        let row = adw::ActionRow::new();
        row.add_suffix(&restart);
        group.add(&row);
        group
    }

    fn network(&self, config: &Config, status: &Status) -> adw::PreferencesGroup {
        let footer = if config.ports.http != 80 || config.ports.https != 443 {
            format!(
                "Sites use ports {} (HTTP) and {} (HTTPS), set in config.json, so their addresses include the port.",
                config.ports.http, config.ports.https
            )
        } else {
            String::new()
        };
        let group = group("Network", &footer);
        let row = info_row("Domain", &format!("*.{}", config.tld));
        row.set_subtitle(&glib::markup_escape_text(&domain_explanation(&config.tld, status)));
        group.add(&row);
        group
    }

    fn data(&self, config: &Config) -> adw::PreferencesGroup {
        let group = group("Data", "");
        let row = info_row("Folder", &config.root);
        let open = gtk::Button::builder().label("Open").valign(gtk::Align::Center).build();
        let root = config.root.clone();
        open.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            gtk::FileLauncher::new(Some(&gio::File::for_path(&root))).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
        });
        row.add_suffix(&open);
        group.add(&row);
        group
    }

    fn startup(&self) -> adw::PreferencesGroup {
        let group = group("Startup", "");
        let banner = self.banner.clone();
        group.add(&switch_row(
            "Open Flock at login",
            "Shows the Flock icon in the tray when you log in. The daemon itself always starts with your computer.",
            autostart::enabled(),
            move |on| {
                if let Err(e) = autostart::set(on) {
                    report(&banner, Some(format!("Could not change the startup setting: {e}")));
                }
            },
        ));
        group
    }

    fn updates(&self, config: &Config) -> adw::PreferencesGroup {
        let group = group("Updates", "");
        group.add(&info_row("Flock", &format!("Version {}", env!("CARGO_PKG_VERSION"))));
        let (model, banner) = (self.model.clone(), self.banner.clone());
        let beta = switch_row(
            "Beta versions",
            "Get new versions of Flock, PHP and services earlier, including pre-releases. They may not work yet.",
            config.channel == "beta",
            move |on| {
                let banner = banner.clone();
                let channel = if on { "beta" } else { "stable" }.to_owned();
                model.change_settings(SettingsChange { channel: Some(channel), ..Default::default() }, move |e| report(&banner, e));
            },
        );
        beta.set_sensitive(config.channel != "experimental");
        group.add(&beta);
        if config.channel == "experimental" {
            let row = adw::ActionRow::builder()
                .title("Experimental channel: packages that aren’t released yet are offered. `flock channel stable` switches back.")
                .css_classes(["warning"])
                .build();
            group.add(&row);
        }
        group
    }

    fn project_folders(&self, config: &Config) -> adw::PreferencesGroup {
        let group = group(
            "Project folders",
            &format!("Every folder inside a project folder is served as <folder>.{} — the same as `flock park`.", config.tld),
        );
        let add = gtk::Button::builder().icon_name("list-add-symbolic").tooltip_text("Add Project Folders").css_classes(["flat"]).build();
        let (model, banner) = (self.model.clone(), self.banner.clone());
        add.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let (model, banner) = (model.clone(), banner.clone());
            gtk::FileDialog::builder().title("Add Project Folders").build().select_multiple_folders(
                window.as_ref(),
                gio::Cancellable::NONE,
                move |result| {
                    let Ok(folders) = result else { return };
                    for folder in folders.iter::<gio::File>().flatten() {
                        if let Some(path) = folder.path() {
                            let path = path.to_string_lossy().into_owned();
                            let banner = banner.clone();
                            model.perform(move |client: Client| client.park(&path), move |e| report(&banner, e));
                        }
                    }
                },
            );
        });
        group.set_header_suffix(Some(&add));

        let parked = config.parked.clone().unwrap_or_default();
        if parked.is_empty() {
            group.add(&adw::ActionRow::builder().title("No project folders").css_classes(["dim-label"]).build());
        }
        for path in parked {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&path)).build();
            row.add_prefix(&gtk::Image::from_icon_name("folder-symbolic"));
            let remove = gtk::Button::builder()
                .icon_name("user-trash-symbolic")
                .tooltip_text("Remove")
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (model, banner) = (self.model.clone(), self.banner.clone());
            remove.connect_clicked(move |button| confirm_unpark(button, &path, model.clone(), banner.clone()));
            row.add_suffix(&remove);
            group.add(&row);
        }
        group
    }
}

impl Pane for GeneralPane {
    fn widget(&self) -> gtk::Widget {
        self.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let Some(status) = data.status.clone() else { return };
        let key = (status.clone(), data.config.clone(), self.model.is_restarting());
        if self.shown.borrow().as_ref() == Some(&key) {
            return;
        }
        self.shown.replace(Some(key));
        let mut groups = vec![self.daemon(&status).upcast()];
        if let Some(config) = &data.config {
            groups.push(self.network(config, &status).upcast());
            groups.push(self.data(config).upcast());
            groups.push(self.startup().upcast());
            groups.push(self.updates(config).upcast());
            groups.push(self.project_folders(config).upcast());
        }
        self.view.set_groups(&groups);
    }
}

/// Asks before a project folder's sites stop being served.
fn confirm_unpark(button: &gtk::Button, path: &str, model: Rc<AppModel>, banner: adw::Banner) {
    let name = std::path::Path::new(path).file_name().map_or(path.to_owned(), |n| n.to_string_lossy().into_owned());
    let dialog = adw::AlertDialog::new(
        Some(&format!("Remove {name} from project folders?")),
        Some(&format!("The sites in {path} are no longer served. The folder and its projects are not touched.")),
    );
    dialog.add_responses(&[("cancel", "Cancel"), ("remove", "Remove")]);
    dialog.set_response_appearance("remove", adw::ResponseAppearance::Destructive);
    let path = path.to_owned();
    dialog.choose(Some(button), gio::Cancellable::NONE, move |response| {
        if response == "remove" {
            model.perform(move |client: Client| client.unpark(&path), move |e| report(&banner, e));
        }
    });
}

/// How names under the TLD resolve: Flock's DNS server through systemd-resolved, which has no
/// fallback (Windows also writes site domains to the hosts file).
fn domain_explanation(tld: &str, status: &Status) -> String {
    if status.dns.wildcard {
        return "Every name, any subdomain included, is resolved by Flock's DNS server through systemd-resolved.".to_owned();
    }
    let problem = if status.dns.wildcard_problem.is_empty() { "Flock's DNS server isn't running" } else { &status.dns.wildcard_problem };
    format!(
        "*.{tld} doesn't resolve: {problem}. Linux has no hosts-file fallback, so sites don't open until it does; `flock install` sets it up again."
    )
}
