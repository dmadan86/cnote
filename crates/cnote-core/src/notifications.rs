//! @mentions: a page link `/w/<ws>/people/<user>` newly present in a page's Markdown notifies that person
//! in-app (core.notifications) and by email (event `mention.created`, plain text over SMTP_URL).

use crate::command::{Command, Ctx, ToolGroup};
use crate::error::{CmdError, CmdResult};
use crate::events::Event;
use crate::handler;
use crate::notes::Empty;
use chrono::{DateTime, Utc};
use regex::Regex;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::HashSet;
use std::sync::LazyLock;
use uuid::Uuid;

static MENTION: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"\]\(/w/([0-9a-fA-F-]{36})/people/([0-9a-fA-F-]{36})\)").expect("static regex")
});

fn mentions(md: &str, ws: Uuid) -> HashSet<Uuid> {
    MENTION
        .captures_iter(md)
        .filter_map(|c| Some((c[1].parse::<Uuid>().ok()?, c[2].parse::<Uuid>().ok()?)))
        .filter(|(w, _)| *w == ws)
        .map(|(_, u)| u)
        .collect()
}

/// Called by note.update after a save: notify people mentioned in `new` but not in `old`.
pub async fn notify_mentions(
    ctx: &mut Ctx,
    note_id: Uuid,
    title: &str,
    old: &str,
    new: &str,
) -> CmdResult<()> {
    let ws = ctx.workspace_id();
    let old = mentions(old, ws);
    for user in mentions(new, ws).difference(&old) {
        if ctx.principal.user_id == Some(*user) {
            continue;
        }
        // Member of this workspace who can read the page, judged with their own role and space access.
        let can_read: bool = sqlx::query_scalar(
            "select exists(select 1 from core.memberships m where m.user_id = $1 and core.note_access($2, $1,
               m.role in ('admin','owner'),
               array(select s.id from core.spaces s
                     left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1
                     where m.role in ('admin','owner') or sm.user_id is not null
                        or (s.visibility = 'open' and m.role <> 'guest'))) <> 'none')",
        )
        .bind(user)
        .bind(note_id)
        .fetch_one(&mut *ctx.tx)
        .await?;
        if !can_read {
            continue;
        }
        let id: Uuid = sqlx::query_scalar(
            "insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title)
             values ($1,$2,'mention',$3,$4,$5) returning id",
        )
        .bind(ws)
        .bind(user)
        .bind(note_id)
        .bind(ctx.principal.user_id)
        .bind(title)
        .fetch_one(&mut *ctx.tx)
        .await?;
        ctx.emit(
            "mention.created",
            Some(note_id),
            json!({"notification_id": id}),
        )
        .await?;
    }
    Ok(())
}

fn me(ctx: &Ctx) -> CmdResult<Uuid> {
    ctx.principal
        .user_id
        .ok_or_else(|| CmdError::forbidden("notifications belong to people"))
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct NotificationOut {
    pub id: Uuid,
    pub kind: String,
    pub note_id: Uuid,
    pub note_title: String,
    pub actor_name: Option<String>,
    pub created_at: DateTime<Utc>,
    pub read_at: Option<DateTime<Utc>>,
}

async fn list(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<NotificationOut>> {
    let uid = me(ctx)?;
    Ok(sqlx::query_as(
        "select n.id, n.kind, n.note_id, n.title as note_title, u.name as actor_name, n.created_at, n.read_at
         from core.notifications n left join core.users u on u.id = n.actor_user_id
         where n.user_id = $1 order by n.created_at desc limit 50",
    )
    .bind(uid)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct MarkRead {
    /// Omit to mark all of your notifications read.
    #[serde(default)]
    pub id: Option<Uuid>,
}

async fn read(ctx: &mut Ctx, i: MarkRead) -> CmdResult<Value> {
    let uid = me(ctx)?;
    let n = sqlx::query(
        "update core.notifications set read_at = now()
         where user_id = $1 and read_at is null and ($2::uuid is null or id = $2)",
    )
    .bind(uid)
    .bind(i.id)
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    Ok(json!({"updated": n}))
}

async fn unread_count(ctx: &mut Ctx, _: Empty) -> CmdResult<Value> {
    let uid = me(ctx)?;
    let n: i64 = sqlx::query_scalar(
        "select count(*) from core.notifications where user_id = $1 and read_at is null",
    )
    .bind(uid)
    .fetch_one(&mut *ctx.tx)
    .await?;
    Ok(json!({"count": n}))
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "notification.list",
            "List your 50 newest notifications.",
            ToolGroup::Read,
            handler!(list),
        ),
        Command::new(
            "notification.read",
            "Mark one notification (or all, when id is omitted) read. Only affects your own.",
            ToolGroup::Read,
            handler!(read),
        ),
        Command::new(
            "notification.unread_count",
            "Number of your unread notifications.",
            ToolGroup::Read,
            handler!(unread_count),
        ),
    ]
}

/// Email for `mention.created`. Skipped when SMTP_URL is unset; a send failure is logged, never retried
/// (the in-app notification already exists and a retry could duplicate the email).
pub async fn email_handler(ctx: &mut Ctx, ev: &Event) -> CmdResult<()> {
    let Ok(smtp) = std::env::var("SMTP_URL") else {
        tracing::debug!("SMTP_URL unset; skipping mention email");
        return Ok(());
    };
    let Some(nid) = ev.payload["notification_id"]
        .as_str()
        .and_then(|s| s.parse::<Uuid>().ok())
    else {
        return Ok(());
    };
    let row: Option<(String, String, Option<String>, Uuid)> = sqlx::query_as(
        "select u.email, n.title, a.name, n.note_id from core.notifications n
         join core.users u on u.id = n.user_id left join core.users a on a.id = n.actor_user_id where n.id = $1",
    )
    .bind(nid)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let Some((to, title, actor, note_id)) = row else {
        return Ok(());
    };
    let base = ["PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"]
        .iter()
        .find_map(|k| std::env::var(k).ok().filter(|v| !v.is_empty()))
        .unwrap_or_else(|| "http://localhost:3000".into());
    let body = format!(
        "{} mentioned you in {}\n\n{}/w/{}/n/{}\n",
        actor.as_deref().unwrap_or("Someone"),
        title,
        base.trim_end_matches('/'),
        ev.workspace_id,
        note_id
    );
    let from = std::env::var("EMAIL_FROM").unwrap_or_default();
    if let Err(e) = send(
        &smtp,
        &from,
        &to,
        &format!("You were mentioned in {title}"),
        body,
    )
    .await
    {
        tracing::warn!(error = %e, "mention email failed");
    }
    Ok(())
}

async fn send(smtp: &str, from: &str, to: &str, subject: &str, body: String) -> anyhow::Result<()> {
    use lettre::{AsyncSmtpTransport, AsyncTransport, Message, Tokio1Executor};
    let msg = Message::builder()
        .from(from.parse()?)
        .to(to.parse()?)
        .subject(subject)
        .body(body)?;
    AsyncSmtpTransport::<Tokio1Executor>::from_url(smtp)?
        .build()
        .send(msg)
        .await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_mentions_for_this_workspace_only() {
        let (ws, other, u) = (Uuid::new_v4(), Uuid::new_v4(), Uuid::new_v4());
        let md = format!("hi [@A](/w/{ws}/people/{u}) and [@B](/w/{other}/people/{u})");
        assert_eq!(mentions(&md, ws), HashSet::from([u]));
    }
}
