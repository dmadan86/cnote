//! GST invoicing on a double-entry ledger (ADR-008): drafts, issue, IRN, credit notes, payments, write-offs.

use crate::irn::{self, Window};
use crate::{gst, ledger};
use chrono::{DateTime, Duration, NaiveDate, Utc};
use cnote_core::access::{assert_note_access, readable_space_ids};
use cnote_core::notes::{CreateNote, add_relation, insert_note};
use cnote_core::{CmdError, CmdResult, Command, Ctx, ErrorCode, Role, ToolGroup, handler};
use rust_decimal::Decimal;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::str::FromStr;
use uuid::Uuid;

fn gst_err(e: gst::GstError) -> CmdError {
    CmdError::invalid(e.to_string())
}

/// Today in India (UTC+5:30); invoice dates and financial years follow the local calendar.
pub fn today_ist() -> NaiveDate {
    (Utc::now() + Duration::minutes(330)).date_naive()
}

// ---------- rows ----------

#[derive(Debug, Clone, Serialize, JsonSchema, sqlx::FromRow)]
pub struct InvoiceRow {
    pub id: Uuid,
    /// invoice | credit_note
    pub kind: String,
    pub number: Option<String>,
    pub status: String,
    pub contact_id: Option<Uuid>,
    pub original_invoice_id: Option<Uuid>,
    pub currency: String,
    pub supplier_gstin: Option<String>,
    pub supplier_state: Option<String>,
    pub buyer_gstin: Option<String>,
    pub place_of_supply: Option<String>,
    pub reverse_charge: bool,
    pub invoice_date: Option<NaiveDate>,
    pub due_date: Option<NaiveDate>,
    pub subtotal_minor: i64,
    pub cgst_minor: i64,
    pub sgst_minor: i64,
    pub igst_minor: i64,
    pub total_minor: i64,
    pub paid_minor: i64,
    pub credited_minor: i64,
    pub written_off_minor: i64,
    pub irn: Option<String>,
    pub ack_no: Option<String>,
    pub ack_date: Option<DateTime<Utc>>,
    pub signed_qr: Option<String>,
    pub irn_cancelled_at: Option<DateTime<Utc>>,
    pub issued_at: Option<DateTime<Utc>>,
    pub cancelled_at: Option<DateTime<Utc>>,
}

impl InvoiceRow {
    pub fn outstanding(&self) -> i64 {
        self.total_minor - self.paid_minor - self.credited_minor - self.written_off_minor
    }
}

pub const INV_COLS: &str = "i.note_id as id, i.kind, i.number, i.status, i.contact_id, i.original_invoice_id, i.currency::text as currency,
    i.supplier_gstin, i.supplier_state, i.buyer_gstin, i.place_of_supply, i.reverse_charge, i.invoice_date, i.due_date,
    i.subtotal_minor, i.cgst_minor, i.sgst_minor, i.igst_minor, i.total_minor, i.paid_minor, i.credited_minor,
    i.written_off_minor, i.irn, i.ack_no, i.ack_date, i.signed_qr, i.irn_cancelled_at, i.issued_at, i.cancelled_at";

async fn load_invoice(ctx: &mut Ctx, id: Uuid, lock: bool) -> CmdResult<InvoiceRow> {
    sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {INV_COLS} from billing.invoices i join core.notes n on n.id = i.note_id
         where i.note_id = $1 and n.deleted_at is null {}",
        if lock { "for update of i" } else { "" }
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("invoice"))
}

#[derive(Debug, Serialize, JsonSchema, sqlx::FromRow)]
pub struct LineOut {
    pub id: Uuid,
    pub position: i32,
    pub description: String,
    pub hsn_sac: Option<String>,
    pub quantity: String,
    pub unit_price_minor: i64,
    pub gst_rate_bp: i32,
    pub taxable_minor: i64,
    pub cgst_minor: i64,
    pub sgst_minor: i64,
    pub igst_minor: i64,
    pub item_note_id: Option<Uuid>,
}

#[derive(Debug, Serialize, JsonSchema, sqlx::FromRow)]
pub struct PaymentOut {
    pub id: Uuid,
    pub amount_minor: i64,
    pub method: String,
    pub reference: Option<String>,
    pub received_at: DateTime<Utc>,
}

#[derive(Serialize, JsonSchema)]
pub struct InvoiceOut {
    #[serde(flatten)]
    pub invoice: InvoiceRow,
    pub contact_name: Option<String>,
    pub outstanding_minor: i64,
    pub lines: Vec<LineOut>,
    pub payments: Vec<PaymentOut>,
}

// ---------- lines ----------

#[derive(Debug, Clone, Deserialize, JsonSchema)]
pub struct LineIn {
    pub description: String,
    /// HSN (goods) or SAC (services) code: 4, 6 or 8 digits.
    #[serde(default)]
    pub hsn_sac: Option<String>,
    /// Decimal quantity, default 1.
    #[serde(default)]
    pub quantity: Option<serde_json::Number>,
    /// Unit price in paise.
    pub unit_price_minor: i64,
    /// GST rate in basis points (1800 = 18%), default 1800.
    #[serde(default)]
    pub gst_rate_bp: Option<u32>,
    /// A task/item note this line bills; adds a `bills` relation from the invoice.
    #[serde(default)]
    pub item_note_id: Option<Uuid>,
}

struct Ln {
    description: String,
    item: Option<Uuid>,
    g: gst::LineIn,
}

