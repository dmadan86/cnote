//! MCP OAuth 2.1 end to end: the real `cnote serve` binary on a random port, plain HTTP, real Postgres.
//! Run `scripts/setup-db.sh` first. Credentials are generated per run.

use base64::Engine;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::process::Stdio;
use tokio::process::{Child, Command};
use url::Url;
use uuid::Uuid;

const REDIRECT: &str = "http://127.0.0.1:7777/callback";

struct Srv {
    base: String,
    http: reqwest::Client,
    _child: Child,
}

async fn start() -> Srv {
    start_with(true).await
}

/// `configured`: whether PUBLIC_BASE_URL is set (otherwise only loopback Hosts are served).
async fn start_with(configured: bool) -> Srv {
    let port = std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port();
    let base = format!("http://127.0.0.1:{port}");
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_cnote"));
    cmd.args(["serve", "--port", &port.to_string()])
        .current_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/../.."))
        .env("OAUTH_CIMD_ALLOW_PRIVATE", "1");
    if configured {
        cmd.env("PUBLIC_BASE_URL", &base);
    } else {
        cmd.env_remove("PUBLIC_BASE_URL");
    }
    let child = cmd
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    let http = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .unwrap();
    for _ in 0..100 {
        if http.get(format!("{base}/healthz")).send().await.is_ok() {
            return Srv {
                base,
                http,
                _child: child,
            };
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    panic!("server did not start");
}

struct User {
    cookie: String,
    ws: Uuid,
}

impl Srv {
    fn url(&self, p: &str) -> String {
        format!("{}{p}", self.base)
    }

    async fn signup(&self) -> User {
        let res = self
            .http
            .post(self.url("/api/auth/signup"))
            .json(&json!({
                "email": format!("{}@test.local", Uuid::new_v4()),
                "name": "Olive",
                "password": Uuid::new_v4().to_string(),
                "workspace_name": "Acme",
            }))
            .send()
            .await
            .unwrap();
        assert!(res.status().is_success());
        let cookie = res.headers()[reqwest::header::SET_COOKIE]
            .to_str()
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .to_string();
        let ws = res.json::<Value>().await.unwrap()["workspace_id"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        User { cookie, ws }
    }

    async fn register(&self, redirect: &str) -> String {
        let res = self
            .http
            .post(self.url("/oauth/register"))
            .json(&json!({"client_name": "Test Agent", "redirect_uris": [redirect], "token_endpoint_auth_method": "none"}))
            .send()
            .await
            .unwrap();
        assert_eq!(res.status(), 201);
        res.json::<Value>().await.unwrap()["client_id"]
            .as_str()
            .unwrap()
            .to_string()
    }

    fn authz_body(&self, client: &str, redirect: &str, challenge: &str) -> Value {
        json!({
            "client_id": client, "redirect_uri": redirect, "response_type": "code",
            "code_challenge": challenge, "code_challenge_method": "S256",
            "resource": format!("{}/mcp", self.base), "state": "st-1",
        })
    }

    /// POST /api/oauth/authorize as the signed-in user.
    async fn authorize(&self, u: &User, mut body: Value, ws: Uuid, approve: bool) -> (u16, Value) {
        body["workspace_id"] = json!(ws);
        body["approve"] = json!(approve);
        let res = self
            .http
            .post(self.url("/api/oauth/authorize"))
            .header("cookie", &u.cookie)
            .json(&body)
            .send()
            .await
            .unwrap();
        (res.status().as_u16(), res.json().await.unwrap())
    }

    async fn token(&self, form: &[(&str, &str)]) -> (u16, Value) {
        let res = self
            .http
            .post(self.url("/oauth/token"))
            .form(form)
            .send()
            .await
            .unwrap();
        assert_eq!(res.headers()["cache-control"], "no-store");
        (res.status().as_u16(), res.json().await.unwrap())
    }

    /// Register, approve, and exchange: returns (client_id, token response).
    async fn connect(&self, u: &User) -> (String, Value) {
        let client = self.register(REDIRECT).await;
        let (verifier, challenge) = pkce();
        let (_, out) = self
            .authorize(
                u,
                self.authz_body(&client, REDIRECT, &challenge),
                u.ws,
                true,
            )
            .await;
        let code = query(&out["redirect_to"], "code");
        let (st, tok) = self
            .token(&[
                ("grant_type", "authorization_code"),
                ("code", &code),
                ("redirect_uri", REDIRECT),
                ("client_id", &client),
                ("code_verifier", &verifier),
            ])
            .await;
        assert_eq!(st, 200, "{tok}");
        (client, tok)
    }

    async fn mcp_status(&self, bearer: &str) -> u16 {
        self.http
            .post(self.url("/mcp"))
            .header("accept", "application/json, text/event-stream")
            .bearer_auth(bearer)
            .json(&json!({"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}))
            .send()
            .await
            .unwrap()
            .status()
            .as_u16()
    }

    async fn command(&self, bearer: &str, ws: Uuid, name: &str) -> (u16, Value) {
        let res = self
            .http
            .post(self.url(&format!("/api/w/{ws}/commands/{name}")))
            .bearer_auth(bearer)
            .json(&json!({}))
            .send()
            .await
            .unwrap();
        (
            res.status().as_u16(),
            res.json().await.unwrap_or(Value::Null),
        )
    }
}

async fn db() -> sqlx::PgPool {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    cnote_db::connect(&std::env::var("DATABASE_URL").unwrap(), 2)
        .await
        .unwrap()
}

fn pkce() -> (String, String) {
    let verifier = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
    let challenge = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(Sha256::digest(verifier.as_bytes()));
    (verifier, challenge)
}

fn query(redirect_to: &Value, key: &str) -> String {
    Url::parse(redirect_to.as_str().expect("redirect_to"))
        .unwrap()
        .query_pairs()
        .find(|(k, _)| k == key)
        .unwrap_or_else(|| panic!("{key} missing in {redirect_to}"))
        .1
        .into_owned()
}

#[tokio::test]
async fn discovery_documents_and_challenge() {
    let s = start().await;
    for path in [
        "/.well-known/oauth-protected-resource",
        "/.well-known/oauth-protected-resource/mcp",
    ] {
        let m: Value = s
            .http
            .get(s.url(path))
            .send()
            .await
            .unwrap()
            .json()
            .await
            .unwrap();
        assert_eq!(m["resource"], format!("{}/mcp", s.base));
        assert_eq!(m["authorization_servers"][0], s.base);
    }
    let res = s
        .http
        .get(s.url("/.well-known/oauth-authorization-server"))
        .header("origin", "https://inspector.example")
        .send()
        .await
        .unwrap();
    assert_eq!(res.headers()["access-control-allow-origin"], "*");
    let m: Value = res.json().await.unwrap();
    assert_eq!(m["issuer"], s.base);
    assert_eq!(m["code_challenge_methods_supported"], json!(["S256"]));
    assert_eq!(m["token_endpoint"], format!("{}/oauth/token", s.base));
    assert_eq!(m["client_id_metadata_document_supported"], true);

    let res = s
        .http
        .post(s.url("/mcp"))
        .json(&json!({}))
        .send()
        .await
        .unwrap();
    assert_eq!(res.status(), 401);
    let challenge = res.headers()["www-authenticate"]
        .to_str()
        .unwrap()
        .to_string();
    assert!(
        challenge.contains(&format!(
            "resource_metadata=\"{}/.well-known/oauth-protected-resource/mcp\"",
            s.base
        )),
        "{challenge}"
    );
}

#[tokio::test]
async fn full_flow_refresh_rotation_reuse_and_revocation() {
    let s = start().await;
    let u = s.signup().await;
    let (client, tok) = s.connect(&u).await;
    let access = tok["access_token"].as_str().unwrap().to_string();
    let refresh = tok["refresh_token"].as_str().unwrap().to_string();
    assert!(access.starts_with("cno_") && refresh.starts_with("cnr_"));
    assert_eq!(tok["token_type"], "Bearer");
    assert_eq!(tok["expires_in"], 3600);

    // Works on the MCP endpoint and the command API, bound to the approved workspace.
    assert_eq!(s.mcp_status(&access).await, 200);
    assert_eq!(s.command(&access, u.ws, "space.list").await.0, 200);

    // Refresh rotates both tokens; the old access token stops working.
    let (st, t2) = s
        .token(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", &refresh),
            ("client_id", &client),
        ])
        .await;
    assert_eq!(st, 200, "{t2}");
    let access2 = t2["access_token"].as_str().unwrap().to_string();
    let refresh2 = t2["refresh_token"].as_str().unwrap().to_string();
    assert_ne!(refresh2, refresh);
    assert_eq!(s.mcp_status(&access).await, 401);
    assert_eq!(s.mcp_status(&access2).await, 200);

    // Replaying the rotated-out refresh token is theft: the whole family dies.
    let (st, e) = s
        .token(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", &refresh),
            ("client_id", &client),
        ])
        .await;
    assert_eq!((st, e["error"].as_str()), (400, Some("invalid_grant")));
    assert_eq!(s.mcp_status(&access2).await, 401);
    let (st, _) = s
        .token(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", &refresh2),
            ("client_id", &client),
        ])
        .await;
    assert_eq!(st, 400);

    // RFC 7009 revocation: refresh token ends the grant; wrong client changes nothing.
    let (client, tok) = s.connect(&u).await;
    let (access, refresh) = (
        tok["access_token"].as_str().unwrap(),
        tok["refresh_token"].as_str().unwrap(),
    );
    let revoke = |token: String, client: String| {
        let s = &s;
        async move {
            s.http
                .post(s.url("/oauth/revoke"))
                .form(&[("token", token), ("client_id", client)])
                .send()
                .await
                .unwrap()
                .status()
        }
    };
    assert_eq!(revoke(refresh.into(), "cnc_someone_else".into()).await, 200);
    assert_eq!(s.mcp_status(access).await, 200);
    assert_eq!(revoke(refresh.into(), client).await, 200);
    assert_eq!(s.mcp_status(access).await, 401);
}

#[tokio::test]
async fn principal_is_an_agent_capped_below_owner() {
    let s = start().await;
    let u = s.signup().await; // the signing-up user is the workspace owner
    let (_, tok) = s.connect(&u).await;
    let access = tok["access_token"].as_str().unwrap();
    let pool = db().await;
    let p = cnote_core::auth::principal_from_oauth_token(&pool, access, &format!("{}/mcp", s.base))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(p.kind, cnote_core::PrincipalKind::Agent);
    assert_eq!(p.role, cnote_core::Role::Admin);
    assert_eq!(p.workspace_id, u.ws);
    // Agents cannot mint keys or manage connected apps.
    assert_eq!(s.command(access, u.ws, "apikey.create").await.0, 403);
    assert_eq!(s.command(access, u.ws, "oauth_app.list").await.0, 403);
}

#[tokio::test]
async fn token_is_bound_to_its_workspace() {
    let s = start().await;
    let a = s.signup().await;
    let b = s.signup().await;
    let (_, tok) = s.connect(&a).await;
    let access = tok["access_token"].as_str().unwrap();
    assert_eq!(s.command(access, b.ws, "space.list").await.0, 403);

    // A user cannot approve into a workspace they do not belong to.
    let client = s.register(REDIRECT).await;
    let (_, challenge) = pkce();
    let (st, _) = s
        .authorize(&a, s.authz_body(&client, REDIRECT, &challenge), b.ws, true)
        .await;
    assert_eq!(st, 403);
    // Authorization requires a session.
    let res = s
        .http
        .post(s.url("/api/oauth/authorize"))
        .json(&json!({"workspace_id": a.ws, "approve": true}))
        .send()
        .await
        .unwrap();
    assert_eq!(res.status(), 401);
}

#[tokio::test]
async fn pkce_is_mandatory_and_verified() {
    let s = start().await;
    let u = s.signup().await;
    let client = s.register(REDIRECT).await;
    let (verifier, challenge) = pkce();

    // plain, missing challenge: bounced back to the (verified) redirect with an error and the state.
    for (field, value) in [("code_challenge_method", "plain"), ("code_challenge", "")] {
        let mut body = s.authz_body(&client, REDIRECT, &challenge);
        body[field] = json!(value);
        let (_, out) = s.authorize(&u, body, u.ws, true).await;
        assert_eq!(out["ok"], false);
        assert_eq!(out["error"], "invalid_request");
        assert_eq!(query(&out["redirect_to"], "error"), "invalid_request");
        assert_eq!(query(&out["redirect_to"], "state"), "st-1");
        assert_eq!(query(&out["redirect_to"], "iss"), s.base);
    }

    let (_, out) = s
        .authorize(&u, s.authz_body(&client, REDIRECT, &challenge), u.ws, true)
        .await;
    assert_eq!(query(&out["redirect_to"], "state"), "st-1");
    let code = query(&out["redirect_to"], "code");
    let exchange = |verifier: String, code: String| {
        let (s, client) = (&s, client.clone());
        async move {
            s.token(&[
                ("grant_type", "authorization_code"),
                ("code", &code),
                ("redirect_uri", REDIRECT),
                ("client_id", &client),
                ("code_verifier", &verifier),
            ])
            .await
        }
    };
    // Wrong verifier and malformed verifier both fail (and do not mint tokens).
    let (_, wrong) = pkce();
    assert_eq!(
        exchange(wrong, code.clone()).await.1["error"],
        "invalid_grant"
    );
    assert_eq!(
        exchange("short".into(), code.clone()).await.1["error"],
        "invalid_request"
    );
    // The right verifier still works afterwards: a failed attempt did not burn the code.
    assert_eq!(exchange(verifier.clone(), code.clone()).await.0, 200);
    // Replay of a used code is refused and revokes what it produced.
    assert_eq!(exchange(verifier, code).await.1["error"], "invalid_grant");
}

#[tokio::test]
async fn code_replay_revokes_issued_tokens() {
    let s = start().await;
    let u = s.signup().await;
    let client = s.register(REDIRECT).await;
    let (verifier, challenge) = pkce();
    let (_, out) = s
        .authorize(&u, s.authz_body(&client, REDIRECT, &challenge), u.ws, true)
        .await;
    let code = query(&out["redirect_to"], "code");
    let form = [
        ("grant_type", "authorization_code"),
        ("code", code.as_str()),
        ("redirect_uri", REDIRECT),
        ("client_id", client.as_str()),
        ("code_verifier", verifier.as_str()),
    ];
    let (st, tok) = s.token(&form).await;
    assert_eq!(st, 200);
    let access = tok["access_token"].as_str().unwrap();
    assert_eq!(s.mcp_status(access).await, 200);
    assert_eq!(s.token(&form).await.0, 400);
    assert_eq!(s.mcp_status(access).await, 401);
}

#[tokio::test]
async fn resource_indicator_is_required_and_bound() {
    let s = start().await;
    let u = s.signup().await;
    let client = s.register(REDIRECT).await;
    let (verifier, challenge) = pkce();

    for resource in ["", "https://evil.example/mcp", "http://127.0.0.1:1/mcp"] {
        let mut body = s.authz_body(&client, REDIRECT, &challenge);
        body["resource"] = json!(resource);
        let (_, out) = s.authorize(&u, body, u.ws, true).await;
        assert_eq!(out["error"], "invalid_target", "{resource}");
    }

    let (_, out) = s
        .authorize(&u, s.authz_body(&client, REDIRECT, &challenge), u.ws, true)
        .await;
    let code = query(&out["redirect_to"], "code");
    let (st, e) = s
        .token(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("redirect_uri", REDIRECT),
            ("client_id", &client),
            ("code_verifier", &verifier),
            ("resource", "https://evil.example/mcp"),
        ])
        .await;
    assert_eq!((st, e["error"].as_str()), (400, Some("invalid_target")));
}

#[tokio::test]
async fn redirect_uri_must_match_exactly() {
    let s = start().await;
    let u = s.signup().await;
    let client = s.register(REDIRECT).await;
    let (verifier, challenge) = pkce();

    // Never redirect to an unregistered URI: the response carries no redirect_to at all.
    for bad in [
        "http://127.0.0.1:7777/callback/",
        "http://127.0.0.1:7778/callback",
        "https://evil.example/callback",
    ] {
        let res = s
            .http
            .post(s.url("/api/oauth/authorize/context"))
            .header("cookie", &u.cookie)
            .json(&s.authz_body(&client, bad, &challenge))
            .send()
            .await
            .unwrap();
        let out: Value = res.json().await.unwrap();
        assert_eq!(out["ok"], false, "{bad}");
        assert!(out["redirect_to"].is_null(), "{bad}");
    }

    // Registration rejects insecure or dangerous redirects.
    for bad in [
        "http://example.com/cb",
        "javascript:alert(1)",
        "https://x.example/cb#f",
    ] {
        let res = s
            .http
            .post(s.url("/oauth/register"))
            .json(&json!({"client_name": "x", "redirect_uris": [bad]}))
            .send()
            .await
            .unwrap();
        assert_eq!(res.status(), 400, "{bad}");
        assert_eq!(
            res.json::<Value>().await.unwrap()["error"],
            "invalid_redirect_uri"
        );
    }

    // The token step re-checks the URI the code was issued for.
    let (_, out) = s
        .authorize(&u, s.authz_body(&client, REDIRECT, &challenge), u.ws, true)
        .await;
    let code = query(&out["redirect_to"], "code");
    let (st, e) = s
        .token(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("redirect_uri", "http://127.0.0.1:7777/other"),
            ("client_id", &client),
            ("code_verifier", &verifier),
        ])
        .await;
    assert_eq!((st, e["error"].as_str()), (400, Some("invalid_grant")));
    // A different client cannot redeem it either.
    let other = s.register(REDIRECT).await;
    let (st, _) = s
        .token(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("redirect_uri", REDIRECT),
            ("client_id", &other),
            ("code_verifier", &verifier),
        ])
        .await;
    assert_eq!(st, 400);
}

