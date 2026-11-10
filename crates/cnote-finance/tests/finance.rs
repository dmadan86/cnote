//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use chrono::Duration;
use cnote_core::auth;
use cnote_core::{ErrorCode, ExecOptions, Principal, Registry};
use cnote_finance::billing::today_ist;
use cnote_finance::gst::complete_gstin;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

struct Env {
    pool: PgPool,
    reg: Arc<Registry>,
}

struct Tenant {
    user: Principal,
    space: Uuid,
    /// customer in Karnataka (29) with a GSTIN
    contact: Uuid,
}

impl Env {
    async fn new() -> Self {
        let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
        let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 8)
            .await
            .unwrap();
        let reg = Registry::new(vec![cnote_core::module(), cnote_finance::module()]);
        Self { pool, reg }
    }

    async fn run(
        &self,
        p: &Principal,
        cmd: &str,
        input: Value,
    ) -> Result<Value, cnote_core::CmdError> {
        self.reg
            .execute(&self.pool, cmd, input, p, &ExecOptions::default())
            .await
    }

    async fn ok(&self, p: &Principal, cmd: &str, input: Value) -> Value {
        self.run(p, cmd, input)
            .await
            .unwrap_or_else(|e| panic!("{cmd}: {e}"))
    }

    /// New workspace whose seller is in Karnataka (29); `aato` toggles IRN window enforcement.
    async fn tenant(&self, aato: bool) -> Tenant {
        let email = format!("{}@test.local", Uuid::new_v4());
        let user = auth::sign_up(&self.pool, &email, "Tess", &Uuid::new_v4().to_string())
            .await
            .unwrap();
        let ws = auth::create_workspace(&self.pool, &self.reg, user, "Acme")
            .await
            .unwrap();
        let p = auth::user_principal(&self.pool, user, ws).await.unwrap();
        self.ok(
            &p,
            "workspace.update",
            json!({"settings": {"gstin": complete_gstin("29ABCDE1234F1Z"), "state_code": "29", "aato_above_10cr": aato}}),
        )
        .await;
        let space = self
            .ok(&p, "space.create", json!({"key": "FIN", "name": "Finance"}))
            .await["id"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        let contact = self.contact(&p, space, "29").await;
        Tenant {
            user: p,
            space,
            contact,
        }
    }

    async fn contact(&self, p: &Principal, space: Uuid, state: &str) -> Uuid {
        let c = self
            .ok(
                p,
                "contact.create",
                json!({"space_id": space, "name": format!("Buyer {state}"), "kind": "company",
                       "gstin": complete_gstin(&format!("{state}AABCT1332L1Z"))}),
            )
            .await;
        assert_eq!(c["state_code"], state, "state derived from GSTIN");
        c["id"].as_str().unwrap().parse().unwrap()
    }

    async fn draft(&self, t: &Tenant, contact: Uuid, date_offset_days: i64) -> Uuid {
        let date = today_ist() + Duration::days(date_offset_days);
        let d = self
            .ok(
                &t.user,
                "invoice.create_draft",
                json!({"space_id": t.space, "contact_id": contact, "invoice_date": date,
                       "lines": [
                         {"description": "Consulting", "hsn_sac": "998314", "quantity": 2.5, "unit_price_minor": 40_001},
                         {"description": "Hosting", "hsn_sac": "998315", "unit_price_minor": 10_005, "gst_rate_bp": 500}]}),
            )
            .await;
        d["id"].as_str().unwrap().parse().unwrap()
    }

    async fn issue(&self, t: &Tenant, id: Uuid) -> Value {
        self.ok(&t.user, "invoice.issue", json!({"id": id})).await
    }

    async fn trial_balance(&self, p: &Principal) -> Value {
        self.ok(p, "ledger.trial_balance", json!({})).await
    }

    /// Run raw SQL inside the tenant (what a buggy deploy or a DBA could attempt).
    async fn raw(&self, ws: Uuid, sql: &str) -> Result<(), sqlx::Error> {
        let mut tx = cnote_db::begin_tenant(&self.pool, ws).await?;
        sqlx::query(sqlx::AssertSqlSafe(sql.to_string()))
            .execute(&mut *tx)
            .await?;
        tx.commit().await
    }
}

