//! HTTP surface. REST is a thin projection of the command layer (ADR-004): POST /api/w/{ws}/commands/{name}.

use axum::extract::{Path, State};
use axum::http::{HeaderMap, HeaderValue, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use cnote_core::{CmdError, ErrorCode, ExecOptions, Principal, Registry, auth};
use serde::Deserialize;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

pub const SESSION_COOKIE: &str = "cn_session";
/// Native apps (mobile/tablet) can't rely on cookies; they get the session token as `cs_<token>` and send it as a bearer.
const NATIVE_SESSION_PREFIX: &str = "cs_";

#[derive(Clone)]
pub struct AppState {
    pub pool: PgPool,
    pub registry: Arc<Registry>,
}

impl AppState {
    pub async fn from_env() -> anyhow::Result<Self> {
        let url = std::env::var("DATABASE_URL")?;
        let max = std::env::var("DB_POOL_MAX")
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(10);
        Ok(Self {
            pool: cnote_db::connect(&url, max).await?,
            registry: crate::modules::registry(),
        })
    }
}

pub struct ApiError(pub CmdError);

impl From<CmdError> for ApiError {
    fn from(e: CmdError) -> Self {
        Self(e)
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        let status = StatusCode::from_u16(self.0.code.http_status())
            .unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
        (status, Json(json!({ "error": self.0 }))).into_response()
    }
}

type ApiResult<T> = Result<T, ApiError>;

fn cookie(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(';'))
        .find_map(|kv| {
            let (k, v) = kv.trim().split_once('=')?;
            (k == name).then(|| v.to_string())
        })
}

fn bearer(headers: &HeaderMap) -> Option<String> {
    headers
        .get(header::AUTHORIZATION)?
        .to_str()
        .ok()?
        .strip_prefix("Bearer ")
        .map(|s| s.trim().to_string())
}

/// Resolve the caller for a workspace: bearer API key (agents, MCP clients) or session cookie (people).
pub async fn principal_for(
    pool: &PgPool,
    headers: &HeaderMap,
    workspace_id: Uuid,
) -> Result<Principal, CmdError> {
    if let Some(key) = bearer(headers).filter(|k| !k.starts_with(NATIVE_SESSION_PREFIX)) {
        let p = auth::principal_from_bearer(pool, &key, &cnote_mcp::mcp_resource(headers))
            .await?
            .ok_or_else(|| {
                CmdError::new(
                    ErrorCode::Unauthenticated,
                    "invalid API key or access token",
                )
            })?;
        if p.workspace_id != workspace_id {
            return Err(CmdError::forbidden("API key belongs to another workspace"));
        }
        return Ok(p);
    }
    let user = session_user(pool, headers).await?;
    auth::user_principal(pool, user.id, workspace_id).await
}

pub async fn session_user(
    pool: &PgPool,
    headers: &HeaderMap,
) -> Result<auth::SessionUser, CmdError> {
    let token = session_token(headers)
        .ok_or_else(|| CmdError::new(ErrorCode::Unauthenticated, "sign in required"))?;
    auth::user_from_session(pool, &token)
        .await?
        .ok_or_else(|| CmdError::new(ErrorCode::Unauthenticated, "session expired"))
}

fn session_token(headers: &HeaderMap) -> Option<String> {
    bearer(headers)
        .and_then(|b| b.strip_prefix(NATIVE_SESSION_PREFIX).map(str::to_string))
        .or_else(|| cookie(headers, SESSION_COOKIE))
}

fn is_native(headers: &HeaderMap) -> bool {
    headers.get("x-cnote-client").is_some_and(|v| v == "native")
}

/// Cookie for browsers; native clients also get the token in the body (they store it in the device keychain).
fn with_session(mut body: Value, headers: &HeaderMap, token: &str) -> Response {
    if is_native(headers) {
        body["token"] = json!(format!("{NATIVE_SESSION_PREFIX}{token}"));
    }
    let mut res = Json(body).into_response();
    res.headers_mut()
        .insert(header::SET_COOKIE, session_cookie(token, 30 * 86400));
    res
}

fn session_cookie(token: &str, max_age: i64) -> HeaderValue {
    let secure = if std::env::var("COOKIE_SECURE").is_ok_and(|v| v == "1" || v == "true") {
        "; Secure"
    } else {
        ""
    };
    HeaderValue::from_str(&format!(
        "{SESSION_COOKIE}={token}; Path=/; HttpOnly; SameSite=Lax; Max-Age={max_age}{secure}"
    ))
    .unwrap()
}

#[derive(Deserialize)]
struct SignUp {
    email: String,
    name: String,
    password: String,
    #[serde(default)]
    workspace_name: Option<String>,
}

