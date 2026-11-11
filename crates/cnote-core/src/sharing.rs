//! Page restrictions and sharing (Confluence-style) on top of space access. The access rule itself lives in
//! SQL (`core.note_access`, migration 0800) and is enforced by `access::assert_note_access`.

use crate::access::{assert_note_access, readable_space_ids, viewer};
use crate::command::{Command, Ctx, ToolGroup};
use crate::error::{CmdError, CmdResult};
use crate::handler;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

#[derive(Deserialize, JsonSchema)]
pub struct ByNoteId {
    pub note_id: Uuid,
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct Person {
    pub user_id: Uuid,
    pub name: String,
    pub email: String,
    /// view | edit
    pub level: String,
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct InheritedFrom {
    pub id: Uuid,
    pub title: String,
    pub restriction: String,
}

#[derive(Serialize, JsonSchema)]
pub struct Permissions {
    /// none | edit | view
    pub restriction: String,
    /// Nearest ancestor that restricts this page (its restriction applies here too).
    pub inherited_from: Option<InheritedFrom>,
    pub people: Vec<Person>,
    /// True when the caller may change the restriction and the people list.
    pub can_manage: bool,
}

async fn note_permissions(ctx: &mut Ctx, i: ByNoteId) -> CmdResult<Permissions> {
    assert_note_access(ctx, i.note_id, false).await?;
    let can_manage = assert_note_access(ctx, i.note_id, true).await.is_ok();
    let restriction = restriction_of(ctx, i.note_id).await?;
    // Ancestor titles are only shown when the caller can read them.
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    let inherited_from = sqlx::query_as(
        "with recursive up as (
           select p.id, p.parent_id, p.title, p.restriction, 1 d from core.notes n join core.notes p on p.id = n.parent_id where n.id = $1
           union all
           select p.id, p.parent_id, p.title, p.restriction, up.d + 1 from up join core.notes p on p.id = up.parent_id where up.d < 50)
         select id, case when core.note_access(id, $3, $4, $2) = 'none' then '' else title end as title, restriction
         from up where restriction <> 'none' order by d limit 1",
    )
    .bind(i.note_id)
    .bind(spaces)
    .bind(uid)
    .bind(admin)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let people = sqlx::query_as(
        "select p.user_id, u.name, u.email, p.level from core.note_permissions p join core.users u on u.id = p.user_id
         where p.note_id = $1 order by u.name",
    )
    .bind(i.note_id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    Ok(Permissions {
        restriction,
        inherited_from,
        people,
        can_manage,
    })
}

async fn restriction_of(ctx: &mut Ctx, note_id: Uuid) -> CmdResult<String> {
    Ok(
        sqlx::query_scalar("select restriction from core.notes where id = $1")
            .bind(note_id)
            .fetch_one(&mut *ctx.tx)
            .await?,
    )
}

#[derive(Deserialize, JsonSchema)]
pub struct Restrict {
    pub note_id: Uuid,
    /// none = anyone with space access (default) | edit = anyone can view, only listed people can edit | view = only listed people can open it. Sub-pages inherit.
    pub restriction: String,
}

async fn note_restrict(ctx: &mut Ctx, i: Restrict) -> CmdResult<Value> {
    if !matches!(i.restriction.as_str(), "none" | "edit" | "view") {
        return Err(CmdError::invalid("restriction must be none, edit or view"));
    }
    assert_note_access(ctx, i.note_id, true).await?;
    sqlx::query("update core.notes set restriction = $2 where id = $1")
        .bind(i.note_id)
        .bind(&i.restriction)
        .execute(&mut *ctx.tx)
        .await?;
    // The person restricting keeps edit access, so nobody locks themselves out.
    if i.restriction != "none"
        && let Some(u) = ctx.principal.user_id
    {
        upsert(ctx, i.note_id, u, "edit").await?;
    }
    ctx.emit(
        "note.restricted",
        Some(i.note_id),
        json!({"restriction": i.restriction}),
    )
    .await?;
    Ok(json!({"restriction": i.restriction}))
}

async fn upsert(ctx: &mut Ctx, note_id: Uuid, user: Uuid, level: &str) -> CmdResult<()> {
    sqlx::query(
        "insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1,$2,$3,$4,$5)
         on conflict (note_id, user_id) do update set level = excluded.level, granted_by = excluded.granted_by",
    )
    .bind(note_id)
    .bind(ctx.workspace_id())
    .bind(user)
    .bind(level)
    .bind(ctx.principal.user_id)
    .execute(&mut *ctx.tx)
    .await?;
    Ok(())
}

/// A restricted page must always keep someone who can edit it.
async fn assert_editor_left(ctx: &mut Ctx, note_id: Uuid) -> CmdResult<()> {
    let ok: bool = sqlx::query_scalar(
        "select n.restriction = 'none' or exists(select 1 from core.note_permissions p where p.note_id = n.id and p.level = 'edit')
         from core.notes n where n.id = $1",
    )
    .bind(note_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if ok {
        Ok(())
    } else {
        Err(CmdError::conflict(
            "a restricted page needs at least one person who can edit it",
        ))
    }
}

#[derive(Deserialize, JsonSchema)]
pub struct Share {
    pub note_id: Uuid,
    /// Workspace member to share with (give user_id or email).
    #[serde(default)]
    pub user_id: Option<Uuid>,
    #[serde(default)]
    pub email: Option<String>,
    /// view | edit
    pub level: String,
}

async fn note_share(ctx: &mut Ctx, i: Share) -> CmdResult<Value> {
    if !matches!(i.level.as_str(), "view" | "edit") {
        return Err(CmdError::invalid("level must be view or edit"));
    }
    assert_note_access(ctx, i.note_id, true).await?;
    let user: Option<Uuid> = match (i.user_id, i.email.as_deref()) {
        (Some(u), _) => {
            sqlx::query_scalar("select user_id from core.memberships where user_id = $1")
                .bind(u)
                .fetch_optional(&mut *ctx.tx)
                .await?
        }
        (None, Some(e)) => sqlx::query_scalar(
            "select m.user_id from core.memberships m join core.users u on u.id = m.user_id where lower(u.email) = lower($1)",
        )
        .bind(e.trim())
        .fetch_optional(&mut *ctx.tx)
        .await?,
        _ => return Err(CmdError::invalid("give user_id or email")),
    };
    let user = user.ok_or_else(|| CmdError::invalid("not a member of this workspace"))?;
    upsert(ctx, i.note_id, user, &i.level).await?;
    assert_editor_left(ctx, i.note_id).await?;
    ctx.emit(
        "note.shared",
        Some(i.note_id),
        json!({"user_id": user, "level": i.level}),
    )
    .await?;
    Ok(json!({"user_id": user, "level": i.level}))
}

#[derive(Deserialize, JsonSchema)]
pub struct Unshare {
    pub note_id: Uuid,
    pub user_id: Uuid,
}

async fn note_unshare(ctx: &mut Ctx, i: Unshare) -> CmdResult<Value> {
    assert_note_access(ctx, i.note_id, true).await?;
    sqlx::query("delete from core.note_permissions where note_id = $1 and user_id = $2")
        .bind(i.note_id)
        .bind(i.user_id)
        .execute(&mut *ctx.tx)
        .await?;
    assert_editor_left(ctx, i.note_id).await?;
    ctx.emit(
        "note.unshared",
        Some(i.note_id),
        json!({"user_id": i.user_id}),
    )
    .await?;
    Ok(json!({"ok": true}))
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct SharedNote {
    pub id: Uuid,
    pub space_id: Uuid,
    pub title: String,
    pub types: Vec<String>,
    pub level: String,
}

/// Pages shared to the caller that sit in spaces the caller cannot otherwise read.
async fn shared_with_me(ctx: &mut Ctx, _: crate::notes::Empty) -> CmdResult<Vec<SharedNote>> {
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    Ok(sqlx::query_as(
        "select n.id, n.space_id, n.title, n.types, p.level from core.note_permissions p join core.notes n on n.id = p.note_id
         where p.user_id = $1 and n.deleted_at is null and n.space_id <> all($2)
           and core.note_access(n.id, $1, $3, $2) <> 'none'
         order by n.updated_at desc",
    )
    .bind(uid)
    .bind(spaces)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "note.permissions",
            "Who can see and edit a page: its restriction, what it inherits, and the people it is shared with.",
            ToolGroup::Read,
            handler!(note_permissions),
        ),
        Command::new(
            "note.restrict",
            "Restrict a page: none (space rules), edit (only listed people edit) or view (only listed people open it). Sub-pages inherit.",
            ToolGroup::WriteNotes,
            handler!(note_restrict),
        ),
        Command::new(
            "note.share",
            "Share a page with a workspace member as viewer or editor; also gives access to the page even without access to its space.",
            ToolGroup::WriteNotes,
            handler!(note_share),
        ),
        Command::new(
            "note.unshare",
            "Remove a person from a page's sharing list.",
            ToolGroup::WriteNotes,
            handler!(note_unshare),
        ),
        Command::new(
            "note.shared_with_me",
            "Pages shared with you from spaces you cannot otherwise read.",
            ToolGroup::Read,
            handler!(shared_with_me),
        ),
    ]
}
