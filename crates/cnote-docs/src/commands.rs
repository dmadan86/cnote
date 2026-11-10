//! Living-page commands (ADR-013): verify, ownership, review queues, ask-with-citations, capture.

use crate::freshness::{DEFAULT_VERIFY_DAYS, LAPSE_GRACE_DAYS, flag};
use chrono::{DateTime, Duration, NaiveDate, Utc};
use cnote_core::access::{assert_note_access, assert_space_access, readable_space_ids};
use cnote_core::ai::{ChatMessage, CompletionRequest};
use cnote_core::handler;
use cnote_core::notes::{CreateNote, insert_note};
use cnote_core::{CmdError, CmdResult, Command, Ctx, Role, ToolGroup};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::BTreeSet;
use uuid::Uuid;

fn user(ctx: &Ctx) -> CmdResult<Uuid> {
    ctx.principal
        .user_id
        .ok_or_else(|| CmdError::forbidden("a user is required"))
}

fn today_plus(days: i64) -> NaiveDate {
    (Utc::now() + Duration::days(days)).date_naive()
}

/// Load a page the caller can read; returns its owner. `manage` demands owner-or-admin.
async fn page_owner(ctx: &mut Ctx, id: Uuid, manage: bool) -> CmdResult<Option<Uuid>> {
    assert_note_access(ctx, id, manage).await?;
    let owner: Option<Uuid> = sqlx::query_scalar(
        "select owner_id from core.notes where id = $1 and cardinality(types) = 0",
    )
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("page"))?;
    if manage && ctx.principal.role < Role::Admin && owner != ctx.principal.user_id {
        return Err(CmdError::forbidden(
            "only the page owner or an admin can do this",
        ));
    }
    Ok(owner)
}

async fn resolve_flags(ctx: &mut Ctx, note_id: Uuid) -> CmdResult<()> {
    sqlx::query("update docs.freshness_flags set resolved_at = now() where note_id = $1 and resolved_at is null")
        .bind(note_id)
        .execute(&mut *ctx.tx)
        .await?;
    Ok(())
}

// ---------- verify / owner / restore ----------

#[derive(Deserialize, JsonSchema)]
pub struct Verify {
    /// The page to confirm is still correct.
    pub note_id: Uuid,
    /// Next verify-by date; defaults to 90 days from today.
    #[serde(default)]
    pub verify_by: Option<NaiveDate>,
}

async fn doc_verify(ctx: &mut Ctx, i: Verify) -> CmdResult<Value> {
    page_owner(ctx, i.note_id, true).await?;
    let by = i
        .verify_by
        .unwrap_or_else(|| today_plus(DEFAULT_VERIFY_DAYS));
    if by <= Utc::now().date_naive() {
        return Err(CmdError::invalid("verify_by must be in the future"));
    }
    sqlx::query(
        "update core.notes set verified_at = now(), verify_by = $2, freshness = 'fresh', lapse_count = 0, archived_at = null
         where id = $1",
    )
    .bind(i.note_id)
    .bind(by)
    .execute(&mut *ctx.tx)
    .await?;
    resolve_flags(ctx, i.note_id).await?;
    ctx.emit("doc.verified", Some(i.note_id), json!({"verify_by": by}))
        .await?;
    Ok(json!({"note_id": i.note_id, "freshness": "fresh", "verify_by": by}))
}

#[derive(Deserialize, JsonSchema)]
pub struct SetOwner {
    pub note_id: Uuid,
    /// A member of this workspace.
    pub owner_id: Uuid,
}

async fn doc_set_owner(ctx: &mut Ctx, i: SetOwner) -> CmdResult<Value> {
    let prev = page_owner(ctx, i.note_id, true).await?;
    let member: bool =
        sqlx::query_scalar("select exists (select 1 from core.memberships where user_id = $1)")
            .bind(i.owner_id)
            .fetch_one(&mut *ctx.tx)
            .await?;
    if !member {
        return Err(CmdError::invalid("new owner must be a workspace member"));
    }
    sqlx::query("update core.notes set owner_id = $2 where id = $1")
        .bind(i.note_id)
        .bind(i.owner_id)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "doc.owner_changed",
        Some(i.note_id),
        json!({"from": prev, "to": i.owner_id}),
    )
    .await?;
    Ok(json!({"note_id": i.note_id, "owner_id": i.owner_id}))
}

