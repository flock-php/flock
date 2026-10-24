//! Pieces the version panes share (PHP, FrankenPHP, Node.js, services): a row's state line, its
//! buttons, a popover of further actions, the install log and when the manifest was checked.

use std::cell::Cell;
use std::rc::Rc;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

use crate::api::{Error, Job};

/// The line under a version's name: a job's progress, its error, a withdrawn build, or `idle`.
pub fn detail(row: &adw::ActionRow, job: Option<&Job>, yanked: &str, update: bool, idle: &str) {
    let (text, class) = match job {
        Some(job) if job.running() => (job.text(), None),
        Some(job) if job.failed() => {
            (if job.error.is_empty() { "The download failed.".to_owned() } else { job.error.clone() }, Some("error"))
        }
        _ if !yanked.is_empty() => {
            (if update { format!("Withdrawn: {yanked} — update to replace it") } else { format!("Withdrawn: {yanked}") }, Some("warning"))
        }
        _ => (idle.to_owned(), if update { Some("accent") } else { None }),
    };
    row.set_subtitle(&glib::markup_escape_text(&text));
    for class in ["error", "warning", "accent"] {
        row.remove_css_class(class);
    }
    if let Some(class) = class {
        row.add_css_class(class);
    }
    if let Some(job) = job.filter(|j| j.running() && (0.0..=1.0).contains(&j.progress) && j.phase != "installing") {
        let bar = gtk::ProgressBar::builder().fraction(job.progress).valign(gtk::Align::Center).width_request(100).build();
        row.add_suffix(&bar);
    }
}

pub fn button(label: &str, suggested: bool, clicked: impl Fn() + 'static) -> gtk::Button {
    let button = gtk::Button::builder().label(label).valign(gtk::Align::Center).build();
    if suggested {
        button.add_css_class("suggested-action");
    }
    button.connect_clicked(move |_| clicked());
    button
}

/// An action of more_menu: its label, whether it can be chosen, whether it is destructive, and what it does.
pub type MenuItem = (String, bool, bool, Box<dyn Fn()>);

/// A ⋯ button with a popover of further actions; destructive ones are red.
pub fn more_menu(items: Vec<MenuItem>) -> gtk::MenuButton {
    let column = gtk::Box::builder().orientation(gtk::Orientation::Vertical).build();
    let popover = gtk::Popover::builder().child(&column).build();
    for (label, sensitive, destructive, action) in items {
        let item = gtk::Button::builder().label(label.as_str()).css_classes(["flat"]).sensitive(sensitive).build();
        if let Some(text) = item.child().and_downcast::<gtk::Label>() {
            text.set_xalign(0.0);
        }
        if destructive {
            item.add_css_class("destructive-action");
        }
        let popover = popover.clone();
        item.connect_clicked(move |_| {
            popover.popdown();
            action();
        });
        column.append(&item);
    }
    gtk::MenuButton::builder()
        .icon_name("view-more-symbolic")
        .tooltip_text("More")
        .popover(&popover)
        .valign(gtk::Align::Center)
        .css_classes(["flat"])
        .build()
}

pub fn badge(text: &str, class: &str) -> gtk::Label {
    let label = gtk::Label::builder().label(text).valign(gtk::Align::Center).css_classes(["caption", "badge"]).build();
    if !class.is_empty() {
        label.add_css_class(class);
    }
    label
}

/// "Checked 5 minutes ago", from the daemon's RFC 3339 time.
pub fn checked_text(checked_at: Option<&str>) -> String {
    let Some(time) = checked_at.and_then(|t| glib::DateTime::from_iso8601(t, None).ok()) else {
        return "Not checked yet".to_owned();
    };
    let Ok(now) = glib::DateTime::now_utc() else { return String::new() };
    let minutes = now.difference(&time).as_seconds() / 60;
    match minutes {
        ..1 => "Checked just now".to_owned(),
        1 => "Checked a minute ago".to_owned(),
        2..60 => format!("Checked {minutes} minutes ago"),
        60..120 => "Checked an hour ago".to_owned(),
        _ if minutes < 48 * 60 => format!("Checked {} hours ago", minutes / 60),
        _ => format!("Checked {} days ago", minutes / (24 * 60)),
    }
}

/// Asks before something is removed; confirmed runs on "Uninstall".
pub fn confirm(parent: &impl IsA<gtk::Widget>, title: &str, body: &str, action: &str, confirmed: impl Fn() + 'static) {
    let dialog = adw::AlertDialog::new(Some(title), Some(body));
    dialog.add_responses(&[("cancel", "Cancel"), ("ok", action)]);
    dialog.set_response_appearance("ok", adw::ResponseAppearance::Destructive);
    dialog.choose(Some(parent), gio::Cancellable::NONE, move |response| {
        if response == "ok" {
            confirmed();
        }
    });
}

/// The tail of an install log, read again every two seconds while the dialog is open and running
/// says the install still runs.
pub fn show_log(
    parent: &impl IsA<gtk::Widget>,
    title: &str,
    read: impl Fn() -> Result<String, Error> + Send + Sync + Clone + 'static,
    running: impl Fn() -> bool + 'static,
) {
    let text = gtk::TextView::builder()
        .editable(false)
        .monospace(true)
        .wrap_mode(gtk::WrapMode::WordChar)
        .top_margin(10)
        .bottom_margin(10)
        .left_margin(10)
        .right_margin(10)
        .build();
    let scrolled = gtk::ScrolledWindow::builder().child(&text).vexpand(true).build();
    let copy = gtk::Button::with_label("Copy");
    let header = adw::HeaderBar::new();
    header.pack_start(&copy);
    let view = adw::ToolbarView::new();
    view.add_top_bar(&header);
    view.set_content(Some(&scrolled));
    let dialog = adw::Dialog::builder().title(title).content_width(720).content_height(460).child(&view).build();

    let buffer = text.buffer();
    let copied = buffer.clone();
    copy.connect_clicked(move |button| {
        let (start, end) = copied.bounds();
        button.clipboard().set_text(&copied.text(&start, &end, false));
    });

    let open = Rc::new(Cell::new(true));
    let closed = open.clone();
    dialog.connect_closed(move |_| closed.set(false));
    let running = Rc::new(running);
    glib::spawn_future_local(async move {
        let mut shown = String::new();
        while open.get() {
            let read = read.clone();
            let log = gio::spawn_blocking(read).await.expect("log request").unwrap_or_else(|_| "No install log yet.".to_owned());
            let tail: Vec<&str> = log.lines().collect();
            let tail = tail[tail.len().saturating_sub(400)..].join("\n");
            if tail != shown {
                buffer.set_text(&tail);
                text.scroll_to_iter(&mut buffer.end_iter(), 0.0, false, 0.0, 1.0);
                shown = tail;
            }
            if !running() {
                break;
            }
            glib::timeout_future(Duration::from_secs(2)).await;
        }
    });
    dialog.present(Some(parent));
}
