//! Page versions and restore. Versions (core.note_revisions) are immutable checkpoints taken while editing.
//! Restoring never rewrites history: it records the live state as a version first, writes the chosen version's
//! content as the page, starts a new collab epoch (so open editors on the old content are fenced out and reload),
//! and records the restore itself as a new version.

use chrono::{DateTime, Utc};
use cnote_core::access::assert_note_access;
use cnote_core::{CmdError, CmdResult, Command, Ctx, Module, Role, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::json;
use uuid::Uuid;

pub fn module() -> Module {
    Module {
        commands: vec![
            Command::new("note.revisions", "List a page's saved versions, newest first.", ToolGroup::Read, handler!(list)),
            Command::new("note.revision", "Read one saved version of a page, with its Markdown content.", ToolGroup::Read, handler!(get)),
            Command::new(
                "note.restore",
                "Restore a page to a saved version. The current content is kept as a version first; nothing is deleted.",
                ToolGroup::WriteNotes,
                handler!(restore),
            )
            .min_role(Role::Guest), // edit access to the page is checked per note
        ],
        workspace_hooks: vec![],
        event_handlers: vec![],
        job_handlers: vec![],
        sweeps: vec![],
    }
}

#[derive(Deserialize, JsonSchema)]
pub struct ByNote {
    pub note_id: Uuid,
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct Revision {
    pub id: Uuid,
    pub note_id: Uuid,
    pub number: i32,
    pub title: String,
    /// edit | before_restore | restore
    pub reason: String,
    pub restored_from: Option<Uuid>,
    pub created_by: Option<Uuid>,
    pub created_by_name: Option<String>,
    pub created_at: DateTime<Utc>,
    /// Only on note.revision.
    #[sqlx(default)]
    pub content_md: Option<String>,
}

const COLS: &str = "r.id, r.note_id, r.number, r.title, r.reason, r.restored_from, r.created_by, \
     u.name as created_by_name, r.created_at";

async fn list(ctx: &mut Ctx, i: ByNote) -> CmdResult<Vec<Revision>> {
    assert_note_access(ctx, i.note_id, false).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {COLS} from core.note_revisions r left join core.users u on u.id = r.created_by
         where r.note_id = $1 order by r.number desc limit 200"
    )))
    .bind(i.note_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

async fn load(ctx: &mut Ctx, id: Uuid) -> CmdResult<Revision> {
    let r: Option<Revision> = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {COLS}, r.content_md from core.note_revisions r left join core.users u on u.id = r.created_by
         where r.id = $1"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let r = r.ok_or_else(|| CmdError::not_found("version"))?;
    // Same answer as a missing version when the page itself is hidden.
    assert_note_access(ctx, r.note_id, false)
        .await
        .map_err(|e| {
            if e.code == cnote_core::ErrorCode::NotFound {
                CmdError::not_found("version")
            } else {
                e
            }
        })?;
    Ok(r)
}

async fn get(ctx: &mut Ctx, i: ById) -> CmdResult<Revision> {
    load(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct Restore {
    pub note_id: Uuid,
    pub revision_id: Uuid,
    /// The collab_epoch the caller reviewed (note.get). If the page was restored since, the restore is refused.
    #[serde(default)]
    pub expected_epoch: Option<i32>,
}

#[derive(Serialize, JsonSchema)]
pub struct Restored {
    pub note_id: Uuid,
    pub collab_epoch: i32,
    pub version: i32,
    /// The version recording this restore.
    pub revision: Revision,
}

async fn insert_rev(
    ctx: &mut Ctx,
    note: Uuid,
    content: &str,
    reason: &str,
    from: Option<Uuid>,
) -> CmdResult<Uuid> {
    Ok(sqlx::query_scalar(
        "insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, restored_from, created_by)
         select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions r where r.note_id = n.id), 0) + 1,
                n.title, $2, $3, $4, $5
         from core.notes n where n.id = $1
         returning id",
    )
    .bind(note)
    .bind(content)
    .bind(reason)
    .bind(from)
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

async fn restore(ctx: &mut Ctx, i: Restore) -> CmdResult<Restored> {
    assert_note_access(ctx, i.note_id, true).await?;
    let src = load(ctx, i.revision_id).await?;
    if src.note_id != i.note_id {
        return Err(CmdError::not_found("version"));
    }
    // Lock the page: concurrent restores and collab snapshots of this page serialise here.
    let epoch: i32 =
        sqlx::query_scalar("select collab_epoch from core.notes where id = $1 for update")
            .bind(i.note_id)
            .fetch_one(&mut *ctx.tx)
            .await?;
    if i.expected_epoch.is_some_and(|e| e != epoch) {
        return Err(CmdError::conflict(
            "the page was restored by someone else since you opened it",
        ));
    }
    // The live content: last snapshot plus every logged edit not yet folded in.
    let (doc, _, _) = crate::load_doc(&mut ctx.tx, i.note_id).await?;
    let live = crate::md::to_markdown(&doc);
    insert_rev(ctx, i.note_id, &live, "before_restore", None).await?;

    let content = src.content_md.clone().unwrap_or_default();
    let (version, epoch): (i32, i32) = sqlx::query_as(
        "update core.notes set content_md = $2, content_ydoc = null, collab_epoch = collab_epoch + 1,
           version = version + 1, updated_at = now()
         where id = $1 returning version, collab_epoch",
    )
    .bind(i.note_id)
    .bind(&content)
    .fetch_one(&mut *ctx.tx)
    .await?;
    // Absorbed into the before_restore version above; the new epoch starts with an empty log.
    sqlx::query("delete from core.note_updates where note_id = $1")
        .bind(i.note_id)
        .execute(&mut *ctx.tx)
        .await?;
    let rid = insert_rev(ctx, i.note_id, &content, "restore", Some(src.id)).await?;
    ctx.emit(
        "note.restored",
        Some(i.note_id),
        json!({"from_revision": src.number, "version": version, "collab_epoch": epoch}),
    )
    .await?;
    let mut revision = load(ctx, rid).await?;
    revision.content_md = None;
    Ok(Restored {
        note_id: i.note_id,
        collab_epoch: epoch,
        version,
        revision,
    })
}