#[tokio::test]
async fn consent_context_deny_and_cross_site() {
    let s = start().await;
    let u = s.signup().await;
    let client = s.register(REDIRECT).await;
    let (_, challenge) = pkce();
    let body = s.authz_body(&client, REDIRECT, &challenge);

    let ctx: Value = s
        .http
        .post(s.url("/api/oauth/authorize/context"))
        .header("cookie", &u.cookie)
        .json(&body)
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(ctx["ok"], true);
    assert_eq!(ctx["client_name"], "Test Agent");
    assert_eq!(ctx["redirect_host"], "127.0.0.1:7777");
    assert_eq!(ctx["workspaces"][0]["id"], u.ws.to_string());

    let (_, out) = s.authorize(&u, body.clone(), u.ws, false).await;
    assert_eq!(query(&out["redirect_to"], "error"), "access_denied");
    assert_eq!(query(&out["redirect_to"], "state"), "st-1");

    let mut b = body;
    b["workspace_id"] = json!(u.ws);
    b["approve"] = json!(true);
    let res = s
        .http
        .post(s.url("/api/oauth/authorize"))
        .header("cookie", &u.cookie)
        .header("sec-fetch-site", "cross-site")
        .json(&b)
        .send()
        .await
        .unwrap();
    assert_eq!(res.status(), 403);
}

