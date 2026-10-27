//! Notifies when a worktree's setup fails and no progress dialog follows it: worktrees an agent
//! app or git created, or whose progress was hidden. Clicking the notification shows the worktree
//! in Sites. The macOS app's WorktreeNotifier.

use std::cell::RefCell;
use std::collections::HashMap;
use std::rc::Rc;

use adw::prelude::*;
use gtk::gio;

use crate::model::AppModel;

/// From now on, compares each status with the one before.
pub fn start(app: &adw::Application, model: &Rc<AppModel>) {
    // Each worktree's setup state, by name, as of the last status.
    let states: RefCell<Option<HashMap<String, String>>> = RefCell::default();
    let (app, weak) = (app.downgrade(), Rc::downgrade(model));
    model.subscribe(move || {
        let (Some(app), Some(model)) = (app.upgrade(), weak.upgrade()) else { return };
        let Some(status) = model.status() else { return };
        let worktrees: Vec<_> = status.sites.iter().filter(|s| s.is_worktree()).collect();
        let now = worktrees.iter().map(|s| (s.name.clone(), s.worktree.as_ref().map(|w| w.state.clone()).unwrap_or_default())).collect();
        let Some(before) = states.replace(Some(now)) else { return };
        for site in worktrees {
            let Some(setup) = site.worktree.as_ref().filter(|w| w.failed()) else { continue };
            if model.follows_worktree(&site.path) || !before.get(&site.name).is_some_and(|state| state != "failed") {
                continue;
            }
            let notification = gio::Notification::new(&format!("Setting up {} failed", site.worktree_label()));
            let error = if setup.error.is_empty() { "see its log" } else { setup.error.as_str() };
            notification.set_body(Some(&format!("{}: {error}", site.parent)));
            notification.set_default_action_and_target_value("app.open", Some(&format!("settings/sites/{}", site.name).to_variant()));
            app.send_notification(Some(&format!("worktree-{}", site.name)), &notification);
        }
    });
}
