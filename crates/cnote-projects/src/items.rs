//! Items: Notes carrying an item type plus a row in `projects.items`. Workflow, assignment, ranking, triage.

use crate::cnql;
use crate::wf::{definition_for, space_wf};
use crate::workflow::{GuardInput, PostAction};
use chrono::{DateTime, NaiveDate, Utc};
use cnote_core::access::{assert_note_access, assert_space_access, readable_space_ids, viewer};
use cnote_core::notes::{CreateNote, insert_note};
use cnote_core::{CmdError, CmdResult, Command, Ctx, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::HashSet;
use uuid::Uuid;

pub const PRIORITIES: [&str; 5] = ["urgent", "high", "medium", "low", "none"];

#[derive(Debug, Serialize, sqlx::FromRow)]
pub struct ItemOut {
    pub id: Uuid,
    pub space_id: Uuid,
    pub key: String,
    pub title: String,
    pub description: String,
    pub item_type: String,
    pub status: String,
    pub status_category: String,
    pub priority: String,
    pub assignee_id: Option<Uuid>,
    pub assignee_agent_id: Option<Uuid>,
    pub reporter_id: Option<Uuid>,
    pub parent_id: Option<Uuid>,
    pub sprint_id: Option<Uuid>,
    pub fix_version_id: Option<Uuid>,
    pub component_id: Option<Uuid>,
    pub estimate: Option<f64>,
    pub due_date: Option<NaiveDate>,
    pub labels: Vec<String>,
    pub custom: Value,
    pub rank: String,
    pub workflow_version: i32,
    pub triage: bool,
    pub resolved_at: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

const ITEM_SELECT: &str = "select i.note_id as id, i.space_id, i.key, n.title, n.content_md as description, i.item_type, i.status,
    i.status_category, i.priority, i.assignee_id, i.assignee_agent_id, i.reporter_id, n.parent_id, i.sprint_id,
    i.fix_version_id, i.component_id, i.estimate::float8 as estimate, i.due_date, i.labels, i.custom, i.rank,
    i.workflow_version, i.triage, i.resolved_at, n.created_at, n.updated_at
    from projects.items i join core.notes n on n.id = i.note_id join core.spaces sp on sp.id = i.space_id";

#[derive(Deserialize, JsonSchema, Clone)]
pub struct ItemRef {
    /// Item id (the note id). Give this or `key`.
    #[serde(default)]
    pub id: Option<Uuid>,
    /// Item key such as ENG-42.
    #[serde(default)]
    pub key: Option<String>,
}

pub async fn load_item(ctx: &mut Ctx, r: &ItemRef, write: bool) -> CmdResult<ItemOut> {
    if r.id.is_none() && r.key.is_none() {
        return Err(CmdError::invalid("id or key is required"));
    }
    let item: ItemOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "{ITEM_SELECT} where n.deleted_at is null and (i.note_id = $1 or i.key = upper($2))"
    )))
    .bind(r.id)
    .bind(r.key.as_deref())
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("item"))?;
    // Item pages can be restricted or shared like any other note.
    assert_note_access(ctx, item.id, write).await?;
    Ok(item)
}

/// Fields a `required_fields` guard can ask for.
pub fn filled_fields(i: &ItemOut) -> HashSet<String> {
    let mut s: HashSet<String> = HashSet::new();
    let mut add = |name: &str, ok: bool| {
        if ok {
            s.insert(name.to_string());
        }
    };
    add(
        "assignee",
        i.assignee_id.is_some() || i.assignee_agent_id.is_some(),
    );
    add("estimate", i.estimate.is_some());
    add("due_date", i.due_date.is_some());
    add("labels", !i.labels.is_empty());
    add("sprint", i.sprint_id.is_some());
    add("fix_version", i.fix_version_id.is_some());
    add("component", i.component_id.is_some());
    add("priority", i.priority != "none");
    add("description", !i.description.trim().is_empty());
    add("parent", i.parent_id.is_some());
    if let Some(o) = i.custom.as_object() {
        for (k, v) in o {
            if !v.is_null() && v != "" {
                s.insert(k.clone());
            }
        }
    }
    s
}

fn check_priority(p: &str) -> CmdResult<()> {
    if PRIORITIES.contains(&p) {
        Ok(())
    } else {
        Err(CmdError::invalid(format!(
            "priority must be one of {}",
            PRIORITIES.join(", ")
        )))
    }
}

