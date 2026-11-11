//! MCP authorization (OAuth 2.1, spec 2025-11-25): protected-resource and authorization-server metadata,
//! dynamic client registration (RFC 7591), client ID metadata documents, authorization code + PKCE (S256 only),
//! RFC 8707 resource binding, rotating refresh tokens with reuse detection, and revocation (RFC 7009).
//! Codes and tokens are opaque; only SHA-256 hashes are stored (migration 0600).

use crate::api::{ApiError, AppState, session_user};
use axum::extract::{ConnectInfo, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Form, Json, Router};
use base64::Engine;
use chrono::{DateTime, Utc};
use cnote_core::auth::{self, sha256_hex};
use cnote_core::{CmdError, ratelimit};
use rand::RngCore;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use tower_http::cors::{Any, CorsLayer};
use url::Url;
use uuid::Uuid;

const ACCESS_TTL_SECS: i64 = 3600;
const REFRESH_TTL_DAYS: i32 = 30;
const CODE_TTL_SECS: i32 = 300;
const METADATA_TTL_SECS: i64 = 3600;
const METADATA_MAX_BYTES: usize = 16 * 1024;
const MAX_REDIRECT_URIS: usize = 10;

pub fn router() -> Router<AppState> {
    // Machine endpoints: bearer/PKCE based, no ambient credentials, so any origin may call them (browser MCP clients).
    let public = Router::new()
        .route(
            "/.well-known/oauth-protected-resource",
            get(protected_resource),
        )
        .route(
            "/.well-known/oauth-protected-resource/mcp",
            get(protected_resource),
        )
        .route(
            "/.well-known/oauth-authorization-server",
            get(authorization_server),
        )
        .route("/oauth/register", post(register))
        .route("/oauth/token", post(token))
        .route("/oauth/revoke", post(revoke))
        .layer(axum::extract::DefaultBodyLimit::max(32 * 1024))
        .layer(
            CorsLayer::new()
                .allow_origin(Any)
                .allow_methods([axum::http::Method::GET, axum::http::Method::POST])
                .allow_headers([header::CONTENT_TYPE, header::AUTHORIZATION]),
        );
    // Consent endpoints: cookie session, deliberately no CORS.
    Router::new()
        .route("/api/oauth/authorize/context", post(authorize_context))
        .route("/api/oauth/authorize", post(authorize))
        .merge(public)
}

// ---------- metadata ----------

fn json_cached(body: Value) -> Response {
    let mut res = Json(body).into_response();
    res.headers_mut().insert(
        header::CACHE_CONTROL,
        "public, max-age=300".parse().expect("static"),
    );
    res
}

/// Issuer/resource origin, or an error when production has not set PUBLIC_BASE_URL (loopback hosts still work).
fn base_of(headers: &HeaderMap) -> Result<String, OauthError> {
    cnote_mcp::public_base(headers).ok_or_else(|| {
        OauthError(
            StatusCode::SERVICE_UNAVAILABLE,
            "server_error",
            "OAuth is not available: the server has no PUBLIC_BASE_URL configured".into(),
        )
    })
}

/// Both well-known URLs describe the same resource: the MCP endpoint.
async fn protected_resource(headers: HeaderMap) -> Response {
    let base = match base_of(&headers) {
        Ok(b) => b,
        Err(e) => return e.into_response(),
    };
    json_cached(json!({
        "resource": format!("{base}/mcp"),
        "authorization_servers": [base],
        "bearer_methods_supported": ["header"],
        "resource_name": "C Note",
    }))
}

fn web_base(base: &str) -> String {
    std::env::var("PUBLIC_WEB_URL")
        .map(|v| v.trim_end_matches('/').to_string())
        .unwrap_or_else(|_| base.to_string())
}

async fn authorization_server(headers: HeaderMap) -> Response {
    let base = match base_of(&headers) {
        Ok(b) => b,
        Err(e) => return e.into_response(),
    };
    json_cached(json!({
        "issuer": base,
        "authorization_endpoint": format!("{}/oauth/authorize", web_base(&base)),
        "token_endpoint": format!("{base}/oauth/token"),
        "registration_endpoint": format!("{base}/oauth/register"),
        "revocation_endpoint": format!("{base}/oauth/revoke"),
        "response_types_supported": ["code"],
        "grant_types_supported": ["authorization_code", "refresh_token"],
        "code_challenge_methods_supported": ["S256"],
        "token_endpoint_auth_methods_supported": ["none"],
        "revocation_endpoint_auth_methods_supported": ["none"],
        "client_id_metadata_document_supported": true,
        "authorization_response_iss_parameter_supported": true,
    }))
}

// ---------- helpers ----------

fn random_secret(prefix: &str) -> String {
    let mut buf = [0u8; 32];
    rand::rng().fill_bytes(&mut buf);
    format!(
        "{prefix}{}",
        base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(buf)
    )
}

