//! cnote-mcp: the MCP server over the command registry (ADR-004). Tools are the `.core()` commands plus
//! `discover` and `invoke`; notes and saved views are resources. Every call runs through `Registry::execute`,
//! so permission checks, tool groups, idempotency and confirmations are the same as for REST and the UI.

pub mod apps;

use axum::Router;
use axum::extract::{Request, State};
use axum::http::{HeaderValue, StatusCode, header, request::Parts};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use cnote_core::access::assert_space_access;
use cnote_core::{
    CmdError, Command, Ctx, ErrorCode, ExecOptions, Principal, Registry, ToolGroup, auth,
};
use rmcp::model::*;
use rmcp::service::{RequestContext, RoleServer};
use rmcp::transport::streamable_http_server::session::never::NeverSessionManager;
use rmcp::transport::streamable_http_server::{StreamableHttpServerConfig, StreamableHttpService};
use rmcp::{ErrorData, ServerHandler, ServiceExt};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use std::time::Duration;
use uuid::Uuid;

const RECENT_NOTES: i64 = 50;

const INSTRUCTIONS: &str = "C Note: notes, projects, docs, CRM and invoicing on one object model. \
Core tools are listed directly. Use `discover` to find any other command by keyword (it returns schemas), \
then run it with `invoke`. Money-moving and outbound commands return confirmation_required; a human must approve \
them in C Note, then call `invoke` again with the confirmation_id.";

const INVOKE_DESCRIPTION: &str = "Run any C Note command by name (dots or underscores), with its arguments. \
Optional `idempotency_key` makes a retried write safe. Commands that move money or send messages \
(invoices, email, WhatsApp) do not run immediately: the result has confirmation_required=true and a \
confirmation_id. A human must approve that request in C Note (an MCP elicitation acceptance is NOT approval); \
after they have, call invoke again with the same command and `confirmation_id`. The approved payload is the one that runs.";

#[derive(Clone)]
pub struct McpServer {
    pool: PgPool,
    registry: Arc<Registry>,
    /// stdio serves one fixed identity; HTTP resolves the principal per request.
    fixed: Option<Principal>,
}

/// Streamable HTTP endpoint at `/mcp`, stateless, authenticated with `Authorization: Bearer` (a `cn_` API key or a `cno_` OAuth access token).
/// Generic over the caller's router state so it merges into any app router.
pub fn router<S: Clone + Send + Sync + 'static>(
    pool: PgPool,
    registry: Arc<Registry>,
) -> Router<S> {
    let server = McpServer {
        pool: pool.clone(),
        registry,
        fixed: None,
    };
    // Bearer-token auth carries no ambient credentials, so the loopback-only Host allowlist (a DNS-rebinding
    // guard for local servers) would only break real deployments.
    let config = StreamableHttpServerConfig::default()
        .with_legacy_session_mode(false)
        .with_json_response(true)
        .with_sse_keep_alive(None)
        .disable_allowed_hosts();
    let service = StreamableHttpService::new(
        move || Ok(server.clone()),
        Arc::new(NeverSessionManager::default()),
        config,
    );
    Router::new()
        .nest_service("/mcp", service)
        .layer(middleware::from_fn_with_state(pool, authenticate))
}

/// Public origin of this server (no trailing slash), for OAuth metadata and challenges. Production sets
/// PUBLIC_BASE_URL. Without it only loopback hosts are served (local development); any other Host gets None,
/// so a spoofed Host header can never mint issuer/resource URLs.
pub fn public_base(headers: &axum::http::HeaderMap) -> Option<String> {
    if let Ok(v) = std::env::var("PUBLIC_BASE_URL") {
        return Some(v.trim_end_matches('/').to_string());
    }
    let host = headers.get(header::HOST).and_then(|v| v.to_str().ok())?;
    let name = match host.strip_prefix('[') {
        Some(rest) => rest.split(']').next()?,
        None => host.split(':').next()?,
    };
    matches!(name, "localhost" | "127.0.0.1" | "::1").then(|| format!("http://{host}"))
}

