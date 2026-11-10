//! cnote-finance: CRM (contacts, deals) and GST invoicing on a double-entry ledger (ADR-008).

pub mod billing;
pub mod crm;
pub mod gst;
pub mod irn;
pub mod ledger;
pub mod reports;

use chrono::NaiveDate;
use cnote_core::{Module, Principal, Registry};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;

pub fn module() -> Module {
    let mut commands = crm::commands();
    commands.extend(billing::commands());
    commands.extend(reports::commands());
    Module {
        commands,
        workspace_hooks: vec![ledger::seed_chart],
        sweeps: vec![("finance.irn_window", 86_400, |pool, _reg: Arc<Registry>| {
            Box::pin(async move {
                irn_window_sweep(&pool, billing::today_ist())
                    .await
                    .map(|_| ())
            })
        })],
        ..Default::default()
    }
}

/// For workspaces with AATO above Rs 10 crore, warn once per un-reported B2B invoice that is 20+ days old.
/// Returns the number of warnings emitted.
pub async fn irn_window_sweep(pool: &PgPool, today: NaiveDate) -> anyhow::Result<usize> {
    let workspaces: Vec<uuid::Uuid> = sqlx::query_scalar("select core.all_workspace_ids()")
        .fetch_all(pool)
        .await?;
    let mut emitted = 0;
    for ws in workspaces {
        let mut tx = cnote_db::begin_tenant(pool, ws).await?;
        let settings: Value =
            sqlx::query_scalar("select settings from core.workspaces where id = $1")
                .bind(ws)
                .fetch_one(&mut *tx)
                .await?;
        if settings["aato_above_10cr"].as_bool() != Some(true) {
            continue;
        }
        let due: Vec<(uuid::Uuid, Option<String>, NaiveDate)> = sqlx::query_as(
            "select i.note_id, i.number, i.invoice_date from billing.invoices i
             where i.status in ('issued','sent','partly_paid','paid') and i.irn is null and i.buyer_gstin is not null
               and $1::date - i.invoice_date >= $2
               and not exists (select 1 from core.events e where e.note_id = i.note_id and e.kind = 'invoice.irn_window_warning')",
        )
        .bind(today)
        .bind(irn::WARN_DAY as i32)
        .fetch_all(&mut *tx)
        .await?;
        let p = Principal::system(ws);
        for (id, number, date) in due {
            let days = (today - date).num_days();
            let payload = json!({"number": number, "days_elapsed": days, "days_left": 30 - days, "blocked": days >= irn::BLOCK_DAY});
            cnote_core::events::emit(&mut tx, &p, "invoice.irn_window_warning", Some(id), payload)
                .await?;
            emitted += 1;
        }
        tx.commit().await?;
    }
    Ok(emitted)
}