/// RFC 6749 error body. `Cache-Control: no-store` on everything the token endpoint returns.
struct OauthError(StatusCode, &'static str, String);

impl OauthError {
    fn new(code: &'static str, desc: impl Into<String>) -> Self {
        Self(StatusCode::BAD_REQUEST, code, desc.into())
    }
}

impl From<sqlx::Error> for OauthError {
    fn from(e: sqlx::Error) -> Self {
        tracing::error!(error = %e, "oauth database error");
        Self(
            StatusCode::INTERNAL_SERVER_ERROR,
            "server_error",
            "internal error".into(),
        )
    }
}

impl IntoResponse for OauthError {
    fn into_response(self) -> Response {
        no_store((
            self.0,
            Json(json!({"error": self.1, "error_description": self.2})),
        ))
    }
}

fn no_store(r: impl IntoResponse) -> Response {
    let mut res = r.into_response();
    let h = res.headers_mut();
    h.insert(header::CACHE_CONTROL, "no-store".parse().expect("static"));
    h.insert(header::PRAGMA, "no-cache".parse().expect("static"));
    res
}

/// Caller address for throttling: the socket peer, or the left-most X-Forwarded-For entry only when the
/// operator says a trusted proxy sets it (TRUST_PROXY=1).
pub(crate) fn client_ip(
    headers: &HeaderMap,
    peer: Option<axum::Extension<ConnectInfo<std::net::SocketAddr>>>,
) -> String {
    if std::env::var("TRUST_PROXY").is_ok_and(|v| v == "1" || v == "true")
        && let Some(ip) = headers
            .get("x-forwarded-for")
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.split(',').next())
            .map(str::trim)
            .filter(|v| v.parse::<std::net::IpAddr>().is_ok())
    {
        return ip.to_string();
    }
    peer.map(|c| (c.0).0.ip().to_string()).unwrap_or_default()
}

fn throttle(key: &str, burst: f64, per_sec: f64) -> Result<(), OauthError> {
    ratelimit::check_with(key, burst, per_sec).map_err(|_| {
        OauthError(
            StatusCode::TOO_MANY_REQUESTS,
            "temporarily_unavailable",
            "too many requests, retry shortly".into(),
        )
    })
}

/// The resource indicator must name this server's MCP endpoint, and nothing else is ever issued. The same
/// token is also accepted on /api/w/{ws}/commands: that API is part of the same protected resource.
fn canonical_resource(raw: &str, base: &str) -> Option<String> {
    let r = raw.trim_end_matches('/');
    (r == format!("{base}/mcp")).then(|| r.to_string())
}

fn host_label(redirect_uri: &str) -> String {
    match Url::parse(redirect_uri) {
        Ok(u) if matches!(u.scheme(), "http" | "https") => {
            let h = u.host_str().unwrap_or_default();
            match u.port() {
                Some(p) => format!("{h}:{p}"),
                None => h.to_string(),
            }
        }
        Ok(u) => format!("{}://{}", u.scheme(), u.host_str().unwrap_or_default()),
        Err(_) => redirect_uri.to_string(),
    }
}

/// https, loopback http (RFC 8252), or a private-use app scheme. No fragments or credentials; exact match later.
fn validate_redirect_uri(raw: &str) -> Result<(), String> {
    if raw.len() > 2048 {
        return Err("redirect_uri too long".into());
    }
    let u = Url::parse(raw).map_err(|_| "redirect_uri is not a valid absolute URI".to_string())?;
    if u.fragment().is_some() || !u.username().is_empty() || u.password().is_some() {
        return Err("redirect_uri must not contain a fragment or credentials".into());
    }
    match u.scheme() {
        "https" => Ok(()),
        "http" => {
            let loopback = match u.host() {
                Some(url::Host::Domain(d)) => d == "localhost",
                Some(url::Host::Ipv4(ip)) => ip.is_loopback(),
                Some(url::Host::Ipv6(ip)) => ip.is_loopback(),
                None => false,
            };
            loopback
                .then_some(())
                .ok_or_else(|| "http redirect_uri is only allowed for loopback".into())
        }
        "javascript" | "data" | "vbscript" | "file" | "blob" | "about" | "ftp" | "ws" | "wss" => {
            Err("redirect_uri scheme not allowed".into())
        }
        _ => Ok(()),
    }
}

fn clean_name(raw: &str) -> String {
    let n: String = raw.chars().filter(|c| !c.is_control()).take(80).collect();
    let n = n.trim();
    if n.is_empty() {
        "Unnamed app".into()
    } else {
        n.into()
    }
}

// ---------- clients ----------

struct Client {
    id: String,
    name: String,
    redirect_uris: Vec<String>,
}

type ClientRow = (String, String, Vec<String>, DateTime<Utc>);

async fn load_client(pool: &PgPool, client_id: &str) -> Result<Option<ClientRow>, sqlx::Error> {
    sqlx::query_as(
        "select client_id, name, redirect_uris, fetched_at from core.oauth_clients where client_id = $1",
    )
    .bind(client_id)
    .fetch_optional(pool)
    .await
}

