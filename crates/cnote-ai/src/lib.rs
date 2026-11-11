//! cnote-ai: model-agnostic AI runtime (ADR-009). Provider adapters, BYO-LLM per workspace, PII redaction,
//! audit + usage, note indexing and hybrid search. All model calls from commands go through `complete`/`embed`
//! below so redaction, audit and metering cannot be skipped.

mod crypto;
mod netguard;
mod providers;
mod redact;

pub use providers::{
    AnthropicProvider, Composite, OpenAiProvider, provider_for, provider_from_env,
};
pub use redact::redact;

use cnote_core::access::{assert_note_access, readable_space_ids, viewer};
use cnote_core::ai::{ChatMessage, Completion, CompletionRequest, ModelProvider};
use cnote_core::events::{self, Event, record_usage};
use cnote_core::{CmdError, CmdResult, Command, Ctx, Module, Role, ToolGroup, handler};
use futures::future::BoxFuture;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::sync::Arc;
use uuid::Uuid;

const CHUNK_CHARS: usize = 800;
const CHUNK_OVERLAP: usize = 100;
const EMBED_BATCH: usize = 64;
/// Cap on note text sent to a model in one prompt.
const MAX_NOTE_CHARS: usize = 24_000;

pub fn module() -> Module {
    Module {
        commands: commands(),
        event_handlers: vec![
            ("note.created", on_note_event),
            ("note.updated", on_note_event),
        ],
        job_handlers: vec![("ai.index_note", index_job)],
        ..Default::default()
    }
}

#[rustfmt::skip]
fn commands() -> Vec<Command> {
    vec![
        Command::new("search.hybrid", "Hybrid search over notes you can read: full-text rank fused with semantic (embedding) similarity. Archived pages excluded.", ToolGroup::Read, handler!(search_hybrid)).core(),
        Command::new("ai.summarize", "Summarize a note.", ToolGroup::Read, handler!(summarize)).core(),
        Command::new("ai.extract_tasks", "Suggest tasks found in a note (e.g. a meeting note). Suggestions only: nothing is created.", ToolGroup::Read, handler!(extract_tasks)).core(),
        Command::new("ai.draft", "Draft text from a prompt, optionally grounded in a note. Returns the draft; nothing is saved.", ToolGroup::Read, handler!(draft)),
        Command::new("ai.set_provider", "Bring your own model: set this workspace's provider (anthropic or openai-compatible), base URL, model and API key. The key is stored encrypted and never returned. While set, C Note does not meter AI tokens.", ToolGroup::Admin, handler!(set_provider)).min_role(Role::Admin),
        Command::new("ai.get_provider", "Show this workspace's AI provider settings (never the key).", ToolGroup::Admin, handler!(get_provider)).min_role(Role::Admin),
        Command::new("ai.clear_provider", "Remove the workspace's own model settings and key, falling back to the platform provider.", ToolGroup::Admin, handler!(clear_provider)).min_role(Role::Admin),
    ]
}

// ---------- provider resolution, audit, metering ----------

struct Resolved {
    provider: Arc<dyn ModelProvider>,
    /// Workspace brings its own key: never metered.
    byo: bool,
    redact: bool,
}

#[derive(sqlx::FromRow)]
struct StoredConfig {
    provider: String,
    base_url: Option<String>,
    model: Option<String>,
    embedding_model: Option<String>,
    key_enc: Option<Vec<u8>>,
}

const CONFIG_COLS: &str = "provider, base_url, model, embedding_model, key_enc";

async fn stored_config(ctx: &mut Ctx) -> CmdResult<Option<StoredConfig>> {
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(format!(
        "select {CONFIG_COLS} from ai.provider_config where workspace_id = $1"
    )))
    .bind(ctx.workspace_id())
    .fetch_optional(&mut *ctx.tx)
    .await?)
}

async fn redact_enabled(ctx: &mut Ctx) -> CmdResult<bool> {
    Ok(sqlx::query_scalar(
        "select coalesce(settings->'ai_redact_pii' = 'true'::jsonb, false) from core.workspaces where id = $1",
    )
    .bind(ctx.workspace_id())
    .fetch_one(&mut *ctx.tx)
    .await?)
}

