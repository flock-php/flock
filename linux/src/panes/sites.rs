//! Sites: the sites added one by one and those of the project folders, and a site's detail: its
//! address, domains and document root, its PHP and Node.js versions, what serves it, its share and
//! Laravel Boost. Version and root pickers offer the project's pin (or the default) first, then
//! what isolates the site on this machine; the pin menu writes them into the project.

use std::cell::RefCell;
use std::rc::Rc;

use adw::prelude::*;
use base64::Engine;
use gtk::{gdk, gio, glib};

use super::common::{PaneView, ask_link, group, report};
use super::versions::{MenuItem, badge, button, more_menu};
use super::{Data, Pane};
use crate::api::{Client, Config, Error, ExtensionReport, PhpExtension, ShareStatus, SiteChange, SiteStatus, Status};
use crate::links::{self, Link};
use crate::model::AppModel;

#[derive(Clone)]
pub struct SitesPane(Rc<Inner>);

struct Inner {
    stack: gtk::Stack,
    list: PaneView,
    detail: PaneView,
    model: Rc<AppModel>,
    banner: adw::Banner,
    status: RefCell<Option<Status>>,
    config: RefCell<Option<Config>>,
    selected: RefCell<Option<String>>,
    /// The FrankenPHP site that is restarting.
    restarting: RefCell<Option<String>>,
    shown: RefCell<Option<String>>,
}

impl SitesPane {
    pub fn new(model: Rc<AppModel>, banner: adw::Banner) -> Self {
        let list = PaneView::new("Sites you added one by one, plus every folder inside your project folders.");
        let detail = PaneView::new("");
        let stack = gtk::Stack::builder().transition_type(gtk::StackTransitionType::SlideLeftRight).build();
        stack.add_named(&list.scrolled, Some("list"));
        stack.add_named(&detail.scrolled, Some("detail"));
        Self(Rc::new(Inner {
            stack,
            list,
            detail,
            model,
            banner,
            status: RefCell::default(),
            config: RefCell::default(),
            selected: RefCell::default(),
            restarting: RefCell::default(),
            shown: RefCell::default(),
        }))
    }
}

impl SitesPane {
    /// Opens a site's detail (a flock://secure link).
    pub fn select(&self, name: &str) {
        self.0.open(name);
    }
}

impl Pane for SitesPane {
    fn widget(&self) -> gtk::Widget {
        self.0.stack.clone().upcast()
    }

    fn update(&self, data: &Data) {
        self.0.status.replace(data.status.clone());
        self.0.config.replace(data.config.clone());
        self.0.render();
    }
}

impl Inner {
    /// Asks about a flock://secure link (main.rs) once the sites are loaded: nothing changes until
    /// the user confirms.
    fn ask_link(self: &Rc<Self>) {
        let Some(Link::Secure(site)) = self.model.link() else { return };
        let Some(status) = self.status.borrow().clone() else { return };
        self.model.set_link(None);
        let resolution = links::resolve_secure(&site, &status.sites);
        if let Some(name) = &resolution.action {
            self.selected.replace(Some(name.clone()));
        }
        let this = self.clone();
        ask_link(&self.stack, resolution, "Secure", move |name| {
            this.change(&name, SiteChange { secured: Some(true), ..Default::default() })
        });
    }

