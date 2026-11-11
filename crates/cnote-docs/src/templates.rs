//! Templates with data bindings (ADR-006). A template is page or board content containing `{{path}}` placeholders;
//! rendering resolves them from a CRM, billing or projects record the caller can read and creates a new note.
//! Placeholders are plain path lookups, never expressions.

use chrono::{FixedOffset, NaiveDate, Utc};
use cnote_core::access::{assert_note_access, readable_space_ids};
use cnote_core::handler;
use cnote_core::notes::{CreateNote, insert_note};
use cnote_core::{CmdError, CmdResult, Command, Ctx, Role, ToolGroup};
use cnote_db::Tx;
use futures::future::BoxFuture;
use regex::Regex;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::{BTreeSet, HashMap};
use std::sync::OnceLock;
use uuid::Uuid;

const KINDS: [&str; 5] = ["none", "contact", "deal", "invoice", "item"];

type Field = (&'static str, &'static str);

const COMMON: &[Field] = &[
    ("workspace.name", "Workspace name"),
    ("today", "Today's date (IST)"),
    ("user.name", "Your name"),
];
const CONTACT: &[Field] = &[
    ("contact.name", "Contact name"),
    ("contact.email", "Contact email"),
    ("contact.phone", "Contact phone"),
    ("contact.company", "Contact company"),
    ("contact.gstin", "Contact GSTIN"),
    ("contact.address", "Billing address"),
];
const DEAL: &[Field] = &[
    ("deal.title", "Deal title"),
    ("deal.value", "Deal value"),
    ("deal.currency", "Deal currency"),
    ("deal.stage", "Deal stage"),
    ("deal.expected_close", "Expected close date"),
    ("deal.probability", "Win probability"),
];
const INVOICE: &[Field] = &[
    ("invoice.number", "Invoice number"),
    ("invoice.status", "Invoice status"),
    ("invoice.date", "Invoice date"),
    ("invoice.due_date", "Due date"),
    ("invoice.subtotal", "Subtotal"),
    ("invoice.tax", "GST total"),
    ("invoice.total", "Invoice total"),
    ("invoice.paid", "Amount paid"),
    ("invoice.balance", "Balance due"),
];
const ITEM: &[Field] = &[
    ("item.key", "Item key"),
    ("item.title", "Item title"),
    ("item.status", "Item status"),
    ("item.priority", "Item priority"),
    ("item.type", "Item type"),
    ("item.due_date", "Due date"),
    ("item.assignee", "Assignee"),
];

/// The one place that says which placeholders each source kind offers (also served by `template.fields`).
fn catalogue(kind: &str) -> Vec<Field> {
    let own: &[&[Field]] = match kind {
        "contact" => &[CONTACT],
        "deal" => &[DEAL, CONTACT],
        "invoice" => &[INVOICE, CONTACT],
        "item" => &[ITEM],
        _ => &[],
    };
    COMMON
        .iter()
        .chain(own.iter().flat_map(|f| f.iter()))
        .copied()
        .collect()
}

// ---------- pure: parsing, substitution, formatting ----------

fn placeholder_re() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"\{\{\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\}\}")
            .expect("static regex")
    })
}

/// Replace every known placeholder in one pass (values are never re-scanned). Unknown paths stay as written
/// and are collected in `missing`.
fn substitute(text: &str, vals: &Vals, missing: &mut BTreeSet<String>) -> String {
    placeholder_re()
        .replace_all(text, |c: &regex::Captures| match vals.get(&c[1]) {
            Some(v) => v.clone(),
            None => {
                missing.insert(c[1].to_string());
                c[0].to_string()
            }
        })
        .into_owned()
}

fn walk_strings(v: &mut Value, f: &mut impl FnMut(&str) -> String) {
    match v {
        Value::String(s) => *s = f(s),
        Value::Array(a) => a.iter_mut().for_each(|x| walk_strings(x, f)),
        Value::Object(o) => o.values_mut().for_each(|x| walk_strings(x, f)),
        _ => {}
    }
}

