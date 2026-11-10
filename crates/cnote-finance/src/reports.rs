//! Finance reports and exports: ageing, GST summary, GSTR-1, Tally XML, trial balance.

use crate::billing::today_ist;
use chrono::{DateTime, Datelike, Duration, NaiveDate, Utc};
use cnote_core::access::readable_space_ids;
use cnote_core::{CmdError, CmdResult, Command, Ctx, Role, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::BTreeMap;
use uuid::Uuid;

// ---------- receivables ageing ----------

pub fn age_bucket(days: i64) -> &'static str {
    match days {
        d if d <= 30 => "0-30",
        d if d <= 60 => "31-60",
        d if d <= 90 => "61-90",
        _ => "90+",
    }
}

const BUCKETS: [&str; 4] = ["0-30", "31-60", "61-90", "90+"];

#[derive(Deserialize, JsonSchema)]
pub struct AsOf {
    /// Report date, default today.
    #[serde(default)]
    pub as_of: Option<NaiveDate>,
}

#[derive(sqlx::FromRow)]
struct OpenInvoice {
    id: Uuid,
    number: Option<String>,
    contact_id: Option<Uuid>,
    contact_name: Option<String>,
    invoice_date: NaiveDate,
    outstanding_minor: i64,
}

async fn receivables_ageing(ctx: &mut Ctx, i: AsOf) -> CmdResult<Value> {
    let as_of = i.as_of.unwrap_or_else(today_ist);
    let spaces = readable_space_ids(ctx).await?;
    let rows: Vec<OpenInvoice> = sqlx::query_as(
        "select i.note_id as id, i.number, i.contact_id, c.title as contact_name, i.invoice_date,
           i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor as outstanding_minor
         from billing.invoices i join core.notes n on n.id = i.note_id left join core.notes c on c.id = i.contact_id
         where i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid')
           and i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor > 0
           and i.invoice_date <= $1 and n.space_id = any($2)
         order by i.invoice_date",
    )
    .bind(as_of)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let mut totals: BTreeMap<&str, (i64, i64)> = BUCKETS.iter().map(|b| (*b, (0, 0))).collect();
    let mut invoices = vec![];
    for r in &rows {
        let days = (as_of - r.invoice_date).num_days();
        let b = age_bucket(days);
        let t = totals.get_mut(b).expect("bucket");
        t.0 += r.outstanding_minor;
        t.1 += 1;
        invoices.push(json!({"id": r.id, "number": r.number, "contact_id": r.contact_id, "contact": r.contact_name,
            "invoice_date": r.invoice_date, "age_days": days, "bucket": b, "outstanding_minor": r.outstanding_minor}));
    }
    Ok(json!({
        "as_of": as_of,
        "buckets": BUCKETS.iter().map(|b| json!({"bucket": b, "outstanding_minor": totals[b].0, "count": totals[b].1})).collect::<Vec<_>>(),
        "total_minor": rows.iter().map(|r| r.outstanding_minor).sum::<i64>(),
        "invoices": invoices,
    }))
}

// ---------- GST summary ----------

#[derive(Deserialize, JsonSchema)]
pub struct Range {
    pub from: NaiveDate,
    pub to: NaiveDate,
}

#[derive(Serialize, sqlx::FromRow)]
struct SummaryRow {
    period: String,
    gst_rate_bp: i32,
    taxable_minor: i64,
    cgst_minor: i64,
    sgst_minor: i64,
    igst_minor: i64,
}

