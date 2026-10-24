//! The main window: what the macOS menu bar panel and the Windows tray panel show. Status,
//! services, sites, the Xdebug and capture toggles, and shortcuts to Mail, Dumps and Logs; until
//! Flock is set up, the setup.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{SettingsChange, SiteStatus, Status};
use crate::model::AppModel;
use crate::setup::SetupView;

pub struct MainWindow {
    pub window: adw::ApplicationWindow,
    title: adw::WindowTitle,
    banner: adw::Banner,
    stack: gtk::Stack,
    status_page: adw::StatusPage,
    status_button: gtk::Button,
    setup: Rc<SetupView>,
    restart: gtk::Button,
    spinner: gtk::Spinner,
    warnings: adw::PreferencesGroup,
    services: adw::PreferencesGroup,
    sites: adw::PreferencesGroup,
    xdebug: adw::SwitchRow,
    dumps: adw::SwitchRow,
    mail_button: gtk::Button,
    dumps_button: gtk::Button,
    /// Rows added for the current status, removed before the next one is shown.
    rows: RefCell<Vec<(adw::PreferencesGroup, gtk::Widget)>>,
    /// The status the rows show, so they are only rebuilt when it changes.
    shown: RefCell<Option<Status>>,
    /// Set while the toggles follow the status, so their handlers don't send it back.
    syncing: Cell<bool>,
    model: Rc<AppModel>,
}

impl MainWindow {
    pub fn new(app: &adw::Application, model: Rc<AppModel>) -> Rc<Self> {
        let title = adw::WindowTitle::new("Flock", "");
        let header = adw::HeaderBar::new();
        header.set_title_widget(Some(&title));

        let restart = gtk::Button::from_icon_name("view-refresh-symbolic");
        restart.set_tooltip_text(Some("Restart Flock"));
        header.pack_start(&restart);
        let spinner = gtk::Spinner::builder().spinning(true).visible(false).build();
        header.pack_start(&spinner);

        let menu = gio::Menu::new();
        menu.append(Some("Settings"), Some("app.settings"));
        menu.append(Some("Quit"), Some("app.quit"));
        let menu_button = gtk::MenuButton::builder().icon_name("open-menu-symbolic").menu_model(&menu).primary(true).build();
        header.pack_end(&menu_button);

        let banner = adw::Banner::builder().button_label("Dismiss").build();

        let warnings = adw::PreferencesGroup::new();
        let services = adw::PreferencesGroup::builder().title("Services").build();
        let sites = adw::PreferencesGroup::builder().title("Sites").build();

        let xdebug = adw::SwitchRow::builder().title("Xdebug").subtitle("Debug requests that ask for it").build();
        let dumps =
            adw::SwitchRow::builder().title("Capture dumps &amp; queries").subtitle("dump(), queries, logs, views and jobs").build();
        let toggles = adw::PreferencesGroup::new();
        toggles.add(&xdebug);
        toggles.add(&dumps);

        let mail_button = shortcut_button("Mail", "mail-unread-symbolic");
        let dumps_button = shortcut_button("Dumps", "applications-science-symbolic");
        let logs_button = shortcut_button("Logs", "text-x-generic-symbolic");
        let shortcuts = gtk::Box::builder().spacing(8).homogeneous(true).build();
        shortcuts.append(&mail_button);
        shortcuts.append(&dumps_button);
        shortcuts.append(&logs_button);

        let content = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(18)
            .margin_top(12)
            .margin_bottom(18)
            .margin_start(12)
            .margin_end(12)
            .build();
        content.append(&warnings);
        content.append(&services);
        content.append(&sites);
        content.append(&toggles);
        content.append(&shortcuts);
        let clamp = adw::Clamp::builder().maximum_size(560).child(&content).build();
        let scrolled = gtk::ScrolledWindow::builder().hscrollbar_policy(gtk::PolicyType::Never).child(&clamp).vexpand(true).build();

        let status_button = gtk::Button::builder().halign(gtk::Align::Center).css_classes(["pill", "suggested-action"]).build();
        let status_page = adw::StatusPage::builder().icon_name("network-server-symbolic").child(&status_button).build();

        let setup = SetupView::new(model.clone());
        let stack = gtk::Stack::new();
        stack.add_named(&scrolled, Some("running"));
        stack.add_named(&status_page, Some("stopped"));
        stack.add_named(&setup.widget, Some("setup"));

        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&stack);

        let toolbar = adw::ToolbarView::new();
        toolbar.add_top_bar(&header);
        toolbar.set_content(Some(&body));

        let window = adw::ApplicationWindow::builder()
            .application(app)
            .title("Flock")
            .default_width(480)
            .default_height(700)
            .content(&toolbar)
            .build();