async fn validate_assignee(
    ctx: &mut Ctx,
    user: Option<Uuid>,
    agent: Option<Uuid>,
) -> CmdResult<()> {
    if user.is_some() && agent.is_some() {
        return Err(CmdError::invalid("assign to a user or an agent, not both"));
    }
    if let Some(u) = user {
        let ok: bool =
            sqlx::query_scalar("select exists(select 1 from core.memberships where user_id = $1)")
                .bind(u)
                .fetch_one(&mut *ctx.tx)
                .await?;
        if !ok {
            return Err(CmdError::invalid(
                "assignee is not a member of this workspace",
            ));
        }
    }
    if let Some(a) = agent {
        let ok: bool = sqlx::query_scalar("select exists(select 1 from core.agents where id = $1)")
            .bind(a)
            .fetch_one(&mut *ctx.tx)
            .await?;
        if !ok {
            return Err(CmdError::invalid("agent not found"));
        }
    }
    Ok(())
}

/// The sprint / version / component must belong to the item's Space.
async fn assert_in_space(
    ctx: &mut Ctx,
    table: &'static str,
    id: Option<Uuid>,
    space: Uuid,
) -> CmdResult<()> {
    let Some(id) = id else { return Ok(()) };
    let ok: bool = sqlx::query_scalar(sqlx::AssertSqlSafe(format!(
        "select exists(select 1 from {table} where id = $1 and space_id = $2)"
    )))
    .bind(id)
    .bind(space)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if ok {
        Ok(())
    } else {
        Err(CmdError::invalid(format!(
            "{table} {id} is not in this space"
        )))
    }
}

// ---------- rank (lexicographic fractional indexing over a-z) ----------

fn digit(c: u8) -> u32 {
    c.saturating_sub(b'a').min(25) as u32
}

fn mid(a: &[u8], b: Option<&[u8]>) -> Vec<u8> {
    if let Some(b) = b {
        let mut n = 0;
        while n < b.len() && a.get(n).copied().unwrap_or(b'a') == b[n] {
            n += 1;
        }
        if n > 0 {
            let mut r = b[..n].to_vec();
            r.extend(mid(&a[n.min(a.len())..], Some(&b[n..])));
            return r;
        }
    }
    let da = a.first().map_or(0, |c| digit(*c));
    let db = b.and_then(|b| b.first()).map_or(26, |c| digit(*c));
    if db > da + 1 {
        vec![b'a' + (da + db).div_ceil(2) as u8]
    } else if let Some(b) = b
        && b.len() > 1
    {
        vec![b[0]]
    } else {
        let mut r = vec![b'a' + da as u8];
        r.extend(mid(a.get(1..).unwrap_or(&[]), None));
        r
    }
}

/// A rank strictly between `lo` and `hi` (None = open end). Equal neighbours get a suffix, which keeps order
/// stable through the `number` tie-break.
pub fn rank_between(lo: Option<&str>, hi: Option<&str>) -> String {
    let lo = lo.unwrap_or("");
    if let Some(h) = hi
        && lo >= h
    {
        return format!("{lo}n");
    }
    String::from_utf8(mid(lo.as_bytes(), hi.map(str::as_bytes))).unwrap_or_else(|_| "n".into())
}

// ---------- create ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateItem {
    /// Space id. Give this or `space_key`.
    #[serde(default)]
    pub space_id: Option<Uuid>,
    /// Space key such as ENG.
    #[serde(default)]
    pub space_key: Option<String>,
    pub title: String,
    /// epic | story | task | bug | subtask (whatever the Space's workflow allows). Default task.
    #[serde(default)]
    pub item_type: Option<String>,
    /// Markdown description.
    #[serde(default)]
    pub description: Option<String>,
    /// urgent | high | medium | low | none
    #[serde(default)]
    pub priority: Option<String>,
    #[serde(default)]
    pub assignee_id: Option<Uuid>,
    /// Assign to an agent principal instead of a person.
    #[serde(default)]
    pub assignee_agent_id: Option<Uuid>,
    /// Parent item (epic or story).
    #[serde(default)]
    pub parent_id: Option<Uuid>,
    #[serde(default)]
    pub labels: Vec<String>,
    /// Story points.
    #[serde(default)]
    pub estimate: Option<f64>,
    #[serde(default)]
    pub due_date: Option<NaiveDate>,
    #[serde(default)]
    pub sprint_id: Option<Uuid>,
    #[serde(default)]
    pub fix_version_id: Option<Uuid>,
    #[serde(default)]
    pub component_id: Option<Uuid>,
    /// Custom field values.
    #[serde(default)]
    pub custom: Option<Value>,
    /// Put the item in the triage inbox. Defaults to true when an agent or API key creates it.
    #[serde(default)]
    pub triage: Option<bool>,
}

