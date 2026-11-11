//! `POST /api/marketplace/token`: an installed app trades its one-time install code (plus client secret) for an API key.
//! Server-to-server, so no CORS. Every failure is the same `invalid_grant`.

use crate::api::AppState;
use crate::oauth::client_ip;
use axum::extract::{ConnectInfo, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use axum::{Json, Router};
use cnote_core::{marketplace, ratelimit};
use serde::Deserialize;
use serde_json::json;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/api/marketplace/token", post(token))
        .layer(axum::extract::DefaultBodyLimit::max(4 * 1024))
}

#[derive(Deserialize)]
struct TokenReq {
    client_id: String,
    client_secret: String,
    code: String,
}

async fn token(
    State(s): State<AppState>,
    headers: HeaderMap,
    peer: Option<axum::Extension<ConnectInfo<std::net::SocketAddr>>>,
    Json(b): Json<TokenReq>,
) -> Response {
    let ip = client_ip(&headers, peer);
    if ratelimit::check_with(&format!("market:token:ip:{ip}"), 20.0, 1.0).is_err() {
        return StatusCode::TOO_MANY_REQUESTS.into_response();
    }
    match marketplace::exchange_token(&s.pool, &b.client_id, &b.client_secret, &b.code).await {
        Ok(Some(v)) => ([(axum::http::header::CACHE_CONTROL, "no-store")], Json(v)).into_response(),
        Ok(None) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "invalid_grant"})),
        )
            .into_response(),
        Err(e) => {
            tracing::error!("marketplace token: {e}");
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }
    }
}