fn parse_lines(lines: &[LineIn]) -> CmdResult<Vec<Ln>> {
    if lines.is_empty() {
        return Err(CmdError::invalid("at least one line is required"));
    }
    lines
        .iter()
        .map(|l| {
            if l.description.trim().is_empty() {
                return Err(CmdError::invalid("line description is required"));
            }
            let qty = match &l.quantity {
                Some(n) => {
                    let s = n.to_string();
                    Decimal::from_str(&s)
                        .or_else(|_| Decimal::from_scientific(&s))
                        .map_err(|_| CmdError::invalid("invalid quantity"))?
                }
                None => Decimal::ONE,
            };
            let g = gst::LineIn {
                qty,
                unit_price_minor: l.unit_price_minor,
                rate_bp: l.gst_rate_bp.unwrap_or(1800),
                hsn_sac: l.hsn_sac.clone().filter(|h| !h.trim().is_empty()),
            };
            gst::taxable_paise(g.qty, g.unit_price_minor).map_err(gst_err)?;
            if g.rate_bp > 2800 {
                return Err(gst_err(gst::GstError::Rate));
            }
            if let Some(h) = &g.hsn_sac {
                gst::validate_hsn(h).map_err(gst_err)?;
            }
            Ok(Ln {
                description: l.description.trim().to_string(),
                item: l.item_note_id,
                g,
            })
        })
        .collect()
}

#[derive(sqlx::FromRow)]
struct DbLine {
    description: String,
    hsn_sac: Option<String>,
    quantity: String,
    unit_price_minor: i64,
    gst_rate_bp: i32,
    item_note_id: Option<Uuid>,
}

async fn load_lines(ctx: &mut Ctx, invoice_id: Uuid) -> CmdResult<Vec<Ln>> {
    let rows: Vec<DbLine> = sqlx::query_as(
        "select description, hsn_sac, quantity::text as quantity, unit_price_minor, gst_rate_bp, item_note_id
         from billing.invoice_lines where invoice_id = $1 order by position",
    )
    .bind(invoice_id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    rows.into_iter()
        .map(|r| {
            Ok(Ln {
                description: r.description,
                item: r.item_note_id,
                g: gst::LineIn {
                    qty: Decimal::from_str(&r.quantity)
                        .map_err(|_| CmdError::internal("bad stored quantity"))?,
                    unit_price_minor: r.unit_price_minor,
                    rate_bp: r.gst_rate_bp as u32,
                    hsn_sac: r.hsn_sac,
                },
            })
        })
        .collect()
}

/// Replace an invoice's lines. With `tax` the computed GST is stored; without, only taxable amounts (drafts).
async fn write_lines(
    ctx: &mut Ctx,
    invoice_id: Uuid,
    lines: &[Ln],
    tax: Option<&gst::InvoiceTax>,
) -> CmdResult<i64> {
    sqlx::query("delete from billing.invoice_lines where invoice_id = $1")
        .bind(invoice_id)
        .execute(&mut *ctx.tx)
        .await?;
    let mut subtotal = 0;
    for (pos, l) in lines.iter().enumerate() {
        let t = match tax {
            Some(t) => t.lines[pos].clone(),
            None => gst::LineTax {
                taxable_minor: gst::taxable_paise(l.g.qty, l.g.unit_price_minor)
                    .map_err(gst_err)?,
                cgst_minor: 0,
                sgst_minor: 0,
                igst_minor: 0,
            },
        };
        subtotal += t.taxable_minor;
        sqlx::query(
            "insert into billing.invoice_lines (workspace_id, invoice_id, position, description, hsn_sac, quantity,
               unit_price_minor, gst_rate_bp, taxable_minor, cgst_minor, sgst_minor, igst_minor, item_note_id)
             values ($1,$2,$3,$4,$5,$6::numeric,$7,$8,$9,$10,$11,$12,$13)",
        )
        .bind(ctx.workspace_id())
        .bind(invoice_id)
        .bind(pos as i32 + 1)
        .bind(&l.description)
        .bind(&l.g.hsn_sac)
        .bind(l.g.qty.to_string())
        .bind(l.g.unit_price_minor)
        .bind(l.g.rate_bp as i32)
        .bind(t.taxable_minor)
        .bind(t.cgst_minor)
        .bind(t.sgst_minor)
        .bind(t.igst_minor)
        .bind(l.item)
        .execute(&mut *ctx.tx)
        .await?;
    }
    Ok(subtotal)
}

/// Link the invoice to every item it bills (`bills` relation), after checking the caller can read them.
async fn link_items(ctx: &mut Ctx, invoice_id: Uuid, lines: &[Ln]) -> CmdResult<()> {
    sqlx::query("delete from core.relations where from_id = $1 and kind = 'bills'")
        .bind(invoice_id)
        .execute(&mut *ctx.tx)
        .await?;
    for item in lines.iter().filter_map(|l| l.item) {
        assert_note_access(ctx, item, false).await?;
        add_relation(ctx, invoice_id, item, "bills", None).await?;
    }
    Ok(())
}

// ---------- helpers ----------

struct ContactInfo {
    name: String,
    gstin: Option<String>,
    state: Option<String>,
}

async fn contact_info(ctx: &mut Ctx, id: Uuid) -> CmdResult<ContactInfo> {
    assert_note_access(ctx, id, false).await?;
    let r: Option<(String, Option<String>, Option<String>)> = sqlx::query_as(
        "select n.title, c.gstin, c.state_code from crm.contacts c join core.notes n on n.id = c.note_id
         where c.note_id = $1 and n.deleted_at is null",
    )
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let (name, gstin, state) = r.ok_or_else(|| CmdError::not_found("contact"))?;
    Ok(ContactInfo { name, gstin, state })
}