/// Boards keep a tldraw snapshot in a ```tldraw fence. Substitute inside the JSON string values so that
/// quotes or newlines in a value cannot break the snapshot.
fn render_board(md: &str, vals: &Vals, missing: &mut BTreeSet<String>) -> CmdResult<String> {
    static FENCE: OnceLock<Regex> = OnceLock::new();
    let fence =
        FENCE.get_or_init(|| Regex::new(r"(?s)```tldraw\n(.*?)\n```").expect("static regex"));
    let Some(m) = fence.captures(md) else {
        return Ok(substitute(md, vals, missing));
    };
    let body = m.get(1).unwrap();
    let mut snap: Value = serde_json::from_str(body.as_str())
        .map_err(|_| CmdError::invalid("the board snapshot in this template is not valid JSON"))?;
    walk_strings(&mut snap, &mut |s| substitute(s, vals, missing));
    let mut out = md.to_string();
    out.replace_range(body.range(), &snap.to_string());
    Ok(out)
}

fn group_digits(d: &str, indian: bool) -> String {
    let mut out = Vec::with_capacity(d.len() + d.len() / 2);
    for (i, &c) in d.as_bytes().iter().rev().enumerate() {
        let sep = if indian {
            i == 3 || (i > 3 && (i - 3) % 2 == 0)
        } else {
            i > 0 && i % 3 == 0
        };
        if sep {
            out.push(b',');
        }
        out.push(c);
    }
    out.reverse();
    String::from_utf8(out).expect("ascii digits")
}

/// i64 minor units to text: Indian grouping with the rupee sign for INR, otherwise "USD 1,234.50".
fn money(minor: i64, currency: &str) -> String {
    let abs = minor.unsigned_abs();
    let inr = currency == "INR";
    let whole = group_digits(&(abs / 100).to_string(), inr);
    let sign = if minor < 0 { "-" } else { "" };
    let sym = if inr {
        "₹".to_string()
    } else {
        format!("{currency} ")
    };
    format!("{sign}{sym}{whole}.{:02}", abs % 100)
}

fn date(d: NaiveDate) -> String {
    d.format("%-d %b %Y").to_string()
}

fn today_ist() -> NaiveDate {
    Utc::now()
        .with_timezone(&FixedOffset::east_opt(19_800).expect("IST offset"))
        .date_naive()
}

// ---------- resolving a source record ----------

type Vals = HashMap<String, String>;

fn put(v: &mut Vals, k: &str, x: impl Into<String>) {
    v.insert(k.to_string(), x.into());
}
fn opt(x: Option<String>) -> String {
    x.unwrap_or_default()
}
fn opt_date(x: Option<NaiveDate>) -> String {
    x.map(date).unwrap_or_default()
}

/// contact.* values; only when the caller can read the contact's space.
async fn contact_values(ctx: &mut Ctx, id: Uuid, v: &mut Vals) -> CmdResult<()> {
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = cnote_core::access::viewer(ctx);
    type Row = (
        String,
        Option<String>,
        Option<String>,
        Option<String>,
        Option<String>,
        Option<String>,
    );
    let row: Option<Row> = sqlx::query_as(
        "select n.title, c.email, c.phone, c.company, c.gstin, c.billing_address
         from crm.contacts c join core.notes n on n.id = c.note_id
         where c.note_id = $1 and n.deleted_at is null and n.space_id = any($2)
           and core.note_access(n.id, $3, $4, $2) <> 'none'",
    )
    .bind(id)
    .bind(&spaces)
    .bind(uid)
    .bind(admin)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    if let Some((name, email, phone, company, gstin, addr)) = row {
        put(v, "contact.name", name);
        put(v, "contact.email", opt(email));
        put(v, "contact.phone", opt(phone));
        put(v, "contact.company", opt(company));
        put(v, "contact.gstin", opt(gstin));
        put(v, "contact.address", opt(addr));
    }
    Ok(())
}

