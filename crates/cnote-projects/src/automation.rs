//! Automations (ADR-007): when X (event or schedule) and conditions, do actions. Actions run through the command
//! layer, so permissions apply: they run as a system principal capped at the role of the person who created the rule.

use crate::items::{item_matches, matching_item_ids};
use chrono::{DateTime, Duration, Utc};
use cnote_core::access::{assert_space_access, readable_space_ids};
use cnote_core::events::{self, Event};
use cnote_core::{
    CmdError, CmdResult, Command, Ctx, Principal, PrincipalKind, Registry, Role, ToolGroup, handler,
};
use futures::future::BoxFuture;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

/// Loop protection: an automation that fires more often than this per minute is blocked.
pub const MAX_RUNS_PER_MINUTE: i64 = 10;
const SCHEDULE_ITEM_CAP: i64 = 100;

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Trigger {
    /// Fires when an event of this kind is emitted (item.transitioned, item.created, item.assigned_to_agent, ...).
    /// With `cnql`, only when the event's item matches the query.
    Event {
        event: String,
        #[serde(default)]
        cnql: Option<String>,
    },
    /// Fires every N minutes for each item matching `cnql` (or once when there is no query).
    Schedule {
        every_minutes: u32,
        #[serde(default)]
        cnql: Option<String>,
    },
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
pub struct Condition {
    /// The triggering item must match this CNQL query.
    pub cnql: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Action {
    Transition {
        to: String,
    },
    Assign {
        #[serde(default)]
        user_id: Option<Uuid>,
        #[serde(default)]
        agent_id: Option<Uuid>,
    },
    Comment {
        body: String,
    },
    /// Any command; "{{item.id}}" and "{{item.key}}" in string values are replaced with the triggering item.
    Command {
        name: String,
        input: Value,
    },
}

fn validate(ctx: &Ctx, t: &Trigger, conds: &[Condition], actions: &[Action]) -> CmdResult<()> {
    let q = |s: &str| {
        crate::cnql::parse(s)
            .map(|_| ())
            .map_err(|e| CmdError::invalid(format!("cnql: {e}")))
    };
    match t {
        Trigger::Event { event, cnql } => {
            if event.trim().is_empty() {
                return Err(CmdError::invalid("trigger.event is required"));
            }
            cnql.as_deref().map_or(Ok(()), q)?;
        }
        Trigger::Schedule {
            every_minutes,
            cnql,
        } => {
            if *every_minutes == 0 {
                return Err(CmdError::invalid("every_minutes must be at least 1"));
            }
            cnql.as_deref().map_or(Ok(()), q)?;
        }
    }
    for c in conds {
        q(&c.cnql)?;
    }
    if actions.is_empty() {
        return Err(CmdError::invalid("at least one action is required"));
    }
    for a in actions {
        match a {
            Action::Command { name, .. } if ctx.registry.get(name).is_none() => {
                return Err(CmdError::invalid(format!("unknown command '{name}'")));
            }
            Action::Assign { user_id, agent_id } if user_id.is_some() && agent_id.is_some() => {
                return Err(CmdError::invalid(
                    "assign: give user_id or agent_id, not both",
                ));
            }
            Action::Transition { to } if to.trim().is_empty() => {
                return Err(CmdError::invalid("transition: 'to' is required"));
            }
            _ => {}
        }
    }
    Ok(())
}

#[derive(Serialize, sqlx::FromRow)]
pub struct AutomationOut {
    id: Uuid,
    space_id: Option<Uuid>,
    name: String,
    enabled: bool,
    draft: bool,
    trigger: Value,
    conditions: Value,
    actions: Value,
    created_by: Option<Uuid>,
    created_at: DateTime<Utc>,
}

const AUTO_COLS: &str =
    "id, space_id, name, enabled, draft, trigger, conditions, actions, created_by, created_at";

#[derive(Deserialize, JsonSchema)]
pub struct CreateAutomation {
    /// Limit the rule to one Space. Without it the rule is workspace-wide and needs an admin.
    #[serde(default)]
    pub space_id: Option<Uuid>,
    pub name: String,
    pub trigger: Trigger,
    #[serde(default)]
    pub conditions: Vec<Condition>,
    pub actions: Vec<Action>,
    #[serde(default = "yes")]
    pub enabled: bool,
    /// Drafts (e.g. imported Jira rules) never run until published.
    #[serde(default)]
    pub draft: bool,
}

fn yes() -> bool {
    true
}

async fn automation_create(ctx: &mut Ctx, i: CreateAutomation) -> CmdResult<AutomationOut> {
    match i.space_id {
        Some(s) => assert_space_access(ctx, s, true).await?,
        None if ctx.principal.role < Role::Admin => {
            return Err(CmdError::forbidden(
                "workspace-wide automations need an admin; pass space_id",
            ));
        }
        None => {}
    }
    if i.name.trim().is_empty() {
        return Err(CmdError::invalid("name is required"));
    }
    validate(ctx, &i.trigger, &i.conditions, &i.actions)?;
    let a: AutomationOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "insert into projects.automations (workspace_id, space_id, name, enabled, draft, trigger, conditions, actions, created_by)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9) returning {AUTO_COLS}"
    )))
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.name.trim())
    .bind(i.enabled)
    .bind(i.draft)
    .bind(serde_json::to_value(&i.trigger)?)
    .bind(serde_json::to_value(&i.conditions)?)
    .bind(serde_json::to_value(&i.actions)?)
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "automation.created",
        None,
        json!({"automation_id": a.id, "name": a.name}),
    )
    .await?;
    Ok(a)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListAutomations {
    #[serde(default)]
    pub space_id: Option<Uuid>,
}

