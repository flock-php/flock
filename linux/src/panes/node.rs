//! Node: the Node.js majors from the package manifest. Install, update, make default and
//! uninstall them, and update their npm. Installs answer when they are done.

use std::cell::{Cell, RefCell};
use std::collections::BTreeMap;
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use super::common::{PaneView, ask_link, group, report};
use super::versions::{badge, button, checked_text, confirm, more_menu};
use super::{Data, Pane};
use crate::api::{Client, Error, NodeRelease, NodeVersions, SettingsChange};
use crate::links::{self, Link};
use crate::model::AppModel;

pub struct NodePane(Rc<Inner>);

struct Inner {
    view: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    versions: RefCell<Option<NodeVersions>>,
    /// What a row is doing while the daemon works, by major: "Downloading Node.js 22.11.0…".
    steps: RefCell<BTreeMap<String, String>>,
    /// The first run's default being installed, which no row started.
    default_install: RefCell<Option<String>>,
    checking: Cell<bool>,
    loaded: Cell<bool>,
    shown: RefCell<Option<String>>,
}

impl NodePane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        Self(Rc::new(Inner {
            view: PaneView::new(
                "node, npm and npx follow the project's .nvmrc / .node-version, then the site's version, then the default.",
            ),
            model,
            banner,
            versions: RefCell::default(),
            steps: RefCell::default(),
            default_install: RefCell::default(),
            checking: Cell::new(false),
            loaded: Cell::new(false),
            shown: RefCell::default(),
        }))
    }
}

impl Pane for NodePane {
    fn widget(&self) -> gtk::Widget {
        self.0.view.scrolled.clone().upcast()
    }

    fn update(&self, data: &Data) {
        let installing = data
            .status
            .as_ref()
            .and_then(|s| s.defaults.as_ref())
            .map(|d| &d.node)
            .filter(|n| n.state == "installing" && !n.version.is_empty())
            .map(|n| n.version.clone());
        // Reads the versions again when an install no row started ends.
        let finished = self.0.default_install.borrow().is_some() && installing.is_none();
        self.0.default_install.replace(installing);
        if !self.0.loaded.replace(true) || finished {
            self.0.load(|c| c.node_versions());
        }
        self.0.render();
    }
}

