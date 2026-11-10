//! CRM objects: contacts and deals, both typed Notes (ADR-002).

use crate::gst;
use chrono::{DateTime, NaiveDate, Utc};
use cnote_core::access::{assert_note_access, readable_space_ids};
use cnote_core::notes::{CreateNote, insert_note};
use cnote_core::{CmdError, CmdResult, Command, Ctx, ToolGroup, handler};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::json;
use uuid::Uuid;

pub const STAGES: [&str; 6] = [
    "lead",
    "qualified",
    "proposal",
    "negotiation",
    "won",
    "lost",
];

fn gst_err(e: gst::GstError) -> CmdError {
    CmdError::invalid(e.to_string())
}

// ---------- contacts ----------

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct ContactOut {
    pub id: Uuid,
    pub space_id: Uuid,
    pub name: String,
    pub kind: String,
    pub email: Option<String>,
    pub phone: Option<String>,
    pub company: Option<String>,
    pub gstin: Option<String>,
    pub state_code: Option<String>,
    pub billing_address: Option<String>,
}

const CONTACT_SQL: &str =
    "select n.id, n.space_id, n.title as name, c.kind, c.email, c.phone, c.company, c.gstin,
       c.state_code, c.billing_address
     from crm.contacts c join core.notes n on n.id = c.note_id";

async fn load_contact(ctx: &mut Ctx, id: Uuid) -> CmdResult<ContactOut> {
    sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "{CONTACT_SQL} where c.note_id = $1 and n.deleted_at is null"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("contact"))
}

