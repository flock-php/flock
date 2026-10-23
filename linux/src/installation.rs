//! Whether Flock is set up for this user, and running the flock CLI for it (setup, restart).

use std::path::PathBuf;
use std::process::Command;

use crate::paths;

/// The flock the package installed (/usr/bin/flk links to it); `flock install` copies it into the
/// user's data dir.
const PACKAGED_FLOCK: &str = "/usr/lib/flockphp/flock";

/// The user's daemon is registered: `flock install` wrote its unit's drop-in.
pub fn is_set_up() -> bool {
    let user = std::env::var("USER").unwrap_or_default();
    PathBuf::from(format!("/etc/systemd/system/flock@{user}.service.d/flock.conf")).exists()
}

/// Flock isn't set up for this user yet, and the app can do it.
pub fn needs_setup() -> bool {
    !is_set_up() && PathBuf::from(PACKAGED_FLOCK).exists()
}

/// The outcome of running flock: whether it succeeded and what it printed.
pub struct Output {
    pub ok: bool,
    pub text: String,
}

/// Runs the user's flock with args, waiting for it (call it from gio::spawn_blocking).
pub fn run(args: &[&str]) -> Output {
    let flock = paths::flock_binary();
    if !flock.exists() {
        return Output {
            ok: false,
            text: format!("Flock is not installed for this user ({} is missing) — run `flk install` once.", flock.display()),
        };
    }
    match Command::new(&flock).args(args).output() {
        Ok(out) => {
            let mut text = String::from_utf8_lossy(&out.stdout).into_owned();
            text.push_str(&String::from_utf8_lossy(&out.stderr));
            Output { ok: out.status.success(), text: text.trim().to_owned() }
        }
        Err(e) => Output { ok: false, text: format!("{}: {e}", flock.display()) },
    }
}