struct Seller {
    gstin: String,
    state: String,
    aato_above_10cr: bool,
}

async fn seller(ctx: &mut Ctx) -> CmdResult<Seller> {
    let s: Value = sqlx::query_scalar("select settings from core.workspaces where id = $1")
        .bind(ctx.workspace_id())
        .fetch_one(&mut *ctx.tx)
        .await?;
    let gstin = s["gstin"].as_str().ok_or_else(|| {
        CmdError::invalid(
            "set the workspace GSTIN (workspace.update settings.gstin) before issuing invoices",
        )
    })?;
    let gstin = gst::validate_gstin(gstin).map_err(gst_err)?;
    let state = s["state_code"]
        .as_str()
        .map(str::to_string)
        .unwrap_or_else(|| gst::gstin_state(&gstin).to_string());
    gst::validate_state(&state).map_err(gst_err)?;
    Ok(Seller {
        gstin,
        state,
        aato_above_10cr: s["aato_above_10cr"].as_bool().unwrap_or(false),
    })
}

pub fn format_number(prefix: &str, fy: &str, n: i32) -> String {
    format!("{prefix}/{fy}/{n:04}")
}

/// Next gap-free number for (series, FY). The UPDATE row-locks the counter until commit, so concurrent
/// issuers queue up and a rolled-back issue gives its number back.
async fn next_number(ctx: &mut Ctx, series: &str, prefix: &str, fy: &str) -> CmdResult<String> {
    let name = format!("{series}:{fy}");
    sqlx::query(
        "insert into billing.sequences (workspace_id, name) values ($1,$2) on conflict do nothing",
    )
    .bind(ctx.workspace_id())
    .bind(&name)
    .execute(&mut *ctx.tx)
    .await?;
    let n: i32 = sqlx::query_scalar(
        "update billing.sequences set next_value = next_value + 1 where workspace_id = $1 and name = $2
         returning next_value - 1",
    )
    .bind(ctx.workspace_id())
    .bind(&name)
    .fetch_one(&mut *ctx.tx)
    .await?;
    Ok(format_number(prefix, fy, n))
}

/// Status after a settlement (payment, credit note, write-off): nothing outstanding means `paid` (settled).
pub fn settled_status(
    total: i64,
    paid: i64,
    credited: i64,
    written_off: i64,
    current: &str,
) -> String {
    if total - paid - credited - written_off == 0 {
        "paid".into()
    } else if paid > 0 {
        "partly_paid".into()
    } else {
        current.into()
    }
}

const OPEN_STATUSES: [&str; 4] = ["issued", "irn_generated", "sent", "partly_paid"];

fn require_open(inv: &InvoiceRow) -> CmdResult<()> {
    if inv.kind != "invoice" || !OPEN_STATUSES.contains(&inv.status.as_str()) {
        return Err(CmdError::conflict(format!(
            "invoice is {} and cannot take this action",
            inv.status
        )));
    }
    Ok(())
}

// ---------- drafts ----------

#[derive(Deserialize, JsonSchema)]
pub struct CreateDraft {
    /// Space that holds the invoice.
    pub space_id: Uuid,
    /// The customer (a contact).
    pub contact_id: Uuid,
    pub lines: Vec<LineIn>,
    /// Defaults to today (IST).
    #[serde(default)]
    pub invoice_date: Option<NaiveDate>,
    /// Defaults to 30 days after the invoice date.
    #[serde(default)]
    pub due_date: Option<NaiveDate>,
    /// GST state code; defaults to the customer's state.
    #[serde(default)]
    pub place_of_supply: Option<String>,
    /// Tax payable by the recipient: no tax is charged on the invoice, but it is flagged.
    #[serde(default)]
    pub reverse_charge: Option<bool>,
    #[serde(default)]
    pub notes: Option<String>,
}

