//! The `import.run` job: process one batch, save the cursor, re-enqueue until done (ADR-012).
//! Phases: records (upsert notes, history, comments, attachments) -> links (parents, relations, which need every
//! record to exist) -> extras (Jira saved filters and automation rules).
//! shortcut: the source text is re-parsed each batch (kept in importers.jobs.input); fine for tens of thousands of
//! records, switch to a staged table if exports get much larger.

use crate::jira::jql_supported;
use crate::model::*;
use crate::parse_input;
use chrono::Utc;
use cnote_core::error::{CmdError, CmdResult};
use cnote_core::notes::{CreateNote, insert_note};
use cnote_core::{Ctx, events};
use serde_json::{Value, json};
use std::collections::HashMap;
use uuid::Uuid;

#[derive(sqlx::FromRow)]
struct JobRow {
    source: String,
    space_id: Option<Uuid>,
    status: String,
    cursor: Value,
    report: Value,
    input: Value,
    created_by: Option<Uuid>,
}

struct Job {
    source: String,
    space_id: Uuid,
    users: HashMap<String, Option<Uuid>>,
}

pub async fn run(ctx: &mut Ctx, payload: Value) -> CmdResult<()> {
    let id: Uuid = payload["import_id"]
        .as_str()
        .and_then(|s| s.parse().ok())
        .ok_or_else(|| CmdError::invalid("import_id missing"))?;
    let row: JobRow = sqlx::query_as(
        "select source, space_id, status, cursor, report, input, created_by from importers.jobs where id = $1 for update",
    )
    .bind(id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if matches!(row.status.as_str(), "done" | "failed") {
        return Ok(());
    }
    // Writes are attributed to the person who started the import.
    ctx.principal.user_id = row.created_by;
    let mut report: Report = serde_json::from_value(row.report.clone()).unwrap_or_default();

    let parsed = match (row.space_id, parse_input(&row.source, &row.input)) {
        (Some(_), Ok(p)) => p,
        (_, res) => {
            let why = res
                .err()
                .unwrap_or_else(|| "the target space was deleted".into());
            report.warn(format!("import failed: {why}"));
            sqlx::query("update importers.jobs set status = 'failed', report = $2, finished_at = now() where id = $1")
                .bind(id)
                .bind(serde_json::to_value(&report)?)
                .execute(&mut *ctx.tx)
                .await?;
            return Ok(());
        }
    };
    let mut job = Job {
        source: row.source.clone(),
        space_id: row.space_id.unwrap(),
        users: HashMap::new(),
    };
    let bs = row.input["batch_size"]
        .as_u64()
        .unwrap_or(200)
        .clamp(1, 1000) as usize;
    let phase = row.cursor["phase"]
        .as_str()
        .unwrap_or("records")
        .to_string();
    let offset = row.cursor["offset"].as_u64().unwrap_or(0) as usize;

    sqlx::query("update importers.jobs set status = 'running' where id = $1")
        .bind(id)
        .execute(&mut *ctx.tx)
        .await?;

    let total = parsed.recs.len();
    let end = (offset + bs).min(total);
    match phase.as_str() {
        "records" => {
            if offset == 0 {
                for w in &parsed.warnings {
                    report.warn(w.clone());
                    if w.starts_with("skipped") || w.starts_with("archived cards") {
                        report.skipped += 1;
                    }
                }
            }
            for rec in &parsed.recs[offset..end] {
                savepoint(ctx).await?;
                let r = upsert(ctx, &mut job, rec, &mut report).await;
                settle(ctx, &mut report, &rec.source_id, r).await?;
            }
        }
        "links" => {
            for rec in &parsed.recs[offset..end] {
                savepoint(ctx).await?;
                let r = link(ctx, &job, rec, &mut report).await;
                settle(ctx, &mut report, &rec.source_id, r).await?;
            }
        }
        _ => extras(ctx, &job, &parsed, &mut report).await?,
    }

    let (next_phase, next_offset) = if phase != "extras" && end < total {
        (phase.as_str(), end)
    } else {
        match phase.as_str() {
            "records" => ("links", 0),
            "links" => ("extras", 0),
            _ => ("done", 0),
        }
    };
    let cursor = json!({"phase": next_phase, "offset": next_offset});
    let report_json = serde_json::to_value(&report)?;
    if next_phase == "done" {
        sqlx::query("update importers.jobs set status = 'done', cursor = $2, report = $3, finished_at = now() where id = $1")
            .bind(id)
            .bind(&cursor)
            .bind(&report_json)
            .execute(&mut *ctx.tx)
            .await?;
        ctx.emit(
            "import.completed",
            None,
            json!({"import_id": id, "source": job.source, "report": report_json}),
        )
        .await?;
    } else {
        sqlx::query("update importers.jobs set cursor = $2, report = $3 where id = $1")
            .bind(id)
            .bind(&cursor)
            .bind(&report_json)
            .execute(&mut *ctx.tx)
            .await?;
        let ws = ctx.workspace_id();
        events::enqueue(
            &mut ctx.tx,
            Some(ws),
            "import.run",
            json!({"import_id": id}),
            None,
        )
        .await?;
    }
    Ok(())
}

/// One bad record must not sink the batch: each runs in a savepoint and failures are reported as skipped.
async fn savepoint(ctx: &mut Ctx) -> CmdResult<()> {
    sqlx::query("savepoint rec").execute(&mut *ctx.tx).await?;
    Ok(())
}

async fn settle(ctx: &mut Ctx, report: &mut Report, what: &str, r: CmdResult<()>) -> CmdResult<()> {
    match r {
        Ok(()) => {
            sqlx::query("release savepoint rec")
                .execute(&mut *ctx.tx)
                .await?;
        }
        Err(e) => {
            sqlx::query("rollback to savepoint rec")
                .execute(&mut *ctx.tx)
                .await?;
            report.skipped += 1;
            report.warn(format!("skipped {what}: {}", e.message));
        }
    }
    Ok(())
}

async fn find_note(ctx: &mut Ctx, job: &Job, source_id: &str) -> CmdResult<Option<Uuid>> {
    Ok(sqlx::query_scalar(
        "select id from core.notes where workspace_id = $1 and source_system = $2 and source_id = $3",
    )
    .bind(ctx.workspace_id())
    .bind(&job.source)
    .bind(source_id)
    .fetch_optional(&mut *ctx.tx)
    .await?)
}

async fn user_by_email(
    ctx: &mut Ctx,
    job: &mut Job,
    email: &str,
    report: &mut Report,
) -> CmdResult<Option<Uuid>> {
    let k = email.to_lowercase();
    if !job.users.contains_key(&k) {
        let id: Option<Uuid> = sqlx::query_scalar(
            "select u.id from core.users u join core.memberships m on m.user_id = u.id where lower(u.email) = $1",
        )
        .bind(&k)
        .fetch_optional(&mut *ctx.tx)
        .await?;
        if id.is_none() {
            report.warn(format!(
                "{email} is not a workspace member: person kept as text only"
            ));
        }
        job.users.insert(k.clone(), id);
    }
    Ok(job.users[&k])
}

async fn insert_event(
    ctx: &mut Ctx,
    job: &Job,
    note_id: Uuid,
    kind: &str,
    key: &str,
    at: Option<chrono::DateTime<Utc>>,
    mut payload: Value,
) -> CmdResult<bool> {
    payload["source_id"] = json!(key);
    let seen: bool = sqlx::query_scalar(
        "select exists(select 1 from core.events where note_id = $1 and payload->>'source_id' = $2)",
    )
    .bind(note_id)
    .bind(key)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if seen {
        return Ok(false);
    }
    sqlx::query(
        "insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, on_behalf_of, payload, created_at)
         values ($1,$2,$3,'import',$4,$5,$6,coalesce($7, now()))",
    )
    .bind(ctx.workspace_id())
    .bind(note_id)
    .bind(kind)
    .bind(&job.source)
    .bind(ctx.principal.user_id)
    .bind(&payload)
    .bind(at)
    .execute(&mut *ctx.tx)
    .await?;
    Ok(true)
}

async fn upsert(ctx: &mut Ctx, job: &mut Job, rec: &Rec, report: &mut Report) -> CmdResult<()> {
    if rec.title.trim().is_empty() || rec.source_id.is_empty() {
        return Err(CmdError::invalid("record has no title or id"));
    }
    for w in &rec.warnings {
        report.warn(w.clone());
    }
    let existing = find_note(ctx, job, &rec.source_id).await?;
    let (note_id, created) = match existing {
        Some(id) => {
            // A re-run applies the source's current state; content_ydoc is dropped so the editor reseeds from content_md.
            sqlx::query(
                "update core.notes set title = $2, content_md = $3, source_url = $4,
                   content_ydoc = case when content_md is distinct from $3 then null else content_ydoc end,
                   version = version + 1, updated_at = now()
                 where id = $1 and (title is distinct from $2 or content_md is distinct from $3 or source_url is distinct from $4)",
            )
            .bind(id)
            .bind(rec.title.trim())
            .bind(&rec.body_md)
            .bind(&rec.source_url)
            .execute(&mut *ctx.tx)
            .await?;
            report.updated += 1;
            (id, false)
        }
        None => {
            let n = insert_note(
                ctx,
                CreateNote {
                    space_id: job.space_id,
                    title: rec.title.clone(),
                    parent_id: None,
                    content_md: Some(rec.body_md.clone()),
                    types: if rec.item.is_some() {
                        vec!["item".into()]
                    } else {
                        vec![]
                    },
                    owner_id: None,
                    verify_by: None,
                },
            )
            .await?;
            sqlx::query(
                "update core.notes set source_system = $2, source_id = $3, source_url = $4,
                   created_at = coalesce($5, created_at), freshness = case when $6 then 'unverified' else freshness end
                 where id = $1",
            )
            .bind(n.id)
            .bind(&job.source)
            .bind(&rec.source_id)
            .bind(&rec.source_url)
            .bind(rec.created_at)
            .bind(rec.item.is_none())
            .execute(&mut *ctx.tx)
            .await?;
            if rec.item.is_none() {
                // Graveyard mode: unclaimed imported pages get archived by the docs module after 90 days.
                sqlx::query("insert into docs.freshness_flags (workspace_id, note_id, reason) values ($1,$2,'imported')")
                    .bind(ctx.workspace_id())
                    .bind(n.id)
                    .execute(&mut *ctx.tx)
                    .await?;
            }
            let p = json!({"source_system": job.source, "source_url": rec.source_url, "author": rec.author, "original_created_at": rec.created_at});
            insert_event(
                ctx,
                job,
                n.id,
                "note.imported",
                &format!("{}:imported", rec.source_id),
                rec.created_at,
                p,
            )
            .await?;
            report.imported += 1;
            (n.id, true)
        }
    };

    if let Some(item) = &rec.item {
        upsert_item(ctx, job, note_id, created, item, report).await?;
    }

    for h in &rec.history {
        let (kind, payload) = if h.field == "status" {
            ("item.transitioned", json!({"from": h.from, "to": h.to}))
        } else {
            (
                "item.updated",
                json!({"field": h.field, "from": h.from, "to": h.to}),
            )
        };
        let mut payload = payload;
        payload["author"] = json!(h.author);
        payload["at"] = json!(h.at);
        if insert_event(
            ctx,
            job,
            note_id,
            kind,
            &format!("{}:h{}", rec.source_id, h.id),
            h.at,
            payload,
        )
        .await?
        {
            report.history_events += 1;
        }
    }
    for c in &rec.comments {
        let author_id = match &c.author_email {
            Some(e) => user_by_email(ctx, job, e, report).await?,
            None => None,
        };
        let is_new: bool = sqlx::query_scalar(
            "insert into core.comments (workspace_id, note_id, author_id, author_name, body, created_at, source_id)
             values ($1,$2,$3,$4,$5,coalesce($6, now()),$7)
             on conflict (note_id, source_id) where source_id is not null do update set body = excluded.body
             returning (xmax = 0)",
        )
        .bind(ctx.workspace_id())
        .bind(note_id)
        .bind(author_id)
        .bind(&c.author)
        .bind(&c.body)
        .bind(c.at)
        .bind(&c.id)
        .fetch_one(&mut *ctx.tx)
        .await?;
        if is_new {
            report.comments += 1;
            let p = json!({"author": c.author, "at": c.at});
            insert_event(
                ctx,
                job,
                note_id,
                "comment.added",
                &format!("{}:c{}", rec.source_id, c.id),
                c.at,
                p,
            )
            .await?;
        }
    }
    for a in &rec.attachments {
        // Files are fetched by a later job; storage_key holds the source URL until then.
        let r = sqlx::query(
            "insert into core.attachments (workspace_id, note_id, filename, content_type, size_bytes, storage_key)
             select $1,$2,$3,$4,$5,$6 where not exists (select 1 from core.attachments where note_id = $2 and storage_key = $6)",
        )
        .bind(ctx.workspace_id())
        .bind(note_id)
        .bind(&a.filename)
        .bind(&a.content_type)
        .bind(a.size)
        .bind(&a.url)
        .execute(&mut *ctx.tx)
        .await?;
        report.attachments += r.rows_affected() as u32;
    }
    Ok(())
}

async fn upsert_item(
    ctx: &mut Ctx,
    job: &mut Job,
    note_id: Uuid,
    created: bool,
    it: &ItemData,
    report: &mut Report,
) -> CmdResult<()> {
    let ws = ctx.workspace_id();
    let assignee = match &it.assignee_email {
        Some(e) => user_by_email(ctx, job, e, report).await?,
        None => None,
    };
    let reporter = match &it.reporter_email {
        Some(e) => user_by_email(ctx, job, e, report).await?,
        None => None,
    };
    let sprint: Option<Uuid> = match &it.sprint {
        None => None,
        Some(s) => {
            let found: Option<Uuid> = sqlx::query_scalar(
                "select id from projects.sprints where space_id = $1 and name = $2",
            )
            .bind(job.space_id)
            .bind(&s.name)
            .fetch_optional(&mut *ctx.tx)
            .await?;
            Some(match found {
                Some(id) => {
                    sqlx::query("update projects.sprints set state = $2, start_date = $3, end_date = $4 where id = $1")
                        .bind(id)
                        .bind(s.state)
                        .bind(s.start)
                        .bind(s.end)
                        .execute(&mut *ctx.tx)
                        .await?;
                    id
                }
                None => {
                    sqlx::query_scalar(
                        "insert into projects.sprints (workspace_id, space_id, name, state, start_date, end_date) values ($1,$2,$3,$4,$5,$6) returning id",
                    )
                    .bind(ws)
                    .bind(job.space_id)
                    .bind(&s.name)
                    .bind(s.state)
                    .bind(s.start)
                    .bind(s.end)
                    .fetch_one(&mut *ctx.tx)
                    .await?
                }
            })
        }
    };
    let version: Option<Uuid> = match &it.fix_version {
        None => None,
        Some(v) => Some(
            sqlx::query_scalar(
                "insert into projects.versions (workspace_id, space_id, name, released, release_date) values ($1,$2,$3,$4,$5)
                 on conflict (space_id, name) do update set released = excluded.released, release_date = excluded.release_date
                 returning id",
            )
            .bind(ws)
            .bind(job.space_id)
            .bind(&v.name)
            .bind(v.released)
            .bind(v.date)
            .fetch_one(&mut *ctx.tx)
            .await?,
        ),
    };
    let component: Option<Uuid> = match &it.component {
        None => None,
        Some(c) => Some(
            sqlx::query_scalar(
                "insert into projects.components (workspace_id, space_id, name) values ($1,$2,$3)
                 on conflict (space_id, name) do update set name = excluded.name returning id",
            )
            .bind(ws)
            .bind(job.space_id)
            .bind(c)
            .fetch_one(&mut *ctx.tx)
            .await?,
        ),
    };
    if created {
        // Lock the space row to hand out the next number, then key = space key + number.
        let (number, space_key): (i32, String) = sqlx::query_as(
            "update core.spaces set item_seq = item_seq + 1 where id = $1 returning item_seq, key",
        )
        .bind(job.space_id)
        .fetch_one(&mut *ctx.tx)
        .await?;
        sqlx::query(
            "insert into projects.items (note_id, workspace_id, space_id, number, key, item_type, status, status_category, priority,
               assignee_id, reporter_id, sprint_id, fix_version_id, component_id, estimate, due_date, labels, custom, resolved_at)
             values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::float8,$16,$17,$18,$19)",
        )
        .bind(note_id)
        .bind(ws)
        .bind(job.space_id)
        .bind(number)
        .bind(format!("{space_key}-{number}"))
        .bind(&it.item_type)
        .bind(&it.status)
        .bind(it.status_category)
        .bind(it.priority)
        .bind(assignee)
        .bind(reporter)
        .bind(sprint)
        .bind(version)
        .bind(component)
        .bind(it.estimate)
        .bind(it.due_date)
        .bind(&it.labels)
        .bind(Value::Object(it.custom.clone()))
        .bind(it.resolved_at)
        .execute(&mut *ctx.tx)
        .await?;
    } else {
        sqlx::query(
            "update projects.items set item_type = $2, status = $3, status_category = $4, priority = $5, assignee_id = $6,
               reporter_id = $7, sprint_id = $8, fix_version_id = $9, component_id = $10, estimate = $11::float8,
               due_date = $12, labels = $13, custom = $14, resolved_at = $15
             where note_id = $1",
        )
        .bind(note_id)
        .bind(&it.item_type)
        .bind(&it.status)
        .bind(it.status_category)
        .bind(it.priority)
        .bind(assignee)
        .bind(reporter)
        .bind(sprint)
        .bind(version)
        .bind(component)
        .bind(it.estimate)
        .bind(it.due_date)
        .bind(&it.labels)
        .bind(Value::Object(it.custom.clone()))
        .bind(it.resolved_at)
        .execute(&mut *ctx.tx)
        .await?;
    }
    Ok(())
}