/// Normalise GSTIN and derive/validate the state code.
fn check_tax_ids(
    gstin: Option<String>,
    state: Option<String>,
) -> CmdResult<(Option<String>, Option<String>)> {
    let gstin = match gstin.as_deref().map(str::trim).filter(|g| !g.is_empty()) {
        Some(g) => Some(gst::validate_gstin(g).map_err(gst_err)?),
        None => None,
    };
    let state = state.filter(|s| !s.trim().is_empty());
    if let Some(s) = &state {
        gst::validate_state(s).map_err(gst_err)?;
    }
    let state = state.or_else(|| gstin.as_deref().map(|g| gst::gstin_state(g).to_string()));
    if let (Some(g), Some(s)) = (&gstin, &state)
        && gst::gstin_state(g) != s
    {
        return Err(CmdError::invalid("state_code does not match the GSTIN"));
    }
    Ok((gstin, state))
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateContact {
    /// Space that holds the contact.
    pub space_id: Uuid,
    /// Person or company name.
    pub name: String,
    /// person | company
    #[serde(default)]
    pub kind: Option<String>,
    #[serde(default)]
    pub email: Option<String>,
    #[serde(default)]
    pub phone: Option<String>,
    #[serde(default)]
    pub company: Option<String>,
    /// 15-character GSTIN; validated including checksum. Needed for B2B e-invoicing.
    #[serde(default)]
    pub gstin: Option<String>,
    /// Two-digit GST state code (derived from the GSTIN when omitted). Decides CGST/SGST vs IGST.
    #[serde(default)]
    pub state_code: Option<String>,
    #[serde(default)]
    pub billing_address: Option<String>,
}

async fn contact_create(ctx: &mut Ctx, i: CreateContact) -> CmdResult<ContactOut> {
    let kind = i.kind.unwrap_or_else(|| "person".into());
    if kind != "person" && kind != "company" {
        return Err(CmdError::invalid("kind must be person or company"));
    }
    if i.name.trim().is_empty() {
        return Err(CmdError::invalid("name is required"));
    }
    let (gstin, state) = check_tax_ids(i.gstin, i.state_code)?;
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: i.space_id,
            title: i.name,
            parent_id: None,
            content_md: None,
            types: vec!["contact".into()],
            owner_id: None,
            verify_by: None,
        },
    )
    .await?;
    sqlx::query(
        "insert into crm.contacts (note_id, workspace_id, kind, email, phone, company, gstin, state_code, billing_address)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(&kind)
    .bind(&i.email)
    .bind(&i.phone)
    .bind(&i.company)
    .bind(&gstin)
    .bind(&state)
    .bind(&i.billing_address)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "contact.created",
        Some(note.id),
        json!({"name": note.title}),
    )
    .await?;
    load_contact(ctx, note.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateContact {
    pub id: Uuid,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub email: Option<String>,
    #[serde(default)]
    pub phone: Option<String>,
    #[serde(default)]
    pub company: Option<String>,
    #[serde(default)]
    pub gstin: Option<String>,
    #[serde(default)]
    pub state_code: Option<String>,
    #[serde(default)]
    pub billing_address: Option<String>,
}

async fn contact_update(ctx: &mut Ctx, i: UpdateContact) -> CmdResult<ContactOut> {
    assert_note_access(ctx, i.id, true).await?;
    let cur = load_contact(ctx, i.id).await?;
    let (gstin, state) = if i.gstin.is_some() || i.state_code.is_some() {
        // Changing the GSTIN without a state re-derives the state from it.
        let state = i
            .state_code
            .or_else(|| i.gstin.is_none().then(|| cur.state_code.clone()).flatten());
        check_tax_ids(i.gstin.or(cur.gstin.clone()), state)?
    } else {
        (cur.gstin.clone(), cur.state_code.clone())
    };
    sqlx::query(
        "update crm.contacts set email = coalesce($2, email), phone = coalesce($3, phone), company = coalesce($4, company),
           gstin = $5, state_code = $6, billing_address = coalesce($7, billing_address) where note_id = $1",
    )
    .bind(i.id)
    .bind(&i.email)
    .bind(&i.phone)
    .bind(&i.company)
    .bind(&gstin)
    .bind(&state)
    .bind(&i.billing_address)
    .execute(&mut *ctx.tx)
    .await?;
    sqlx::query("update core.notes set title = coalesce($2, title), updated_at = now(), version = version + 1 where id = $1")
        .bind(i.id)
        .bind(i.name.as_deref().map(str::trim).filter(|n| !n.is_empty()))
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit("contact.updated", Some(i.id), json!({})).await?;
    load_contact(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

async fn contact_get(ctx: &mut Ctx, i: ById) -> CmdResult<ContactOut> {
    assert_note_access(ctx, i.id, false).await?;
    load_contact(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct ListContacts {
    #[serde(default)]
    pub space_id: Option<Uuid>,
    /// Substring match on name, email or company.
    #[serde(default)]
    pub q: Option<String>,
    #[serde(default)]
    pub limit: Option<i64>,
}

async fn contact_list(ctx: &mut Ctx, i: ListContacts) -> CmdResult<Vec<ContactOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "{CONTACT_SQL} where n.deleted_at is null and n.space_id = any($1) and ($2::uuid is null or n.space_id = $2)
           and ($3::text is null or n.title ilike '%'||$3||'%' or c.email ilike '%'||$3||'%' or c.company ilike '%'||$3||'%')
         order by n.title limit $4"
    )))
    .bind(spaces)
    .bind(i.space_id)
    .bind(i.q.filter(|q| !q.trim().is_empty()))
    .bind(i.limit.unwrap_or(100).clamp(1, 500))
    .fetch_all(&mut *ctx.tx)
    .await?)
}

// ---------- deals ----------

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct DealOut {
    pub id: Uuid,
    pub space_id: Uuid,
    pub title: String,
    pub stage: String,
    pub amount_minor: i64,
    pub currency: String,
    pub contact_id: Option<Uuid>,
    pub owner_id: Option<Uuid>,
    pub expected_close: Option<NaiveDate>,
    pub probability: i32,
    pub closed_at: Option<DateTime<Utc>>,
}

const DEAL_SQL: &str = "select n.id, n.space_id, n.title, d.stage, d.amount_minor, d.currency::text as currency, d.contact_id,
       d.owner_id, d.expected_close, d.probability, d.closed_at
     from crm.deals d join core.notes n on n.id = d.note_id";

async fn load_deal(ctx: &mut Ctx, id: Uuid) -> CmdResult<DealOut> {
    sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "{DEAL_SQL} where d.note_id = $1 and n.deleted_at is null"
    )))
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("deal"))
}

fn check_stage(s: &str) -> CmdResult<()> {
    if STAGES.contains(&s) {
        Ok(())
    } else {
        Err(CmdError::invalid(format!(
            "stage must be one of {}",
            STAGES.join(", ")
        )))
    }
}