/// The protected resource tokens are issued for and checked against.
pub fn mcp_resource(headers: &axum::http::HeaderMap) -> String {
    public_base(headers)
        .map(|b| format!("{b}/mcp"))
        .unwrap_or_default()
}

/// 401 with the RFC 9728 pointer so MCP clients can discover the authorization server.
fn unauthorized(headers: &axum::http::HeaderMap, error: Option<&str>) -> Response {
    let mut challenge = "Bearer realm=\"cnote\"".to_string();
    if let Some(base) = public_base(headers) {
        challenge.push_str(&format!(
            ", resource_metadata=\"{base}/.well-known/oauth-protected-resource/mcp\""
        ));
    }
    if let Some(e) = error {
        challenge.push_str(&format!(", error=\"{e}\""));
    }
    let mut res = (
        StatusCode::UNAUTHORIZED,
        axum::Json(json!({"error": {"code": "unauthenticated", "message": "Authorization: Bearer <cn_ API key or cno_ OAuth token> required"}})),
    )
        .into_response();
    res.headers_mut().insert(
        header::WWW_AUTHENTICATE,
        HeaderValue::from_str(&challenge).unwrap_or_else(|_| HeaderValue::from_static("Bearer")),
    );
    res
}

async fn authenticate(State(pool): State<PgPool>, mut req: Request, next: Next) -> Response {
    let Some(key) = req
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.strip_prefix("Bearer "))
        .map(|k| k.trim().to_string())
    else {
        return unauthorized(req.headers(), None);
    };
    match auth::principal_from_bearer(&pool, &key, &mcp_resource(req.headers())).await {
        Ok(Some(p)) => {
            req.extensions_mut().insert(p);
            next.run(req).await
        }
        Ok(None) => unauthorized(req.headers(), Some("invalid_token")),
        Err(e) => {
            tracing::error!(error = %e, "bearer lookup failed");
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }
    }
}

/// `cnote mcp-stdio`: the same server over stdin/stdout for one API key's identity.
pub async fn serve_stdio(
    pool: PgPool,
    registry: Arc<Registry>,
    principal: Principal,
) -> anyhow::Result<()> {
    let server = McpServer {
        pool,
        registry,
        fixed: Some(principal),
    };
    server
        .serve(rmcp::transport::stdio())
        .await?
        .waiting()
        .await?;
    Ok(())
}

// ---------- tool catalogue ----------

pub fn tool_name(command: &str) -> String {
    command.replace('.', "_")
}

fn schema_object(v: &Value) -> Arc<JsonObject> {
    Arc::new(v.as_object().cloned().unwrap_or_default())
}

fn command_tool(c: &Command) -> Tool {
    let mut description = c.description.to_string();
    if c.confirm {
        description.push_str(
            " Requires human approval in C Note: returns confirmation_required and a confirmation_id; \
             after approval, call `invoke` with this command and that confirmation_id.",
        );
    }
    let annotations = ToolAnnotations::new()
        .read_only(c.group == ToolGroup::Read)
        .destructive(c.group == ToolGroup::Destructive);
    Tool::new(
        tool_name(c.name),
        description,
        schema_object(&c.input_schema),
    )
    .annotate(annotations)
}

fn discover_tool() -> Tool {
    Tool::new(
        "discover",
        "Search ALL C Note commands by keyword (name and description). Returns names, descriptions, tool groups and input schemas; run one with `invoke`.",
        schema_object(&json!({
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Keywords, e.g. \"invoice send\" or \"deal stage\". Empty lists everything."},
                "limit": {"type": "integer", "description": "Max results (default 15)"}
            },
            "required": ["query"]
        })),
    )
    .annotate(ToolAnnotations::new().read_only(true))
}

fn invoke_tool() -> Tool {
    Tool::new(
        "invoke",
        INVOKE_DESCRIPTION,
        schema_object(&json!({
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Command name, e.g. \"invoice.issue\" (from `discover`)"},
                "arguments": {"type": "object", "description": "The command's input, per its schema"},
                "idempotency_key": {"type": "string", "description": "Retrying with the same key returns the first result instead of repeating the write"},
                "confirmation_id": {"type": "string", "description": "Id of a request a human approved in C Note"}
            },
            "required": ["command"]
        })),
    )
    .annotate(ToolAnnotations::new().destructive(true))
}

