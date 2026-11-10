//! Double-entry ledger (ADR-008). Entries are append-only; a deferred DB trigger rejects any unbalanced entry at commit.

use cnote_core::{CmdError, CmdResult, Ctx};
use cnote_db::Tx;
use futures::future::BoxFuture;
use uuid::Uuid;

pub const RECEIVABLES: &str = "1100";
pub const BANK: &str = "1200";
pub const CGST_OUT: &str = "2201";
pub const SGST_OUT: &str = "2202";
pub const IGST_OUT: &str = "2203";
pub const REVENUE: &str = "4000";
pub const BAD_DEBT: &str = "5100";

const CHART: [(&str, &str, &str); 7] = [
    (RECEIVABLES, "Accounts receivable", "asset"),
    (BANK, "Bank / UPI clearing", "asset"),
    (CGST_OUT, "CGST output", "liability"),
    (SGST_OUT, "SGST output", "liability"),
    (IGST_OUT, "IGST output", "liability"),
    (REVENUE, "Sales revenue", "revenue"),
    (BAD_DEBT, "Bad debt expense", "expense"),
];

/// Workspace hook: seed the chart of accounts.
pub fn seed_chart(tx: &mut Tx, ws: Uuid, _owner: Uuid) -> BoxFuture<'_, anyhow::Result<()>> {
    Box::pin(async move {
        for (code, name, kind) in CHART {
            sqlx::query("insert into billing.accounts (workspace_id, code, name, kind) values ($1,$2,$3,$4) on conflict do nothing")
                .bind(ws)
                .bind(code)
                .bind(name)
                .bind(kind)
                .execute(&mut **tx)
                .await?;
        }
        Ok(())
    })
}

/// (account code, debit, credit) in paise.
pub type Leg = (&'static str, i64, i64);

/// Post one balanced journal entry. Zero legs are dropped.
pub async fn post(
    ctx: &mut Ctx,
    source_type: &str,
    source_id: Uuid,
    memo: &str,
    legs: &[Leg],
) -> CmdResult<Uuid> {
    let (d, c) = legs
        .iter()
        .fold((0i64, 0i64), |(d, c), l| (d + l.1, c + l.2));
    if d != c {
        return Err(CmdError::internal(format!(
            "unbalanced entry: debits {d} credits {c}"
        )));
    }
    let ws = ctx.workspace_id();
    let entry: Uuid = sqlx::query_scalar(
        "insert into billing.journal_entries (workspace_id, source_type, source_id, memo) values ($1,$2,$3,$4) returning id",
    )
    .bind(ws)
    .bind(source_type)
    .bind(source_id)
    .bind(memo)
    .fetch_one(&mut *ctx.tx)
    .await?;
    for (code, debit, credit) in legs.iter().filter(|l| l.1 != 0 || l.2 != 0) {
        sqlx::query("insert into billing.journal_lines (workspace_id, entry_id, account_code, debit_minor, credit_minor) values ($1,$2,$3,$4,$5)")
            .bind(ws)
            .bind(entry)
            .bind(code)
            .bind(debit)
            .bind(credit)
            .execute(&mut *ctx.tx)
            .await?;
    }
    Ok(entry)
}

/// Legs for a sale (or, with `reverse`, its credit-note reversal): receivable against revenue and output taxes.
pub fn sale_legs(subtotal: i64, cgst: i64, sgst: i64, igst: i64, reverse: bool) -> Vec<Leg> {
    let total = subtotal + cgst + sgst + igst;
    let side = |code, amt| {
        if reverse {
            (code, amt, 0)
        } else {
            (code, 0, amt)
        }
    };
    let recv = if reverse {
        (RECEIVABLES, 0, total)
    } else {
        (RECEIVABLES, total, 0)
    };
    vec![
        recv,
        side(REVENUE, subtotal),
        side(CGST_OUT, cgst),
        side(SGST_OUT, sgst),
        side(IGST_OUT, igst),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sale_legs_balance_both_ways() {
        for rev in [false, true] {
            let l = sale_legs(10_000, 900, 901, 0, rev);
            assert_eq!(
                l.iter().map(|x| x.1).sum::<i64>(),
                l.iter().map(|x| x.2).sum::<i64>()
            );
        }
    }
}