fn balance(tb: &Value, code: &str) -> i64 {
    tb["accounts"]
        .as_array()
        .unwrap()
        .iter()
        .find(|a| a["code"] == code)
        .map(|a| a["balance_minor"].as_i64().unwrap())
        .unwrap_or_else(|| panic!("no account {code}"))
}

async fn events(env: &Env, p: &Principal, note: Uuid) -> Vec<String> {
    env.ok(p, "note.history", json!({"id": note}))
        .await
        .as_array()
        .unwrap()
        .iter()
        .map(|e| e["kind"].as_str().unwrap().to_string())
        .collect()
}

#[tokio::test]
async fn issue_posts_a_balanced_entry_and_trial_balance_is_zero() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let id = env.draft(&t, t.contact, 0).await;
    let inv = env.issue(&t, id).await;

    // 2.5 x 400.01 = 1000.025 -> 100003 paise (half-up); 10005 @5%
    assert_eq!(inv["status"], "issued");
    let fy = cnote_finance::gst::financial_year(today_ist());
    assert_eq!(inv["number"], format!("INV/{fy}/0001"));
    assert_eq!(inv["subtotal_minor"], 100_003 + 10_005);
    // intra-state: 18% of 100003 = 18000.54 -> 18001 (9000 + 9001); 5% of 10005 = 500.25 -> 500 (250 + 250)
    assert_eq!(inv["cgst_minor"], 9_000 + 250);
    assert_eq!(inv["sgst_minor"], 9_001 + 250);
    assert_eq!(inv["igst_minor"], 0);
    let total = inv["total_minor"].as_i64().unwrap();
    assert_eq!(total, 110_008 + 18_001 + 500);
    assert_eq!(inv["supplier_state"], "29");
    assert!(inv["lines"][0]["cgst_minor"].as_i64().unwrap() > 0);

    let tb = env.trial_balance(&t.user).await;
    assert_eq!(tb["net_minor"], 0);
    assert_eq!(tb["total_debit_minor"], tb["total_credit_minor"]);
    assert_eq!(balance(&tb, "1100"), total);
    assert_eq!(balance(&tb, "4000"), -110_008);
    assert_eq!(balance(&tb, "2201"), -9_250);
    assert_eq!(balance(&tb, "2203"), 0);
    assert!(
        events(&env, &t.user, id)
            .await
            .contains(&"invoice.issued".to_string())
    );

    // inter-state sale uses IGST only
    let mh = env.contact(&t.user, t.space, "27").await;
    let id2 = env.draft(&t, mh, 0).await;
    let inv2 = env.issue(&t, id2).await;
    assert_eq!(inv2["number"], format!("INV/{fy}/0002"));
    assert_eq!(
        (
            inv2["cgst_minor"].as_i64().unwrap(),
            inv2["igst_minor"].as_i64().unwrap()
        ),
        (0, 18_001 + 500)
    );
    assert_eq!(env.trial_balance(&t.user).await["net_minor"], 0);

    // issuing twice is refused
    let e = env
        .run(&t.user, "invoice.issue", json!({"id": id}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
}

#[tokio::test]
async fn database_rejects_unbalanced_entries() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let ws = t.user.workspace_id;
    let mut tx = cnote_db::begin_tenant(&env.pool, ws).await.unwrap();
    let entry: Uuid = sqlx::query_scalar(
        "insert into billing.journal_entries (workspace_id, source_type, source_id) values ($1,'invoice',$2) returning id",
    )
    .bind(ws)
    .bind(Uuid::new_v4())
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    sqlx::query("insert into billing.journal_lines (workspace_id, entry_id, account_code, debit_minor) values ($1,$2,'1100',100)")
        .bind(ws)
        .bind(entry)
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("insert into billing.journal_lines (workspace_id, entry_id, account_code, credit_minor) values ($1,$2,'4000',99)")
        .bind(ws)
        .bind(entry)
        .execute(&mut *tx)
        .await
        .unwrap();
    let err = tx.commit().await.unwrap_err().to_string();
    assert!(err.contains("does not balance"), "{err}");

    // an entry with no lines is rejected too
    let mut tx = cnote_db::begin_tenant(&env.pool, ws).await.unwrap();
    sqlx::query("insert into billing.journal_entries (workspace_id, source_type, source_id) values ($1,'invoice',$2)")
        .bind(ws)
        .bind(Uuid::new_v4())
        .execute(&mut *tx)
        .await
        .unwrap();
    assert!(tx.commit().await.is_err());
}

