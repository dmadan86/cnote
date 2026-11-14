//! Freshness engine (ADR-013): binding changes and lapsed dates mark pages stale; AI only ever *proposes* edits.

use chrono::{Duration, NaiveDate, Utc};
use cnote_core::ai::{ChatMessage, CompletionRequest};
use cnote_core::events::{self, Event};
use cnote_core::{CmdError, CmdResult, Ctx, Principal, Registry};
use futures::future::BoxFuture;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

pub const DEFAULT_VERIFY_DAYS: i64 = 90;
/// After a date lapse the owner gets this long to answer before the second lapse archives the page.
pub const LAPSE_GRACE_DAYS: i64 = 14;
/// Imported `unverified` pages nobody claimed within this many days are archived.
pub const GRAVEYARD_DAYS: i32 = 90;

/// Event kinds that say nothing about the content of the note they are attached to.
const IGNORED: [&str; 6] = [
    "doc.",
    "relation.",
    "comment.",
    "space.",
    "note.created",
    "note.deleted",
];

pub async fn flag(
    ctx: &mut Ctx,
    note_id: Uuid,
    reason: &str,
    source_note: Option<Uuid>,
    source_event: Option<i64>,
) -> CmdResult<Uuid> {
    Ok(sqlx::query_scalar(
        "insert into docs.freshness_flags (workspace_id, note_id, reason, source_note_id, source_event_id)
         values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(note_id)
    .bind(reason)
    .bind(source_note)
    .bind(source_event)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

/// Any event on note X: pages bound to X become stale, get a flag and an AI edit proposal job.
pub async fn on_event(ctx: &mut Ctx, ev: &Event) -> CmdResult<()> {
    let Some(src) = ev.note_id else {
        return Ok(());
    };
    if IGNORED.iter().any(|p| ev.kind.starts_with(p)) {
        return Ok(());
    }
    if ev.kind == "note.updated"
        && !ev.payload["changed"]
            .as_array()
            .is_some_and(|a| a.iter().any(|c| c == "content" || c == "title"))
    {
        return Ok(());
    }
    let pages: Vec<Uuid> = sqlx::query_scalar(
        "select distinct r.from_id from core.relations r join core.notes n on n.id = r.from_id
         where r.to_id = $1 and r.kind = 'binds' and r.from_id <> $1
           and cardinality(n.types) = 0 and n.deleted_at is null and n.freshness <> 'archived'",
    )
    .bind(src)
    .fetch_all(&mut *ctx.tx)
    .await?;
    for page in pages {
        // One open flag per (page, source): a burst of events on X must not pile up proposals.
        let open: bool = sqlx::query_scalar(
            "select exists (select 1 from docs.freshness_flags where note_id = $1 and source_note_id = $2
               and reason = 'binding_changed' and resolved_at is null)",
        )
        .bind(page)
        .bind(src)
        .fetch_one(&mut *ctx.tx)
        .await?;
        if open {
            continue;
        }
        let flag_id = flag(ctx, page, "binding_changed", Some(src), Some(ev.id)).await?;
        sqlx::query(
            "update core.notes set freshness = 'stale' where id = $1 and freshness = 'fresh'",
        )
        .bind(page)
        .execute(&mut *ctx.tx)
        .await?;
        ctx.emit(
            "doc.stale",
            Some(page),
            json!({"reason": "binding_changed", "source_note_id": src, "source_event": ev.kind, "flag_id": flag_id}),
        )
        .await?;
        let ws = ctx.workspace_id();
        events::enqueue(
            &mut ctx.tx,
            Some(ws),
            "docs.propose_edit",
            json!({"flag_id": flag_id}),
            None,
        )
        .await?;
    }
    Ok(())
}

/// Job `docs.propose_edit`: ask the model for a revised page and store it for the owner to review.
pub fn propose_edit_job(ctx: &mut Ctx, payload: Value) -> BoxFuture<'_, CmdResult<()>> {
    Box::pin(async move {
        let flag_id: Uuid = payload["flag_id"]
            .as_str()
            .and_then(|s| s.parse().ok())
            .ok_or_else(|| CmdError::invalid("flag_id required"))?;
        let row: Option<(Uuid, Option<Uuid>, Option<i64>)> = sqlx::query_as(
            "select note_id, source_note_id, source_event_id from docs.freshness_flags
             where id = $1 and resolved_at is null
               and not exists (select 1 from docs.edit_proposals where flag_id = $1)",
        )
        .bind(flag_id)
        .fetch_optional(&mut *ctx.tx)
        .await?;
        let Some((page, source, event_id)) = row else {
            return Ok(()); // resolved meanwhile, or already proposed (job retry)
        };
        let (title, content, owner, base_version): (String, String, Option<Uuid>, i32) =
            sqlx::query_as(
                "select title, content_md, owner_id, version from core.notes where id = $1",
            )
            .bind(page)
            .fetch_one(&mut *ctx.tx)
            .await?;
        // Quote the source only if the page owner may read it (the proposal is shown to the owner).
        let readable = match owner {
            Some(o) => crate::bindings::readable_space_ids_for(ctx, o).await?,
            None => vec![],
        };
        let owner_admin = match owner {
            Some(o) => crate::bindings::is_admin_user(ctx, o).await?,
            None => false,
        };
        let src: Option<(String, String)> =
            match source {
                Some(s) => sqlx::query_as(
                    "select title, content_md from core.notes where id = $1 and space_id = any($2)
                       and core.note_access(id, $3, $4, $2) <> 'none'",
                )
                .bind(s)
                .bind(&readable)
                .bind(owner)
                .bind(owner_admin)
                .fetch_optional(&mut *ctx.tx)
                .await?,
                None => None,
            };
        let kind: Option<String> = match event_id {
            Some(e) => {
                sqlx::query_scalar("select kind from core.events where id = $1")
                    .bind(e)
                    .fetch_optional(&mut *ctx.tx)
                    .await?
            }
            None => None,
        };
        let (src_title, src_md) = src.unwrap_or_default();
        let kind = kind.unwrap_or_else(|| "a bound note changed".into());
        let req = CompletionRequest {
            purpose: "propose_edit".into(),
            max_tokens: (content.len() / 3 + 256).min(8000) as u32,
            messages: vec![
                ChatMessage {
                    role: "system".into(),
                    content: format!(
                        "You maintain the wiki page \"{title}\". The note \"{src_title}\" it documents changed ({kind}). \
                         Reply with the full revised page in Markdown, changing only what the new state of that note \
                         makes wrong. Current state of the note:\n{src_md}"
                    ),
                },
                ChatMessage {
                    role: "user".into(),
                    content: content.clone(),
                },
            ],
        };
        let done = ctx.registry.ai.complete(req).await?;
        let rationale = format!(
            "\"{src_title}\" changed ({kind}), so this page may be out of date. Drafted by {}/{}; review before applying.",
            done.provider, done.model
        );
        let id: Uuid = sqlx::query_scalar(
            "insert into docs.edit_proposals (workspace_id, note_id, flag_id, proposed_md, rationale, base_version)
             values ($1,$2,$3,$4,$5,$6) returning id",
        )
        .bind(ctx.workspace_id())
        .bind(page)
        .bind(flag_id)
        .bind(done.text)
        .bind(&rationale)
        .bind(base_version)
        .fetch_one(&mut *ctx.tx)
        .await?;
        ctx.emit(
            "doc.proposal_created",
            Some(page),
            json!({"proposal_id": id, "flag_id": flag_id}),
        )
        .await?;
        Ok(())
    })
}

/// Daily sweep over every workspace.
pub fn sweep(pool: PgPool, registry: Arc<Registry>) -> BoxFuture<'static, anyhow::Result<()>> {
    Box::pin(async move {
        let ids: Vec<Uuid> = sqlx::query_scalar("select core.all_workspace_ids()")
            .fetch_all(&pool)
            .await?;
        for ws in ids {
            let mut ctx = Ctx {
                tx: cnote_db::begin_tenant(&pool, ws).await?,
                principal: Principal::system(ws),
                registry: registry.clone(),
            };
            sweep_workspace(&mut ctx, Utc::now().date_naive()).await?;
            ctx.tx.commit().await?;
        }
        Ok(())
    })
}