impl Inner {
    fn load(self: &Rc<Self>, f: impl FnOnce(Client) -> Result<NodeVersions, Error> + Send + 'static) {
        let this = self.clone();
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("node request");
            this.apply(result);
        });
    }

    fn apply(self: &Rc<Self>, result: Result<NodeVersions, Error>) {
        match result {
            Ok(versions) => {
                self.versions.replace(Some(versions));
                report(&self.banner, None);
            }
            Err(e) => report(&self.banner, Some(e.to_string())),
        }
        self.render();
    }

    /// Runs a request for a row, showing step under its name until the daemon answers.
    fn run(self: &Rc<Self>, major: &str, step: String, f: impl FnOnce(Client) -> Result<NodeVersions, Error> + Send + 'static) {
        self.steps.borrow_mut().insert(major.to_owned(), step);
        self.render();
        let (this, major) = (self.clone(), major.to_owned());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || f(Client)).await.expect("node request");
            this.steps.borrow_mut().remove(&major);
            this.apply(result);
        });
    }

    /// Asks about a pending flock://install/node link once the versions are loaded.
    fn ask_link(self: &Rc<Self>) {
        let Some(Link::Install(link)) = self.model.link().filter(|l| matches!(l, Link::Install(i) if i.service == links::NODE)) else {
            return;
        };
        let installing: Vec<String> = self.steps.borrow().keys().cloned().chain(self.default_install.borrow().clone()).collect();
        let Some(resolution) = link.resolve_node(self.versions.borrow().as_ref(), &installing) else { return };
        self.model.set_link(None);
        let this = self.clone();
        ask_link(&self.view.scrolled, resolution, "Install", move |target| {
            let major = target.line.clone();
            this.run(&target.line, format!("Downloading Node.js {}…", target.version), move |c| c.install_node(&major));
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let versions = self.versions.borrow().clone();
        let key = format!("{versions:?}{:?}{:?}{}", self.steps.borrow(), self.default_install.borrow(), self.checking.get());
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));
        let Some(versions) = versions else {
            let loading = group("", "");
            loading.add(&adw::ActionRow::builder().title("Loading Node.js versions…").build());
            self.view.set_groups(&[loading.upcast()]);
            return;
        };
        let mut footer = String::new();
        if !versions.check_error.is_empty() {
            footer = format!("Couldn't check for updates: {}\n\n", versions.check_error);
        }
        footer.push_str("Official Node.js builds, the newest release of each major. A project that pins a version in .nvmrc gets that major offered when you run node, npm or npx there. Global packages (npm install -g) belong to a Node line and survive its updates; those of the default version are on your PATH.");
        let list = group("Versions", &footer);
        list.set_header_suffix(Some(&self.check_button(versions.checked_at.as_deref())));
        let releases = versions.versions.unwrap_or_default();
        if releases.is_empty() {
            list.add(&adw::ActionRow::builder().title("No Node.js versions to show yet.").css_classes(["dim-label"]).build());
        }
        for release in &releases {
            list.add(&self.release_row(release));
        }
        self.view.set_groups(&[list.upcast()]);
    }

    fn check_button(self: &Rc<Self>, checked_at: Option<&str>) -> gtk::Widget {
        let line = gtk::Box::builder().spacing(6).build();
        line.append(&gtk::Label::builder().label(checked_text(checked_at)).css_classes(["caption", "dim-label"]).build());
        if self.checking.get() {
            line.append(&gtk::Spinner::builder().spinning(true).build());
        } else {
            let this = self.clone();
            line.append(&button("Check now", false, move || {
                this.checking.set(true);
                this.render();
                let this = this.clone();
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(|| Client.check_node()).await.expect("check request");
                    this.checking.set(false);
                    this.apply(result);
                });
            }));
            line.last_child().expect("button").add_css_class("flat");
        }
        line.upcast()
    }

    fn release_row(self: &Rc<Self>, release: &NodeRelease) -> adw::ActionRow {
        let installed = !release.installed.is_empty();
        let version = if release.update {
            format!("{} → {}", release.installed, release.latest)
        } else if installed {
            release.installed.clone()
        } else {
            release.latest.clone()
        };
        let row = adw::ActionRow::builder().title(format!("Node {}", release.major)).build();
        let icon = gtk::Image::from_icon_name("package-x-generic-symbolic");
        if !installed {
            icon.add_css_class("dim-label");
        }
        row.add_prefix(&icon);
        row.add_suffix(&gtk::Label::builder().label(&version).css_classes(["dim-label", "numeric"]).build());
        if release.default {
            row.add_suffix(&badge("Default", "accent"));
        }

        let step = self.steps.borrow().get(&release.major).cloned().or_else(|| {
            (self.default_install.borrow().as_deref() == Some(release.major.as_str()))
                .then(|| format!("Downloading Node.js {}…", release.latest))
        });
        if let Some(step) = &step {
            row.set_subtitle(&glib::markup_escape_text(step));
            row.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        } else {
            let mut line = (if release.update {
                "Update available"
            } else if installed {
                "Up to date"
            } else {
                "Not installed"
            })
            .to_owned();
            if !release.npm.is_empty() {
                line.push_str(&format!(" · npm {}", release.npm));
                if !release.npm_latest.is_empty() {
                    line.push_str(&format!(" (npm {} available)", release.npm_latest));
                }
            }
            row.set_subtitle(&glib::markup_escape_text(&line));
            if release.update {
                row.add_css_class("accent");
            }
        }

        let actions = gtk::Box::builder().spacing(6).sensitive(step.is_none()).build();
        let major = release.major.clone();
        let install = |label: &str, suggested: bool| {
            let (this, m, latest) = (self.clone(), major.clone(), release.latest.clone());
            button(label, suggested, move || {
                let m2 = m.clone();
                this.run(&m, format!("Downloading Node.js {latest}…"), move |c| c.install_node(&m2));
            })
        };
        if !installed {
            actions.append(&install("Install", false));
        } else {
            if release.update {
                let update = install(&format!("Update to {}", release.latest), true);
                update.set_tooltip_text(Some(&format!(
                    "Downloads Node.js {}, then removes {}. Global packages stay.",
                    release.latest, release.installed
                )));
                actions.append(&update);
            }
            if !release.npm_latest.is_empty() {
                let (this, m, npm) = (self.clone(), major.clone(), release.npm_latest.clone());
                actions.append(&button("Update npm", false, move || {
                    let m2 = m.clone();
                    this.run(&m, format!("Updating npm to {npm}…"), move |c| c.update_npm(&m2));
                }));
            }
            if !release.default {
                let (this, m) = (self.clone(), major.clone());
                actions.append(&button("Make Default", false, move || this.make_default(&m)));
            }
            let (this, m, r) = (self.clone(), major.clone(), row.clone());
            actions.append(&more_menu(vec![(
                "Uninstall…".to_owned(),
                !release.default,
                true,
                Box::new(move || {
                    let (this, m) = (this.clone(), m.clone());
                    confirm(
                        &r,
                        &format!("Uninstall Node {m}?"),
                        &format!("Its global npm packages are removed too. Projects and sites that use Node {m} fall back to the default version until you install it again."),
                        "Uninstall",
                        move || {
                            let m2 = m.clone();
                            this.run(&m, "Uninstalling…".to_owned(), move |c| c.uninstall_node(&m2));
                        },
                    );
                }),
            )]));
        }
        row.add_suffix(&actions);
        row
    }

    fn make_default(self: &Rc<Self>, major: &str) {
        self.steps.borrow_mut().insert(major.to_owned(), format!("Making Node {major} the default…"));
        self.render();
        let (this, banner, m) = (self.clone(), self.banner.clone(), major.to_owned());
        self.model.change_settings(SettingsChange { default_node: Some(major.to_owned()), ..Default::default() }, move |e| {
            this.steps.borrow_mut().remove(&m);
            report(&banner, e);
            this.load(|c| c.node_versions());
        });
    }
}