async fn invoice_create_draft(ctx: &mut Ctx, i: CreateDraft) -> CmdResult<InvoiceOut> {
    let lines = parse_lines(&i.lines)?;
    let c = contact_info(ctx, i.contact_id).await?;
    if let Some(p) = &i.place_of_supply {
        gst::validate_state(p).map_err(gst_err)?;
    }
    let date = i.invoice_date.unwrap_or_else(today_ist);
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: i.space_id,
            title: format!("Invoice for {}", c.name),
            parent_id: None,
            content_md: i.notes,
            types: vec!["invoice".into()],
            owner_id: None,
            verify_by: None,
        },
    )
    .await?;
    sqlx::query(
        "insert into billing.invoices (note_id, workspace_id, kind, status, contact_id, buyer_gstin, place_of_supply,
           reverse_charge, invoice_date, due_date)
         values ($1,$2,'invoice','draft',$3,$4,$5,$6,$7,$8)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(i.contact_id)
    .bind(&c.gstin)
    .bind(i.place_of_supply.or(c.state))
    .bind(i.reverse_charge.unwrap_or(false))
    .bind(date)
    .bind(i.due_date.unwrap_or(date + Duration::days(30)))
    .execute(&mut *ctx.tx)
    .await?;
    let subtotal = write_lines(ctx, note.id, &lines, None).await?;
    sqlx::query(
        "update billing.invoices set subtotal_minor = $2, total_minor = $2 where note_id = $1",
    )
    .bind(note.id)
    .bind(subtotal)
    .execute(&mut *ctx.tx)
    .await?;
    link_items(ctx, note.id, &lines).await?;
    ctx.emit(
        "invoice.drafted",
        Some(note.id),
        json!({"contact_id": i.contact_id, "subtotal_minor": subtotal}),
    )
    .await?;
    invoice_detail(ctx, note.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateDraft {
    pub id: Uuid,
    #[serde(default)]
    pub contact_id: Option<Uuid>,
    /// Replaces all lines when given.
    #[serde(default)]
    pub lines: Option<Vec<LineIn>>,
    #[serde(default)]
    pub invoice_date: Option<NaiveDate>,
    #[serde(default)]
    pub due_date: Option<NaiveDate>,
    #[serde(default)]
    pub place_of_supply: Option<String>,
    #[serde(default)]
    pub reverse_charge: Option<bool>,
}

async fn invoice_update_draft(ctx: &mut Ctx, i: UpdateDraft) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    if inv.status != "draft" {
        return Err(CmdError::conflict(
            "only drafts can be edited; issued invoices change through credit notes",
        ));
    }
    if let Some(p) = &i.place_of_supply {
        gst::validate_state(p).map_err(gst_err)?;
    }
    let mut buyer = None;
    let mut contact_state = None;
    if let Some(cid) = i.contact_id {
        let c = contact_info(ctx, cid).await?;
        buyer = c.gstin;
        contact_state = c.state;
    }
    sqlx::query(
        "update billing.invoices set contact_id = coalesce($2, contact_id), buyer_gstin = case when $2 is null then buyer_gstin else $3 end,
           place_of_supply = coalesce($4, $5, place_of_supply), reverse_charge = coalesce($6, reverse_charge),
           invoice_date = coalesce($7, invoice_date), due_date = coalesce($8, due_date) where note_id = $1",
    )
    .bind(i.id)
    .bind(i.contact_id)
    .bind(buyer)
    .bind(i.place_of_supply)
    .bind(contact_state)
    .bind(i.reverse_charge)
    .bind(i.invoice_date)
    .bind(i.due_date)
    .execute(&mut *ctx.tx)
    .await?;
    if let Some(l) = &i.lines {
        let lines = parse_lines(l)?;
        let subtotal = write_lines(ctx, i.id, &lines, None).await?;
        sqlx::query(
            "update billing.invoices set subtotal_minor = $2, total_minor = $2 where note_id = $1",
        )
        .bind(i.id)
        .bind(subtotal)
        .execute(&mut *ctx.tx)
        .await?;
        link_items(ctx, i.id, &lines).await?;
    }
    ctx.emit("invoice.draft_updated", Some(i.id), json!({}))
        .await?;
    invoice_detail(ctx, i.id).await
}

// ---------- read ----------

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

async fn invoice_detail(ctx: &mut Ctx, id: Uuid) -> CmdResult<InvoiceOut> {
    let invoice = load_invoice(ctx, id, false).await?;
    let contact_name: Option<String> = match invoice.contact_id {
        Some(c) => {
            sqlx::query_scalar("select title from core.notes where id = $1")
                .bind(c)
                .fetch_optional(&mut *ctx.tx)
                .await?
        }
        None => None,
    };
    let lines = sqlx::query_as(
        "select id, position, description, hsn_sac, quantity::text as quantity, unit_price_minor, gst_rate_bp,
           taxable_minor, cgst_minor, sgst_minor, igst_minor, item_note_id
         from billing.invoice_lines where invoice_id = $1 order by position",
    )
    .bind(id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let payments = sqlx::query_as(
        "select id, amount_minor, method, reference, received_at from billing.payments where invoice_id = $1 order by received_at",
    )
    .bind(id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    Ok(InvoiceOut {
        outstanding_minor: invoice.outstanding(),
        invoice,
        contact_name,
        lines,
        payments,
    })
}

async fn invoice_get(ctx: &mut Ctx, i: ById) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, false).await?;
    invoice_detail(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct ListInvoices {
    #[serde(default)]
    pub status: Option<String>,
    #[serde(default)]
    pub contact_id: Option<Uuid>,
    /// invoice | credit_note
    #[serde(default)]
    pub kind: Option<String>,
    #[serde(default)]
    pub limit: Option<i64>,
}

async fn invoice_list(ctx: &mut Ctx, i: ListInvoices) -> CmdResult<Vec<InvoiceRow>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {INV_COLS} from billing.invoices i join core.notes n on n.id = i.note_id
         where n.deleted_at is null and n.space_id = any($1) and ($2::text is null or i.status = $2)
           and ($3::uuid is null or i.contact_id = $3) and ($4::text is null or i.kind = $4)
         order by n.created_at desc limit $5"
    )))
    .bind(spaces)
    .bind(i.status)
    .bind(i.contact_id)
    .bind(i.kind)
    .bind(i.limit.unwrap_or(100).clamp(1, 500))
    .fetch_all(&mut *ctx.tx)
    .await?)
}

/// (lines, tax, place of supply, buyer GSTIN)
type DraftTax = (Vec<Ln>, gst::InvoiceTax, String, Option<String>);