pub async fn sweep_workspace(ctx: &mut Ctx, today: NaiveDate) -> CmdResult<()> {
    // Graveyard mode: imported pages nobody claimed.
    let unclaimed: Vec<(Uuid, Option<Uuid>)> = sqlx::query_as(
        "update core.notes set freshness = 'archived', archived_at = now()
         where freshness = 'unverified' and cardinality(types) = 0 and deleted_at is null
           and created_at <= now() - make_interval(days => $1)
         returning id, owner_id",
    )
    .bind(GRAVEYARD_DAYS)
    .fetch_all(&mut *ctx.tx)
    .await?;
    for (id, owner) in unclaimed {
        ctx.emit(
            "doc.archived",
            Some(id),
            json!({"reason": "unclaimed_import", "owner_id": owner}),
        )
        .await?;
    }

    let lapsed: Vec<(Uuid, Option<Uuid>, i32)> = sqlx::query_as(
        "select id, owner_id, lapse_count from core.notes
         where cardinality(types) = 0 and deleted_at is null and freshness in ('fresh','stale') and verify_by < $1",
    )
    .bind(today)
    .fetch_all(&mut *ctx.tx)
    .await?;
    for (id, owner, count) in lapsed {
        let count = count + 1;
        flag(ctx, id, "date_lapsed", None, None).await?;
        if count >= 2 {
            sqlx::query("update core.notes set freshness = 'archived', archived_at = now(), lapse_count = $2 where id = $1")
                .bind(id)
                .bind(count)
                .execute(&mut *ctx.tx)
                .await?;
            ctx.emit(
                "doc.archived",
                Some(id),
                json!({"reason": "lapsed_twice", "owner_id": owner}),
            )
            .await?;
        } else {
            sqlx::query("update core.notes set freshness = 'stale', lapse_count = $2, verify_by = $3 where id = $1")
                .bind(id)
                .bind(count)
                .bind(today + Duration::days(LAPSE_GRACE_DAYS))
                .execute(&mut *ctx.tx)
                .await?;
            ctx.emit(
                "doc.lapsed",
                Some(id),
                json!({"owner_id": owner, "lapse_count": count, "reverify_by": today + Duration::days(LAPSE_GRACE_DAYS)}),
            )
            .await?;
        }
    }
    Ok(())
}
