//! Outbound hooks: when events match (kind prefixes) and/or every N minutes, deliver a JSON body to
//! - `http`: POST to a URL, signed `X-CNote-Signature: sha256=hex(HMAC(secret, "{timestamp}.{body}"))`;
//! - `mcp`: call `tool` on a remote MCP server (streamable HTTP), arguments = `arguments` + `{"cnote": body}`.
//!
//! Delivery runs in the job worker; a failure retries up to MAX_ATTEMPTS with exponential backoff.
//! Commands are Admin-only and also reachable from C Note's own MCP server via `discover`/`invoke`.

use crate::crypto::{decrypt, encrypt, master_key};
use crate::netguard::{check_endpoint, client};
use chrono::{DateTime, Utc};
use cnote_core::auth::{hmac_sha256, random_token};
use cnote_core::events::{Event, enqueue};
use cnote_core::{CmdError, CmdResult, Command, Ctx, Registry, Role, ToolGroup, handler};
use futures::future::BoxFuture;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use std::time::Duration;
use uuid::Uuid;

const MAX_ATTEMPTS: i64 = 5;
const TIMEOUT: Duration = Duration::from_secs(20);

pub fn commands() -> Vec<Command> {
    // Every hook command is Admin: hooks send workspace activity to outside systems.
    let admin = ToolGroup::Admin;
    [
        Command::new(
            "hook.list",
            "List outbound hooks (webhooks, schedules, MCP tool calls) with their last delivery status.",
            admin,
            handler!(list),
        ),
        Command::new(
            "hook.create",
            "Create an outbound hook. target 'http' POSTs a signed JSON body to url; target 'mcp' calls `tool` on the \
             MCP server at url (optional bearer `token`, static `arguments`, plus `cnote` = the body). Fires on events whose \
             kind starts with one of `events` ('*' = all) and/or every `every_minutes` (>= 5, first run at `start_at`). \
             Returns the HTTP signing secret once.",
            admin,
            handler!(create),
        )
        .returns_secret(),
        Command::new("hook.set_enabled", "Pause or resume a hook.", admin, handler!(set_enabled)),
        Command::new("hook.delete", "Delete a hook.", admin, handler!(delete)),
        Command::new(
            "hook.test",
            "Deliver a test body to a hook now and return whether it succeeded.",
            admin,
            handler!(test),
        ),
    ]
    .into_iter()
    .map(|c| c.min_role(Role::Admin))
    .collect()
}

#[derive(Serialize, JsonSchema, sqlx::FromRow)]
pub struct HookOut {
    id: Uuid,
    name: String,
    target: String,
    url: String,
    tool: Option<String>,
    arguments: Value,
    events: Vec<String>,
    every_minutes: Option<i32>,
    next_run_at: Option<DateTime<Utc>>,
    enabled: bool,
    last_run_at: Option<DateTime<Utc>>,
    last_status: Option<String>,
    last_error: Option<String>,
    created_at: DateTime<Utc>,
}