/// Output tax by month and rate; credit notes count negative.
async fn gst_summary(ctx: &mut Ctx, i: Range) -> CmdResult<Value> {
    let spaces = readable_space_ids(ctx).await?;
    let rows: Vec<SummaryRow> = sqlx::query_as(
        "select to_char(i.invoice_date, 'YYYY-MM') as period, l.gst_rate_bp,
           sum(case when i.kind = 'credit_note' then -l.taxable_minor else l.taxable_minor end)::bigint as taxable_minor,
           sum(case when i.kind = 'credit_note' then -l.cgst_minor else l.cgst_minor end)::bigint as cgst_minor,
           sum(case when i.kind = 'credit_note' then -l.sgst_minor else l.sgst_minor end)::bigint as sgst_minor,
           sum(case when i.kind = 'credit_note' then -l.igst_minor else l.igst_minor end)::bigint as igst_minor
         from billing.invoice_lines l join billing.invoices i on i.note_id = l.invoice_id
           join core.notes n on n.id = i.note_id
         where i.status <> 'draft' and i.invoice_date between $1 and $2 and n.space_id = any($3)
         group by 1, 2 order by 1, 2",
    )
    .bind(i.from)
    .bind(i.to)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let sum = |f: fn(&SummaryRow) -> i64| rows.iter().map(f).sum::<i64>();
    Ok(json!({
        "from": i.from, "to": i.to, "rows": rows,
        "totals": {"taxable_minor": sum(|r| r.taxable_minor), "cgst_minor": sum(|r| r.cgst_minor),
                   "sgst_minor": sum(|r| r.sgst_minor), "igst_minor": sum(|r| r.igst_minor)},
    }))
}

// ---------- GSTR-1 ----------

/// Paise as a rupee amount for GSTN JSON (exact two-decimal text parsed into a JSON number).
pub fn rupees(paise: i64) -> Value {
    let s = format!(
        "{}{}.{:02}",
        if paise < 0 { "-" } else { "" },
        paise.abs() / 100,
        paise.abs() % 100
    );
    serde_json::from_str(&s).unwrap_or(Value::Null)
}

fn month_range(period: &str) -> CmdResult<(NaiveDate, NaiveDate)> {
    let bad = || CmdError::invalid("period must look like 2026-11");
    let (y, m) = period.split_once('-').ok_or_else(bad)?;
    let start = NaiveDate::from_ymd_opt(
        y.parse().map_err(|_| bad())?,
        m.parse().map_err(|_| bad())?,
        1,
    )
    .ok_or_else(bad)?;
    let next = if start.month() == 12 {
        NaiveDate::from_ymd_opt(start.year() + 1, 1, 1)
    } else {
        NaiveDate::from_ymd_opt(start.year(), start.month() + 1, 1)
    }
    .ok_or_else(bad)?;
    Ok((start, next - Duration::days(1)))
}

#[derive(Deserialize, JsonSchema)]
pub struct Period {
    /// Month as YYYY-MM.
    pub period: String,
}

#[derive(sqlx::FromRow)]
struct DocRow {
    id: Uuid,
    kind: String,
    number: String,
    invoice_date: NaiveDate,
    total_minor: i64,
    place_of_supply: String,
    buyer_gstin: Option<String>,
    reverse_charge: bool,
}

#[derive(sqlx::FromRow)]
struct RateRow {
    invoice_id: Uuid,
    gst_rate_bp: i32,
    taxable: i64,
    cgst: i64,
    sgst: i64,
    igst: i64,
}

fn item(num: usize, r: &RateRow, sign: i64) -> Value {
    json!({"num": num, "itm_det": {"rt": rupees(r.gst_rate_bp as i64), "txval": rupees(sign * r.taxable),
        "camt": rupees(sign * r.cgst), "samt": rupees(sign * r.sgst), "iamt": rupees(sign * r.igst)}})
}

