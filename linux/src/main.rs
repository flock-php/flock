//! Flock's Linux app: a tray icon and a libadwaita window for the local daemon, like the macOS menu
//! bar app and the Windows tray app. It talks to the daemon over its local API (api.json).

mod api;
mod dumps;
mod installation;
mod logs;
mod mail;
mod model;
mod panes;
mod paths;
mod prefs;
mod setup;
mod timeline;
mod tray;
mod ui;
mod window;

use std::cell::{Cell, RefCell};
use std::path::PathBuf;
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

const APP_ID: &str = "dev.flock.Flock";

/// Styles libadwaita has no class for: the small labels next to versions and sites.
const STYLE: &str = "
.badge { padding: 1px 7px; border-radius: 99px; font-weight: 600; background: alpha(currentColor, 0.12); }
";

/// The app's windows: the Flock window, created at start, and the others when first opened.
#[derive(Default)]
struct Windows {
    main: RefCell<Option<Rc<window::MainWindow>>>,
    mail: RefCell<Option<Rc<mail::MailWindow>>>,
    dumps: RefCell<Option<Rc<dumps::DumpsWindow>>>,
    logs: RefCell<Option<Rc<logs::LogsWindow>>>,
}

impl Windows {
    /// Opens the Flock window at a pane, "services/<id>" at a service or "sites/<name>" at a site.
    fn pane(&self, pane: &str) -> Option<gtk::Window> {
        let window = self.main.borrow().clone()?;
        if let Some(service) = pane.strip_prefix("services/") {
            window.select_service(service);
        } else if let Some(site) = pane.strip_prefix("sites/") {
            window.select_site(site);
        } else {
            window.select(pane);
        }
        self.main()
    }

    /// Brings the Flock window up at the pane it shows; until Flock is set up, that is the setup.
    fn main(&self) -> Option<gtk::Window> {
        let window = self.main.borrow().as_ref()?.window.clone();
        window.present();
        Some(window.upcast())
    }

    fn mail(&self, app: &adw::Application, model: &Rc<model::AppModel>) -> gtk::Window {
        let window = self.mail.borrow_mut().get_or_insert_with(|| mail::MailWindow::new(app, model.clone())).clone();
        window.window.present();
        window.window.clone().upcast()
    }

    fn dumps(&self, app: &adw::Application, model: &Rc<model::AppModel>) -> gtk::Window {
        let window = self.dumps.borrow_mut().get_or_insert_with(|| dumps::DumpsWindow::new(app, model.clone())).clone();
        window.window.present();
        window.window.clone().upcast()
    }

    /// Opens the Logs window, at a log when one is given.
    fn logs(&self, app: &adw::Application, source: Option<logs::Source>) -> gtk::Window {
        let window = self.logs.borrow_mut().get_or_insert_with(|| logs::LogsWindow::new(app)).clone();
        if let Some(source) = source {
            window.select(source);
        }
        window.window.present();
        window.window.clone().upcast()
    }

    /// Opens a window by name: flock (the Flock window as it was) or setup, mail (mail/newest at the
    /// newest message), dumps, logs (logs/sites/<name>, logs/services/<name> at a log), settings
    /// (General) or settings/<pane>, e.g. settings/sites.
    fn open(&self, app: &adw::Application, model: &Rc<model::AppModel>, name: &str) -> Option<gtk::Window> {
        match name {
            "flock" | "setup" => self.main(),
            "mail" => Some(self.mail(app, model)),
            "dumps" => Some(self.dumps(app, model)),
            "logs" => Some(self.logs(app, None)),
            "mail/newest" => {
                let window = self.mail(app, model);
                if let Some(mail) = self.mail.borrow().as_ref() {
                    mail.select_newest();
                }
                Some(window)
            }
            "settings" => self.pane("general"),
            _ => {
                if let Some(site) = name.strip_prefix("logs/sites/") {
                    Some(self.logs(app, Some(logs::Source::Site(site.to_owned()))))
                } else if let Some(service) = name.strip_prefix("logs/services/") {
                    Some(self.logs(app, Some(logs::Source::Service(service.to_owned()))))
                } else {
                    name.strip_prefix("settings/").and_then(|pane| self.pane(pane))
                }
            }
        }
    }
}