/// Every placeholder value for `kind`/`source`. The caller must be able to read the source note.
async fn resolve(ctx: &mut Ctx, kind: &str, source: Option<Uuid>) -> CmdResult<Vals> {
    let mut v = Vals::new();
    let ws: String = sqlx::query_scalar("select name from core.workspaces where id = $1")
        .bind(ctx.workspace_id())
        .fetch_one(&mut *ctx.tx)
        .await?;
    put(&mut v, "workspace.name", ws);
    put(&mut v, "today", date(today_ist()));
    let user: Option<String> = sqlx::query_scalar("select name from core.users where id = $1")
        .bind(ctx.principal.user_id)
        .fetch_optional(&mut *ctx.tx)
        .await?;
    put(&mut v, "user.name", opt(user));
    let Some(id) = source else { return Ok(v) };
    assert_note_access(ctx, id, false).await?;
    let wrong = || CmdError::invalid(format!("the source record is not a {kind}"));
    match kind {
        "contact" => {
            contact_values(ctx, id, &mut v).await?;
            if !v.contains_key("contact.name") {
                return Err(wrong());
            }
        }
        "deal" => {
            type Row = (
                String,
                i64,
                String,
                String,
                Option<NaiveDate>,
                i32,
                Option<Uuid>,
            );
            let r: Row = sqlx::query_as(
                "select n.title, d.amount_minor, d.currency::text, d.stage, d.expected_close, d.probability, d.contact_id
                 from crm.deals d join core.notes n on n.id = d.note_id where d.note_id = $1 and n.deleted_at is null",
            )
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?
            .ok_or_else(wrong)?;
            put(&mut v, "deal.title", r.0);
            put(&mut v, "deal.value", money(r.1, &r.2));
            put(&mut v, "deal.currency", r.2);
            put(&mut v, "deal.stage", r.3);
            put(&mut v, "deal.expected_close", opt_date(r.4));
            put(&mut v, "deal.probability", format!("{}%", r.5));
            if let Some(c) = r.6 {
                contact_values(ctx, c, &mut v).await?;
            }
        }
        "invoice" => {
            type Row = (
                Option<String>,
                String,
                String,
                Option<NaiveDate>,
                Option<NaiveDate>,
                i64,
                i64,
                i64,
                i64,
                i64,
                Option<Uuid>,
            );
            let r: Row = sqlx::query_as(
                "select i.number, i.status, i.currency::text, i.invoice_date, i.due_date, i.subtotal_minor,
                        i.cgst_minor + i.sgst_minor + i.igst_minor, i.total_minor, i.paid_minor, i.total_minor - i.paid_minor, i.contact_id
                 from billing.invoices i join core.notes n on n.id = i.note_id where i.note_id = $1 and n.deleted_at is null",
            )
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?
            .ok_or_else(wrong)?;
            let (number, status, cur, d, due, sub, tax, total, paid, bal, contact) = r;
            put(
                &mut v,
                "invoice.number",
                number.unwrap_or_else(|| "DRAFT".into()),
            );
            put(&mut v, "invoice.status", status);
            put(&mut v, "invoice.date", opt_date(d));
            put(&mut v, "invoice.due_date", opt_date(due));
            put(&mut v, "invoice.subtotal", money(sub, &cur));
            put(&mut v, "invoice.tax", money(tax, &cur));
            put(&mut v, "invoice.total", money(total, &cur));
            put(&mut v, "invoice.paid", money(paid, &cur));
            put(&mut v, "invoice.balance", money(bal, &cur));
            if let Some(c) = contact {
                contact_values(ctx, c, &mut v).await?;
            }
        }
        "item" => {
            type Row = (
                String,
                String,
                String,
                String,
                String,
                Option<NaiveDate>,
                Option<String>,
            );
            let r: Row = sqlx::query_as(
                "select i.key, n.title, i.status, i.priority, i.item_type, i.due_date,
                        (select u.name from core.users u where u.id = i.assignee_id)
                 from projects.items i join core.notes n on n.id = i.note_id where i.note_id = $1 and n.deleted_at is null",
            )
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?
            .ok_or_else(wrong)?;
            put(&mut v, "item.key", r.0);
            put(&mut v, "item.title", r.1);
            put(&mut v, "item.status", r.2);
            put(&mut v, "item.priority", r.3);
            put(&mut v, "item.type", r.4);
            put(&mut v, "item.due_date", opt_date(r.5));
            put(&mut v, "item.assignee", opt(r.6));
        }
        _ => return Err(CmdError::invalid("this template takes no source record")),
    }
    Ok(v)
}