async fn resolve(ctx: &mut Ctx) -> CmdResult<Resolved> {
    let redact = redact_enabled(ctx).await?;
    let (provider, byo) = match stored_config(ctx).await? {
        Some(c) => {
            // Re-check at call time: DNS may have changed since the URL was saved.
            if let Some(u) = &c.base_url {
                netguard::check_endpoint(u)
                    .await
                    .map_err(CmdError::forbidden)?;
            }
            let key = match &c.key_enc {
                Some(blob) => Some(crypto::decrypt(
                    &crypto::master_key()?,
                    ctx.workspace_id(),
                    blob,
                )?),
                None => None,
            };
            (
                provider_for(&c.provider, key, c.base_url, c.model, c.embedding_model),
                true,
            )
        }
        None => (ctx.registry.ai.clone(), false),
    };
    // The deterministic local provider never leaves the process, so there is nothing to redact.
    let redact = redact && provider.name() != "local";
    Ok(Resolved {
        provider,
        byo,
        redact,
    })
}

#[allow(clippy::too_many_arguments)]
async fn record(
    ctx: &mut Ctx,
    r: &Resolved,
    provider: &str,
    model: &str,
    purpose: &str,
    prompt: Value,
    response: Option<Value>,
    (tokens_in, tokens_out): (u32, u32),
) -> CmdResult<()> {
    let actor = ctx
        .principal
        .agent_id
        .or(ctx.principal.user_id)
        .map(|u| u.to_string());
    sqlx::query(
        "insert into ai.audit (workspace_id, actor_id, provider, model, purpose, prompt, response, tokens_in, tokens_out)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
    )
    .bind(ctx.workspace_id())
    .bind(actor)
    .bind(provider)
    .bind(model)
    .bind(purpose)
    .bind(prompt)
    .bind(response)
    .bind(tokens_in as i32)
    .bind(tokens_out as i32)
    .execute(&mut *ctx.tx)
    .await?;
    if !r.byo {
        let meta = json!({"purpose": purpose, "provider": provider, "model": model});
        let ws = ctx.workspace_id();
        record_usage(
            &mut ctx.tx,
            ws,
            "ai.tokens_in",
            tokens_in as i64,
            meta.clone(),
        )
        .await?;
        record_usage(&mut ctx.tx, ws, "ai.tokens_out", tokens_out as i64, meta).await?;
    }
    Ok(())
}

fn provider_err(e: anyhow::Error) -> CmdError {
    tracing::warn!(error = %e, "model provider call failed");
    CmdError::internal(format!("AI provider error: {e}"))
}

/// Run a completion with redaction, audit and metering.
async fn complete(
    ctx: &mut Ctx,
    purpose: &str,
    messages: Vec<ChatMessage>,
    max_tokens: u32,
) -> CmdResult<Completion> {
    let r = resolve(ctx).await?;
    let messages: Vec<ChatMessage> = if r.redact {
        messages
            .into_iter()
            .map(|m| ChatMessage {
                content: redact(&m.content),
                ..m
            })
            .collect()
    } else {
        messages
    };
    let prompt = json!(messages);
    let c = r
        .provider
        .complete(CompletionRequest {
            purpose: purpose.into(),
            messages,
            max_tokens,
        })
        .await
        .map_err(provider_err)?;
    record(
        ctx,
        &r,
        &c.provider,
        &c.model,
        purpose,
        prompt,
        Some(json!({"text": c.text})),
        (c.tokens_in, c.tokens_out),
    )
    .await?;
    Ok(c)
}

/// Embed texts with redaction, audit and metering. Returns the embedding model id the vectors belong to.
async fn embed(
    ctx: &mut Ctx,
    purpose: &str,
    texts: Vec<String>,
) -> CmdResult<(String, Vec<Vec<f32>>)> {
    let r = resolve(ctx).await?;
    let texts: Vec<String> = if r.redact {
        texts.iter().map(|t| redact(t)).collect()
    } else {
        texts
    };
    let chars: usize = texts.iter().map(|t| t.chars().count()).sum();
    let n = texts.len();
    let vectors = r.provider.embed(texts).await.map_err(provider_err)?;
    let model = r.provider.embedding_model().to_string();
    // Embedding APIs report no per-call token count through the trait; ~4 chars/token is the metering estimate.
    record(
        ctx,
        &r,
        r.provider.name(),
        &model,
        purpose,
        json!({"texts": n, "chars": chars}),
        None,
        ((chars / 4) as u32, 0),
    )
    .await?;
    Ok((model, vectors))
}

