//! Sprints, versions, components and reports (burndown, velocity are computed from `core.events`).

use crate::workflow::Category;
use chrono::{DateTime, Duration, NaiveDate, Utc};
use cnote_core::access::{assert_space_access, readable_space_ids};
use cnote_core::{CmdError, CmdResult, Command, Ctx, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::HashMap;
use uuid::Uuid;

// ---------- sprints ----------

#[derive(Serialize, sqlx::FromRow)]
pub struct SprintOut {
    id: Uuid,
    space_id: Uuid,
    name: String,
    goal: Option<String>,
    state: String,
    start_date: Option<NaiveDate>,
    end_date: Option<NaiveDate>,
}

const SPRINT_COLS: &str = "id, space_id, name, goal, state, start_date, end_date";

async fn load_sprint(ctx: &mut Ctx, id: Uuid, write: bool) -> CmdResult<SprintOut> {
    let s: SprintOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {SPRINT_COLS} from projects.sprints where id = $1"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("sprint"))?;
    assert_space_access(ctx, s.space_id, write).await?;
    Ok(s)
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateSprint {
    pub space_id: Uuid,
    pub name: String,
    #[serde(default)]
    pub goal: Option<String>,
    #[serde(default)]
    pub start_date: Option<NaiveDate>,
    #[serde(default)]
    pub end_date: Option<NaiveDate>,
}

fn check_dates(s: Option<NaiveDate>, e: Option<NaiveDate>) -> CmdResult<()> {
    match (s, e) {
        (Some(s), Some(e)) if e < s => Err(CmdError::invalid("end_date is before start_date")),
        _ => Ok(()),
    }
}

async fn sprint_create(ctx: &mut Ctx, i: CreateSprint) -> CmdResult<SprintOut> {
    assert_space_access(ctx, i.space_id, true).await?;
    check_dates(i.start_date, i.end_date)?;
    if i.name.trim().is_empty() {
        return Err(CmdError::invalid("name is required"));
    }
    let s: SprintOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "insert into projects.sprints (workspace_id, space_id, name, goal, start_date, end_date)
         values ($1,$2,$3,$4,$5,$6) returning {SPRINT_COLS}"
    )))
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.name.trim())
    .bind(&i.goal)
    .bind(i.start_date)
    .bind(i.end_date)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "sprint.created",
        None,
        json!({"sprint_id": s.id, "space_id": s.space_id, "name": s.name}),
    )
    .await?;
    Ok(s)
}

#[derive(Deserialize, JsonSchema)]
pub struct StartSprint {
    pub id: Uuid,
    /// Defaults to today.
    #[serde(default)]
    pub start_date: Option<NaiveDate>,
    /// Defaults to the planned end date, or two weeks after the start.
    #[serde(default)]
    pub end_date: Option<NaiveDate>,
}

async fn sprint_start(ctx: &mut Ctx, i: StartSprint) -> CmdResult<SprintOut> {
    let s = load_sprint(ctx, i.id, true).await?;
    if s.state != "future" {
        return Err(CmdError::conflict(format!(
            "sprint is {}, only a future sprint can start",
            s.state
        )));
    }
    let active: bool = sqlx::query_scalar(
        "select exists(select 1 from projects.sprints where space_id = $1 and state = 'active')",
    )
    .bind(s.space_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if active {
        return Err(CmdError::conflict(
            "another sprint is already active in this space; complete it first",
        ));
    }
    let start = i
        .start_date
        .or(s.start_date)
        .unwrap_or_else(|| Utc::now().date_naive());
    let end = i
        .end_date
        .or(s.end_date)
        .unwrap_or(start + Duration::days(14));
    check_dates(Some(start), Some(end))?;
    let out: SprintOut = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "update projects.sprints set state = 'active', start_date = $2, end_date = $3 where id = $1 returning {SPRINT_COLS}"
    )))
    .bind(s.id)
    .bind(start)
    .bind(end)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let (count, points): (i64, f64) = sqlx::query_as(
        "select count(*), coalesce(sum(coalesce(estimate, 1)), 0)::float8 from projects.items where sprint_id = $1",
    )
    .bind(s.id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "sprint.started",
        None,
        json!({"sprint_id": s.id, "space_id": s.space_id, "name": s.name, "committed_count": count, "committed_points": points}),
    )
    .await?;
    Ok(out)
}