/// GSTR-1 JSON sections: b2b (buyer has a GSTIN), b2cs (consumers, aggregated by place of supply and rate) and
/// cdnr (credit notes to registered buyers). b2cl (inter-state consumer invoices above Rs 2.5 lakh) is not split out.
async fn gstr1(ctx: &mut Ctx, i: Period) -> CmdResult<Value> {
    let (from, to) = month_range(&i.period)?;
    let spaces = readable_space_ids(ctx).await?;
    let settings: Value = sqlx::query_scalar("select settings from core.workspaces where id = $1")
        .bind(ctx.workspace_id())
        .fetch_one(&mut *ctx.tx)
        .await?;
    let supplier_state = settings["state_code"].as_str().unwrap_or("").to_string();
    let docs: Vec<DocRow> = sqlx::query_as(
        "select i.note_id as id, i.kind, i.number, i.invoice_date, i.total_minor, i.place_of_supply, i.buyer_gstin, i.reverse_charge
         from billing.invoices i join core.notes n on n.id = i.note_id
         where i.number is not null and i.invoice_date between $1 and $2 and n.space_id = any($3)
         order by i.number",
    )
    .bind(from)
    .bind(to)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let ids: Vec<Uuid> = docs.iter().map(|d| d.id).collect();
    let rates: Vec<RateRow> = sqlx::query_as(
        "select invoice_id, gst_rate_bp, sum(taxable_minor)::bigint as taxable, sum(cgst_minor)::bigint as cgst,
           sum(sgst_minor)::bigint as sgst, sum(igst_minor)::bigint as igst
         from billing.invoice_lines where invoice_id = any($1) group by invoice_id, gst_rate_bp order by gst_rate_bp",
    )
    .bind(&ids)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let items = |id: Uuid, sign: i64| -> Vec<Value> {
        rates
            .iter()
            .filter(|r| r.invoice_id == id)
            .enumerate()
            .map(|(n, r)| item(n + 1, r, sign))
            .collect()
    };
    let (mut b2b, mut cdnr): (BTreeMap<String, Vec<Value>>, BTreeMap<String, Vec<Value>>) =
        Default::default();
    let mut b2cs: BTreeMap<(String, i32), [i64; 4]> = BTreeMap::new();
    for d in &docs {
        let date = d.invoice_date.format("%d-%m-%Y").to_string();
        let rchrg = if d.reverse_charge { "Y" } else { "N" };
        match (&d.buyer_gstin, d.kind.as_str()) {
            (Some(g), "invoice") => b2b.entry(g.clone()).or_default().push(json!({
                "inum": d.number, "idt": date, "val": rupees(d.total_minor), "pos": d.place_of_supply,
                "rchrg": rchrg, "inv_typ": "R", "itms": items(d.id, 1)})),
            (Some(g), _) => cdnr.entry(g.clone()).or_default().push(json!({
                "ntty": "C", "nt_num": d.number, "nt_dt": date, "val": rupees(d.total_minor),
                "pos": d.place_of_supply, "rchrg": rchrg, "itms": items(d.id, 1)})),
            (None, kind) => {
                let sign = if kind == "credit_note" { -1 } else { 1 };
                for r in rates.iter().filter(|r| r.invoice_id == d.id) {
                    let e = b2cs.entry((d.place_of_supply.clone(), r.gst_rate_bp)).or_default();
                    for (acc, v) in e.iter_mut().zip([r.taxable, r.cgst, r.sgst, r.igst]) {
                        *acc += sign * v;
                    }
                }
            }
        }
    }
    let wrap = |m: BTreeMap<String, Vec<Value>>, key: &str| -> Vec<Value> {
        m.into_iter()
            .map(|(ctin, docs)| json!({"ctin": ctin, key: docs}))
            .collect()
    };
    Ok(json!({
        "gstin": settings["gstin"], "fp": format!("{:02}{}", from.month(), from.year()),
        "b2b": wrap(b2b, "inv"),
        "b2cs": b2cs.into_iter().map(|((pos, rt), a)| json!({
            "sply_ty": if pos == supplier_state { "INTRA" } else { "INTER" }, "pos": pos, "typ": "OE",
            "rt": rupees(rt as i64), "txval": rupees(a[0]), "camt": rupees(a[1]), "samt": rupees(a[2]), "iamt": rupees(a[3])
        })).collect::<Vec<_>>(),
        "cdnr": wrap(cdnr, "nt"),
    }))
}

// ---------- Tally ----------

pub fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