#[tokio::test]
async fn issued_invoices_are_immutable_in_the_database() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let ws = t.user.workspace_id;
    let id = env.draft(&t, t.contact, 0).await;

    // drafts can be edited, through the command...
    let upd = env
        .ok(
            &t.user,
            "invoice.update_draft",
            json!({"id": id, "lines": [{"description": "Only", "unit_price_minor": 5000}]}),
        )
        .await;
    assert_eq!(upd["subtotal_minor"], 5000);
    env.issue(&t, id).await;
    // ...and not after issue
    let e = env
        .run(
            &t.user,
            "invoice.update_draft",
            json!({"id": id, "contact_id": t.contact}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);

    for sql in [
        format!("update billing.invoices set subtotal_minor = 1 where note_id = '{id}'"),
        format!("update billing.invoices set number = 'X' where note_id = '{id}'"),
        format!("update billing.invoices set status = 'draft' where note_id = '{id}'"),
        format!("delete from billing.invoices where note_id = '{id}'"),
        format!("update billing.invoice_lines set unit_price_minor = 1 where invoice_id = '{id}'"),
        format!("delete from billing.invoice_lines where invoice_id = '{id}'"),
    ] {
        let err = env.raw(ws, &sql).await.unwrap_err().to_string();
        assert!(
            err.contains("immutable") || err.contains("cannot be deleted"),
            "{sql}: {err}"
        );
    }
    // settlement/IRN fields remain writable
    env.raw(
        ws,
        &format!("update billing.invoices set status = 'sent' where note_id = '{id}'"),
    )
    .await
    .unwrap();
    // the journal is append-only too
    assert!(
        env.raw(ws, "delete from billing.journal_lines")
            .await
            .is_err()
    );
}

#[tokio::test]
async fn partial_then_full_payment() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let id = env.draft(&t, t.contact, 0).await;
    let total = env.issue(&t, id).await["total_minor"].as_i64().unwrap();

    let p1 = env
        .ok(
            &t.user,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": 50_000, "reference": "UTR1"}),
        )
        .await;
    assert_eq!(p1["status"], "partly_paid");
    assert_eq!(p1["outstanding_minor"], total - 50_000);

    let over = env
        .run(
            &t.user,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": total}),
        )
        .await
        .unwrap_err();
    assert_eq!(over.code, ErrorCode::Invalid);
    let zero = env
        .run(
            &t.user,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": 0}),
        )
        .await
        .unwrap_err();
    assert_eq!(zero.code, ErrorCode::Invalid);

    let p2 = env
        .ok(
            &t.user,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": total - 50_000, "method": "bank"}),
        )
        .await;
    assert_eq!(p2["status"], "paid");
    assert_eq!(p2["payments"].as_array().unwrap().len(), 2);
    let ev = events(&env, &t.user, id).await;
    assert_eq!(ev.iter().filter(|k| *k == "invoice.paid").count(), 1);

    let tb = env.trial_balance(&t.user).await;
    assert_eq!(balance(&tb, "1100"), 0);
    assert_eq!(balance(&tb, "1200"), total);
    assert_eq!(tb["net_minor"], 0);
    // a paid invoice takes no more money
    let e = env
        .run(
            &t.user,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": 1}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
}

