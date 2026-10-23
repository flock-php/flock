//! The daemon's local HTTP API (internal/api): its address and token come from api.json. Requests
//! block, so the UI runs them with gio::spawn_blocking.

use std::fmt;
use std::time::Duration;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};

use crate::paths;

#[derive(Debug, Clone, PartialEq)]
pub enum Error {
    NotRunning,
    Server { status: u16, message: String },
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        match self {
            Error::NotRunning => write!(f, "Flock is not running"),
            Error::Server { status, message } if message.is_empty() => write!(f, "Flock answered with HTTP {status}"),
            Error::Server { message, .. } => write!(f, "{message}"),
        }
    }
}

/// Contents of api.json, written by the daemon.
#[derive(Deserialize)]
struct ApiConfig {
    url: String,
    token: String,
}

/// Default timeout of a request; slow endpoints pass their own.
const DEFAULT_TIMEOUT: Duration = Duration::from_secs(5);

#[derive(Clone, Default)]
pub struct Client;

impl Client {
    pub fn status(&self) -> Result<Status, Error> {
        self.request("GET", "/v1/status", None::<&()>, DEFAULT_TIMEOUT)
    }

    /// Changes only the given settings and returns the new status.
    pub fn update_settings(&self, change: &SettingsChange) -> Result<Status, Error> {
        self.request("POST", "/v1/settings", Some(change), DEFAULT_TIMEOUT)
    }

    /// Reads api.json for every request: a restarted daemon listens on a new port with a new token.
    fn request<B: Serialize, T: DeserializeOwned>(
        &self,
        method: &str,
        path: &str,
        body: Option<&B>,
        timeout: Duration,
    ) -> Result<T, Error> {
        let data = std::fs::read(paths::api_json()).map_err(|_| Error::NotRunning)?;
        let config: ApiConfig = serde_json::from_slice(&data).map_err(|_| Error::NotRunning)?;
        let agent: ureq::Agent = ureq::Agent::config_builder().http_status_as_error(false).timeout_global(Some(timeout)).build().into();
        let url = format!("{}{}", config.url.trim_end_matches('/'), path);
        let auth = format!("Bearer {}", config.token);
        let response = match (method, body) {
            ("GET", _) => agent.get(&url).header("Authorization", &auth).call(),
            ("DELETE", _) => agent.delete(&url).header("Authorization", &auth).call(),
            (_, body) => {
                let json = body.map(|b| serde_json::to_vec(b).expect("request body")).unwrap_or_default();
                let builder = match method {
                    "PATCH" => agent.patch(&url),
                    "PUT" => agent.put(&url),
                    _ => agent.post(&url),
                };
                builder.header("Authorization", &auth).content_type("application/json").send(&json[..])
            }
        };
        let mut response = response.map_err(|_| Error::NotRunning)?;
        let status = response.status().as_u16();
        let bytes = response.body_mut().read_to_vec().map_err(|_| Error::NotRunning)?;
        if !(200..300).contains(&status) {
            #[derive(Deserialize)]
            struct ErrorBody {
                error: String,
            }
            let message = serde_json::from_slice::<ErrorBody>(&bytes).map(|e| e.error).unwrap_or_default();
            return Err(Error::Server { status, message });
        }
        serde_json::from_slice(&bytes).map_err(|e| Error::Server { status, message: format!("unexpected answer from Flock: {e}") })
    }
}

/// POST /v1/settings: only the fields that are set change.
#[derive(Serialize, Default)]
pub struct SettingsChange {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub xdebug: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub xdebug_breakpoints: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dumps: Option<bool>,
}

/// GET /v1/status (api.Status).
#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct Status {
    pub version: String,
    pub pid: i64,
    pub dns: DnsStatus,
    pub http_port: u16,
    pub https_port: u16,
    pub mail: String,
    pub dumps: bool,
    pub mail_count: u32,
    pub dump_count: u32,
    pub event_count: u32,
    pub postgres: Option<String>,
    pub valkey: Option<String>,
    pub extras: Vec<ExtraStatus>,
    pub xdebug: bool,
    pub xdebug_breakpoints: bool,
    pub sites: Vec<SiteStatus>,
    pub processes: Vec<ProcStatus>,
    pub warnings: Vec<String>,
    pub channel: String,
}

impl Status {
    /// Something needs the user's attention: a warning or a process that is down.
    pub fn is_degraded(&self) -> bool {
        !self.warnings.is_empty() || self.processes.iter().any(|p| !p.up)
    }

    pub fn process(&self, name: &str) -> Option<&ProcStatus> {
        self.processes.iter().find(|p| p.name == name)
    }
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct DnsStatus {
    pub tld: String,
    pub wildcard: bool,
    pub wildcard_problem: String,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ExtraStatus {
    pub id: String,
    pub name: String,
    pub version: String,
    pub port: u16,
    pub enabled: bool,
    pub up: bool,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ProcStatus {
    pub name: String,
    pub up: bool,
    pub restarts: u32,
    pub last_error: Option<String>,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct SiteStatus {
    pub name: String,
    pub domain: String,
    pub path: String,
    pub php: String,
    pub secured: bool,
    pub url: Option<String>,
    pub problem: Option<String>,
    pub notice: Option<String>,
    pub root_state: Option<String>,
    pub debugging: bool,
    pub server: String,
    pub worker: String,
    pub share: Option<ShareStatus>,
}

impl SiteStatus {
    pub fn shared(&self) -> bool {
        self.share.as_ref().is_some_and(|s| !s.url.is_empty())
    }
}

#[derive(Deserialize, Debug, Clone, PartialEq, Default)]
#[serde(default)]
pub struct ShareStatus {
    pub url: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    // A status as the daemon writes it, with fields the app doesn't read.
    #[test]
    fn decodes_a_status() {
        let json = r#"{"version":"1.4.0","pid":42,"started_at":"2026-10-23T21:00:00Z",
            "dns":{"tld":"test","addr":"127.0.0.1:53535","wildcard":true,"max_depth":4},
            "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","dumps":true,"capture_off":null,
            "mail_count":3,"dump_count":0,"event_count":0,"postgres":"18.6","xdebug":false,
            "sites":[{"name":"acme","domain":"acme.test","path":"/home/a/acme","php":"8.4","secured":true,
              "url":"https://acme.test","domains":[],"server":"fastcgi","worker":"","debugging":false}],
            "processes":[{"name":"postgres","up":true,"restarts":0}],"channel":"stable"}"#;
        let status: Status = serde_json::from_str(json).unwrap();
        assert_eq!(status.version, "1.4.0");
        assert_eq!(status.postgres.as_deref(), Some("18.6"));
        assert_eq!(status.sites[0].url.as_deref(), Some("https://acme.test"));
        assert!(!status.sites[0].shared());
        assert!(!status.is_degraded());
    }

    #[test]
    fn degraded_when_a_process_is_down() {
        let down = ProcStatus { name: "postgres".into(), ..Default::default() };
        let status = Status { processes: vec![down], ..Default::default() };
        assert!(status.is_degraded());
    }
}