#[derive(sqlx::FromRow)]
struct Voucher {
    id: Uuid,
    source_type: String,
    posted_at: DateTime<Utc>,
    memo: Option<String>,
    number: Option<String>,
    party: Option<String>,
}

#[derive(sqlx::FromRow)]
struct VLine {
    entry_id: Uuid,
    name: String,
    debit_minor: i64,
    credit_minor: i64,
}

fn tally_amount(paise: i64) -> String {
    format!(
        "{}{}.{:02}",
        if paise < 0 { "-" } else { "" },
        paise.abs() / 100,
        paise.abs() % 100
    )
}

/// Tally import XML: one voucher per journal entry. Debits use ISDEEMEDPOSITIVE=Yes with a negative amount.
async fn export_tally(ctx: &mut Ctx, i: Range) -> CmdResult<Value> {
    let vouchers: Vec<Voucher> = sqlx::query_as(
        "select e.id, e.source_type, e.posted_at, e.memo, i.number, c.title as party
         from billing.journal_entries e
           left join billing.payments p on e.source_type = 'payment' and p.id = e.source_id
           left join billing.invoices i on i.note_id = case when e.source_type = 'payment' then p.invoice_id else e.source_id end
           left join core.notes c on c.id = i.contact_id
         where e.posted_at >= $1::date and e.posted_at < $2::date + 1 order by e.posted_at, e.id",
    )
    .bind(i.from)
    .bind(i.to)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let ids: Vec<Uuid> = vouchers.iter().map(|v| v.id).collect();
    let lines: Vec<VLine> = sqlx::query_as(
        "select l.entry_id, a.name, l.debit_minor, l.credit_minor from billing.journal_lines l
           join billing.accounts a on a.code = l.account_code and a.workspace_id = l.workspace_id
         where l.entry_id = any($1) order by l.id",
    )
    .bind(&ids)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let mut xml = String::from(
        "<ENVELOPE><HEADER><TALLYREQUEST>Import Data</TALLYREQUEST></HEADER><BODY><IMPORTDATA>\
         <REQUESTDESC><REPORTNAME>Vouchers</REPORTNAME></REQUESTDESC><REQUESTDATA>\n",
    );
    for v in &vouchers {
        let vtype = match v.source_type.as_str() {
            "invoice" => "Sales",
            "credit_note" => "Credit Note",
            "payment" => "Receipt",
            _ => "Journal",
        };
        let date = (v.posted_at + Duration::minutes(330)).format("%Y%m%d");
        let party = xml_escape(v.party.as_deref().unwrap_or(""));
        xml += &format!(
            "<TALLYMESSAGE xmlns:UDF=\"TallyUDF\"><VOUCHER VCHTYPE=\"{vtype}\" ACTION=\"Create\">\
             <DATE>{date}</DATE><VOUCHERTYPENAME>{vtype}</VOUCHERTYPENAME><VOUCHERNUMBER>{}</VOUCHERNUMBER>\
             <PARTYLEDGERNAME>{party}</PARTYLEDGERNAME><NARRATION>{}</NARRATION>",
            xml_escape(v.number.as_deref().unwrap_or("")),
            xml_escape(v.memo.as_deref().unwrap_or(""))
        );
        for l in lines.iter().filter(|l| l.entry_id == v.id) {
            let (positive, amount) = if l.debit_minor > 0 {
                ("Yes", -l.debit_minor)
            } else {
                ("No", l.credit_minor)
            };
            xml += &format!(
                "<ALLLEDGERENTRIES.LIST><LEDGERNAME>{}</LEDGERNAME><ISDEEMEDPOSITIVE>{positive}</ISDEEMEDPOSITIVE>\
                 <AMOUNT>{}</AMOUNT></ALLLEDGERENTRIES.LIST>",
                xml_escape(&l.name),
                tally_amount(amount)
            );
        }
        xml += "</VOUCHER></TALLYMESSAGE>\n";
    }
    xml += "</REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>";
    Ok(json!({"vouchers": vouchers.len(), "xml": xml}))
}