// ---------- commands ----------

fn check_kind(k: &str) -> CmdResult<()> {
    KINDS.contains(&k).then_some(()).ok_or_else(|| {
        CmdError::invalid("source_kind must be none, contact, deal, invoice or item")
    })
}

#[derive(Deserialize, JsonSchema)]
pub struct Save {
    /// The page or board to copy into a template.
    pub note_id: Uuid,
    /// Template name; saving an existing name replaces that template.
    pub name: String,
    /// What the placeholders bind to: none | contact | deal | invoice | item.
    pub source_kind: String,
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct TemplateOut {
    pub id: Uuid,
    pub name: String,
    /// page | board
    pub kind: String,
    pub source_kind: String,
    pub updated_at: chrono::DateTime<Utc>,
}

async fn template_save(ctx: &mut Ctx, i: Save) -> CmdResult<TemplateOut> {
    check_kind(&i.source_kind)?;
    let name = i.name.trim();
    if name.is_empty() {
        return Err(CmdError::invalid("name is required"));
    }
    let space = assert_note_access(ctx, i.note_id, false).await?;
    let readable = readable_space_ids(ctx).await?;
    // A template is visible to every reader of the space, so it must not copy a page only some people may open.
    if cnote_core::access::is_view_restricted(ctx, i.note_id).await? {
        return Err(CmdError::forbidden(
            "a page with view restrictions cannot be saved as a template",
        ));
    }
    let (title, types, md): (String, Vec<String>, String) = sqlx::query_as(
        "select title, types, content_md from core.notes where id = $1 and deleted_at is null",
    )
    .bind(i.note_id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("note"))?;
    let kind = if types.is_empty() {
        "page"
    } else if types.iter().any(|t| t == "whiteboard" || t == "design") {
        "board"
    } else {
        return Err(CmdError::invalid("only pages and boards can be templates"));
    };
    let t: TemplateOut = sqlx::query_as(
        "insert into docs.templates (workspace_id, name, kind, source_kind, types, title, content_md, created_by, space_id)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
         on conflict (workspace_id, name) do update set kind = excluded.kind, source_kind = excluded.source_kind,
           types = excluded.types, title = excluded.title, content_md = excluded.content_md,
           space_id = excluded.space_id, updated_at = now()
         where (docs.templates.space_id is null or docs.templates.space_id = any($10))
           and (docs.templates.created_by = $8 or $11)
         returning id, name, kind, source_kind, updated_at",
    )
    .bind(ctx.workspace_id())
    .bind(name)
    .bind(kind)
    .bind(&i.source_kind)
    .bind(&types)
    .bind(&title)
    .bind(&md)
    .bind(ctx.principal.user_id)
    .bind(space)
    .bind(&readable)
    .bind(ctx.principal.role >= Role::Admin)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::conflict("a template with this name already exists"))?;
    ctx.emit(
        "template.saved",
        Some(i.note_id),
        json!({"template_id": t.id, "name": t.name}),
    )
    .await?;
    Ok(t)
}

#[derive(Deserialize, JsonSchema, Default)]
pub struct List {
    /// Only templates for this source kind (templates with no source are always included).
    #[serde(default)]
    pub source_kind: Option<String>,
    /// page | board
    #[serde(default)]
    pub kind: Option<String>,
}