#[derive(Deserialize, JsonSchema)]
pub struct NoteRef {
    pub note_id: Uuid,
}

async fn doc_restore(ctx: &mut Ctx, i: NoteRef) -> CmdResult<Value> {
    page_owner(ctx, i.note_id, true).await?;
    let n = sqlx::query(
        "update core.notes set freshness = 'stale', archived_at = null, lapse_count = 0, verify_by = $2
         where id = $1 and freshness = 'archived'",
    )
    .bind(i.note_id)
    .bind(today_plus(LAPSE_GRACE_DAYS))
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 0 {
        return Err(CmdError::conflict("page is not archived"));
    }
    // Restored pages come back stale: the owner has to re-verify them.
    flag(ctx, i.note_id, "date_lapsed", None, None).await?;
    ctx.emit("doc.restored", Some(i.note_id), json!({})).await?;
    Ok(json!({"note_id": i.note_id, "freshness": "stale"}))
}

// ---------- owner dashboard ----------

#[derive(Deserialize, JsonSchema, Default)]
pub struct Empty {}

#[derive(Serialize, sqlx::FromRow)]
struct FlaggedPage {
    id: Uuid,
    title: String,
    space_id: Uuid,
    freshness: String,
    verify_by: Option<NaiveDate>,
    lapse_count: i32,
    /// binding_changed / date_lapsed / imported, newest first
    reasons: Vec<String>,
}