async fn list(ctx: &mut Ctx, _: cnote_core::notes::Empty) -> CmdResult<Vec<HookOut>> {
    Ok(sqlx::query_as(
        "select id, name, target, url, tool, arguments, events, every_minutes, next_run_at, enabled,
                last_run_at, last_status, last_error, created_at
         from core.hooks order by created_at",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct CreateHook {
    name: String,
    /// "http" or "mcp"
    target: String,
    url: String,
    /// MCP tool to call (target "mcp").
    #[serde(default)]
    tool: Option<String>,
    /// Static MCP tool arguments; `cnote` is added with the delivery body.
    #[serde(default)]
    arguments: Option<Value>,
    /// Bearer token for the MCP server (target "mcp"). Stored encrypted.
    #[serde(default)]
    token: Option<String>,
    /// Event kind prefixes, e.g. ["item.created", "note."]; "*" for all.
    #[serde(default)]
    events: Vec<String>,
    #[serde(default)]
    every_minutes: Option<i32>,
    #[serde(default)]
    start_at: Option<DateTime<Utc>>,
}

fn validate(i: &CreateHook) -> CmdResult<()> {
    let bad = |m: &str| Err(CmdError::invalid(m));
    if i.name.trim().is_empty() || i.name.len() > 80 {
        return bad("name must be 1-80 characters");
    }
    if i.events.len() > 20
        || i.events.iter().any(|e| {
            e.is_empty()
                || e.len() > 60
                || !e
                    .chars()
                    .all(|c| c.is_ascii_lowercase() || "._*".contains(c))
        })
    {
        return bad(
            "events must be up to 20 kind prefixes like 'item.created' or 'note.' ('*' for all)",
        );
    }
    if i.every_minutes.is_some_and(|m| !(5..=525_600).contains(&m)) {
        return bad("every_minutes must be between 5 and 525600");
    }
    if i.events.is_empty() && i.every_minutes.is_none() {
        return bad("give events, every_minutes, or both");
    }
    match i.target.as_str() {
        "http" if i.tool.is_some() || i.token.is_some() || i.arguments.is_some() => {
            bad("tool, token and arguments are only for target 'mcp'")
        }
        "http" => Ok(()),
        "mcp" if i.tool.as_deref().is_none_or(|t| t.trim().is_empty()) => {
            bad("tool is required for target 'mcp'")
        }
        "mcp" if i.arguments.as_ref().is_some_and(|a| !a.is_object()) => {
            bad("arguments must be an object")
        }
        "mcp" => Ok(()),
        _ => bad("target must be 'http' or 'mcp'"),
    }
}

async fn create(ctx: &mut Ctx, i: CreateHook) -> CmdResult<Value> {
    validate(&i)?;
    check_endpoint(&i.url)
        .await
        .map_err(|e| CmdError::invalid(e.replace("base_url", "url")))?;
    let ws = ctx.principal.workspace_id;
    let signing = (i.target == "http").then(|| format!("whsec_{}", random_token(24)));
    let secret = signing
        .clone()
        .or(i.token.clone().filter(|t| !t.is_empty()));
    let secret_enc = match &secret {
        Some(s) => Some(encrypt(&master_key()?, ws, s)?),
        None => None,
    };
    let next = i.every_minutes.map(|m| {
        i.start_at
            .unwrap_or_else(|| Utc::now() + chrono::Duration::minutes(m as i64))
    });
    let id: Uuid = sqlx::query_scalar(
        "insert into core.hooks (workspace_id, name, target, url, tool, arguments, secret_enc, events, every_minutes,
                                 next_run_at, created_by)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) returning id",
    )
    .bind(ws)
    .bind(i.name.trim())
    .bind(&i.target)
    .bind(&i.url)
    .bind(&i.tool)
    .bind(i.arguments.unwrap_or_else(|| json!({})))
    .bind(secret_enc)
    .bind(&i.events)
    .bind(i.every_minutes)
    .bind(next)
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    Ok(json!({"id": id, "signing_secret": signing}))
}

#[derive(Deserialize, JsonSchema)]
pub struct SetEnabled {
    id: Uuid,
    enabled: bool,
}

async fn set_enabled(ctx: &mut Ctx, i: SetEnabled) -> CmdResult<Value> {
    let n = sqlx::query("update core.hooks set enabled = $2 where id = $1")
        .bind(i.id)
        .bind(i.enabled)
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
    if n == 0 {
        return Err(CmdError::not_found("hook"));
    }
    Ok(json!({"id": i.id, "enabled": i.enabled}))
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    id: Uuid,
}

async fn delete(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    let n = sqlx::query("delete from core.hooks where id = $1")
        .bind(i.id)
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
    Ok(json!({"deleted": n > 0}))
}

async fn test(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    let body = json!({"type": "test", "hook_id": i.id, "workspace_id": ctx.principal.workspace_id, "sent_at": Utc::now()});
    match deliver(ctx, i.id, &body).await? {
        Ok(()) => Ok(json!({"ok": true})),
        Err(e) => Ok(json!({"ok": false, "error": e})),
    }
}

