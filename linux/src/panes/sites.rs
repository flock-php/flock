//! Sites: the sites added one by one and those of the project folders, and a site's detail: its
//! address, domains and document root, its PHP and Node.js versions, what serves it, its share and
//! Laravel Boost. Version and root pickers offer the project's pin (or the default) first, then
//! what isolates the site on this machine; the pin menu writes them into the project.
//!
//! A site's git worktrees are listed under it. A worktree's detail shows its setup and database
//! instead of settings: it uses its site's.
//!
//! Proxies (`flock proxy`) follow the sites in a group of their own; a proxy's detail shows what
//! answers at its upstream, its HTTPS and its upstream. Site and proxy names never clash (the
//! daemon refuses it), so one selection covers both.

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use adw::prelude::*;
use base64::Engine;
use gtk::{gdk, gio, glib};

use super::common::{PaneView, ask_link, code_block, group, info_row, report, status_dot};
use super::suffix::{Draft, INHERIT, PRESETS, SuffixPicker};
use super::versions::{MenuItem, badge, button, more_menu};
use super::worktree_job;
use super::{Data, Pane};
use crate::api::{
    Branches, Client, Config, DnsStatus, Error, ExtensionReport, PhpExtension, ProxyStatus, ShareStatus, SiteChange, SiteDomain,
    SiteStatus, Status, WorktreeStatus,
};
use crate::installation;
use crate::links::{self, Link};
use crate::model::AppModel;
use crate::prefs;
use crate::ui;

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
    /// The sites whose worktrees the list shows (prefs.rs).
    expanded: RefCell<Vec<String>>,
    /// The folder of the worktree being removed.
    removing: RefCell<Option<String>>,
}

/// A worktree to remove: its folder, name, what it is listed as and its site.
#[derive(Clone)]
struct Removal {
    path: String,
    name: String,
    label: String,
    parent: String,
}

