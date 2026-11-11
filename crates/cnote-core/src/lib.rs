//! C Note core: the command layer, principals, auth, events and the universal Note.

pub mod access;
pub mod admin;
pub mod ai;
pub mod auth;
pub mod command;
pub mod error;
pub mod events;
pub mod notes;
pub mod principal;
pub mod ratelimit;
pub mod sharing;

pub use command::{Command, Ctx, ExecOptions, Module, Registry, ToolGroup};
pub use error::{CmdError, CmdResult, ErrorCode};
pub use principal::{Principal, PrincipalKind, Role};

pub fn module() -> Module {
    let mut commands = notes::commands();
    commands.extend(admin::commands());
    commands.extend(sharing::commands());
    Module {
        commands,
        workspace_hooks: vec![default_space],
        ..Default::default()
    }
}

/// Every workspace starts with one open space so pages, contacts and deals have a home on day one.
fn default_space<'a>(
    tx: &'a mut cnote_db::Tx,
    workspace_id: uuid::Uuid,
    _owner: uuid::Uuid,
) -> futures::future::BoxFuture<'a, anyhow::Result<()>> {
    Box::pin(async move {
        sqlx::query(
            "insert into core.spaces (workspace_id, key, name, template) values ($1, 'GEN', 'General', 'blank')",
        )
        .bind(workspace_id)
        .execute(&mut **tx)
        .await?;
        Ok(())
    })
}
