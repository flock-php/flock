//! The main window: what the macOS menu bar panel and the Windows tray panel show. Status,
//! services, sites, the Xdebug and capture toggles, and shortcuts to Mail, Dumps and Logs.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Client, SettingsChange, SiteStatus, Status};
use crate::installation;

/// How often the window asks the daemon for its status.
const POLL_INTERVAL: Duration = Duration::from_secs(2);

pub struct MainWindow {
    pub window: adw::ApplicationWindow,
    title: adw::WindowTitle,
    toasts: adw::ToastOverlay,
    banner: adw::Banner,
    stack: gtk::Stack,
    status_page: adw::StatusPage,
    status_button: gtk::Button,
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
    status: RefCell<Option<Status>>,
    /// Why the status couldn't be read; replaced by every poll.
    error: RefCell<Option<String>>,
    needs_setup: Cell<bool>,
    restarting: Cell<bool>,
    polling: Cell<bool>,
    /// Set while the toggles follow the status, so their handlers don't send it back.
    syncing: Cell<bool>,
}

impl MainWindow {
    pub fn new(app: &adw::Application) -> Rc<Self> {
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
        let dumps_button = shortcut_button("Dumps", "bug-symbolic");
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

        let stack = gtk::Stack::new();
        stack.add_named(&scrolled, Some("running"));
        stack.add_named(&status_page, Some("stopped"));

        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&stack);
        let toasts = adw::ToastOverlay::new();
        toasts.set_child(Some(&body));

        let toolbar = adw::ToolbarView::new();
        toolbar.add_top_bar(&header);
        toolbar.set_content(Some(&toasts));

        let window = adw::ApplicationWindow::builder()
            .application(app)
            .title("Flock")
            .default_width(440)
            .default_height(680)
            .content(&toolbar)
            .build();

        let this = Rc::new(Self {
            window,
            title,
            toasts,
            banner,
            stack,
            status_page,
            status_button,
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
            status: RefCell::new(None),
            error: RefCell::new(None),
            needs_setup: Cell::new(installation::needs_setup()),
            restarting: Cell::new(false),
            polling: Cell::new(false),
            syncing: Cell::new(false),
        });
        this.connect(&logs_button);
        this.render();
        this
    }

    fn connect(self: &Rc<Self>, logs_button: &gtk::Button) {
        self.banner.connect_button_clicked(|banner| banner.set_revealed(false));

        let this = self.clone();
        self.restart.connect_clicked(move |_| this.clone().restart());

        let this = self.clone();
        self.status_button.connect_clicked(move |_| {
            if this.needs_setup.get() {
                this.toast("Setup comes with the setup window; until then run `flk install` in a terminal");
            } else {
                this.clone().restart();
            }
        });

        let this = self.clone();
        self.xdebug.connect_active_notify(move |row| {
            if !this.syncing.get() {
                this.clone().change(SettingsChange { xdebug: Some(row.is_active()), ..Default::default() });
            }
        });
        let this = self.clone();
        self.dumps.connect_active_notify(move |row| {
            if !this.syncing.get() {
                this.clone().change(SettingsChange { dumps: Some(row.is_active()), ..Default::default() });
            }
        });

        for button in [&self.mail_button, &self.dumps_button, logs_button] {
            let this = self.clone();
            button.connect_clicked(move |_| this.toast("This window isn't in the Linux app yet"));
        }
    }

    /// Polls the daemon's status from now on.
    pub fn start_polling(self: &Rc<Self>) {
        self.clone().poll();
        let this = self.clone();
        glib::timeout_add_local(POLL_INTERVAL, move || {
            this.clone().poll();
            glib::ControlFlow::Continue
        });
    }

    fn poll(self: Rc<Self>) {
        if self.polling.replace(true) {
            return;
        }
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(|| Client.status()).await.expect("status request");
            self.polling.set(false);
            match result {
                Ok(status) => self.apply(status),
                Err(e) => {
                    self.status.replace(None);
                    self.error.replace(Some(e.to_string()));
                }
            }
            // `flock install` or `flock uninstall` in a terminal changes it while the app runs.
            self.needs_setup.set(installation::needs_setup());
            self.render();
        });
    }

    fn apply(&self, status: Status) {
        self.status.replace(Some(status));
        self.error.replace(None);
    }

    /// Changes a setting; a failure shows in the banner.
    fn change(self: Rc<Self>, change: SettingsChange) {
        glib::spawn_future_local(async move {
            match gio::spawn_blocking(move || Client.update_settings(&change)).await.expect("settings request") {
                Ok(status) => {
                    self.apply(status);
                    self.banner.set_revealed(false);
                }
                Err(e) => self.show_error(&e.to_string()),
            }
            self.render();
        });
    }

    /// Runs `flock restart`, which also starts a stopped daemon.
    fn restart(self: Rc<Self>) {
        self.restarting.set(true);
        self.render();
        glib::spawn_future_local(async move {
            let out = gio::spawn_blocking(|| installation::run(&["restart"])).await.expect("flock restart");
            self.restarting.set(false);
            if out.ok {
                self.banner.set_revealed(false);
            } else {
                self.show_error(&out.text);
            }
            self.poll();
        });
    }

    fn show_error(&self, message: &str) {
        self.banner.set_title(&glib::markup_escape_text(message));
        self.banner.set_revealed(true);
    }

    fn toast(&self, message: &str) {
        self.toasts.add_toast(adw::Toast::new(message));
    }

    /// Shows the current state. Rows are rebuilt from the status; the window keeps its scroll position.
    fn render(&self) {
        let status = self.status.borrow().clone();
        let needs_setup = self.needs_setup.get();
        let restarting = self.restarting.get();

        self.spinner.set_visible(restarting);
        self.restart.set_sensitive(!restarting && !needs_setup && status.is_some());
        self.title.set_subtitle(&match (&status, needs_setup) {
            (_, true) => "Not set up".to_owned(),
            (Some(s), _) if s.is_degraded() => format!("Needs attention · v{}", s.version),
            (Some(s), _) => format!("Running · v{}", s.version),
            (None, _) => "Not running".to_owned(),
        });

        // Before the setup is done, a daemon that answers is the setup's (it may still fail).
        let Some(status) = status.filter(|_| !needs_setup) else {
            self.stack.set_visible_child_name("stopped");
            if needs_setup {
                self.status_page.set_title("Set Up Flock");
                self.status_page.set_description(Some(
                    "Setup installs the daemon, puts php, composer and node on your PATH, and makes *.test sites work with trusted HTTPS.",
                ));
                self.status_button.set_label("Set Up Flock…");
            } else {
                self.status_page.set_title("Flock Is Not Running");
                self.status_page.set_description(self.error.borrow().as_deref());
                self.status_button.set_label("Start Flock");
            }
            self.status_button.set_sensitive(!restarting);
            return;
        };
        self.stack.set_visible_child_name("running");

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

        self.syncing.set(true);
        self.xdebug.set_active(status.xdebug);
        self.dumps.set_active(status.dumps);
        self.syncing.set(false);
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
            let row = adw::ActionRow::builder()
                .title("No database or cache installed")
                .subtitle("Install them in Settings → Services")
                .css_classes(["dim-label"])
                .build();
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
                let bug = gtk::Image::from_icon_name("bug-symbolic");
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