/// Lines and GST for a draft as issue would compute them; also the buyer GSTIN and place of supply.
async fn draft_tax(ctx: &mut Ctx, inv: &InvoiceRow, s: &Seller) -> CmdResult<DraftTax> {
    let contact = match inv.contact_id {
        Some(c) => Some(contact_info(ctx, c).await?),
        None => None,
    };
    let buyer_gstin = contact
        .as_ref()
        .and_then(|c| c.gstin.clone())
        .or(inv.buyer_gstin.clone());
    let place = inv
        .place_of_supply
        .clone()
        .or_else(|| contact.as_ref().and_then(|c| c.state.clone()))
        .or_else(|| {
            buyer_gstin
                .as_deref()
                .map(|g| gst::gstin_state(g).to_string())
        })
        .ok_or_else(|| {
            CmdError::invalid("place of supply is unknown: set it or give the contact a state code")
        })?;
    let lines = load_lines(ctx, inv.id).await?;
    if lines.is_empty() {
        return Err(CmdError::invalid("an invoice needs at least one line"));
    }
    let g: Vec<_> = lines.iter().map(|l| l.g.clone()).collect();
    let tax = gst::compute_invoice(&g, &s.state, &place, inv.reverse_charge).map_err(gst_err)?;
    Ok((lines, tax, place, buyer_gstin))
}

/// Read-only: the totals `invoice.issue` would produce for this draft, so the UI can show them before issuing.
async fn invoice_preview(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    assert_note_access(ctx, i.id, false).await?;
    let inv = load_invoice(ctx, i.id, false).await?;
    if inv.status != "draft" {
        return Err(CmdError::conflict("only a draft can be previewed"));
    }
    let s = seller(ctx).await?;
    let (_, t, place, _) = draft_tax(ctx, &inv, &s).await?;
    Ok(
        json!({"intra_state": t.supply == gst::Supply::Intra, "place_of_supply": place, "supplier_state": s.state,
        "reverse_charge": t.reverse_charge, "subtotal_minor": t.subtotal_minor, "cgst_minor": t.cgst_minor,
        "sgst_minor": t.sgst_minor, "igst_minor": t.igst_minor, "total_minor": t.total_minor}),
    )
}

// ---------- issue ----------

async fn invoice_issue(ctx: &mut Ctx, i: ById) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    if inv.kind != "invoice" || inv.status != "draft" {
        return Err(CmdError::conflict("only a draft invoice can be issued"));
    }
    let s = seller(ctx).await?;
    let (lines, tax, place, buyer_gstin) = draft_tax(ctx, &inv, &s).await?;
    write_lines(ctx, i.id, &lines, Some(&tax)).await?;

    let date = inv.invoice_date.unwrap_or_else(today_ist);
    let number = next_number(ctx, "invoice", "INV", &gst::financial_year(date)).await?;
    sqlx::query(
        "update billing.invoices set status = 'issued', number = $2, issued_at = now(), supplier_gstin = $3,
           supplier_state = $4, buyer_gstin = $5, place_of_supply = $6, invoice_date = $7,
           due_date = coalesce(due_date, $7::date + 30), subtotal_minor = $8, cgst_minor = $9, sgst_minor = $10,
           igst_minor = $11, total_minor = $12 where note_id = $1",
    )
    .bind(i.id)
    .bind(&number)
    .bind(&s.gstin)
    .bind(&s.state)
    .bind(&buyer_gstin)
    .bind(&place)
    .bind(date)
    .bind(tax.subtotal_minor)
    .bind(tax.cgst_minor)
    .bind(tax.sgst_minor)
    .bind(tax.igst_minor)
    .bind(tax.total_minor)
    .execute(&mut *ctx.tx)
    .await?;
    ledger::post(
        ctx,
        "invoice",
        i.id,
        &format!("Invoice {number}"),
        &ledger::sale_legs(
            tax.subtotal_minor,
            tax.cgst_minor,
            tax.sgst_minor,
            tax.igst_minor,
            false,
        ),
    )
    .await?;
    ctx.emit(
        "invoice.issued",
        Some(i.id),
        json!({"number": number, "total_minor": tax.total_minor, "contact_id": inv.contact_id,
               "reverse_charge": tax.reverse_charge, "intra_state": tax.supply == gst::Supply::Intra}),
    )
    .await?;
    invoice_detail(ctx, i.id).await
}

// ---------- IRN ----------

fn irn_request(inv: &InvoiceRow) -> CmdResult<irn::IrnRequest> {
    let date = inv.invoice_date.unwrap_or_else(today_ist);
    Ok(irn::IrnRequest {
        supplier_gstin: inv.supplier_gstin.clone().unwrap_or_default(),
        doc_type: if inv.kind == "credit_note" {
            "CRN"
        } else {
            "INV"
        },
        number: inv.number.clone().unwrap_or_default(),
        financial_year: gst::financial_year(date),
        invoice_date: date,
        buyer_gstin: inv
            .buyer_gstin
            .clone()
            .ok_or_else(|| CmdError::invalid("e-invoicing needs the buyer's GSTIN (B2B only)"))?,
        total_minor: inv.total_minor,
    })
}