/// Registered clients come from the table; `https://` client IDs are metadata documents, re-fetched when stale.
async fn resolve_client(pool: &PgPool, client_id: &str) -> Result<Client, String> {
    let db = |_| "server error".to_string();
    let cached = load_client(pool, client_id).await.map_err(db)?;
    let is_doc = client_id.starts_with("https://")
        || (client_id.starts_with("http://") && cimd_private_ok());
    if !is_doc {
        return cached
            .map(|(id, name, redirect_uris, _)| Client {
                id,
                name,
                redirect_uris,
            })
            .ok_or_else(|| "unknown client_id".into());
    }
    if let Some((id, name, redirect_uris, fetched)) = &cached
        && (Utc::now() - *fetched).num_seconds() < METADATA_TTL_SECS
    {
        return Ok(Client {
            id: id.clone(),
            name: name.clone(),
            redirect_uris: redirect_uris.clone(),
        });
    }
    let doc = fetch_client_document(client_id).await?;
    sqlx::query(
        "insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ($1,'metadata',$2,$3)
         on conflict (client_id) do update set name = $2, redirect_uris = $3, fetched_at = now()",
    )
    .bind(client_id)
    .bind(&doc.name)
    .bind(&doc.redirect_uris)
    .execute(pool)
    .await
    .map_err(db)?;
    Ok(Client {
        id: client_id.into(),
        name: doc.name,
        redirect_uris: doc.redirect_uris,
    })
}

fn cimd_private_ok() -> bool {
    std::env::var("OAUTH_CIMD_ALLOW_PRIVATE").is_ok_and(|v| v == "1" || v == "true")
}

struct ClientDoc {
    name: String,
    redirect_uris: Vec<String>,
}

async fn fetch_client_document(client_id: &str) -> Result<ClientDoc, String> {
    let url = Url::parse(client_id).map_err(|_| "invalid client_id URL")?;
    if url.path() == "/" || url.path().is_empty() || url.fragment().is_some() {
        return Err("client_id URL must have a path and no fragment".into());
    }
    if url
        .path_segments()
        .is_some_and(|mut s| s.any(|seg| seg == "." || seg == ".."))
    {
        return Err("client_id URL must not contain dot segments".into());
    }
    let doc = crate::netguard::fetch_json(client_id, METADATA_MAX_BYTES).await?;
    parse_client_document(client_id, &doc)
}

fn parse_client_document(client_id: &str, doc: &Value) -> Result<ClientDoc, String> {
    if doc.get("client_id").and_then(Value::as_str) != Some(client_id) {
        return Err("metadata client_id does not match its URL".into());
    }
    if doc.get("client_secret").is_some()
        || doc
            .get("token_endpoint_auth_method")
            .is_some_and(|m| m != "none")
    {
        return Err("only public clients (token_endpoint_auth_method none) are supported".into());
    }
    let uris: Vec<String> = doc
        .get("redirect_uris")
        .and_then(Value::as_array)
        .ok_or("metadata has no redirect_uris")?
        .iter()
        .map(|v| {
            v.as_str()
                .map(String::from)
                .ok_or("redirect_uris must be strings")
        })
        .collect::<Result<_, _>>()?;
    if uris.is_empty() || uris.len() > MAX_REDIRECT_URIS {
        return Err("redirect_uris must hold 1 to 10 entries".into());
    }
    uris.iter().try_for_each(|u| validate_redirect_uri(u))?;
    let fallback = Url::parse(client_id)
        .ok()
        .and_then(|u| u.host_str().map(String::from))
        .unwrap_or_default();
    Ok(ClientDoc {
        name: clean_name(
            doc.get("client_name")
                .and_then(Value::as_str)
                .unwrap_or(&fallback),
        ),
        redirect_uris: uris,
    })
}

// ---------- dynamic client registration ----------

#[derive(Deserialize)]
struct RegisterReq {
    #[serde(default)]
    client_name: String,
    #[serde(default)]
    redirect_uris: Vec<String>,
    #[serde(default)]
    token_endpoint_auth_method: Option<String>,
    #[serde(default)]
    grant_types: Option<Vec<String>>,
    #[serde(default)]
    response_types: Option<Vec<String>>,
}

fn reg_error(code: &'static str, desc: &str) -> Response {
    OauthError::new(code, desc).into_response()
}