    fn render(self: &Rc<Self>) {
        self.ask_link();
        let key = format!("{:?}{:?}{:?}{:?}", self.status.borrow(), self.config.borrow(), self.selected.borrow(), self.restarting.borrow());
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));
        let site = self.selected.borrow().as_ref().and_then(|name| self.site(name));
        match site {
            Some(site) => self.render_detail(&site),
            None => {
                self.stack.set_visible_child_name("list");
                self.render_list();
            }
        }
    }

    fn site(&self, name: &str) -> Option<SiteStatus> {
        self.status.borrow().as_ref()?.sites.iter().find(|s| s.name == name).cloned()
    }

    fn open(self: &Rc<Self>, name: &str) {
        self.selected.replace(Some(name.to_owned()));
        self.shown.replace(None);
        self.render();
        self.detail.scrolled.vadjustment().set_value(0.0);
        self.stack.set_visible_child_name("detail");
    }

    fn back(self: &Rc<Self>) {
        self.selected.replace(None);
        self.shown.replace(None);
        self.render();
    }

    /// Runs a request that answers with the status; a failure shows in the banner.
    fn perform(self: &Rc<Self>, f: impl FnOnce(Client) -> Result<Status, Error> + Send + 'static) {
        let banner = self.banner.clone();
        self.model.perform(f, move |e| report(&banner, e));
    }

    fn change(self: &Rc<Self>, name: &str, change: SiteChange) {
        let name = name.to_owned();
        self.perform(move |c| c.update_site(&name, &change));
    }

    /// Asks before a change to the project's files or a removal.
    fn confirm(
        self: &Rc<Self>,
        title: &str,
        body: &str,
        action: &str,
        destructive: bool,
        f: impl FnOnce(Client) -> Result<Status, Error> + Send + 'static,
    ) {
        let dialog = adw::AlertDialog::new(Some(title), Some(body));
        dialog.add_responses(&[("cancel", "Cancel"), ("ok", action)]);
        dialog.set_response_appearance(
            "ok",
            if destructive { adw::ResponseAppearance::Destructive } else { adw::ResponseAppearance::Suggested },
        );
        let (this, f) = (self.clone(), RefCell::new(Some(f)));
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if let Some(f) = f.take().filter(|_| response == "ok") {
                this.perform(f);
            }
        });
    }

    // MARK: the list

    fn render_list(self: &Rc<Self>) {
        let sites = self.status.borrow().as_ref().map(|s| s.sites.clone()).unwrap_or_default();
        let group = group("", "");
        let add = gtk::Button::builder().label("Add Site…").css_classes(["flat"]).build();
        let this = self.clone();
        add.connect_clicked(move |button| {
            let window = button.root().and_downcast::<gtk::Window>();
            let this = this.clone();
            gtk::FileDialog::builder().title("Add Site").accept_label("Add").build().select_folder(
                window.as_ref(),
                gio::Cancellable::NONE,
                move |result| {
                    if let Some(path) = result.ok().and_then(|f| f.path()) {
                        let path = path.to_string_lossy().into_owned();
                        this.perform(move |c| c.link_site(&path));
                    }
                },
            );
        });
        group.set_header_suffix(Some(&add));
        if sites.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title("No sites")
                    .subtitle("Add a single site here, or add a project folder in General.")
                    .build(),
            );
        }
        for site in &sites {
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&site.name))
                .subtitle(glib::markup_escape_text(&site.domain))
                .activatable(true)
                .build();
            row.set_tooltip_text(Some(&site.path));
            if let Some(problem) = problem(site) {
                let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
                warning.add_css_class("warning");
                warning.set_tooltip_text(Some(&problem));
                row.add_suffix(&warning);
            }
            if site.shared() {
                row.add_suffix(&badge("Shared", "success"));
            }
            if site.secured {
                let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
                lock.add_css_class("dim-label");
                lock.set_tooltip_text(Some("Served over HTTPS"));
                row.add_suffix(&lock);
            }
            row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
            let (this, name) = (self.clone(), site.name.clone());
            row.connect_activated(move |_| this.open(&name));
            group.add(&row);
        }
        self.list.set_groups(&[group.upcast()]);
    }

    // MARK: a site's detail

    fn render_detail(self: &Rc<Self>, site: &SiteStatus) {
        let config = self.config.borrow().clone().unwrap_or_default();
        let status = self.status.borrow().clone().unwrap_or_default();
        let mut groups: Vec<gtk::Widget> = vec![self.header(site).upcast()];
        groups.push(self.site_group(site, &status).upcast());
        groups.push(self.share_group(site).upcast());
        groups.push(self.versions_group(site, &config).upcast());
        groups.push(self.server_group(site).upcast());
        if site.boost == "missing" || site.boost == "outdated" {
            groups.push(self.boost_group(site).upcast());
        }
        self.detail.set_groups(&groups);
    }

    fn header(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title("Sites").activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&site.name))
            .subtitle(glib::markup_escape_text(&format!("{}\n{}", site.url.as_deref().unwrap_or(&site.domain), site.path)))
            .subtitle_selectable(true)
            .build();
        if site.parked {
            title.add_suffix(&badge("Project folder", ""));
        }
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let icon_button =
            |icon: &str, tooltip: &str| gtk::Button::builder().icon_name(icon).tooltip_text(tooltip).css_classes(["flat"]).build();
        let open = icon_button("dev.flock.Flock-sites-symbolic", "Open in the browser");
        open.set_sensitive(site.url.is_some());
        let url = site.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let folder = icon_button("folder-symbolic", "Open the project folder");
        let path = site.path.clone();
        folder.connect_clicked(move |b| launch_file(b, &path));
        actions.append(&folder);
        let extensions = icon_button("application-x-addon-symbolic", "PHP extensions");
        let (this, s) = (self.clone(), site.clone());
        extensions.connect_clicked(move |_| this.show_extensions(&s));
        actions.append(&extensions);
        actions.append(&self.pin_menu(site));
        if !site.parked {
            let remove = icon_button("user-trash-symbolic", "Remove site");
            let (this, s) = (self.clone(), site.clone());
            remove.connect_clicked(move |_| {
                let name = s.name.clone();
                this.confirm(
                    &format!("Remove {}?", s.name),
                    &format!("Flock stops serving {}. The project in {} is not touched.", s.domain, s.path),
                    "Remove",
                    true,
                    move |c| c.unlink_site(&name),
                );
            });
            actions.append(&remove);
        }
        title.add_suffix(&actions);
        header.add(&title);
        if let Some(problem) = &site.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    fn site_group(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> adw::PreferencesGroup {
        let group = group(
            "Site",
            "The document root is the folder Flock serves the site from. One picked here applies on this machine only; pin it to write it into .flockrc for everyone.",
        );
        let https = adw::SwitchRow::builder().title("HTTPS").active(site.secured).build();
        let (this, name) = (self.clone(), site.name.clone());
        https.connect_active_notify(move |row| this.change(&name, SiteChange { secured: Some(row.is_active()), ..Default::default() }));
        group.add(&https);

        for domain in site.domains.iter().flatten() {
            let title = if domain.alias { format!("{} (alias)", domain.host) } else { domain.host.clone() };
            let row = adw::ActionRow::builder().title(glib::markup_escape_text(&title)).build();
            row.add_prefix(&gtk::Image::from_icon_name("network-workgroup-symbolic"));
            if let Some(problem) = &domain.problem {
                row.set_subtitle(&glib::markup_escape_text(problem));
                row.add_css_class("warning");
            } else if let Some(url) = domain.url.clone() {
                row.set_activatable(true);
                row.connect_activated(move |r| launch_uri(r, &url));
            }
            let remove = gtk::Button::builder()
                .icon_name("list-remove-symbolic")
                .tooltip_text(format!("Remove {}", domain.host))
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, s, entry, host) = (self.clone(), site.name.clone(), domain.entry.clone(), domain.host.clone());
            remove.connect_clicked(move |_| {
                let (s2, e2) = (s.clone(), entry.clone());
                this.confirm(&format!("Remove {host}?"), &format!("{host} is no longer served by {s}."), "Remove", true, move |c| {
                    c.remove_domain(&s2, &e2)
                });
            });
            row.add_suffix(&remove);
            group.add(&row);
        }
        let add =
            adw::ActionRow::builder().title("Add Domain…").subtitle("Extra domains: subdomains and aliases").activatable(true).build();
        add.add_prefix(&gtk::Image::from_icon_name("list-add-symbolic"));
        let (this, s, dns) = (self.clone(), site.name.clone(), status.dns.clone());
        add.connect_activated(move |_| this.add_domain(&s, &dns));
        group.add(&add);
        if !status.dns.wildcard && !status.dns.wildcard_problem.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&format!(
                        "Subdomains only work when you add them here: {}.",
                        status.dns.wildcard_problem
                    )))
                    .css_classes(["warning"])
                    .build(),
            );
        }

        group.add(&self.root_row(site));
        if let Some(hint) = root_hint(site) {
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&hint))
                .css_classes([if site.root_state.as_deref() == Some("empty") { "dim-label" } else { "warning" }])
                .build();
            if site.root_state.as_deref().unwrap_or_default().is_empty() {
                let (this, name) = (self.clone(), site.name.clone());
                row.add_suffix(&button("Use .flockrc", false, move || {
                    this.change(&name, SiteChange { root: Some(String::new()), ..Default::default() })
                }));
            }
            group.add(&row);
        }
        group
    }

    fn add_domain(self: &Rc<Self>, site: &str, dns: &crate::api::DnsStatus) {
        let tld = if dns.tld.is_empty() { "test" } else { &dns.tld };
        let help = if dns.wildcard || dns.tld.is_empty() {
            format!(
                "Every subdomain up to {} levels below the site already works. Add a full name like {site}-2.{tld} for an alias, or a subdomain (tenant1, a.b) so it is listed here and also resolves on Windows machines without wildcard DNS.",
                dns.max_depth.max(1)
            )
        } else {
            format!(
                "Wildcard subdomains don't work here ({}), so add each subdomain you use (tenant1, a.b). A full name adds an alias.",
                dns.wildcard_problem
            )
        };
        let entry = gtk::Entry::builder().placeholder_text(format!("tenant1, a.b or {site}-2.{tld}")).activates_default(true).build();
        let dialog = adw::AlertDialog::new(Some(&format!("Add a domain to {site}")), Some(&help));
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", "Cancel"), ("add", "Add")]);
        dialog.set_response_appearance("add", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("add"));
        let (this, site) = (self.clone(), site.to_owned());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let domain = entry.text().trim().to_owned();
            if response == "add" && !domain.is_empty() {
                let site = site.clone();
                this.perform(move |c| c.add_domain(&site, &domain));
            }
        });
    }

    /// The document root: the project's or the detected one first (following the project), the
    /// folders Flock found and the one set here (this machine only), then any folder.
    fn root_row(self: &Rc<Self>, site: &SiteStatus) -> adw::ComboRow {
        let follow = match (site.root_pinned_here(), site.root_source.as_str(), site.root_state.as_deref()) {
            (true, _, _) => format!("Use .flockrc ({})", root_label(&site.root_pinned)),
            (_, "default", Some("not_found")) => "Automatic (none found)".to_owned(),
            (_, "default", _) => format!("Automatic ({})", root_label(&site.root)),
            _ => "Automatic".to_owned(),
        };
        let mut folders = site.root_suggestions.clone().unwrap_or_default();
        if site.root_source == "isolated" && !folders.contains(&site.root) {
            folders.push(site.root.clone());
        }
        let mut labels = vec![follow];
        labels.extend(folders.iter().map(|f| root_label(f)));
        labels.push("Choose Folder…".to_owned());
        let model = gtk::StringList::new(&labels.iter().map(String::as_str).collect::<Vec<_>>());
        let row = adw::ComboRow::builder().title("Document root").model(&model).build();
        let selected = if site.root_source == "isolated" { folders.iter().position(|f| *f == site.root).map_or(0, |i| i + 1) } else { 0 };
        row.set_selected(selected as u32);
        let (this, s) = (self.clone(), site.clone());
        row.connect_selected_notify(move |row| {
            let index = row.selected() as usize;
            if index == folders.len() + 1 {
                this.choose_root(&s);
            } else {
                let root = if index == 0 { String::new() } else { folders[index - 1].clone() };
                this.change(&s.name, SiteChange { root: Some(root), ..Default::default() });
            }
        });
        row
    }

    fn choose_root(self: &Rc<Self>, site: &SiteStatus) {
        let window = self.stack.root().and_downcast::<gtk::Window>();
        let dialog = gtk::FileDialog::builder()
            .title(format!("Choose the folder with the app's index.php in {}", site.name))
            .accept_label("Choose")
            .initial_folder(&gio::File::for_path(&site.path))
            .build();
        let (this, s) = (self.clone(), site.clone());
        dialog.select_folder(window.as_ref(), gio::Cancellable::NONE, move |result| {
            let Some(folder) = result.ok().and_then(|f| f.path()) else {
                this.shown.replace(None);
                this.render();
                return;
            };
            let project = std::fs::canonicalize(&s.path).unwrap_or_else(|_| s.path.clone().into());
            let chosen = std::fs::canonicalize(&folder).unwrap_or(folder.clone());
            match chosen.strip_prefix(&project) {
                Ok(rest) => {
                    let root = if rest.as_os_str().is_empty() { ".".to_owned() } else { rest.to_string_lossy().into_owned() };
                    this.change(&s.name, SiteChange { root: Some(root), ..Default::default() });
                }
                Err(_) => report(
                    &this.banner,
                    Some(format!("{} isn't in {}. The document root must be a folder in the project.", folder.display(), s.path)),
                ),
            }
        });
    }

    fn share_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Share", "");
        let name = site.name.clone();
        let stop = {
            let (this, name) = (self.clone(), name.clone());
            move || {
                let (this2, name) = (this.clone(), name.clone());
                let banner = this.banner.clone();
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(move || Client.stop_share(&name)).await.expect("share request");
                    report(&banner, result.err().map(|e| e.to_string()));
                    this2.model.refresh();
                });
            }
        };
        match &site.share {
            None => {
                group.set_description(Some("Show the site at a temporary public address to preview work in progress, never for production. Visitors don't get debug pages, dumps or the dev tools."));
                let row = adw::ActionRow::new();
                let (this, n) = (self.clone(), name.clone());
                let share = button("Share…", false, move || this.ask_share(&n));
                share.set_sensitive(site.url.is_some());
                row.add_suffix(&share);
                group.add(&row);
            }
            Some(share) if share.state == "running" => self.running_share(&group, &name, share, stop),
            Some(share) if share.state == "failed" => {
                let row = adw::ActionRow::builder()
                    .title(glib::markup_escape_text(if share.error.is_empty() { "Sharing failed." } else { &share.error }))
                    .css_classes(["warning"])
                    .build();
                let (this, n, allow, debug) = (self.clone(), name.clone(), share.allow.clone().unwrap_or_default(), share.debug);
                row.add_suffix(&button("Try Again", false, move || this.start_share(&n, allow.clone(), debug)));
                row.add_suffix(&button("Dismiss", false, stop));
                group.add(&row);
            }
            Some(share) => {
                let row = adw::ActionRow::builder()
                    .title(if share.state == "building" { "Building assets…" } else { "Starting the tunnel…" })
                    .build();
                row.add_prefix(&gtk::Spinner::builder().spinning(true).build());
                row.add_suffix(&button("Stop", false, stop));
                group.add(&row);
            }
        }
        group
    }

    fn running_share(self: &Rc<Self>, group: &adw::PreferencesGroup, name: &str, share: &ShareStatus, stop: impl Fn() + 'static) {
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&share.url)).title_selectable(true).build();
        let url = share.url.clone();
        row.add_suffix(&button("Copy", false, {
            let url = url.clone();
            let row = row.clone();
            move || row.clipboard().set_text(&url)
        }));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        open.connect_clicked(move |b| launch_uri(b, &url));
        row.add_suffix(&open);
        group.add(&row);

        if let Some(texture) = base64::engine::general_purpose::STANDARD
            .decode(&share.qr)
            .ok()
            .and_then(|png| gdk::Texture::from_bytes(&glib::Bytes::from_owned(png)).ok())
        {
            let picture = gtk::Picture::builder()
                .paintable(&texture)
                .width_request(160)
                .height_request(160)
                .can_shrink(true)
                .halign(gtk::Align::Start)
                .build();
            picture.set_tooltip_text(Some(&format!("QR code of {}", share.url)));
            let row = adw::PreferencesRow::builder().activatable(false).child(&picture).build();
            picture.set_margin_top(12);
            picture.set_margin_bottom(12);
            picture.set_margin_start(12);
            group.add(&row);
        }
        let mut notes = Vec::new();
        if let Some(expires) =
            share.expires_at.as_deref().and_then(|t| glib::DateTime::from_iso8601(t, None).ok()).and_then(|t| t.to_local().ok())
        {
            notes.push(format!("Ends at {}", expires.format("%H:%M").map(|s| s.to_string()).unwrap_or_default()));
        }
        let allow = share.allow.clone().unwrap_or_default();
        if !allow.is_empty() {
            notes.push(format!("Only for: {}", allow.join(", ")));
        }
        if share.debug {
            notes.push("Debug mode is on: visitors see stack traces".to_owned());
        }
        if !notes.is_empty() {
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(&notes.join("\n"))).css_classes(["dim-label"]).build());
        }
        for warning in [&share.warning, &share.rebuild_error].into_iter().filter(|w| !w.is_empty()) {
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(warning)).css_classes(["warning"]).build());
        }
        let actions = adw::ActionRow::new();
        if share.can_rebuild {
            let (this, n) = (self.clone(), name.to_owned());
            let rebuild = button(if share.rebuilding { "Rebuilding…" } else { "Rebuild Assets" }, false, move || {
                let (this, n) = (this.clone(), n.clone());
                glib::spawn_future_local(async move {
                    let result = gio::spawn_blocking(move || Client.rebuild_share(&n)).await.expect("share request");
                    report(&this.banner, result.err().map(|e| e.to_string()));
                    this.model.refresh();
                });
            });
            rebuild.set_sensitive(!share.rebuilding);
            rebuild.set_tooltip_text(Some("Runs npm run build again, so visitors see your latest changes when they reload"));
            actions.add_suffix(&rebuild);
        }
        actions.add_suffix(&button("Stop Sharing", false, stop));
        group.add(&actions);
    }

    /// Asks who may open the share (anyone with the link, or only the emails listed) and whether
    /// visitors see the app's debug mode.
    fn ask_share(self: &Rc<Self>, name: &str) {
        let allow = adw::EntryRow::builder().title("Only these people (optional)").build();
        allow.set_tooltip_text(Some("colleague@acme.com, *@client.com"));
        let debug = adw::SwitchRow::builder()
            .title("Show error details to visitors")
            .subtitle("Turns the app's debug mode on for this share, so visitors see stack traces. Only for people you trust.")
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&allow);
        list.append(&debug);
        let dialog = adw::AlertDialog::new(
            Some(&format!("Share {name}")),
            Some(
                "Anyone with the link can open the site unless you list email addresses — Cloudflare then emails them a code. The link stops working after 8 hours or when you stop sharing.",
            ),
        );
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("share", "Share")]);
        dialog.set_response_appearance("share", adw::ResponseAppearance::Suggested);
        let (this, name) = (self.clone(), name.to_owned());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "share" {
                let emails = allow.text().split([',', ' ', '\n']).map(str::trim).filter(|e| !e.is_empty()).map(str::to_owned).collect();
                this.start_share(&name, emails, debug.is_active());
            }
        });
    }

    fn start_share(self: &Rc<Self>, name: &str, allow: Vec<String>, debug: bool) {
        let (this, name) = (self.clone(), name.to_owned());
        glib::spawn_future_local(async move {
            let result = gio::spawn_blocking(move || Client.start_share(&name, &allow, debug)).await.expect("share request");
            report(&this.banner, result.err().map(|e| e.to_string()));
            this.model.refresh();
        });
    }

    fn versions_group(self: &Rc<Self>, site: &SiteStatus, config: &Config) -> adw::PreferencesGroup {
        let group = group(
            "Versions",
            "A version picked here applies on this machine only. To set it for everyone working on the project, pin it: PHP goes into .flockrc, Node.js into .nvmrc.",
        );
        let php: Vec<String> = config.php.iter().flatten().map(|p| p.version.clone()).collect();
        let mut node: Vec<String> = config.node.iter().flatten().map(|v| major(v)).collect();
        node.dedup();
        let (this, name) = (self.clone(), site.name.clone());
        group.add(&version_row(
            "PHP",
            &site.php,
            &site.php_source,
            &site.php_pinned,
            &site.php_file,
            &config.default_php,
            &php,
            move |v| this.change(&name, SiteChange { php: Some(v), ..Default::default() }),
        ));
        let (this, name) = (self.clone(), site.name.clone());
        group.add(&version_row(
            "Node",
            &major(&site.node),
            &site.node_source,
            &major(&site.node_pinned),
            &site.node_file,
            &config.default_node,
            &node,
            move |v| this.change(&name, SiteChange { node: Some(v), ..Default::default() }),
        ));
        if site.php_source == "isolated" && !site.php_pinned.is_empty() && site.php_pinned != site.php {
            let file = file_name(&site.php_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&format!(
                    "PHP {} is isolated here; the project pins {} in {file}.",
                    site.php, site.php_pinned
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&format!("Use {file}"), false, move || {
                this.change(&name, SiteChange { php: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if site.node_source == "isolated" && !site.node_pinned.is_empty() && major(&site.node_pinned) != major(&site.node) {
            let file = file_name(&site.node_file);
            let row = adw::ActionRow::builder()
                .title(glib::markup_escape_text(&format!(
                    "Node {} is isolated here; the project pins {} in {file}.",
                    site.node, site.node_pinned
                )))
                .css_classes(["warning"])
                .build();
            let (this, name) = (self.clone(), site.name.clone());
            row.add_suffix(&button(&format!("Use {file}"), false, move || {
                this.change(&name, SiteChange { node: Some(String::new()), ..Default::default() })
            }));
            group.add(&row);
        }
        if !site.node_shadowed.is_empty() {
            group.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&format!(".flockrc's Node {} wins over {}.", site.node_pinned, site.node_shadowed)))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        group
    }

    fn server_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let footer = if site.server == "frankenphp" && !site.octane_available {
            "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM. For worker mode, install Laravel Octane in the project (composer require laravel/octane)."
        } else {
            "FrankenPHP needs PHP 8.2 or newer. Requests that ask for Xdebug still go to PHP-FPM."
        };
        let group = group("Server", footer);
        let franken = site.server == "frankenphp";
        let runs_on = adw::ComboRow::builder()
            .title("Runs on")
            .model(&gtk::StringList::new(&["PHP-FPM", "FrankenPHP"]))
            .selected(u32::from(franken))
            .build();
        runs_on.set_sensitive(site.supports_frankenphp() || franken);
        let (this, name) = (self.clone(), site.name.clone());
        runs_on.connect_selected_notify(move |row| {
            let server = if row.selected() == 1 { "frankenphp" } else { "fastcgi" };
            this.change(&name, SiteChange { server: Some(server.to_owned()), ..Default::default() });
        });
        group.add(&runs_on);
        if !franken {
            return group;
        }
        let octane = site.worker == "octane";
        let mode = adw::ComboRow::builder()
            .title("Mode")
            .model(&gtk::StringList::new(&["Classic", "Laravel Octane"]))
            .selected(u32::from(octane))
            .build();
        mode.set_sensitive(site.octane_available || octane);
        let (this, name) = (self.clone(), site.name.clone());
        mode.connect_selected_notify(move |row| {
            let worker = if row.selected() == 1 { "octane" } else { "" };
            this.change(&name, SiteChange { worker: Some(worker.to_owned()), ..Default::default() });
        });
        group.add(&mode);

        if let Some(franken) = &site.frankenphp {
            let (text, class) = match franken.state.as_str() {
                "running" => {
                    let mut text = format!("Running FrankenPHP {} with PHP {}", franken.version, franken.php_version);
                    if octane {
                        text.push_str(" · worker mode");
                    }
                    (text, "dim-label")
                }
                "installing" => {
                    let mut text = format!("Downloading FrankenPHP for PHP {}…", site.php);
                    if (0.0..=1.0).contains(&franken.progress) {
                        text.push_str(&format!(" {}%", (franken.progress * 100.0).round()));
                    }
                    (format!("{text}\nPHP-FPM serves the site until it's ready."), "dim-label")
                }
                _ => (format!("{}\nPHP-FPM serves the site meanwhile.", franken.problem), "warning"),
            };
            group.add(&adw::ActionRow::builder().title(glib::markup_escape_text(text.trim())).css_classes([class]).build());
        }
        let actions = adw::ActionRow::new();
        let restarting = self.restarting.borrow().as_deref() == Some(site.name.as_str());
        if restarting {
            actions.add_suffix(&gtk::Spinner::builder().spinning(true).build());
        }
        let (this, name) = (self.clone(), site.name.clone());
        let restart = button("Restart", false, move || this.restart(&name));
        restart.set_sensitive(site.frankenphp.as_ref().is_some_and(|f| f.state == "running") && !restarting);
        restart.set_tooltip_text(Some("Restart FrankenPHP, e.g. so Octane's workers load changed code"));
        actions.add_suffix(&restart);
        let log = site
            .frankenphp
            .as_ref()
            .map(|f| f.log.clone())
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| format!("frankenphp-{}", site.name));
        let show_log = gtk::Button::builder().label("Show Log").valign(gtk::Align::Center).action_name("app.open").build();
        show_log.set_action_target_value(Some(&format!("logs/services/{log}").to_variant()));
        actions.add_suffix(&show_log);
        group.add(&actions);
        group
    }

    fn restart(self: &Rc<Self>, name: &str) {
        self.restarting.replace(Some(name.to_owned()));
        self.render();
        let (this, banner, n) = (self.clone(), self.banner.clone(), name.to_owned());
        self.model.perform(
            move |c| c.restart_site(&n),
            move |e| {
                this.restarting.replace(None);
                report(&banner, e);
                this.render();
            },
        );
    }

    fn boost_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let missing = site.boost == "missing";
        let group = group(
            "Laravel Boost",
            if missing {
                "The project uses Laravel Boost. Adding Flock to its guidelines tells AI agents that Flock serves the app and picks its PHP version."
            } else {
                "Flock's guideline in the project's Boost guidelines is outdated."
            },
        );
        let row = adw::ActionRow::new();
        let (this, s) = (self.clone(), site.clone());
        row.add_suffix(&button(if missing { "Add Flock to its guidelines…" } else { "Update it…" }, false, move || {
            let name = s.name.clone();
            this.confirm(
                &if missing { format!("Add Flock to {}'s Boost guidelines?", s.name) } else { format!("Update Flock's guideline in {}?", s.name) },
                &format!(
                    "Writes .ai/guidelines/flock.md in {} and runs php artisan boost:update, so AI agents know Flock serves the app and picks its PHP version. Commit it with the rest of .ai/guidelines. This changes files in your project.",
                    s.path
                ),
                if missing { "Add" } else { "Update" },
                false,
                move |c| c.add_boost_guideline(&name),
            );
        }));
        group.add(&row);
        group
    }

    /// Pins versions and the document root into the project, or removes them again.
    fn pin_menu(self: &Rc<Self>, site: &SiteStatus) -> gtk::MenuButton {
        let node = major(&site.node);
        let node_file = if site.node_pinned_here() { file_name(&site.node_file) } else { ".nvmrc".to_owned() };
        let can_pin_php = !(site.php_source == "project" && site.php_pinned_here());
        let can_pin_node = !node.is_empty() && !(site.node_source == "project" && site.node_pinned_here());
        let state = site.root_state.clone().unwrap_or_default();
        let can_pin_root = (state.is_empty() || state == "empty") && site.root_source != "project";
        let mut items: Vec<MenuItem> = Vec::new();
        let s = site.clone();
        let pin = {
            let this = self.clone();
            move |title: String, body: String, tool: &'static str, version: String| -> Box<dyn Fn()> {
                let (this, name) = (this.clone(), s.name.clone());
                Box::new(move || {
                    let (n, v) = (name.clone(), version.clone());
                    this.confirm(&title, &body, "Pin", false, move |c| c.pin(&n, tool, &v));
                })
            }
        };
        if can_pin_php {
            items.push((
                format!("Pin PHP {} to Project…", site.php),
                true,
                false,
                pin(
                    format!("Pin PHP {} to {}?", site.php, site.name),
                    format!("Writes php={} to .flockrc in {}. Commit it, and everyone using Flock on this project gets PHP {}. This changes a file in your project.", site.php, site.path, site.php),
                    "php",
                    site.php.clone(),
                ),
            ));
        }
        if can_pin_node {
            items.push((
                format!("Pin Node {node} to Project…"),
                true,
                false,
                pin(
                    format!("Pin Node {node} to {}?", site.name),
                    format!("Writes {node} to {node_file} in {}. Commit it, and Flock, nvm and other Node tools use Node {node} for this project. This changes a file in your project.", site.path),
                    "node",
                    node.clone(),
                ),
            ));
        }
        if can_pin_root {
            let label = root_label(&site.root);
            items.push((
                format!("Pin Document Root ({label}) to Project…"),
                true,
                false,
                pin(
                    format!("Pin the document root {label} to {}?", site.name),
                    format!("Writes root={} to .flockrc in {}. Commit it, and everyone using Flock on this project serves it from {label}. This changes a file in your project.", site.root, site.path),
                    "root",
                    site.root.clone(),
                ),
            ));
        }
        let unpin = |label: String, title: String, body: String, tool: &'static str| -> (String, bool, bool, Box<dyn Fn()>) {
            let (this, name) = (self.clone(), site.name.clone());
            (
                label,
                true,
                true,
                Box::new(move || {
                    let n = name.clone();
                    this.confirm(&title, &body, "Unpin", true, move |c| c.unpin(&n, tool));
                }),
            )
        };
        if site.php_pinned_here() {
            items.push(unpin(
                format!("Unpin PHP ({})…", file_name(&site.php_file)),
                format!("Unpin PHP from {}?", site.name),
                format!(
                    "Removes the PHP version from {}. The site then uses Flock's default PHP. This changes files in your project.",
                    site.php_file
                ),
                "php",
            ));
        }
        if site.node_pinned_here() {
            items.push(unpin(
                format!("Unpin Node ({})…", file_name(&site.node_file)),
                format!("Unpin Node from {}?", site.name),
                format!("Removes the Node.js version from .flockrc and deletes .nvmrc / .node-version in {}. The site then uses Flock's default Node.js. This changes files in your project.", site.path),
                "node",
            ));
        }
        if site.root_pinned_here() {
            items.push(unpin(
                "Unpin Document Root (.flockrc)…".to_owned(),
                format!("Unpin the document root from {}?", site.name),
                format!(
                    "Removes root= from .flockrc in {}. Flock then finds the document root itself. This changes files in your project.",
                    site.path
                ),
                "root",
            ));
        }
        let enabled = !items.is_empty();
        let menu = more_menu(items);
        menu.set_icon_name("view-pin-symbolic");
        menu.set_tooltip_text(Some("Pin versions and the document root to the project"));
        menu.set_sensitive(enabled);
        menu
    }

    // MARK: PHP extensions

    fn show_extensions(self: &Rc<Self>, site: &SiteStatus) {
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        let status = gtk::Label::builder().label("Loading…").css_classes(["dim-label"]).build();
        let column = gtk::Box::builder()
            .orientation(gtk::Orientation::Vertical)
            .spacing(12)
            .margin_top(12)
            .margin_bottom(18)
            .margin_start(18)
            .margin_end(18)
            .build();
        column.append(&status);
        column.append(&list);
        column.append(
            &gtk::Label::builder()
                .label("Requiring an extension in composer.json makes composer install refuse a server without it, instead of the app failing there.")
                .wrap(true)
                .xalign(0.0)
                .css_classes(["caption", "dim-label"])
                .build(),
        );
        let view = adw::ToolbarView::new();
        view.add_top_bar(&adw::HeaderBar::new());
        view.set_content(Some(&gtk::ScrolledWindow::builder().child(&column).vexpand(true).build()));
        let dialog = adw::Dialog::builder()
            .title(format!("PHP extensions of {}", site.name))
            .content_width(560)
            .content_height(440)
            .child(&view)
            .build();
        dialog.present(Some(&self.stack));

        let show: Show = Rc::new({
            let (list, status, path) = (list.clone(), status.clone(), site.path.clone());
            move |result: Result<ExtensionReport, Error>, again: Rc<dyn Fn(String)>| {
                while let Some(child) = list.first_child() {
                    list.remove(&child);
                }
                match result {
                    Err(e) => {
                        status.set_label(&e.to_string());
                        status.add_css_class("error");
                    }
                    Ok(report) => {
                        status.remove_css_class("error");
                        let extensions = report.extensions.clone().unwrap_or_default();
                        status.set_label(&if extensions.is_empty() {
                            "The project requires no PHP extensions.".to_owned()
                        } else if report.laravel {
                            format!("PHP {} · Laravel app", report.php)
                        } else {
                            format!("PHP {}", report.php)
                        });
                        for ext in &extensions {
                            list.append(&extension_row(ext, &report.php, &path, again.clone()));
                        }
                    }
                }
            }
        });
        load_extensions(site.name.clone(), show);
    }
}