async fn doc_flags(ctx: &mut Ctx, _: Empty) -> CmdResult<Vec<FlaggedPage>> {
    let me = user(ctx)?;
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(
        "select n.id, n.title, n.space_id, n.freshness, n.verify_by, n.lapse_count,
                array_agg(f.reason order by f.created_at desc) as reasons
         from core.notes n join docs.freshness_flags f on f.note_id = n.id and f.resolved_at is null
         where n.owner_id = $1 and n.space_id = any($2) and n.deleted_at is null and n.freshness <> 'archived'
         group by n.id order by min(f.created_at)",
    )
    .bind(me)
    .bind(spaces)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct ListProposals {
    /// Only proposals for this page.
    #[serde(default)]
    pub note_id: Option<Uuid>,
    /// pending (default) | approved | rejected
    #[serde(default)]
    pub status: Option<String>,
}

#[derive(Serialize, sqlx::FromRow)]
struct ProposalOut {
    id: Uuid,
    note_id: Uuid,
    title: String,
    proposed_md: String,
    rationale: String,
    status: String,
    created_at: DateTime<Utc>,
}

async fn doc_proposals(ctx: &mut Ctx, i: ListProposals) -> CmdResult<Vec<ProposalOut>> {
    let spaces = readable_space_ids(ctx).await?;
    Ok(sqlx::query_as(
        "select p.id, p.note_id, n.title, p.proposed_md, p.rationale, p.status, p.created_at
         from docs.edit_proposals p join core.notes n on n.id = p.note_id
         where n.space_id = any($1) and n.deleted_at is null and p.status = $2 and ($3::uuid is null or p.note_id = $3)
         order by p.created_at desc limit 100",
    )
    .bind(spaces)
    .bind(i.status.unwrap_or_else(|| "pending".into()))
    .bind(i.note_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct Decide {
    pub proposal_id: Uuid,
    /// true applies the proposed content to the page; false rejects it.
    pub approve: bool,
}

async fn doc_proposal_decide(ctx: &mut Ctx, i: Decide) -> CmdResult<Value> {
    let me = user(ctx)?;
    let (note_id, flag_id, md): (Uuid, Option<Uuid>, String) = sqlx::query_as(
        "select note_id, flag_id, proposed_md from docs.edit_proposals where id = $1 and status = 'pending' for update",
    )
    .bind(i.proposal_id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("pending proposal"))?;
    page_owner(ctx, note_id, true).await?;
    if i.approve {
        ctx.call("note.update", json!({"id": note_id, "content_md": md}))
            .await?;
        if let Some(f) = flag_id {
            sqlx::query("update docs.freshness_flags set resolved_at = now() where id = $1")
                .bind(f)
                .execute(&mut *ctx.tx)
                .await?;
        }
        // The owner reviewed the edit; the page is fresh again unless other flags are still open.
        sqlx::query(
            "update core.notes set freshness = 'fresh' where id = $1 and freshness = 'stale'
               and not exists (select 1 from docs.freshness_flags where note_id = $1 and resolved_at is null)",
        )
        .bind(note_id)
        .execute(&mut *ctx.tx)
        .await?;
    }
    let status = if i.approve { "approved" } else { "rejected" };
    sqlx::query("update docs.edit_proposals set status = $2, decided_by = $3, decided_at = now() where id = $1")
        .bind(i.proposal_id)
        .bind(status)
        .bind(me)
        .execute(&mut *ctx.tx)
        .await?;
    ctx.emit(
        "doc.proposal_decided",
        Some(note_id),
        json!({"proposal_id": i.proposal_id, "status": status, "decided_by": me}),
    )
    .await?;
    Ok(json!({"proposal_id": i.proposal_id, "status": status}))
}

// ---------- freshness score ----------

#[derive(Deserialize, JsonSchema)]
pub struct Score {
    /// Limit to one space.
    #[serde(default)]
    pub space_id: Option<Uuid>,
    /// A page counts as verified if it was verified (or created) within this many days. Default 90.
    #[serde(default)]
    pub window_days: Option<i32>,
}

async fn doc_freshness_score(ctx: &mut Ctx, i: Score) -> CmdResult<Value> {
    let spaces = readable_space_ids(ctx).await?;
    let rows: Vec<(Uuid, String, i64, i64)> = sqlx::query_as(
        "select s.id, s.key, count(*), count(*) filter (where n.freshness = 'fresh'
                 and coalesce(n.verified_at, n.created_at) >= now() - make_interval(days => $2))
         from core.notes n join core.spaces s on s.id = n.space_id
         where cardinality(n.types) = 0 and n.deleted_at is null and n.freshness <> 'archived'
           and n.space_id = any($1) and ($3::uuid is null or n.space_id = $3)
         group by s.id, s.key order by s.key",
    )
    .bind(spaces)
    .bind(i.window_days.unwrap_or(90))
    .bind(i.space_id)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let ratio = |v: i64, t: i64| if t == 0 { 1.0 } else { v as f64 / t as f64 };
    let (total, verified) = rows.iter().fold((0, 0), |a, r| (a.0 + r.2, a.1 + r.3));
    Ok(json!({
        "total": total, "verified": verified, "score": ratio(verified, total),
        "spaces": rows.iter().map(|(id, key, t, v)| json!({
            "space_id": id, "key": key, "total": t, "verified": v, "score": ratio(*v, *t)
        })).collect::<Vec<_>>(),
    }))
}

// ---------- ask with citations; capture requests ----------

#[derive(Deserialize, JsonSchema)]
pub struct Ask {
    /// A question in plain language, answered only from pages you can read.
    pub question: String,
}

const STOP: [&str; 24] = [
    "the", "and", "for", "are", "was", "how", "what", "who", "why", "when", "where", "does", "did",
    "can", "our", "you", "with", "that", "this", "from", "have", "has", "should", "which",
];

fn keywords(q: &str) -> Vec<String> {
    let set: BTreeSet<String> = q
        .to_lowercase()
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| w.len() > 2 && !STOP.contains(w))
        .map(String::from)
        .collect();
    set.into_iter().take(8).collect()
}

async fn search(ctx: &mut Ctx, q: &str) -> CmdResult<Vec<Value>> {
    let v = ctx
        .call("search.notes", json!({"q": q, "limit": 5}))
        .await?;
    Ok(v.as_array().cloned().unwrap_or_default())
}

async fn doc_ask(ctx: &mut Ctx, i: Ask) -> CmdResult<Value> {
    let words = keywords(&i.question);
    if words.is_empty() {
        return Err(CmdError::invalid(
            "ask a question with at least one meaningful word",
        ));
    }
    // Every keyword must match for an answer; any single keyword only makes a "partial" match worth routing to.
    let hits = search(ctx, &words.join(" ")).await?;
    if !hits.is_empty() {
        let citations: Vec<Value> = hits
            .iter()
            .map(|h| {
                json!({"title": h["title"], "note_id": h["id"], "freshness": h["freshness"],
                       "verified_at": h["verified_at"], "snippet": h["snippet"]})
            })
            .collect();
        let sources: String = hits
            .iter()
            .map(|h| {
                format!(
                    "[{}] {}\n{}",
                    h["title"].as_str().unwrap_or(""),
                    h["freshness"].as_str().unwrap_or(""),
                    h["snippet"].as_str().unwrap_or("")
                )
            })
            .collect::<Vec<_>>()
            .join("\n\n");
        let answer = ctx
            .registry
            .ai
            .complete(CompletionRequest {
                purpose: "answer".into(),
                max_tokens: 512,
                messages: vec![
                    ChatMessage {
                        role: "system".into(),
                        content: format!(
                            "Answer the question using only these pages, and say when a page is stale.\nQuestion: {}",
                            i.question
                        ),
                    },
                    ChatMessage { role: "user".into(), content: sources.clone() },
                ],
            })
            .await
            .map(|c| c.text)
            .unwrap_or(sources);
        return Ok(json!({"answered": true, "answer": answer, "citations": citations}));
    }

    let partial = search(ctx, &words.join(" or ")).await?;
    let best = partial
        .first()
        .and_then(|h| h["id"].as_str())
        .and_then(|s| s.parse::<Uuid>().ok());
    let mut routed: Option<Uuid> = match best {
        Some(id) => {
            sqlx::query_scalar("select owner_id from core.notes where id = $1")
                .bind(id)
                .fetch_one(&mut *ctx.tx)
                .await?
        }
        None => None,
    };
    if routed.is_none() {
        routed = sqlx::query_scalar(
            "select user_id from core.memberships where role in ('owner','admin') order by (role = 'admin') desc, created_at limit 1",
        )
        .fetch_optional(&mut *ctx.tx)
        .await?;
    }
    let id: Uuid = sqlx::query_scalar(
        "insert into docs.capture_requests (workspace_id, question, asked_by, routed_to) values ($1,$2,$3,$4) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(i.question.trim())
    .bind(ctx.principal.user_id)
    .bind(routed)
    .fetch_one(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "doc.capture_requested",
        None,
        json!({"request_id": id, "routed_to": routed}),
    )
    .await?;
    Ok(json!({
        "answered": false,
        "answer": "No page answers this yet. I asked the likely owner to write it up and will link the answer here.",
        "citations": [],
        "capture_request_id": id,
        "routed_to": routed,
        "related": partial.iter().map(|h| json!({"title": h["title"], "note_id": h["id"]})).collect::<Vec<_>>(),
    }))
}

#[derive(Deserialize, JsonSchema)]
pub struct ListCaptures {
    /// open (default) | answered | dismissed
    #[serde(default)]
    pub status: Option<String>,
}

#[derive(Serialize, sqlx::FromRow)]
struct CaptureOut {
    id: Uuid,
    question: String,
    asked_by: Option<Uuid>,
    routed_to: Option<Uuid>,
    answer_note_id: Option<Uuid>,
    status: String,
    created_at: DateTime<Utc>,
}

async fn capture_list(ctx: &mut Ctx, i: ListCaptures) -> CmdResult<Vec<CaptureOut>> {
    Ok(sqlx::query_as(
        "select id, question, asked_by, routed_to, answer_note_id, status, created_at
         from docs.capture_requests
         where status = $1 and ($2 or routed_to = $3 or asked_by = $3)
         order by created_at desc limit 100",
    )
    .bind(i.status.unwrap_or_else(|| "open".into()))
    .bind(ctx.principal.role >= Role::Admin)
    .bind(ctx.principal.user_id)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct CaptureAnswer {
    pub request_id: Uuid,
    /// The page that answers the question.
    pub note_id: Uuid,
}

async fn capture_answer(ctx: &mut Ctx, i: CaptureAnswer) -> CmdResult<Value> {
    let (routed, asked): (Option<Uuid>, Option<Uuid>) = sqlx::query_as(
        "select routed_to, asked_by from docs.capture_requests where id = $1 and status = 'open' for update",
    )
    .bind(i.request_id)
    .fetch_optional(&mut *ctx.tx)
    .await?
    .ok_or_else(|| CmdError::not_found("open capture request"))?;
    let me = ctx.principal.user_id;
    if ctx.principal.role < Role::Admin && me != routed && me != asked {
        return Err(CmdError::forbidden("not your capture request"));
    }
    assert_note_access(ctx, i.note_id, false).await?;
    sqlx::query(
        "update docs.capture_requests set answer_note_id = $2, status = 'answered' where id = $1",
    )
    .bind(i.request_id)
    .bind(i.note_id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "doc.capture_answered",
        Some(i.note_id),
        json!({"request_id": i.request_id}),
    )
    .await?;
    Ok(json!({"request_id": i.request_id, "status": "answered", "note_id": i.note_id}))
}

#[derive(Deserialize, JsonSchema)]
pub struct Capture {
    /// transcript | chat | pr
    pub source: String,
    /// The raw material to turn into a page.
    pub text: String,
    pub space_id: Uuid,
    #[serde(default)]
    pub title: Option<String>,
}

async fn doc_capture(ctx: &mut Ctx, i: Capture) -> CmdResult<Value> {
    if !["transcript", "chat", "pr"].contains(&i.source.as_str()) {
        return Err(CmdError::invalid("source must be transcript, chat or pr"));
    }
    if i.text.trim().is_empty() {
        return Err(CmdError::invalid("text is required"));
    }
    assert_space_access(ctx, i.space_id, true).await?;
    let done = ctx
        .registry
        .ai
        .complete(CompletionRequest {
            purpose: "draft".into(),
            max_tokens: 1500,
            messages: vec![
                ChatMessage {
                    role: "system".into(),
                    content: format!(
                        "Turn this {} into a concise wiki page in Markdown: decisions, facts, owners, open questions. Invent nothing.",
                        i.source
                    ),
                },
                ChatMessage { role: "user".into(), content: i.text.clone() },
            ],
        })
        .await?;
    let title = i.title.filter(|t| !t.trim().is_empty()).unwrap_or_else(|| {
        let first = i
            .text
            .lines()
            .find(|l| !l.trim().is_empty())
            .unwrap_or("")
            .trim();
        let t: String = first.chars().take(80).collect();
        if t.is_empty() {
            format!("Captured {}", i.source)
        } else {
            t
        }
    });
    let note = insert_note(
        ctx,
        CreateNote {
            space_id: i.space_id,
            title,
            parent_id: None,
            content_md: Some(done.text),
            types: vec![],
            owner_id: None, // defaults to the caller
            verify_by: None,
        },
    )
    .await?;
    ctx.emit("doc.captured", Some(note.id), json!({"source": i.source}))
        .await?;
    Ok(serde_json::to_value(note)?)
}

pub fn commands() -> Vec<Command> {
    use ToolGroup::{Read, WriteNotes};
    vec![
        Command::new("doc.verify", "Confirm a page is still correct: resets its freshness, lapse count and flags, and sets the next verify-by date (default +90 days). Page owner or admin.", WriteNotes, handler!(doc_verify)).core(),
        Command::new("doc.set_owner", "Hand a page to another workspace member. Page owner or admin.", WriteNotes, handler!(doc_set_owner)),
        Command::new("doc.flags", "Your dashboard: pages you own that are stale or lapsed, with the reasons.", Read, handler!(doc_flags)),
        Command::new("doc.proposals", "List AI-proposed page edits waiting for review (never applied automatically).", Read, handler!(doc_proposals)),
        Command::new("doc.proposal_decide", "Approve (applies the edit to the page) or reject an AI-proposed edit. Page owner or admin.", WriteNotes, handler!(doc_proposal_decide)),
        Command::new("doc.restore", "Bring an archived page back into search; it returns stale until re-verified.", WriteNotes, handler!(doc_restore)),
        Command::new("doc.freshness_score", "Share of pages verified within the window, for the workspace and per space.", Read, handler!(doc_freshness_score)),
        Command::new("doc.ask", "Ask a question; answers cite the pages used with their freshness. If no page answers, a capture request is routed to the likely owner.", Read, handler!(doc_ask)).core(),
        Command::new("capture.list", "List capture requests (unanswered questions) routed to you, or all for admins.", Read, handler!(capture_list)),
        Command::new("capture.answer", "Link the page that answers a capture request and close it.", WriteNotes, handler!(capture_answer)),
        Command::new("doc.capture", "Draft a page from a transcript, chat or PR text; you become its owner.", WriteNotes, handler!(doc_capture)),
    ]
}