async fn template_list(ctx: &mut Ctx, i: List) -> CmdResult<Vec<TemplateOut>> {
    Ok(sqlx::query_as(
        "select id, name, kind, source_kind, updated_at from docs.templates
         where ($1::text is null or source_kind in ('none', $1)) and ($2::text is null or kind = $2)
           and (space_id is null or space_id = any($3)) order by name",
    )
    .bind(i.source_kind)
    .bind(i.kind)
    .bind(readable_space_ids(ctx).await?)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct Delete {
    pub template_id: Uuid,
}

async fn template_delete(ctx: &mut Ctx, i: Delete) -> CmdResult<Value> {
    let readable = readable_space_ids(ctx).await?;
    let n = sqlx::query(
        // Only the author or an admin may delete; readers of the space may not.
        "delete from docs.templates where id = $1 and (space_id is null or space_id = any($2))
           and (created_by = $3 or $4)",
    )
    .bind(i.template_id)
    .bind(&readable)
    .bind(ctx.principal.user_id)
    .bind(ctx.principal.role >= Role::Admin)
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 0 {
        return Err(CmdError::not_found("template"));
    }
    ctx.emit(
        "template.deleted",
        None,
        json!({"template_id": i.template_id}),
    )
    .await?;
    Ok(json!({"deleted": true}))
}

#[derive(Deserialize, JsonSchema)]
pub struct Fields {
    /// none | contact | deal | invoice | item
    pub source_kind: String,
}

#[derive(Serialize, JsonSchema)]
pub struct FieldOut {
    /// Insert as {{path}}.
    pub path: &'static str,
    pub label: &'static str,
}

async fn template_fields(_: &mut Ctx, i: Fields) -> CmdResult<Vec<FieldOut>> {
    check_kind(&i.source_kind)?;
    Ok(catalogue(&i.source_kind)
        .into_iter()
        .map(|(path, label)| FieldOut { path, label })
        .collect())
}

#[derive(sqlx::FromRow)]
struct Tpl {
    name: String,
    kind: String,
    source_kind: String,
    types: Vec<String>,
    title: String,
    content_md: String,
}

async fn load_template(ctx: &mut Ctx, id: Uuid) -> CmdResult<Tpl> {
    sqlx::query_as(
        "select name, kind, source_kind, types, title, content_md from docs.templates
         where id = $1 and (space_id is null or space_id = any($2))",
    )
    .bind(id)
    .bind(readable_space_ids(ctx).await?)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("template"))
}

/// (title, body, unresolved placeholders) for a template against its source record.
async fn render(
    ctx: &mut Ctx,
    t: &Tpl,
    source: Option<Uuid>,
) -> CmdResult<(String, String, Vec<String>)> {
    if t.source_kind != "none" && source.is_none() {
        return Err(CmdError::invalid(format!(
            "this template needs a {} as its source",
            t.source_kind
        )));
    }
    let vals = resolve(ctx, &t.source_kind, source).await?;
    let mut missing = BTreeSet::new();
    let title = substitute(&t.title, &vals, &mut missing);
    let body = if t.kind == "board" {
        render_board(&t.content_md, &vals, &mut missing)?
    } else {
        substitute(&t.content_md, &vals, &mut missing)
    };
    Ok((title, body, missing.into_iter().collect()))
}

#[derive(Deserialize, JsonSchema)]
pub struct Render {
    pub template_id: Uuid,
    /// The contact, deal, invoice or item the placeholders read from. Required unless the template has no source.
    #[serde(default)]
    pub source_id: Option<Uuid>,
    /// Where to create the note; defaults to the source record's space.
    #[serde(default)]
    pub space_id: Option<Uuid>,
    /// Overrides the template's title.
    #[serde(default)]
    pub title: Option<String>,
}

async fn template_render(ctx: &mut Ctx, i: Render) -> CmdResult<Value> {
    let t = load_template(ctx, i.template_id).await?;
    let (title, body, unresolved) = render(ctx, &t, i.source_id).await?;
    let space = match (i.space_id, i.source_id) {
        (Some(s), _) => s,
        (None, Some(src)) => {
            sqlx::query_scalar("select space_id from core.notes where id = $1")
                .bind(src)
                .fetch_one(&mut *ctx.tx)
                .await?
        }
        _ => {
            return Err(CmdError::invalid(
                "space_id is required for a template without a source",
            ));
        }
    };
    let title = i.title.filter(|x| !x.trim().is_empty()).unwrap_or(title);
    let title = if title.trim().is_empty() {
        t.name.clone()
    } else {
        title
    };
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: space,
            title,
            parent_id: None,
            content_md: Some(body),
            types: t.types.clone(),
            owner_id: None,
            verify_by: None,
        },
    )
    .await?;
    sqlx::query(
        "insert into docs.template_bindings (note_id, workspace_id, template_id, source_kind, source_id) values ($1,$2,$3,$4,$5)",
    )
    .bind(note.id)
    .bind(ctx.workspace_id())
    .bind(i.template_id)
    .bind(&t.source_kind)
    .bind(i.source_id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "template.rendered",
        Some(note.id),
        json!({"template_id": i.template_id, "source_id": i.source_id, "unresolved": unresolved}),
    )
    .await?;
    Ok(json!({"note": note, "unresolved": unresolved}))
}

