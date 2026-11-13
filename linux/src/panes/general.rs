//! General: the daemon, the domain, the data folder, opening at login, updates and the project
//! folders.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, code_block, group, info_row, report, status_dot, switch_row};
use super::suffix::{Draft, SuffixPicker};
use super::versions::busy_button;
use super::{Data, Pane};
use crate::api::{Client, Config, Status};
use crate::autostart;
use crate::installation;
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

        let model = self.model.clone();
        let restart = busy_button("Restart Daemon", "Restarting…", self.model.is_restarting(), move || model.restart());
        let row = adw::ActionRow::new();
        row.add_suffix(&restart);
        group.add(&row);
        group
    }

    /// The global domain switch (spec 4): the suffix picker with the daemon's live verdict, and
    /// an Apply that runs the whole switch through the packaged CLI — `flock tld` raises the
    /// desktop's polkit password prompt itself, so the app does no privilege work. The picker
    /// compares against the config's tld: the CLI writes the config first and only then does the
    /// DNS and certificate steps, so after a declined prompt the refreshed pane already sits on
    /// the new suffix, which is what the recovery sentence tells the user about.
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
        // The picker makes no network call itself: this closure is the daemon call, run on the
        // thread it spawns after its own debounce (panes/suffix.rs).
        let picker = SuffixPicker::new(&config.tld, false, |tld| Client.check_tld(&tld));
        let domain = picker.row();
        domain.set_title("Domain");
        domain.set_subtitle(&glib::markup_escape_text(&domain_explanation(&config.tld, status)));
        group.add(&domain);
        group.add(&picker.entry());
        group.add(&picker.verdict());
        // While the switch runs, the CLI's last output line shows under the picker.
        let progress = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["dim-label"]).visible(false).build();
        group.add(&progress);

        let apply = gtk::Button::builder().label("Apply").valign(gtk::Align::Center).sensitive(false).build();
        picker.set_changed({
            let (apply, picker) = (apply.clone(), picker.clone());
            move || apply.set_sensitive(picker.can_apply())
        });
        // The previous failed run's output row, removed when the picker starts another one.
        let output: Rc<RefCell<Option<adw::ExpanderRow>>> = Rc::new(RefCell::default());
        {
            let (picker, domain, entry, progress, group, banner, model, output) = (
                picker.clone(),
                domain.clone(),
                picker.entry(),
                progress.clone(),
                group.clone(),
                self.banner.clone(),
                self.model.clone(),
                output,
            );
            apply.connect_clicked(move |button| {
                // Only a draft the daemon accepted enables Apply, and General offers no Inherit,
                // so the run's value is always a normalized suffix — the only thing argv takes.
                let Some(Draft::Custom(suffix)) = picker.draft() else { return };
                // The stream's closures outlive the click: every handle they touch is a clone.
                let (button, picker, domain, entry, group, banner, model) =
                    (button.clone(), picker.clone(), domain.clone(), entry.clone(), group.clone(), banner.clone(), model.clone());
                let (output, running, finished) = (output.clone(), progress.clone(), progress.clone());
                // Busy until the CLI is done: the spinner box busy_button shows, and the picker
                // disabled so no draft can drift while the password prompt is up.
                button.set_sensitive(false);
                let content = gtk::Box::builder().spacing(6).build();
                content.append(&gtk::Spinner::builder().spinning(true).build());
                content.append(&gtk::Label::new(Some("Switching…")));
                button.set_child(Some(&content));
                domain.set_sensitive(false);
                entry.set_sensitive(false);
                running.set_visible(false);
                clear_output(&group, &output);
                installation::run_streaming(
                    vec!["tld".to_owned(), suffix.clone()],
                    move |line| {
                        running.set_label(line.trim());
                        running.set_visible(true);
                    },
                    move |out| {
                        finished.set_visible(false);
                        if out.ok {
                            // The new tld is the config's current one: the refresh re-renders the
                            // pane, and the picker seeds there with a free banner.
                            report(&banner, None);
                            model.refresh();
                            return;
                        }
                        // The run didn't finish; the picker returns for a retry or another choice.
                        button.set_label("Apply");
                        button.set_sensitive(picker.can_apply());
                        domain.set_sensitive(true);
                        entry.set_sensitive(true);
                        if installation::declined(&out) {
                            // The prompt came after the config switch and before the DNS and
                            // certificate steps, so the recovery sentence is the whole story.
                            report(
                                &banner,
                                Some(format!(
                                    "You declined the password. Flock’s config is on .{suffix} already; \
                                     finish the switch by running `flock install` in a terminal."
                                )),
                            );
                        } else {
                            report(&banner, Some(format!("Switching to .{suffix} didn’t finish.")));
                            if !out.text.trim().is_empty() {
                                let expander = adw::ExpanderRow::builder().title("Output").build();
                                expander.add_row(&code_block(out.text.trim()));
                                group.add(&expander);
                                output.borrow_mut().replace(expander);
                            }
                        }
                        model.refresh();
                    },
                );
            });
        }
        let actions = adw::ActionRow::new();
        actions.add_suffix(&apply);
        group.add(&actions);
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

    /// Linux apps don't update themselves: the flockphp package comes with the system's updates.
    fn updates(&self, config: &Config, status: &Status) -> adw::PreferencesGroup {
        let group = group("Updates", "");
        // The version in the title: as a value next to the subtitle and the button, it got squeezed.
        let version = adw::ActionRow::builder().title(glib::markup_escape_text(&format!("Flock {}", status.version))).build();
        let aur = installation::aur_package();
        match aur {
            // The desktop's updater doesn't update AUR packages.
            Some(package) => version.set_subtitle(&glib::markup_escape_text(&format!(
                "New versions come from the AUR package {package}: update it with your AUR helper, e.g. `yay -Syu`."
            ))),
            None => version.set_subtitle("New versions come with your system's updates. `flock update` installs one right away."),
        }
        if let Some((name, updater)) = system_updater().filter(|_| aur.is_none()) {
            let open =
                gtk::Button::builder().label("Check for Updates…").tooltip_text(format!("Opens {name}")).valign(gtk::Align::Center).build();
            let banner = self.banner.clone();
            open.connect_clicked(move |_| {
                if let Err(e) = std::process::Command::new(updater[0]).args(&updater[1..]).spawn() {
                    report(&banner, Some(format!("Could not open {}: {e}", updater[0])));
                }
            });
            version.add_suffix(&open);
        }
        group.add(&version);
        // `flock channel`: on Linux, Flock's own betas come from its package repository's beta
        // suite, which the system's file names, so the switch asks for a password. On Arch, they are
        // an AUR package of their own, which `flock channel` names.
        let beta = adw::SwitchRow::builder()
            .title("Beta versions")
            .subtitle("Get new versions of Flock, PHP and services earlier, including pre-releases. They may not work yet.")
            .active(config.channel == "beta")
            .build();
        let (model, banner, current) = (self.model.clone(), self.banner.clone(), config.channel == "beta");
        beta.connect_active_notify(move |row| {
            let on = row.is_active();
            // Also the switch going back after a declined prompt.
            if on == current {
                return;
            }
            let (model, banner, row) = (model.clone(), banner.clone(), row.clone());
            row.set_sensitive(false);
            glib::spawn_future_local(async move {
                let channel = if on { "beta" } else { "stable" };
                let out = gio::spawn_blocking(move || installation::run(&["channel", channel])).await.expect("flock channel");
                row.set_sensitive(true);
                if out.ok {
                    if let Some(hint) = out.text.lines().find(|line| line.contains("AUR package")) {
                        report(&banner, Some(hint.to_owned()));
                    }
                } else {
                    if !installation::declined(&out) {
                        report(&banner, Some(out.text));
                    }
                    row.set_active(!on);
                }
                model.refresh();
            });
        });
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
            groups.push(self.updates(config, &status).upcast());
            groups.push(self.project_folders(config).upcast());
        }
        self.view.set_groups(&groups);
    }
}

/// The desktop's program for system updates, its name and the command that opens its updates: Ubuntu's
/// Software Updater, GNOME Software or KDE Discover.
fn system_updater() -> Option<(&'static str, &'static [&'static str])> {
    const UPDATERS: [(&str, &[&str]); 3] = [
        ("Software Updater", &["update-manager"]),
        ("Software", &["gnome-software", "--mode=updates"]),
        ("Discover", &["plasma-discover", "--mode", "Update"]),
    ];
    let path = std::env::var_os("PATH")?;
    UPDATERS.into_iter().find(|(_, u)| std::env::split_paths(&path).any(|dir| dir.join(u[0]).is_file()))
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

/// Takes the output row of a previous failed `flock tld` run out of the Network group, so a
/// retry doesn't stack its failure text under the old one.
fn clear_output(group: &adw::PreferencesGroup, output: &RefCell<Option<adw::ExpanderRow>>) {
    if let Some(expander) = output.borrow_mut().take() {
        group.remove(&expander);
    }
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
