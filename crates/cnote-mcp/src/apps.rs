//! "Connected apps": the OAuth grants a person has approved in this workspace, and revoking them.
//! People only: an agent must not be able to list or revoke its own (or another app's) access.

use chrono::{DateTime, Utc};
use cnote_core::notes::Empty;
use cnote_core::{CmdError, CmdResult, Command, Ctx, Module, PrincipalKind, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

pub fn module() -> Module {
    Module {
        commands: vec![
            Command::new(
                "oauth_app.list",
                "List the apps (MCP clients) you have connected to this workspace through OAuth.",
                ToolGroup::Admin,
                handler!(list),
            ),
            Command::new(
                "oauth_app.revoke",
                "Disconnect an OAuth app: its access and refresh tokens stop working immediately.",
                ToolGroup::Admin,
                handler!(revoke),
            ),
        ],
        ..Default::default()
    }
}

fn person(ctx: &Ctx) -> CmdResult<Uuid> {
    match (ctx.principal.kind, ctx.principal.user_id) {
        (PrincipalKind::User, Some(u)) => Ok(u),
        _ => Err(CmdError::forbidden("only people can manage connected apps")),
    }
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct AppOut {
    pub id: Uuid,
    pub client_id: String,
    pub name: String,
    pub created_at: DateTime<Utc>,
    pub last_used_at: Option<DateTime<Utc>>,
}

async fn list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<AppOut>> {
    let user = person(ctx)?;
    Ok(sqlx::query_as(
        "select g.id, g.client_id, c.name, g.created_at, g.last_used_at
         from core.oauth_grants g join core.oauth_clients c on c.client_id = g.client_id
         where g.user_id = $1 and g.workspace_id = $2 and g.revoked_at is null
         order by g.created_at desc",
    )
    .bind(user)
    .bind(ctx.workspace_id())
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct RevokeIn {
    /// Connected app id from `oauth_app.list`.
    pub id: Uuid,
}

async fn revoke(ctx: &mut Ctx, i: RevokeIn) -> CmdResult<Value> {
    let user = person(ctx)?;
    let n = sqlx::query(
        "update core.oauth_grants set revoked_at = now()
         where id = $1 and user_id = $2 and workspace_id = $3 and revoked_at is null",
    )
    .bind(i.id)
    .bind(user)
    .bind(ctx.workspace_id())
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 1 {
        sqlx::query("delete from core.oauth_tokens where grant_id = $1")
            .bind(i.id)
            .execute(&mut *ctx.tx)
            .await?;
        ctx.emit("oauth_app.revoked", None, json!({"grant_id": i.id}))
            .await?;
    }
    Ok(json!({"revoked": n == 1}))
}