async fn item_create(ctx: &mut Ctx, i: CreateItem) -> CmdResult<ItemOut> {
    let title = i.title.trim().to_string();
    if title.is_empty() {
        return Err(CmdError::invalid("title is required"));
    }
    let space_id: Uuid = match (i.space_id, &i.space_key) {
        (Some(id), _) => id,
        (None, Some(k)) => sqlx::query_scalar("select id from core.spaces where key = upper($1)")
            .bind(k.trim())
            .fetch_optional(&mut *ctx.tx)
            .await?
            .ok_or_else(|| CmdError::not_found("space"))?,
        _ => return Err(CmdError::invalid("space_id or space_key is required")),
    };
    assert_space_access(ctx, space_id, true).await?;
    let wf = space_wf(ctx, space_id).await?;
    let item_type = i
        .item_type
        .as_deref()
        .unwrap_or("task")
        .trim()
        .to_lowercase();
    if !wf.def.types.is_empty() && !wf.def.types.contains(&item_type) {
        return Err(CmdError::invalid(format!(
            "item type '{item_type}' is not allowed here; use one of {}",
            wf.def.types.join(", ")
        )));
    }
    let priority = i.priority.as_deref().unwrap_or("medium");
    check_priority(priority)?;
    validate_assignee(ctx, i.assignee_id, i.assignee_agent_id).await?;
    assert_in_space(ctx, "projects.sprints", i.sprint_id, space_id).await?;
    assert_in_space(ctx, "projects.versions", i.fix_version_id, space_id).await?;
    assert_in_space(ctx, "projects.components", i.component_id, space_id).await?;

    let note = insert_note(
        ctx,
        CreateNote {
            space_id,
            title,
            parent_id: i.parent_id,
            content_md: i.description,
            types: vec![item_type.clone()],
            owner_id: None,
            verify_by: None,
        },
    )
    .await?;
    // The row lock on the space serialises concurrent creators, so keys are unique and sequential.
    let (number, space_key): (i32, String) = sqlx::query_as(
        "update core.spaces set item_seq = item_seq + 1 where id = $1 returning item_seq, key",
    )
    .bind(space_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let max_rank: Option<String> = sqlx::query_scalar(
        "select max(rank collate \"C\") from projects.items where space_id = $1",
    )
    .bind(space_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let status = wf.def.initial().clone();
    let key = format!("{space_key}-{number}");
    let triage = i.triage.unwrap_or(ctx.principal.is_machine());
    sqlx::query(
        "insert into projects.items (note_id, workspace_id, space_id, number, key, item_type, status, status_category,
           priority, assignee_id, assignee_agent_id, reporter_id, sprint_id, fix_version_id, component_id, estimate,
           due_date, labels, custom, rank, workflow_version, triage)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::float8::numeric,$17,$18,$19,$20,$21,$22)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(space_id)
    .bind(number)
    .bind(&key)
    .bind(&item_type)
    .bind(&status.name)
    .bind(status.category.as_str())
    .bind(priority)
    .bind(i.assignee_id)
    .bind(i.assignee_agent_id)
    .bind(ctx.principal.user_id)
    .bind(i.sprint_id)
    .bind(i.fix_version_id)
    .bind(i.component_id)
    .bind(i.estimate)
    .bind(i.due_date)
    .bind(&i.labels)
    .bind(i.custom.unwrap_or_else(|| json!({})))
    .bind(rank_between(max_rank.as_deref(), None))
    .bind(wf.version)
    .bind(triage)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "item.created",
        Some(note.id),
        json!({"key": key, "item_type": item_type, "status": status.name, "space_id": space_id, "triage": triage}),
    )
    .await?;
    if let Some(a) = i.assignee_agent_id {
        ctx.emit(
            "item.assigned_to_agent",
            Some(note.id),
            json!({"key": key, "agent_id": a}),
        )
        .await?;
    }
    load_item(
        ctx,
        &ItemRef {
            id: Some(note.id),
            key: None,
        },
        false,
    )
    .await
}

// ---------- get ----------

#[derive(Serialize)]
pub struct ItemDetail {
    #[serde(flatten)]
    item: ItemOut,
    /// Transitions available from the current status.
    transitions: Vec<Value>,
    evidence: Vec<Value>,
}