pub fn tool_list(registry: &Registry) -> Vec<Tool> {
    let mut tools: Vec<Tool> = registry
        .list()
        .into_iter()
        .filter(|c| c.core)
        .map(command_tool)
        .collect();
    tools.push(discover_tool());
    tools.push(invoke_tool());
    tools
}

/// Keyword search over command names and descriptions. Every query word must appear; name hits rank higher.
pub fn discover(registry: &Registry, query: &str, limit: usize) -> Vec<Value> {
    let words: Vec<String> = query
        .to_lowercase()
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| !w.is_empty())
        .map(String::from)
        .collect();
    let mut hits: Vec<(usize, &Command)> = registry
        .list()
        .into_iter()
        .filter_map(|c| {
            let name = c.name.to_lowercase();
            let desc = c.description.to_lowercase();
            let mut score = 0;
            for w in &words {
                if name.contains(w.as_str()) {
                    score += 3;
                } else if desc.contains(w.as_str()) {
                    score += 1;
                } else {
                    return None;
                }
            }
            Some((score, c))
        })
        .collect();
    hits.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.name.cmp(b.1.name)));
    hits.into_iter()
        .take(limit)
        .map(|(_, c)| {
            json!({
                "name": c.name,
                "description": c.description,
                "group": c.group,
                "confirmation_required": c.confirm,
                "input_schema": c.input_schema,
            })
        })
        .collect()
}

/// Accepts "invoice.issue" or "invoice_issue".
fn resolve_command<'a>(registry: &'a Registry, name: &str) -> Option<&'a Command> {
    registry.get(name).or_else(|| {
        registry
            .list()
            .into_iter()
            .find(|c| tool_name(c.name) == name)
    })
}

// ---------- results ----------

fn tool_ok(v: Value) -> CallToolResult {
    let text = serde_json::to_string_pretty(&v).unwrap_or_default();
    let mut r = CallToolResult::success(vec![ContentBlock::text(text)]);
    // structuredContent must be an object.
    if v.is_object() {
        r.structured_content = Some(v);
    }
    r
}

/// MCP tool error carrying the CmdError code, so agents can branch on it (forbidden, not_found, invalid, ...).
fn tool_err(e: &CmdError) -> CallToolResult {
    let body = json!({"error": e});
    let mut r = CallToolResult::error(vec![ContentBlock::text(body.to_string())]);
    r.structured_content = Some(body);
    r
}

fn rpc_err(e: CmdError) -> ErrorData {
    match e.code {
        ErrorCode::NotFound => ErrorData::resource_not_found(e.message, None),
        ErrorCode::Invalid => ErrorData::invalid_params(e.message, None),
        _ => ErrorData::internal_error(format!("{:?}: {}", e.code, e.message), None),
    }
}

fn confirmation_id(result: &Value) -> Option<Uuid> {
    (result["confirmation_required"] == json!(true))
        .then(|| result["confirmation_id"].as_str()?.parse().ok())
        .flatten()
}

fn approval_message(id: Uuid) -> String {
    format!(
        "This action needs human approval. Ask the person to approve request {id} in C Note \
         (Settings > Confirmations). Your acceptance here is not approval. After they approve, call `invoke` \
         again with the same command and confirmation_id {id}."
    )
}

// ---------- the handler ----------

impl McpServer {
    fn principal(&self, ctx: &RequestContext<RoleServer>) -> Result<Principal, ErrorData> {
        if let Some(p) = &self.fixed {
            return Ok(p.clone());
        }
        ctx.extensions
            .get::<Parts>()
            .and_then(|parts| parts.extensions.get::<Principal>())
            .cloned()
            .ok_or_else(|| ErrorData::invalid_request("unauthenticated", None))
    }

    async fn run(
        &self,
        p: &Principal,
        command: &str,
        args: Value,
        opts: &ExecOptions,
    ) -> Result<Value, CmdError> {
        self.registry
            .execute(&self.pool, command, args, p, opts)
            .await
    }