fn vec_literal(v: &[f32]) -> String {
    let parts: Vec<String> = v.iter().map(|x| x.to_string()).collect();
    format!("[{}]", parts.join(","))
}

// ---------- BYO-LLM settings ----------

#[derive(Deserialize, JsonSchema)]
pub struct SetProvider {
    /// "anthropic" or "openai" (any OpenAI-compatible endpoint, including self-hosted).
    pub provider: String,
    /// Endpoint base URL, e.g. https://api.openai.com/v1 or your self-hosted gateway. Provider default when omitted.
    #[serde(default)]
    pub base_url: Option<String>,
    /// Chat model id.
    #[serde(default)]
    pub model: Option<String>,
    /// Embedding model id (openai-compatible only; Anthropic has no embeddings).
    #[serde(default)]
    pub embedding_model: Option<String>,
    /// API key. Required when provider or base URL changes; otherwise omit to keep the stored key.
    #[serde(default)]
    pub api_key: Option<String>,
}

async fn set_provider(ctx: &mut Ctx, i: SetProvider) -> CmdResult<Value> {
    if !matches!(i.provider.as_str(), "anthropic" | "openai") {
        return Err(CmdError::invalid(
            "provider must be \"anthropic\" or \"openai\"",
        ));
    }
    let base_url = i
        .base_url
        .map(|u| u.trim().to_string())
        .filter(|u| !u.is_empty());
    if let Some(u) = &base_url {
        netguard::check_endpoint(u)
            .await
            .map_err(CmdError::invalid)?;
    }
    let ws = ctx.workspace_id();
    let existing = stored_config(ctx).await?;
    let new_key = i
        .api_key
        .map(|k| k.trim().to_string())
        .filter(|k| !k.is_empty());
    let key_enc = match new_key {
        // Refuse before anything is stored if the master key is missing or malformed.
        Some(k) => Some(crypto::encrypt(&crypto::master_key()?, ws, &k)?),
        // A stored key is only reused for the endpoint it was given for; it must not follow a changed URL.
        None => existing
            .filter(|e| e.provider == i.provider && e.base_url == base_url)
            .and_then(|e| e.key_enc),
    };
    sqlx::query(
        "insert into ai.provider_config (workspace_id, provider, base_url, model, embedding_model, key_enc, updated_by)
         values ($1,$2,$3,$4,$5,$6,$7)
         on conflict (workspace_id) do update set provider = $2, base_url = $3, model = $4,
           embedding_model = $5, key_enc = $6, updated_by = $7, updated_at = now()",
    )
    .bind(ws)
    .bind(&i.provider)
    .bind(&base_url)
    .bind(&i.model)
    .bind(&i.embedding_model)
    .bind(&key_enc)
    .bind(ctx.principal.user_id)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "ai.provider_changed",
        None,
        json!({"provider": i.provider, "base_url": base_url, "model": i.model, "has_key": key_enc.is_some()}),
    )
    .await?;
    get_provider(ctx, Empty {}).await
}

#[derive(Deserialize, JsonSchema)]
pub struct Empty {}

async fn get_provider(ctx: &mut Ctx, _: Empty) -> CmdResult<Value> {
    let redact_pii = redact_enabled(ctx).await?;
    let process = ctx.registry.ai.name().to_string();
    Ok(match stored_config(ctx).await? {
        Some(c) => json!({
            "configured": true, "provider": c.provider, "base_url": c.base_url, "model": c.model,
            "embedding_model": c.embedding_model, "has_key": c.key_enc.is_some(),
            "redact_pii": redact_pii, "metered": false,
        }),
        None => json!({
            "configured": false, "platform_provider": process, "redact_pii": redact_pii, "metered": true,
        }),
    })
}

async fn clear_provider(ctx: &mut Ctx, _: Empty) -> CmdResult<Value> {
    let n = sqlx::query("delete from ai.provider_config where workspace_id = $1")
        .bind(ctx.workspace_id())
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
    if n > 0 {
        ctx.emit("ai.provider_changed", None, json!({"cleared": true}))
            .await?;
    }
    Ok(json!({"cleared": n > 0}))
}

// ---------- indexing ----------