/// Shows an extension report; the callback requires an extension, then shows the new report.
type Show = Rc<dyn Fn(Result<ExtensionReport, Error>, Rc<dyn Fn(String)>)>;

/// Loads a site's extension report and shows it.
fn load_extensions(name: String, show: Show) {
    glib::spawn_future_local(async move {
        let n = name.clone();
        let result = gio::spawn_blocking(move || Client.site_extensions(&n)).await.expect("extensions request");
        show(result, requirer(name, show.clone()));
    });
}

/// Requires an extension in the project (composer require), then shows the new report.
fn requirer(name: String, show: Show) -> Rc<dyn Fn(String)> {
    Rc::new(move |ext: String| {
        let (name, show) = (name.clone(), show.clone());
        glib::spawn_future_local(async move {
            let n = name.clone();
            let result = gio::spawn_blocking(move || Client.require_extension(&n, &ext)).await.expect("extensions request");
            show(result, requirer(name, show.clone()));
        });
    })
}

fn extension_row(ext: &PhpExtension, php: &str, path: &str, require: Rc<dyn Fn(String)>) -> adw::ActionRow {
    let required_by = ext.required_by.clone().unwrap_or_default();
    let declared = !required_by.is_empty();
    let mut detail =
        if declared { format!("Required by {}", required_by.join(", ")) } else { format!("Not required, but used: {}", ext.hint) };
    if !ext.available {
        detail.push_str(&format!(" — Flock's PHP {php} doesn't include it"));
    }
    let row = adw::ActionRow::builder().title(glib::markup_escape_text(&ext.name)).subtitle(glib::markup_escape_text(&detail)).build();
    let (icon, class) = match (ext.available, declared) {
        (false, _) => ("process-stop-symbolic", "error"),
        (true, true) => ("emblem-ok-symbolic", "success"),
        (true, false) => ("dialog-warning-symbolic", "warning"),
    };
    let icon = gtk::Image::from_icon_name(icon);
    icon.add_css_class(class);
    row.add_prefix(&icon);
    if !declared {
        let add = gtk::Button::builder().label("Add to composer.json").valign(gtk::Align::Center).build();
        let (name, path) = (ext.name.clone(), path.to_owned());
        add.connect_clicked(move |button| {
            let dialog = adw::AlertDialog::new(
                Some(&format!("Require ext-{name} in composer.json?")),
                Some(&format!("Runs composer require ext-{name}:* in {path}, which changes composer.json and composer.lock. Commit them with the project.")),
            );
            dialog.add_responses(&[("cancel", "Cancel"), ("require", "Require")]);
            dialog.set_response_appearance("require", adw::ResponseAppearance::Suggested);
            let (name, require, button) = (name.clone(), require.clone(), button.clone());
            dialog.choose(Some(&button.clone()), gio::Cancellable::NONE, move |response| {
                if response == "require" {
                    button.set_sensitive(false);
                    button.set_label("Adding…");
                    require(name.clone());
                }
            });
        });
        row.add_suffix(&add);
    }
    row
}