#[derive(Deserialize, JsonSchema)]
pub struct CompleteSprint {
    pub id: Uuid,
    /// Where unfinished items go. Default: the next future sprint of the space, or the backlog if there is none.
    #[serde(default)]
    pub move_to_sprint_id: Option<Uuid>,
}

async fn sprint_complete(ctx: &mut Ctx, i: CompleteSprint) -> CmdResult<Value> {
    let s = load_sprint(ctx, i.id, true).await?;
    if s.state != "active" {
        return Err(CmdError::conflict(format!(
            "sprint is {}, only an active sprint can be completed",
            s.state
        )));
    }
    let dest: Option<Uuid> = match i.move_to_sprint_id {
        Some(d) => {
            let t = load_sprint(ctx, d, true).await?;
            if t.space_id != s.space_id || t.state != "future" || t.id == s.id {
                return Err(CmdError::invalid(
                    "move_to_sprint_id must be a future sprint of the same space",
                ));
            }
            Some(d)
        }
        None => {
            sqlx::query_scalar(
                "select id from projects.sprints where space_id = $1 and state = 'future'
                 order by start_date nulls last, created_at limit 1",
            )
            .bind(s.space_id)
            .fetch_optional(&mut *ctx.tx)
            .await?
        }
    };
    let (done_count, done_points): (i64, f64) = sqlx::query_as(
        "select count(*), coalesce(sum(coalesce(estimate, 1)), 0)::float8 from projects.items where sprint_id = $1 and status_category = 'done'",
    )
    .bind(s.id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let moved: Vec<(Uuid, f64)> = sqlx::query_as(
        "update projects.items set sprint_id = $2 where sprint_id = $1 and status_category <> 'done'
         returning note_id, coalesce(estimate, 1)::float8",
    )
    .bind(s.id)
    .bind(dest)
    .fetch_all(&mut *ctx.tx)
    .await?;
    sqlx::query("update projects.sprints set state = 'closed', end_date = coalesce(end_date, current_date) where id = $1")
        .bind(s.id)
        .execute(&mut *ctx.tx)
        .await?;
    let moved_points: f64 = moved.iter().map(|m| m.1).sum();
    let moved_ids: Vec<Uuid> = moved.iter().map(|m| m.0).collect();
    let payload = json!({
        "sprint_id": s.id, "space_id": s.space_id, "name": s.name,
        "completed_count": done_count, "completed_points": done_points,
        "moved_count": moved_ids.len(), "moved_points": moved_points,
        "moved_to": dest, "moved_item_ids": moved_ids,
    });
    ctx.emit("sprint.completed", None, payload.clone()).await?;
    Ok(payload)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListSprints {
    #[serde(default)]
    pub space_id: Option<Uuid>,
    /// future | active | closed
    #[serde(default)]
    pub state: Option<String>,
}

async fn sprint_list(ctx: &mut Ctx, i: ListSprints) -> CmdResult<Vec<SprintOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {SPRINT_COLS} from projects.sprints where space_id = any($1)
           and ($2::uuid is null or space_id = $2) and ($3::text is null or state = $3)
         order by start_date nulls last, created_at"
    )))
    .bind(spaces)
    .bind(i.space_id)
    .bind(i.state)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- versions & components ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateVersion {
    pub space_id: Uuid,
    pub name: String,
    #[serde(default)]
    pub release_date: Option<NaiveDate>,
}

#[derive(Serialize, sqlx::FromRow)]
pub struct VersionOut {
    id: Uuid,
    space_id: Uuid,
    name: String,
    released: bool,
    release_date: Option<NaiveDate>,
}

#[derive(Deserialize, JsonSchema)]
pub struct BySpace {
    pub space_id: Uuid,
}

