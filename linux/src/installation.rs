//! Whether Flock is set up for this user, and running the flock CLI for it (setup, restart,
//! uninstall).

use std::io::{BufRead, BufReader};
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::sync::{OnceLock, mpsc};
use std::time::Duration;

use gtk::glib;
use serde::Deserialize;

use crate::paths;

/// The flock the package installed (/usr/bin/flk links to it); `flock install` copies it into the
/// user's data dir.
const PACKAGED_FLOCK: &str = "/usr/libexec/flockphp/flock";

/// The user's daemon is registered: `flock install` wrote its unit's drop-in.
pub fn is_set_up() -> bool {
    let user = std::env::var("USER").unwrap_or_default();
    PathBuf::from(format!("/etc/systemd/system/flock@{user}.service.d/flock.conf")).exists()
}

/// Flock isn't set up for this user yet, and the app can do it.
pub fn needs_setup() -> bool {
    !is_set_up() && PathBuf::from(PACKAGED_FLOCK).exists()
}

/// The AUR package Flock is installed from on Arch (flockphp-bin or flockphp-beta-bin, see
/// linux/packaging/aur), which AUR helpers update; None elsewhere.
pub fn aur_package() -> Option<&'static str> {
    static PACKAGE: OnceLock<Option<String>> = OnceLock::new();
    PACKAGE
        .get_or_init(|| {
            let out = Command::new("pacman").args(["-Qqo", PACKAGED_FLOCK]).stderr(Stdio::null()).output().ok()?;
            let name = String::from_utf8_lossy(&out.stdout).trim().to_owned();
            (out.status.success() && !name.is_empty()).then_some(name)
        })
        .as_deref()
}

/// The outcome of running flock: whether it succeeded, how it exited and what it printed.
pub struct Output {
    pub ok: bool,
    /// The exit code, None when flock didn't run or a signal ended it.
    pub code: Option<i32>,
    pub text: String,
}

/// Runs the user's flock with args, waiting for it (call it from gio::spawn_blocking).
pub fn run(args: &[&str]) -> Output {
    let flock = paths::flock_binary();
    if !flock.exists() {
        return Output {
            ok: false,
            code: None,
            text: format!("Flock is not installed for this user ({} is missing) — run `flk install` once.", flock.display()),
        };
    }
    match Command::new(&flock).args(args).output() {
        Ok(out) => {
            let mut text = String::from_utf8_lossy(&out.stdout).into_owned();
            text.push_str(&String::from_utf8_lossy(&out.stderr));
            Output { ok: out.status.success(), code: out.status.code(), text: text.trim().to_owned() }
        }
        Err(e) => Output { ok: false, code: None, text: format!("{}: {e}", flock.display()) },
    }
}

/// The flock to set Flock up or remove it with: the package's, else the user's.
pub fn setup_flock() -> Option<PathBuf> {
    [PathBuf::from(PACKAGED_FLOCK), paths::flock_binary()].into_iter().find(|f| f.exists())
}

/// Runs flock without a terminal (so it asks for the password with the desktop's polkit dialog),
/// calling on_line with every line it prints, then done with the outcome.
pub fn run_streaming(args: Vec<String>, on_line: impl Fn(&str) + 'static, done: impl FnOnce(Output) + 'static) {
    let Some(flock) = setup_flock() else {
        done(Output { ok: false, code: None, text: "Flock isn't installed: install the flockphp package first.".to_owned() });
        return;
    };
    let (tx, rx) = mpsc::channel::<Result<String, Output>>();
    std::thread::spawn(move || {
        let child = Command::new(&flock).args(&args).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn();
        let mut child = match child {
            Ok(child) => child,
            Err(e) => {
                let _ = tx.send(Err(Output { ok: false, code: None, text: format!("{}: {e}", flock.display()) }));
                return;
            }
        };
        let stderr = child.stderr.take().expect("stderr");
        let err_tx = tx.clone();
        let errors = std::thread::spawn(move || {
            for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                let _ = err_tx.send(Ok(line));
            }
        });
        for line in BufReader::new(child.stdout.take().expect("stdout")).lines().map_while(Result::ok) {
            let _ = tx.send(Ok(line));
        }
        let _ = errors.join();
        let status = child.wait().ok();
        let ok = status.is_some_and(|s| s.success());
        let code = status.and_then(|s| s.code());
        let _ = tx.send(Err(Output { ok, code, text: String::new() }));
    });

    // The lines arrive on this (the UI) thread.
    let mut text = String::new();
    let mut done = Some(done);
    glib::timeout_add_local(Duration::from_millis(100), move || {
        while let Ok(message) = rx.try_recv() {
            match message {
                Ok(line) => {
                    text.push_str(&line);
                    text.push('\n');
                    on_line(&line);
                }
                Err(mut output) => {
                    if output.text.is_empty() {
                        output.text = text.trim_end().to_owned();
                    }
                    if let Some(done) = done.take() {
                        done(output);
                    }
                    return glib::ControlFlow::Break;
                }
            }
        }
        glib::ControlFlow::Continue
    });
}

/// The password dialog was dismissed: flock exits with 4 (exitDeclined in internal/cli/exitcodes.go).
pub fn declined(output: &Output) -> bool {
    output.code == Some(4)
}

/// Another program on one of Flock's ports (`flock conflicts --json`).
#[derive(Deserialize, Clone, Debug, PartialEq)]
pub struct Conflict {
    pub port: Option<u16>,
    pub service: String,
    pub holder: String,
    /// What to quit: a program, or a systemd service.
    pub app: String,
    /// "quit nginx (pid 812)", "run `sudo systemctl disable --now apache2`".
    pub hint: Option<String>,
}

/// The programs to stop before Flock can be set up, each with the ports it holds. Empty when there
/// are none, and when the check itself fails: `flock install` checks again and reports them.
pub fn conflicts() -> Vec<(String, Option<String>, Vec<Conflict>)> {
    #[derive(Deserialize)]
    struct Response {
        conflicts: Vec<Conflict>,
    }
    let Some(flock) = setup_flock() else { return Vec::new() };
    let Ok(out) = Command::new(flock).args(["conflicts", "--json"]).stdin(Stdio::null()).output() else { return Vec::new() };
    let Ok(response) = serde_json::from_slice::<Response>(&out.stdout) else { return Vec::new() };
    let mut apps: Vec<(String, Option<String>, Vec<Conflict>)> = Vec::new();
    for conflict in response.conflicts {
        match apps.iter_mut().find(|(app, _, _)| *app == conflict.app) {
            Some((_, _, list)) => list.push(conflict),
            None => apps.push((conflict.app.clone(), conflict.hint.clone(), vec![conflict])),
        }
    }
    apps
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn declined_goes_by_the_exit_code_not_the_text() {
        let text = "Error: privileged setup: administrator permission was declined".to_owned();
        assert!(declined(&Output { ok: false, code: Some(4), text: text.clone() }));
        assert!(!declined(&Output { ok: false, code: Some(1), text }));
        assert!(!declined(&Output { ok: false, code: None, text: String::new() }));
    }
}