async fn relate(
    ctx: &mut Ctx,
    from: Uuid,
    to: Uuid,
    kind: &str,
    report: &mut Report,
) -> CmdResult<()> {
    let r = sqlx::query(
        "insert into core.relations (workspace_id, from_id, to_id, kind) values ($1,$2,$3,$4) on conflict (from_id, to_id, kind) do nothing",
    )
    .bind(ctx.workspace_id())
    .bind(from)
    .bind(to)
    .bind(kind)
    .execute(&mut *ctx.tx)
    .await?;
    report.relations += r.rows_affected() as u32;
    Ok(())
}

async fn link(ctx: &mut Ctx, job: &Job, rec: &Rec, report: &mut Report) -> CmdResult<()> {
    let Some(me) = find_note(ctx, job, &rec.source_id).await? else {
        return Ok(());
    };
    if let Some(parent) = &rec.parent {
        match find_note(ctx, job, parent).await? {
            Some(p) if p != me => {
                if rec.item.is_none() {
                    sqlx::query("update core.notes set parent_id = $1 where id = $2 and parent_id is distinct from $1")
                        .bind(p)
                        .bind(me)
                        .execute(&mut *ctx.tx)
                        .await?;
                }
                relate(ctx, p, me, "child", report).await?;
            }
            Some(_) => {}
            None => report.warn(format!(
                "parent {parent} is not part of this import: link skipped"
            )),
        }
    }
    for l in &rec.links {
        let Some(other) = find_note(ctx, job, &l.other).await? else {
            report.warn(format!(
                "link to {} skipped: not part of this import",
                l.other
            ));
            continue;
        };
        let (from, to) = match (l.kind, l.outward) {
            // Symmetric links appear on both issues; a fixed order lets the unique key dedupe them.
            ("relates", _) => (me.min(other), me.max(other)),
            (_, true) => (me, other),
            (_, false) => (other, me),
        };
        relate(ctx, from, to, l.kind, report).await?;
    }
    Ok(())
}