async fn register(
    State(s): State<AppState>,
    headers: HeaderMap,
    peer: Option<axum::Extension<ConnectInfo<std::net::SocketAddr>>>,
    Json(b): Json<RegisterReq>,
) -> Response {
    if let Err(e) = base_of(&headers) {
        return e.into_response();
    }
    if let Err(e) = throttle(
        &format!("oauth:register:{}", client_ip(&headers, peer)),
        10.0,
        0.1,
    ) {
        return e.into_response();
    }
    if b.token_endpoint_auth_method
        .as_deref()
        .is_some_and(|m| m != "none")
    {
        return reg_error(
            "invalid_client_metadata",
            "only token_endpoint_auth_method none is supported",
        );
    }
    let ok_set = |v: &Option<Vec<String>>, allowed: &[&str]| {
        v.as_ref()
            .is_none_or(|v| v.iter().all(|x| allowed.contains(&x.as_str())))
    };
    if !ok_set(&b.grant_types, &["authorization_code", "refresh_token"])
        || !ok_set(&b.response_types, &["code"])
    {
        return reg_error(
            "invalid_client_metadata",
            "unsupported grant_types or response_types",
        );
    }
    if b.redirect_uris.is_empty() || b.redirect_uris.len() > MAX_REDIRECT_URIS {
        return reg_error("invalid_redirect_uri", "provide 1 to 10 redirect_uris");
    }
    if let Err(e) = b
        .redirect_uris
        .iter()
        .try_for_each(|u| validate_redirect_uri(u))
    {
        return reg_error("invalid_redirect_uri", &e);
    }
    let name = clean_name(&b.client_name);
    let client_id = random_secret("cnc_");
    let stored = sqlx::query(
        "insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ($1,'registered',$2,$3)",
    )
    .bind(&client_id)
    .bind(&name)
    .bind(&b.redirect_uris)
    .execute(&s.pool)
    .await;
    if let Err(e) = stored {
        return OauthError::from(e).into_response();
    }
    // Abandoned registrations (never approved by anyone) do not pile up.
    let _ = sqlx::query(
        "delete from core.oauth_clients c where c.kind = 'registered' and c.created_at < now() - interval '30 days'
         and not exists (select 1 from core.oauth_grants g where g.client_id = c.client_id)",
    )
    .execute(&s.pool)
    .await;
    // Cached metadata documents nobody ever approved are re-fetchable, so they go quickly.
    let _ = sqlx::query(
        "delete from core.oauth_clients c where c.kind = 'metadata' and c.fetched_at < now() - interval '1 day'
         and not exists (select 1 from core.oauth_grants g where g.client_id = c.client_id)",
    )
    .execute(&s.pool)
    .await;
    no_store((
        StatusCode::CREATED,
        Json(json!({
            "client_id": client_id,
            "client_name": name,
            "redirect_uris": b.redirect_uris,
            "token_endpoint_auth_method": "none",
            "grant_types": ["authorization_code", "refresh_token"],
            "response_types": ["code"],
        })),
    ))
}

// ---------- authorization (consent) ----------

#[derive(Deserialize)]
struct AuthzParams {
    #[serde(default)]
    client_id: String,
    #[serde(default)]
    redirect_uri: String,
    #[serde(default)]
    response_type: String,
    #[serde(default)]
    code_challenge: String,
    #[serde(default)]
    code_challenge_method: String,
    #[serde(default)]
    resource: String,
    #[serde(default)]
    state: Option<String>,
    #[serde(default)]
    scope: Option<String>,
}

struct Validated {
    client: Client,
    redirect_uri: String,
    challenge: String,
    resource: String,
    state: Option<String>,
    scope: String,
}

/// An authorization request problem. `redirect_to` is set only once client_id and redirect_uri are verified;
/// before that the user must be shown the error instead (RFC 6749 4.1.2.1).
struct AuthzErr {
    code: &'static str,
    desc: String,
    redirect_to: Option<String>,
}

fn with_query(base: &str, pairs: &[(&str, &str)]) -> Option<String> {
    let mut u = Url::parse(base).ok()?;
    u.query_pairs_mut().extend_pairs(pairs.iter().copied());
    Some(u.into())
}

fn error_redirect(
    redirect_uri: &str,
    state: Option<&str>,
    iss: &str,
    code: &str,
    desc: &str,
) -> Option<String> {
    let mut pairs = vec![("error", code), ("error_description", desc), ("iss", iss)];
    if let Some(st) = state {
        pairs.push(("state", st));
    }
    with_query(redirect_uri, &pairs)
}

fn is_b64url(s: &str) -> bool {
    s.bytes()
        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}