/// What a site's list row warns about: the site, its document root, a domain, or its FrankenPHP.
fn problem(site: &SiteStatus) -> Option<String> {
    if let Some(p) = site.problem.clone().or_else(|| site.notice.clone()) {
        return Some(p);
    }
    if let Some(domain) = site.domains.iter().flatten().find(|d| d.problem.is_some()) {
        return Some(format!("{}: {}", domain.host, domain.problem.clone().unwrap_or_default()));
    }
    site.frankenphp
        .as_ref()
        .filter(|f| site.server == "frankenphp" && (f.state == "failed" || f.state == "unavailable") && !f.problem.is_empty())
        .map(|f| f.problem.clone())
}

/// Why the site isn't served from its root, or a root set here over the project's.
fn root_hint(site: &SiteStatus) -> Option<String> {
    match site.root_state.as_deref().unwrap_or_default() {
        "not_found" => Some("No public/, www/, web/ or webroot/ with an index.php was found, so Flock shows a page instead of the site. Choose the folder with the app's index.php.".to_owned()),
        "missing" => Some(format!("Choose the folder with the app's index.php, or create {}.", site.root)),
        "empty" => Some(format!("{} has no index.php or index.html, so Flock shows a page instead of the site until it does.", root_label(&site.root))),
        _ if site.root_source == "isolated" && site.root_pinned_here() => {
            Some(format!("{} is set here; the project sets {} in .flockrc.", root_label(&site.root), root_label(&site.root_pinned)))
        }
        _ => None,
    }
}