        let this = Rc::new(Self {
            window,
            title,
            banner,
            stack,
            status_page,
            status_button,
            setup,
            restart,
            spinner,
            warnings,
            services,
            sites,
            xdebug,
            dumps,
            mail_button,
            dumps_button,
            rows: RefCell::new(Vec::new()),
            shown: RefCell::new(None),
            syncing: Cell::new(false),
            model,
        });
        this.connect(&logs_button);
        this.render();
        let weak = Rc::downgrade(&this);
        this.model.subscribe(move || {
            if let Some(this) = weak.upgrade() {
                this.render();
            }
        });
        this
    }

    fn connect(self: &Rc<Self>, logs_button: &gtk::Button) {
        let model = self.model.clone();
        self.banner.connect_button_clicked(move |_| model.dismiss_error());

        let model = self.model.clone();
        self.restart.connect_clicked(move |_| model.restart());

        let model = self.model.clone();
        self.status_button.connect_clicked(move |_| model.restart());
        let weak = Rc::downgrade(self);
        self.setup.connect_finish(move || {
            if let Some(this) = weak.upgrade() {
                this.render();
            }
        });

        let this = self.clone();
        self.xdebug.connect_active_notify(move |row| {
            if !this.syncing.get() {
                this.change(SettingsChange { xdebug: Some(row.is_active()), ..Default::default() });
            }
        });
        let this = self.clone();
        self.dumps.connect_active_notify(move |row| {
            if !this.syncing.get() {
                this.change(SettingsChange { dumps: Some(row.is_active()), ..Default::default() });
            }
        });

        self.mail_button.set_action_name(Some("app.mail"));
        self.dumps_button.set_action_name(Some("app.dumps"));
        logs_button.set_action_name(Some("app.logs"));
    }

    /// Changes a setting; a failure shows in the banner.
    fn change(&self, change: SettingsChange) {
        let model = self.model.clone();
        self.model.change_settings(change, move |error| model.report(error));
    }

    /// Shows the current state. Rows are rebuilt from the status; the window keeps its scroll position.
    fn render(&self) {
        let status = self.model.status();
        let needs_setup = self.model.needs_setup();
        let restarting = self.model.is_restarting();

        self.spinner.set_visible(restarting);
        self.restart.set_sensitive(!restarting && !needs_setup && !self.setup.engaged() && status.is_some());
        self.title.set_subtitle(&self.model.headline());
        match self.model.action_error() {
            Some(message) => {
                self.banner.set_title(&glib::markup_escape_text(&message));
                self.banner.set_revealed(true);
            }
            None => self.banner.set_revealed(false),
        }

        // Until the setup is done, a daemon that answers is the setup's (it may still fail).
        if needs_setup || self.setup.engaged() {
            self.stack.set_visible_child_name("setup");
            self.shown.replace(None);
            return;
        }
        let Some(status) = status else {
            self.stack.set_visible_child_name("stopped");
            self.status_page.set_title("Flock Is Not Running");
            self.status_page.set_description(self.model.error().as_deref());
            self.status_button.set_label("Start Flock");
            self.status_button.set_sensitive(!restarting);
            self.shown.replace(None);
            return;
        };
        self.stack.set_visible_child_name("running");
        // Also after a failed change, which leaves the status as it was.
        self.syncing.set(true);
        self.xdebug.set_active(status.xdebug);
        self.dumps.set_active(status.dumps);
        self.syncing.set(false);
        if self.shown.borrow().as_ref() == Some(&status) {
            return;
        }
        self.shown.replace(Some(status.clone()));

        for (group, row) in self.rows.take() {
            group.remove(&row);
        }
        self.warnings.set_visible(!status.warnings.is_empty());
        for warning in &status.warnings {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(warning)).css_classes(["warning"]).build();
            row.add_prefix(&gtk::Image::from_icon_name("dialog-warning-symbolic"));
            self.add(&self.warnings, row);
        }
        self.render_services(&status);
        self.render_sites(&status.sites);

        set_count(&self.mail_button, status.mail_count);
        set_count(&self.dumps_button, status.dump_count);
    }

    fn render_services(&self, status: &Status) {
        self.add(&self.services, service_row("Web", &format!("http :{} · https :{}", status.http_port, status.https_port), true));
        if status.dns.wildcard {
            self.add(&self.services, service_row("DNS", &format!("*.{}", status.dns.tld), true));
        } else if !status.dns.tld.is_empty() {
            let detail = if status.dns.wildcard_problem.is_empty() {
                format!("*.{} doesn't resolve", status.dns.tld)
            } else {
                status.dns.wildcard_problem.clone()
            };
            self.add(&self.services, service_row("DNS", &detail, false));
        }
        let mail = if status.mail.is_empty() { "stopped".to_owned() } else { format!("smtp {}", status.mail) };
        self.add(&self.services, service_row("Mail", &mail, !status.mail.is_empty()));
        if let Some(version) = &status.postgres {
            self.add(&self.services, service_row("PostgreSQL", version, status.process("postgres").is_some_and(|p| p.up)));
        }
        if let Some(version) = &status.valkey {
            self.add(&self.services, service_row("Valkey", version, status.process("valkey").is_some_and(|p| p.up)));
        }
        for extra in &status.extras {
            let detail = if extra.enabled { format!("{} on 127.0.0.1:{}", extra.version, extra.port) } else { "stopped".to_owned() };
            self.add(&self.services, service_row(&extra.name, &detail, extra.up));
        }
        if status.postgres.is_none() && status.valkey.is_none() && status.extras.is_empty() {
            let row = adw::ActionRow::builder().title("No database or cache installed").build();
            let install = gtk::Button::builder()
                .label("Install…")
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .tooltip_text("Opens Settings → Services")
                .build();
            install.set_action_name(Some("app.open"));
            install.set_action_target_value(Some(&"settings/services/postgres".to_variant()));
            row.add_suffix(&install);
            self.add(&self.services, row);
        }
        for process in status.processes.iter().filter(|p| p.name.starts_with("php")) {
            let detail = match (process.up, process.restarts) {
                (false, _) => process.last_error.clone().unwrap_or_else(|| "down".to_owned()),
                (true, 0) => "up".to_owned(),
                (true, n) => format!("up (restarted {n}×)"),
            };
            self.add(&self.services, service_row(&process.name, &detail, process.up));
        }
        for process in status.processes.iter().filter(|p| !p.up && !p.name.starts_with("php")) {
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&format!("{}: {}", process.name, process.last_error.as_deref().unwrap_or("down"))))
                .css_classes(["error"])
                .build();
            self.add(&self.services, row);
        }
    }

    fn render_sites(&self, sites: &[SiteStatus]) {
        if sites.is_empty() {
            let row = adw::ActionRow::builder().title("No sites yet").subtitle("Run `flock link` or `flock park` in a project").build();
            self.add(&self.sites, row);
            return;
        }
        for site in sites {
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&site.domain)).build();
            if let Some(problem) = &site.problem {
                row.set_subtitle(&glib::markup_escape_text(problem));
                row.add_css_class("error");
            } else if let Some(notice) = &site.notice {
                row.set_subtitle(&glib::markup_escape_text(notice));
                row.add_css_class("warning");
            }
            let icon = gtk::Image::from_icon_name(if site.secured { "channel-secure-symbolic" } else { "web-browser-symbolic" });
            if site.secured {
                icon.add_css_class("success");
            }
            row.add_prefix(&icon);
            if site.debugging {
                let bug = gtk::Image::from_icon_name("applications-engineering-symbolic");
                bug.add_css_class("warning");
                bug.set_tooltip_text(Some("Debugging"));
                row.add_suffix(&bug);
            }
            if site.shared() {
                row.add_suffix(&badge("Shared", Some("success")));
            }
            if site.server == "frankenphp" {
                row.add_suffix(&badge(if site.worker == "octane" { "Octane" } else { "FrankenPHP" }, None));
            }
            let php = gtk::Label::builder().label(format!("PHP {}", site.php)).css_classes(["caption", "dim-label", "numeric"]).build();
            row.add_suffix(&php);
            row.set_tooltip_text(Some(&site_help(site)));
            if let Some(url) = site.url.clone() {
                row.set_activatable(true);
                row.connect_activated(move |row| {
                    let window = row.root().and_downcast::<gtk::Window>();
                    gtk::UriLauncher::new(&url).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
                });
            }
            self.add(&self.sites, row);
        }
    }

    fn add(&self, group: &adw::PreferencesGroup, row: impl IsA<gtk::Widget>) {
        group.add(&row);
        self.rows.borrow_mut().push((group.clone(), row.upcast()));
    }
}