// ---------- trial balance ----------

#[derive(Serialize, sqlx::FromRow)]
struct TbRow {
    code: String,
    name: String,
    kind: String,
    debit_minor: i64,
    credit_minor: i64,
}

async fn trial_balance(ctx: &mut Ctx, i: AsOf) -> CmdResult<Value> {
    let as_of = i.as_of.unwrap_or_else(today_ist);
    let rows: Vec<TbRow> = sqlx::query_as(
        "select a.code, a.name, a.kind, coalesce(sum(l.debit_minor), 0)::bigint as debit_minor,
           coalesce(sum(l.credit_minor), 0)::bigint as credit_minor
         from billing.accounts a
           left join (select l.* from billing.journal_lines l join billing.journal_entries e on e.id = l.entry_id
                      where e.posted_at < $1::date + 1) l on l.account_code = a.code
         group by a.code, a.name, a.kind order by a.code",
    )
    .bind(as_of)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let (d, c) = rows
        .iter()
        .fold((0, 0), |(d, c), r| (d + r.debit_minor, c + r.credit_minor));
    Ok(json!({
        "as_of": as_of,
        "accounts": rows.iter().map(|r| json!({"code": r.code, "name": r.name, "kind": r.kind,
            "debit_minor": r.debit_minor, "credit_minor": r.credit_minor, "balance_minor": r.debit_minor - r.credit_minor})).collect::<Vec<_>>(),
        "total_debit_minor": d, "total_credit_minor": c, "net_minor": d - c,
    }))
}

pub fn commands() -> Vec<Command> {
    use ToolGroup::Read;
    vec![
        Command::new(
            "report.receivables_ageing",
            "Outstanding invoices bucketed 0-30 / 31-60 / 61-90 / 90+ days by invoice age.",
            Read,
            handler!(receivables_ageing),
        )
        .min_role(Role::Member),
        Command::new(
            "report.gst_summary",
            "Output GST by month and rate for a date range (credit notes negative).",
            Read,
            handler!(gst_summary),
        )
        .min_role(Role::Member),
        Command::new(
            "report.gstr1",
            "GSTR-1 JSON for a month: B2B, B2C and credit/debit notes.",
            Read,
            handler!(gstr1),
        )
        .min_role(Role::Member),
        Command::new(
            "export.tally",
            "Tally Prime XML vouchers (sales, receipts, credit notes) for a date range.",
            Read,
            handler!(export_tally),
        )
        .min_role(Role::Admin),
        Command::new(
            "ledger.trial_balance",
            "Trial balance per account; debits and credits always net to zero.",
            Read,
            handler!(trial_balance),
        )
        .min_role(Role::Admin),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn buckets() {
        for (d, b) in [
            (-5, "0-30"),
            (0, "0-30"),
            (30, "0-30"),
            (31, "31-60"),
            (60, "31-60"),
            (61, "61-90"),
            (90, "61-90"),
            (91, "90+"),
        ] {
            assert_eq!(age_bucket(d), b, "{d}");
        }
    }

    #[test]
    fn money_json() {
        assert_eq!(rupees(118_050).to_string(), "1180.5");
        assert_eq!(rupees(5).to_string(), "0.05");
        assert_eq!(rupees(-250).to_string(), "-2.5");
        assert_eq!(rupees(1800).to_string(), "18.0");
        assert_eq!(tally_amount(-118_000), "-1180.00");
    }

    #[test]
    fn periods_and_xml() {
        let (f, t) = month_range("2026-02").unwrap();
        assert_eq!(
            (f.to_string(), t.to_string()),
            ("2026-02-01".into(), "2026-02-28".into())
        );
        assert_eq!(month_range("2026-12").unwrap().1.to_string(), "2026-12-31");
        assert!(month_range("2026-13").is_err() && month_range("x").is_err());
        assert_eq!(
            xml_escape("A & B <\"c\">"),
            "A &amp; B &lt;&quot;c&quot;&gt;"
        );
    }
}