async fn invoice_generate_irn(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    if inv.number.is_none() || inv.status == "draft" || inv.status == "cancelled" {
        return Err(CmdError::conflict(
            "invoice must be issued and not cancelled",
        ));
    }
    if inv.irn.is_some() {
        return Err(CmdError::conflict(
            "an IRN already exists for this document",
        ));
    }
    let req = irn_request(&inv)?;
    let s = seller(ctx).await?;
    let warning = match irn::window(req.invoice_date, today_ist(), s.aato_above_10cr) {
        Window::Blocked => {
            return Err(CmdError::new(
                ErrorCode::Conflict,
                "IRN reporting window closed: invoices must be reported within 30 days of the invoice date",
            ));
        }
        Window::Warn { days_left } => Some(format!(
            "only {days_left} day(s) left in the 30-day IRN reporting window"
        )),
        _ => None,
    };
    let r = irn::gsp()
        .generate_irn(&req)
        .await
        .map_err(|e| CmdError::internal(format!("GSP error: {e}")))?;
    sqlx::query(
        "update billing.invoices set irn = $2, ack_no = $3, ack_date = $4, signed_qr = $5,
           status = case when status = 'issued' then 'irn_generated' else status end where note_id = $1",
    )
    .bind(i.id)
    .bind(&r.irn)
    .bind(&r.ack_no)
    .bind(r.ack_date)
    .bind(&r.signed_qr)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "invoice.irn_generated",
        Some(i.id),
        json!({"irn": r.irn, "ack_no": r.ack_no}),
    )
    .await?;
    Ok(json!({"invoice": invoice_detail(ctx, i.id).await?, "warning": warning}))
}

#[derive(Deserialize, JsonSchema)]
pub struct CancelIrn {
    pub id: Uuid,
    /// Reason for cancelling the IRN.
    pub reason: String,
}

async fn invoice_cancel_irn(ctx: &mut Ctx, i: CancelIrn) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    let (Some(irn), Some(ack)) = (inv.irn.as_deref(), inv.ack_date) else {
        return Err(CmdError::conflict("invoice has no IRN"));
    };
    if inv.irn_cancelled_at.is_some() {
        return Err(CmdError::conflict("IRN is already cancelled"));
    }
    if Utc::now() - ack > Duration::hours(24) {
        return Err(CmdError::conflict(
            "an IRN can only be cancelled within 24 hours of acknowledgement; issue a credit note instead",
        ));
    }
    irn::gsp()
        .cancel_irn(irn, &i.reason)
        .await
        .map_err(|e| CmdError::internal(format!("GSP error: {e}")))?;
    sqlx::query(
        "update billing.invoices set irn_cancelled_at = now(),
           status = case when status = 'irn_generated' then 'issued' else status end where note_id = $1",
    )
    .bind(i.id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "invoice.irn_cancelled",
        Some(i.id),
        json!({"reason": i.reason}),
    )
    .await?;
    invoice_detail(ctx, i.id).await
}

// ---------- credit notes ----------

/// Issue a credit note against `orig` for `lines`: tax is computed with the original's supply, the ledger is
/// reversed and the original's outstanding amount drops. Caller holds the lock on `orig`.
async fn issue_credit_note(
    ctx: &mut Ctx,
    orig: &InvoiceRow,
    lines: &[Ln],
    reason: &str,
) -> CmdResult<InvoiceRow> {
    let (Some(sstate), Some(place), Some(onum)) =
        (&orig.supplier_state, &orig.place_of_supply, &orig.number)
    else {
        return Err(CmdError::conflict("invoice has not been issued"));
    };
    let g: Vec<_> = lines.iter().map(|l| l.g.clone()).collect();
    let tax = gst::compute_invoice(&g, sstate, place, orig.reverse_charge).map_err(gst_err)?;
    if tax.total_minor <= 0 {
        return Err(CmdError::invalid("credit note total must be positive"));
    }
    if tax.total_minor > orig.outstanding() {
        return Err(CmdError::invalid(format!(
            "credit note total {} exceeds the outstanding amount {}",
            tax.total_minor,
            orig.outstanding()
        )));
    }
    let space: Uuid = sqlx::query_scalar("select space_id from core.notes where id = $1")
        .bind(orig.id)
        .fetch_one(&mut *ctx.tx)
        .await?;
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: space,
            title: format!("Credit note for {onum}"),
            parent_id: None,
            content_md: Some(reason.to_string()),
            types: vec!["credit_note".into()],
            owner_id: None,
            verify_by: None,
        },
    )
    .await?;
    let date = today_ist();
    sqlx::query(
        "insert into billing.invoices (note_id, workspace_id, kind, status, contact_id, original_invoice_id, supplier_gstin,
           supplier_state, buyer_gstin, place_of_supply, reverse_charge, invoice_date)
         values ($1,$2,'credit_note','draft',$3,$4,$5,$6,$7,$8,$9,$10)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(orig.contact_id)
    .bind(orig.id)
    .bind(&orig.supplier_gstin)
    .bind(sstate)
    .bind(&orig.buyer_gstin)
    .bind(place)
    .bind(orig.reverse_charge)
    .bind(date)
    .execute(&mut *ctx.tx)
    .await?;
    write_lines(ctx, note.id, lines, Some(&tax)).await?;
    let number = next_number(ctx, "credit_note", "CN", &gst::financial_year(date)).await?;
    sqlx::query(
        "update billing.invoices set status = 'issued', number = $2, issued_at = now(), subtotal_minor = $3,
           cgst_minor = $4, sgst_minor = $5, igst_minor = $6, total_minor = $7 where note_id = $1",
    )
    .bind(note.id)
    .bind(&number)
    .bind(tax.subtotal_minor)
    .bind(tax.cgst_minor)
    .bind(tax.sgst_minor)
    .bind(tax.igst_minor)
    .bind(tax.total_minor)
    .execute(&mut *ctx.tx)
    .await?;
    ledger::post(
        ctx,
        "credit_note",
        note.id,
        &format!("Credit note {number} against {onum}"),
        &ledger::sale_legs(
            tax.subtotal_minor,
            tax.cgst_minor,
            tax.sgst_minor,
            tax.igst_minor,
            true,
        ),
    )
    .await?;
    let credited = orig.credited_minor + tax.total_minor;
    sqlx::query("update billing.invoices set credited_minor = $2, status = $3 where note_id = $1")
        .bind(orig.id)
        .bind(credited)
        .bind(settled_status(
            orig.total_minor,
            orig.paid_minor,
            credited,
            orig.written_off_minor,
            &orig.status,
        ))
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "credit_note.issued",
        Some(note.id),
        json!({"number": number, "original_invoice_id": orig.id, "total_minor": tax.total_minor, "reason": reason}),
    )
    .await?;
    load_invoice(ctx, note.id, false).await
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateCreditNote {
    /// The issued invoice being credited.
    pub invoice_id: Uuid,
    /// What is being credited (a partial credit note); tax uses the original invoice's place of supply.
    pub lines: Vec<LineIn>,
    pub reason: String,
}