async fn item_get(ctx: &mut Ctx, i: ItemRef) -> CmdResult<ItemDetail> {
    let item = load_item(ctx, &i, false).await?;
    let wf = space_wf(ctx, item.space_id).await?;
    let def = definition_for(ctx, item.space_id, &wf, item.workflow_version).await?;
    let transitions = def
        .allowed_from(&item.status)
        .iter()
        .map(|t| json!({"name": t.name, "to": t.to, "guards": t.guards}))
        .collect();
    let rows: Vec<(String, String, Option<String>, DateTime<Utc>)> = sqlx::query_as(
        "select kind, source, url, created_at from projects.evidence where note_id = $1 order by created_at",
    )
    .bind(item.id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let evidence = rows
        .into_iter()
        .map(
            |(kind, source, url, at)| json!({"kind": kind, "source": source, "url": url, "at": at}),
        )
        .collect();
    Ok(ItemDetail {
        item,
        transitions,
        evidence,
    })
}

// ---------- update ----------

#[derive(Deserialize, JsonSchema)]
pub struct UpdateItem {
    #[serde(flatten)]
    pub item: ItemRef,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(default)]
    pub priority: Option<String>,
    #[serde(default)]
    pub labels: Option<Vec<String>>,
    #[serde(default)]
    pub estimate: Option<f64>,
    #[serde(default)]
    pub due_date: Option<NaiveDate>,
    #[serde(default)]
    pub sprint_id: Option<Uuid>,
    #[serde(default)]
    pub fix_version_id: Option<Uuid>,
    #[serde(default)]
    pub component_id: Option<Uuid>,
    #[serde(default)]
    pub item_type: Option<String>,
    /// Custom field values, merged into the existing ones.
    #[serde(default)]
    pub custom: Option<Value>,
    /// Fields to clear: estimate, due_date, sprint, fix_version, component.
    #[serde(default)]
    pub clear: Vec<String>,
}

async fn item_update(ctx: &mut Ctx, i: UpdateItem) -> CmdResult<ItemOut> {
    let before = load_item(ctx, &i.item, true).await?;
    if let Some(p) = &i.priority {
        check_priority(p)?;
    }
    if let Some(c) = &i.custom
        && !c.is_object()
    {
        return Err(CmdError::invalid("custom must be an object"));
    }
    if let Some(bad) = i.clear.iter().find(|c| {
        !["estimate", "due_date", "sprint", "fix_version", "component"].contains(&c.as_str())
    }) {
        return Err(CmdError::invalid(format!("cannot clear '{bad}'")));
    }
    let item_type = match &i.item_type {
        Some(t) => {
            let t = t.trim().to_lowercase();
            let wf = space_wf(ctx, before.space_id).await?;
            if !wf.def.types.is_empty() && !wf.def.types.contains(&t) {
                return Err(CmdError::invalid(format!(
                    "item type '{t}' is not allowed here; use one of {}",
                    wf.def.types.join(", ")
                )));
            }
            Some(t)
        }
        None => None,
    };
    assert_in_space(ctx, "projects.sprints", i.sprint_id, before.space_id).await?;
    assert_in_space(ctx, "projects.versions", i.fix_version_id, before.space_id).await?;
    assert_in_space(ctx, "projects.components", i.component_id, before.space_id).await?;
    if i.title.is_some() || i.description.is_some() {
        ctx.call(
            "note.update",
            json!({"id": before.id, "title": i.title, "content_md": i.description}),
        )
        .await?;
    }
    sqlx::query(
        "update projects.items set
           priority = coalesce($2, priority),
           labels = coalesce($3, labels),
           estimate = case when 'estimate' = any($4) then null else coalesce($5::float8::numeric, estimate) end,
           due_date = case when 'due_date' = any($4) then null else coalesce($6, due_date) end,
           sprint_id = case when 'sprint' = any($4) then null else coalesce($7, sprint_id) end,
           fix_version_id = case when 'fix_version' = any($4) then null else coalesce($8, fix_version_id) end,
           component_id = case when 'component' = any($4) then null else coalesce($9, component_id) end,
           custom = custom || coalesce($10, '{}'::jsonb),
           item_type = coalesce($11, item_type)
         where note_id = $1",
    )
    .bind(before.id)
    .bind(&i.priority)
    .bind(&i.labels)
    .bind(&i.clear)
    .bind(i.estimate)
    .bind(i.due_date)
    .bind(i.sprint_id)
    .bind(i.fix_version_id)
    .bind(i.component_id)
    .bind(&i.custom)
    .bind(&item_type)
    .execute(&mut *ctx.tx)
    .await?;
    if let Some(t) = &item_type {
        sqlx::query("update core.notes set types = array[$2] where id = $1")
            .bind(before.id)
            .bind(t)
            .execute(&mut *ctx.tx)
            .await?;
    }
    let after = load_item(
        ctx,
        &ItemRef {
            id: Some(before.id),
            key: None,
        },
        false,
    )
    .await?;
    let mut changed = vec![];
    macro_rules! diff {
        ($($f:ident),*) => { $( if before.$f != after.$f { changed.push(stringify!($f)); } )* };
    }
    diff!(
        title,
        description,
        priority,
        labels,
        estimate,
        due_date,
        sprint_id,
        fix_version_id,
        component_id,
        item_type,
        custom
    );
    if !changed.is_empty() {
        ctx.emit(
            "item.updated",
            Some(after.id),
            json!({"key": after.key, "changed": changed}),
        )
        .await?;
    }
    Ok(after)
}

// ---------- transition ----------