/// Split into ~`size` char windows with `overlap`, preferring to cut at a line or word boundary.
pub fn chunk_text(text: &str, size: usize, overlap: usize) -> Vec<String> {
    let chars: Vec<char> = text.chars().collect();
    let mut out = vec![];
    let mut start = 0;
    while start < chars.len() {
        let mut end = (start + size).min(chars.len());
        if end < chars.len()
            && let Some(cut) = (start + size / 2..end)
                .rev()
                .find(|&i| chars[i] == '\n')
                .or_else(|| {
                    (start + size / 2..end)
                        .rev()
                        .find(|&i| chars[i].is_whitespace())
                })
        {
            end = cut + 1;
        }
        let piece: String = chars[start..end].iter().collect();
        if !piece.trim().is_empty() {
            out.push(piece.trim().to_string());
        }
        if end >= chars.len() {
            break;
        }
        start = end.saturating_sub(overlap).max(start + 1);
    }
    out
}

fn on_note_event<'a>(ctx: &'a mut Ctx, e: &'a Event) -> BoxFuture<'a, CmdResult<()>> {
    Box::pin(async move {
        let Some(note_id) = e.note_id else {
            return Ok(());
        };
        let ws = ctx.workspace_id();
        events::enqueue(
            &mut ctx.tx,
            Some(ws),
            "ai.index_note",
            json!({"note_id": note_id}),
            None,
        )
        .await
    })
}

fn index_job(ctx: &mut Ctx, payload: Value) -> BoxFuture<'_, CmdResult<()>> {
    Box::pin(async move {
        let note_id: Uuid = payload["note_id"]
            .as_str()
            .and_then(|s| s.parse().ok())
            .ok_or_else(|| CmdError::invalid("ai.index_note needs note_id"))?;
        index_note(ctx, note_id).await
    })
}