async fn automation_list(ctx: &mut Ctx, i: ListAutomations) -> CmdResult<Vec<AutomationOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {AUTO_COLS} from projects.automations
         where (space_id is null or space_id = any($1)) and ($2::uuid is null or space_id = $2) order by created_at"
    )))
    .bind(spaces)
    .bind(i.space_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateAutomation {
    pub id: Uuid,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub enabled: Option<bool>,
    /// Set false to publish a draft.
    #[serde(default)]
    pub draft: Option<bool>,
    #[serde(default)]
    pub trigger: Option<Trigger>,
    #[serde(default)]
    pub conditions: Option<Vec<Condition>>,
    #[serde(default)]
    pub actions: Option<Vec<Action>>,
}

async fn load_automation(ctx: &mut Ctx, id: Uuid, write: bool) -> CmdResult<AutomationOut> {
    let a: AutomationOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {AUTO_COLS} from projects.automations where id = $1"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("automation"))?;
    match a.space_id {
        Some(s) => assert_space_access(ctx, s, write).await?,
        None if write && ctx.principal.role < Role::Admin => {
            return Err(CmdError::forbidden(
                "workspace-wide automations need an admin",
            ));
        }
        None => {}
    }
    Ok(a)
}

async fn automation_update(ctx: &mut Ctx, i: UpdateAutomation) -> CmdResult<AutomationOut> {
    let cur = load_automation(ctx, i.id, true).await?;
    let trigger: Trigger = match &i.trigger {
        Some(t) => t.clone(),
        None => serde_json::from_value(cur.trigger.clone())?,
    };
    let conds: Vec<Condition> = match &i.conditions {
        Some(c) => c.clone(),
        None => serde_json::from_value(cur.conditions.clone())?,
    };
    let actions: Vec<Action> = match &i.actions {
        Some(a) => a.clone(),
        None => serde_json::from_value(cur.actions.clone())?,
    };
    validate(ctx, &trigger, &conds, &actions)?;
    let a: AutomationOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "update projects.automations set name = coalesce($2, name), enabled = coalesce($3, enabled),
           draft = coalesce($4, draft), trigger = $5, conditions = $6, actions = $7
         where id = $1 returning {AUTO_COLS}"
    )))
    .bind(i.id)
    .bind(i.name.as_deref().map(str::trim))
    .bind(i.enabled)
    .bind(i.draft)
    .bind(serde_json::to_value(&trigger)?)
    .bind(serde_json::to_value(&conds)?)
    .bind(serde_json::to_value(&actions)?)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "automation.updated",
        None,
        json!({"automation_id": a.id, "enabled": a.enabled, "draft": a.draft}),
    )
    .await?;
    Ok(a)
}