/// The database modes of a worktree, and how the Database rows name them.
const DB_MODES: [&str; 3] = ["shared", "copy", "fresh"];
const DB_CHOICES: [&str; 3] = ["The site's", "A copy of the site's", "A new, empty one"];

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
            expanded: RefCell::new(prefs::load().expanded_sites),
            removing: RefCell::default(),
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
        let key = format!(
            "{:?}{:?}{:?}{:?}{:?}",
            self.status.borrow(),
            self.config.borrow(),
            self.selected.borrow(),
            self.restarting.borrow(),
            self.removing.borrow()
        );
        if self.shown.borrow().as_deref() == Some(key.as_str()) {
            return;
        }
        self.shown.replace(Some(key));
        let selected = self.selected.borrow().clone();
        if let Some(site) = selected.as_deref().and_then(|name| self.site(name)) {
            self.render_detail(&site);
        } else if let Some(proxy) = selected.as_deref().and_then(|name| self.proxy(name)) {
            self.render_proxy(&proxy);
        } else {
            self.stack.set_visible_child_name("list");
            self.render_list();
        }
    }

    fn site(&self, name: &str) -> Option<SiteStatus> {
        self.status.borrow().as_ref()?.sites.iter().find(|s| s.name == name).cloned()
    }

    fn proxy(&self, name: &str) -> Option<ProxyStatus> {
        self.status.borrow().as_ref()?.proxies.iter().find(|p| p.name == name).cloned()
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
        let status = self.status.borrow().clone().unwrap_or_default();
        let sites = status.top_sites();
        // No sites or proxies: only the empty state, centred in the whole pane.
        if sites.is_empty() && status.proxies.is_empty() {
            let empty =
                ui::empty("dev.flock.Flock-sites-symbolic", "No sites", "Add a single site here, or add a project folder in General.");
            let buttons = gtk::Box::builder().spacing(12).halign(gtk::Align::Center).build();
            let add = self.add_button();
            add.add_css_class("suggested-action");
            add.add_css_class("pill");
            buttons.append(&add);
            let add_proxy = self.add_proxy_button();
            add_proxy.add_css_class("pill");
            buttons.append(&add_proxy);
            empty.set_child(Some(&buttons));
            self.list.set_groups(&[empty.upcast()]);
            return;
        }
        let mut groups: Vec<adw::PreferencesGroup> = Vec::new();
        if !sites.is_empty() {
            let group = group("", "");
            for site in sites {
                let worktrees = status.worktrees_of(&site.name);
                if worktrees.is_empty() {
                    group.add(&self.site_row(site));
                } else {
                    group.add(&self.expander(site, &worktrees));
                }
            }
            groups.push(group);
        }
        if !status.proxies.is_empty() {
            let group = group("Proxies", "");
            for proxy in &status.proxies {
                group.add(&self.proxy_row(proxy));
            }
            groups.push(group);
        }
        // Add Proxy… and Add Site… on the first group.
        if let Some(first) = groups.first() {
            let buttons = gtk::Box::builder().spacing(6).valign(gtk::Align::Center).build();
            let add_proxy = self.add_proxy_button();
            add_proxy.add_css_class("flat");
            buttons.append(&add_proxy);
            let add = self.add_button();
            add.add_css_class("flat");
            buttons.append(&add);
            first.set_header_suffix(Some(&buttons));
        }
        self.list.set_groups(&groups.into_iter().map(|g| g.upcast()).collect::<Vec<gtk::Widget>>());
    }

    /// Add Proxy…: asks for a name and the upstream, then adds the proxy and opens it.
    fn add_proxy_button(self: &Rc<Self>) -> gtk::Button {
        let tld = self.status.borrow().as_ref().map(|s| s.dns.tld.clone()).filter(|t| !t.is_empty()).unwrap_or_else(|| "test".into());
        let add = gtk::Button::builder()
            .label("Add Proxy…")
            .tooltip_text(format!("Serve a server of its own, e.g. a Node.js dev server on a port, at a .{tld} domain"))
            .build();
        let this = self.clone();
        add.connect_clicked(move |_| this.add_proxy(&tld));
        add
    }

    fn add_proxy(self: &Rc<Self>, tld: &str) {
        let name = adw::EntryRow::builder().title("Name, e.g. my-app").build();
        let upstream = adw::EntryRow::builder().title("Port, host:port or URL, e.g. 3000").activates_default(true).build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&upstream);
        let dialog = adw::AlertDialog::new(
            Some("Add a proxy"),
            Some(&format!(
                "Flock serves <name>.{tld} from a server of its own, e.g. a Next.js, Nuxt or Vite dev server. A port means 127.0.0.1."
            )),
        );
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("add", "Add")]);
        dialog.set_response_appearance("add", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("add"));
        let this = self.clone();
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let (name, upstream) = (name.text().trim().to_owned(), upstream.text().trim().to_owned());
            if response != "add" || name.is_empty() || upstream.is_empty() {
                return;
            }
            // Its detail shows once the daemon added it; the new status renders it.
            let (pane, banner, n) = (this.clone(), this.banner.clone(), name.clone());
            this.model.perform(
                move |c| c.proxy(&name, &upstream, None),
                move |e| {
                    if e.is_none() {
                        pane.selected.replace(Some(n.clone()));
                        pane.shown.replace(None);
                        pane.detail.scrolled.vadjustment().set_value(0.0);
                        pane.stack.set_visible_child_name("detail");
                    }
                    report(&banner, e);
                },
            );
        });
    }

    /// A proxy's row: its name and where it passes requests, a warning when it isn't served,
    /// whether something answers there and a lock when it is secured.
    fn proxy_row(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::ActionRow {
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&proxy.name))
            .subtitle(glib::markup_escape_text(&format!("{} → {}", proxy.domain, proxy.upstream_label())))
            .activatable(true)
            .build();
        row.set_tooltip_text(Some(&proxy.upstream));
        for widget in proxy_suffixes(proxy) {
            row.add_suffix(&widget);
        }
        row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, name) = (self.clone(), proxy.name.clone());
        row.connect_activated(move |_| this.open(&name));
        row
    }

    /// Add Site…: picks a folder and links it.
    fn add_button(self: &Rc<Self>) -> gtk::Button {
        let add = gtk::Button::builder().label("Add Site…").build();
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
        add
    }

    /// A site's row, or a worktree's (by its branch), which opens its detail.
    fn site_row(self: &Rc<Self>, site: &SiteStatus) -> adw::ActionRow {
        let title = display_name(site);
        let row = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&title))
            .subtitle(glib::markup_escape_text(&site.domain))
            .activatable(true)
            .build();
        row.set_tooltip_text(Some(&site.path));
        for widget in worktree_suffixes(site).into_iter().chain(site_suffixes(site)) {
            row.add_suffix(&widget);
        }
        row.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, name) = (self.clone(), site.name.clone());
        row.connect_activated(move |_| this.open(&name));
        row
    }

    /// A site with worktrees: its row expands to them (collapsed until the user expands it, which
    /// is remembered), and its arrow opens the site.
    fn expander(self: &Rc<Self>, site: &SiteStatus, worktrees: &[&SiteStatus]) -> adw::ExpanderRow {
        let row = adw::ExpanderRow::builder()
            .title(glib::markup_escape_text(&display_name(site)))
            .subtitle(glib::markup_escape_text(&site.domain))
            .expanded(self.expanded.borrow().contains(&site.name))
            .build();
        row.set_tooltip_text(Some(&site.path));
        let count = badge(&worktrees.len().to_string(), "");
        let worktrees_text = if worktrees.len() == 1 { "1 worktree".to_owned() } else { format!("{} worktrees", worktrees.len()) };
        count.set_tooltip_text(Some(&worktrees_text));
        row.add_suffix(&count);
        for widget in site_suffixes(site) {
            row.add_suffix(&widget);
        }
        let open = gtk::Button::builder()
            .icon_name("go-next-symbolic")
            .tooltip_text(format!("Open {}", site.name))
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        let (this, name) = (self.clone(), site.name.clone());
        open.connect_clicked(move |_| this.open(&name));
        row.add_suffix(&open);
        for worktree in worktrees {
            row.add_row(&self.site_row(worktree));
        }
        let (this, name) = (self.clone(), site.name.clone());
        row.connect_expanded_notify(move |row| {
            let expanded = row.is_expanded();
            {
                let mut list = this.expanded.borrow_mut();
                list.retain(|s| *s != name);
                if expanded {
                    list.push(name.clone());
                }
            }
            prefs::set_expanded(&name, expanded);
        });
        row
    }

    // MARK: a site's detail

    fn render_detail(self: &Rc<Self>, site: &SiteStatus) {
        if site.is_worktree() {
            self.render_worktree(site);
            return;
        }
        let config = self.config.borrow().clone().unwrap_or_default();
        let status = self.status.borrow().clone().unwrap_or_default();
        let mut groups: Vec<gtk::Widget> = vec![self.header(site).upcast()];
        groups.push(self.site_group(site).upcast());
        groups.extend(self.domain_groups(site, &status).into_iter().map(|g| g.upcast()));
        groups.push(self.share_group(site).upcast());
        groups.push(self.versions_group(site, &config).upcast());
        groups.push(self.server_group(site).upcast());
        if site.boost == "missing" || site.boost == "outdated" {
            groups.push(self.boost_group(site).upcast());
        }
        groups.push(self.worktrees_group(site, &status).upcast());
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
            .title(glib::markup_escape_text(&display_name(site)))
            .subtitle(glib::markup_escape_text(&format!("{}\n{}", site.url.as_deref().unwrap_or(&site.domain), site.path)))
            .subtitle_selectable(true)
            .build();
        if site.parked {
            title.add_suffix(&badge("Project folder", ""));
        }
        // The header's two pencils (macOS's click-to-edit name and the pencil beside the URL,
        // Windows's header pencils): each sits next to the thing it edits — the display name, and
        // the address the site is served at. A worktree never reaches this header (render_detail
        // routes it to its own), and macOS shows the domain pencil only where a URL is served.
        let pencil = |tooltip: &str| {
            gtk::Button::builder()
                .icon_name("document-edit-symbolic")
                .tooltip_text(tooltip)
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build()
        };
        let rename_title = pencil("Rename the display name…");
        let (this, s) = (self.clone(), site.clone());
        rename_title.connect_clicked(move |_| this.rename_title(&s));
        title.add_suffix(&rename_title);
        if site.url.is_some() {
            let rename_host = pencil("Change the domain name…");
            let (this, s) = (self.clone(), site.clone());
            rename_host.connect_clicked(move |_| this.rename_host(&s));
            title.add_suffix(&rename_host);
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

    /// Renames the display name (macOS's SiteTitleField, Windows's RenameTitleAsync): the dialog
    /// pre-fills the display name and the help line names the site an empty field falls back to.
    /// The submit is a title-only PATCH — the wire carries nothing but `title` (api.rs's
    /// skip_serializing_if pins it; encodes_a_site_change), and an empty one stores "" so the site
    /// shows its folder name again. A refusal lands in the pane's banner the way every other
    /// site edit's does; a success arrives with the Status the PATCH returns.
    fn rename_title(self: &Rc<Self>, site: &SiteStatus) {
        let entry = adw::EntryRow::builder().title("Display name").build();
        entry.set_text(&site.title);
        entry.set_activates_default(true);
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&entry);
        // EntryRow gained a placeholder only in libadwaita 1.6, above Ubuntu 24.04's 1.5, so the
        // name an empty field inherits is spoken in the help line instead (macOS's placeholder).
        let dialog = adw::AlertDialog::new(
            Some(&format!("Rename {}", display_name(site))),
            Some(&format!("Shown in Flock. Leave empty to use the site name ({}).", site.name)),
        );
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("rename", "Rename")]);
        dialog.set_response_appearance("rename", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("rename"));
        dialog.set_close_response("cancel");
        let (this, name) = (self.clone(), site.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response != "rename" {
                return;
            }
            // Whatever stands, trimmed, goes: an empty field is the way back to the site name.
            let typed = entry.text().trim().to_owned();
            this.change(&name, SiteChange { title: Some(typed), ..Default::default() });
        });
    }

    /// Changes the domain name — the label the site is served as under its suffix (macOS's
    /// RenameHostSheet, Windows's RenameHostAsync). Nothing is validated here: the bundled CLI's
    /// `flock site host` owns the label rules and retargets the site's and its worktrees' .env
    /// files in one run, so its output — refusals included — is what the user reads. A site that
    /// overrides the name today also gets the way back to the folder name.
    fn rename_host(self: &Rc<Self>, site: &SiteStatus) {
        let dns = self.status.borrow().as_ref().map(|s| s.dns.clone()).unwrap_or_default();
        let global = if dns.tld.is_empty() { "test".to_owned() } else { dns.tld.clone() };
        // What it serves under today; an old daemon names none, and then the global suffix stands.
        let suffix = if site.tld.is_empty() { global } else { site.tld.clone() };
        let entry = adw::EntryRow::builder().title("Domain name").build();
        entry.set_text(if site.site_host.is_empty() { &site.name } else { &site.site_host });
        entry.set_activates_default(true);
        // The live preview stands right under the field and follows every keystroke — the shape
        // the alias sheet's help label has in this file.
        let lands = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["caption", "dim-label"]).build();
        let stays = gtk::Label::builder()
            .label(format!("The suffix stays .{suffix}; it is chosen on the site’s Domain row."))
            .wrap(true)
            .xalign(0.0)
            .css_classes(["caption", "dim-label"])
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&entry);
        let column = gtk::Box::builder().orientation(gtk::Orientation::Vertical).spacing(12).build();
        column.append(&list);
        column.append(&lands);
        column.append(&stays);
        let dialog = adw::AlertDialog::new(Some(&format!("Change the domain name of {}", display_name(site))), None);
        dialog.set_extra_child(Some(&column));
        if site.site_host.is_empty() {
            dialog.add_responses(&[("cancel", "Cancel"), ("rename", "Rename")]);
        } else {
            // Going back to the folder name is offered only where an override stands today
            // (macOS's and Windows's guard on site_host).
            dialog.add_responses(&[("folder", "Use the folder name"), ("cancel", "Cancel"), ("rename", "Rename")]);
        }
        dialog.set_response_appearance("rename", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("rename"));
        dialog.set_close_response("cancel");
        let update: Rc<dyn Fn()> = Rc::new({
            let (entry, lands, dialog) = (entry.clone(), lands.clone(), dialog.clone());
            move || {
                let text = entry.text();
                let typed = text.trim();
                // Rename needs a label; whether it is a valid one is the CLI's call, not ours
                // (macOS's canRename).
                dialog.set_response_enabled("rename", !typed.is_empty());
                lands.set_label(&format!("The site will be served at {}.{}", if typed.is_empty() { "name" } else { typed }, suffix));
            }
        });
        entry.connect_changed({
            let update = update.clone();
            move |_| update()
        });
        update();
        let (this, name) = (self.clone(), site.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            // The raw typed label goes to argv as it stands: the CLI screens it (R1).
            let args = match response.as_str() {
                "folder" => vec!["site".to_owned(), "host".to_owned(), "--default".to_owned(), name.clone()],
                "rename" => {
                    let typed = entry.text().trim().to_owned();
                    if typed.is_empty() {
                        return;
                    }
                    vec!["site".to_owned(), "host".to_owned(), typed, name.clone()]
                }
                _ => return,
            };
            this.cli_run_sheet(
                args,
                "Renaming the domain…",
                "Flock is renaming the domain…",
                &format!("Renaming {name}’s domain didn’t finish"),
                "You declined the password. Finish by running `flock site host` in a terminal.",
            );
        });
    }

    fn site_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group(
            "Site",
            "The document root is the folder Flock serves the site from. One picked here applies on this machine only; pin it to write it into .flockrc for everyone.",
        );
        let https = adw::SwitchRow::builder().title("HTTPS").active(site.secured).build();
        let (this, name) = (self.clone(), site.name.clone());
        https.connect_active_notify(move |row| this.change(&name, SiteChange { secured: Some(row.is_active()), ..Default::default() }));
        group.add(&https);
        self.domain_rows(site, &group);

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

    /// The Site group's Domain row (macOS's field("Domain")): the shared suffix picker with the
    /// daemon's live verdict, and an Apply that runs the whole switch through the packaged CLI —
    /// `flock site tld` raises the desktop's polkit password prompt itself, like General's global
    /// switch (general.rs) does for `flock tld`. A site without a suffix of its own sits on
    /// Inherit; its `site_tld` picks the menu, and one the menu doesn't offer sits in the custom
    /// field. Inherit has no verdict to wait for, so its Apply is offered only while the site
    /// pins a suffix today (macOS's canApplySuffix).
    fn domain_rows(self: &Rc<Self>, site: &SiteStatus, group: &adw::PreferencesGroup) {
        let status = self.status.borrow().clone().unwrap_or_default();
        let dns = &status.dns;
        let global = if dns.tld.is_empty() { "test".to_owned() } else { dns.tld.clone() };
        // Apply compares against the suffix the site is served under today; an old daemon names
        // none, and then the global one stands in for the Inherit row's label.
        let current = if site.tld.is_empty() { global.clone() } else { site.tld.clone() };
        // The picker makes no network call itself: this closure is the daemon call, run on the
        // thread it spawns after its own debounce (panes/suffix.rs).
        let picker = SuffixPicker::new(&current, true, |tld| Client.check_tld(&tld));
        picker.row().set_title("Domain");
        picker.row().set_subtitle(&glib::markup_escape_text(&format!(
            "The suffix {} is served under. Inherit follows Flock’s global (.{global}); a picked suffix pins {} to its own.",
            site.name, site.name
        )));
        if !site.site_tld.is_empty() {
            if PRESETS.contains(&site.site_tld.as_str()) {
                picker.set_selected(&site.site_tld);
            } else {
                picker.set_custom(&site.site_tld);
            }
        }
        group.add(&picker.row());
        group.add(&picker.entry());
        group.add(&picker.verdict());
        // While the switch runs, the CLI's last output line shows under the picker.
        let progress = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["dim-label"]).visible(false).build();
        group.add(&progress);
        let apply = gtk::Button::builder().label("Apply").valign(gtk::Align::Center).sensitive(false).build();
        let actions = adw::ActionRow::new();
        actions.add_suffix(&apply);
        group.add(&actions);

        let running = Rc::new(Cell::new(false));
        let pinned = !site.site_tld.is_empty();
        let gate = {
            let (apply, picker, running) = (apply.clone(), picker.clone(), running.clone());
            move || apply.set_sensitive(!running.get() && if picker.is_inherit() { pinned } else { picker.can_apply() })
        };
        picker.set_changed(gate.clone());
        gate();
        let output: Rc<RefCell<Option<adw::ExpanderRow>>> = Rc::new(RefCell::default());
        {
            let (this, group) = (self.clone(), group.clone());
            let (name, global) = (site.name.clone(), global.clone());
            apply.connect_clicked(move |button| {
                // The draft is captured now, so a mid-run edit can't change what runs; Inherit
                // names the global suffix in the declined sentence, as on macOS.
                let (inherit, applied) = match picker.draft() {
                    Some(Draft::Inherit) => (true, global.clone()),
                    Some(Draft::Custom(suffix)) => (false, suffix),
                    None => return,
                };
                let mut args = vec!["site".to_owned(), "tld".to_owned()];
                args.push(if inherit { "--inherit".to_owned() } else { applied.clone() });
                args.push(name.clone());
                // Busy until the CLI is done: the spinner box busy_button shows, and the picker
                // disabled so no draft can drift while the password prompt is up.
                running.set(true);
                gate();
                let content = gtk::Box::builder().spacing(6).build();
                content.append(&gtk::Spinner::builder().spinning(true).build());
                content.append(&gtk::Label::new(Some("Applying…")));
                button.set_child(Some(&content));
                picker.row().set_sensitive(false);
                picker.entry().set_sensitive(false);
                progress.set_visible(false);
                clear_output(&group, &output);
                let (banner, model) = (this.banner.clone(), this.model.clone());
                let (button, group, gate, running, picker, name, progress_line, progress_done, output) = (
                    button.clone(),
                    group.clone(),
                    gate.clone(),
                    running.clone(),
                    picker.clone(),
                    name.clone(),
                    progress.clone(),
                    progress.clone(),
                    output.clone(),
                );
                installation::run_streaming(
                    args,
                    move |line| {
                        progress_line.set_label(line.trim());
                        progress_line.set_visible(true);
                    },
                    move |out| {
                        running.set(false);
                        progress_done.set_visible(false);
                        if out.ok {
                            // The CLI already wrote the site's suffix: the refresh re-seeds this
                            // row's picker with the new one.
                            report(&banner, None);
                            model.refresh();
                            return;
                        }
                        // The run didn't finish; the picker returns for a retry or another choice.
                        button.set_label("Apply");
                        picker.row().set_sensitive(true);
                        picker.entry().set_sensitive(true);
                        gate();
                        if installation::declined(&out) {
                            // The prompt came after the site's suffix was switched and before the
                            // DNS and certificate steps, so the recovery sentence is the whole
                            // story.
                            report(
                                &banner,
                                Some(format!(
                                    "You declined the password. Flock’s config is on .{applied} for {name} already; \
                                     finish the switch by running `flock install` in a terminal."
                                )),
                            );
                        } else {
                            report(&banner, Some(format!("Switching {name} to .{applied} didn’t finish.")));
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
    }

    /// The site's extra domains, split the way both sibling apps show them: aliases are names of
    /// their own beside the site, subdomains hang under its domain (macOS's field("Aliases") and
    /// field("Subdomains")). Both groups always stand: an empty one shows only its Add row, as
    /// on macOS.
    fn domain_groups(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> Vec<adw::PreferencesGroup> {
        let domains = site.domains.clone().unwrap_or_default();
        let aliases = group("Aliases", "");
        for domain in domains.iter().filter(|d| d.alias) {
            aliases.add(&self.domain_row(site, domain));
        }
        aliases.add(&self.add_domain_row(site, "Add Alias…", "Another name for the site, with its own subdomains", true));
        let subdomains = group("Subdomains", "");
        for domain in domains.iter().filter(|d| !d.alias) {
            subdomains.add(&self.domain_row(site, domain));
        }
        subdomains.add(&self.add_domain_row(
            site,
            "Add Subdomain…",
            &format!("A name under {}, following the site’s suffix", site.domain),
            false,
        ));
        if !status.dns.wildcard && !status.dns.wildcard_problem.is_empty() {
            subdomains.add(
                &adw::ActionRow::builder()
                    .title(glib::markup_escape_text(&format!(
                        "Subdomains only work when you add them here: {}.",
                        status.dns.wildcard_problem
                    )))
                    .css_classes(["warning"])
                    .build(),
            );
        }
        vec![aliases, subdomains]
    }

    /// One row of the Aliases and Subdomains lists: the name (its problem, if any) and Remove.
    fn domain_row(self: &Rc<Self>, site: &SiteStatus, domain: &SiteDomain) -> adw::ActionRow {
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&domain.host)).build();
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
        row
    }

    /// The link at the end of one domain list that opens its add dialog.
    fn add_domain_row(self: &Rc<Self>, site: &SiteStatus, title: &str, subtitle: &str, alias: bool) -> adw::ActionRow {
        let row = adw::ActionRow::builder().title(title).subtitle(subtitle).activatable(true).build();
        row.add_prefix(&gtk::Image::from_icon_name("list-add-symbolic"));
        let (this, site, status) = (self.clone(), site.clone(), self.status.borrow().clone().unwrap_or_default());
        row.connect_activated(move |_| if alias { this.add_alias(&site, &status) } else { this.add_subdomain(&site, &status) });
        row
    }

    /// Adds an alias (macOS's AddAliasSheet): a name of its own beside the site, served under the
    /// picked suffix — Inherit rides the site's suffix and moves with it, a picked suffix pins the
    /// alias in place, even one equal to the site's own. The add goes through the API; a refusal
    /// is the dialog's own red line, never the pane's banner, so the label and suffix stand for a
    /// retry. A suffix new to this machine is provisioned on the spot: the add chains the bundled
    /// CLI's `flock install`, polkit prompt included, in a sheet of its own.
    fn add_alias(self: &Rc<Self>, site: &SiteStatus, status: &Status) {
        let dns = &status.dns;
        let global = if dns.tld.is_empty() { "test".to_owned() } else { dns.tld.clone() };
        let site_suffix = if site.tld.is_empty() { global } else { site.tld.clone() };
        let name = adw::EntryRow::builder().title("Name, e.g. api2").activates_default(true).build();
        // A fresh picker per open (task 2's ruling): it lands on Inherit, as the macOS sheet does.
        let picker = SuffixPicker::new(&site_suffix, true, |tld| Client.check_tld(&tld));
        picker.set_selected_inherit();
        picker.row().set_title("Suffix");
        let help = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["caption", "dim-label"]).build();
        let error = gtk::Label::builder().wrap(true).xalign(0.0).selectable(true).css_classes(["error"]).visible(false).build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        list.append(&picker.row());
        list.append(&picker.entry());
        let buttons = gtk::Box::builder().spacing(6).halign(gtk::Align::End).build();
        let cancel = gtk::Button::builder().label("Cancel").valign(gtk::Align::Center).css_classes(["flat"]).build();
        let add = gtk::Button::builder().label("Add").valign(gtk::Align::Center).sensitive(false).build();
        add.add_css_class("suggested-action");
        buttons.append(&cancel);
        buttons.append(&add);
        let column = dialog_column();
        column.append(&list);
        column.append(&picker.verdict());
        column.append(&help);
        column.append(&error);
        column.append(&buttons);
        let dialog =
            adw::Dialog::builder().title(format!("Add an alias to {}", site.name)).content_width(460).child(&dialog_view(&column)).build();
        dialog.set_default_widget(Some(&add));
        dialog.set_focus(Some(&name));

        let submitting = Rc::new(Cell::new(false));
        let update: Rc<dyn Fn()> = Rc::new({
            let (name, picker, help, add, submitting) = (name.clone(), picker.clone(), help.clone(), add.clone(), submitting.clone());
            let (site_name, site_suffix) = (site.name.clone(), site_suffix.clone());
            let (wildcard, problem) = (dns.wildcard, dns.wildcard_problem.clone());
            move || {
                let label = name.text().trim().to_owned();
                let preview = if label.is_empty() { "name".to_owned() } else { label.clone() };
                // The suffix an unchecked draft previews is the one the sheet itself names (macOS).
                let shown = match picker.draft() {
                    Some(Draft::Inherit) => site_suffix.clone(),
                    Some(Draft::Custom(suffix)) => suffix,
                    None => "suffix".to_owned(),
                };
                let mut text = format!(
                    "An alias is a separate name for {site_name}, with its own subdomains. The label is one word, no dots; \
                     {preview}.{shown} is what lands. Inherit follows {site_name}’s suffix (.{site_suffix}) and moves with it; \
                     a picked suffix pins the alias in place."
                );
                if !wildcard && !problem.is_empty() {
                    text.push_str(&format!(" Listing names matters here: wildcard subdomains don’t work ({problem})."));
                }
                help.set_label(&text);
                // Add needs a label, and on a picked suffix the daemon's verdict: a pending or
                // refused check keeps it off (macOS's canAdd — a draft is a checked, normalized
                // suffix or Inherit, and a suffix equal to the site's own is a legal pin).
                let suffix_ready = picker.draft().is_some();
                add.set_sensitive(!submitting.get() && !label.is_empty() && suffix_ready);
            }
        });
        name.connect_changed({
            let update = update.clone();
            move |_| update()
        });
        picker.set_changed({
            let update = update.clone();
            move || update()
        });
        update();
        cancel.connect_clicked({
            let dialog = dialog.clone();
            move |_| {
                dialog.close();
            }
        });
        add.connect_clicked({
            let (name, picker, add, error, dialog, submitting, update) =
                (name.clone(), picker.clone(), add.clone(), error.clone(), dialog.clone(), submitting.clone(), update.clone());
            let (this, site_name, dns) = (self.clone(), site.name.clone(), dns.clone());
            move |_| {
                let domain = name.text().trim().to_owned();
                let suffix = match picker.draft() {
                    Some(Draft::Inherit) => INHERIT.to_owned(),
                    Some(Draft::Custom(suffix)) => suffix,
                    None => return,
                };
                if domain.is_empty() || submitting.get() {
                    return;
                }
                // Review Focus 3: the chain condition reads the state before the add moves it.
                let fresh = is_new_suffix(&dns, &suffix);
                submitting.set(true);
                error.set_visible(false);
                update();
                let content = gtk::Box::builder().spacing(6).build();
                content.append(&gtk::Spinner::builder().spinning(true).build());
                content.append(&gtk::Label::new(Some("Adding…")));
                add.set_child(Some(&content));
                // Both request halves get their own clones: the click closure is Fn and may run again.
                let (job, add, error, dialog, submitting, update, done_name, done_suffix) = (
                    this.clone(),
                    add.clone(),
                    error.clone(),
                    dialog.clone(),
                    submitting.clone(),
                    update.clone(),
                    site_name.clone(),
                    suffix.clone(),
                );
                let (req_name, req_domain, req_suffix) = (site_name.clone(), domain.clone(), suffix.clone());
                this.model.perform(
                    move |c| c.add_domain(&req_name, &req_domain, &req_suffix),
                    move |e| {
                        submitting.set(false);
                        add.set_label("Add");
                        update();
                        match e {
                            Some(message) => {
                                // A refusal is the dialog's own red line, never the banner: the
                                // typed label and suffix stand for a retry (macOS).
                                error.set_label(&message);
                                error.set_visible(true);
                            }
                            None => {
                                // The add landed: the pane shows it through the status this very
                                // request returned. A suffix new to the machine provisions next.
                                dialog.close();
                                if fresh {
                                    job.chain_install(&done_name, &done_suffix);
                                }
                            }
                        }
                    },
                );
            }
        });
        dialog.present(Some(&self.stack));
    }

    /// Adds a subdomain (macOS's AddSubdomainSheet): one name under the site's domain ("tenant1",
    /// "a.b"). There is no suffix to pick — it hangs under the site and follows its suffix — so
    /// there is nothing to provision either: the site's suffix is served already. The add goes
    /// through the legacy classification (no suffix); the daemon decides what lands.
    fn add_subdomain(self: &Rc<Self>, site: &SiteStatus, status: &Status) {
        let dns = &status.dns;
        let depth = dns.max_depth.max(1);
        let name = adw::EntryRow::builder().title("Name, e.g. tenant1 or a.b").activates_default(true).build();
        let help = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["caption", "dim-label"]).build();
        let error = gtk::Label::builder().wrap(true).xalign(0.0).selectable(true).css_classes(["error"]).visible(false).build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&name);
        let buttons = gtk::Box::builder().spacing(6).halign(gtk::Align::End).build();
        let cancel = gtk::Button::builder().label("Cancel").valign(gtk::Align::Center).css_classes(["flat"]).build();
        let add = gtk::Button::builder().label("Add").valign(gtk::Align::Center).sensitive(false).build();
        add.add_css_class("suggested-action");
        buttons.append(&cancel);
        buttons.append(&add);
        let column = dialog_column();
        column.append(&list);
        column.append(&help);
        column.append(&error);
        column.append(&buttons);
        let dialog = adw::Dialog::builder()
            .title(format!("Add a subdomain to {}", site.name))
            .content_width(460)
            .child(&dialog_view(&column))
            .build();
        dialog.set_default_widget(Some(&add));
        dialog.set_focus(Some(&name));

        let submitting = Rc::new(Cell::new(false));
        let update: Rc<dyn Fn()> = Rc::new({
            let (name, help, add, submitting) = (name.clone(), help.clone(), add.clone(), submitting.clone());
            let (domain, wildcard, problem) = (site.domain.clone(), dns.wildcard, dns.wildcard_problem.clone());
            move || {
                let typed = name.text().trim().to_owned();
                let preview = if typed.is_empty() { "name".to_owned() } else { typed.clone() };
                // Why list a name that already works: here, and on machines without wildcard DNS
                // (macOS's subdomainHelp, the two worlds apart).
                let mut text = format!("{preview}.{domain} hangs under the site’s domain and follows its suffix.");
                if wildcard {
                    text.push_str(&format!(
                        " Every subdomain up to {depth} levels deep already works without listing; list it to see it here \
                         and on machines without wildcard DNS."
                    ));
                } else {
                    text.push_str(&format!(" Listing matters here: wildcard subdomains don’t work ({problem}), so add each name you use."));
                }
                help.set_label(&text);
                // Add needs a name and nothing else — there is no check to wait for (macOS).
                add.set_sensitive(!submitting.get() && !typed.is_empty());
            }
        });
        name.connect_changed({
            let update = update.clone();
            move |_| update()
        });
        update();
        cancel.connect_clicked({
            let dialog = dialog.clone();
            move |_| {
                dialog.close();
            }
        });
        add.connect_clicked({
            let (name, add, error, dialog, submitting, update) =
                (name.clone(), add.clone(), error.clone(), dialog.clone(), submitting.clone(), update.clone());
            let (this, site_name) = (self.clone(), site.name.clone());
            move |_| {
                let domain = name.text().trim().to_owned();
                if domain.is_empty() || submitting.get() {
                    return;
                }
                submitting.set(true);
                error.set_visible(false);
                update();
                let content = gtk::Box::builder().spacing(6).build();
                content.append(&gtk::Spinner::builder().spinning(true).build());
                content.append(&gtk::Label::new(Some("Adding…")));
                add.set_child(Some(&content));
                let (add, error, dialog, submitting, update) =
                    (add.clone(), error.clone(), dialog.clone(), submitting.clone(), update.clone());
                let (req_name, req_domain) = (site_name.clone(), domain.clone());
                this.model.perform(
                    move |c| c.add_domain(&req_name, &req_domain, ""),
                    move |e| {
                        submitting.set(false);
                        add.set_label("Add");
                        update();
                        match e {
                            Some(message) => {
                                error.set_label(&message);
                                error.set_visible(true);
                            }
                            None => {
                                dialog.close();
                            }
                        }
                    },
                );
            }
        });
        dialog.present(Some(&self.stack));
    }

    /// The chained `flock install` for a suffix new to the machine (macOS's chainInstall in its
    /// ProvisionSheet, Windows's second ProvisionSuffixAsync dialog): the alias is in the config
    /// already, and the bundled CLI makes the suffix resolve with trusted certificates, the polkit
    /// prompt included — so the run and its password dialog belong to this sheet, not the pane.
    /// The sheet itself is cli_run_sheet's, shared with the domain rename.
    fn chain_install(self: &Rc<Self>, site: &str, suffix: &str) {
        self.cli_run_sheet(
            vec!["install".to_owned()],
            &format!("Provisioning .{suffix}…"),
            &format!("Flock is making .{suffix} resolve…"),
            &format!("Provisioning .{suffix} didn’t finish"),
            &format!(
                "You declined the password. The alias is added to {site}; finish by running `flock install` \
                 in a terminal to make .{suffix} resolve."
            ),
        );
    }

    /// The pane's one shape for a bundled CLI run that owns the moment (macOS's ProvisionSheet,
    /// Windows's run dialogs): a second sheet with a spinner and the CLI's last line while it runs,
    /// the CLI's own words in an Output row when it ends short, the recovery note when the polkit
    /// prompt was declined; success closes the sheet by itself, and however it ended the pane
    /// refreshes when the sheet closes (macOS's onDismiss). Used by the alias chain and the domain
    /// rename; the suffix switch keeps its inline variant because its controls stay on screen.
    fn cli_run_sheet(self: &Rc<Self>, args: Vec<String>, title: &str, running_label: &str, failed_title: &str, declined_note: &str) {
        // The run's callbacks outlive this call, so the sheet's words are owned from here on.
        let (title, running_label, failed_title, declined_note) =
            (title.to_owned(), running_label.to_owned(), failed_title.to_owned(), declined_note.to_owned());
        let spinner = gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build();
        let progress = gtk::Label::builder().label(running_label).wrap(true).xalign(0.0).hexpand(true).build();
        let running = gtk::Box::builder().spacing(8).build();
        running.append(&spinner);
        running.append(&progress);
        let note = gtk::Label::builder().wrap(true).xalign(0.0).css_classes(["warning"]).visible(false).build();
        let expander = adw::ExpanderRow::builder().title("Output").build();
        let output = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).visible(false).build();
        output.append(&expander);
        let column = dialog_column();
        column.append(&running);
        column.append(&note);
        column.append(&output);
        let dialog = adw::Dialog::builder().title(title).content_width(520).child(&dialog_view(&column)).build();
        dialog.connect_closed({
            let model = self.model.clone();
            move |_| model.refresh()
        });
        dialog.present(Some(&self.stack));
        installation::run_streaming(
            args,
            move |line| progress.set_label(line.trim()),
            move |out| {
                spinner.set_spinning(false);
                if out.ok {
                    // Done: the sheet closes itself, and the close refreshes the rows.
                    dialog.close();
                    return;
                }
                // Ended short: the sheet keeps the CLI's own words, as General's Domain card does.
                dialog.set_title(&failed_title);
                running.set_visible(false);
                if installation::declined(&out) {
                    note.set_label(&declined_note);
                    note.set_visible(true);
                }
                if !out.text.trim().is_empty() {
                    expander.add_row(&code_block(out.text.trim()));
                    output.set_visible(true);
                }
            },
        );
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

    // MARK: worktrees

    /// A site's worktrees and New Worktree….
    fn worktrees_group(self: &Rc<Self>, site: &SiteStatus, status: &Status) -> adw::PreferencesGroup {
        let group = group(
            "Worktrees",
            &format!("Each git worktree of {} is served at <branch>.{}, with its own dependencies and .env.", site.name, site.domain),
        );
        let add = gtk::Button::builder().label("New Worktree…").css_classes(["flat"]).build();
        let (this, s) = (self.clone(), site.clone());
        add.connect_clicked(move |_| this.new_worktree(&s));
        group.set_header_suffix(Some(&add));
        let worktrees = status.worktrees_of(&site.name);
        if worktrees.is_empty() {
            group.add(
                &adw::ActionRow::builder().title("No worktrees").subtitle("Create one here, with git or with your agent app.").build(),
            );
        }
        for worktree in worktrees {
            group.add(&self.site_row(worktree));
        }
        group
    }

    /// Asks for the branch, where it starts and the database of a new worktree. The branches to
    /// start from are read when it opens; a branch that exists is checked out as it is.
    fn new_worktree(self: &Rc<Self>, site: &SiteStatus) {
        let branch = gtk::Entry::builder()
            .placeholder_text("a name like brave-otter")
            .activates_default(true)
            .valign(gtk::Align::Center)
            .hexpand(true)
            .build();
        let branch_row = adw::ActionRow::builder().title("Branch").build();
        branch_row.add_suffix(&branch);
        let from_model = gtk::StringList::new(&["The current commit"]);
        let from = adw::ComboRow::builder().title("Start from").model(&from_model).sensitive(false).build();
        let database = adw::ComboRow::builder()
            .title("Database")
            .model(&gtk::StringList::new(&["Project default", DB_CHOICES[0], DB_CHOICES[1], DB_CHOICES[2]]))
            .build();
        let list = gtk::ListBox::builder().css_classes(["boxed-list"]).selection_mode(gtk::SelectionMode::None).build();
        list.append(&branch_row);
        list.append(&from);
        list.append(&database);
        let dialog = adw::AlertDialog::new(Some(&format!("New Worktree of {}", site.name)), Some(&new_worktree_text(site, "", false)));
        dialog.set_extra_child(Some(&list));
        dialog.add_responses(&[("cancel", "Cancel"), ("create", "Create")]);
        dialog.set_response_appearance("create", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("create"));

        // The branches, the value of each Start from choice, and whether they were read.
        let branches: Rc<RefCell<Branches>> = Rc::default();
        let values: Rc<RefCell<Vec<String>>> = Rc::new(RefCell::new(vec![String::new()]));
        let loaded = Rc::new(Cell::new(false));
        // Follows the branch typed in: one that exists starts from itself.
        let follow: Rc<dyn Fn(&str)> = Rc::new({
            let (weak, from, branches, loaded, s) = (dialog.downgrade(), from.clone(), branches.clone(), loaded.clone(), site.clone());
            move |text: &str| {
                let branch = text.trim();
                let exists = branches.borrow().has_local(branch);
                from.set_sensitive(loaded.get() && !exists);
                if let Some(dialog) = weak.upgrade() {
                    dialog.set_body(&new_worktree_text(&s, branch, exists));
                }
            }
        });
        let f = follow.clone();
        branch.connect_changed(move |entry| f(entry.text().as_str()));
        let (name, entry) = (site.name.clone(), branch.clone());
        let (from_list, model, b, v, l) = (from.clone(), from_model.clone(), branches.clone(), values.clone(), loaded.clone());
        glib::spawn_future_local(async move {
            // An older daemon has no branches to offer: the current commit only.
            let read = gio::spawn_blocking(move || Client.site_branches(&name)).await.expect("branches request").unwrap_or_default();
            let choices = read.choices();
            let labels: Vec<&str> = choices.iter().map(|(_, label)| label.as_str()).collect();
            model.splice(0, model.n_items(), &labels);
            let current = choices.iter().position(|(value, _)| *value == read.current).unwrap_or(0);
            from_list.set_selected(current as u32);
            v.replace(choices.into_iter().map(|(value, _)| value).collect());
            b.replace(read);
            l.set(true);
            follow(entry.text().as_str());
        });

        let (this, name) = (self.clone(), site.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response != "create" {
                return;
            }
            let branch = branch.text().trim().to_owned();
            let from = if branches.borrow().has_local(&branch) {
                String::new()
            } else {
                values.borrow().get(from.selected() as usize).cloned().unwrap_or_default()
            };
            let db = ["", "shared", "copy", "fresh"].get(database.selected() as usize).copied().unwrap_or_default().to_owned();
            let (title, site) = (format!("New Worktree of {name}"), name.clone());
            let start: worktree_job::Start =
                Box::new(move |c: Client| c.create_worktree(&site, &branch, &from, &db).map(|created| created.path));
            // Closed once it is ready, the dialog shows the new worktree.
            let pane = this.clone();
            worktree_job::open(&this.stack, this.model.clone(), &title, start, move |worktree| pane.open(&worktree));
        });
    }

    /// Removes a worktree and its own database. One git or its hook refuses to remove (changes, a
    /// failing hook) can be removed anyway.
    fn remove_worktree(self: &Rc<Self>, removal: Removal, force: bool) {
        self.removing.replace(Some(removal.path.clone()));
        self.render();
        let this = self.clone();
        glib::spawn_future_local(async move {
            let path = removal.path.clone();
            let result = gio::spawn_blocking(move || Client.remove_worktree(&path, force)).await.expect("worktree request");
            this.removing.replace(None);
            match result {
                Ok(_) => {
                    report(&this.banner, None);
                    // Its site's detail follows the worktree's.
                    let selected = this.selected.borrow().clone();
                    if selected.as_deref() == Some(removal.name.as_str()) {
                        this.selected.replace(Some(removal.parent.clone()));
                    }
                }
                Err(e) if !force => this.offer_force(removal, &e.to_string()),
                Err(e) => report(&this.banner, Some(e.to_string())),
            }
            this.render();
            this.model.refresh();
        });
    }

    fn offer_force(self: &Rc<Self>, removal: Removal, error: &str) {
        let dialog = adw::AlertDialog::new(Some(&format!("Couldn't remove {}", removal.label)), Some(error));
        dialog.add_responses(&[("cancel", "Cancel"), ("force", "Remove Anyway")]);
        dialog.set_response_appearance("force", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "force" {
                this.remove_worktree(removal.clone(), true);
            }
        });
    }

    // MARK: a worktree's detail

    fn render_worktree(self: &Rc<Self>, site: &SiteStatus) {
        let mut groups: Vec<gtk::Widget> = vec![self.worktree_header(site).upcast(), self.worktree_info(site).upcast()];
        if let Some(setup) = &site.worktree {
            groups.push(self.database_group(site, setup).upcast());
        }
        groups.push(self.setup_group(site).upcast());
        self.detail.set_groups(&groups);
    }

    fn worktree_header(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title(glib::markup_escape_text(&site.parent)).activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let (this, parent) = (self.clone(), site.parent.clone());
        back.connect_activated(move |_| this.open(&parent));
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&site.worktree_label()))
            .subtitle(glib::markup_escape_text(&format!("{}\n{}", site.url.as_deref().unwrap_or(&site.domain), site.path)))
            .subtitle_selectable(true)
            .build();
        title.add_suffix(&badge("Worktree", ""));
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open in the browser")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        open.set_sensitive(site.url.is_some());
        let url = site.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        title.add_suffix(&open);
        // While the hook and git run, a spinner instead of the button.
        if self.removing.borrow().as_deref() == Some(site.path.as_str()) {
            title.add_suffix(&gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build());
        } else {
            let remove = gtk::Button::builder()
                .icon_name("user-trash-symbolic")
                .tooltip_text("Remove worktree")
                .valign(gtk::Align::Center)
                .css_classes(["flat"])
                .build();
            let (this, s) = (self.clone(), site.clone());
            remove.connect_clicked(move |_| this.ask_remove(&s));
            title.add_suffix(&remove);
        }
        header.add(&title);
        if let Some(problem) = &site.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    /// Branch, folder and address, and the versions it gets from its site (or the branch's pins).
    fn worktree_info(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Worktree", "");
        group.add(&info_row("Branch", if site.branch.is_empty() { "None (detached HEAD)" } else { site.branch.as_str() }));
        let folder = info_row("Folder", &site.path);
        let open_folder = gtk::Button::builder()
            .icon_name("folder-symbolic")
            .tooltip_text("Open the folder")
            .valign(gtk::Align::Center)
            .css_classes(["flat"])
            .build();
        let path = site.path.clone();
        open_folder.connect_clicked(move |b| launch_file(b, &path));
        folder.add_suffix(&open_folder);
        group.add(&folder);
        let address = info_row("URL", site.url.as_deref().unwrap_or(&site.domain));
        if let Some(url) = site.url.clone() {
            address.set_activatable(true);
            address.connect_activated(move |r| launch_uri(r, &url));
        }
        group.add(&address);
        let versions = [("PHP", &site.php, &site.php_source, &site.php_file), ("Node.js", &site.node, &site.node_source, &site.node_file)];
        for (label, version, source, file) in versions {
            let row = info_row(label, if version.is_empty() { "—" } else { version.as_str() });
            if source == "project" && !file.is_empty() {
                row.set_subtitle(&glib::markup_escape_text(&format!("The branch pins it in {}", file_name(file))));
            }
            group.add(&row);
        }
        let settings = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&format!("Uses {}'s settings", site.parent)))
            .subtitle("Change its PHP and Node.js versions, HTTPS and document root on the site.")
            .activatable(true)
            .build();
        settings.add_suffix(&gtk::Image::from_icon_name("go-next-symbolic"));
        let (this, parent) = (self.clone(), site.parent.clone());
        settings.connect_activated(move |_| this.open(&parent));
        group.add(&settings);
        group
    }

    fn database_group(self: &Rc<Self>, site: &SiteStatus, setup: &WorktreeStatus) -> adw::PreferencesGroup {
        let group = group("Database", "A database of its own gets the branch's migrations run on it.");
        // The project's default mode isn't in the status: a database of its own is most likely a copy.
        let current: u32 = match setup.db_mode.as_str() {
            "shared" => 0,
            "copy" => 1,
            "fresh" => 2,
            _ if !setup.database.is_empty() => 1,
            _ => 0,
        };
        let mode = adw::ComboRow::builder().title("Database").model(&gtk::StringList::new(&DB_CHOICES)).selected(current).build();
        if let Some(database) = site.own_database() {
            mode.set_subtitle(&glib::markup_escape_text(database));
        }
        mode.set_sensitive(!setup.busy());
        let (this, s) = (self.clone(), site.clone());
        mode.connect_selected_notify(move |row| {
            if let Some(db) = DB_MODES.get(row.selected() as usize).filter(|_| row.selected() != current) {
                this.switch_database(&s, db);
            }
        });
        group.add(&mode);
        group
    }

    /// Asks before switching a worktree's database; the one Flock made for it is dropped.
    fn switch_database(self: &Rc<Self>, site: &SiteStatus, db: &str) {
        let (label, parent) = (site.worktree_label(), site.parent.clone());
        let own = site.own_database().map(str::to_owned);
        let (title, body) = match (&own, db) {
            (Some(name), _) => (format!("Drop {name}?"), format!("Its database {name} is dropped.")),
            (None, "shared") => (format!("Use {parent}'s database?"), format!("{label} then uses {parent}'s database.")),
            (None, "copy") => {
                let body = format!("Flock copies {parent}'s database and runs the branch's migrations on it.");
                (format!("Give {label} a copy of the database?"), body)
            }
            (None, _) => (format!("Give {label} a fresh database?"), "Flock creates an empty database and runs the migrations.".to_owned()),
        };
        let dialog = adw::AlertDialog::new(Some(&title), Some(&body));
        dialog.add_responses(&[("cancel", "Cancel"), ("switch", "Switch")]);
        dialog.set_response_appearance(
            "switch",
            if own.is_some() { adw::ResponseAppearance::Destructive } else { adw::ResponseAppearance::Suggested },
        );
        let (this, path, db) = (self.clone(), site.path.clone(), db.to_owned());
        let job_title = format!("Switching {label}'s Database");
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response != "switch" {
                // The row shows the mode it has again.
                this.shown.replace(None);
                this.render();
                return;
            }
            let (path, db) = (path.clone(), db.clone());
            let start: worktree_job::Start = Box::new(move |c: Client| c.switch_worktree_database(&path, &db).map(|_| path));
            worktree_job::open(&this.stack, this.model.clone(), &job_title, start, |_| {});
        });
    }

    fn setup_group(self: &Rc<Self>, site: &SiteStatus) -> adw::PreferencesGroup {
        let group = group("Setup", "");
        let Some(setup) = &site.worktree else {
            let row = adw::ActionRow::builder()
                .title("Flock hasn't set this worktree up")
                .subtitle("Setting it up copies dependencies and .env, installs and runs the project's hooks.")
                .build();
            let (this, s) = (self.clone(), site.clone());
            row.add_suffix(&button("Set Up", false, move || this.run_setup(&s, false)));
            group.add(&row);
            return group;
        };
        let row = adw::ActionRow::builder().title(glib::markup_escape_text(&setup.text())).build();
        if setup.busy() {
            row.add_prefix(&gtk::Spinner::builder().spinning(true).build());
        } else if setup.failed() {
            row.set_subtitle(&glib::markup_escape_text(&setup_error(setup)));
            row.add_css_class("warning");
        }
        if !setup.log.is_empty() {
            let show_log = gtk::Button::builder().label("Show Log").valign(gtk::Align::Center).action_name("app.open").build();
            show_log.set_action_target_value(Some(&format!("logs/services/{}", setup.log).to_variant()));
            row.add_suffix(&show_log);
        }
        let (this, s) = (self.clone(), site.clone());
        let again = button("Run Setup Again", false, move || this.run_setup(&s, true));
        again.set_sensitive(!setup.busy());
        row.add_suffix(&again);
        group.add(&row);
        group
    }

    /// Sets the worktree up (again), in the progress dialog.
    fn run_setup(self: &Rc<Self>, site: &SiteStatus, again: bool) {
        let path = site.path.clone();
        let start: worktree_job::Start = Box::new(move |c: Client| c.setup_worktree(&path, again).map(|_| path));
        worktree_job::open(&self.stack, self.model.clone(), &format!("Setting Up {}", site.worktree_label()), start, |_| {});
    }

    fn ask_remove(self: &Rc<Self>, site: &SiteStatus) {
        let label = site.worktree_label();
        let mut body = match site.own_database() {
            Some(db) => format!("Flock runs its before_delete hook and deletes {} and its database {db}.", site.path),
            None => format!("Flock runs its before_delete hook and deletes {}.", site.path),
        };
        if !site.branch.is_empty() {
            body.push_str(&format!(" The branch {} stays.", site.branch));
        }
        let dialog = adw::AlertDialog::new(Some(&format!("Remove {label}?")), Some(&body));
        dialog.add_responses(&[("cancel", "Cancel"), ("remove", "Remove")]);
        dialog.set_response_appearance("remove", adw::ResponseAppearance::Destructive);
        let this = self.clone();
        let removal = Removal { path: site.path.clone(), name: site.name.clone(), label, parent: site.parent.clone() };
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            if response == "remove" {
                this.remove_worktree(removal.clone(), false);
            }
        });
    }

    // MARK: a proxy's detail

    /// A proxy: its address and actions, then what answers at its upstream, its HTTPS and upstream.
    fn render_proxy(self: &Rc<Self>, proxy: &ProxyStatus) {
        let groups: Vec<gtk::Widget> = vec![self.proxy_header(proxy).upcast(), self.proxy_group(proxy).upcast()];
        self.detail.set_groups(&groups);
    }

    fn proxy_header(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let header = group("", "");
        let back = adw::ActionRow::builder().title("Sites").activatable(true).build();
        back.add_prefix(&gtk::Image::from_icon_name("go-previous-symbolic"));
        let this = self.clone();
        back.connect_activated(move |_| this.back());
        header.add(&back);

        let title = adw::ActionRow::builder()
            .title(glib::markup_escape_text(&proxy.name))
            .subtitle(glib::markup_escape_text(proxy.url.as_deref().unwrap_or(&proxy.domain)))
            .subtitle_selectable(true)
            .build();
        title.add_suffix(&badge("Proxy", ""));
        let actions = gtk::Box::builder().spacing(4).valign(gtk::Align::Center).build();
        let open = gtk::Button::builder()
            .icon_name("dev.flock.Flock-sites-symbolic")
            .tooltip_text("Open in the browser")
            .css_classes(["flat"])
            .build();
        open.set_sensitive(proxy.url.is_some());
        let url = proxy.url.clone().unwrap_or_default();
        open.connect_clicked(move |b| launch_uri(b, &url));
        actions.append(&open);
        let remove = gtk::Button::builder().icon_name("user-trash-symbolic").tooltip_text("Remove proxy").css_classes(["flat"]).build();
        let (this, p) = (self.clone(), proxy.clone());
        remove.connect_clicked(move |_| {
            let name = p.name.clone();
            this.confirm(
                &format!("Remove {}?", p.name),
                &format!("Flock stops serving {}. The server at {} keeps running.", p.domain, p.upstream),
                "Remove",
                true,
                move |c| c.remove_proxy(&name),
            );
        });
        actions.append(&remove);
        title.add_suffix(&actions);
        header.add(&title);
        if let Some(problem) = &proxy.problem {
            header.add(&adw::ActionRow::builder().title(glib::markup_escape_text(problem)).css_classes(["warning"]).build());
        }
        header
    }

    fn proxy_group(self: &Rc<Self>, proxy: &ProxyStatus) -> adw::PreferencesGroup {
        let group = group(
            "Proxy",
            &format!(
                "Every request passes to the upstream with its Host header, WebSocket upgrades (hot reload) included. Flock doesn't start the server, so start it as usual. Until it answers, {} shows a page saying so.",
                proxy.domain
            ),
        );
        let server = info_row("Server", &proxy.server_text());
        server.add_prefix(&status_dot(proxy.running));
        if !proxy.running {
            server.set_subtitle(&glib::markup_escape_text(&format!("Nothing answers at {} yet.", proxy.upstream_label())));
        }
        group.add(&server);

        let https = adw::SwitchRow::builder().title("HTTPS").active(proxy.secured).build();
        let (this, name) = (self.clone(), proxy.name.clone());
        https.connect_active_notify(move |row| {
            let (name, on) = (name.clone(), row.is_active());
            this.perform(move |c| c.secure_proxy(&name, on));
        });
        group.add(&https);

        let upstream = info_row("Upstream", &proxy.upstream);
        let (this, p) = (self.clone(), proxy.clone());
        upstream.add_suffix(&button("Change…", false, move || this.change_upstream(&p)));
        group.add(&upstream);
        group
    }

    /// Asks for a proxy's new upstream; its HTTPS stays as it is.
    fn change_upstream(self: &Rc<Self>, proxy: &ProxyStatus) {
        let entry =
            gtk::Entry::builder().text(proxy.upstream_label()).placeholder_text("Port, host:port or URL").activates_default(true).build();
        let dialog = adw::AlertDialog::new(
            Some(&format!("Change the upstream of {}", proxy.name)),
            Some("A port means 127.0.0.1; give a host and port (localhost:5173) or a URL (https://127.0.0.1:8443) for anything else."),
        );
        dialog.set_extra_child(Some(&entry));
        dialog.add_responses(&[("cancel", "Cancel"), ("change", "Change")]);
        dialog.set_response_appearance("change", adw::ResponseAppearance::Suggested);
        dialog.set_default_response(Some("change"));
        let (this, name) = (self.clone(), proxy.name.clone());
        dialog.choose(Some(&self.stack), gio::Cancellable::NONE, move |response| {
            let upstream = entry.text().trim().to_owned();
            if response == "change" && !upstream.is_empty() {
                let name = name.clone();
                this.perform(move |c| c.proxy(&name, &upstream, None));
            }
        });
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

/// A site's suffixes in the list: what it warns about, its share and its lock.
fn site_suffixes(site: &SiteStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    if let Some(problem) = problem(site) {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(&problem));
        widgets.push(warning.upcast());
    }
    if site.shared() {
        widgets.push(badge("Shared", "success").upcast());
    }
    if site.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some("Served over HTTPS"));
        widgets.push(lock.upcast());
    }
    widgets
}