#[tokio::test]
async fn connected_apps_list_and_revoke() {
    let s = start().await;
    let u = s.signup().await;
    let (_, tok) = s.connect(&u).await;
    let access = tok["access_token"].as_str().unwrap();

    let run = |name: &'static str, body: Value| {
        let (s, u) = (&s, &u);
        async move {
            s.http
                .post(s.url(&format!("/api/w/{}/commands/{name}", u.ws)))
                .header("cookie", &u.cookie)
                .json(&body)
                .send()
                .await
                .unwrap()
                .json::<Value>()
                .await
                .unwrap()
        }
    };
    let apps = run("oauth_app.list", json!({})).await;
    assert_eq!(apps.as_array().unwrap().len(), 1);
    assert_eq!(apps[0]["name"], "Test Agent");
    // Another user does not see it.
    let other = s.signup().await;
    let theirs: Value = s
        .http
        .post(s.url(&format!("/api/w/{}/commands/oauth_app.list", other.ws)))
        .header("cookie", &other.cookie)
        .json(&json!({}))
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert!(theirs.as_array().unwrap().is_empty());

    assert_eq!(s.mcp_status(access).await, 200);
    let out = run("oauth_app.revoke", json!({"id": apps[0]["id"]})).await;
    assert_eq!(out["revoked"], true);
    assert_eq!(s.mcp_status(access).await, 401);
    assert!(
        run("oauth_app.list", json!({}))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn client_id_metadata_document_flow() {
    let s = start().await;
    let u = s.signup().await;
    // A throwaway metadata host on loopback (allowed only because the test server sets OAUTH_CIMD_ALLOW_PRIVATE).
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let client_id = format!("http://{}/client.json", listener.local_addr().unwrap());
    let doc =
        json!({"client_id": client_id, "client_name": "Doc Client", "redirect_uris": [REDIRECT]});
    let app = axum::Router::new().route(
        "/client.json",
        axum::routing::get(move || {
            let doc = doc.clone();
            async move { axum::Json(doc) }
        }),
    );
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

    let (verifier, challenge) = pkce();
    let (_, out) = s
        .authorize(
            &u,
            s.authz_body(&client_id, REDIRECT, &challenge),
            u.ws,
            true,
        )
        .await;
    let code = query(&out["redirect_to"], "code");
    let (st, tok) = s
        .token(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("redirect_uri", REDIRECT),
            ("client_id", &client_id),
            ("code_verifier", &verifier),
        ])
        .await;
    assert_eq!(st, 200, "{tok}");
    assert_eq!(
        s.mcp_status(tok["access_token"].as_str().unwrap()).await,
        200
    );
}