#[derive(Deserialize, JsonSchema)]
pub struct AutomationRuns {
    pub automation_id: Uuid,
    /// Default 50, max 200.
    #[serde(default)]
    pub limit: Option<i64>,
}

#[derive(Serialize, sqlx::FromRow)]
pub struct RunOut {
    id: i64,
    event_id: Option<i64>,
    status: String,
    log: Value,
    created_at: DateTime<Utc>,
}

async fn automation_runs(ctx: &mut Ctx, i: AutomationRuns) -> CmdResult<Vec<RunOut>> {
    load_automation(ctx, i.automation_id, false).await?;
    Ok(sqlx::query_as(
        "select id, event_id, status, log, created_at from projects.automation_runs
         where automation_id = $1 order by id desc limit $2",
    )
    .bind(i.automation_id)
    .bind(i.limit.unwrap_or(50).clamp(1, 200))
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- execution ----------

#[derive(sqlx::FromRow)]
struct AutoRow {
    id: Uuid,
    space_id: Option<Uuid>,
    trigger: Value,
    conditions: Value,
    actions: Value,
    created_by: Option<Uuid>,
}

const ROW_COLS: &str = "id, space_id, trigger, conditions, actions, created_by";

fn subst(v: &Value, id: Option<Uuid>, key: &str) -> Value {
    match v {
        Value::String(s) => Value::String(
            s.replace(
                "{{item.id}}",
                &id.map(|i| i.to_string()).unwrap_or_default(),
            )
            .replace("{{item.key}}", key),
        ),
        Value::Array(a) => Value::Array(a.iter().map(|x| subst(x, id, key)).collect()),
        Value::Object(o) => Value::Object(
            o.iter()
                .map(|(k, x)| (k.clone(), subst(x, id, key)))
                .collect(),
        ),
        x => x.clone(),
    }
}

async fn run_action(ctx: &mut Ctx, a: &Action, item: Option<Uuid>, key: &str) -> CmdResult<Value> {
    let need_item =
        || item.ok_or_else(|| CmdError::invalid("this action needs an item; the trigger has none"));
    match a {
        Action::Transition { to } => {
            ctx.call("item.transition", json!({"id": need_item()?, "to": to}))
                .await
        }
        Action::Assign { user_id, agent_id } => {
            ctx.call(
                "item.assign",
                json!({"id": need_item()?, "user_id": user_id, "agent_id": agent_id}),
            )
            .await
        }
        Action::Comment { body } => {
            ctx.call(
                "comment.add",
                json!({"note_id": need_item()?, "body": subst(&json!(body), item, key)}),
            )
            .await
        }
        Action::Command { name, input } => ctx.call(name, subst(input, item, key)).await,
    }
}

fn action_name(a: &Action) -> &'static str {
    match a {
        Action::Transition { .. } => "transition",
        Action::Assign { .. } => "assign",
        Action::Comment { .. } => "comment",
        Action::Command { .. } => "command",
    }
}

enum Outcome {
    Skipped(String),
    Success(Vec<Value>),
    Failed(Vec<Value>),
}

/// Conditions + actions for one target item, all-or-nothing behind a savepoint.
async fn exec_target(ctx: &mut Ctx, a: &AutoRow, item: Option<Uuid>) -> CmdResult<Outcome> {
    let conds: Vec<Condition> = serde_json::from_value(a.conditions.clone()).unwrap_or_default();
    for c in &conds {
        let ok = match item {
            Some(id) => item_matches(ctx, id, &c.cnql).await?,
            None => false,
        };
        if !ok {
            return Ok(Outcome::Skipped(format!("condition not met: {}", c.cnql)));
        }
    }
    let actions: Vec<Action> = serde_json::from_value(a.actions.clone())?;
    let key: String = match item {
        Some(id) => sqlx::query_scalar("select key from projects.items where note_id = $1")
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?
            .unwrap_or_default(),
        None => String::new(),
    };
    sqlx::query("savepoint automation_target")
        .execute(&mut *ctx.tx)
        .await?;
    let mut log = vec![];
    for act in &actions {
        match run_action(ctx, act, item, &key).await {
            Ok(_) => log.push(json!({"action": action_name(act), "item": key, "ok": true})),
            Err(e) => {
                sqlx::query("rollback to savepoint automation_target")
                    .execute(&mut *ctx.tx)
                    .await?;
                log.push(json!({"action": action_name(act), "item": key, "ok": false, "error": e.message}));
                return Ok(Outcome::Failed(log));
            }
        }
    }
    sqlx::query("release savepoint automation_target")
        .execute(&mut *ctx.tx)
        .await?;
    Ok(Outcome::Success(log))
}

async fn run_principal(ctx: &mut Ctx, created_by: Option<Uuid>) -> CmdResult<Principal> {
    let role = match created_by {
        Some(u) => {
            sqlx::query_scalar::<_, String>("select role from core.memberships where user_id = $1")
                .bind(u)
                .fetch_optional(&mut *ctx.tx)
                .await?
                .map_or(Role::Guest, |r| Role::parse(&r))
        }
        None => Role::Member, // imported rules have no author
    };
    Ok(Principal {
        kind: PrincipalKind::System,
        user_id: created_by,
        agent_id: None,
        workspace_id: ctx.workspace_id(),
        role,
        scopes: None,
    })
}

/// One firing of an automation over its targets, logged as a single row in `automation_runs`.
async fn fire(
    ctx: &mut Ctx,
    a: &AutoRow,
    event_id: Option<i64>,
    targets: Vec<Option<Uuid>>,
) -> CmdResult<&'static str> {
    // Serialise firings per automation so the rate check below cannot be raced.
    sqlx::query("select pg_advisory_xact_lock(hashtextextended($1, 0))")
        .bind(a.id.to_string())
        .execute(&mut *ctx.tx)
        .await?;
    let recent: i64 = sqlx::query_scalar(
        "select count(*) from projects.automation_runs
         where automation_id = $1 and status <> 'loop_blocked' and created_at > now() - interval '1 minute'",
    )
    .bind(a.id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let (status, log) = if recent >= MAX_RUNS_PER_MINUTE {
        (
            "loop_blocked",
            vec![
                json!({"error": format!("ran {recent} times in the last minute (limit {MAX_RUNS_PER_MINUTE}); possible loop")}),
            ],
        )
    } else {
        let saved = ctx.principal.clone();
        ctx.principal = run_principal(ctx, a.created_by).await?;
        let (mut log, mut failed, mut ran) = (vec![], false, false);
        for t in targets {
            match exec_target(ctx, a, t).await {
                Ok(Outcome::Skipped(why)) => log.push(json!({"skipped": why})),
                Ok(Outcome::Success(l)) => {
                    ran = true;
                    log.extend(l)
                }
                Ok(Outcome::Failed(l)) => {
                    failed = true;
                    log.extend(l)
                }
                Err(e) => {
                    failed = true;
                    log.push(json!({"error": e.message}))
                }
            }
        }
        ctx.principal = saved;
        (
            if failed {
                "failed"
            } else if ran {
                "success"
            } else {
                "skipped"
            },
            log,
        )
    };
    sqlx::query("insert into projects.automation_runs (workspace_id, automation_id, event_id, status, log) values ($1,$2,$3,$4,$5)")
        .bind(ctx.workspace_id())
        .bind(a.id)
        .bind(event_id)
        .bind(status)
        .bind(json!(log))
        .execute(&mut *ctx.tx)
        .await?;
    if status != "skipped" && status != "loop_blocked" {
        events::record_usage(
            &mut ctx.tx,
            ctx.principal.workspace_id,
            "automation.run",
            1,
            json!({"automation_id": a.id}),
        )
        .await?;
    }
    Ok(status)
}

/// Event handler: run every enabled, non-draft event automation whose trigger matches. Failures are logged
/// in `automation_runs`, never returned, so one broken rule cannot make the whole event dispatch retry.
pub fn on_event<'a>(ctx: &'a mut Ctx, e: &'a Event) -> BoxFuture<'a, CmdResult<()>> {
    Box::pin(async move {
        let rows: Vec<AutoRow> = sqlx::query_as(sqlx::AssertSqlSafe(format!(
            "select {ROW_COLS} from projects.automations
             where enabled and not draft and trigger->>'kind' = 'event' and trigger->>'event' = $1 order by created_at"
        )))
        .bind(&e.kind)
        .fetch_all(&mut *ctx.tx)
        .await?;
        if rows.is_empty() {
            return Ok(());
        }
        let item: Option<Uuid> = match e.note_id {
            Some(n) => {
                sqlx::query_scalar("select note_id from projects.items where note_id = $1")
                    .bind(n)
                    .fetch_optional(&mut *ctx.tx)
                    .await?
            }
            None => None,
        };
        let space: Option<Uuid> = match e.note_id {
            Some(n) => {
                sqlx::query_scalar("select space_id from core.notes where id = $1")
                    .bind(n)
                    .fetch_optional(&mut *ctx.tx)
                    .await?
            }
            None => e.payload["space_id"].as_str().and_then(|s| s.parse().ok()),
        };
        for a in rows {
            if a.space_id.is_some() && a.space_id != space {
                continue;
            }
            let Ok(Trigger::Event { cnql, .. }) =
                serde_json::from_value::<Trigger>(a.trigger.clone())
            else {
                continue;
            };
            if let Some(q) = cnql {
                let ok = match item {
                    Some(id) => item_matches(ctx, id, &q).await.unwrap_or(false),
                    None => false,
                };
                if !ok {
                    continue;
                }
            }
            if let Err(err) = fire(ctx, &a, Some(e.id), vec![item]).await {
                tracing::warn!(automation = %a.id, error = %err, "automation firing failed");
            }
        }
        Ok(())
    })
}

/// Run due schedule automations of one workspace. Returns how many fired.
pub async fn sweep_workspace(
    pool: &PgPool,
    registry: &Arc<Registry>,
    ws: Uuid,
) -> CmdResult<usize> {
    let mut tx = cnote_db::begin_tenant(pool, ws).await?;
    let ids: Vec<Uuid> = sqlx::query_scalar(
        "select id from projects.automations where enabled and not draft and trigger->>'kind' = 'schedule'",
    )
    .fetch_all(&mut *tx)
    .await?;
    tx.commit().await?;
    let mut fired = 0;
    for id in ids {
        let tx = cnote_db::begin_tenant(pool, ws).await?;
        let mut ctx = Ctx {
            tx,
            principal: Principal::system(ws),
            registry: registry.clone(),
        };
        // Another worker may be sweeping the same rule; let it.
        let locked: bool =
            sqlx::query_scalar("select pg_try_advisory_xact_lock(hashtextextended($1, 0))")
                .bind(format!("sweep:{id}"))
                .fetch_one(&mut *ctx.tx)
                .await?;
        if !locked {
            continue;
        }
        let a: AutoRow = sqlx::query_as(sqlx::AssertSqlSafe(format!(
            "select {ROW_COLS} from projects.automations where id = $1"
        )))
        .bind(id)
        .fetch_one(&mut *ctx.tx)
        .await?;
        let Ok(Trigger::Schedule {
            every_minutes,
            cnql,
        }) = serde_json::from_value::<Trigger>(a.trigger.clone())
        else {
            continue;
        };
        let last: Option<DateTime<Utc>> = sqlx::query_scalar(
            "select max(created_at) from projects.automation_runs where automation_id = $1 and status <> 'loop_blocked'",
        )
        .bind(id)
        .fetch_one(&mut *ctx.tx)
        .await?;
        if last.is_some_and(|l| Utc::now() - l < Duration::minutes(every_minutes as i64)) {
            continue;
        }
        let targets: Vec<Option<Uuid>> = match cnql {
            Some(q) => match matching_item_ids(&mut ctx, a.space_id, &q, SCHEDULE_ITEM_CAP).await {
                Ok(ids) => ids.into_iter().map(Some).collect(),
                Err(e) => {
                    tracing::warn!(automation = %id, error = %e, "schedule query failed");
                    continue;
                }
            },
            None => vec![None],
        };
        if targets.is_empty() {
            continue;
        }
        fire(&mut ctx, &a, None, targets).await?;
        ctx.tx.commit().await?;
        fired += 1;
    }
    Ok(fired)
}

pub fn sweep(pool: PgPool, registry: Arc<Registry>) -> BoxFuture<'static, anyhow::Result<()>> {
    Box::pin(async move {
        let ids: Vec<Uuid> = sqlx::query_scalar("select core.all_workspace_ids()")
            .fetch_all(&pool)
            .await?;
        for ws in ids {
            if let Err(e) = sweep_workspace(&pool, &registry, ws).await {
                tracing::warn!(workspace = %ws, error = %e, "automation sweep failed");
            }
        }
        Ok(())
    })
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "automation.create",
            "Create an automation: a trigger (event or schedule), optional CNQL conditions, and actions (transition, assign, comment, command).",
            ToolGroup::WriteItems,
            handler!(automation_create),
        ),
        Command::new(
            "automation.list",
            "List automations you can see.",
            ToolGroup::Read,
            handler!(automation_list),
        ),
        Command::new(
            "automation.update",
            "Edit, enable/disable or publish (draft=false) an automation.",
            ToolGroup::WriteItems,
            handler!(automation_update),
        ),
        Command::new(
            "automation.runs",
            "Run log of an automation (success, skipped, failed, loop_blocked).",
            ToolGroup::Read,
            handler!(automation_runs),
        ),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn serde_shapes() {
        let t: Trigger = serde_json::from_value(
            json!({"kind": "event", "event": "item.transitioned", "cnql": "status = Done"}),
        )
        .unwrap();
        assert!(matches!(t, Trigger::Event { .. }));
        let t: Trigger =
            serde_json::from_value(json!({"kind": "schedule", "every_minutes": 60})).unwrap();
        assert!(matches!(
            t,
            Trigger::Schedule {
                every_minutes: 60,
                cnql: None
            }
        ));
        let a: Vec<Action> = serde_json::from_value(json!([
            {"type": "transition", "to": "Done"}, {"type": "assign", "agent_id": Uuid::nil()},
            {"type": "comment", "body": "hi"}, {"type": "command", "name": "item.update", "input": {}}
        ]))
        .unwrap();
        assert_eq!(a.len(), 4);
        assert!(serde_json::from_value::<Trigger>(json!({"kind": "webhook"})).is_err());
    }

    #[test]
    fn substitution() {
        let id = Uuid::nil();
        let v = subst(
            &json!({"a": ["{{item.key}} x", 1], "b": {"c": "{{item.id}}"}}),
            Some(id),
            "ENG-1",
        );
        assert_eq!(v, json!({"a": ["ENG-1 x", 1], "b": {"c": id.to_string()}}));
    }
}