#[derive(Deserialize, JsonSchema)]
pub struct TransitionItem {
    #[serde(flatten)]
    pub item: ItemRef,
    /// Target status name (e.g. "In Review") or transition name.
    pub to: String,
}

async fn set_field(ctx: &mut Ctx, id: Uuid, field: &str, v: &Value) -> CmdResult<()> {
    let bad = |m: &str| CmdError::invalid(format!("set_field {field}: {m}"));
    match field {
        "priority" => {
            let p = v.as_str().ok_or_else(|| bad("expected a string"))?;
            check_priority(p)?;
            sqlx::query("update projects.items set priority = $2 where note_id = $1").bind(id).bind(p)
        }
        "estimate" => sqlx::query("update projects.items set estimate = $2::float8::numeric where note_id = $1")
            .bind(id)
            .bind(if v.is_null() { None } else { Some(v.as_f64().ok_or_else(|| bad("expected a number"))?) }),
        "due_date" => {
            let d = match v.as_str() {
                Some(s) => Some(s.parse::<NaiveDate>().map_err(|_| bad("expected YYYY-MM-DD"))?),
                None if v.is_null() => None,
                None => return Err(bad("expected YYYY-MM-DD")),
            };
            sqlx::query("update projects.items set due_date = $2 where note_id = $1").bind(id).bind(d)
        }
        "labels" => {
            let l: Vec<String> = serde_json::from_value(v.clone()).map_err(|_| bad("expected an array of strings"))?;
            sqlx::query("update projects.items set labels = $2 where note_id = $1").bind(id).bind(l)
        }
        _ => sqlx::query("update projects.items set custom = custom || jsonb_build_object($2::text, $3::jsonb) where note_id = $1")
            .bind(id)
            .bind(field)
            .bind(v),
    }
    .execute(&mut *ctx.tx)
    .await?;
    Ok(())
}

/// Apply the workflow transition from the item's current status to `target` (status or transition name).
pub async fn transition_item(ctx: &mut Ctx, item: ItemOut, target: &str) -> CmdResult<ItemOut> {
    let wf = space_wf(ctx, item.space_id).await?;
    let def = definition_for(ctx, item.space_id, &wf, item.workflow_version).await?;
    let evidence: Vec<String> =
        sqlx::query_scalar("select distinct kind from projects.evidence where note_id = $1")
            .bind(item.id)
            .fetch_all(&mut *ctx.tx)
            .await?;
    let filled = filled_fields(&item);
    let t = def
        .pick(
            &item.status,
            target,
            &GuardInput {
                role: ctx.principal.role,
                filled: &filled,
                evidence: &evidence,
            },
        )
        .map_err(CmdError::invalid)?
        .clone();
    let to = def.status(&t.to).expect("validated definition").clone();
    sqlx::query(
        "update projects.items set status = $2, status_category = $3,
           resolved_at = case when $3 = 'done' then coalesce(resolved_at, now()) else null end
         where note_id = $1",
    )
    .bind(item.id)
    .bind(&to.name)
    .bind(to.category.as_str())
    .execute(&mut *ctx.tx)
    .await?;
    sqlx::query("update core.notes set updated_at = now(), version = version + 1 where id = $1")
        .bind(item.id)
        .execute(&mut *ctx.tx)
        .await?;
    for a in &t.post_actions {
        match a {
            PostAction::SetField { field, value } => set_field(ctx, item.id, field, value).await?,
            PostAction::AssignReporter => {
                sqlx::query("update projects.items set assignee_id = reporter_id, assignee_agent_id = null where note_id = $1 and reporter_id is not null")
                    .bind(item.id)
                    .execute(&mut *ctx.tx)
                    .await?;
            }
            PostAction::Emit { event } => {
                ctx.emit(
                    event,
                    Some(item.id),
                    json!({"key": item.key, "from": item.status, "to": to.name}),
                )
                .await?;
            }
        }
    }
    ctx.emit(
        "item.transitioned",
        Some(item.id),
        json!({
            "key": item.key, "from": item.status, "to": to.name, "from_category": item.status_category,
            "category": to.category.as_str(), "transition": t.name, "workflow_version": item.workflow_version,
            "space_id": item.space_id, "sprint_id": item.sprint_id, "estimate": item.estimate,
        }),
    )
    .await?;
    load_item(
        ctx,
        &ItemRef {
            id: Some(item.id),
            key: None,
        },
        false,
    )
    .await
}

async fn item_transition(ctx: &mut Ctx, i: TransitionItem) -> CmdResult<ItemOut> {
    let item = load_item(ctx, &i.item, true).await?;
    transition_item(ctx, item, &i.to).await
}

// ---------- assign ----------