/// Replace the note's chunks for the current embedding model. Other models' rows stay until their index is dropped.
pub async fn index_note(ctx: &mut Ctx, note_id: Uuid) -> CmdResult<()> {
    let row: Option<(String, String, String)> = sqlx::query_as(
        "select title, content_md, freshness from core.notes where id = $1 and deleted_at is null",
    )
    .bind(note_id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let chunks = match &row {
        Some((title, content, freshness)) if freshness != "archived" => {
            chunk_text(&format!("{title}\n\n{content}"), CHUNK_CHARS, CHUNK_OVERLAP)
        }
        _ => vec![],
    };
    if chunks.is_empty() {
        sqlx::query("delete from ai.chunks where note_id = $1")
            .bind(note_id)
            .execute(&mut *ctx.tx)
            .await?;
        return Ok(());
    }
    let mut vectors = Vec::with_capacity(chunks.len());
    let mut model = String::new();
    for batch in chunks.chunks(EMBED_BATCH) {
        let (m, v) = embed(ctx, "index", batch.to_vec()).await?;
        model = m;
        vectors.extend(v);
    }
    let literals: Vec<String> = vectors.iter().map(|v| vec_literal(v)).collect();
    let ordinals: Vec<i32> = (0..chunks.len() as i32).collect();
    sqlx::query("delete from ai.chunks where note_id = $1 and model = $2")
        .bind(note_id)
        .bind(&model)
        .execute(&mut *ctx.tx)
        .await?;
    sqlx::query(
        "insert into ai.chunks (workspace_id, note_id, model, ordinal, content, embedding)
         select $1, $2, $3, o, c, e::vector from unnest($4::int[], $5::text[], $6::text[]) as t(o, c, e)",
    )
    .bind(ctx.workspace_id())
    .bind(note_id)
    .bind(&model)
    .bind(&ordinals)
    .bind(&chunks)
    .bind(&literals)
    .execute(&mut *ctx.tx)
    .await?;
    Ok(())
}

// ---------- search.hybrid ----------

#[derive(Deserialize, JsonSchema)]
pub struct HybridSearch {
    /// Natural-language query.
    pub q: String,
    /// Max results (default 10, max 50).
    #[serde(default)]
    pub limit: Option<i64>,
}

#[derive(Serialize, sqlx::FromRow)]
pub struct HybridHit {
    pub id: Uuid,
    pub space_id: Uuid,
    pub title: String,
    pub types: Vec<String>,
    pub freshness: String,
    pub snippet: String,
    /// Reciprocal-rank-fusion score; higher is better.
    pub score: f32,
}

/// Reciprocal rank fusion (k = 60) of the full-text and vector rankings. Both CTEs filter by readable spaces and page
/// restrictions, and exclude archived/deleted notes inside the query, so nothing unreadable can leak through the fusion.
const HYBRID_SQL: &str = "
with ft as (
  select n.id, row_number() over (order by ts_rank(n.search, websearch_to_tsquery('english', $1)) desc) as rnk
  from core.notes n
  where n.deleted_at is null and n.freshness <> 'archived' and n.space_id = any($2)
    and core.note_access(n.id, $6, $7, $2) <> 'none'
    and n.search @@ websearch_to_tsquery('english', $1)
  order by ts_rank(n.search, websearch_to_tsquery('english', $1)) desc
  limit 50
), near as (
  select c.note_id, min(c.embedding <=> $3::vector) as dist
  from ai.chunks c join core.notes n on n.id = c.note_id
  where c.model = $4 and n.deleted_at is null and n.freshness <> 'archived' and n.space_id = any($2)
    and core.note_access(n.id, $6, $7, $2) <> 'none'
  group by c.note_id
  order by dist
  limit 50
), vec as (
  select note_id as id, row_number() over (order by dist) as rnk from near
), fused as (
  select id, sum(1.0 / (60 + rnk)) as score from (select id, rnk from ft union all select id, rnk from vec) u group by id
)
select n.id, n.space_id, n.title, n.types, n.freshness,
       ts_headline('english', n.content_md, websearch_to_tsquery('english', $1), 'MaxWords=30,MinWords=10') as snippet,
       f.score::real as score
from fused f join core.notes n on n.id = f.id
order by f.score desc, n.title
limit $5";

async fn search_hybrid(ctx: &mut Ctx, i: HybridSearch) -> CmdResult<Vec<HybridHit>> {
    let q = i.q.trim();
    if q.is_empty() {
        return Err(CmdError::invalid("q is required"));
    }
    let spaces = readable_space_ids(ctx).await?;
    let (uid, admin) = viewer(ctx);
    let (model, mut v) = embed(ctx, "search", vec![q.to_string()]).await?;
    let query_vec = vec_literal(&v.remove(0));
    // Let the HNSW scan keep going past ef_search when the permission filter discards candidates (pgvector 0.8+).
    sqlx::query("select set_config('hnsw.iterative_scan', 'relaxed_order', true)")
        .execute(&mut *ctx.tx)
        .await?;
    Ok(sqlx::query_as(sqlx::AssertSqlSafe(HYBRID_SQL))
        .bind(q)
        .bind(spaces)
        .bind(query_vec)
        .bind(model)
        .bind(i.limit.unwrap_or(10).clamp(1, 50))
        .bind(uid)
        .bind(admin)
        .fetch_all(&mut *ctx.tx)
        .await?)
}

// ---------- summarize / extract_tasks / draft ----------

#[derive(Deserialize, JsonSchema)]
pub struct NoteRef {
    /// Note id.
    pub note_id: Uuid,
}

async fn note_text(ctx: &mut Ctx, id: Uuid) -> CmdResult<(String, String)> {
    assert_note_access(ctx, id, false).await?;
    let (title, content): (String, String) =
        sqlx::query_as("select title, content_md from core.notes where id = $1")
            .bind(id)
            .fetch_one(&mut *ctx.tx)
            .await?;
    Ok((title, content.chars().take(MAX_NOTE_CHARS).collect()))
}

fn msg(role: &str, content: String) -> ChatMessage {
    ChatMessage {
        role: role.into(),
        content,
    }
}

#[derive(Serialize)]
pub struct TextOut {
    pub text: String,
    pub provider: String,
    pub model: String,
}

impl From<Completion> for TextOut {
    fn from(c: Completion) -> Self {
        Self {
            text: c.text,
            provider: c.provider,
            model: c.model,
        }
    }
}

async fn summarize(ctx: &mut Ctx, i: NoteRef) -> CmdResult<TextOut> {
    let (title, content) = note_text(ctx, i.note_id).await?;
    let c = complete(
        ctx,
        "summarize",
        vec![
            msg("system", "Summarize the note in a few sentences, then list key decisions and open questions. Use only the note's content.".into()),
            msg("user", format!("Title: {title}\n\n{content}")),
        ],
        600,
    )
    .await?;
    Ok(c.into())
}

#[derive(Serialize, Deserialize, Debug, PartialEq)]
pub struct SuggestedTask {
    pub title: String,
    #[serde(default)]
    pub assignee: Option<String>,
    #[serde(default)]
    pub due: Option<String>,
}

/// Models are asked for a JSON array; fall back to bullet lines when they answer in prose.
pub fn parse_tasks(text: &str) -> Vec<SuggestedTask> {
    if let (Some(a), Some(b)) = (text.find('['), text.rfind(']'))
        && a < b
        && let Ok(v) = serde_json::from_str::<Vec<SuggestedTask>>(&text[a..=b])
        && !v.is_empty()
    {
        return v;
    }
    text.lines()
        .filter_map(|l| {
            let l = l.trim();
            let l = l.strip_prefix("- ").or_else(|| l.strip_prefix("* "))?;
            let l = l
                .strip_prefix("[ ] ")
                .or_else(|| l.strip_prefix("[x] "))
                .unwrap_or(l)
                .trim();
            (!l.is_empty()).then(|| SuggestedTask {
                title: l.into(),
                assignee: None,
                due: None,
            })
        })
        .collect()
}

async fn extract_tasks(ctx: &mut Ctx, i: NoteRef) -> CmdResult<Value> {
    let (title, content) = note_text(ctx, i.note_id).await?;
    let c = complete(
        ctx,
        "extract_tasks",
        vec![
            msg("system", "Extract action items from the note. Reply with only a JSON array of objects {\"title\", \"assignee\", \"due\"}; use null for unknown assignee or due date. Do not invent tasks.".into()),
            msg("user", format!("Title: {title}\n\n{content}")),
        ],
        800,
    )
    .await?;
    Ok(json!({
        "suggested_tasks": parse_tasks(&c.text),
        "note": "Suggestions only; create items with the project commands after review.",
        "provider": c.provider, "model": c.model,
    }))
}

#[derive(Deserialize, JsonSchema)]
pub struct Draft {
    /// What to write.
    pub prompt: String,
    /// Optional note to ground the draft in (you must be able to read it).
    #[serde(default)]
    pub note_id: Option<Uuid>,
}

async fn draft(ctx: &mut Ctx, i: Draft) -> CmdResult<TextOut> {
    if i.prompt.trim().is_empty() {
        return Err(CmdError::invalid("prompt is required"));
    }
    let mut messages = vec![msg(
        "system",
        "You draft text for a team workspace. Be concise and direct. Use any provided note as context.".into(),
    )];
    let mut user = String::new();
    if let Some(id) = i.note_id {
        let (title, content) = note_text(ctx, id).await?;
        user.push_str(&format!("Context note: {title}\n\n{content}\n\n---\n\n"));
    }
    user.push_str(i.prompt.trim());
    messages.push(msg("user", user));
    Ok(complete(ctx, "draft", messages, 1200).await?.into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chunks_overlap_and_cover() {
        let text = "alpha beta gamma delta ".repeat(200);
        let chunks = chunk_text(&text, 800, 100);
        assert!(chunks.len() >= 5);
        assert!(chunks.iter().all(|c| c.chars().count() <= 800));
        // overlap: each chunk starts with text that also ended the previous one
        let tail: String = chunks[0]
            .chars()
            .rev()
            .take(30)
            .collect::<Vec<_>>()
            .into_iter()
            .rev()
            .collect();
        assert!(chunks[1].contains(tail.split_whitespace().last().unwrap()));
        assert!(chunk_text("   \n ", 800, 100).is_empty());
        assert_eq!(chunk_text("short", 800, 100), vec!["short"]);
        // multibyte text never splits inside a character
        assert!(!chunk_text(&"नमस्ते ".repeat(300), 800, 100).is_empty());
    }

    #[test]
    fn tasks_from_json_or_bullets() {
        let j = r#"Here you go: [{"title":"Send quote","assignee":"Asha","due":"2026-12-01"},{"title":"Book room"}]"#;
        let t = parse_tasks(j);
        assert_eq!(t.len(), 2);
        assert_eq!(t[0].assignee.as_deref(), Some("Asha"));
        assert_eq!(t[1].due, None);
        let b = parse_tasks("Notes\n- [ ] Email Ravi\n* Update the deck\nnot a task");
        assert_eq!(
            b.iter().map(|t| t.title.as_str()).collect::<Vec<_>>(),
            ["Email Ravi", "Update the deck"]
        );
    }
}
