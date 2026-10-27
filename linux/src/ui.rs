//! Widgets and formats the windows share: times, monospaced text and HTML views.

use adw::prelude::*;
use gtk::glib;
use webkit6::prelude::*;

/// "just now", "5 minutes ago", "3 hours ago", else the date, from the daemon's RFC 3339 time.
pub fn relative_time(time: &str) -> String {
    let (Some(time), Ok(now)) = (glib::DateTime::from_iso8601(time, None).ok(), glib::DateTime::now_utc()) else {
        return String::new();
    };
    let seconds = now.difference(&time).as_seconds();
    match seconds {
        ..60 => "just now".to_owned(),
        60..120 => "a minute ago".to_owned(),
        120..3600 => format!("{} minutes ago", seconds / 60),
        3600..7200 => "an hour ago".to_owned(),
        7200..86400 => format!("{} hours ago", seconds / 3600),
        _ => local_time(&time, "%e %b"),
    }
}

/// A daemon time in the user's time zone, e.g. "24 Oct 2026, 14:03:12".
pub fn date_time(time: &str) -> String {
    glib::DateTime::from_iso8601(time, None).map(|t| local_time(&t, "%e %b %Y, %H:%M:%S")).unwrap_or_default()
}

/// "14:03:12.481" for a timeline.
pub fn clock(time: &str) -> String {
    glib::DateTime::from_iso8601(time, None).map(|t| local_time(&t, "%H:%M:%S.%f").chars().take(12).collect()).unwrap_or_default()
}

fn local_time(time: &glib::DateTime, format: &str) -> String {
    time.to_local().and_then(|t| t.format(format)).map(|s| s.trim().to_owned()).unwrap_or_default()
}

/// Scrollable, selectable monospaced text.
pub fn monospace(text: &str) -> gtk::ScrolledWindow {
    let view =
        gtk::TextView::builder().editable(false).monospace(true).top_margin(12).bottom_margin(12).left_margin(12).right_margin(12).build();
    view.buffer().set_text(text);
    gtk::ScrolledWindow::builder().child(&view).vexpand(true).hexpand(true).build()
}

/// Renders HTML. Mail runs with JavaScript off and remote loads blocked (no tracking pixels, no
/// surprises): a Content-Security-Policy allows only inline styles and data: images. Dumps need
/// JavaScript for Symfony's collapsible output.
pub fn html_view(html: &str, javascript: bool) -> webkit6::WebView {
    let view = webkit6::WebView::builder().vexpand(true).hexpand(true).build();
    if let Some(settings) = webkit6::prelude::WebViewExt::settings(&view) {
        settings.set_enable_javascript(javascript);
        settings.set_auto_load_images(true);
        settings.set_enable_developer_extras(false);
    }
    view.load_html(&with_policy(html, javascript), None);
    // Links open in the browser, not in the view.
    view.connect_decide_policy(|view, decision, kind| {
        if kind != webkit6::PolicyDecisionType::NavigationAction {
            return false;
        }
        let Some(action) = decision.downcast_ref::<webkit6::NavigationPolicyDecision>().and_then(|d| d.navigation_action()) else {
            return false;
        };
        let uri = action.clone().request().and_then(|r| r.uri()).unwrap_or_default();
        if uri.starts_with("http://") || uri.starts_with("https://") || uri.starts_with("mailto:") {
            decision.ignore();
            let window = view.root().and_downcast::<gtk::Window>();
            gtk::UriLauncher::new(&uri).launch(window.as_ref(), gtk::gio::Cancellable::NONE, |_| {});
            return true;
        }
        false
    });
    view
}

/// The page behind a Content-Security-Policy that blocks every remote load.
pub fn with_policy(html: &str, javascript: bool) -> String {
    let policy = if javascript {
        "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src data:"
    } else {
        "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:"
    };
    format!("<meta http-equiv=\"Content-Security-Policy\" content=\"{policy}\">{html}")
}

/// A page saying there is nothing to show (yet).
pub fn empty(icon: &str, title: &str, description: &str) -> adw::StatusPage {
    adw::StatusPage::builder().icon_name(icon).title(title).description(glib::markup_escape_text(description)).vexpand(true).build()
}