async fn validate_authz(pool: &PgPool, base: &str, p: &AuthzParams) -> Result<Validated, AuthzErr> {
    let fail = |code, desc: &str| AuthzErr {
        code,
        desc: desc.into(),
        redirect_to: None,
    };
    if p.client_id.is_empty() || p.client_id.len() > 2048 {
        return Err(fail("invalid_request", "client_id is required"));
    }
    let client = resolve_client(pool, &p.client_id)
        .await
        .map_err(|e| fail("invalid_request", &e))?;
    // Exact string match against the registered URIs.
    if !client.redirect_uris.contains(&p.redirect_uri) {
        return Err(fail(
            "invalid_request",
            "redirect_uri does not match a registered redirect URI",
        ));
    }
    let redirectable = |code: &'static str, desc: &str| AuthzErr {
        code,
        desc: desc.into(),
        redirect_to: error_redirect(&p.redirect_uri, p.state.as_deref(), base, code, desc),
    };
    if p.response_type != "code" {
        return Err(redirectable(
            "unsupported_response_type",
            "response_type must be code",
        ));
    }
    if p.code_challenge_method != "S256" {
        return Err(redirectable(
            "invalid_request",
            "PKCE with code_challenge_method S256 is required",
        ));
    }
    if p.code_challenge.len() != 43 || !is_b64url(&p.code_challenge) {
        return Err(redirectable(
            "invalid_request",
            "code_challenge must be a base64url SHA-256 digest",
        ));
    }
    if p.resource.is_empty() {
        return Err(redirectable(
            "invalid_target",
            "resource parameter is required",
        ));
    }
    let Some(resource) = canonical_resource(&p.resource, base) else {
        return Err(redirectable(
            "invalid_target",
            "resource must be this server's MCP endpoint",
        ));
    };
    Ok(Validated {
        client,
        redirect_uri: p.redirect_uri.clone(),
        challenge: p.code_challenge.clone(),
        resource,
        state: p.state.clone(),
        scope: p
            .scope
            .clone()
            .unwrap_or_default()
            .chars()
            .take(200)
            .collect(),
    })
}

/// Consent page data: who is asking, where the browser will return, and the workspaces the user can pick.
async fn authorize_context(
    State(s): State<AppState>,
    headers: HeaderMap,
    Json(p): Json<AuthzParams>,
) -> Result<Response, ApiError> {
    let user = session_user(&s.pool, &headers).await?;
    let base = base_of(&headers).map_err(|_| CmdError::internal("OAuth is not configured"))?;
    // Each call may trigger an outbound metadata fetch and a cache row, so it is bounded per user.
    ratelimit::check_with(&format!("oauth:authz:{}", user.id), 20.0, 0.5)?;
    match validate_authz(&s.pool, &base, &p).await {
        Ok(v) => {
            let workspaces = auth::user_workspaces(&s.pool, user.id).await?;
            Ok(no_store(Json(json!({
                "ok": true,
                "client_name": v.client.name,
                "redirect_host": host_label(&v.redirect_uri),
                "workspaces": workspaces,
            }))))
        }
        Err(e) => Ok(no_store(Json(json!({
            "ok": false, "error": e.code, "description": e.desc, "redirect_to": e.redirect_to,
        })))),
    }
}

#[derive(Deserialize)]
struct AuthorizeReq {
    #[serde(flatten)]
    params: AuthzParams,
    workspace_id: Uuid,
    approve: bool,
}

async fn authorize(
    State(s): State<AppState>,
    headers: HeaderMap,
    Json(b): Json<AuthorizeReq>,
) -> Result<Response, ApiError> {
    // Belt and braces next to JSON-only bodies: a cross-site page cannot trigger an approval.
    if headers
        .get("sec-fetch-site")
        .is_some_and(|v| v == "cross-site")
    {
        return Err(CmdError::forbidden("cross-site request refused").into());
    }
    let user = session_user(&s.pool, &headers).await?;
    let base = base_of(&headers).map_err(|_| CmdError::internal("OAuth is not configured"))?;
    ratelimit::check_with(&format!("oauth:authz:{}", user.id), 20.0, 0.5)?;
    let v = match validate_authz(&s.pool, &base, &b.params).await {
        Ok(v) => v,
        Err(e) => {
            return Ok(no_store(Json(json!({
                "ok": false, "error": e.code, "description": e.desc, "redirect_to": e.redirect_to,
            }))));
        }
    };
    if !b.approve {
        let to = error_redirect(
            &v.redirect_uri,
            v.state.as_deref(),
            &base,
            "access_denied",
            "the user denied the request",
        );
        return Ok(no_store(Json(json!({"ok": true, "redirect_to": to}))));
    }
    // Membership is re-checked here: the workspace id comes from the browser.
    auth::user_principal(&s.pool, user.id, b.workspace_id).await?;
    let code = random_secret("cnx_");
    sqlx::query(
        "insert into core.oauth_codes (code_hash, client_id, user_id, workspace_id, redirect_uri, code_challenge, resource, scope, expires_at)
         values ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(secs => $9))",
    )
    .bind(sha256_hex(&code))
    .bind(&v.client.id)
    .bind(user.id)
    .bind(b.workspace_id)
    .bind(&v.redirect_uri)
    .bind(&v.challenge)
    .bind(&v.resource)
    .bind(&v.scope)
    .bind(CODE_TTL_SECS as f64)
    .execute(&s.pool)
    .await
    .map_err(|e| {
        tracing::error!(error = %e, "storing authorization code failed");
        CmdError::internal("could not complete the authorization")
    })?;
    let mut pairs = vec![("code", code.as_str()), ("iss", base.as_str())];
    if let Some(st) = v.state.as_deref() {
        pairs.push(("state", st));
    }
    let to = with_query(&v.redirect_uri, &pairs);
    Ok(no_store(Json(json!({"ok": true, "redirect_to": to}))))
}