/// Every event: queue one delivery per enabled hook whose prefixes match. Bodies are built now, so retries resend the same bytes.
pub fn on_event<'a>(ctx: &'a mut Ctx, e: &'a Event) -> BoxFuture<'a, CmdResult<()>> {
    Box::pin(async move {
        let ids: Vec<Uuid> = sqlx::query_scalar(
            "select id from core.hooks
             where enabled and exists (select 1 from unnest(events) p where p = '*' or starts_with($1, p))",
        )
        .bind(&e.kind)
        .fetch_all(&mut *ctx.tx)
        .await?;
        for id in ids {
            let body =
                json!({"type": "event", "hook_id": id, "workspace_id": e.workspace_id, "event": e});
            enqueue(
                &mut ctx.tx,
                Some(e.workspace_id),
                "hook.deliver",
                json!({"hook_id": id, "body": body, "attempt": 1}),
                None,
            )
            .await?;
        }
        Ok(())
    })
}

/// Each minute: advance every due schedule past now (skipping missed runs) and queue one delivery for it.
pub fn sweep(pool: PgPool, _: Arc<Registry>) -> BoxFuture<'static, anyhow::Result<()>> {
    Box::pin(async move {
        let wss: Vec<Uuid> = sqlx::query_scalar("select core.all_workspace_ids()")
            .fetch_all(&pool)
            .await?;
        for ws in wss {
            let mut tx = cnote_db::begin_tenant(&pool, ws).await?;
            let due: Vec<(Uuid, String, DateTime<Utc>)> = sqlx::query_as(
                "with due as (select id, next_run_at from core.hooks
                              where enabled and every_minutes is not null and next_run_at <= now() for update skip locked)
                 update core.hooks h
                    set next_run_at = due.next_run_at + make_interval(mins => h.every_minutes
                          * (floor(extract(epoch from now() - due.next_run_at) / 60 / h.every_minutes)::int + 1))
                 from due where h.id = due.id
                 returning h.id, h.name, due.next_run_at",
            )
            .fetch_all(&mut *tx)
            .await?;
            for (id, name, at) in due {
                let body = json!({"type": "schedule", "hook_id": id, "workspace_id": ws, "name": name, "scheduled_for": at});
                enqueue(
                    &mut tx,
                    Some(ws),
                    "hook.deliver",
                    json!({"hook_id": id, "body": body, "attempt": 1}),
                    None,
                )
                .await
                .map_err(|e| anyhow::anyhow!("{e}"))?;
            }
            tx.commit().await?;
        }
        Ok(())
    })
}

pub fn deliver_job(ctx: &mut Ctx, p: Value) -> BoxFuture<'_, CmdResult<()>> {
    Box::pin(async move {
        let id: Uuid = serde_json::from_value(p["hook_id"].clone())
            .map_err(|_| CmdError::invalid("hook_id"))?;
        let attempt = p["attempt"].as_i64().unwrap_or(1);
        if deliver(ctx, id, &p["body"]).await?.is_err() && attempt < MAX_ATTEMPTS {
            let at = Utc::now() + chrono::Duration::minutes(1 << attempt);
            let retry = json!({"hook_id": id, "body": p["body"], "attempt": attempt + 1});
            enqueue(
                &mut ctx.tx,
                Some(ctx.principal.workspace_id),
                "hook.deliver",
                retry,
                Some(at),
            )
            .await?;
        }
        Ok(())
    })
}

#[derive(sqlx::FromRow)]
struct Target {
    target: String,
    url: String,
    tool: Option<String>,
    arguments: Value,
    secret_enc: Option<Vec<u8>>,
}

/// Outer error: database trouble. Inner: the delivery failed (also recorded on the hook). A missing or paused hook is a no-op.
async fn deliver(ctx: &mut Ctx, id: Uuid, body: &Value) -> CmdResult<Result<(), String>> {
    let t: Option<Target> = sqlx::query_as(
        "select target, url, tool, arguments, secret_enc from core.hooks where id = $1 and enabled",
    )
    .bind(id)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let Some(t) = t else { return Ok(Ok(())) };
    let ws = ctx.principal.workspace_id;
    let r = async {
        // DNS may have changed since the hook was saved.
        check_endpoint(&t.url)
            .await
            .map_err(|e| e.replace("base_url", "url"))?;
        let secret = match &t.secret_enc {
            Some(b) => Some(
                master_key()
                    .and_then(|k| decrypt(&k, ws, b))
                    .map_err(|e| e.to_string())?,
            ),
            None => None,
        };
        let call = async {
            if t.target == "mcp" {
                call_mcp(&t, secret, body).await
            } else {
                post_signed(&t.url, secret.as_deref().unwrap_or_default(), body).await
            }
        };
        tokio::time::timeout(TIMEOUT, call)
            .await
            .map_err(|_| "timed out".to_string())?
    }
    .await;
    if let Err(e) = &r {
        tracing::warn!(hook = %id, workspace = %ws, target = %t.target, error = %e, "hook delivery failed");
    }
    sqlx::query("update core.hooks set last_run_at = now(), last_status = $2, last_error = $3 where id = $1")
        .bind(id)
        .bind(if r.is_ok() { "ok" } else { "failed" })
        .bind(r.as_ref().err())
        .execute(&mut *ctx.tx)
        .await?;
    Ok(r)
}