async fn version_list(ctx: &mut Ctx, i: BySpace) -> CmdResult<Vec<VersionOut>> {
    assert_space_access(ctx, i.space_id, false).await?;
    Ok(sqlx::query_as(
        "select id, space_id, name, released, release_date from projects.versions where space_id = $1
         order by released, release_date nulls last, name",
    )
    .bind(i.space_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

async fn component_list(ctx: &mut Ctx, i: BySpace) -> CmdResult<Vec<ComponentOut>> {
    assert_space_access(ctx, i.space_id, false).await?;
    Ok(sqlx::query_as("select id, space_id, name, lead_id from projects.components where space_id = $1 order by name")
        .bind(i.space_id)
        .fetch_all(&mut *ctx.tx)
        .await?)
}

async fn version_create(ctx: &mut Ctx, i: CreateVersion) -> CmdResult<VersionOut> {
    assert_space_access(ctx, i.space_id, true).await?;
    let v: VersionOut = sqlx::query_as(
        "insert into projects.versions (workspace_id, space_id, name, release_date) values ($1,$2,$3,$4)
         returning id, space_id, name, released, release_date",
    )
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.name.trim())
    .bind(i.release_date)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "version.created",
        None,
        json!({"version_id": v.id, "space_id": v.space_id, "name": v.name}),
    )
    .await?;
    Ok(v)
}

#[derive(Deserialize, JsonSchema)]
pub struct ReleaseVersion {
    pub id: Uuid,
    /// Defaults to today.
    #[serde(default)]
    pub release_date: Option<NaiveDate>,
}

async fn version_release(ctx: &mut Ctx, i: ReleaseVersion) -> CmdResult<VersionOut> {
    let space: Uuid = sqlx::query_scalar("select space_id from projects.versions where id = $1")
        .bind(i.id)
        .fetch_optional(&mut *ctx.tx)
        .await?
        .ok_or_else(|| CmdError::not_found("version"))?;
    assert_space_access(ctx, space, true).await?;
    let v: Option<VersionOut> = sqlx::query_as(
        "update projects.versions set released = true, release_date = coalesce($2, current_date)
         where id = $1 and not released returning id, space_id, name, released, release_date",
    )
    .bind(i.id)
    .bind(i.release_date)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let v = v.ok_or_else(|| CmdError::conflict("version is already released"))?;
    let items: Vec<String> = sqlx::query_scalar(
        "select key from projects.items where fix_version_id = $1 order by number",
    )
    .bind(v.id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "version.released",
        None,
        json!({"version_id": v.id, "space_id": v.space_id, "name": v.name, "items": items}),
    )
    .await?;
    Ok(v)
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateComponent {
    pub space_id: Uuid,
    pub name: String,
    #[serde(default)]
    pub lead_id: Option<Uuid>,
}

#[derive(Serialize, sqlx::FromRow)]
pub struct ComponentOut {
    id: Uuid,
    space_id: Uuid,
    name: String,
    lead_id: Option<Uuid>,
}

async fn component_create(ctx: &mut Ctx, i: CreateComponent) -> CmdResult<ComponentOut> {
    assert_space_access(ctx, i.space_id, true).await?;
    let c: ComponentOut = sqlx::query_as(
        "insert into projects.components (workspace_id, space_id, name, lead_id) values ($1,$2,$3,$4)
         returning id, space_id, name, lead_id",
    )
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.name.trim())
    .bind(i.lead_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "component.created",
        None,
        json!({"component_id": c.id, "space_id": c.space_id, "name": c.name}),
    )
    .await?;
    Ok(c)
}

// ---------- reports ----------

#[derive(Serialize, Debug, PartialEq)]
pub struct BurnPoint {
    pub date: NaiveDate,
    pub remaining: f64,
    pub ideal: f64,
}

/// Remaining work at the end of each day. An item counts as burned from the day its latest transition (by
/// that day) landed in a done category. `events` are (item, time, category) in any order.
pub fn burndown(
    start: NaiveDate,
    end: NaiveDate,
    items: &[(Uuid, f64)],
    events: &[(Uuid, DateTime<Utc>, Category)],
) -> Vec<BurnPoint> {
    let total: f64 = items.iter().map(|i| i.1).sum();
    let days = (end - start).num_days().max(0);
    let mut by_item: HashMap<Uuid, Vec<&(Uuid, DateTime<Utc>, Category)>> = HashMap::new();
    for e in events {
        by_item.entry(e.0).or_default().push(e);
    }
    (0..=days)
        .map(|d| {
            let date = start + Duration::days(d);
            let burned: f64 = items
                .iter()
                .filter(|(id, _)| {
                    by_item
                        .get(id)
                        .and_then(|ev| {
                            ev.iter()
                                .filter(|e| e.1.date_naive() <= date)
                                .max_by_key(|e| e.1)
                        })
                        .is_some_and(|e| e.2 == Category::Done)
                })
                .map(|i| i.1)
                .sum();
            let ideal = if days == 0 {
                0.0
            } else {
                total * (1.0 - d as f64 / days as f64)
            };
            BurnPoint {
                date,
                remaining: total - burned,
                ideal,
            }
        })
        .collect()
}

#[derive(Deserialize, JsonSchema)]
pub struct BurndownIn {
    pub sprint_id: Uuid,
}

async fn report_burndown(ctx: &mut Ctx, i: BurndownIn) -> CmdResult<Value> {
    let s = load_sprint(ctx, i.sprint_id, false).await?;
    let Some(start) = s.start_date else {
        return Err(CmdError::invalid("sprint has not started"));
    };
    let end = s.end_date.unwrap_or(start + Duration::days(14));
    let today = Utc::now().date_naive();
    let last = if s.state == "active" {
        end.min(today).max(start)
    } else {
        end.max(start)
    };
    // Scope: what is in the sprint now plus what was carried out of it when it closed.
    let items: Vec<(Uuid, f64)> = sqlx::query_as(
        "select note_id, coalesce(estimate, 1)::float8 from projects.items
         where sprint_id = $1
            or note_id in (select (jsonb_array_elements_text(payload->'moved_item_ids'))::uuid
                           from core.events where kind = 'sprint.completed' and payload->>'sprint_id' = $1::text)",
    )
    .bind(s.id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let ids: Vec<Uuid> = items.iter().map(|i| i.0).collect();
    let rows: Vec<(Uuid, DateTime<Utc>, String)> = sqlx::query_as(
        "select note_id, created_at, payload->>'category' from core.events
         where kind = 'item.transitioned' and note_id = any($1)",
    )
    .bind(&ids)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let events: Vec<_> = rows
        .into_iter()
        .map(|(id, at, c)| {
            (
                id,
                at,
                if c == "done" {
                    Category::Done
                } else {
                    Category::InProgress
                },
            )
        })
        .collect();
    let total: f64 = items.iter().map(|i| i.1).sum();
    Ok(json!({
        "sprint_id": s.id, "name": s.name, "state": s.state, "total_points": total,
        "points": burndown(start, last, &items, &events),
    }))
}

#[derive(Deserialize, JsonSchema)]
pub struct VelocityIn {
    pub space_id: Uuid,
    /// How many closed sprints to include. Default 6.
    #[serde(default)]
    pub sprints: Option<i64>,
}

/// Points per closed sprint (unestimated items count as 1) and the average. Read from `sprint.completed` events.
async fn report_velocity(ctx: &mut Ctx, i: VelocityIn) -> CmdResult<Value> {
    assert_space_access(ctx, i.space_id, false).await?;
    let rows: Vec<(Value, DateTime<Utc>)> = sqlx::query_as(
        "select payload, created_at from core.events
         where kind = 'sprint.completed' and payload->>'space_id' = $1::text order by id desc limit $2",
    )
    .bind(i.space_id)
    .bind(i.sprints.unwrap_or(6).clamp(1, 52))
    .fetch_all(&mut *ctx.tx)
    .await?;
    let sprints: Vec<Value> = rows
        .iter()
        .map(|(p, at)| {
            let committed = p["completed_points"].as_f64().unwrap_or(0.0)
                + p["moved_points"].as_f64().unwrap_or(0.0);
            json!({
                "sprint_id": p["sprint_id"], "name": p["name"], "completed_at": at,
                "completed_points": p["completed_points"], "completed_count": p["completed_count"],
                "committed_points": committed,
            })
        })
        .collect();
    let sum: f64 = rows
        .iter()
        .map(|(p, _)| p["completed_points"].as_f64().unwrap_or(0.0))
        .sum();
    let average = if rows.is_empty() {
        0.0
    } else {
        sum / rows.len() as f64
    };
    Ok(json!({"space_id": i.space_id, "sprints": sprints, "average_points": average}))
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "sprint.create",
            "Create a sprint in a Space.",
            ToolGroup::WriteItems,
            handler!(sprint_create),
        ),
        Command::new(
            "sprint.start",
            "Start a future sprint (one active sprint per Space).",
            ToolGroup::WriteItems,
            handler!(sprint_start),
        ),
        Command::new(
            "sprint.complete",
            "Complete the active sprint; unfinished items move to the next sprint or the backlog.",
            ToolGroup::WriteItems,
            handler!(sprint_complete),
        ),
        Command::new(
            "sprint.list",
            "List sprints, optionally by Space and state.",
            ToolGroup::Read,
            handler!(sprint_list),
        ),
        Command::new(
            "version.list",
            "List versions in a Space.",
            ToolGroup::Read,
            handler!(version_list),
        ),
        Command::new(
            "component.list",
            "List components in a Space.",
            ToolGroup::Read,
            handler!(component_list),
        ),
        Command::new(
            "version.create",
            "Create a release version (fixVersion) in a Space.",
            ToolGroup::WriteItems,
            handler!(version_create),
        ),
        Command::new(
            "version.release",
            "Mark a version released.",
            ToolGroup::WriteItems,
            handler!(version_release),
        ),
        Command::new(
            "component.create",
            "Create a component in a Space.",
            ToolGroup::WriteItems,
            handler!(component_create),
        ),
        Command::new(
            "report.burndown",
            "Daily remaining points for a sprint, from the item event log.",
            ToolGroup::Read,
            handler!(report_burndown),
        ),
        Command::new(
            "report.velocity",
            "Completed points for the last closed sprints of a Space and their average.",
            ToolGroup::Read,
            handler!(report_velocity),
        ),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    #[test]
    fn burndown_counts_latest_state_per_day() {
        let (a, b, c) = (Uuid::new_v4(), Uuid::new_v4(), Uuid::new_v4());
        let d = |day: u32| NaiveDate::from_ymd_opt(2026, 3, day).unwrap();
        let at = |day: u32, h: u32| Utc.with_ymd_and_hms(2026, 3, day, h, 0, 0).unwrap();
        let items = vec![(a, 5.0), (b, 3.0), (c, 2.0)];
        let events = vec![
            (a, at(2, 9), Category::Done),
            (b, at(2, 10), Category::Done),
            (b, at(3, 8), Category::InProgress), // reopened
            (c, at(4, 8), Category::Done),
        ];
        let pts = burndown(d(1), d(4), &items, &events);
        let rem: Vec<f64> = pts.iter().map(|p| p.remaining).collect();
        assert_eq!(rem, vec![10.0, 2.0, 5.0, 3.0]);
        assert_eq!(pts[0].ideal, 10.0);
        assert_eq!(pts[3].ideal, 0.0);
    }

    #[test]
    fn burndown_single_day() {
        let pts = burndown(
            NaiveDate::from_ymd_opt(2026, 3, 1).unwrap(),
            NaiveDate::from_ymd_opt(2026, 3, 1).unwrap(),
            &[(Uuid::new_v4(), 2.0)],
            &[],
        );
        assert_eq!(pts.len(), 1);
        assert_eq!(pts[0].remaining, 2.0);
    }
}