// ---------- token endpoint ----------

#[derive(Deserialize)]
struct TokenReq {
    grant_type: Option<String>,
    code: Option<String>,
    redirect_uri: Option<String>,
    client_id: Option<String>,
    code_verifier: Option<String>,
    refresh_token: Option<String>,
    resource: Option<String>,
}

async fn token(
    State(s): State<AppState>,
    headers: HeaderMap,
    peer: Option<axum::Extension<ConnectInfo<std::net::SocketAddr>>>,
    Form(b): Form<TokenReq>,
) -> Response {
    match token_inner(&s.pool, &headers, &client_ip(&headers, peer), b).await {
        Ok(v) => no_store(Json(v)),
        Err(e) => e.into_response(),
    }
}

async fn token_inner(
    pool: &PgPool,
    headers: &HeaderMap,
    ip: &str,
    b: TokenReq,
) -> Result<Value, OauthError> {
    // Per caller address only: junk from one source cannot starve anyone else. The per-client bucket is charged
    // later, once a real code or refresh token was found, so strangers cannot block a public client.
    throttle(&format!("oauth:token:ip:{ip}"), 60.0, 2.0)?;
    let client_id = b
        .client_id
        .as_deref()
        .filter(|c| !c.is_empty())
        .ok_or_else(|| OauthError::new("invalid_request", "client_id is required"))?;
    let base = base_of(headers)?;
    match b.grant_type.as_deref() {
        Some("authorization_code") => exchange_code(pool, &base, client_id, &b).await,
        Some("refresh_token") => refresh(pool, &base, client_id, &b).await,
        _ => Err(OauthError::new(
            "unsupported_grant_type",
            "grant_type must be authorization_code or refresh_token",
        )),
    }
}

fn invalid_grant(desc: &str) -> OauthError {
    OauthError::new("invalid_grant", desc)
}

fn check_resource(requested: &Option<String>, bound: &str, base: &str) -> Result<(), OauthError> {
    match requested.as_deref().filter(|r| !r.is_empty()) {
        Some(r) if canonical_resource(r, base).as_deref() != Some(bound) => Err(OauthError::new(
            "invalid_target",
            "resource does not match the one this grant was issued for",
        )),
        _ => Ok(()),
    }
}

/// Insert a fresh access + refresh pair for a grant and build the response body.
async fn issue_tokens(
    tx: &mut sqlx::PgConnection,
    grant: Uuid,
    scope: &str,
) -> Result<Value, sqlx::Error> {
    let access = random_secret("cno_");
    let refresh = random_secret("cnr_");
    for (tok, kind, ttl) in [
        (&access, "access", ACCESS_TTL_SECS),
        (&refresh, "refresh", REFRESH_TTL_DAYS as i64 * 86400),
    ] {
        sqlx::query(
            "insert into core.oauth_tokens (token_hash, kind, grant_id, expires_at) values ($1,$2,$3, now() + make_interval(secs => $4))",
        )
        .bind(sha256_hex(tok))
        .bind(kind)
        .bind(grant)
        .bind(ttl as f64)
        .execute(&mut *tx)
        .await?;
    }
    Ok(json!({
        "access_token": access,
        "token_type": "Bearer",
        "expires_in": ACCESS_TTL_SECS,
        "refresh_token": refresh,
        "scope": scope,
    }))
}

async fn revoke_grant(pool: &PgPool, grant: Uuid) -> Result<(), sqlx::Error> {
    let mut tx = pool.begin().await?;
    revoke_grant_tx(&mut tx, grant).await?;
    tx.commit().await
}

async fn revoke_grant_tx(tx: &mut sqlx::PgConnection, grant: Uuid) -> Result<(), sqlx::Error> {
    sqlx::query(
        "update core.oauth_grants set revoked_at = coalesce(revoked_at, now()) where id = $1",
    )
    .bind(grant)
    .execute(&mut *tx)
    .await?;
    sqlx::query("delete from core.oauth_tokens where grant_id = $1")
        .bind(grant)
        .execute(&mut *tx)
        .await?;
    Ok(())
}

type CodeRow = (
    String,
    Uuid,
    Uuid,
    String,
    String,
    String,
    String,
    bool,
    bool,
    Option<Uuid>,
);