fn main() -> glib::ExitCode {
    let args: Vec<String> = std::env::args().collect();
    let flag = |name: &str| args.iter().position(|a| a == name).and_then(|i| args.get(i + 1)).cloned();
    // --background: the start at login, which stays in the tray (when the desktop has one).
    // --open <window>: opens setup, mail, settings or settings/<pane> at start.
    // --screenshot <file.png>: renders that window (else the main one) once it shows the status,
    // saves it and quits (development and the docs' screenshots).
    let background = args.iter().any(|a| a == "--background");
    let open = flag("--open");
    let screenshot = flag("--screenshot").map(PathBuf::from);

    let app = adw::Application::builder().application_id(APP_ID).build();
    let model = model::AppModel::new();
    let windows = Rc::new(Windows::default());

    app.connect_startup(|_| {
        let css = gtk::CssProvider::new();
        css.load_from_string(STYLE);
        gtk::style_context_add_provider_for_display(
            &gtk::gdk::Display::default().expect("display"),
            &css,
            gtk::STYLE_PROVIDER_PRIORITY_APPLICATION,
        );
        if let Some(icons) = tray::development_icons() {
            gtk::IconTheme::for_display(&gtk::gdk::Display::default().expect("display")).add_search_path(icons.join("hicolor"));
        }
        gtk::Window::set_default_icon_name(APP_ID);
    });

    let (m, w) = (model.clone(), windows.clone());
    app.connect_activate(move |app| {
        // Started again (from the launcher) while it runs, maybe hidden in the tray.
        if w.main().is_some() {
            return;
        }
        let main = window::MainWindow::new(app, m.clone());
        m.start_polling();
        w.main.replace(Some(main.clone()));
        // At login, the window only shows if there is no tray to stay in.
        let mut shown = (!background || screenshot.is_some()).then(|| w.main()).flatten();
        if screenshot.is_none() {
            start_tray(app, &m, &w);
        }
        if let Some(window) = open.as_deref().and_then(|name| w.open(app, &m, name)) {
            shown = Some(window);
        }
        if let (Some(path), Some(shown)) = (screenshot.clone(), shown) {
            // The paintable follows the content from its next frame on.
            let paintable = gtk::WidgetPaintable::new(shown.child().as_ref());
            // FLOCK_APP_SCREENSHOT_DELAY: seconds to wait for slow content (default 3).
            let delay = std::env::var("FLOCK_APP_SCREENSHOT_DELAY").ok().and_then(|d| d.parse().ok()).unwrap_or(3);
            glib::timeout_add_local_once(Duration::from_secs(delay), move || {
                save_screenshot(&shown, &paintable, &path);
                shown.application().expect("application").quit();
            });
        }
    });

    // app.settings opens General, app.open('settings/sites') a pane, app.open('mail') a window.
    let quit = gio::ActionEntry::builder("quit").activate(|app: &adw::Application, _, _| app.quit()).build();
    let (m, w) = (model.clone(), windows.clone());
    let open_action = gio::ActionEntry::builder("open")
        .parameter_type(Some(glib::VariantTy::STRING))
        .activate(move |app: &adw::Application, _, name| {
            if let Some(name) = name.and_then(|n| n.str()) {
                w.open(app, &m, name);
            }
        })
        .build();
    let mut entries = vec![quit, open_action];
    for name in ["settings", "setup", "mail", "dumps", "logs"] {
        let (m, w) = (model.clone(), windows.clone());
        entries.push(
            gio::ActionEntry::builder(name)
                .activate(move |app: &adw::Application, _, _| {
                    w.open(app, &m, name);
                })
                .build(),
        );
    }
    app.add_action_entries(entries);
    app.set_accels_for_action("app.quit", &["<Control>q"]);
    app.set_accels_for_action("app.settings", &["<Control>comma"]);

    // GApplication would reject our flags as unknown options.
    app.run_with_args(&args[..1])
}

/// Shows the tray icon. With a tray, closing the Flock window hides it, and the app keeps running;
/// without one, closing it quits.
fn start_tray(app: &adw::Application, model: &Rc<model::AppModel>, windows: &Rc<Windows>) {
    let w = windows.clone();
    let shown = Cell::new(false);
    let available = move |yes: bool| {
        let Some(main) = w.main.borrow().as_ref().map(|m| m.window.clone()) else { return };
        // Without a tray from the start, the window is the app: it shows after all (at login), and
        // closing it quits. A tray that goes away later (GNOME's, while the screen is locked) keeps
        // a hidden window hidden; the launcher shows it.
        if !yes && !shown.get() {
            main.present();
        }
        shown.set(shown.get() || yes);
        main.set_hide_on_close(shown.get());
    };
    let (app, m, w) = (app.clone(), model.clone(), windows.clone());
    tray::start(model.clone(), available, move |command| match command {
        tray::Command::Show => {
            w.main();
        }
        tray::Command::Open(name) => {
            w.open(&app, &m, name);
        }
        tray::Command::Browse(url) => gtk::UriLauncher::new(&url).launch(None::<&gtk::Window>, gio::Cancellable::NONE, |_| {}),
        tray::Command::Change(change) => {
            let model = m.clone();
            m.change_settings(change, move |error| model.report(error));
        }
        tray::Command::Restart => m.restart(),
        tray::Command::Quit => app.quit(),
        tray::Command::Available(_) => {}
    });
}

/// Renders the window's content (a toplevel can't paint itself into a paintable) into a PNG.
fn save_screenshot(window: &gtk::Window, paintable: &gtk::WidgetPaintable, path: &PathBuf) {
    let (width, height) = (paintable.intrinsic_width(), paintable.intrinsic_height());
    let snapshot = gtk::Snapshot::new();
    paintable.snapshot(&snapshot, width as f64, height as f64);
    let Some(node) = snapshot.to_node() else {
        eprintln!("screenshot: the window drew nothing ({width}×{height})");
        return;
    };
    let Some(renderer) = window.native().and_then(|n| n.renderer()) else {
        eprintln!("screenshot: the window has no renderer");
        return;
    };
    let texture = renderer.render_texture(&node, None);
    if let Err(e) = texture.save_to_png(path) {
        eprintln!("screenshot: {e}");
    }
}
