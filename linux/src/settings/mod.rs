//! The Settings window: a sidebar of panes, the selected pane next to it. Like the macOS app's
//! Settings window, it reads the daemon's config and services while it is open.

mod about;
mod ai;
mod common;
mod debugger;
mod dumps;
mod general;
mod mail;
mod node;
mod php;
mod services;
mod sites;
mod versions;

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Client, Config, Service, Status};
use crate::model::AppModel;

/// What the panes show: the status, and while the window is open the config and services.
#[derive(Clone, PartialEq, Default)]
pub struct Data {
    pub status: Option<Status>,
    pub config: Option<Config>,
    pub services: Vec<Service>,
}

/// One pane of the window.
pub trait Pane {
    fn widget(&self) -> gtk::Widget;
    /// Shows data; panes rebuild their rows only when what they show changed.
    fn update(&self, data: &Data);
}

/// The panes in sidebar order: id, title, icon.
const PANES: [(&str, &str, &str); 10] = [
    ("general", "General", "emblem-system-symbolic"),
    ("sites", "Sites", "web-browser-symbolic"),
    ("php", "PHP", "utilities-terminal-symbolic"),
    ("node", "Node", "package-x-generic-symbolic"),
    ("services", "Services", "network-server-symbolic"),
    ("mail", "Mail", "mail-unread-symbolic"),
    ("dumps", "Dumps", "applications-science-symbolic"),
    ("debugger", "Debugger", "applications-engineering-symbolic"),
    ("ai", "AI", "starred-symbolic"),
    ("about", "About", "help-about-symbolic"),
];

pub struct SettingsWindow {
    pub window: adw::Window,
    model: Rc<AppModel>,
    stack: gtk::Stack,
    content: adw::NavigationPage,
    sidebar: gtk::ListBox,
    banner: adw::Banner,
    unavailable: adw::StatusPage,
    unavailable_button: gtk::Button,
    panes: Vec<(&'static str, Box<dyn Pane>)>,
    services: services::ServicesPane,
    sites: sites::SitesPane,
    data: RefCell<Data>,
    loading: Cell<bool>,
}

impl SettingsWindow {
    pub fn new(app: &adw::Application, model: Rc<AppModel>) -> Rc<Self> {
        let sidebar = gtk::ListBox::builder().css_classes(["navigation-sidebar"]).build();
        for (_, title, icon) in PANES {
            let row = gtk::Box::builder().spacing(12).margin_top(6).margin_bottom(6).margin_start(6).build();
            row.append(&gtk::Image::from_icon_name(icon));
            row.append(&gtk::Label::new(Some(title)));
            sidebar.append(&row);
        }
        let sidebar_view = adw::ToolbarView::new();
        sidebar_view.add_top_bar(&adw::HeaderBar::new());
        sidebar_view.set_content(Some(&gtk::ScrolledWindow::builder().child(&sidebar).build()));
        let sidebar_page = adw::NavigationPage::new(&sidebar_view, "Settings");

        let banner = adw::Banner::builder().button_label("Dismiss").build();
        let services = services::ServicesPane::new(banner.clone());
        let sites = sites::SitesPane::new(model.clone(), banner.clone());
        let panes: Vec<(&'static str, Box<dyn Pane>)> = vec![
            ("general", Box::new(general::GeneralPane::new(model.clone(), banner.clone()))),
            ("sites", Box::new(sites.clone())),
            ("php", Box::new(php::PhpPane::new(model.clone(), banner.clone()))),
            ("node", Box::new(node::NodePane::new(model.clone(), banner.clone()))),
            ("services", Box::new(services.clone())),
            ("mail", Box::new(mail::MailPane::new())),
            ("dumps", Box::new(dumps::DumpsPane::new(model.clone(), banner.clone()))),
            ("debugger", Box::new(debugger::DebuggerPane::new(model.clone(), banner.clone()))),
            ("ai", Box::new(ai::AiPane::new(banner.clone()))),
            ("about", Box::new(about::AboutPane::new(model.clone()))),
        ];
        let stack = gtk::Stack::new();
        for (id, pane) in &panes {
            stack.add_named(&pane.widget(), Some(id));
        }
        for (id, title, _) in PANES {
            if stack.child_by_name(id).is_none() {
                let page = adw::StatusPage::builder()
                    .icon_name("preferences-other-symbolic")
                    .title(title)
                    .description("This pane isn't in the Linux app yet.")
                    .build();
                stack.add_named(&page, Some(id));
            }
        }
        let unavailable_button = gtk::Button::builder().halign(gtk::Align::Center).css_classes(["pill", "suggested-action"]).build();
        let unavailable = adw::StatusPage::builder().icon_name("network-server-symbolic").child(&unavailable_button).build();
        stack.add_named(&unavailable, Some("unavailable"));

        let body = gtk::Box::new(gtk::Orientation::Vertical, 0);
        body.append(&banner);
        body.append(&stack);
        let content_view = adw::ToolbarView::new();
        content_view.add_top_bar(&adw::HeaderBar::new());
        content_view.set_content(Some(&body));
        let content = adw::NavigationPage::new(&content_view, "General");

        let split = adw::NavigationSplitView::builder().sidebar(&sidebar_page).content(&content).build();
        let window = adw::Window::builder()
            .application(app)
            .title("Settings")
            .default_width(900)
            .default_height(640)
            .content(&split)
            .hide_on_close(true)
            .build();

        let this = Rc::new(Self {
            window,
            model,
            stack,
            content,
            sidebar,
            banner,
            unavailable,
            unavailable_button,
            panes,
            services,
            sites,
            data: RefCell::new(Data::default()),
            loading: Cell::new(false),
        });
        this.connect();
        this.select("general");
        this
    }