#[derive(Deserialize, JsonSchema)]
pub struct AssignItem {
    #[serde(flatten)]
    pub item: ItemRef,
    /// Person to assign. Omit both user_id and agent_id to unassign.
    #[serde(default)]
    pub user_id: Option<Uuid>,
    /// Agent principal to assign (agents are first-class assignees).
    #[serde(default)]
    pub agent_id: Option<Uuid>,
}

async fn item_assign(ctx: &mut Ctx, i: AssignItem) -> CmdResult<ItemOut> {
    let before = load_item(ctx, &i.item, true).await?;
    validate_assignee(ctx, i.user_id, i.agent_id).await?;
    sqlx::query(
        "update projects.items set assignee_id = $2, assignee_agent_id = $3 where note_id = $1",
    )
    .bind(before.id)
    .bind(i.user_id)
    .bind(i.agent_id)
    .execute(&mut *ctx.tx)
    .await?;
    sqlx::query("update core.notes set updated_at = now(), version = version + 1 where id = $1")
        .bind(before.id)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "item.assigned",
        Some(before.id),
        json!({
            "key": before.key, "assignee_id": i.user_id, "assignee_agent_id": i.agent_id,
            "previous_assignee_id": before.assignee_id, "previous_assignee_agent_id": before.assignee_agent_id,
        }),
    )
    .await?;
    if let Some(a) = i.agent_id {
        ctx.emit(
            "item.assigned_to_agent",
            Some(before.id),
            json!({"key": before.key, "agent_id": a, "title": before.title}),
        )
        .await?;
    }
    load_item(
        ctx,
        &ItemRef {
            id: Some(before.id),
            key: None,
        },
        false,
    )
    .await
}

// ---------- list ----------

#[derive(Deserialize, JsonSchema)]
pub struct ListItems {
    /// CNQL query, e.g. `project = ENG AND statusCategory != Done AND assignee = currentUser() ORDER BY priority DESC`.
    #[serde(default)]
    pub cnql: Option<String>,
    /// Default 50, max 200.
    #[serde(default)]
    pub limit: Option<i64>,
}

fn cnql_ctx(ctx: &Ctx) -> cnql::Cx {
    cnql::Cx {
        now: Utc::now(),
        user: ctx.principal.user_id,
    }
}

async fn item_list(ctx: &mut Ctx, i: ListItems) -> CmdResult<Vec<ItemOut>> {
    list_with(ctx, i.cnql.as_deref().unwrap_or(""), "true", i.limit).await
}

/// Run a CNQL query over the items the caller may read; `extra` is a static SQL predicate.
async fn list_with(
    ctx: &mut Ctx,
    query: &str,
    extra: &'static str,
    limit: Option<i64>,
) -> CmdResult<Vec<ItemOut>> {
    let q = cnql::parse(query).map_err(|e| CmdError::invalid(e.to_string()))?;
    // $1 spaces, $2 limit, $3 user, $4 admin; CNQL binds follow.
    let c = cnql::compile(&q, &cnql_ctx(ctx), 5).map_err(|e| CmdError::invalid(e.to_string()))?;
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    let sql = format!(
        "{ITEM_SELECT} where n.deleted_at is null and i.space_id = any($1) and core.note_access(n.id, $3, $4, $1) <> 'none'
           and {extra} and ({}) order by {} limit $2",
        c.where_sql, c.order_sql
    );
    let mut qb = sqlx::query_as::<_, ItemOut>(sqlx::AssertSqlSafe(sql))
        .bind(spaces)
        .bind(limit.unwrap_or(50).clamp(1, 200))
        .bind(uid)
        .bind(admin);
    for b in c.binds {
        qb = b.apply(qb);
    }
    Ok(qb.fetch_all(&mut *ctx.tx).await?)
}

/// Does the item match a CNQL query? Used by automation triggers and conditions.
pub async fn item_matches(ctx: &mut Ctx, note_id: Uuid, query: &str) -> CmdResult<bool> {
    let q = cnql::parse(query).map_err(|e| CmdError::invalid(e.to_string()))?;
    let c = cnql::compile(&q, &cnql_ctx(ctx), 2).map_err(|e| CmdError::invalid(e.to_string()))?;
    let sql = format!(
        "select exists(select 1 from {} where i.note_id = $1 and ({}))",
        cnql::FROM_SQL,
        c.where_sql
    );
    let mut qb = sqlx::query_as::<_, (bool,)>(sqlx::AssertSqlSafe(sql)).bind(note_id);
    for b in c.binds {
        qb = b.apply(qb);
    }
    Ok(qb.fetch_one(&mut *ctx.tx).await?.0)
}

