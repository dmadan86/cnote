//! The command layer (ADR-004): every capability is a typed command with a JSON schema and a tool group.
//! UI, REST, MCP, automations and agents all call `Registry::execute`, so permission checks live in one place.

use crate::error::{CmdError, CmdResult, ErrorCode};
use crate::events::{self, Event};
use crate::principal::{Principal, Role};
use crate::ratelimit;
use cnote_db::Tx;
use futures::future::BoxFuture;
use schemars::JsonSchema;
use serde::{Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::collections::HashMap;
use std::sync::Arc;
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ToolGroup {
    Read,
    WriteNotes,
    WriteItems,
    ManageCrm,
    /// Money-moving.
    ManageInvoices,
    /// Outbound email/WhatsApp.
    SendMessages,
    Admin,
    Destructive,
}

impl ToolGroup {
    pub const ALL: [ToolGroup; 8] = [
        Self::Read,
        Self::WriteNotes,
        Self::WriteItems,
        Self::ManageCrm,
        Self::ManageInvoices,
        Self::SendMessages,
        Self::Admin,
        Self::Destructive,
    ];
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Read => "read",
            Self::WriteNotes => "write_notes",
            Self::WriteItems => "write_items",
            Self::ManageCrm => "manage_crm",
            Self::ManageInvoices => "manage_invoices",
            Self::SendMessages => "send_messages",
            Self::Admin => "admin",
            Self::Destructive => "destructive",
        }
    }
}

/// Groups enabled for agents/API keys when a workspace has not configured `settings.agent_tool_groups`.
/// Money-moving, outbound and destructive groups are off by default.
pub const DEFAULT_AGENT_GROUPS: [&str; 4] = ["read", "write_notes", "write_items", "manage_crm"];

pub struct Ctx {
    pub tx: Tx,
    pub principal: Principal,
    pub registry: Arc<Registry>,
}

impl Ctx {
    pub fn workspace_id(&self) -> Uuid {
        self.principal.workspace_id
    }
    /// Append an event attributed to the caller; its dispatch is enqueued in the same transaction (outbox).
    pub async fn emit(
        &mut self,
        kind: &str,
        note_id: Option<Uuid>,
        payload: Value,
    ) -> CmdResult<i64> {
        events::emit(&mut self.tx, &self.principal, kind, note_id, payload).await
    }
    /// Call another command inside this transaction. Permission checks still apply.
    pub fn call<'a>(&'a mut self, name: &'a str, input: Value) -> BoxFuture<'a, CmdResult<Value>> {
        let reg = self.registry.clone();
        Box::pin(async move { reg.run_in(self, name, input, &ExecOptions::default()).await })
    }
}

type ErasedHandler =
    Arc<dyn for<'a> Fn(&'a mut Ctx, Value) -> BoxFuture<'a, CmdResult<Value>> + Send + Sync>;
pub type HandlerFn<I, O> = for<'a> fn(&'a mut Ctx, I) -> BoxFuture<'a, CmdResult<O>>;

#[derive(Clone)]
pub struct Command {
    pub name: &'static str,
    pub description: &'static str,
    pub group: ToolGroup,
    pub min_role: Role,
    /// Money-moving or outbound: agents/API keys get a confirmation request a human must approve.
    pub confirm: bool,
    /// In the compact default MCP tool list (~25 tools); the rest are reachable through `discover`.
    pub core: bool,
    pub input_schema: Value,
    handler: ErasedHandler,
}

impl Command {
    pub fn new<I, O>(
        name: &'static str,
        description: &'static str,
        group: ToolGroup,
        h: HandlerFn<I, O>,
    ) -> Self
    where
        I: DeserializeOwned + JsonSchema + Send + 'static,
        O: Serialize + Send + 'static,
    {
        let handler: ErasedHandler = Arc::new(move |ctx: &mut Ctx, v: Value| {
            Box::pin(async move {
                let input: I = serde_json::from_value(v)
                    .map_err(|e| CmdError::invalid(format!("invalid input: {e}")))?;
                let out = h(ctx, input).await?;
                Ok(serde_json::to_value(out)?)
            })
        });
        let mut schema =
            serde_json::to_value(schemars::schema_for!(I)).unwrap_or(json!({"type": "object"}));
        if let Some(o) = schema.as_object_mut() {
            o.remove("$schema");
            o.remove("title");
        }
        Self {
            name,
            description,
            group,
            min_role: if group == ToolGroup::Read {
                Role::Guest
            } else {
                Role::Member
            },
            confirm: false,
            core: false,
            input_schema: schema,
            handler,
        }
    }
    pub fn min_role(mut self, r: Role) -> Self {
        self.min_role = r;
        self
    }
    pub fn confirm(mut self) -> Self {
        self.confirm = true;
        self
    }
    pub fn core(mut self) -> Self {
        self.core = true;
        self
    }
}

