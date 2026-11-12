//! Core commands over the universal Note (ADR-002): spaces, notes, relations, comments, history, search, views.

use crate::access::{assert_note_access, assert_space_access, readable_space_ids, viewer};
use crate::command::{Command, Ctx, ToolGroup};
use crate::error::{CmdError, CmdResult};
use crate::handler;
use chrono::{DateTime, NaiveDate, Utc};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

pub const DEFAULT_VERIFY_DAYS: i64 = 90;

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema, sqlx::FromRow)]
pub struct Space {
    pub id: Uuid,
    pub key: String,
    pub name: String,
    pub template: String,
    pub visibility: String,
    pub config: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema, sqlx::FromRow)]
pub struct Note {
    pub id: Uuid,
    pub space_id: Uuid,
    pub parent_id: Option<Uuid>,
    pub title: String,
    pub types: Vec<String>,
    pub content_md: String,
    pub owner_id: Option<Uuid>,
    pub verify_by: Option<NaiveDate>,
    pub verified_at: Option<DateTime<Utc>>,
    pub freshness: String,
    pub source_system: Option<String>,
    pub source_id: Option<String>,
    pub source_url: Option<String>,
    pub created_by: Option<Uuid>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
    pub version: i32,
    /// none | edit | view (page restriction, see note.restrict)
    pub restriction: String,
}

pub const NOTE_COLS: &str = "id, space_id, parent_id, title, types, content_md, owner_id, verify_by, verified_at, freshness, \
     source_system, source_id, source_url, created_by, created_at, updated_at, version, restriction";

// ---------- spaces ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateSpace {
    /// Short uppercase key used in item keys, e.g. ENG -> ENG-42
    pub key: String,
    pub name: String,
    /// blank | software | kanban | docs | crm | billing
    #[serde(default)]
    pub template: Option<String>,
    /// open | private
    #[serde(default)]
    pub visibility: Option<String>,
}

async fn space_create(ctx: &mut Ctx, i: CreateSpace) -> CmdResult<Space> {
    let key = i.key.trim().to_uppercase();
    if key.is_empty() || key.len() > 10 || !key.chars().all(|c| c.is_ascii_alphanumeric()) {
        return Err(CmdError::invalid("key must be 1-10 letters or digits"));
    }
    let s: Space = sqlx::query_as(
        "insert into core.spaces (workspace_id, key, name, template, visibility) values ($1,$2,$3,$4,$5)
         returning id, key, name, template, visibility, config",
    )
    .bind(ctx.workspace_id())
    .bind(&key)
    .bind(i.name.trim())
    .bind(i.template.unwrap_or_else(|| "blank".into()))
    .bind(i.visibility.unwrap_or_else(|| "open".into()))
    .fetch_one(&mut *ctx.tx)
    .await?;
    if s.visibility == "private"
        && let Some(u) = ctx.principal.user_id
    {
        sqlx::query("insert into core.space_members (workspace_id, space_id, user_id, role) values ($1,$2,$3,'admin')")
            .bind(ctx.workspace_id())
            .bind(s.id)
            .bind(u)
            .execute(&mut *ctx.tx)
            .await?;
    }
    ctx.emit(
        "space.created",
        None,
        json!({"space_id": s.id, "key": s.key, "template": s.template}),
    )
    .await?;
    Ok(s)
}

#[derive(Deserialize, JsonSchema, Default)]
pub struct Empty {}

async fn space_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<Space>> {
    let ids = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as("select id, key, name, template, visibility, config from core.spaces where id = any($1) order by name")
        .bind(ids)
        .fetch_all(&mut *ctx.tx)
        .await?)
}

// ---------- notes ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateNote {
    pub space_id: Uuid,
    pub title: String,
    #[serde(default)]
    pub parent_id: Option<Uuid>,
    #[serde(default)]
    pub content_md: Option<String>,
    /// Types this note carries, e.g. ["task"], ["deal"], ["meeting"]. Empty = a page.
    #[serde(default)]
    pub types: Vec<String>,
    /// Page owner (ADR-013). Defaults to the creator.
    #[serde(default)]
    pub owner_id: Option<Uuid>,
    /// Verify-by date (ADR-013). Pages default to 90 days from today.
    #[serde(default)]
    pub verify_by: Option<NaiveDate>,
}