#[derive(Deserialize, JsonSchema)]
pub struct Refresh {
    /// A page created by template.render. Boards are one-shot and cannot be refreshed.
    pub note_id: Uuid,
}

async fn template_refresh(ctx: &mut Ctx, i: Refresh) -> CmdResult<Value> {
    assert_note_access(ctx, i.note_id, true).await?;
    let b: (Option<Uuid>, Option<Uuid>) = sqlx::query_as(
        "select template_id, source_id from docs.template_bindings where note_id = $1",
    )
    .bind(i.note_id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::invalid("this note was not created from a template"))?;
    let tid =
        b.0.ok_or_else(|| CmdError::invalid("the template this note came from was deleted"))?;
    let t = load_template(ctx, tid).await?;
    if t.kind != "page" {
        return Err(CmdError::invalid(
            "boards are one-shot: render the template again to get fresh data",
        ));
    }
    let (_, body, unresolved) = render(ctx, &t, b.1).await?;
    let version: i32 = sqlx::query_scalar(
        "update core.notes set content_md = $2, updated_at = now(), version = version + 1 where id = $1 returning version",
    )
    .bind(i.note_id)
    .bind(&body)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "note.updated",
        Some(i.note_id),
        json!({"changed": ["content"], "version": version}),
    )
    .await?;
    Ok(json!({"note_id": i.note_id, "unresolved": unresolved}))
}

pub fn commands() -> Vec<Command> {
    use ToolGroup::{Read, WriteNotes};
    vec![
        Command::new("template.save", "Save a page or board as a template with {{placeholders}} bound to a contact, deal, invoice or item (or none). Same name replaces.", WriteNotes, handler!(template_save)),
        Command::new("template.list", "List templates, optionally for one source kind or page/board.", Read, handler!(template_list)),
        Command::new("template.delete", "Delete a template. Notes already rendered from it keep their content.", WriteNotes, handler!(template_delete)),
        Command::new("template.fields", "List the placeholders available for a source kind, for an insert-field picker.", Read, handler!(template_fields)),
        Command::new("template.render", "Create a new page or board from a template, filling placeholders from a record you can read. Unknown placeholders stay visible and are listed in `unresolved`.", WriteNotes, handler!(template_render)).core(),
        Command::new("template.refresh", "Re-render a page made from a template using the record's current data. Overwrites the page body.", WriteNotes, handler!(template_refresh)),
    ]
}

// ---------- starter templates ----------

const PROPOSAL: &str = "# Proposal for {{contact.name}}\n\nPrepared on {{today}} by {{workspace.name}}.\n\n## Overview\n\n{{deal.title}}: a short description of what we will deliver.\n\n## Investment\n\nProposed value: **{{deal.value}}**. Expected decision by {{deal.expected_close}}.\n\n## Next steps\n\n1. Review this proposal.\n2. Confirm scope and timeline.\n3. Sign off and we begin.\n\nContact: {{contact.name}}, {{contact.email}}\n";
const COVER: &str = "Dear {{contact.name}},\n\nPlease find attached invoice {{invoice.number}} dated {{invoice.date}} for **{{invoice.total}}** (including GST of {{invoice.tax}}), due on {{invoice.due_date}}.\n\nThank you for your business. Reply to this note if anything needs correcting.\n\nRegards,\n{{user.name}}\n{{workspace.name}}\n";