fn check_deal_fields(amount: Option<i64>, probability: Option<i32>) -> CmdResult<()> {
    if amount.is_some_and(|a| a < 0) {
        return Err(CmdError::invalid("amount_minor cannot be negative"));
    }
    if probability.is_some_and(|p| !(0..=100).contains(&p)) {
        return Err(CmdError::invalid("probability must be 0-100"));
    }
    Ok(())
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateDeal {
    pub space_id: Uuid,
    pub title: String,
    #[serde(default)]
    pub contact_id: Option<Uuid>,
    /// Deal value in paise.
    #[serde(default)]
    pub amount_minor: Option<i64>,
    #[serde(default)]
    pub currency: Option<String>,
    /// lead | qualified | proposal | negotiation | won | lost (default lead)
    #[serde(default)]
    pub stage: Option<String>,
    #[serde(default)]
    pub expected_close: Option<NaiveDate>,
    /// 0-100
    #[serde(default)]
    pub probability: Option<i32>,
    #[serde(default)]
    pub owner_id: Option<Uuid>,
}

async fn deal_create(ctx: &mut Ctx, i: CreateDeal) -> CmdResult<DealOut> {
    let stage = i.stage.unwrap_or_else(|| "lead".into());
    check_stage(&stage)?;
    check_deal_fields(i.amount_minor, i.probability)?;
    if let Some(c) = i.contact_id {
        assert_note_access(ctx, c, false).await?;
        load_contact(ctx, c).await?;
    }
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: i.space_id,
            title: i.title,
            parent_id: None,
            content_md: None,
            types: vec!["deal".into()],
            owner_id: i.owner_id,
            verify_by: None,
        },
    )
    .await?;
    let (prob, closed) = stage_defaults(&stage, i.probability);
    sqlx::query(
        "insert into crm.deals (note_id, workspace_id, stage, amount_minor, currency, contact_id, owner_id, expected_close, probability, closed_at)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9, case when $10 then now() end)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(&stage)
    .bind(i.amount_minor.unwrap_or(0))
    .bind(i.currency.unwrap_or_else(|| "INR".into()))
    .bind(i.contact_id)
    .bind(i.owner_id.or(ctx.principal.user_id))
    .bind(i.expected_close)
    .bind(prob)
    .bind(closed)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit("deal.created", Some(note.id), json!({"stage": stage}))
        .await?;
    load_deal(ctx, note.id).await
}

/// (probability, closed) for a stage; won/lost pin the probability.
fn stage_defaults(stage: &str, requested: Option<i32>) -> (i32, bool) {
    match stage {
        "won" => (100, true),
        "lost" => (0, true),
        _ => (requested.unwrap_or(10), false),
    }
}

#[derive(Deserialize, JsonSchema)]
pub struct UpdateDeal {
    pub id: Uuid,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub contact_id: Option<Uuid>,
    #[serde(default)]
    pub amount_minor: Option<i64>,
    #[serde(default)]
    pub expected_close: Option<NaiveDate>,
    #[serde(default)]
    pub probability: Option<i32>,
    #[serde(default)]
    pub owner_id: Option<Uuid>,
}

async fn deal_update(ctx: &mut Ctx, i: UpdateDeal) -> CmdResult<DealOut> {
    assert_note_access(ctx, i.id, true).await?;
    load_deal(ctx, i.id).await?;
    check_deal_fields(i.amount_minor, i.probability)?;
    if let Some(c) = i.contact_id {
        assert_note_access(ctx, c, false).await?;
        load_contact(ctx, c).await?;
    }
    sqlx::query(
        "update crm.deals set contact_id = coalesce($2, contact_id), amount_minor = coalesce($3, amount_minor),
           expected_close = coalesce($4, expected_close), probability = coalesce($5, probability),
           owner_id = coalesce($6, owner_id) where note_id = $1",
    )
    .bind(i.id)
    .bind(i.contact_id)
    .bind(i.amount_minor)
    .bind(i.expected_close)
    .bind(i.probability)
    .bind(i.owner_id)
    .execute(&mut *ctx.tx)
    .await?;
    sqlx::query("update core.notes set title = coalesce($2, title), updated_at = now(), version = version + 1 where id = $1")
        .bind(i.id)
        .bind(i.title.as_deref().map(str::trim).filter(|n| !n.is_empty()))
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit("deal.updated", Some(i.id), json!({})).await?;
    load_deal(ctx, i.id).await
}

#[derive(Deserialize, JsonSchema)]
pub struct MoveStage {
    pub id: Uuid,
    /// lead | qualified | proposal | negotiation | won | lost
    pub stage: String,
}

