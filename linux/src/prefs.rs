//! The app's own preferences, in $XDG_CONFIG_HOME/flock/app.json (what UserDefaults holds on
//! macOS). `flock uninstall` removes the folder.

use std::path::PathBuf;

use serde::{Deserialize, Serialize};

use crate::paths;

#[derive(Serialize, Deserialize, Default, Clone, PartialEq, Debug)]
#[serde(default)]
pub struct Prefs {
    /// Where the Dumps window opens source files: an id of EDITORS.
    pub editor: String,
    /// The sites whose worktrees the Sites list shows, by name; the others are collapsed.
    pub expanded_sites: Vec<String>,
    /// Leaves the Sites submenu out of the tray menu (General → Tray).
    pub hide_sites: bool,
}

/// Editors the Dumps window can open files in: id, name and URL scheme ("" for none).
pub const EDITORS: [(&str, &str); 5] =
    [("phpstorm", "PhpStorm"), ("vscode", "VS Code"), ("cursor", "Cursor"), ("zed", "Zed"), ("none", "Don't link")];

pub fn dir() -> PathBuf {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .filter(|d| d.is_absolute())
        .unwrap_or_else(|| paths::home().join(".config"))
        .join("flock")
}

fn file() -> PathBuf {
    dir().join("app.json")
}

pub fn load() -> Prefs {
    let mut prefs: Prefs = std::fs::read(file()).ok().and_then(|data| serde_json::from_slice(&data).ok()).unwrap_or_default();
    if prefs.editor.is_empty() {
        prefs.editor = "phpstorm".to_owned();
    }
    prefs
}

/// Remembers whether a site's worktrees are shown in the Sites list.
pub fn set_expanded(site: &str, expanded: bool) {
    let mut prefs = load();
    prefs.expanded_sites.retain(|s| s != site);
    if expanded {
        prefs.expanded_sites.push(site.to_owned());
    }
    let _ = save(&prefs);
}

/// Remembers whether the tray menu leaves out the Sites submenu.
pub fn set_hide_sites(hide: bool) -> std::io::Result<()> {
    let mut prefs = load();
    prefs.hide_sites = hide;
    save(&prefs)
}

pub fn save(prefs: &Prefs) -> std::io::Result<()> {
    std::fs::create_dir_all(dir())?;
    std::fs::write(file(), serde_json::to_vec_pretty(prefs).expect("prefs"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shows_sites_unless_the_preference_says_otherwise() {
        // app.json from before the preference existed.
        let prefs: Prefs = serde_json::from_str(r#"{"editor":"zed","expanded_sites":["acme"]}"#).unwrap();
        assert!(!prefs.hide_sites);
        assert!(!Prefs::default().hide_sites);

        let saved = serde_json::to_vec(&Prefs { hide_sites: true, ..Default::default() }).unwrap();
        assert!(serde_json::from_slice::<Prefs>(&saved).unwrap().hide_sites);
    }
}
