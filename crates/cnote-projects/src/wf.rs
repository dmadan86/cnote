//! Per-Space workflow configuration (ADR-007/ADR-014). No schemes: a Space copies its template's workflow into
//! `spaces.config` and may edit it. Every change bumps a version and snapshots it in `projects.workflows`;
//! items keep their `workflow_version` until migrated.

use crate::workflow::Definition;
use cnote_core::access::assert_space_access;
use cnote_core::{CmdError, CmdResult, Command, Ctx, Role, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

pub struct SpaceWf {
    pub template: String,
    pub config: Value,
    pub def: Definition,
    pub version: i32,
}

pub async fn space_wf(ctx: &mut Ctx, space_id: Uuid) -> CmdResult<SpaceWf> {
    let (template, config): (String, Value) =
        sqlx::query_as("select template, config from core.spaces where id = $1")
            .bind(space_id)
            .fetch_optional(&mut *ctx.tx)
            .await?
            .ok_or_else(|| CmdError::not_found("space"))?;
    let def = config
        .get("workflow")
        .and_then(|w| serde_json::from_value(w.clone()).ok())
        .unwrap_or_else(|| Definition::template(&template));
    let version = config
        .get("workflow_version")
        .and_then(Value::as_i64)
        .unwrap_or(1) as i32;
    Ok(SpaceWf {
        template,
        config,
        def,
        version,
    })
}

/// The definition an item at `version` follows (the current one unless the item lags behind).
pub async fn definition_for(
    ctx: &mut Ctx,
    space_id: Uuid,
    wf: &SpaceWf,
    version: i32,
) -> CmdResult<Definition> {
    if version == wf.version {
        return Ok(wf.def.clone());
    }
    let d: Option<Value> = sqlx::query_scalar(
        "select definition from projects.workflows where space_id = $1 and type_name = 'item' and version = $2",
    )
    .bind(space_id)
    .bind(version)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    Ok(d.and_then(|d| serde_json::from_value(d).ok())
        .unwrap_or_else(|| wf.def.clone()))
}

async fn snapshot(ctx: &mut Ctx, space_id: Uuid, version: i32, def: &Definition) -> CmdResult<()> {
    sqlx::query(
        "insert into projects.workflows (workspace_id, space_id, type_name, version, definition)
         values ($1,$2,'item',$3,$4) on conflict do nothing",
    )
    .bind(ctx.workspace_id())
    .bind(space_id)
    .bind(version)
    .bind(serde_json::to_value(def)?)
    .execute(&mut *ctx.tx)
    .await?;
    Ok(())
}

/// Publish `def` as the Space's next workflow version. Returns (version, items migrated).
async fn set_workflow(
    ctx: &mut Ctx,
    space_id: Uuid,
    def: Definition,
    migrate_items: bool,
) -> CmdResult<(i32, u64)> {
    def.validate().map_err(CmdError::invalid)?;
    let cur = space_wf(ctx, space_id).await?;
    snapshot(ctx, space_id, cur.version, &cur.def).await?; // keep the version items may still be on
    let version = cur.version + 1;
    snapshot(ctx, space_id, version, &def).await?;
    sqlx::query("update core.spaces set config = config || $2 where id = $1")
        .bind(space_id)
        .bind(json!({"workflow": def, "workflow_version": version}))
        .execute(&mut *ctx.tx)
        .await?;
    let migrated = if migrate_items {
        // Only items whose status still exists move; the rest stay on their version.
        sqlx::query(
            "update projects.items i set workflow_version = $2, status = s.name, status_category = s.category
             from jsonb_to_recordset($3::jsonb) as s(name text, category text)
             where i.space_id = $1 and lower(i.status) = lower(s.name)",
        )
        .bind(space_id)
        .bind(version)
        .bind(json!(def.statuses))
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected()
    } else {
        0
    };
    ctx.emit(
        "workflow.updated",
        None,
        json!({"space_id": space_id, "version": version, "migrated_items": migrated}),
    )
    .await?;
    Ok((version, migrated))
}

#[derive(Deserialize, JsonSchema)]
pub struct SpaceRef {
    pub space_id: Uuid,
}

#[derive(Serialize)]
pub struct WorkflowOut {
    space_id: Uuid,
    template: String,
    version: i32,
    definition: Definition,
}

async fn workflow_get(ctx: &mut Ctx, i: SpaceRef) -> CmdResult<WorkflowOut> {
    assert_space_access(ctx, i.space_id, false).await?;
    let wf = space_wf(ctx, i.space_id).await?;
    Ok(WorkflowOut {
        space_id: i.space_id,
        template: wf.template,
        version: wf.version,
        definition: wf.def,
    })
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateWorkflow {
    pub space_id: Uuid,
    /// {statuses:[{name,category}], transitions:[{name,from,to,guards,post_actions}], types?}
    pub definition: Definition,
    /// Also move existing items to the new version (items whose status no longer exists stay behind).
    #[serde(default)]
    pub migrate_items: bool,
}

async fn workflow_update(ctx: &mut Ctx, i: UpdateWorkflow) -> CmdResult<Value> {
    assert_space_access(ctx, i.space_id, true).await?;
    let (version, migrated) = set_workflow(ctx, i.space_id, i.definition, i.migrate_items).await?;
    Ok(json!({"version": version, "migrated_items": migrated}))
}

async fn space_drift(ctx: &mut Ctx, i: SpaceRef) -> CmdResult<Value> {
    assert_space_access(ctx, i.space_id, false).await?;
    let wf = space_wf(ctx, i.space_id).await?;
    let differences = wf.def.diff(&Definition::template(&wf.template));
    Ok(json!({
        "template": wf.template,
        "version": wf.version,
        "in_sync": differences.is_empty(),
        "differences": differences,
    }))
}

#[derive(Deserialize, JsonSchema)]
pub struct Realign {
    pub space_id: Uuid,
    #[serde(default)]
    pub migrate_items: bool,
}

async fn space_realign(ctx: &mut Ctx, i: Realign) -> CmdResult<Value> {
    assert_space_access(ctx, i.space_id, true).await?;
    let wf = space_wf(ctx, i.space_id).await?;
    let template = Definition::template(&wf.template);
    if wf.def == template {
        return Ok(json!({"changed": false, "version": wf.version}));
    }
    let (version, migrated) = set_workflow(ctx, i.space_id, template, i.migrate_items).await?;
    Ok(json!({"changed": true, "version": version, "migrated_items": migrated}))
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new("workflow.get", "Read a Space's item workflow (statuses, transitions, guards) and its version.", ToolGroup::Read, handler!(workflow_get)),
        Command::new("workflow.update", "Replace a Space's item workflow; bumps the version. Items keep their version until migrated.", ToolGroup::Admin, handler!(workflow_update)).min_role(Role::Admin),
        Command::new("space.drift", "Report how a Space's workflow differs from its template.", ToolGroup::Read, handler!(space_drift)),
        Command::new("space.realign", "Reset a Space's workflow to its template (new version).", ToolGroup::Admin, handler!(space_realign)).min_role(Role::Admin),
    ]
}