/// A worktree's suffixes in a list: its setup while it runs or when it failed, and its own
/// database ("copy", "fresh").
fn worktree_suffixes(site: &SiteStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    let Some(setup) = &site.worktree else { return widgets };
    if setup.busy() {
        widgets.push(gtk::Spinner::builder().spinning(true).valign(gtk::Align::Center).build().upcast());
        widgets.push(gtk::Label::builder().label(setup.text()).css_classes(["dim-label", "caption"]).build().upcast());
    } else if setup.failed() {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(&setup_error(setup)));
        widgets.push(warning.upcast());
    }
    if !setup.database.is_empty() {
        let tag = badge(if setup.db_mode.is_empty() { "own database" } else { setup.db_mode.as_str() }, "");
        tag.set_tooltip_text(Some(&format!("Its own database: {}", setup.database)));
        widgets.push(tag.upcast());
    }
    widgets
}

fn setup_error(setup: &WorktreeStatus) -> String {
    if setup.error.is_empty() { "The setup failed.".to_owned() } else { setup.error.clone() }
}

/// What the New Worktree dialog says it does with the branch typed in.
fn new_worktree_text(site: &SiteStatus, branch: &str, exists: bool) -> String {
    let address = if branch.is_empty() { "its own address".to_owned() } else { format!("{}.{}", slug(branch), site.domain) };
    if exists {
        return format!(
            "{branch} exists, so the worktree checks it out. Flock creates it next to {}, sets it up and serves it at {address}.",
            site.name
        );
    }
    let name = if branch.is_empty() { "a new branch with a name of its own".to_owned() } else { format!("the new branch {branch}") };
    format!("Flock creates the worktree next to {} on {name}, sets it up and serves it at {address}.", site.name)
}

