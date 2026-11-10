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

/// Load a note's space and assert access in one step. Returns the space id.
pub async fn assert_note_access(ctx: &mut Ctx, note_id: Uuid, write: bool) -> CmdResult<Uuid> {
    let space: Option<Uuid> =
        sqlx::query_scalar("select space_id from core.notes where id = $1 and deleted_at is null")
            .bind(note_id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    let space = space.ok_or_else(|| CmdError::not_found("note"))?;
    assert_space_access(ctx, space, write).await?;
    Ok(space)
}