    /// Run `f` in a tenant transaction (resources are read through the command layer in one snapshot).
    async fn with_ctx<T>(
        &self,
        p: &Principal,
        f: impl AsyncFnOnce(&mut Ctx) -> Result<T, CmdError>,
    ) -> Result<T, CmdError> {
        let tx = cnote_db::begin_tenant(&self.pool, p.workspace_id).await?;
        let mut ctx = Ctx {
            tx,
            principal: p.clone(),
            registry: self.registry.clone(),
        };
        let out = f(&mut ctx).await?;
        ctx.tx.commit().await?;
        Ok(out)
    }

    /// Execute a command for a tool call and shape the reply, handling the human-approval flow.
    async fn exec_tool(
        &self,
        ctx: &RequestContext<RoleServer>,
        p: &Principal,
        command: &str,
        mut args: Value,
        idempotency_key: Option<String>,
        confirmation_id_in: Option<Uuid>,
    ) -> CallToolResponse {
        // Meta-arguments are ours, not the command's.
        let mut idem = idempotency_key;
        let mut confirm = confirmation_id_in;
        if let Some(o) = args.as_object_mut() {
            idem = idem.or_else(|| {
                o.remove("idempotency_key")
                    .and_then(|v| v.as_str().map(String::from))
            });
            confirm = confirm.or_else(|| {
                o.remove("confirmation_id")
                    .and_then(|v| v.as_str()?.parse().ok())
            });
        }
        let opts = ExecOptions {
            idempotency_key: idem,
            confirmation_id: confirm,
        };
        match self.run(p, command, args, &opts).await {
            Err(e) => tool_err(&e).into(),
            Ok(v) => match confirmation_id(&v) {
                Some(id) => self.ask_human(ctx, id, v).await,
                None => tool_ok(v).into(),
            },
        }
    }

    /// A confirmation was created. If the client can elicit, nudge the person to approve in C Note. Their reply is
    /// never treated as approval: only a decision recorded in C Note by an authorised human unlocks the command.
    async fn ask_human(
        &self,
        ctx: &RequestContext<RoleServer>,
        id: Uuid,
        result: Value,
    ) -> CallToolResponse {
        let can_elicit = ctx
            .client_capabilities()
            .is_some_and(|c| c.elicitation.is_some());
        let message = approval_message(id);
        if can_elicit {
            let request = ElicitRequestParams::FormElicitationParams {
                meta: None,
                message: message.clone(),
                requested_schema: serde_json::from_value(json!({
                    "type": "object",
                    "properties": {"noted": {"type": "boolean", "title": "I will approve it in C Note"}}
                }))
                .expect("static elicitation schema"),
            };
            let stateless = ctx.protocol_version().is_none_or(|v| !v.has_initialize());
            if stateless {
                // 2026-07-28 forbids server-to-client requests: ask via a multi round-trip result instead.
                let mut requests = InputRequests::new();
                requests.insert(
                    "approval".into(),
                    InputRequest::Elicitation(ElicitRequest::new(request)),
                );
                return InputRequiredResult::new(
                    Some(requests),
                    Some(format!("confirmation:{id}")),
                )
                .into();
            }
            if !ctx.peer.supported_elicitation_modes().is_empty() {
                let ask = ctx.peer.create_elicitation(request);
                let _ = tokio::time::timeout(Duration::from_secs(30), ask).await;
            }
        }
        let mut out = result;
        out["message"] = json!(message);
        tool_ok(out).into()
    }
}

fn parse_confirmation_state(state: &str) -> Option<Uuid> {
    state.strip_prefix("confirmation:")?.parse().ok()
}

impl ServerHandler for McpServer {
    fn get_info(&self) -> ServerConfig {
        ServerConfig::new(
            ServerCapabilities::builder()
                .enable_tools()
                .enable_resources()
                .build(),
        )
        .with_instructions(INSTRUCTIONS)
    }