/// The name a worktree is served under, as Flock makes it from the branch (config.WorktreeSlug):
/// "Feature/Auth" → "feature-auth".
fn slug(branch: &str) -> String {
    let mut out = String::new();
    for c in branch.to_lowercase().chars() {
        if c.is_ascii_alphanumeric() || c == '-' {
            out.push(c);
        } else if matches!(c, '/' | '_' | '.') {
            out.push('-');
        }
    }
    while out.contains("--") {
        out = out.replace("--", "-");
    }
    out.trim_matches('-').to_owned()
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

/// A proxy's suffixes in the list: why it isn't served, whether something answers at its
/// upstream, and its lock.
fn proxy_suffixes(proxy: &ProxyStatus) -> Vec<gtk::Widget> {
    let mut widgets: Vec<gtk::Widget> = Vec::new();
    if let Some(problem) = &proxy.problem {
        let warning = gtk::Image::from_icon_name("dialog-warning-symbolic");
        warning.add_css_class("warning");
        warning.set_tooltip_text(Some(problem));
        widgets.push(warning.upcast());
    }
    let dot = status_dot(proxy.running);
    dot.set_tooltip_text(Some(&proxy.server_text()));
    widgets.push(dot.upcast());
    if proxy.secured {
        let lock = gtk::Image::from_icon_name("channel-secure-symbolic");
        lock.add_css_class("dim-label");
        lock.set_tooltip_text(Some("Served over HTTPS"));
        widgets.push(lock.upcast());
    }
    widgets
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

/// A dialog's content column: spaced and margined, as the extension list and the worktree job
/// sheet build theirs.
fn dialog_column() -> gtk::Box {
    gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(12)
        .margin_top(12)
        .margin_bottom(18)
        .margin_start(18)
        .margin_end(18)
        .build()
}

/// The dialog's view: a header bar to close from, and the content column.
fn dialog_view(column: &gtk::Box) -> adw::ToolbarView {
    let view = adw::ToolbarView::new();
    view.add_top_bar(&adw::HeaderBar::new());
    view.set_content(Some(column));
    view
}

/// What Flock calls a site wherever the user reads its name (macOS's `title.isEmpty ? name :
/// title`, Windows's DisplayName): a worktree is named by its branch or served label — the title
/// and host belong to its parent site — and every other site shows the display name set on it,
/// over the site name when it carries none (or an old daemon sent no title at all).
fn display_name(site: &SiteStatus) -> String {
    if site.is_worktree() {
        site.worktree_label()
    } else if site.title.is_empty() {
        site.name.clone()
    } else {
        site.title.clone()
    }
}

/// Whether the suffix an alias just settled on is new to this machine (Review Focus 3, macOS's
/// `isNewSuffix`): never for Inherit (the site's suffix is provisioned by definition) nor for the
/// global one (`dns.tld`) nor for a suffix something already serves under (`dns.suffixes`; a row
/// without a tld is the ledger's contract and counts as nothing). A daemon from before the list
/// chains once, and `flock install` is idempotent.
fn is_new_suffix(dns: &DnsStatus, suffix: &str) -> bool {
    !suffix.is_empty() && suffix != INHERIT && suffix != dns.tld && !dns.suffixes.iter().any(|s| !s.tld.is_empty() && s.tld == suffix)
}

/// Takes the output row of a previous failed `flock site tld` run out of the Site group, so a
/// retry doesn't stack its failure text under the old one (general.rs's clear_output does the
/// same for the global switch's identical Expander).
fn clear_output(group: &adw::PreferencesGroup, output: &RefCell<Option<adw::ExpanderRow>>) {
    if let Some(expander) = output.borrow_mut().take() {
        group.remove(&expander);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn makes_the_worktree_slug_like_flock() {
        assert_eq!(slug("Feature/Auth_2.x"), "feature-auth-2-x");
        assert_eq!(slug("fix//ümlaut-"), "fix-mlaut");
        assert_eq!(slug("brave-otter"), "brave-otter");
    }

    fn dns(tld: &str, suffixes: &[&str]) -> DnsStatus {
        DnsStatus {
            tld: tld.to_owned(),
            suffixes: suffixes.iter().map(|t| crate::api::SuffixStatus { tld: t.to_string(), ..Default::default() }).collect(),
            ..Default::default()
        }
    }

    #[test]
    fn inherit_and_an_empty_suffix_never_chain_the_install() {
        // An inherited alias rides the site's suffix, which is provisioned by definition.
        assert!(!is_new_suffix(&dns("test", &["test"]), INHERIT));
        assert!(!is_new_suffix(&dns("test", &[]), ""));
    }

    #[test]
    fn a_suffix_something_serves_under_never_chains() {
        // The global one, and any suffix the ledger lists (Review Focus 3: no chain on an
        // active suffix).
        assert!(!is_new_suffix(&dns("test", &["test", "loc"]), "test"));
        assert!(!is_new_suffix(&dns("test", &["test", "loc"]), "loc"));
    }

    #[test]
    fn a_suffix_nothing_serves_under_chains_the_install_once() {
        assert!(is_new_suffix(&dns("test", &["test", "loc"]), "internal"));
        // A daemon from before the suffixes list: the first picked suffix chains once, and
        // `flock install` is idempotent.
        assert!(is_new_suffix(&dns("test", &[]), "internal"));
    }

    #[test]
    fn suffix_rows_without_a_tld_are_not_active_suffixes() {
        // The legacy ledger contract (3c): rows with an empty tld stand for nothing served, so
        // they can't stop the chain.
        assert!(is_new_suffix(&dns("test", &[""]), "internal"));
    }

    #[test]
    fn names_a_site_by_its_display_name_over_its_name() {
        // The list and the header name the site the same way (macOS's SiteTitleField, Windows's
        // DisplayName): the display name stands, the site name shows when it carries none — and
        // the display name of a worktree is its branch, however its parent site is titled.
        let titled = SiteStatus { name: "acme".into(), title: "ACME Blog".into(), ..Default::default() };
        assert_eq!(display_name(&titled), "ACME Blog");
        let plain = SiteStatus { name: "acme".into(), ..Default::default() };
        assert_eq!(display_name(&plain), "acme");
        let worktree = SiteStatus {
            name: "feature-auth.acme".into(),
            parent: "acme".into(),
            branch: "feature/auth".into(),
            title: "x".into(),
            ..Default::default()
        };
        assert_eq!(display_name(&worktree), "feature/auth");
    }
}