async fn credit_note_create(ctx: &mut Ctx, i: CreateCreditNote) -> CmdResult<InvoiceRow> {
    assert_note_access(ctx, i.invoice_id, true).await?;
    let orig = load_invoice(ctx, i.invoice_id, true).await?;
    require_open(&orig)?;
    let lines = parse_lines(&i.lines)?;
    issue_credit_note(ctx, &orig, &lines, &i.reason).await
}

#[derive(Deserialize, JsonSchema)]
pub struct CancelInvoice {
    pub id: Uuid,
    pub reason: String,
}

/// Cancel = a full credit note plus status `cancelled`. Refused once anything was paid, credited or written off.
async fn invoice_cancel(ctx: &mut Ctx, i: CancelInvoice) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    require_open(&inv)?;
    if inv.status == "partly_paid"
        || inv.paid_minor + inv.credited_minor + inv.written_off_minor > 0
    {
        return Err(CmdError::conflict(
            "invoice has payments, credits or write-offs; use a partial credit note instead",
        ));
    }
    if inv.irn.is_some() && inv.irn_cancelled_at.is_none() {
        return Err(CmdError::conflict(
            "cancel the IRN first (allowed within 24 hours of acknowledgement)",
        ));
    }
    let lines = load_lines(ctx, i.id).await?;
    let cn = issue_credit_note(ctx, &inv, &lines, &i.reason).await?;
    sqlx::query(
        "update billing.invoices set status = 'cancelled', cancelled_at = now() where note_id = $1",
    )
    .bind(i.id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "invoice.cancelled",
        Some(i.id),
        json!({"credit_note_id": cn.id, "reason": i.reason}),
    )
    .await?;
    invoice_detail(ctx, i.id).await
}

// ---------- payments and write-offs ----------

#[derive(Deserialize, JsonSchema)]
pub struct RecordPayment {
    pub invoice_id: Uuid,
    /// Amount received in paise; cannot exceed the outstanding balance.
    pub amount_minor: i64,
    /// upi | bank | cash | card ... (default upi)
    #[serde(default)]
    pub method: Option<String>,
    /// UTR / transaction reference.
    #[serde(default)]
    pub reference: Option<String>,
}

