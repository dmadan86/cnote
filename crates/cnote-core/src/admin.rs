//! Workspace administration: members, agents, API keys, tool groups, confirmations, usage.

use crate::auth::{sha256_hex, slugify};
use crate::command::{Command, Ctx, DEFAULT_AGENT_GROUPS, ToolGroup};
use crate::error::{CmdError, CmdResult};
use crate::handler;
use crate::notes::Empty;
use crate::principal::{PrincipalKind, Role};
use base64::Engine;
use chrono::{DateTime, Utc};
use rand::RngCore;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct Member {
    pub user_id: Uuid,
    pub email: String,
    pub name: String,
    pub role: String,
}

async fn member_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<Member>> {
    Ok(sqlx::query_as(
        "select m.user_id, u.email, u.name, m.role from core.memberships m join core.users u on u.id = m.user_id where m.deactivated_at is null order by u.name",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct AddMember {
    pub email: String,
    #[serde(default)]
    pub name: Option<String>,
    /// admin | member | guest
    pub role: String,
}

/// Adds an existing user, or creates a passwordless placeholder they can claim by signing up.
async fn member_add(ctx: &mut Ctx, i: AddMember) -> CmdResult<Member> {
    let role = Role::parse(&i.role);
    if role == Role::Owner || !matches!(i.role.as_str(), "admin" | "member" | "guest") {
        return Err(CmdError::invalid("role must be admin, member or guest"));
    }
    let email = i.email.trim().to_lowercase();
    let name = i
        .name
        .unwrap_or_else(|| email.split('@').next().unwrap_or("").to_string());
    let user_id: Uuid = sqlx::query_scalar(
        "insert into core.users (email, name) values ($1,$2) on conflict (email) do update set email = excluded.email returning id",
    )
    .bind(&email)
    .bind(&name)
    .fetch_one(&mut *ctx.tx)
    .await?;
    // Admins cannot touch the owner or peers at/above their own level; nobody can grant above themselves.
    let existing: Option<String> =
        sqlx::query_scalar("select role from core.memberships where user_id = $1")
            .bind(user_id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    let me = ctx.principal.role;
    if let Some(cur) = existing.map(|r| Role::parse(&r))
        && (cur == Role::Owner || (cur >= me && me != Role::Owner))
    {
        return Err(CmdError::forbidden("cannot change this member's role"));
    }
    if role > me {
        return Err(CmdError::forbidden("cannot grant a role above your own"));
    }
    sqlx::query(
        "insert into core.memberships (workspace_id, user_id, role) values ($1,$2,$3)
         on conflict (workspace_id, user_id) do update set role = excluded.role",
    )
    .bind(ctx.workspace_id())
    .bind(user_id)
    .bind(role.as_str())
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "member.added",
        None,
        json!({"user_id": user_id, "role": role.as_str()}),
    )
    .await?;
    Ok(Member {
        user_id,
        email,
        name,
        role: role.as_str().into(),
    })
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateAgent {
    pub name: String,
    /// internal (C Note agent) | external (a coding agent over MCP)
    #[serde(default)]
    pub kind: Option<String>,
    /// Tool groups the agent's key may use; defaults to read/write_notes/write_items/manage_crm.
    #[serde(default)]
    pub scopes: Vec<String>,
}

#[derive(Serialize, JsonSchema)]
pub struct AgentCreated {
    pub agent_id: Uuid,
    pub api_key_id: Uuid,
    /// Shown once; store it in the agent's MCP client config.
    pub api_key: String,
}

fn new_key() -> (String, String) {
    let mut buf = [0u8; 24];
    rand::rng().fill_bytes(&mut buf);
    let prefix = hex::encode(&Uuid::new_v4().as_bytes()[..4]);
    let key = format!(
        "cn_{prefix}_{}",
        base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(buf)
    );
    (prefix, key)
}

/// Agent principals (ADR-014): act on behalf of their creator, never with more than the creator's role.
async fn agent_create(ctx: &mut Ctx, i: CreateAgent) -> CmdResult<AgentCreated> {
    let owner = ctx
        .principal
        .user_id
        .ok_or_else(|| CmdError::forbidden("agents must be created by a person"))?;
    if ctx.principal.kind != PrincipalKind::User {
        return Err(CmdError::forbidden("only people can create agents"));
    }
    let scopes = if i.scopes.is_empty() {
        DEFAULT_AGENT_GROUPS.iter().map(|s| s.to_string()).collect()
    } else {
        i.scopes
    };
    let agent_id: Uuid = sqlx::query_scalar(
        "insert into core.agents (workspace_id, name, kind, owner_user_id, scopes) values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(i.name.trim())
    .bind(i.kind.unwrap_or_else(|| "external".into()))
    .bind(owner)
    .bind(&scopes)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let (prefix, key) = new_key();
    let api_key_id: Uuid = sqlx::query_scalar(
        "insert into core.api_keys (workspace_id, user_id, agent_id, name, prefix, key_hash, scopes)
         values ($1,$2,$3,$4,$5,$6,$7) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(owner)
    .bind(agent_id)
    .bind(format!("agent:{}", slugify(&i.name)))
    .bind(prefix)
    .bind(sha256_hex(&key))
    .bind(&scopes)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "agent.created",
        None,
        json!({"agent_id": agent_id, "scopes": scopes}),
    )
    .await?;
    Ok(AgentCreated {
        agent_id,
        api_key_id,
        api_key: key,
    })
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct AgentOut {
    pub id: Uuid,
    pub name: String,
    pub kind: String,
    pub owner_user_id: Uuid,
    pub scopes: Vec<String>,
    pub created_at: DateTime<Utc>,
}

async fn agent_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<AgentOut>> {
    Ok(sqlx::query_as(
        "select id, name, kind, owner_user_id, scopes, created_at from core.agents order by name",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateKey {
    pub name: String,
    #[serde(default)]
    pub scopes: Vec<String>,
}

#[derive(Serialize, JsonSchema)]
pub struct KeyCreated {
    pub id: Uuid,
    /// Shown once.
    pub api_key: String,
}

async fn apikey_create(ctx: &mut Ctx, i: CreateKey) -> CmdResult<KeyCreated> {
    if ctx.principal.kind != PrincipalKind::User {
        return Err(CmdError::forbidden("only people can create API keys"));
    }
    let (prefix, key) = new_key();
    let id: Uuid = sqlx::query_scalar(
        "insert into core.api_keys (workspace_id, user_id, name, prefix, key_hash, scopes) values ($1,$2,$3,$4,$5,$6) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(ctx.principal.user_id)
    .bind(i.name)
    .bind(prefix)
    .bind(sha256_hex(&key))
    .bind(i.scopes)
    .fetch_one(&mut *ctx.tx)
    .await?;
    Ok(KeyCreated { id, api_key: key })
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct KeyOut {
    pub id: Uuid,
    pub name: String,
    pub prefix: String,
    pub agent_id: Option<Uuid>,
    pub scopes: Vec<String>,
    pub last_used_at: Option<DateTime<Utc>>,
    pub revoked_at: Option<DateTime<Utc>>,
}

async fn apikey_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<KeyOut>> {
    Ok(sqlx::query_as(
        "select id, name, prefix, agent_id, scopes, last_used_at, revoked_at from core.api_keys order by created_at desc",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

async fn apikey_revoke(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    let n = sqlx::query(
        "update core.api_keys set revoked_at = now() where id = $1 and revoked_at is null",
    )
    .bind(i.id)
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    Ok(json!({"revoked": n == 1}))
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct WorkspaceOut {
    pub id: Uuid,
    pub name: String,
    pub slug: String,
    pub plan: String,
    pub region: String,
    pub settings: Value,
}

async fn workspace_get(ctx: &mut Ctx, _: Empty) -> CmdResult<WorkspaceOut> {
    Ok(sqlx::query_as(
        "select id, name, slug, plan, region, settings from core.workspaces where id = $1",
    )
    .bind(ctx.workspace_id())
    .fetch_one(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateSettings {
    #[serde(default)]
    pub name: Option<String>,
    /// Merged into settings (e.g. agent_tool_groups, gstin, state_code, brand_kit, legal_name, address).
    pub settings: Value,
}

async fn workspace_update(ctx: &mut Ctx, i: UpdateSettings) -> CmdResult<WorkspaceOut> {
    if let Some(groups) = i
        .settings
        .get("agent_tool_groups")
        .and_then(|g| g.as_array())
    {
        for g in groups {
            if !ToolGroup::ALL
                .iter()
                .any(|t| Some(t.as_str()) == g.as_str())
            {
                return Err(CmdError::invalid(format!("unknown tool group {g}")));
            }
        }
    }
    let w: WorkspaceOut = sqlx::query_as(
        "update core.workspaces set name = coalesce($2, name), settings = settings || $3 where id = $1
         returning id, name, slug, plan, region, settings",
    )
    .bind(ctx.workspace_id())
    .bind(i.name)
    .bind(&i.settings)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "workspace.settings_updated",
        None,
        json!({"keys": i.settings.as_object().map(|o| o.keys().cloned().collect::<Vec<_>>())}),
    )
    .await?;
    Ok(w)
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct ConfirmationOut {
    pub id: Uuid,
    pub command: String,
    pub input: Value,
    pub requested_by: String,
    pub status: String,
    pub created_at: DateTime<Utc>,
}

async fn confirmation_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<ConfirmationOut>> {
    Ok(sqlx::query_as(
        "select id, command, input, requested_by, status, created_at from core.confirmations
         where status = 'pending' order by created_at desc",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct Decide {
    pub id: Uuid,
    pub approve: bool,
}

/// Human approval of a money-moving/outbound action requested by an agent (ADR-004). Logged as an event.
async fn confirmation_decide(ctx: &mut Ctx, i: Decide) -> CmdResult<Value> {
    if ctx.principal.kind != PrincipalKind::User {
        return Err(CmdError::forbidden("only a person can confirm"));
    }
    let status = if i.approve { "confirmed" } else { "rejected" };
    let cmd: Option<String> = sqlx::query_scalar(
        "update core.confirmations set status = $2, confirmed_by = $3, decided_at = now()
         where id = $1 and status = 'pending' returning command",
    )
    .bind(i.id)
    .bind(status)
    .bind(ctx.principal.user_id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let cmd = cmd.ok_or_else(|| CmdError::not_found("pending confirmation"))?;
    ctx.emit(
        "confirmation.decided",
        None,
        json!({"confirmation_id": i.id, "command": cmd, "status": status}),
    )
    .await?;
    Ok(json!({"status": status}))
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct UsageRow {
    pub kind: String,
    pub quantity: i64,
}

/// Cost dashboard data (ADR-011): last 30 days of meterable events.
async fn usage_summary(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<UsageRow>> {
    Ok(sqlx::query_as(
        "select kind, sum(quantity)::bigint as quantity from core.usage where created_at > now() - interval '30 days' group by kind order by kind",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "member.list",
            "List workspace members.",
            ToolGroup::Read,
            handler!(member_list),
        ),
        Command::new(
            "member.add",
            "Add or re-role a member by email.",
            ToolGroup::Admin,
            handler!(member_add),
        )
        .min_role(Role::Admin),
        Command::new(
            "agent.create",
            "Create an agent principal and its API key (acts on your behalf, capped by your role).",
            ToolGroup::Admin,
            handler!(agent_create),
        )
        .min_role(Role::Admin)
        .returns_secret(),
        Command::new(
            "agent.list",
            "List agent principals.",
            ToolGroup::Read,
            handler!(agent_list),
        ),
        Command::new(
            "apikey.create",
            "Create a personal API key for MCP or REST.",
            ToolGroup::Admin,
            handler!(apikey_create),
        )
        .returns_secret(),
        Command::new(
            "apikey.list",
            "List API keys (no secrets).",
            ToolGroup::Admin,
            handler!(apikey_list),
        )
        .min_role(Role::Admin),
        Command::new(
            "apikey.revoke",
            "Revoke an API key.",
            ToolGroup::Admin,
            handler!(apikey_revoke),
        )
        .min_role(Role::Admin),
        Command::new(
            "workspace.get",
            "Workspace details and settings.",
            ToolGroup::Read,
            handler!(workspace_get),
        ),
        Command::new(
            "workspace.update",
            "Update workspace name and settings (tool groups, GSTIN, brand kit).",
            ToolGroup::Admin,
            handler!(workspace_update),
        )
        .min_role(Role::Admin),
        Command::new(
            "confirmation.list",
            "Pending agent actions awaiting human confirmation.",
            ToolGroup::Read,
            handler!(confirmation_list),
        ),
        Command::new(
            "confirmation.decide",
            "Approve or reject a pending agent action.",
            ToolGroup::Admin,
            handler!(confirmation_decide),
        ),
        Command::new(
            "usage.summary",
            "Usage over the last 30 days (cost monitoring).",
            ToolGroup::Read,
            handler!(usage_summary),
        )
        .min_role(Role::Admin),
    ]
}