async fn signup(
    State(s): State<AppState>,
    headers: HeaderMap,
    Json(b): Json<SignUp>,
) -> ApiResult<Response> {
    let user_id = auth::sign_up(&s.pool, &b.email, &b.name, &b.password).await?;
    let ws = match b.workspace_name.filter(|n| !n.trim().is_empty()) {
        Some(n) => Some(auth::create_workspace(&s.pool, &s.registry, user_id, &n).await?),
        None => None,
    };
    let token = auth::create_session(&s.pool, user_id).await?;
    Ok(with_session(
        json!({"user_id": user_id, "workspace_id": ws}),
        &headers,
        &token,
    ))
}

#[derive(Deserialize)]
struct Login {
    email: String,
    password: String,
}

async fn login(
    State(s): State<AppState>,
    headers: HeaderMap,
    Json(b): Json<Login>,
) -> ApiResult<Response> {
    let (user_id, token) = auth::sign_in(&s.pool, &b.email, &b.password).await?;
    Ok(with_session(json!({"user_id": user_id}), &headers, &token))
}

async fn logout(State(s): State<AppState>, headers: HeaderMap) -> ApiResult<Response> {
    if let Some(t) = session_token(&headers) {
        auth::destroy_session(&s.pool, &t).await?;
    }
    let mut res = Json(json!({"ok": true})).into_response();
    res.headers_mut()
        .insert(header::SET_COOKIE, session_cookie("", 0));
    Ok(res)
}

async fn me(State(s): State<AppState>, headers: HeaderMap) -> ApiResult<Json<Value>> {
    let user = session_user(&s.pool, &headers).await?;
    let workspaces = auth::user_workspaces(&s.pool, user.id).await?;
    Ok(Json(json!({"user": user, "workspaces": workspaces})))
}

#[derive(Deserialize)]
struct NewWorkspace {
    name: String,
}

async fn create_workspace(
    State(s): State<AppState>,
    headers: HeaderMap,
    Json(b): Json<NewWorkspace>,
) -> ApiResult<Json<Value>> {
    let user = session_user(&s.pool, &headers).await?;
    let id = auth::create_workspace(&s.pool, &s.registry, user.id, &b.name).await?;
    Ok(Json(json!({"id": id})))
}

async fn list_commands(State(s): State<AppState>) -> Json<Value> {
    Json(json!(s.registry.list().iter().map(|c| json!({
        "name": c.name, "description": c.description, "group": c.group, "min_role": c.min_role,
        "confirm": c.confirm, "core": c.core, "input_schema": c.input_schema,
    })).collect::<Vec<_>>()))
}

async fn run_command(
    State(s): State<AppState>,
    Path((ws, name)): Path<(Uuid, String)>,
    headers: HeaderMap,
    body: Option<Json<Value>>,
) -> ApiResult<Json<Value>> {
    let p = principal_for(&s.pool, &headers, ws).await?;
    let opts = ExecOptions {
        idempotency_key: headers
            .get("idempotency-key")
            .and_then(|v| v.to_str().ok())
            .map(String::from),
        confirmation_id: headers
            .get("x-confirmation-id")
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.parse().ok()),
    };
    let input = body.map(|Json(v)| v).unwrap_or(json!({}));
    Ok(Json(
        s.registry.execute(&s.pool, &name, input, &p, &opts).await?,
    ))
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/api/auth/signup", post(signup))
        .route("/api/auth/login", post(login))
        .route("/api/auth/logout", post(logout))
        .route("/api/auth/me", get(me))
        .route("/api/workspaces", post(create_workspace))
        .route("/api/commands", get(list_commands))
        .route("/api/w/{ws}/commands/{name}", post(run_command))
        .route("/api/hooks/github/{ws}", post(github_hook))
        .merge(cnote_mcp::router(
            state.pool.clone(),
            state.registry.clone(),
        ))
        .fallback_service(cnote_collab::router(
            state.pool.clone(),
            state.registry.clone(),
        ))
        .merge(crate::oauth::router())
        .merge(crate::marketplace::router())
        // EXTRA_ROUTES: modules mount routers here (mcp, collab, webhooks)
        // Info-level span: every error logged during a request carries its method and URI (command + workspace).
        .layer(
            tower_http::trace::TraceLayer::new_for_http().make_span_with(
                tower_http::trace::DefaultMakeSpan::new().level(tracing::Level::INFO),
            ),
        )
        .with_state(state)
}

/// GitHub webhook (cnote-projects): signature-verified, creates evidence for items named in PRs and deployments.
async fn github_hook(
    State(s): State<AppState>,
    Path(ws): Path<Uuid>,
    headers: HeaderMap,
    body: axum::body::Bytes,
) -> ApiResult<Json<Value>> {
    let sig = headers
        .get("x-hub-signature-256")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    Ok(Json(
        cnote_projects::handle_github_webhook(&s.pool, &s.registry, ws, sig, &body).await?,
    ))
}