#[tokio::test]
async fn credit_notes_reverse_the_ledger() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let id = env.draft(&t, t.contact, 0).await;
    let total = env.issue(&t, id).await["total_minor"].as_i64().unwrap();

    // partial: credit 10 x 100.00 @18% = 1180.00
    let cn = env
        .ok(&t.user, "credit_note.create", json!({"invoice_id": id, "reason": "Discount",
            "lines": [{"description": "Consulting discount", "quantity": 10, "unit_price_minor": 10_000}]}))
        .await;
    assert_eq!(cn["kind"], "credit_note");
    assert_eq!(cn["total_minor"], 118_000);
    assert!(cn["number"].as_str().unwrap().starts_with("CN/"));
    let inv = env.ok(&t.user, "invoice.get", json!({"id": id})).await;
    assert_eq!(inv["outstanding_minor"], total - 118_000);
    let tb = env.trial_balance(&t.user).await;
    assert_eq!(balance(&tb, "1100"), total - 118_000);
    assert_eq!(tb["net_minor"], 0);

    // a credit note cannot exceed what is still owed
    let e = env
        .run(
            &t.user,
            "credit_note.create",
            json!({"invoice_id": id, "reason": "x",
            "lines": [{"description": "Too much", "unit_price_minor": total}]}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
    // an invoice with credits cannot be cancelled wholesale
    let e = env
        .run(
            &t.user,
            "invoice.cancel",
            json!({"id": id, "reason": "oops"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);

    // full cancel of a fresh invoice
    let id2 = env.draft(&t, t.contact, 0).await;
    let t2 = env.issue(&t, id2).await["total_minor"].as_i64().unwrap();
    let before = balance(&env.trial_balance(&t.user).await, "1100");
    let cancelled = env
        .ok(
            &t.user,
            "invoice.cancel",
            json!({"id": id2, "reason": "Duplicate"}),
        )
        .await;
    assert_eq!(cancelled["status"], "cancelled");
    let tb = env.trial_balance(&t.user).await;
    assert_eq!(balance(&tb, "1100"), before - t2);
    assert_eq!(tb["net_minor"], 0);
    // net taxable = two invoices - the partial credit note - the cancellation credit note
    let summary = env
        .ok(
            &t.user,
            "report.gst_summary",
            json!({"from": today_ist() - Duration::days(1), "to": today_ist() + Duration::days(1)}),
        )
        .await;
    assert_eq!(
        summary["totals"]["taxable_minor"],
        110_008 + 110_008 - 100_000 - 110_008
    );
    // payments on a cancelled invoice are refused
    let e = env
        .run(
            &t.user,
            "payment.record",
            json!({"invoice_id": id2, "amount_minor": 1}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
}

#[tokio::test]
async fn write_off_and_reports() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let old = env.draft(&t, t.contact, -75).await;
    let recent = env.draft(&t, t.contact, -5).await;
    let t_old = env.issue(&t, old).await["total_minor"].as_i64().unwrap();
    let t_new = env.issue(&t, recent).await["total_minor"].as_i64().unwrap();
    env.ok(
        &t.user,
        "payment.record",
        json!({"invoice_id": recent, "amount_minor": 1000}),
    )
    .await;

    let ag = env
        .ok(&t.user, "report.receivables_ageing", json!({}))
        .await;
    let amt = |b: &str| {
        ag["buckets"]
            .as_array()
            .unwrap()
            .iter()
            .find(|x| x["bucket"] == b)
            .unwrap()["outstanding_minor"]
            .as_i64()
            .unwrap()
    };
    assert_eq!(
        (amt("0-30"), amt("61-90"), amt("31-60"), amt("90+")),
        (t_new - 1000, t_old, 0, 0)
    );
    assert_eq!(ag["total_minor"], t_old + t_new - 1000);

    let wo = env
        .ok(
            &t.user,
            "invoice.write_off",
            json!({"id": old, "reason": "Customer closed"}),
        )
        .await;
    assert_eq!(wo["outstanding_minor"], 0);
    assert_eq!(wo["status"], "paid");
    let tb = env.trial_balance(&t.user).await;
    assert_eq!(balance(&tb, "5100"), t_old);
    assert_eq!(tb["net_minor"], 0);

    // GSTR-1: both are B2B to the same GSTIN
    let period = today_ist().format("%Y-%m").to_string();
    let this_month = env
        .ok(&t.user, "report.gstr1", json!({"period": period}))
        .await;
    assert!(this_month["gstin"].is_string());
    assert!(this_month["fp"].as_str().unwrap().len() == 6);

    // Tally XML covers invoices, receipts and the write-off journal
    let xml = env
        .ok(
            &t.user,
            "export.tally",
            json!({"from": today_ist() - Duration::days(1), "to": today_ist() + Duration::days(1)}),
        )
        .await;
    let x = xml["xml"].as_str().unwrap();
    assert_eq!(xml["vouchers"], 4);
    assert!(
        x.contains("VCHTYPE=\"Sales\"")
            && x.contains("VCHTYPE=\"Receipt\"")
            && x.contains("VCHTYPE=\"Journal\"")
    );
    assert!(x.contains("<LEDGERNAME>Accounts receivable</LEDGERNAME>"));
}

#[tokio::test]
async fn gstr1_splits_b2b_and_b2c() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let consumer = env
        .ok(
            &t.user,
            "contact.create",
            json!({"space_id": t.space, "name": "Walk-in", "state_code": "29"}),
        )
        .await["id"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    let b2b = env.draft(&t, t.contact, 0).await;
    let b2c = env.draft(&t, consumer, 0).await;
    env.issue(&t, b2b).await;
    env.issue(&t, b2c).await;
    let r = env
        .ok(
            &t.user,
            "report.gstr1",
            json!({"period": today_ist().format("%Y-%m").to_string()}),
        )
        .await;
    assert_eq!(r["b2b"].as_array().unwrap().len(), 1);
    assert_eq!(r["b2b"][0]["inv"].as_array().unwrap().len(), 1);
    assert_eq!(
        r["b2b"][0]["inv"][0]["itms"].as_array().unwrap().len(),
        2,
        "one item per rate"
    );
    let b2cs = r["b2cs"].as_array().unwrap();
    assert_eq!(b2cs.len(), 2);
    assert!(
        b2cs.iter()
            .all(|x| x["sply_ty"] == "INTRA" && x["iamt"].as_f64() == Some(0.0))
    );
    let s = env
        .ok(
            &t.user,
            "report.gst_summary",
            json!({"from": today_ist(), "to": today_ist()}),
        )
        .await;
    assert_eq!(s["rows"].as_array().unwrap().len(), 2);
}

#[tokio::test]
async fn irn_window_is_enforced_for_large_taxpayers() {
    let env = Env::new().await;
    let t = env.tenant(true).await;

    // day 20: allowed with a warning; deterministic IRN; the GSP is called once
    let d20 = env.draft(&t, t.contact, -20).await;
    let inv = env.issue(&t, d20).await;
    let r = env
        .ok(&t.user, "invoice.generate_irn", json!({"id": d20}))
        .await;
    assert!(r["warning"].as_str().unwrap().contains("10 day"));
    let irn = r["invoice"]["irn"].as_str().unwrap();
    assert_eq!(irn.len(), 64);
    assert_eq!(r["invoice"]["status"], "irn_generated");
    assert!(r["invoice"]["signed_qr"].is_string() && r["invoice"]["ack_no"].is_string());
    let again = env
        .run(&t.user, "invoice.generate_irn", json!({"id": d20}))
        .await
        .unwrap_err();
    assert_eq!(again.code, ErrorCode::Conflict);
    let _ = inv;

    // day 29: blocked
    let d29 = env.draft(&t, t.contact, -29).await;
    env.issue(&t, d29).await;
    let e = env
        .run(&t.user, "invoice.generate_irn", json!({"id": d29}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
    assert!(e.message.contains("window"), "{}", e.message);

    // the daily sweep warns once about the un-reported day-29 invoice
    let n = cnote_finance::irn_window_sweep(&env.pool, today_ist())
        .await
        .unwrap();
    assert!(n >= 1);
    cnote_finance::irn_window_sweep(&env.pool, today_ist())
        .await
        .unwrap();
    let ev = events(&env, &t.user, d29).await;
    assert_eq!(
        ev.iter()
            .filter(|k| *k == "invoice.irn_window_warning")
            .count(),
        1
    );
    assert!(
        !events(&env, &t.user, d20)
            .await
            .contains(&"invoice.irn_window_warning".to_string())
    );

    // cancel within 24h; not after
    let c = env
        .ok(
            &t.user,
            "invoice.cancel_irn",
            json!({"id": d20, "reason": "wrong buyer"}),
        )
        .await;
    assert!(c["irn_cancelled_at"].is_string());
    assert_eq!(c["status"], "issued");
    let twice = env
        .run(
            &t.user,
            "invoice.cancel_irn",
            json!({"id": d20, "reason": "x"}),
        )
        .await
        .unwrap_err();
    assert_eq!(twice.code, ErrorCode::Conflict);

    let fresh = env.draft(&t, t.contact, 0).await;
    env.issue(&t, fresh).await;
    env.ok(&t.user, "invoice.generate_irn", json!({"id": fresh}))
        .await;
    // an invoice with a live IRN cannot be cancelled before the IRN is
    let e = env
        .run(
            &t.user,
            "invoice.cancel",
            json!({"id": fresh, "reason": "x"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
    env.raw(
        t.user.workspace_id,
        &format!("update billing.invoices set ack_date = now() - interval '25 hours' where note_id = '{fresh}'"),
    )
    .await
    .unwrap();
    let late = env
        .run(
            &t.user,
            "invoice.cancel_irn",
            json!({"id": fresh, "reason": "x"}),
        )
        .await
        .unwrap_err();
    assert_eq!(late.code, ErrorCode::Conflict);
    assert!(late.message.contains("24 hours"));
}

#[tokio::test]
async fn irn_needs_buyer_gstin_and_window_off_for_small_taxpayers() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let old = env.draft(&t, t.contact, -40).await;
    env.issue(&t, old).await;
    env.ok(&t.user, "invoice.generate_irn", json!({"id": old}))
        .await; // not enforced when AATO <= 10cr

    let consumer = env
        .ok(
            &t.user,
            "contact.create",
            json!({"space_id": t.space, "name": "Walk-in", "state_code": "29"}),
        )
        .await["id"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    let b2c = env.draft(&t, consumer, 0).await;
    env.issue(&t, b2c).await;
    let e = env
        .run(&t.user, "invoice.generate_irn", json!({"id": b2c}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn tenants_are_isolated() {
    let env = Env::new().await;
    let a = env.tenant(false).await;
    let b = env.tenant(false).await;
    let id = env.draft(&a, a.contact, 0).await;
    env.issue(&a, id).await;

    for (cmd, input) in [
        ("invoice.get", json!({"id": id})),
        ("contact.get", json!({"id": a.contact})),
        ("invoice.issue", json!({"id": id})),
        (
            "payment.record",
            json!({"invoice_id": id, "amount_minor": 1}),
        ),
    ] {
        let e = env.run(&b.user, cmd, input).await.unwrap_err();
        assert_eq!(e.code, ErrorCode::NotFound, "{cmd}");
    }
    // B cannot invoice A's contact either
    let e = env
        .run(
            &b.user,
            "invoice.create_draft",
            json!({"space_id": b.space, "contact_id": a.contact,
            "lines": [{"description": "x", "unit_price_minor": 1}]}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
    assert_eq!(
        env.ok(&b.user, "invoice.list", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );
    assert_eq!(
        env.ok(&b.user, "contact.list", json!({"q": "Buyer"}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );
    let tb = env.trial_balance(&b.user).await;
    assert_eq!(tb["total_debit_minor"], 0);
    assert_eq!(
        env.ok(&b.user, "report.receivables_ageing", json!({}))
            .await["total_minor"],
        0
    );
}

#[tokio::test]
async fn crm_pipeline_and_validation() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let bad = env
        .run(
            &t.user,
            "contact.create",
            json!({"space_id": t.space, "name": "X", "gstin": "29ABCDE1234F1ZZ"}),
        )
        .await
        .unwrap_err();
    assert_eq!(bad.code, ErrorCode::Invalid);
    let upd = env
        .ok(
            &t.user,
            "contact.update",
            json!({"id": t.contact, "phone": "+91 99999 00000", "name": "Renamed"}),
        )
        .await;
    assert_eq!(
        (
            upd["phone"].as_str(),
            upd["name"].as_str(),
            upd["state_code"].as_str()
        ),
        (Some("+91 99999 00000"), Some("Renamed"), Some("29"))
    );

    let deal = env
        .ok(&t.user, "deal.create", json!({"space_id": t.space, "title": "Big project", "contact_id": t.contact, "amount_minor": 5_000_000}))
        .await;
    assert_eq!(deal["stage"], "lead");
    let id: Uuid = deal["id"].as_str().unwrap().parse().unwrap();
    let q = env
        .ok(
            &t.user,
            "deal.move_stage",
            json!({"id": id, "stage": "proposal"}),
        )
        .await;
    assert_eq!(q["stage"], "proposal");
    assert!(
        env.run(
            &t.user,
            "deal.move_stage",
            json!({"id": id, "stage": "nope"})
        )
        .await
        .is_err()
    );
    let won = env
        .ok(
            &t.user,
            "deal.move_stage",
            json!({"id": id, "stage": "won"}),
        )
        .await;
    assert_eq!(
        (won["probability"].as_i64(), won["closed_at"].is_string()),
        (Some(100), true)
    );
    let ev = events(&env, &t.user, id).await;
    assert_eq!(ev.iter().filter(|k| *k == "deal.stage_changed").count(), 2);
    assert!(ev.contains(&"deal.won".to_string()));
    assert_eq!(
        env.ok(&t.user, "deal.list", json!({"stage": "won"}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        env.ok(&t.user, "deal.list", json!({"stage": "lead"}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );
}

#[tokio::test]
async fn invoice_lines_link_to_work_items() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    let task = env
        .ok(
            &t.user,
            "note.create",
            json!({"space_id": t.space, "title": "Build thing", "types": ["task"]}),
        )
        .await["id"]
        .as_str()
        .unwrap()
        .to_string();
    let d = env
        .ok(&t.user, "invoice.create_draft", json!({"space_id": t.space, "contact_id": t.contact,
            "lines": [{"description": "Build thing", "unit_price_minor": 100_000, "item_note_id": task}]}))
        .await;
    let n = env.ok(&t.user, "note.get", json!({"id": d["id"]})).await;
    let rels = n["relations"].as_array().unwrap();
    assert!(
        rels.iter()
            .any(|r| r["kind"] == "bills" && r["to_id"] == task.as_str())
    );
}

#[tokio::test]
async fn agents_need_human_confirmation_to_issue() {
    let env = Env::new().await;
    let t = env.tenant(false).await;
    env.ok(
        &t.user,
        "workspace.update",
        json!({"settings": {"agent_tool_groups": ["read", "manage_crm", "manage_invoices"]}}),
    )
    .await;
    let created = env
        .ok(
            &t.user,
            "agent.create",
            json!({"name": "Biller", "scopes": ["read", "manage_crm", "manage_invoices"]}),
        )
        .await;
    let agent = auth::principal_from_api_key(&env.pool, created["api_key"].as_str().unwrap())
        .await
        .unwrap()
        .unwrap();
    let id = env.draft(&t, t.contact, 0).await;

    let r = env.ok(&agent, "invoice.issue", json!({"id": id})).await;
    assert_eq!(r["confirmation_required"], true);
    assert_eq!(
        env.ok(&t.user, "invoice.get", json!({"id": id})).await["status"],
        "draft"
    );
    let cid: Uuid = r["confirmation_id"].as_str().unwrap().parse().unwrap();

    // unconfirmed id is refused
    let opts = ExecOptions {
        confirmation_id: Some(cid),
        ..Default::default()
    };
    let e = env
        .reg
        .execute(&env.pool, "invoice.issue", json!({"id": id}), &agent, &opts)
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);

    // agents cannot approve their own request
    assert!(
        env.run(
            &agent,
            "confirmation.decide",
            json!({"id": cid, "approve": true})
        )
        .await
        .is_err()
    );
    env.ok(
        &t.user,
        "confirmation.decide",
        json!({"id": cid, "approve": true}),
    )
    .await;
    let issued = env
        .reg
        .execute(&env.pool, "invoice.issue", json!({"id": id}), &agent, &opts)
        .await
        .unwrap();
    assert_eq!(issued["status"], "issued");
    // single use
    assert!(
        env.reg
            .execute(&env.pool, "invoice.issue", json!({"id": id}), &agent, &opts)
            .await
            .is_err()
    );

    // payments are gated the same way; drafting is not
    let p = env
        .ok(
            &agent,
            "payment.record",
            json!({"invoice_id": id, "amount_minor": 100}),
        )
        .await;
    assert_eq!(p["confirmation_required"], true);
    let d = env
        .run(
            &agent,
            "invoice.create_draft",
            json!({"space_id": t.space, "contact_id": t.contact,
            "lines": [{"description": "x", "unit_price_minor": 100}]}),
        )
        .await
        .unwrap();
    assert_eq!(d["status"], "draft");
}

#[tokio::test]
async fn numbering_is_gap_free_under_concurrency() {
    let env = Arc::new(Env::new().await);
    let t = Arc::new(env.tenant(false).await);
    let mut drafts = vec![];
    for _ in 0..6 {
        drafts.push(env.draft(&t, t.contact, 0).await);
    }
    let handles: Vec<_> = drafts
        .iter()
        .map(|id| {
            let (env, t, id) = (env.clone(), t.clone(), *id);
            tokio::spawn(async move {
                env.issue(&t, id).await["number"]
                    .as_str()
                    .unwrap()
                    .to_string()
            })
        })
        .collect();
    let mut numbers = vec![];
    for h in handles {
        numbers.push(h.await.unwrap());
    }
    numbers.sort();
    let fy = cnote_finance::gst::financial_year(today_ist());
    let expected: Vec<_> = (1..=6).map(|n| format!("INV/{fy}/{n:04}")).collect();
    assert_eq!(numbers, expected);
    assert_eq!(env.trial_balance(&t.user).await["net_minor"], 0);

    // a failed issue (no GSTIN configured) must not burn a number
    let other = env.tenant(false).await;
    env.ok(
        &other.user,
        "workspace.update",
        json!({"settings": {"gstin": "bad"}}),
    )
    .await;
    let d = env.draft(&other, other.contact, 0).await;
    assert!(
        env.run(&other.user, "invoice.issue", json!({"id": d}))
            .await
            .is_err()
    );
    env.ok(
        &other.user,
        "workspace.update",
        json!({"settings": {"gstin": complete_gstin("29ABCDE1234F1Z")}}),
    )
    .await;
    assert_eq!(
        env.issue(&other, d).await["number"],
        format!("INV/{fy}/0001")
    );
}