#[tokio::test]
async fn registration_is_rate_limited() {
    let s = start().await;
    let mut limited = false;
    for _ in 0..40 {
        let res = s
            .http
            .post(s.url("/oauth/register"))
            .json(&json!({"redirect_uris": [REDIRECT]}))
            .send()
            .await
            .unwrap();
        if res.status() == 429 {
            limited = true;
            break;
        }
    }
    assert!(limited);
}

#[tokio::test]
async fn token_resource_is_checked_on_every_use() {
    let s = start().await;
    let u = s.signup().await;
    let (_, tok) = s.connect(&u).await;
    let access = tok["access_token"].as_str().unwrap();
    assert_eq!(s.mcp_status(access).await, 200);
    // The same protected resource also serves the command API.
    assert_eq!(s.command(access, u.ws, "space.list").await.0, 200);
    sqlx::query("update core.oauth_grants set resource = 'https://other.example/mcp' where workspace_id = $1")
        .bind(u.ws)
        .execute(&db().await)
        .await
        .unwrap();
    assert_eq!(s.mcp_status(access).await, 401);
    assert_eq!(s.command(access, u.ws, "space.list").await.0, 401);
}

#[tokio::test]
async fn removed_member_grants_stay_dead_after_re_adding() {
    let s = start().await;
    let u = s.signup().await;
    let (client, tok) = s.connect(&u).await;
    let access = tok["access_token"].as_str().unwrap();
    let refresh = tok["refresh_token"].as_str().unwrap();
    let pool = db().await;
    let user_id: Uuid =
        sqlx::query_scalar("select user_id from core.oauth_grants where workspace_id = $1")
            .bind(u.ws)
            .fetch_one(&pool)
            .await
            .unwrap();

    let mut tx = cnote_db::begin_tenant(&pool, u.ws).await.unwrap();
    sqlx::query("delete from core.memberships where user_id = $1")
        .bind(user_id)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    assert_eq!(s.mcp_status(access).await, 401);

    let mut tx = cnote_db::begin_tenant(&pool, u.ws).await.unwrap();
    sqlx::query(
        "insert into core.memberships (workspace_id, user_id, role) values ($1,$2,'member')",
    )
    .bind(u.ws)
    .bind(user_id)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    assert_eq!(
        s.mcp_status(access).await,
        401,
        "re-adding must not resurrect the grant"
    );
    let (st, e) = s
        .token(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", refresh),
            ("client_id", &client),
        ])
        .await;
    assert_eq!((st, e["error"].as_str()), (400, Some("invalid_grant")));
}