    async fn list_tools(
        &self,
        _: Option<PaginatedRequestParams>,
        ctx: RequestContext<RoleServer>,
    ) -> Result<ListToolsResult, ErrorData> {
        self.principal(&ctx)?;
        Ok(ListToolsResult::with_all_items(tool_list(&self.registry)))
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        ctx: RequestContext<RoleServer>,
    ) -> Result<CallToolResponse, ErrorData> {
        let p = self.principal(&ctx)?;
        // Retry after our elicitation round trip: the command already ran once and created the confirmation.
        if let Some(id) = request
            .request_state
            .as_deref()
            .and_then(parse_confirmation_state)
        {
            return Ok(tool_ok(json!({
                "confirmation_required": true,
                "confirmation_id": id,
                "message": approval_message(id),
            }))
            .into());
        }
        let args = Value::Object(request.arguments.unwrap_or_default());
        let name = request.name.as_ref();
        Ok(match name {
            "discover" => {
                let q = args["query"].as_str().unwrap_or_default();
                let limit = args["limit"].as_u64().unwrap_or(15).clamp(1, 50) as usize;
                tool_ok(json!({"commands": discover(&self.registry, q, limit)})).into()
            }
            "invoke" => {
                let Some(command) = args["command"].as_str() else {
                    return Ok(tool_err(&CmdError::invalid("command is required")).into());
                };
                let Some(cmd) = resolve_command(&self.registry, command) else {
                    return Ok(
                        tool_err(&CmdError::not_found(&format!("command {command}"))).into(),
                    );
                };
                let inner = args.get("arguments").cloned().unwrap_or_else(|| json!({}));
                let idem = args["idempotency_key"].as_str().map(String::from);
                let confirm = args["confirmation_id"]
                    .as_str()
                    .and_then(|s| s.parse().ok());
                self.exec_tool(&ctx, &p, cmd.name, inner, idem, confirm)
                    .await
            }
            _ => {
                // Only the advertised core tools are callable by name; everything else goes through `invoke`.
                let cmd = self
                    .registry
                    .list()
                    .into_iter()
                    .find(|c| c.core && tool_name(c.name) == name);
                let Some(cmd) = cmd else {
                    return Err(ErrorData::invalid_params(
                        format!("unknown tool {name}"),
                        None,
                    ));
                };
                self.exec_tool(&ctx, &p, cmd.name, args, None, None).await
            }
        })
    }

    async fn list_resources(
        &self,
        _: Option<PaginatedRequestParams>,
        ctx: RequestContext<RoleServer>,
    ) -> Result<ListResourcesResult, ErrorData> {
        let p = self.principal(&ctx)?;
        let ws = p.workspace_id;
        let rows: Vec<(Uuid, String, String)> = self
            .with_ctx(&p, async |c| {
                let spaces = cnote_core::access::readable_space_ids(c).await?;
                let (uid, admin) = cnote_core::access::viewer(c);
                Ok(sqlx::query_as(
                    "select id, title, freshness from core.notes
                     where deleted_at is null and freshness <> 'archived' and space_id = any($1)
                       and core.note_access(id, $3, $4, $1) <> 'none'
                     order by updated_at desc limit $2",
                )
                .bind(spaces)
                .bind(RECENT_NOTES)
                .bind(uid)
                .bind(admin)
                .fetch_all(&mut *c.tx)
                .await?)
            })
            .await
            .map_err(rpc_err)?;
        let resources = rows
            .into_iter()
            .map(|(id, title, freshness)| {
                Resource::new(format!("cnote://ws/{ws}/note/{id}"), title)
                    .with_mime_type("text/markdown")
                    .with_description(format!("Note ({freshness})"))
            })
            .collect();
        Ok(ListResourcesResult::with_all_items(resources))
    }