/// Wrap an `async fn(&mut Ctx, I) -> CmdResult<O>` as a command handler.
#[macro_export]
macro_rules! handler {
    ($f:path) => {
        |ctx, input| Box::pin($f(ctx, input))
    };
}

pub type WorkspaceHook = for<'a> fn(&'a mut Tx, Uuid, Uuid) -> BoxFuture<'a, anyhow::Result<()>>;
pub type EventHandler = for<'a> fn(&'a mut Ctx, &'a Event) -> BoxFuture<'a, CmdResult<()>>;
pub type JobHandler = for<'a> fn(&'a mut Ctx, Value) -> BoxFuture<'a, CmdResult<()>>;
/// Periodic sweep across all tenants (freshness date sweep, IRN window checks, dunning).
pub type SweepHandler = fn(PgPool, Arc<Registry>) -> BoxFuture<'static, anyhow::Result<()>>;

/// A module contributes commands and background behaviour. Assembled once at startup.
#[derive(Default)]
pub struct Module {
    pub commands: Vec<Command>,
    pub workspace_hooks: Vec<WorkspaceHook>,
    /// (event kind prefix, handler). Prefix "" matches every event.
    pub event_handlers: Vec<(&'static str, EventHandler)>,
    pub job_handlers: Vec<(&'static str, JobHandler)>,
    /// (name, interval seconds, handler)
    pub sweeps: Vec<(&'static str, u64, SweepHandler)>,
}

pub struct Registry {
    /// AI provider for this process (ADR-009). Per-workspace BYO-LLM keys are resolved by `cnote-ai`.
    pub ai: Arc<dyn crate::ai::ModelProvider>,
    pub commands: HashMap<&'static str, Command>,
    pub workspace_hooks: Vec<WorkspaceHook>,
    pub event_handlers: Vec<(&'static str, EventHandler)>,
    pub job_handlers: HashMap<&'static str, JobHandler>,
    pub sweeps: Vec<(&'static str, u64, SweepHandler)>,
}

#[derive(Default, Clone)]
pub struct ExecOptions {
    pub idempotency_key: Option<String>,
    pub confirmation_id: Option<Uuid>,
}

impl Registry {
    pub fn new(modules: Vec<Module>) -> Arc<Self> {
        Self::with_ai(modules, Arc::new(crate::ai::LocalProvider))
    }

    pub fn with_ai(modules: Vec<Module>, ai: Arc<dyn crate::ai::ModelProvider>) -> Arc<Self> {
        let mut r = Registry {
            ai,
            commands: HashMap::new(),
            workspace_hooks: vec![],
            event_handlers: vec![],
            job_handlers: HashMap::new(),
            sweeps: vec![],
        };
        for m in modules {
            for c in m.commands {
                assert!(
                    r.commands.insert(c.name, c.clone()).is_none(),
                    "duplicate command {}",
                    c.name
                );
            }
            r.workspace_hooks.extend(m.workspace_hooks);
            r.event_handlers.extend(m.event_handlers);
            for (k, h) in m.job_handlers {
                assert!(
                    r.job_handlers.insert(k, h).is_none(),
                    "duplicate job handler {k}"
                );
            }
            r.sweeps.extend(m.sweeps);
        }
        Arc::new(r)
    }

    pub fn get(&self, name: &str) -> Option<&Command> {
        self.commands.get(name)
    }

    pub fn list(&self) -> Vec<&Command> {
        let mut v: Vec<_> = self.commands.values().collect();
        v.sort_by_key(|c| c.name);
        v
    }

    /// Execute a command in its own tenant transaction.
    pub async fn execute(
        self: &Arc<Self>,
        pool: &PgPool,
        name: &str,
        input: Value,
        principal: &Principal,
        opts: &ExecOptions,
    ) -> CmdResult<Value> {
        let tx = cnote_db::begin_tenant(pool, principal.workspace_id).await?;
        let mut ctx = Ctx {
            tx,
            principal: principal.clone(),
            registry: self.clone(),
        };
        let out = self.run_in(&mut ctx, name, input, opts).await?;
        ctx.tx.commit().await?;
        Ok(out)
    }

    /// Execute inside an existing context (nested calls, automations, event handlers).
    pub async fn run_in(
        &self,
        ctx: &mut Ctx,
        name: &str,
        input: Value,
        opts: &ExecOptions,
    ) -> CmdResult<Value> {
        let cmd = self
            .get(name)
            .ok_or_else(|| CmdError::not_found(&format!("command {name}")))?;
        let p = ctx.principal.clone();
        if p.role < cmd.min_role {
            return Err(CmdError::forbidden(format!(
                "{name} requires role {}",
                cmd.min_role.as_str()
            )));
        }
        if let Some(scopes) = &p.scopes
            && !scopes.iter().any(|s| s == "*" || s == cmd.group.as_str())
        {
            return Err(CmdError::forbidden(format!(
                "scope {} not granted",
                cmd.group.as_str()
            )));
        }
        if p.kind != crate::principal::PrincipalKind::System {
            let who = p.user_id.or(p.agent_id).unwrap_or_default();
            ratelimit::check(&format!("{}:{who}", p.workspace_id))?;
        }

        if p.is_machine() {
            let settings: Value =
                sqlx::query_scalar("select settings from core.workspaces where id = $1")
                    .bind(p.workspace_id)
                    .fetch_one(&mut *ctx.tx)
                    .await?;
            let allowed = match settings.get("agent_tool_groups").and_then(|v| v.as_array()) {
                Some(a) => a.iter().any(|g| g.as_str() == Some(cmd.group.as_str())),
                None => DEFAULT_AGENT_GROUPS.contains(&cmd.group.as_str()),
            };
            if !allowed {
                return Err(CmdError::forbidden(format!(
                    "tool group {} is disabled for agents in this workspace",
                    cmd.group.as_str()
                )));
            }
        }

        if let Some(key) = &opts.idempotency_key {
            let hit: Option<(String, Value)> = sqlx::query_as(
                "select command, result from core.idempotency where workspace_id = $1 and key = $2",
            )
            .bind(p.workspace_id)
            .bind(key)
            .fetch_optional(&mut *ctx.tx)
            .await?;
            if let Some((command, result)) = hit {
                if command != name {
                    return Err(CmdError::conflict(
                        "idempotency key reused for a different command",
                    ));
                }
                return Ok(result);
            }
        }

        if cmd.confirm && p.is_machine() {
            match opts.confirmation_id {
                None => {
                    let requested_by = p
                        .agent_id
                        .or(p.user_id)
                        .map(|u| u.to_string())
                        .unwrap_or_default();
                    let id: Uuid = sqlx::query_scalar(
                        "insert into core.confirmations (workspace_id, command, input, requested_by) values ($1,$2,$3,$4) returning id",
                    )
                    .bind(p.workspace_id)
                    .bind(name)
                    .bind(&input)
                    .bind(requested_by)
                    .fetch_one(&mut *ctx.tx)
                    .await?;
                    return Ok(json!({
                        "confirmation_required": true,
                        "confirmation_id": id,
                        "message": format!("{name} moves money or sends a message; a human must confirm it in C Note."),
                    }));
                }
                Some(cid) => {
                    // The confirmed input is authoritative: the agent cannot swap the payload after approval.
                    let confirmed: Option<Value> = sqlx::query_scalar(
                        "update core.confirmations set status = 'expired' where id = $1 and command = $2 and status = 'confirmed' returning input",
                    )
                    .bind(cid)
                    .bind(name)
                    .fetch_optional(&mut *ctx.tx)
                    .await?;
                    let Some(confirmed) = confirmed else {
                        return Err(CmdError::forbidden(
                            "confirmation missing, rejected or already used",
                        ));
                    };
                    let out = (cmd.handler)(ctx, confirmed).await?;
                    crate::events::record_usage(
                        &mut ctx.tx,
                        p.workspace_id,
                        "mcp.call",
                        1,
                        json!({"command": name}),
                    )
                    .await?;
                    return Ok(out);
                }
            }
        }

        let out = (cmd.handler)(ctx, input).await?;

        if let Some(key) = &opts.idempotency_key {
            sqlx::query("insert into core.idempotency (workspace_id, key, command, result) values ($1,$2,$3,$4)")
                .bind(p.workspace_id)
                .bind(key)
                .bind(name)
                .bind(&out)
                .execute(&mut *ctx.tx)
                .await?;
        }
        if p.is_machine() {
            crate::events::record_usage(
                &mut ctx.tx,
                p.workspace_id,
                "mcp.call",
                1,
                json!({"command": name}),
            )
            .await?;
        }
        Ok(out)
    }
}

pub fn err_code(e: &CmdError) -> ErrorCode {
    e.code
}