/// Shared by modules that create typed notes (items, deals, invoices). Emits note.created.
pub async fn insert_note(ctx: &mut Ctx, i: CreateNote) -> CmdResult<Note> {
    assert_space_access(ctx, i.space_id, true).await?;
    if let Some(parent) = i.parent_id {
        // a child inherits the parent's restrictions, so creating one is an edit of the parent
        assert_note_access(ctx, parent, true).await?;
    }
    let is_page = i.types.is_empty();
    let owner = i.owner_id.or(ctx.principal.user_id);
    let verify_by = i.verify_by.or_else(|| {
        is_page.then(|| (Utc::now() + chrono::Duration::days(DEFAULT_VERIFY_DAYS)).date_naive())
    });
    let n: Note = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "insert into core.notes (workspace_id, space_id, parent_id, title, types, content_md, owner_id, verify_by, created_by)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9) returning {NOTE_COLS}"
    )))
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.parent_id)
    .bind(i.title.trim())
    .bind(&i.types)
    .bind(i.content_md.unwrap_or_default())
    .bind(owner)
    .bind(verify_by)
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if let Some(parent) = n.parent_id {
        add_relation(ctx, parent, n.id, "child", None).await?;
    }
    ctx.emit(
        "note.created",
        Some(n.id),
        json!({"title": n.title, "types": n.types, "space_id": n.space_id}),
    )
    .await?;
    Ok(n)
}