pub fn seed_templates(tx: &mut Tx, ws: Uuid, owner: Uuid) -> BoxFuture<'_, anyhow::Result<()>> {
    Box::pin(async move {
        for (name, source, title, body) in [
            ("Proposal", "deal", "Proposal: {{deal.title}}", PROPOSAL),
            (
                "Invoice cover letter",
                "invoice",
                "Invoice {{invoice.number}}: cover letter",
                COVER,
            ),
        ] {
            sqlx::query(
                "insert into docs.templates (workspace_id, name, kind, source_kind, title, content_md, created_by)
                 values ($1,$2,'page',$3,$4,$5,$6) on conflict do nothing",
            )
            .bind(ws)
            .bind(name)
            .bind(source)
            .bind(title)
            .bind(body)
            .bind(owner)
            .execute(&mut **tx)
            .await?;
        }
        Ok(())
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn vals() -> Vals {
        [
            ("contact.name", "Asha \"A\" Rao"),
            ("invoice.total", "₹1,000.00"),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect()
    }

    #[test]
    fn substitutes_known_and_keeps_unknown() {
        let mut m = BTreeSet::new();
        let out = substitute(
            "Hi {{ contact.name }}, {{invoice.total}} {{deal.value}} {{deal.value}} {{1+1}}",
            &vals(),
            &mut m,
        );
        assert_eq!(
            out,
            "Hi Asha \"A\" Rao, ₹1,000.00 {{deal.value}} {{deal.value}} {{1+1}}"
        );
        assert_eq!(m.into_iter().collect::<Vec<_>>(), vec!["deal.value"]);
    }

    #[test]
    fn values_are_not_rescanned() {
        let v: Vals = [("a.b".to_string(), "{{contact.name}}".to_string())].into();
        let mut m = BTreeSet::new();
        assert_eq!(substitute("{{a.b}}", &v, &mut m), "{{contact.name}}");
        assert!(m.is_empty());
    }

    #[test]
    fn board_values_stay_valid_json() {
        let md = "```tldraw\n{\"store\":{\"s1\":{\"props\":{\"text\":\"For {{contact.name}} {{x.y}}\"}}}}\n```";
        let mut m = BTreeSet::new();
        let out = render_board(md, &vals(), &mut m).unwrap();
        let body = out
            .trim_start_matches("```tldraw\n")
            .trim_end_matches("\n```");
        let j: Value = serde_json::from_str(body).unwrap();
        assert_eq!(
            j["store"]["s1"]["props"]["text"],
            "For Asha \"A\" Rao {{x.y}}"
        );
        assert!(m.contains("x.y"));
        assert!(render_board("```tldraw\nnot json\n```", &vals(), &mut m).is_err());
    }

    #[test]
    fn money_uses_indian_grouping_for_inr() {
        assert_eq!(money(0, "INR"), "₹0.00");
        assert_eq!(money(99_999, "INR"), "₹999.99");
        assert_eq!(money(100_000, "INR"), "₹1,000.00");
        assert_eq!(money(1_234_567_890, "INR"), "₹1,23,45,678.90");
        assert_eq!(money(-500, "INR"), "-₹5.00");
        assert_eq!(money(123_456_789, "USD"), "USD 1,234,567.89");
    }

    #[test]
    fn date_format() {
        assert_eq!(
            date(NaiveDate::from_ymd_opt(2026, 4, 1).unwrap()),
            "1 Apr 2026"
        );
    }

    #[test]
    fn catalogue_has_common_and_kind_fields() {
        let c = catalogue("deal");
        assert!(c.iter().any(|f| f.0 == "today") && c.iter().any(|f| f.0 == "contact.email"));
        assert_eq!(catalogue("none").len(), COMMON.len());
    }
}