/// The project's pin ("Use .nvmrc (Node 20)") or the default first, following the project; then
/// the installed versions, which isolate the site.
#[allow(clippy::too_many_arguments)]
fn version_row(
    label: &str,
    current: &str,
    source: &str,
    pinned: &str,
    file: &str,
    default: &str,
    installed: &[String],
    change: impl Fn(String) + 'static,
) -> adw::ComboRow {
    let follow = if !pinned.is_empty() {
        format!("Use {} ({label} {pinned})", file_name(file))
    } else if default.is_empty() {
        "Default".to_owned()
    } else {
        format!("Default ({label} {default})")
    };
    let mut versions = installed.to_vec();
    if source == "isolated" && !current.is_empty() && !versions.iter().any(|v| v == current) {
        versions.push(current.to_owned());
    }
    let mut labels = vec![follow];
    labels.extend(versions.iter().map(|v| format!("{label} {v}")));
    let model = gtk::StringList::new(&labels.iter().map(String::as_str).collect::<Vec<_>>());
    let row = adw::ComboRow::builder().title(if label == "Node" { "Node.js" } else { label }).model(&model).build();
    row.set_tooltip_text(Some(&match source {
        "isolated" => format!("{label} {current} is set for this site on this machine (isolated)."),
        "project" => format!("The project pins {label} {pinned} in {}.", file_name(file)),
        _ => format!("The site follows Flock's default {label} version."),
    }));
    let selected = if source == "isolated" { versions.iter().position(|v| v == current).map_or(0, |i| i + 1) } else { 0 };
    row.set_selected(selected as u32);
    row.connect_selected_notify(move |row| {
        let index = row.selected() as usize;
        change(if index == 0 { String::new() } else { versions[index - 1].clone() });
    });
    row
}

/// "src/www", or "Project folder" for ".".
fn root_label(root: &str) -> String {
    if root == "." { "Project folder".to_owned() } else { root.to_owned() }
}

fn file_name(path: &str) -> String {
    std::path::Path::new(path).file_name().map_or(path.to_owned(), |n| n.to_string_lossy().into_owned())
}

/// "22.11.0" → "22"; pins and isolations are majors.
fn major(version: &str) -> String {
    version.split('.').next().unwrap_or_default().to_owned()
}

fn launch_uri(widget: &impl IsA<gtk::Widget>, uri: &str) {
    let window = widget.root().and_downcast::<gtk::Window>();
    gtk::UriLauncher::new(uri).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}

fn launch_file(widget: &impl IsA<gtk::Widget>, path: &str) {
    let window = widget.root().and_downcast::<gtk::Window>();
    gtk::FileLauncher::new(Some(&gio::File::for_path(path))).launch(window.as_ref(), gio::Cancellable::NONE, |_| {});
}