    async fn list_resource_templates(
        &self,
        _: Option<PaginatedRequestParams>,
        _: RequestContext<RoleServer>,
    ) -> Result<ListResourceTemplatesResult, ErrorData> {
        Ok(ListResourceTemplatesResult::with_all_items(vec![
            ResourceTemplate::new("cnote://ws/{ws}/note/{id}", "note")
                .with_description(
                    "A note as Markdown with title, owner, freshness and verify_by front matter",
                )
                .with_mime_type("text/markdown"),
            ResourceTemplate::new("cnote://ws/{ws}/view/{id}", "view")
                .with_description(
                    "A saved view: its CNQL query and, when available, its current results",
                )
                .with_mime_type("text/markdown"),
        ]))
    }

    async fn read_resource(
        &self,
        request: ReadResourceRequestParams,
        ctx: RequestContext<RoleServer>,
    ) -> Result<ReadResourceResponse, ErrorData> {
        let p = self.principal(&ctx)?;
        let (ws, kind, id) = parse_uri(&request.uri).ok_or_else(|| {
            ErrorData::invalid_params(format!("bad resource uri {}", request.uri), None)
        })?;
        // A key is bound to one workspace; another workspace's resource does not exist as far as it can tell.
        if ws != p.workspace_id {
            return Err(ErrorData::resource_not_found("resource not found", None));
        }
        let text = self
            .with_ctx(&p, async |c| match kind {
                "note" => read_note(c, id).await,
                _ => read_view(c, id).await,
            })
            .await
            .map_err(rpc_err)?;
        Ok(ReadResourceResult::new(vec![
            ResourceContents::text(text, request.uri).with_mime_type("text/markdown"),
        ])
        .into())
    }
}

/// `cnote://ws/{ws}/note/{id}` or `.../view/{id}`.
pub fn parse_uri(uri: &str) -> Option<(Uuid, &str, Uuid)> {
    let mut parts = uri.strip_prefix("cnote://ws/")?.split('/');
    let ws = parts.next()?.parse().ok()?;
    let kind = parts.next().filter(|k| matches!(*k, "note" | "view"))?;
    let id = parts.next()?.parse().ok()?;
    parts.next().is_none().then_some((ws, kind, id))
}

/// JSON strings are valid YAML scalars, so front matter values are JSON-encoded.
fn front_matter(fields: &[(&str, Value)]) -> String {
    let mut out = String::from("---\n");
    for (k, v) in fields.iter().filter(|(_, v)| !v.is_null()) {
        out.push_str(&format!("{k}: {v}\n"));
    }
    out.push_str("---\n");
    out
}

pub fn note_markdown(note: &Value, owner: Option<&str>) -> String {
    let fm = front_matter(&[
        ("title", note["title"].clone()),
        (
            "owner",
            owner
                .map(|o| json!(o))
                .unwrap_or_else(|| note["owner_id"].clone()),
        ),
        ("freshness", note["freshness"].clone()),
        ("verify_by", note["verify_by"].clone()),
        ("verified_at", note["verified_at"].clone()),
        ("types", note["types"].clone()),
        ("id", note["id"].clone()),
        ("updated_at", note["updated_at"].clone()),
    ]);
    format!("{fm}\n{}", note["content_md"].as_str().unwrap_or_default())
}

async fn read_note(ctx: &mut Ctx, id: Uuid) -> Result<String, CmdError> {
    let note = ctx.call("note.get", json!({"id": id})).await?;
    let owner: Option<String> = match note["owner_id"]
        .as_str()
        .and_then(|s| s.parse::<Uuid>().ok())
    {
        Some(o) => {
            sqlx::query_scalar("select name from core.users where id = $1")
                .bind(o)
                .fetch_optional(&mut *ctx.tx)
                .await?
        }
        None => None,
    };
    Ok(note_markdown(&note, owner.as_deref()))
}

