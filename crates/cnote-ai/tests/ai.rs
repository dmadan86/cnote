//! Integration tests against Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use base64::Engine;
use cnote_core::auth;
use cnote_core::events::Event;
use cnote_core::{Ctx, ErrorCode, ExecOptions, Principal, Registry};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::{Arc, Mutex};
use uuid::Uuid;

async fn setup() -> (PgPool, Arc<Registry>) {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    // These tests talk to a mock model server on 127.0.0.1; production rejects private endpoints
    // (see netguard unit tests). Safe: every test in this binary sets the same value.
    unsafe { std::env::set_var("AI_ALLOW_PRIVATE_ENDPOINTS", "1") };
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
        .await
        .unwrap();
    let reg = Registry::new(vec![cnote_core::module(), cnote_ai::module()]);
    (pool, reg)
}

async fn run(
    pool: &PgPool,
    reg: &Arc<Registry>,
    p: &Principal,
    cmd: &str,
    input: Value,
) -> Result<Value, cnote_core::CmdError> {
    reg.execute(pool, cmd, input, p, &ExecOptions::default())
        .await
}

async fn ok(pool: &PgPool, reg: &Arc<Registry>, p: &Principal, cmd: &str, input: Value) -> Value {
    run(pool, reg, p, cmd, input)
        .await
        .unwrap_or_else(|e| panic!("{cmd}: {e}"))
}

async fn tenant(pool: &PgPool, reg: &Registry) -> (Uuid, Principal) {
    let user = auth::sign_up(
        pool,
        &format!("{}@test.local", Uuid::new_v4()),
        "Tess",
        &Uuid::new_v4().to_string(),
    )
    .await
    .unwrap();
    let ws = auth::create_workspace(pool, reg, user, "Acme")
        .await
        .unwrap();
    (user, auth::user_principal(pool, user, ws).await.unwrap())
}

/// Stand-in for the worker loop: runs queued event dispatches and jobs for one workspace until idle.
async fn drain(pool: &PgPool, reg: &Arc<Registry>, ws: Uuid) {
    loop {
        let mut tx = cnote_db::begin_tenant(pool, ws).await.unwrap();
        let jobs: Vec<(i64, String, Value)> = sqlx::query_as(
            "select id, kind, payload from core.jobs where status = 'queued' and workspace_id = $1 order by id",
        )
        .bind(ws)
        .fetch_all(&mut *tx)
        .await
        .unwrap();
        tx.commit().await.unwrap();
        if jobs.is_empty() {
            return;
        }
        for (id, kind, payload) in jobs {
            let tx = cnote_db::begin_tenant(pool, ws).await.unwrap();
            let mut ctx = Ctx {
                tx,
                principal: Principal::system(ws),
                registry: reg.clone(),
            };
            if kind == "event.dispatch" {
                let ev: Event = sqlx::query_as("select * from core.events where id = $1")
                    .bind(payload["event_id"].as_i64().unwrap())
                    .fetch_one(&mut *ctx.tx)
                    .await
                    .unwrap();
                for (prefix, h) in &reg.event_handlers {
                    if ev.kind.starts_with(prefix) {
                        h(&mut ctx, &ev).await.unwrap();
                    }
                }
            } else if let Some(h) = reg.job_handlers.get(kind.as_str()) {
                h(&mut ctx, payload).await.unwrap();
            }
            sqlx::query("update core.jobs set status = 'done' where id = $1")
                .bind(id)
                .execute(&mut *ctx.tx)
                .await
                .unwrap();
            ctx.tx.commit().await.unwrap();
        }
    }
}

async fn count(pool: &PgPool, ws: Uuid, sql: &str) -> i64 {
    let mut tx = cnote_db::begin_tenant(pool, ws).await.unwrap();
    let n = sqlx::query_scalar(sqlx::AssertSqlSafe(sql))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    n
}

fn titles(v: &Value) -> Vec<String> {
    v.as_array()
        .unwrap()
        .iter()
        .map(|h| h["title"].as_str().unwrap().to_string())
        .collect()
}

