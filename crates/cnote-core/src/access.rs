//! Space-level access on top of RLS (ADR-002: permissions live on the tree and are inherited; a Type cannot loosen them).
//! Open spaces: every member reads/writes, guests need explicit space membership. Private spaces: explicit membership.
//! Admins and owners see everything.

use crate::command::Ctx;
use crate::error::{CmdError, CmdResult};
use crate::principal::Role;
use uuid::Uuid;

pub async fn assert_space_access(ctx: &mut Ctx, space_id: Uuid, write: bool) -> CmdResult<()> {
    let p = ctx.principal.clone();
    let row: Option<(String, Option<String>)> = sqlx::query_as(
        "select s.visibility, sm.role from core.spaces s
         left join core.space_members sm on sm.space_id = s.id and sm.user_id = $2
         where s.id = $1",
    )
    .bind(space_id)
    .bind(p.user_id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let Some((visibility, space_role)) = row else {
        return Err(CmdError::not_found("space"));
    };
    if p.role >= Role::Admin {
        return Ok(());
    }
    let needs_membership = visibility == "private" || p.role == Role::Guest;
    if needs_membership && space_role.is_none() {
        return Err(CmdError::not_found("space"));
    }
    if write
        && (space_role.as_deref() == Some("viewer")
            || (p.role == Role::Guest && space_role.is_none()))
    {
        return Err(CmdError::forbidden("read-only access to this space"));
    }
    Ok(())
}

/// Ids of spaces the caller may read; use to filter list and search queries in SQL (`= any($n)`).
pub async fn readable_space_ids(ctx: &mut Ctx) -> CmdResult<Vec<Uuid>> {
    let p = ctx.principal.clone();
    Ok(sqlx::query_scalar(
        "select s.id from core.spaces s
         left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1
         where $2 or sm.user_id is not null or (s.visibility = 'open' and $3)",
    )
    .bind(p.user_id)
    .bind(p.role >= Role::Admin)
    .bind(p.role != Role::Guest)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

/// (user, is_admin) binds for `core.note_access(note, uid, is_admin, readable_spaces)`. Add
/// `and core.note_access(n.id, $uid, $admin, $spaces) <> 'none'` to every query that lists notes, next to the
/// `space_id = any($spaces)` filter, so restricted pages are dropped inside SQL.
pub fn viewer(ctx: &Ctx) -> (Option<Uuid>, bool) {
    (ctx.principal.user_id, ctx.principal.role >= Role::Admin)
}

/// Page-level access for the caller: none | view | edit (page restrictions and shares, migration 0800).
pub async fn note_level(ctx: &mut Ctx, note_id: Uuid, spaces: &[Uuid]) -> CmdResult<String> {
    let (uid, admin) = viewer(ctx);
    Ok(sqlx::query_scalar("select core.note_access($1,$2,$3,$4)")
        .bind(note_id)
        .bind(uid)
        .bind(admin)
        .bind(spaces)
        .fetch_one(&mut *ctx.tx)
        .await?)
}

/// True when the page or any ancestor is view-restricted (content that only listed people may open).
pub async fn is_view_restricted(ctx: &mut Ctx, note_id: Uuid) -> CmdResult<bool> {
    Ok(sqlx::query_scalar(
        "with recursive c as (select id, parent_id, restriction, 1 d from core.notes where id = $1
           union all select p.id, p.parent_id, p.restriction, c.d + 1 from core.notes p join c on p.id = c.parent_id where c.d < 50)
         select exists(select 1 from c where restriction = 'view')",
    )
    .bind(note_id)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

/// Keep only the notes the caller may read (for result sets assembled outside one SQL query).
pub async fn readable_notes(ctx: &mut Ctx, ids: &[Uuid]) -> CmdResult<Vec<Uuid>> {
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    Ok(sqlx::query_scalar(
        "select id from unnest($1::uuid[]) id where core.note_access(id, $2, $3, $4) <> 'none'",
    )
    .bind(ids)
    .bind(uid)
    .bind(admin)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

/// Load a note's space and assert access in one step. Returns the space id. A page the caller cannot read is
/// not found; a read-only or restricted page is forbidden for writes. A page shared to a caller without space
/// access is readable (writable at level edit) without any other access to that space.
pub async fn assert_note_access(ctx: &mut Ctx, note_id: Uuid, write: bool) -> CmdResult<Uuid> {
    let space: Option<Uuid> =
        sqlx::query_scalar("select space_id from core.notes where id = $1 and deleted_at is null")
            .bind(note_id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    let space = space.ok_or_else(|| CmdError::not_found("note"))?;
    let spaces = readable_space_ids(ctx).await?;
    let level = note_level(ctx, note_id, &spaces).await?;
    if level == "none" {
        return Err(CmdError::not_found("note"));
    }
    if spaces.contains(&space) {
        assert_space_access(ctx, space, write).await?;
    }
    if write && level != "edit" {
        return Err(CmdError::forbidden("you can only view this page"));
    }
    Ok(space)
}