async fn exchange_code(
    pool: &PgPool,
    base: &str,
    client_id: &str,
    b: &TokenReq,
) -> Result<Value, OauthError> {
    let code = b
        .code
        .as_deref()
        .filter(|c| !c.is_empty())
        .ok_or_else(|| OauthError::new("invalid_request", "code is required"))?;
    let verifier = b
        .code_verifier
        .as_deref()
        .ok_or_else(|| OauthError::new("invalid_request", "code_verifier is required"))?;
    // RFC 7636: 43..128 unreserved characters.
    if !(43..=128).contains(&verifier.len())
        || !verifier
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"-._~".contains(&c))
    {
        return Err(OauthError::new(
            "invalid_request",
            "malformed code_verifier",
        ));
    }
    let hash = sha256_hex(code);
    let row: Option<CodeRow> = sqlx::query_as(
        "select client_id, user_id, workspace_id, redirect_uri, code_challenge, resource, scope,
                used_at is not null, expires_at < now(), grant_id
         from core.oauth_codes where code_hash = $1",
    )
    .bind(&hash)
    .fetch_optional(pool)
    .await?;
    let Some((cid, user, ws, redirect, challenge, resource, scope, used, expired, grant)) = row
    else {
        return Err(invalid_grant("unknown or expired code"));
    };
    throttle(&format!("oauth:client:{cid}"), 30.0, 1.0)?;
    if used {
        // A replayed code means it leaked: revoke what it already produced.
        if let Some(g) = grant {
            revoke_grant(pool, g).await?;
        }
        return Err(invalid_grant("code already used"));
    }
    if expired || cid != client_id || b.redirect_uri.as_deref() != Some(redirect.as_str()) {
        return Err(invalid_grant("code, client or redirect_uri mismatch"));
    }
    let computed = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(Sha256::digest(verifier.as_bytes()));
    if computed != challenge {
        return Err(invalid_grant("PKCE verification failed"));
    }
    check_resource(&b.resource, &resource, base)?;

    let mut tx = pool.begin().await?;
    // Single-use: the conditional update is the arbiter if two exchanges race.
    let claimed = sqlx::query(
        "update core.oauth_codes set used_at = now() where code_hash = $1 and used_at is null",
    )
    .bind(&hash)
    .execute(&mut *tx)
    .await?
    .rows_affected();
    if claimed != 1 {
        return Err(invalid_grant("code already used"));
    }
    let grant: Uuid = sqlx::query_scalar(
        "insert into core.oauth_grants (client_id, user_id, workspace_id, resource, scope) values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(client_id)
    .bind(user)
    .bind(ws)
    .bind(&resource)
    .bind(&scope)
    .fetch_one(&mut *tx)
    .await?;
    sqlx::query("update core.oauth_codes set grant_id = $2 where code_hash = $1")
        .bind(&hash)
        .bind(grant)
        .execute(&mut *tx)
        .await?;
    let out = issue_tokens(&mut tx, grant, &scope).await?;
    tx.commit().await?;
    // Housekeeping for spent material, off the hot path of any one client.
    let _ = sqlx::query("delete from core.oauth_codes where expires_at < now() - interval '1 day'")
        .execute(pool)
        .await;
    let _ =
        sqlx::query("delete from core.oauth_tokens where expires_at < now() - interval '1 day'")
            .execute(pool)
            .await;
    Ok(out)
}

async fn refresh(
    pool: &PgPool,
    base: &str,
    client_id: &str,
    b: &TokenReq,
) -> Result<Value, OauthError> {
    let presented = b
        .refresh_token
        .as_deref()
        .filter(|t| t.starts_with("cnr_"))
        .ok_or_else(|| OauthError::new("invalid_request", "refresh_token is required"))?;
    let mut tx = pool.begin().await?;
    let row: Option<(Uuid, bool, bool, String, String, bool, String, Uuid, Uuid)> = sqlx::query_as(
        "select t.grant_id, t.used_at is not null, t.expires_at < now(), g.client_id, g.resource,
                g.revoked_at is not null, g.scope, g.user_id, g.workspace_id
         from core.oauth_tokens t join core.oauth_grants g on g.id = t.grant_id
         where t.token_hash = $1 and t.kind = 'refresh' for update of t",
    )
    .bind(sha256_hex(presented))
    .fetch_optional(&mut *tx)
    .await?;
    let Some((grant, used, expired, cid, resource, revoked, scope, user, ws)) = row else {
        return Err(invalid_grant("unknown refresh token"));
    };
    if cid != client_id || revoked {
        return Err(invalid_grant("refresh token is not valid for this client"));
    }
    throttle(&format!("oauth:client:{cid}"), 30.0, 1.0)?;
    if used {
        // Rotation means each refresh token works once; a second use is a stolen copy. Kill the whole family.
        revoke_grant_tx(&mut tx, grant).await?;
        tx.commit().await?;
        return Err(invalid_grant("refresh token reuse detected; grant revoked"));
    }
    if expired {
        return Err(invalid_grant("refresh token expired"));
    }
    check_resource(&b.resource, &resource, base)?;
    // Whoever approved this grant must still belong to the workspace.
    if auth::user_principal(pool, user, ws).await.is_err() {
        revoke_grant_tx(&mut tx, grant).await?;
        tx.commit().await?;
        return Err(invalid_grant(
            "the user is no longer a member of the workspace",
        ));
    }
    sqlx::query("update core.oauth_tokens set used_at = now() where token_hash = $1")
        .bind(sha256_hex(presented))
        .execute(&mut *tx)
        .await?;
    // The previous access token dies with the refresh token that minted it.
    sqlx::query("delete from core.oauth_tokens where grant_id = $1 and kind = 'access'")
        .bind(grant)
        .execute(&mut *tx)
        .await?;
    let out = issue_tokens(&mut tx, grant, &scope).await?;
    tx.commit().await?;
    Ok(out)
}