/// Items matching a CNQL query inside one space (schedule triggers).
pub async fn matching_item_ids(
    ctx: &mut Ctx,
    space: Option<Uuid>,
    query: &str,
    limit: i64,
) -> CmdResult<Vec<Uuid>> {
    let q = cnql::parse(query).map_err(|e| CmdError::invalid(e.to_string()))?;
    let c = cnql::compile(&q, &cnql_ctx(ctx), 3).map_err(|e| CmdError::invalid(e.to_string()))?;
    let sql = format!(
        "select i.note_id from {} where n.deleted_at is null and ($1::uuid is null or i.space_id = $1) and ({}) order by {} limit $2",
        cnql::FROM_SQL,
        c.where_sql,
        c.order_sql
    );
    let mut qb = sqlx::query_as::<_, (Uuid,)>(sqlx::AssertSqlSafe(sql))
        .bind(space)
        .bind(limit);
    for b in c.binds {
        qb = b.apply(qb);
    }
    Ok(qb
        .fetch_all(&mut *ctx.tx)
        .await?
        .into_iter()
        .map(|r| r.0)
        .collect())
}

// ---------- rank ----------

#[derive(Deserialize, JsonSchema)]
pub struct RankItem {
    #[serde(flatten)]
    pub item: ItemRef,
    /// Place the item directly after this item (id or key).
    #[serde(default)]
    pub after: Option<String>,
    /// Place the item directly before this item (id or key).
    #[serde(default)]
    pub before: Option<String>,
}

async fn neighbour_rank(ctx: &mut Ctx, space: Uuid, r: &str) -> CmdResult<String> {
    sqlx::query_scalar("select rank from projects.items where space_id = $1 and (note_id::text = $2 or key = upper($2))")
        .bind(space)
        .bind(r)
        .fetch_optional(&mut *ctx.tx)
        .await?
        .ok_or_else(|| CmdError::not_found(&format!("item {r} in this space")))
}

async fn item_rank(ctx: &mut Ctx, i: RankItem) -> CmdResult<ItemOut> {
    let item = load_item(ctx, &i.item, true).await?;
    let (lo, hi) = match (&i.after, &i.before) {
        (None, None) => return Err(CmdError::invalid("give after and/or before")),
        (Some(a), Some(b)) => (
            Some(neighbour_rank(ctx, item.space_id, a).await?),
            Some(neighbour_rank(ctx, item.space_id, b).await?),
        ),
        (Some(a), None) => {
            let lo = neighbour_rank(ctx, item.space_id, a).await?;
            let hi: Option<String> = sqlx::query_scalar(
                "select min(rank collate \"C\") from projects.items where space_id = $1 and note_id <> $3 and rank collate \"C\" > $2 collate \"C\"",
            )
            .bind(item.space_id)
            .bind(&lo)
            .bind(item.id)
            .fetch_one(&mut *ctx.tx)
            .await?;
            (Some(lo), hi)
        }
        (None, Some(b)) => {
            let hi = neighbour_rank(ctx, item.space_id, b).await?;
            let lo: Option<String> = sqlx::query_scalar(
                "select max(rank collate \"C\") from projects.items where space_id = $1 and note_id <> $3 and rank collate \"C\" < $2 collate \"C\"",
            )
            .bind(item.space_id)
            .bind(&hi)
            .bind(item.id)
            .fetch_one(&mut *ctx.tx)
            .await?;
            (lo, Some(hi))
        }
    };
    if let (Some(l), Some(h)) = (&lo, &hi)
        && l > h
    {
        return Err(CmdError::invalid("'after' ranks below 'before'"));
    }
    let rank = rank_between(lo.as_deref(), hi.as_deref());
    sqlx::query("update projects.items set rank = $2 where note_id = $1")
        .bind(item.id)
        .bind(&rank)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "item.ranked",
        Some(item.id),
        json!({"key": item.key, "rank": rank}),
    )
    .await?;
    load_item(
        ctx,
        &ItemRef {
            id: Some(item.id),
            key: None,
        },
        false,
    )
    .await
}

// ---------- triage ----------

#[derive(Deserialize, JsonSchema)]
pub struct TriageList {
    #[serde(default)]
    pub limit: Option<i64>,
}

async fn triage_list(ctx: &mut Ctx, i: TriageList) -> CmdResult<Vec<ItemOut>> {
    list_with(ctx, "ORDER BY created", "i.triage", i.limit).await
}

#[derive(Deserialize, JsonSchema)]
pub struct TriageAccept {
    #[serde(flatten)]
    pub item: ItemRef,
    #[serde(default)]
    pub priority: Option<String>,
    #[serde(default)]
    pub assignee_id: Option<Uuid>,
    #[serde(default)]
    pub assignee_agent_id: Option<Uuid>,
}