async fn payment_record(ctx: &mut Ctx, i: RecordPayment) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.invoice_id, true).await?;
    let inv = load_invoice(ctx, i.invoice_id, true).await?;
    require_open(&inv)?;
    if i.amount_minor <= 0 {
        return Err(CmdError::invalid("amount must be positive"));
    }
    if i.amount_minor > inv.outstanding() {
        return Err(CmdError::invalid(format!(
            "payment {} exceeds the outstanding amount {}",
            i.amount_minor,
            inv.outstanding()
        )));
    }
    let pid: Uuid = sqlx::query_scalar(
        "insert into billing.payments (workspace_id, invoice_id, amount_minor, method, reference)
         values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(i.invoice_id)
    .bind(i.amount_minor)
    .bind(i.method.unwrap_or_else(|| "upi".into()))
    .bind(&i.reference)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let paid = inv.paid_minor + i.amount_minor;
    let status = settled_status(
        inv.total_minor,
        paid,
        inv.credited_minor,
        inv.written_off_minor,
        &inv.status,
    );
    sqlx::query("update billing.invoices set paid_minor = $2, status = $3 where note_id = $1")
        .bind(i.invoice_id)
        .bind(paid)
        .bind(&status)
        .execute(&mut *ctx.tx)
        .await?;
    ledger::post(
        ctx,
        "payment",
        pid,
        &format!("Payment for {}", inv.number.as_deref().unwrap_or("")),
        &[
            (ledger::BANK, i.amount_minor, 0),
            (ledger::RECEIVABLES, 0, i.amount_minor),
        ],
    )
    .await?;
    ctx.emit(
        "payment.recorded",
        Some(i.invoice_id),
        json!({"payment_id": pid, "amount_minor": i.amount_minor, "status": status}),
    )
    .await?;
    if status == "paid" {
        ctx.emit(
            "invoice.paid",
            Some(i.invoice_id),
            json!({"number": inv.number, "total_minor": inv.total_minor}),
        )
        .await?;
    }
    invoice_detail(ctx, i.invoice_id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct WriteOff {
    pub id: Uuid,
    /// Paise to write off as bad debt; defaults to the whole outstanding balance.
    #[serde(default)]
    pub amount_minor: Option<i64>,
    pub reason: String,
}

async fn invoice_write_off(ctx: &mut Ctx, i: WriteOff) -> CmdResult<InvoiceOut> {
    assert_note_access(ctx, i.id, true).await?;
    let inv = load_invoice(ctx, i.id, true).await?;
    require_open(&inv)?;
    let amount = i.amount_minor.unwrap_or(inv.outstanding());
    if amount <= 0 || amount > inv.outstanding() {
        return Err(CmdError::invalid(
            "write-off must be positive and within the outstanding amount",
        ));
    }
    let wo = inv.written_off_minor + amount;
    let status = settled_status(
        inv.total_minor,
        inv.paid_minor,
        inv.credited_minor,
        wo,
        &inv.status,
    );
    sqlx::query(
        "update billing.invoices set written_off_minor = $2, status = $3 where note_id = $1",
    )
    .bind(i.id)
    .bind(wo)
    .bind(&status)
    .execute(&mut *ctx.tx)
    .await?;
    // GST on bad debt is not reversed here: that is a filing decision for the accountant.
    ledger::post(
        ctx,
        "write_off",
        i.id,
        &format!(
            "Write-off {}: {}",
            inv.number.as_deref().unwrap_or(""),
            i.reason
        ),
        &[
            (ledger::BAD_DEBT, amount, 0),
            (ledger::RECEIVABLES, 0, amount),
        ],
    )
    .await?;
    ctx.emit(
        "invoice.written_off",
        Some(i.id),
        json!({"amount_minor": amount, "reason": i.reason}),
    )
    .await?;
    invoice_detail(ctx, i.id).await
}

pub fn commands() -> Vec<Command> {
    use ToolGroup::{ManageInvoices, Read};
    vec![
        Command::new("invoice.create_draft", "Create a draft GST invoice for a contact. Lines may link work items (adds `bills` relations).", ManageInvoices, handler!(invoice_create_draft)).core(),
        Command::new("invoice.update_draft", "Edit a draft invoice (issued invoices are immutable; use credit notes).", ManageInvoices, handler!(invoice_update_draft)),
        Command::new("invoice.get", "Get an invoice with lines, payments and outstanding balance.", Read, handler!(invoice_get)),
        Command::new("invoice.list", "List invoices and credit notes.", Read, handler!(invoice_list)),
        Command::new("invoice.preview", "Preview the GST totals a draft invoice would get when issued (read-only).", Read, handler!(invoice_preview)),
        Command::new("invoice.issue", "Issue a draft: assigns the FY number, computes GST, posts the ledger entry. Agents need human confirmation.", ManageInvoices, handler!(invoice_issue)).confirm().core(),
        Command::new("invoice.generate_irn", "Generate the e-invoice IRN through the GSP (30-day window enforced for AATO above Rs 10 crore).", ManageInvoices, handler!(invoice_generate_irn)),
        Command::new("invoice.cancel_irn", "Cancel an IRN within 24 hours of acknowledgement.", ManageInvoices, handler!(invoice_cancel_irn)).confirm(),
        Command::new("invoice.cancel", "Cancel an unpaid issued invoice with a full credit note and reversal entry.", ManageInvoices, handler!(invoice_cancel)).confirm(),
        Command::new("credit_note.create", "Issue a (partial) credit note against an issued invoice.", ManageInvoices, handler!(credit_note_create)).confirm(),
        Command::new("payment.record", "Record a payment (full or partial) against an invoice and post it to the ledger.", ManageInvoices, handler!(payment_record)).confirm().core(),
        Command::new("invoice.write_off", "Write off an outstanding balance as bad debt.", ManageInvoices, handler!(invoice_write_off)).confirm().min_role(Role::Admin),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn numbers_and_settlement() {
        assert_eq!(format_number("INV", "2026-27", 1), "INV/2026-27/0001");
        assert_eq!(format_number("CN", "2026-27", 12345), "CN/2026-27/12345");
        assert_eq!(
            settled_status(100, 0, 0, 0, "irn_generated"),
            "irn_generated"
        );
        assert_eq!(settled_status(100, 40, 0, 0, "issued"), "partly_paid");
        assert_eq!(settled_status(100, 100, 0, 0, "issued"), "paid");
        assert_eq!(settled_status(100, 40, 60, 0, "partly_paid"), "paid");
        assert_eq!(settled_status(100, 0, 0, 100, "issued"), "paid");
    }

    #[test]
    fn line_parsing() {
        let l = |q: Option<&str>, p, h: Option<&str>| LineIn {
            description: "Work".into(),
            hsn_sac: h.map(Into::into),
            quantity: q.map(|q| serde_json::from_str(q).unwrap()),
            unit_price_minor: p,
            gst_rate_bp: None,
            item_note_id: None,
        };
        let ok = parse_lines(&[l(Some("2.5"), 100, Some("998314"))]).unwrap();
        assert_eq!(ok[0].g.qty.to_string(), "2.5");
        assert_eq!(ok[0].g.rate_bp, 1800);
        assert_eq!(
            parse_lines(&[l(None, 100, None)]).unwrap()[0].g.qty,
            Decimal::ONE
        );
        assert!(parse_lines(&[]).is_err());
        assert!(parse_lines(&[l(Some("0"), 100, None)]).is_err());
        assert!(parse_lines(&[l(None, -1, None)]).is_err());
        assert!(parse_lines(&[l(None, 1, Some("12"))]).is_err());
    }
}
