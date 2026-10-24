//! The panes of the Flock window, like the macOS app's Settings window has them.

pub mod about;
pub mod ai;
mod common;
pub mod debugger;
pub mod dumps;
pub mod general;
pub mod mail;
pub mod node;
pub mod php;
pub mod services;
pub mod sites;
mod versions;

use crate::api::{Config, Service, Status};

/// What the panes show: the status, and while the window is open the config and services.
#[derive(Clone, PartialEq, Default)]
pub struct Data {
    pub status: Option<Status>,
    pub config: Option<Config>,
    pub services: Vec<Service>,
}

/// One pane of the window.
pub trait Pane {
    fn widget(&self) -> gtk::Widget;
    /// Shows data; panes rebuild their rows only when what they show changed.
    fn update(&self, data: &Data);
}
