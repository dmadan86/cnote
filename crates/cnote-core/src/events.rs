use crate::error::CmdResult;
use crate::principal::Principal;
use chrono::{DateTime, Utc};
use cnote_db::Tx;
use serde::Serialize;
use serde_json::{Value, json};
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, sqlx::FromRow)]
pub struct Event {
    pub id: i64,
    pub workspace_id: Uuid,
    pub note_id: Option<Uuid>,
    pub kind: String,
    pub actor_type: String,
    pub actor_id: Option<String>,
    pub on_behalf_of: Option<Uuid>,
    pub payload: Value,
    pub created_at: DateTime<Utc>,
}

/// Append to the per-note event log and enqueue its dispatch in the same transaction (transactional outbox).
pub async fn emit(
    tx: &mut Tx,
    p: &Principal,
    kind: &str,
    note_id: Option<Uuid>,
    payload: Value,
) -> CmdResult<i64> {
    let (actor_type, actor_id, on_behalf_of) = p.actor();
    let id: i64 = sqlx::query_scalar(
        "insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, on_behalf_of, payload)
         values ($1,$2,$3,$4,$5,$6,$7) returning id",
    )
    .bind(p.workspace_id)
    .bind(note_id)
    .bind(kind)
    .bind(actor_type)
    .bind(actor_id)
    .bind(on_behalf_of)
    .bind(&payload)
    .fetch_one(&mut **tx)
    .await?;
    enqueue(
        tx,
        Some(p.workspace_id),
        "event.dispatch",
        json!({"event_id": id}),
        None,
    )
    .await?;
    Ok(id)
}

pub async fn enqueue(
    tx: &mut Tx,
    workspace_id: Option<Uuid>,
    kind: &str,
    payload: Value,
    run_at: Option<DateTime<Utc>>,
) -> CmdResult<()> {
    sqlx::query("insert into core.jobs (workspace_id, kind, payload, run_at) values ($1,$2,$3,coalesce($4, now()))")
        .bind(workspace_id)
        .bind(kind)
        .bind(payload)
        .bind(run_at)
        .execute(&mut **tx)
        .await?;
    Ok(())
}

/// Internal usage ledger (ADR-011): every meterable event, billed or not.
pub async fn record_usage(
    tx: &mut Tx,
    workspace_id: Uuid,
    kind: &str,
    quantity: i64,
    meta: Value,
) -> CmdResult<()> {
    sqlx::query("insert into core.usage (workspace_id, kind, quantity, meta) values ($1,$2,$3,$4)")
        .bind(workspace_id)
        .bind(kind)
        .bind(quantity)
        .bind(meta)
        .execute(&mut **tx)
        .await?;
    Ok(())
}