async fn triage_accept(ctx: &mut Ctx, i: TriageAccept) -> CmdResult<ItemOut> {
    let item = load_item(ctx, &i.item, true).await?;
    if !item.triage {
        return Err(CmdError::conflict("item is not in triage"));
    }
    if let Some(p) = &i.priority {
        check_priority(p)?;
    }
    sqlx::query("update projects.items set triage = false, priority = coalesce($2, priority) where note_id = $1")
        .bind(item.id)
        .bind(&i.priority)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "item.triage_accepted",
        Some(item.id),
        json!({"key": item.key}),
    )
    .await?;
    if i.assignee_id.is_some() || i.assignee_agent_id.is_some() {
        return item_assign(
            ctx,
            AssignItem {
                item: ItemRef {
                    id: Some(item.id),
                    key: None,
                },
                user_id: i.assignee_id,
                agent_id: i.assignee_agent_id,
            },
        )
        .await;
    }
    load_item(
        ctx,
        &ItemRef {
            id: Some(item.id),
            key: None,
        },
        false,
    )
    .await
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new("item.create", "Create an item (task, bug, story, epic...) with the next key (ENG-n) in a Space. Starts in the workflow's first status.", ToolGroup::WriteItems, handler!(item_create)).core(),
        Command::new("item.get", "Read an item by id or key, with its available transitions and recorded evidence.", ToolGroup::Read, handler!(item_get)),
        Command::new("item.update", "Update an item's title, description, priority, estimate, labels, dates, sprint, version, component or custom fields.", ToolGroup::WriteItems, handler!(item_update)),
        Command::new("item.transition", "Move an item to another status. Workflow guards apply (required fields, role, evidence).", ToolGroup::WriteItems, handler!(item_transition)).core(),
        Command::new("item.assign", "Assign an item to a person or an agent (omit both to unassign).", ToolGroup::WriteItems, handler!(item_assign)),
        Command::new("item.list", "List items with a CNQL query (JQL-compatible), limited to Spaces you can read.", ToolGroup::Read, handler!(item_list)).core(),
        Command::new("item.rank", "Reorder an item in the backlog: place it after and/or before another item.", ToolGroup::WriteItems, handler!(item_rank)),
        Command::new("triage.list", "Items waiting in the triage inbox (created by agents or integrations).", ToolGroup::Read, handler!(triage_list)),
        Command::new("triage.accept", "Accept a triage item into the backlog, optionally setting priority and assignee.", ToolGroup::WriteItems, handler!(triage_accept)),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rank_between_is_strictly_between() {
        let mut ranks: Vec<String> = vec![];
        // append many, insert in the middle many, prepend many
        let mut last: Option<String> = None;
        for _ in 0..40 {
            let r = rank_between(last.as_deref(), None);
            assert!(
                last.as_deref().is_none_or(|l| l < r.as_str()),
                "{last:?} < {r}"
            );
            ranks.push(r.clone());
            last = Some(r);
        }
        let (mut lo, hi) = (ranks[3].clone(), ranks[4].clone());
        for _ in 0..40 {
            let r = rank_between(Some(&lo), Some(&hi));
            assert!(lo < r && r < hi, "{lo} < {r} < {hi}");
            lo = r;
        }
        let mut hi = ranks[0].clone();
        for _ in 0..40 {
            let r = rank_between(None, Some(&hi));
            assert!(r < hi && !r.is_empty(), "{r} < {hi}");
            hi = r;
        }
    }

    #[test]
    fn rank_between_equal_neighbours_does_not_panic() {
        assert_eq!(rank_between(Some("m"), Some("m")), "mn");
        assert_eq!(rank_between(None, None), "n");
        assert!(rank_between(Some("a"), Some("b")).starts_with('a'));
    }

    #[test]
    fn filled_fields_reads_the_item() {
        let i = ItemOut {
            id: Uuid::nil(),
            space_id: Uuid::nil(),
            key: "A-1".into(),
            title: "t".into(),
            description: " ".into(),
            item_type: "task".into(),
            status: "Todo".into(),
            status_category: "todo".into(),
            priority: "none".into(),
            assignee_id: None,
            assignee_agent_id: Some(Uuid::nil()),
            reporter_id: None,
            parent_id: None,
            sprint_id: None,
            fix_version_id: None,
            component_id: None,
            estimate: Some(3.0),
            due_date: None,
            labels: vec![],
            custom: json!({"team": "core", "empty": null}),
            rank: "m".into(),
            workflow_version: 1,
            triage: false,
            resolved_at: None,
            created_at: Utc::now(),
            updated_at: Utc::now(),
        };
        let f = filled_fields(&i);
        assert!(f.contains("assignee") && f.contains("estimate") && f.contains("team"));
        assert!(
            !f.contains("priority")
                && !f.contains("description")
                && !f.contains("empty")
                && !f.contains("labels")
        );
    }
}
