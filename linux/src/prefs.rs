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

pub fn save(prefs: &Prefs) -> std::io::Result<()> {
    std::fs::create_dir_all(dir())?;
    std::fs::write(file(), serde_json::to_vec_pretty(prefs).expect("prefs"))
}
