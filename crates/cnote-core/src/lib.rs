//! C Note core: the command layer, principals, auth, events and the universal Note.

pub mod access;
pub mod admin;
pub mod auth;
pub mod command;
pub mod error;
pub mod events;
pub mod notes;
pub mod principal;
pub mod ratelimit;

pub use command::{Command, Ctx, ExecOptions, Module, Registry, ToolGroup};
pub use error::{CmdError, CmdResult, ErrorCode};
pub use principal::{Principal, PrincipalKind, Role};

pub fn module() -> Module {
    let mut commands = notes::commands();
    commands.extend(admin::commands());
    Module {
        commands,
        ..Default::default()
    }
}