#[tokio::test]
async fn unconfigured_server_serves_oauth_only_on_loopback_hosts() {
    let s = start_with(false).await;
    // Loopback Host (local development): works, and the issuer follows the Host.
    let m: Value = s
        .http
        .get(s.url("/.well-known/oauth-authorization-server"))
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(m["issuer"], s.base);
    // Any other Host is refused rather than echoed into issuer/resource URLs.
    for path in [
        "/.well-known/oauth-authorization-server",
        "/.well-known/oauth-protected-resource",
    ] {
        let res = s
            .http
            .get(s.url(path))
            .header("host", "evil.example")
            .send()
            .await
            .unwrap();
        assert_eq!(res.status(), 503, "{path}");
    }
    let res = s
        .http
        .post(s.url("/oauth/register"))
        .header("host", "evil.example")
        .json(&json!({"redirect_uris": [REDIRECT]}))
        .send()
        .await
        .unwrap();
    assert_eq!(res.status(), 503);
    let res = s
        .http
        .post(s.url("/mcp"))
        .header("host", "evil.example")
        .json(&json!({}))
        .send()
        .await
        .unwrap();
    assert_eq!(res.status(), 401);
    assert!(
        !res.headers()["www-authenticate"]
            .to_str()
            .unwrap()
            .contains("evil.example")
    );
}

#[tokio::test]
async fn consent_context_is_throttled_per_user() {
    let s = start().await;
    let u = s.signup().await;
    let (_, challenge) = pkce();
    let body = s.authz_body("cnc_unknown", REDIRECT, &challenge);
    let mut limited = false;
    for _ in 0..40 {
        let res = s
            .http
            .post(s.url("/api/oauth/authorize/context"))
            .header("cookie", &u.cookie)
            .json(&body)
            .send()
            .await
            .unwrap();
        if res.status() == 429 {
            limited = true;
            break;
        }
    }
    assert!(limited);
}

#[tokio::test]
async fn junk_token_requests_do_not_charge_a_clients_own_bucket() {
    let s = start().await;
    let u = s.signup().await;
    let (client, _) = s.connect(&u).await;
    // 50 junk exchanges naming the client stay under the per-address limit and must not drain the client bucket.
    for _ in 0..50 {
        let (st, _) = s
            .token(&[
                ("grant_type", "authorization_code"),
                ("code", "cnx_junk"),
                ("redirect_uri", REDIRECT),
                ("client_id", &client),
                ("code_verifier", &"a".repeat(43)),
            ])
            .await;
        assert_eq!(st, 400);
    }
}