async fn extras(ctx: &mut Ctx, job: &Job, parsed: &Parsed, report: &mut Report) -> CmdResult<()> {
    let ws = ctx.workspace_id();
    for f in &parsed.filters {
        let supported = jql_supported(&f.jql);
        if !supported {
            report.warn(format!(
                "JQL not translatable for filter '{}': stored as text",
                f.name
            ));
        }
        let options =
            json!({"source_system": job.source, "source_id": f.id, "jql_supported": supported});
        let updated = sqlx::query(
            "update core.views set name = $3, query = $4, options = $5
             where space_id = $1 and options->>'source_system' = $2 and options->>'source_id' = $6",
        )
        .bind(job.space_id)
        .bind(&job.source)
        .bind(&f.name)
        .bind(&f.jql)
        .bind(&options)
        .bind(&f.id)
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
        if updated == 0 {
            sqlx::query("insert into core.views (workspace_id, space_id, name, layout, query, options, created_by) values ($1,$2,$3,'table',$4,$5,$6)")
                .bind(ws)
                .bind(job.space_id)
                .bind(&f.name)
                .bind(&f.jql)
                .bind(&options)
                .bind(ctx.principal.user_id)
                .execute(&mut *ctx.tx)
                .await?;
        }
        report.views += 1;
    }
    for a in &parsed.automations {
        let trigger = json!({"kind": "imported", "source": job.source, "original": a.trigger});
        let updated = sqlx::query(
            "update projects.automations set trigger = $3, conditions = $4, actions = $5
             where space_id = $1 and name = $2 and draft",
        )
        .bind(job.space_id)
        .bind(&a.name)
        .bind(&trigger)
        .bind(&a.conditions)
        .bind(&a.actions)
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
        if updated == 0 {
            sqlx::query(
                "insert into projects.automations (workspace_id, space_id, name, enabled, draft, trigger, conditions, actions, created_by)
                 values ($1,$2,$3,false,true,$4,$5,$6,$7)",
            )
            .bind(ws)
            .bind(job.space_id)
            .bind(&a.name)
            .bind(&trigger)
            .bind(&a.conditions)
            .bind(&a.actions)
            .bind(ctx.principal.user_id)
            .execute(&mut *ctx.tx)
            .await?;
        }
        report.automations += 1;
        report.warn(format!(
            "automation rule '{}' imported as a disabled draft: review before enabling",
            a.name
        ));
    }
    Ok(())
}