async fn read_view(ctx: &mut Ctx, id: Uuid) -> Result<String, CmdError> {
    let view: Option<(String, String, String, Option<Uuid>)> =
        sqlx::query_as("select name, layout, query, space_id from core.views where id = $1")
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    let (name, layout, query, space) = view.ok_or_else(|| CmdError::not_found("view"))?;
    if let Some(space) = space {
        assert_space_access(ctx, space, false).await?;
    }
    let mut out = front_matter(&[
        ("name", json!(name)),
        ("layout", json!(layout)),
        ("query", json!(query)),
    ]);
    if ctx.registry.get("item.list").is_some() {
        let rows = ctx.call("item.list", json!({"cnql": query})).await?;
        out.push_str(&format!(
            "\n```json\n{}\n```\n",
            serde_json::to_string_pretty(&rows).unwrap_or_default()
        ));
    } else {
        out.push_str(&format!("\n{query}\n"));
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn registry() -> Arc<Registry> {
        Registry::new(vec![cnote_core::module()])
    }

    #[test]
    fn tools_are_core_commands_plus_discover_and_invoke() {
        let reg = registry();
        let tools = tool_list(&reg);
        let names: Vec<_> = tools.iter().map(|t| t.name.to_string()).collect();
        assert!(names.contains(&"note_create".to_string()));
        assert!(names.contains(&"discover".to_string()) && names.contains(&"invoke".to_string()));
        assert!(
            !names.contains(&"note_delete".to_string()),
            "non-core commands are reached through invoke"
        );
        assert!(names.iter().all(|n| !n.contains('.')));
        let get = tools.iter().find(|t| t.name == "note_get").unwrap();
        assert_eq!(get.annotations.as_ref().unwrap().read_only_hint, Some(true));
        let create = tools.iter().find(|t| t.name == "note_create").unwrap();
        assert_eq!(
            create.annotations.as_ref().unwrap().read_only_hint,
            Some(false)
        );
        assert_eq!(create.input_schema["type"], "object");
        assert!(create.input_schema["properties"]["title"].is_object());
    }

    #[test]
    fn destructive_commands_are_annotated() {
        let reg = registry();
        let del = reg.get("note.delete").unwrap();
        assert_eq!(
            command_tool(del).annotations.unwrap().destructive_hint,
            Some(true)
        );
    }

    #[test]
    fn discover_ranks_name_hits_and_requires_all_words() {
        let reg = registry();
        let hits = discover(&reg, "note delete", 5);
        assert_eq!(hits[0]["name"], "note.delete");
        assert!(hits[0]["input_schema"].is_object());
        assert!(discover(&reg, "zzzz nothing", 5).is_empty());
        assert!(discover(&reg, "", 3).len() == 3);
        assert_eq!(
            resolve_command(&reg, "note_create").unwrap().name,
            "note.create"
        );
    }

    #[test]
    fn uri_parsing() {
        let (ws, id) = (Uuid::new_v4(), Uuid::new_v4());
        assert_eq!(
            parse_uri(&format!("cnote://ws/{ws}/note/{id}")),
            Some((ws, "note", id))
        );
        assert_eq!(
            parse_uri(&format!("cnote://ws/{ws}/view/{id}")),
            Some((ws, "view", id))
        );
        assert!(parse_uri(&format!("cnote://ws/{ws}/space/{id}")).is_none());
        assert!(parse_uri(&format!("cnote://ws/{ws}/note/{id}/x")).is_none());
        assert!(parse_uri("https://example.com").is_none());
    }

    #[test]
    fn note_front_matter() {
        let note = json!({"title": "Runbook: \"prod\"", "owner_id": null, "freshness": "stale", "verify_by": "2026-12-01",
            "verified_at": null, "types": [], "id": "n1", "updated_at": "t", "content_md": "# Body"});
        let md = note_markdown(&note, Some("Tess"));
        assert!(md.starts_with("---\ntitle: \"Runbook: \\\"prod\\\"\"\nowner: \"Tess\"\nfreshness: \"stale\"\nverify_by: \"2026-12-01\"\n"));
        assert!(!md.contains("verified_at"), "nulls are omitted");
        assert!(md.ends_with("---\n\n# Body"));
    }

    #[test]
    fn confirmation_detection() {
        let id = Uuid::new_v4();
        assert_eq!(
            confirmation_id(&json!({"confirmation_required": true, "confirmation_id": id})),
            Some(id)
        );
        assert_eq!(confirmation_id(&json!({"id": 1})), None);
        assert_eq!(
            parse_confirmation_state(&format!("confirmation:{id}")),
            Some(id)
        );
    }
}