pub fn signature(secret: &str, ts: i64, body: &str) -> String {
    format!(
        "sha256={}",
        hex::encode(hmac_sha256(
            secret.as_bytes(),
            format!("{ts}.{body}").as_bytes()
        ))
    )
}

async fn post_signed(url: &str, secret: &str, body: &Value) -> Result<(), String> {
    let raw = body.to_string();
    let ts = Utc::now().timestamp();
    let res = client()
        .post(url)
        .header("content-type", "application/json")
        .header("x-cnote-timestamp", ts.to_string())
        .header("x-cnote-signature", signature(secret, ts, &raw))
        .body(raw)
        .send()
        .await
        .map_err(|e| format!("request failed: {e}"))?;
    if res.status().is_success() {
        Ok(())
    } else {
        Err(format!("HTTP {}", res.status().as_u16()))
    }
}

async fn call_mcp(t: &Target, token: Option<String>, body: &Value) -> Result<(), String> {
    use rmcp::ServiceExt;
    use rmcp::model::CallToolRequestParams;
    use rmcp::transport::StreamableHttpClientTransport;
    use rmcp::transport::streamable_http_client::StreamableHttpClientTransportConfig;
    let mut cfg = StreamableHttpClientTransportConfig::with_uri(t.url.clone());
    if let Some(tok) = token {
        cfg = cfg.auth_header(tok);
    }
    let svc =
        ().serve(StreamableHttpClientTransport::with_client(client(), cfg))
            .await
            .map_err(|e| format!("MCP connect failed: {e}"))?;
    let mut args = t.arguments.as_object().cloned().unwrap_or_default();
    args.insert("cnote".into(), body.clone());
    let tool = t.tool.clone().unwrap_or_default();
    let res = svc
        .call_tool(CallToolRequestParams::new(tool).with_arguments(args))
        .await;
    let _ = svc.cancel().await;
    match res {
        Ok(r) if r.is_error == Some(true) => Err(format!("MCP tool error: {}", json!(r.content))),
        Ok(_) => Ok(()),
        Err(e) => Err(format!("MCP call failed: {e}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn signature_is_hmac_of_timestamp_dot_body() {
        let s = signature("k", 1, "{}");
        assert_eq!(
            s,
            format!("sha256={}", hex::encode(hmac_sha256(b"k", b"1.{}")))
        );
    }

    #[test]
    fn validation() {
        let h = |v: Value| serde_json::from_value::<CreateHook>(v).unwrap();
        assert!(
            validate(&h(
                json!({"name":"a","target":"http","url":"u","events":["note."]})
            ))
            .is_ok()
        );
        assert!(validate(&h(json!({"name":"a","target":"http","url":"u"}))).is_err());
        assert!(
            validate(&h(
                json!({"name":"a","target":"http","url":"u","every_minutes":1})
            ))
            .is_err()
        );
        assert!(
            validate(&h(
                json!({"name":"a","target":"mcp","url":"u","every_minutes":5})
            ))
            .is_err()
        );
        assert!(
            validate(&h(
                json!({"name":"a","target":"mcp","url":"u","tool":"t","every_minutes":5})
            ))
            .is_ok()
        );
        assert!(
            validate(&h(
                json!({"name":"a","target":"http","url":"u","events":["DROP;"]})
            ))
            .is_err()
        );
    }
}