fn service_row(name: &str, detail: &str, up: bool) -> adw::ActionRow {
    let row = adw::ActionRow::builder().title(glib::markup_escape_text(name)).build();
    let dot = gtk::Label::builder().label("●").css_classes([if up { "success" } else { "error" }]).build();
    row.add_prefix(&dot);
    let label = gtk::Label::builder()
        .label(detail)
        .css_classes(["dim-label"])
        .ellipsize(gtk::pango::EllipsizeMode::Middle)
        .max_width_chars(28)
        .build();
    label.set_tooltip_text(Some(detail));
    row.add_suffix(&label);
    row
}

fn badge(text: &str, class: Option<&str>) -> gtk::Label {
    let label = gtk::Label::builder().label(text).valign(gtk::Align::Center).css_classes(["caption", "badge"]).build();
    if let Some(class) = class {
        label.add_css_class(class);
    }
    label
}

/// The row's tooltip: where a site without a document root is fixed, else its address.
fn site_help(site: &SiteStatus) -> String {
    if site.root_state.as_deref() == Some("not_found") {
        return format!(
            "No public/, www/, web/ or webroot/ with an index.php in {}. Choose the document root in Settings → Sites.",
            site.path
        );
    }
    site.url.clone().or_else(|| site.problem.clone()).unwrap_or_else(|| site.path.clone())
}

fn shortcut_button(title: &str, icon: &str) -> gtk::Button {
    let content = adw::ButtonContent::builder().label(title).icon_name(icon).build();
    gtk::Button::builder().child(&content).build()
}

/// Shows a count after a shortcut's title, e.g. "Mail 3".
fn set_count(button: &gtk::Button, count: u32) {
    if let Some(content) = button.child().and_downcast::<adw::ButtonContent>() {
        let title = content.label().split(' ').next().unwrap_or_default().to_owned();
        content.set_label(&if count > 0 { format!("{title} {count}") } else { title });
    }
}
