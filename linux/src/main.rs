//! Flock's Linux app: a libadwaita window for the local daemon, like the macOS menu bar app and
//! the Windows tray app. It talks to the daemon over its local API (api.json).

mod api;
mod installation;
mod paths;
mod window;

use std::path::PathBuf;
use std::time::Duration;

use adw::prelude::*;
use gtk::{gio, glib};

const APP_ID: &str = "dev.flock.Flock";

fn main() -> glib::ExitCode {
    // --screenshot <file.png>: renders the window once the status is shown, saves it and quits
    // (development and the docs' screenshots).
    let args: Vec<String> = std::env::args().collect();
    let screenshot = args.iter().position(|a| a == "--screenshot").and_then(|i| args.get(i + 1)).map(PathBuf::from);

    let app = adw::Application::builder().application_id(APP_ID).build();
    app.connect_activate(move |app| {
        if let Some(window) = app.active_window() {
            window.present();
            return;
        }
        let main = window::MainWindow::new(app);
        main.start_polling();
        main.window.present();
        if let Some(path) = screenshot.clone() {
            // The paintable follows the content from its next frame on.
            let paintable = gtk::WidgetPaintable::new(main.window.content().as_ref());
            let window = main.window.clone();
            glib::timeout_add_local_once(Duration::from_secs(3), move || {
                save_screenshot(&window, &paintable, &path);
                window.application().expect("application").quit();
            });
        }
    });

    let quit = gio::ActionEntry::builder("quit").activate(|app: &adw::Application, _, _| app.quit()).build();
    let settings = gio::ActionEntry::builder("settings")
        .activate(|app: &adw::Application, _, _| {
            if let Some(window) = app.active_window() {
                let dialog = adw::AlertDialog::new(Some("Settings"), Some("The Settings window isn't in the Linux app yet."));
                dialog.add_response("ok", "OK");
                dialog.present(Some(&window));
            }
        })
        .build();
    app.add_action_entries([quit, settings]);
    app.set_accels_for_action("app.quit", &["<Control>q"]);
    app.set_accels_for_action("app.settings", &["<Control>comma"]);

    // GApplication would reject --screenshot as an unknown option.
    app.run_with_args(&args[..1])
}

/// Renders the window's content (a toplevel can't paint itself into a paintable) into a PNG.
fn save_screenshot(window: &adw::ApplicationWindow, paintable: &gtk::WidgetPaintable, path: &PathBuf) {
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