#[tokio::test]
async fn hybrid_search_indexing_permissions_and_metering() {
    let (pool, reg) = setup().await;
    let (_, owner) = tenant(&pool, &reg).await;
    let ws = owner.workspace_id;
    let (_, other_ws) = tenant(&pool, &reg).await;

    // A second member who is not in the private space.
    let email = format!("{}@test.local", Uuid::new_v4());
    let member_user = auth::sign_up(&pool, &email, "Mo", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    ok(
        &pool,
        &reg,
        &owner,
        "member.add",
        json!({"email": email, "role": "member"}),
    )
    .await;
    let member = auth::user_principal(&pool, member_user, ws).await.unwrap();

    let open = ok(
        &pool,
        &reg,
        &owner,
        "space.create",
        json!({"key": "ENG", "name": "Eng"}),
    )
    .await;
    let private = ok(
        &pool,
        &reg,
        &owner,
        "space.create",
        json!({"key": "HR", "name": "HR", "visibility": "private"}),
    )
    .await;
    let mk = |space: &Value, title: &str, body: &str| json!({"space_id": space["id"], "title": title, "content_md": body});
    let roadmap = ok(
        &pool,
        &reg,
        &owner,
        "note.create",
        mk(
            &open,
            "Roadmap",
            "kubernetes migration plan for the platform team",
        ),
    )
    .await;
    ok(
        &pool,
        &reg,
        &owner,
        "note.create",
        mk(
            &private,
            "Compensation",
            "kubernetes engineers salary bands confidential",
        ),
    )
    .await;
    let old = ok(
        &pool,
        &reg,
        &owner,
        "note.create",
        mk(&open, "Old runbook", "kubernetes legacy runbook"),
    )
    .await;
    ok(
        &pool,
        &reg,
        &owner,
        "note.create",
        mk(&open, "Lunch", "pizza on friday"),
    )
    .await;
    drain(&pool, &reg, ws).await;

    assert_eq!(
        count(
            &pool,
            ws,
            "select count(*) from ai.chunks where model = 'local-hash-1536'"
        )
        .await,
        4
    );

    // Re-index on update replaces the rows instead of piling up.
    ok(&pool, &reg, &owner, "note.update", json!({"id": roadmap["id"], "content_md": "kubernetes migration plan, phase two: observability"})).await;
    drain(&pool, &reg, ws).await;
    assert_eq!(count(&pool, ws, "select count(*) from ai.chunks").await, 4);
    assert_eq!(
        count(&pool, ws, &format!("select count(*) from ai.chunks where note_id = '{}' and content like '%phase two%'", roadmap["id"].as_str().unwrap())).await,
        1
    );

    // Archived pages are excluded.
    let mut tx = cnote_db::begin_tenant(&pool, ws).await.unwrap();
    sqlx::query("update core.notes set freshness = 'archived', archived_at = now() where id = $1")
        .bind(old["id"].as_str().unwrap().parse::<Uuid>().unwrap())
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();

    let as_owner = ok(
        &pool,
        &reg,
        &owner,
        "search.hybrid",
        json!({"q": "kubernetes migration"}),
    )
    .await;
    // Unrelated notes can trail in via the vector half (top-k, no similarity floor); archived ones never appear.
    assert_eq!(titles(&as_owner)[..2], ["Roadmap", "Compensation"]);
    assert!(!titles(&as_owner).contains(&"Old runbook".to_string()));
    assert!(as_owner[0]["score"].as_f64().unwrap() > as_owner[1]["score"].as_f64().unwrap());

    // The private space is filtered in SQL: the member never sees it, in either half of the fusion.
    let as_member = ok(
        &pool,
        &reg,
        &member,
        "search.hybrid",
        json!({"q": "kubernetes migration"}),
    )
    .await;
    assert_eq!(titles(&as_member)[0], "Roadmap");
    assert!(!titles(&as_member).contains(&"Compensation".to_string()));

    // Tenant isolation.
    let foreign = ok(
        &pool,
        &reg,
        &other_ws,
        "search.hybrid",
        json!({"q": "kubernetes"}),
    )
    .await;
    assert!(foreign.as_array().unwrap().is_empty());
    assert_eq!(
        run(&pool, &reg, &owner, "search.hybrid", json!({"q": "  "}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::Invalid
    );

    // Summarize / extract_tasks / draft: audited and metered (platform provider), and the member cannot read private notes.
    let meeting = ok(
        &pool,
        &reg,
        &owner,
        "note.create",
        mk(
            &open,
            "Standup",
            "Decisions made.\n- [ ] Email Ravi the quote\n- [ ] Book the offsite venue",
        ),
    )
    .await;
    let before = count(&pool, ws, "select count(*) from core.notes").await;
    let tasks = ok(
        &pool,
        &reg,
        &owner,
        "ai.extract_tasks",
        json!({"note_id": meeting["id"]}),
    )
    .await;
    let found: Vec<&str> = tasks["suggested_tasks"]
        .as_array()
        .unwrap()
        .iter()
        .map(|t| t["title"].as_str().unwrap())
        .collect();
    assert_eq!(found, ["Email Ravi the quote", "Book the offsite venue"]);
    assert_eq!(
        count(&pool, ws, "select count(*) from core.notes").await,
        before,
        "suggestions are not created"
    );
    let s = ok(
        &pool,
        &reg,
        &owner,
        "ai.summarize",
        json!({"note_id": roadmap["id"]}),
    )
    .await;
    assert_eq!(s["provider"], "local");
    ok(
        &pool,
        &reg,
        &owner,
        "ai.draft",
        json!({"prompt": "write a status update", "note_id": roadmap["id"]}),
    )
    .await;
    assert!(count(&pool, ws, "select count(*) from ai.audit where purpose in ('summarize','extract_tasks','draft','search','index')").await >= 8);
    assert!(
        count(
            &pool,
            ws,
            "select coalesce(sum(quantity),0)::bigint from core.usage where kind = 'ai.tokens_in'"
        )
        .await
            > 0
    );
    assert!(
        count(
            &pool,
            ws,
            "select count(*) from core.usage where kind = 'ai.tokens_out'"
        )
        .await
            > 0
    );
    let hidden = ok(
        &pool,
        &reg,
        &owner,
        "note.list",
        json!({"space_id": private["id"]}),
    )
    .await;
    let private_note = hidden[0]["id"].clone();
    assert_eq!(
        run(
            &pool,
            &reg,
            &member,
            "ai.summarize",
            json!({"note_id": private_note})
        )
        .await
        .unwrap_err()
        .code,
        ErrorCode::NotFound
    );
}

#[derive(Clone, Default)]
struct Seen {
    auth: Arc<Mutex<Option<String>>>,
    body: Arc<Mutex<Option<Value>>>,
}

async fn mock_openai(seen: Seen) -> String {
    use axum::{Json, Router, http::HeaderMap, routing::post};
    let app = Router::new().route(
        "/v1/chat/completions",
        post(move |headers: HeaderMap, Json(body): Json<Value>| {
            let seen = seen.clone();
            async move {
                *seen.auth.lock().unwrap() = headers
                    .get("authorization")
                    .and_then(|v| v.to_str().ok())
                    .map(String::from);
                *seen.body.lock().unwrap() = Some(body);
                Json(json!({
                    "model": "mock-1",
                    "choices": [{"message": {"role": "assistant", "content": "A tidy summary."}}],
                    "usage": {"prompt_tokens": 12, "completion_tokens": 4}
                }))
            }
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    format!("http://{addr}/v1")
}

#[tokio::test]
async fn byo_provider_encrypts_key_never_meters_and_redacts() {
    let (pool, reg) = setup().await;
    let (_, owner) = tenant(&pool, &reg).await;
    let ws = owner.workspace_id;
    let key = format!("sk-{}", Uuid::new_v4());
    let set = json!({"provider": "openai", "base_url": "http://127.0.0.1:9/v1", "model": "mock-1", "api_key": key});

    // Only this test touches CNOTE_MASTER_KEY.
    unsafe { std::env::remove_var("CNOTE_MASTER_KEY") };
    let e = run(&pool, &reg, &owner, "ai.set_provider", set.clone())
        .await
        .unwrap_err();
    assert_eq!(
        e.code,
        ErrorCode::Invalid,
        "refuses to store a key without a master key"
    );
    assert_eq!(
        count(&pool, ws, "select count(*) from ai.provider_config").await,
        0
    );

    let master = base64::engine::general_purpose::STANDARD.encode(rand::random::<[u8; 32]>());
    unsafe { std::env::set_var("CNOTE_MASTER_KEY", &master) };

    // Members cannot configure it.
    let email = format!("{}@test.local", Uuid::new_v4());
    let mu = auth::sign_up(&pool, &email, "Mo", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    ok(
        &pool,
        &reg,
        &owner,
        "member.add",
        json!({"email": email, "role": "member"}),
    )
    .await;
    let member = auth::user_principal(&pool, mu, ws).await.unwrap();
    assert_eq!(
        run(&pool, &reg, &member, "ai.set_provider", set.clone())
            .await
            .unwrap_err()
            .code,
        ErrorCode::Forbidden
    );

    let seen = Seen::default();
    let base = mock_openai(seen.clone()).await;
    let set = json!({"provider": "openai", "base_url": base, "model": "mock-1", "api_key": key});
    let got = ok(&pool, &reg, &owner, "ai.set_provider", set).await;
    assert_eq!(got["has_key"], true);
    let shown = ok(&pool, &reg, &owner, "ai.get_provider", json!({})).await;
    assert!(
        !shown.to_string().contains(&key),
        "the key is never returned"
    );
    assert_eq!(shown["metered"], false);
    let stored: Vec<u8> = {
        let mut tx = cnote_db::begin_tenant(&pool, ws).await.unwrap();
        sqlx::query_scalar("select key_enc from ai.provider_config")
            .fetch_one(&mut *tx)
            .await
            .unwrap()
    };
    assert!(
        !stored.windows(key.len()).any(|w| w == key.as_bytes()),
        "stored encrypted"
    );
    assert_eq!(
        count(&pool, ws, "select count(*) from core.events where kind = 'ai.provider_changed' and payload::text like '%sk-%'").await,
        0,
        "events never carry the key"
    );

    // Redaction on, then a call: the decrypted key reaches the endpoint, PII does not, and nothing is metered.
    ok(
        &pool,
        &reg,
        &owner,
        "workspace.update",
        json!({"settings": {"ai_redact_pii": true}}),
    )
    .await;
    let space = ok(
        &pool,
        &reg,
        &owner,
        "space.create",
        json!({"key": "OPS", "name": "Ops"}),
    )
    .await;
    let note = ok(&pool, &reg, &owner, "note.create", json!({
        "space_id": space["id"], "title": "Vendor",
        "content_md": "Reach asha@vendor.example or 9876543210; GSTIN 29ABCDE1234F1Z5, PAN ABCDE1234F."
    })).await;
    let out = ok(
        &pool,
        &reg,
        &owner,
        "ai.summarize",
        json!({"note_id": note["id"]}),
    )
    .await;
    assert_eq!(out["text"], "A tidy summary.");
    assert_eq!(
        seen.auth.lock().unwrap().as_deref(),
        Some(format!("Bearer {key}").as_str())
    );
    let sent = seen.body.lock().unwrap().clone().unwrap().to_string();
    for pii in [
        "asha@vendor.example",
        "9876543210",
        "29ABCDE1234F1Z5",
        "ABCDE1234F",
    ] {
        assert!(
            !sent.contains(pii),
            "{pii} must be redacted before the external call"
        );
    }
    assert!(sent.contains("[EMAIL]") && sent.contains("[GSTIN]"));
    assert_eq!(
        count(
            &pool,
            ws,
            "select count(*) from core.usage where kind like 'ai.tokens%'"
        )
        .await,
        0,
        "BYO is not metered"
    );
    let audited: i64 = count(&pool, ws, "select count(*) from ai.audit where provider = 'openai' and model = 'mock-1' and tokens_in = 12 and tokens_out = 4 and prompt::text not like '%asha@%'").await;
    assert_eq!(audited, 1, "audit stores the redacted prompt");

    // Changing the endpoint without a new key drops the stored key instead of forwarding it.
    let moved = ok(
        &pool,
        &reg,
        &owner,
        "ai.set_provider",
        json!({"provider": "openai", "base_url": "http://127.0.0.1:9/v1"}),
    )
    .await;
    assert_eq!(moved["has_key"], false);
    let cleared = ok(&pool, &reg, &owner, "ai.clear_provider", json!({})).await;
    assert_eq!(cleared["cleared"], true);
    assert_eq!(
        ok(&pool, &reg, &owner, "ai.get_provider", json!({})).await["configured"],
        false
    );
}