    fn connect(self: &Rc<Self>) {
        let weak = Rc::downgrade(self);
        self.sidebar.connect_row_selected(move |_, row| {
            if let (Some(this), Some(row)) = (weak.upgrade(), row) {
                this.show(PANES[row.index() as usize].0);
            }
        });
        self.banner.connect_button_clicked(|banner| banner.set_revealed(false));

        let model = self.model.clone();
        self.unavailable_button.connect_clicked(move |button| {
            if model.needs_setup() {
                let _ = button.activate_action("app.setup", None);
            } else {
                model.restart();
            }
        });

        let weak = Rc::downgrade(self);
        self.model.subscribe(move || {
            if let Some(this) = weak.upgrade().filter(|this| this.window.is_visible()) {
                this.reload();
            }
        });
        let weak = Rc::downgrade(self);
        self.window.connect_visible_notify(move |window| {
            if let Some(this) = weak.upgrade().filter(|_| window.is_visible()) {
                this.reload();
            }
        });
    }

    /// Shows a pane, e.g. from the main window's menu.
    pub fn select(&self, id: &str) {
        if let Some(index) = PANES.iter().position(|(pane, _, _)| *pane == id) {
            self.sidebar.select_row(self.sidebar.row_at_index(index as i32).as_ref());
        }
    }

    /// Shows a site's detail in the Sites pane.
    pub fn select_site(&self, name: &str) {
        self.select("sites");
        self.sites.select(name);
    }

    /// Shows a service's detail in the Services pane.
    pub fn select_service(&self, id: &str) {
        self.select("services");
        self.services.select(id);
    }

    fn show(&self, id: &str) {
        let title = PANES.iter().find(|(pane, _, _)| *pane == id).map_or("", |p| p.1);
        self.content.set_title(title);
        self.stack.set_visible_child_name(id);
        self.render();
    }

    fn selected(&self) -> &'static str {
        self.sidebar.selected_row().map_or("general", |row| PANES[row.index() as usize].0)
    }

    /// Reads the config and services again, then shows them with the current status.
    fn reload(self: &Rc<Self>) {
        self.render();
        if self.model.status().is_none() || self.loading.replace(true) {
            return;
        }
        let this = self.clone();
        glib::spawn_future_local(async move {
            let (config, services) = gio::spawn_blocking(|| (Client.config(), Client.services())).await.expect("settings requests");
            this.loading.set(false);
            {
                let mut data = this.data.borrow_mut();
                if let Ok(config) = config {
                    data.config = Some(config);
                }
                if let Ok(services) = services {
                    data.services = services;
                }
            }
            this.render();
        });
    }

    fn render(&self) {
        let status = self.model.status();
        let id = self.selected();
        if status.is_none() && id != "about" {
            if self.model.needs_setup() {
                self.unavailable.set_title("Flock Isn't Set Up Yet");
                self.unavailable.set_description(Some("Set Flock up to run your sites and change its settings."));
                self.unavailable_button.set_label("Set Up Flock…");
                self.unavailable_button.set_visible(true);
            } else {
                self.unavailable.set_title("Flock Is Not Running");
                self.unavailable
                    .set_description(Some(&self.model.error().unwrap_or_else(|| "Start the daemon to change its settings.".to_owned())));
                self.unavailable_button.set_label("Start Flock");
                self.unavailable_button.set_visible(true);
                self.unavailable_button.set_sensitive(!self.model.is_restarting());
            }
            self.stack.set_visible_child_name("unavailable");
            return;
        }
        self.stack.set_visible_child_name(id);
        let data = {
            let mut data = self.data.borrow_mut();
            data.status = status;
            data.clone()
        };
        for (pane_id, pane) in &self.panes {
            if *pane_id == id {
                pane.update(&data);
            }
        }
    }
}