async fn note_create(ctx: &mut Ctx, i: CreateNote) -> CmdResult<Note> {
    insert_note(ctx, i).await
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

#[derive(Serialize, JsonSchema)]
pub struct NoteDetail {
    #[serde(flatten)]
    pub note: Note,
    pub relations: Vec<RelationOut>,
    /// cnote:// resource URI (ADR-004)
    pub uri: String,
    /// What the caller may do with this page: view | edit
    pub level: String,
}

pub async fn load_note(ctx: &mut Ctx, id: Uuid) -> CmdResult<Note> {
    sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {NOTE_COLS} from core.notes where id = $1 and deleted_at is null"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("note"))
}

async fn note_get(ctx: &mut Ctx, i: ById) -> CmdResult<NoteDetail> {
    assert_note_access(ctx, i.id, false).await?;
    let level = if assert_note_access(ctx, i.id, true).await.is_ok() {
        "edit"
    } else {
        "view"
    };
    let note = load_note(ctx, i.id).await?;
    let relations = list_relations(ctx, i.id).await?;
    let uri = format!("cnote://ws/{}/note/{}", ctx.workspace_id(), note.id);
    Ok(NoteDetail {
        note,
        relations,
        uri,
        level: level.into(),
    })
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateNote {
    pub id: Uuid,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub content_md: Option<String>,
    #[serde(default)]
    pub owner_id: Option<Uuid>,
    #[serde(default)]
    pub verify_by: Option<NaiveDate>,
    #[serde(default)]
    pub parent_id: Option<Uuid>,
    /// Optimistic concurrency: fail with conflict if the note changed since this version.
    #[serde(default)]
    pub expected_version: Option<i32>,
}

async fn note_update(ctx: &mut Ctx, i: UpdateNote) -> CmdResult<Note> {
    assert_note_access(ctx, i.id, true).await?;
    if let Some(parent) = i.parent_id {
        assert_note_access(ctx, parent, true).await?;
    }
    let before = load_note(ctx, i.id).await?;
    if let Some(v) = i.expected_version
        && v != before.version
    {
        return Err(CmdError::conflict(format!(
            "note changed (version {} != {v})",
            before.version
        )));
    }
    let n: Note = sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "update core.notes set title = coalesce($2, title), content_md = coalesce($3, content_md),
           owner_id = coalesce($4, owner_id), verify_by = coalesce($5, verify_by), parent_id = coalesce($6, parent_id),
           updated_at = now(), version = version + 1
         where id = $1 returning {NOTE_COLS}"
    )))
    .bind(i.id)
    .bind(i.title.as_deref().map(str::trim))
    .bind(&i.content_md)
    .bind(i.owner_id)
    .bind(i.verify_by)
    .bind(i.parent_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let mut changed = vec![];
    if n.title != before.title {
        changed.push("title")
    }
    if n.content_md != before.content_md {
        changed.push("content")
    }
    if n.owner_id != before.owner_id {
        changed.push("owner")
    }
    if n.verify_by != before.verify_by {
        changed.push("verify_by")
    }
    if n.parent_id != before.parent_id {
        changed.push("parent")
    }
    ctx.emit(
        "note.updated",
        Some(n.id),
        json!({"changed": changed, "version": n.version}),
    )
    .await?;
    if n.content_md != before.content_md {
        crate::notifications::notify_mentions(
            ctx,
            n.id,
            &n.title,
            &before.content_md,
            &n.content_md,
        )
        .await?;
    }
    Ok(n)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListNotes {
    #[serde(default)]
    pub space_id: Option<Uuid>,
    #[serde(default)]
    pub parent_id: Option<Uuid>,
    /// Only notes carrying this type; "page" = notes without types
    #[serde(default)]
    pub r#type: Option<String>,
    #[serde(default)]
    pub limit: Option<i64>,
}

async fn note_list(ctx: &mut Ctx, i: ListNotes) -> CmdResult<Vec<Note>> {
    let ids = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {NOTE_COLS} from core.notes
         where deleted_at is null and freshness <> 'archived' and (space_id = any($1) or $3::uuid is not null)
           and core.note_access(id, $6, $7, $1) <> 'none'
           and ($2::uuid is null or space_id = $2) and ($3::uuid is null or parent_id = $3)
           and ($4::text is null or ($4 = 'page' and types = '{{}}') or $4 = any(types))
         order by updated_at desc limit $5"
    )))
    .bind(ids)
    .bind(i.space_id)
    .bind(i.parent_id)
    .bind(i.r#type)
    .bind(i.limit.unwrap_or(100).clamp(1, 500))
    .bind(uid)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

async fn note_delete(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    assert_note_access(ctx, i.id, true).await?;
    sqlx::query("update core.notes set deleted_at = now() where id = $1")
        .bind(i.id)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit("note.deleted", Some(i.id), json!({})).await?;
    Ok(json!({"deleted": true}))
}

#[derive(Serialize, sqlx::FromRow)]
pub struct HistoryOut {
    #[serde(flatten)]
    #[sqlx(flatten)]
    pub event: crate::events::Event,
    /// Who did it: the person or agent behind the event (None for the system).
    pub actor_name: Option<String>,
    /// Who it was about: the person mentioned, shared with, assigned, or made owner.
    pub subject_name: Option<String>,
}

async fn note_history(ctx: &mut Ctx, i: ById) -> CmdResult<Vec<HistoryOut>> {
    assert_note_access(ctx, i.id, false).await?;
    Ok(sqlx::query_as(
        "select e.*,
           coalesce(u.name, a.name) as actor_name,
           (select s.name from core.users s
             where s.id::text = coalesce(e.payload->>'user_id', e.payload->>'assignee_id',
                     case when e.kind = 'doc.owner_changed' then e.payload->>'to' end,
                     (select n.user_id::text from core.notifications n where n.id::text = e.payload->>'notification_id'))
           ) as subject_name
         from core.events e
         left join core.users u on e.actor_type = 'user' and u.id::text = e.actor_id
         left join core.agents a on e.actor_type = 'agent' and a.id::text = e.actor_id
         where e.note_id = $1 order by e.id desc limit 200",
    )
    .bind(i.id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- relations ----------

#[derive(Debug, Serialize, Deserialize, JsonSchema, sqlx::FromRow)]
pub struct RelationOut {
    pub id: Uuid,
    pub from_id: Uuid,
    pub to_id: Uuid,
    pub kind: String,
    pub external_ref: Option<String>,
    pub other_title: Option<String>,
}

pub async fn add_relation(
    ctx: &mut Ctx,
    from: Uuid,
    to: Uuid,
    kind: &str,
    external_ref: Option<&str>,
) -> CmdResult<()> {
    sqlx::query(
        "insert into core.relations (workspace_id, from_id, to_id, kind, external_ref) values ($1,$2,$3,$4,$5)
         on conflict (from_id, to_id, kind) do nothing",
    )
    .bind(ctx.workspace_id())
    .bind(from)
    .bind(to)
    .bind(kind)
    .bind(external_ref)
    .execute(&mut *ctx.tx)
    .await?;
    Ok(())
}

/// Relations whose other end the caller can read (no titles leak from private spaces).
async fn list_relations(ctx: &mut Ctx, id: Uuid) -> CmdResult<Vec<RelationOut>> {
    let ids = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    Ok(sqlx::query_as(
        "select r.id, r.from_id, r.to_id, r.kind, r.external_ref, o.title as other_title
         from core.relations r
         join core.notes o on o.id = case when r.from_id = $1 then r.to_id else r.from_id end
         where (r.from_id = $1 or r.to_id = $1) and o.deleted_at is null and o.space_id = any($2)
           and core.note_access(o.id, $3, $4, $2) <> 'none'
         order by r.created_at",
    )
    .bind(id)
    .bind(ids)
    .bind(uid)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct AddRelation {
    pub from_id: Uuid,
    pub to_id: Uuid,
    /// blocks | depends_on | bills | designed_for | belongs_to | relates | binds | duplicates
    pub kind: String,
}

async fn relation_add(ctx: &mut Ctx, i: AddRelation) -> CmdResult<Value> {
    assert_note_access(ctx, i.from_id, true).await?;
    assert_note_access(ctx, i.to_id, false).await?;
    if i.from_id == i.to_id {
        return Err(CmdError::invalid("a note cannot relate to itself"));
    }
    add_relation(ctx, i.from_id, i.to_id, &i.kind, None).await?;
    ctx.emit(
        "relation.added",
        Some(i.from_id),
        json!({"to_id": i.to_id, "kind": i.kind}),
    )
    .await?;
    Ok(json!({"ok": true}))
}

async fn relation_remove(ctx: &mut Ctx, i: AddRelation) -> CmdResult<Value> {
    assert_note_access(ctx, i.from_id, true).await?;
    sqlx::query("delete from core.relations where from_id = $1 and to_id = $2 and kind = $3")
        .bind(i.from_id)
        .bind(i.to_id)
        .bind(&i.kind)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "relation.removed",
        Some(i.from_id),
        json!({"to_id": i.to_id, "kind": i.kind}),
    )
    .await?;
    Ok(json!({"ok": true}))
}

// ---------- comments ----------

#[derive(Deserialize, JsonSchema)]
pub struct AddComment {
    pub note_id: Uuid,
    pub body: String,
    /// Inline comment: the exact page text it is about (1-500 chars).
    #[serde(default)]
    pub anchor_text: Option<String>,
    /// Up to 64 characters just before `anchor_text`, to pick the right occurrence.
    #[serde(default)]
    pub anchor_prefix: Option<String>,
    /// Reply to this top-level comment.
    #[serde(default)]
    pub parent_id: Option<Uuid>,
}

const COMMENT_COLS: &str = "c.id, c.note_id, c.author_id, c.author_name, c.body, c.created_at, c.anchor_text,
    c.anchor_prefix, c.parent_id, c.resolved_at, (select name from core.users where id = c.resolved_by) as resolved_by_name";

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct CommentOut {
    pub id: Uuid,
    pub note_id: Uuid,
    pub author_id: Option<Uuid>,
    pub author_name: Option<String>,
    pub body: String,
    pub created_at: DateTime<Utc>,
    pub anchor_text: Option<String>,
    pub anchor_prefix: Option<String>,
    pub parent_id: Option<Uuid>,
    pub resolved_at: Option<DateTime<Utc>>,
    pub resolved_by_name: Option<String>,
}

async fn comment_add(ctx: &mut Ctx, i: AddComment) -> CmdResult<CommentOut> {
    assert_note_access(ctx, i.note_id, false).await?;
    if i.body.trim().is_empty() {
        return Err(CmdError::invalid("comment body is empty"));
    }
    let anchor = i.anchor_text.filter(|a| !a.trim().is_empty());
    if anchor.as_ref().is_some_and(|a| a.chars().count() > 500)
        || i.anchor_prefix
            .as_ref()
            .is_some_and(|p| p.chars().count() > 64)
    {
        return Err(CmdError::invalid(
            "anchor_text is at most 500 and anchor_prefix at most 64 characters",
        ));
    }
    if let Some(parent) = i.parent_id {
        if anchor.is_some() {
            return Err(CmdError::invalid("a reply cannot have its own anchor"));
        }
        let top: Option<bool> = sqlx::query_scalar(
            "select parent_id is null from core.comments where id = $1 and note_id = $2",
        )
        .bind(parent)
        .bind(i.note_id)
        .fetch_optional(&mut *ctx.tx)
        .await?;
        if top != Some(true) {
            return Err(CmdError::invalid(
                "parent_id must be a top-level comment on this note",
            ));
        }
    }
    let id: Uuid = sqlx::query_scalar(
        "insert into core.comments (workspace_id, note_id, author_id, author_name, body, anchor_text, anchor_prefix, parent_id)
         values ($1,$2,$3,(select name from core.users where id = $3),$4,$5,$6,$7) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(i.note_id)
    .bind(ctx.principal.user_id)
    .bind(i.body.trim())
    .bind(&anchor)
    .bind(anchor.as_ref().and(i.anchor_prefix.as_ref()))
    .bind(i.parent_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "comment.added",
        Some(i.note_id),
        json!({"comment_id": id, "inline": anchor.is_some(), "reply": i.parent_id.is_some()}),
    )
    .await?;
    comment_by_id(ctx, id).await
}

async fn comment_by_id(ctx: &mut Ctx, id: Uuid) -> CmdResult<CommentOut> {
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {COMMENT_COLS} from core.comments c where c.id = $1"
    )))
    .bind(id)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct ResolveComment {
    pub id: Uuid,
    /// false reopens it.
    #[serde(default = "yes")]
    pub resolved: bool,
}

fn yes() -> bool {
    true
}

/// Anyone who can read the page can comment, so anyone who can read it can resolve or reopen a thread.
async fn comment_resolve(ctx: &mut Ctx, i: ResolveComment) -> CmdResult<CommentOut> {
    let note: Option<Uuid> =
        sqlx::query_scalar("select note_id from core.comments where id = $1 and parent_id is null")
            .bind(i.id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    let note = note.ok_or_else(|| CmdError::not_found("comment"))?;
    assert_note_access(ctx, note, false).await?;
    sqlx::query(
        "update core.comments set resolved_at = case when $2 then now() end,
                resolved_by = case when $2 then $3::uuid end where id = $1",
    )
    .bind(i.id)
    .bind(i.resolved)
    .bind(ctx.principal.user_id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        if i.resolved {
            "comment.resolved"
        } else {
            "comment.reopened"
        },
        Some(note),
        json!({"comment_id": i.id}),
    )
    .await?;
    comment_by_id(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct ByNote {
    pub note_id: Uuid,
}

async fn comment_list(ctx: &mut Ctx, i: ByNote) -> CmdResult<Vec<CommentOut>> {
    assert_note_access(ctx, i.note_id, false).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {COMMENT_COLS} from core.comments c where c.note_id = $1 order by c.created_at"
    )))
    .bind(i.note_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- search (full text; the ai module adds hybrid/semantic search) ----------

#[derive(Deserialize, JsonSchema)]
pub struct Search {
    pub q: String,
    #[serde(default)]
    pub limit: Option<i64>,
    #[serde(default)]
    pub include_archived: bool,
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct SearchHit {
    pub id: Uuid,
    pub space_id: Uuid,
    pub title: String,
    pub types: Vec<String>,
    pub freshness: String,
    pub verified_at: Option<DateTime<Utc>>,
    pub snippet: String,
    pub rank: f32,
}

async fn search_notes(ctx: &mut Ctx, i: Search) -> CmdResult<Vec<SearchHit>> {
    let ids = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    // Permission filter is applied inside the query, not after (ADR-009).
    Ok(sqlx::query_as(
        "select id, space_id, title, types, freshness, verified_at,
                ts_headline('english', content_md, websearch_to_tsquery('english', $1), 'MaxWords=30,MinWords=10') as snippet,
                (ts_rank(search, websearch_to_tsquery('english', $1)) + similarity(title, $1))::real as rank
         from core.notes
         where deleted_at is null and ($4 or freshness <> 'archived')
           and (space_id = any($2) or exists (select 1 from core.note_permissions sp where sp.note_id = notes.id and sp.user_id = $5))
           and core.note_access(id, $5, $6, $2) <> 'none'
           and (search @@ websearch_to_tsquery('english', $1) or title % $1 or title ilike '%' || $1 || '%')
         order by rank desc limit $3",
    )
    .bind(i.q.trim())
    .bind(ids)
    .bind(i.limit.unwrap_or(20).clamp(1, 100))
    .bind(i.include_archived)
    .bind(uid)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- views (saved CNQL queries) ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateView {
    #[serde(default)]
    pub space_id: Option<Uuid>,
    pub name: String,
    /// table | board | timeline | pipeline | list | calendar
    pub layout: String,
    /// CNQL query, e.g. `project = ENG AND status != Done ORDER BY priority DESC`
    pub query: String,
    #[serde(default)]
    pub options: Option<Value>,
}

#[derive(Serialize, Deserialize, JsonSchema, sqlx::FromRow)]
pub struct ViewOut {
    pub id: Uuid,
    pub space_id: Option<Uuid>,
    pub name: String,
    pub layout: String,
    pub query: String,
    pub options: Value,
}

async fn view_create(ctx: &mut Ctx, i: CreateView) -> CmdResult<ViewOut> {
    Ok(sqlx::query_as(
        "insert into core.views (workspace_id, space_id, name, layout, query, options, created_by)
         values ($1,$2,$3,$4,$5,$6,$7) returning id, space_id, name, layout, query, options",
    )
    .bind(ctx.workspace_id())
    .bind(i.space_id)
    .bind(i.name)
    .bind(i.layout)
    .bind(i.query)
    .bind(i.options.unwrap_or(json!({})))
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListViews {
    #[serde(default)]
    pub space_id: Option<Uuid>,
}

async fn view_list(ctx: &mut Ctx, i: ListViews) -> CmdResult<Vec<ViewOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(
        "select id, space_id, name, layout, query, options from core.views
         where ($1::uuid is null or space_id = $1) and (space_id is null or space_id = any($2)) order by name",
    )
    .bind(i.space_id)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new("space.create", "Create a Space (a container of notes with its own key, types and workflow).", ToolGroup::WriteNotes, handler!(space_create)),
        Command::new("space.list", "List the Spaces you can read.", ToolGroup::Read, handler!(space_list)).core(),
        Command::new("note.create", "Create a note (page). Pages get an owner and a verify-by date (90 days) by default.", ToolGroup::WriteNotes, handler!(note_create)).core(),
        Command::new("note.get", "Read a note with its relations.", ToolGroup::Read, handler!(note_get)).core(),
        Command::new("note.update", "Update a note's title, Markdown content, owner, verify-by date or parent.", ToolGroup::WriteNotes, handler!(note_update)).core(),
        Command::new("note.list", "List notes in a space or under a parent, optionally by type.", ToolGroup::Read, handler!(note_list)).core(),
        Command::new("note.delete", "Delete a note (soft delete, restorable by an admin).", ToolGroup::Destructive, handler!(note_delete)),
        Command::new("note.history", "Event history of a note (append-only audit trail).", ToolGroup::Read, handler!(note_history)),
        Command::new("relation.add", "Link two notes with a typed relation (blocks, depends_on, bills, designed_for, relates...).", ToolGroup::WriteNotes, handler!(relation_add)).core(),
        Command::new("relation.remove", "Remove a typed relation between two notes.", ToolGroup::WriteNotes, handler!(relation_remove)),
        Command::new("comment.add", "Comment on a note. Optional anchor_text (+ anchor_prefix) makes it an inline comment on that page text; parent_id makes it a reply.", ToolGroup::WriteNotes, handler!(comment_add)).core(),
        Command::new("comment.list", "List comments on a note.", ToolGroup::Read, handler!(comment_list)),
        Command::new("comment.resolve", "Resolve (or reopen with resolved=false) a top-level comment thread.", ToolGroup::WriteNotes, handler!(comment_resolve)),
        Command::new("search.notes", "Full-text search across notes you can read; archived pages excluded by default.", ToolGroup::Read, handler!(search_notes)).core(),
        Command::new("view.create", "Save a view (a CNQL query with a layout).", ToolGroup::WriteNotes, handler!(view_create)),
        Command::new("view.list", "List saved views.", ToolGroup::Read, handler!(view_list)),
    ]
}