async fn deal_move_stage(ctx: &mut Ctx, i: MoveStage) -> CmdResult<DealOut> {
    check_stage(&i.stage)?;
    assert_note_access(ctx, i.id, true).await?;
    let before = load_deal(ctx, i.id).await?;
    if before.stage == i.stage {
        return Ok(before);
    }
    let (prob, closed) = stage_defaults(&i.stage, Some(before.probability));
    sqlx::query(
        "update crm.deals set stage = $2, probability = $3, closed_at = case when $4 then now() end where note_id = $1",
    )
    .bind(i.id)
    .bind(&i.stage)
    .bind(prob)
    .bind(closed)
    .execute(&mut *ctx.tx)
    .await?;
    let deal = load_deal(ctx, i.id).await?;
    ctx.emit(
        "deal.stage_changed",
        Some(i.id),
        json!({"from": before.stage, "to": deal.stage}),
    )
    .await?;
    if deal.stage == "won" {
        ctx.emit(
            "deal.won",
            Some(i.id),
            json!({"amount_minor": deal.amount_minor, "currency": deal.currency, "contact_id": deal.contact_id}),
        )
        .await?;
    }
    Ok(deal)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListDeals {
    #[serde(default)]
    pub space_id: Option<Uuid>,
    #[serde(default)]
    pub stage: Option<String>,
    #[serde(default)]
    pub contact_id: Option<Uuid>,
    #[serde(default)]
    pub limit: Option<i64>,
}

async fn deal_list(ctx: &mut Ctx, i: ListDeals) -> CmdResult<Vec<DealOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "{DEAL_SQL} where n.deleted_at is null and n.space_id = any($1) and ($2::uuid is null or n.space_id = $2)
           and ($3::text is null or d.stage = $3) and ($4::uuid is null or d.contact_id = $4)
         order by n.updated_at desc limit $5"
    )))
    .bind(spaces)
    .bind(i.space_id)
    .bind(i.stage)
    .bind(i.contact_id)
    .bind(i.limit.unwrap_or(100).clamp(1, 500))
    .fetch_all(&mut *ctx.tx)
    .await?)
}

pub fn commands() -> Vec<Command> {
    use ToolGroup::{ManageCrm, Read};
    vec![
        Command::new(
            "contact.create",
            "Create a CRM contact (person or company) with optional GSTIN and state code.",
            ManageCrm,
            handler!(contact_create),
        )
        .core(),
        Command::new(
            "contact.update",
            "Update a contact's details.",
            ManageCrm,
            handler!(contact_update),
        ),
        Command::new(
            "contact.get",
            "Get one contact.",
            Read,
            handler!(contact_get),
        ),
        Command::new(
            "contact.list",
            "List/search contacts.",
            Read,
            handler!(contact_list),
        ),
        Command::new(
            "deal.create",
            "Create a deal in the pipeline.",
            ManageCrm,
            handler!(deal_create),
        ),
        Command::new(
            "deal.update",
            "Update a deal's amount, contact, close date or owner.",
            ManageCrm,
            handler!(deal_update),
        ),
        Command::new(
            "deal.move_stage",
            "Move a deal to another pipeline stage (emits deal.stage_changed, and deal.won).",
            ManageCrm,
            handler!(deal_move_stage),
        )
        .core(),
        Command::new(
            "deal.list",
            "List deals, optionally by stage or contact.",
            Read,
            handler!(deal_list),
        ),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stage_rules() {
        assert!(STAGES.iter().all(|s| check_stage(s).is_ok()));
        assert!(check_stage("closed").is_err());
        assert_eq!(stage_defaults("won", Some(5)), (100, true));
        assert_eq!(stage_defaults("lost", None), (0, true));
        assert_eq!(stage_defaults("proposal", Some(40)), (40, false));
    }

    #[test]
    fn tax_ids() {
        let g = gst::complete_gstin("29ABCDE1234F1Z");
        let (gstin, state) = check_tax_ids(Some(g.clone()), None).unwrap();
        assert_eq!(
            (gstin.as_deref(), state.as_deref()),
            (Some(g.as_str()), Some("29"))
        );
        assert!(check_tax_ids(Some(g), Some("27".into())).is_err());
        assert!(check_tax_ids(None, Some("00".into())).is_err());
        assert!(check_tax_ids(Some("bad".into()), None).is_err());
        assert_eq!(check_tax_ids(None, None).unwrap(), (None, None));
    }
}