// ---------- revocation (RFC 7009) ----------

#[derive(Deserialize)]
struct RevokeReq {
    token: Option<String>,
    client_id: Option<String>,
}

/// Always 200 (RFC 7009), whether or not the token existed. Revoking a refresh token ends the whole grant;
/// the caller must be the client the token was issued to.
async fn revoke(
    State(s): State<AppState>,
    headers: HeaderMap,
    peer: Option<axum::Extension<ConnectInfo<std::net::SocketAddr>>>,
    Form(b): Form<RevokeReq>,
) -> Response {
    if let Err(e) = throttle(
        &format!("oauth:revoke:{}", client_ip(&headers, peer)),
        30.0,
        1.0,
    ) {
        return e.into_response();
    }
    if let (Some(token), Some(client_id)) = (b.token.as_deref(), b.client_id.as_deref()) {
        let hash = sha256_hex(token);
        let row: Result<Option<(Uuid, String, String)>, _> = sqlx::query_as(
            "select t.grant_id, t.kind, g.client_id from core.oauth_tokens t
             join core.oauth_grants g on g.id = t.grant_id where t.token_hash = $1",
        )
        .bind(&hash)
        .fetch_optional(&s.pool)
        .await;
        match row {
            Ok(Some((grant, kind, cid))) if cid == client_id => {
                let r = if kind == "refresh" {
                    revoke_grant(&s.pool, grant).await.map(|_| ())
                } else {
                    sqlx::query("delete from core.oauth_tokens where token_hash = $1")
                        .bind(&hash)
                        .execute(&s.pool)
                        .await
                        .map(|_| ())
                };
                if let Err(e) = r {
                    return OauthError::from(e).into_response();
                }
            }
            Err(e) => return OauthError::from(e).into_response(),
            _ => {}
        }
    }
    no_store(StatusCode::OK)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redirect_uri_rules() {
        for ok in [
            "https://claude.ai/api/mcp/auth_callback",
            "http://localhost:6274/oauth/callback",
            "http://127.0.0.1:8080/cb",
            "http://[::1]:9/cb",
            "cursor://anysphere.cursor-retrieval/oauth/callback",
        ] {
            assert!(validate_redirect_uri(ok).is_ok(), "{ok}");
        }
        for bad in [
            "http://example.com/cb",
            "http://localhost.evil.com/cb",
            "https://example.com/cb#frag",
            "https://a@example.com/cb",
            "javascript:alert(1)",
            "data:text/html,x",
            "/relative",
            "",
        ] {
            assert!(validate_redirect_uri(bad).is_err(), "{bad}");
        }
    }

    #[test]
    fn resource_must_be_this_server() {
        let base = "https://notes.example.com";
        assert_eq!(
            canonical_resource("https://notes.example.com/mcp/", base).as_deref(),
            Some("https://notes.example.com/mcp")
        );
        assert!(canonical_resource("https://notes.example.com", base).is_none());
        assert!(canonical_resource("https://evil.example.com/mcp", base).is_none());
        assert!(canonical_resource("https://notes.example.com.evil.io/mcp", base).is_none());
    }

    #[test]
    fn metadata_document_rules() {
        let id = "https://app.example.com/client.json";
        let good = json!({"client_id": id, "client_name": "App", "redirect_uris": ["https://app.example.com/cb"]});
        assert_eq!(parse_client_document(id, &good).unwrap().name, "App");
        let other = json!({"client_id": "https://x.example.com/c.json", "redirect_uris": ["https://app.example.com/cb"]});
        assert!(parse_client_document(id, &other).is_err());
        let secret = json!({"client_id": id, "token_endpoint_auth_method": "client_secret_basic", "redirect_uris": ["https://app.example.com/cb"]});
        assert!(parse_client_document(id, &secret).is_err());
        let bad_uri = json!({"client_id": id, "redirect_uris": ["http://app.example.com/cb"]});
        assert!(parse_client_document(id, &bad_uri).is_err());
    }

    #[test]
    fn consent_shows_redirect_host() {
        assert_eq!(host_label("https://claude.ai/cb"), "claude.ai");
        assert_eq!(host_label("http://localhost:6274/cb"), "localhost:6274");
        assert_eq!(
            host_label("cursor://anysphere.x/cb"),
            "cursor://anysphere.x"
        );
    }
}
