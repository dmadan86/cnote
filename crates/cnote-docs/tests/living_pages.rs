//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use chrono::{Duration, Utc};
use cnote_core::ai::{Completion, CompletionRequest, LocalProvider, ModelProvider};
use cnote_core::{Ctx, ErrorCode, ExecOptions, Principal, Registry, Role, auth};
use futures::future::BoxFuture;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

/// Returns the page with a marker appended, so approving a proposal visibly changes content.
struct Reviser;
impl ModelProvider for Reviser {
    fn name(&self) -> &str {
        "test"
    }
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>> {
        Box::pin(async move {
            let last = req
                .messages
                .last()
                .map(|m| m.content.clone())
                .unwrap_or_default();
            Ok(Completion {
                text: format!("{last}\nREVISED"),
                provider: "test".into(),
                model: "reviser".into(),
                tokens_in: 0,
                tokens_out: 0,
            })
        })
    }
    fn embedding_model(&self) -> &str {
        LocalProvider.embedding_model()
    }
    fn embed(&self, t: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>> {
        Box::pin(async move { Ok(t.iter().map(|x| cnote_core::ai::hash_embed(x)).collect()) })
    }
}

async fn setup() -> (PgPool, Arc<Registry>) {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
        .await
        .unwrap();
    let reg = Registry::with_ai(
        vec![cnote_core::module(), cnote_docs::module()],
        Arc::new(Reviser),
    );
    (pool, reg)
}

async fn user(pool: &PgPool) -> Uuid {
    auth::sign_up(
        pool,
        &format!("{}@test.local", Uuid::new_v4()),
        "Tess",
        &Uuid::new_v4().to_string(),
    )
    .await
    .unwrap()
}

/// A workspace with an owner.
async fn tenant(pool: &PgPool, reg: &Registry) -> (Uuid, Principal) {
    let u = user(pool).await;
    let ws = auth::create_workspace(pool, reg, u, "Acme").await.unwrap();
    (ws, auth::user_principal(pool, u, ws).await.unwrap())
}

async fn add_member(pool: &PgPool, ws: Uuid) -> Principal {
    let u = user(pool).await;
    let mut tx = cnote_db::begin_tenant(pool, ws).await.unwrap();
    sqlx::query(
        "insert into core.memberships (workspace_id, user_id, role) values ($1,$2,'member')",
    )
    .bind(ws)
    .bind(u)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    Principal::user(u, ws, Role::Member)
}

struct T {
    pool: PgPool,
    reg: Arc<Registry>,
}

impl T {
    async fn run(
        &self,
        p: &Principal,
        cmd: &str,
        input: Value,
    ) -> Result<Value, cnote_core::CmdError> {
        self.reg
            .execute(&self.pool, cmd, input, p, &ExecOptions::default())
            .await
    }
    async fn ok(&self, p: &Principal, cmd: &str, input: Value) -> Value {
        self.run(p, cmd, input)
            .await
            .unwrap_or_else(|e| panic!("{cmd}: {e}"))
    }
    /// Run queued event/job work for one workspace to completion, as the worker would.
    async fn drain(&self, ws: Uuid) {
        loop {
            let mut tx = cnote_db::begin_tenant(&self.pool, ws).await.unwrap();
            let job: Option<(i64, String, Value)> = sqlx::query_as(
                "select id, kind, payload from core.jobs where status = 'queued' order by id limit 1 for update skip locked",
            )
            .fetch_optional(&mut *tx)
            .await
            .unwrap();
            let Some((id, kind, payload)) = job else {
                return;
            };
            sqlx::query("update core.jobs set status = 'done' where id = $1")
                .bind(id)
                .execute(&mut *tx)
                .await
                .unwrap();
            let mut ctx = Ctx {
                tx,
                principal: Principal::system(ws),
                registry: self.reg.clone(),
            };
            if kind == "event.dispatch" {
                let ev: cnote_core::events::Event =
                    sqlx::query_as("select * from core.events where id = $1")
                        .bind(payload["event_id"].as_i64().unwrap())
                        .fetch_one(&mut *ctx.tx)
                        .await
                        .unwrap();
                // Same attribution as the real worker (crates/cnote-server/src/worker.rs).
                ctx.principal.user_id = ev.on_behalf_of.or_else(|| {
                    ev.actor_id
                        .as_deref()
                        .and_then(|a| a.parse().ok())
                        .filter(|_| ev.actor_type == "user")
                });
                for (prefix, h) in self.reg.event_handlers.iter() {
                    if ev.kind.starts_with(prefix) {
                        h(&mut ctx, &ev).await.unwrap();
                    }
                }
            } else if let Some(h) = self.reg.job_handlers.get(kind.as_str()) {
                h(&mut ctx, payload).await.unwrap();
            }
            ctx.tx.commit().await.unwrap();
        }
    }
    async fn sql(&self, ws: Uuid, q: &'static str, id: Uuid) {
        let mut tx = cnote_db::begin_tenant(&self.pool, ws).await.unwrap();
        sqlx::query(q).bind(id).execute(&mut *tx).await.unwrap();
        tx.commit().await.unwrap();
    }
    async fn space(&self, p: &Principal, key: &str, visibility: &str) -> Uuid {
        let s = self
            .ok(
                p,
                "space.create",
                json!({"key": key, "name": key, "visibility": visibility}),
            )
            .await;
        s["id"].as_str().unwrap().parse().unwrap()
    }
    async fn page(&self, p: &Principal, space: Uuid, title: &str, md: &str) -> Uuid {
        let n = self
            .ok(
                p,
                "note.create",
                json!({"space_id": space, "title": title, "content_md": md}),
            )
            .await;
        n["id"].as_str().unwrap().parse().unwrap()
    }
    /// A typed note with a project item key, without depending on the projects crate.
    async fn item(&self, p: &Principal, space: Uuid, key: &str) -> Uuid {
        let n = self
            .ok(
                p,
                "note.create",
                json!({"space_id": space, "title": "Ship it", "types": ["task"]}),
            )
            .await;
        let id: Uuid = n["id"].as_str().unwrap().parse().unwrap();
        let mut tx = cnote_db::begin_tenant(&self.pool, p.workspace_id)
            .await
            .unwrap();
        sqlx::query("insert into projects.items (note_id, workspace_id, space_id, number, key, status) values ($1,$2,$3,12,$4,'todo')")
            .bind(id)
            .bind(p.workspace_id)
            .bind(space)
            .bind(key)
            .execute(&mut *tx)
            .await
            .unwrap();
        tx.commit().await.unwrap();
        id
    }
    async fn lapses(&self, ws: Uuid, id: Uuid) -> i32 {
        let mut tx = cnote_db::begin_tenant(&self.pool, ws).await.unwrap();
        sqlx::query_scalar("select lapse_count from core.notes where id = $1")
            .bind(id)
            .fetch_one(&mut *tx)
            .await
            .unwrap()
    }
    async fn freshness(&self, p: &Principal, id: Uuid) -> String {
        self.ok(p, "note.get", json!({"id": id})).await["freshness"]
            .as_str()
            .unwrap()
            .to_string()
    }
}

async fn t() -> (T, Uuid, Principal) {
    let (pool, reg) = setup().await;
    let (ws, owner) = tenant(&pool, &reg).await;
    (T { pool, reg }, ws, owner)
}

#[tokio::test]
async fn binding_change_flags_page_and_proposal_flows() {
    let (t, ws, owner) = t().await;
    let space = t.space(&owner, "ENG", "open").await;
    let item = t.item(&owner, space, "ENG-12").await;
    let page = t
        .page(
            &owner,
            space,
            "Deploy guide",
            "Releases are tracked in ENG-12 and repo:acme/api/deploy.md",
        )
        .await;
    t.drain(ws).await;

    let binds: i64 = {
        let mut tx = cnote_db::begin_tenant(&t.pool, ws).await.unwrap();
        sqlx::query_scalar("select count(*) from core.relations where from_id = $1 and to_id = $2 and kind = 'binds' and external_ref = 'ENG-12'")
            .bind(page).bind(item).fetch_one(&mut *tx).await.unwrap()
    };
    assert_eq!(binds, 1);

    // An event on the bound item.
    let mut tx = cnote_db::begin_tenant(&t.pool, ws).await.unwrap();
    cnote_core::events::emit(
        &mut tx,
        &owner,
        "item.transitioned",
        Some(item),
        json!({"to": "done"}),
    )
    .await
    .unwrap();
    tx.commit().await.unwrap();
    t.drain(ws).await;

    assert_eq!(t.freshness(&owner, page).await, "stale");
    let flags = t.ok(&owner, "doc.flags", json!({})).await;
    assert_eq!(flags.as_array().unwrap().len(), 1);
    assert_eq!(flags[0]["reasons"][0], "binding_changed");
    let props = t.ok(&owner, "doc.proposals", json!({})).await;
    assert_eq!(props.as_array().unwrap().len(), 1);
    assert!(props[0]["rationale"].as_str().unwrap().contains("Ship it"));
    // Nothing was rewritten silently.
    let n = t.ok(&owner, "note.get", json!({"id": page})).await;
    assert!(!n["content_md"].as_str().unwrap().contains("REVISED"));

    // A second event on the same item does not pile up another proposal.
    let mut tx = cnote_db::begin_tenant(&t.pool, ws).await.unwrap();
    cnote_core::events::emit(&mut tx, &owner, "item.updated", Some(item), json!({}))
        .await
        .unwrap();
    tx.commit().await.unwrap();
    t.drain(ws).await;
    assert_eq!(
        t.ok(&owner, "doc.proposals", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );

    let pid = props[0]["id"].clone();
    t.ok(
        &owner,
        "doc.proposal_decide",
        json!({"proposal_id": pid, "approve": true}),
    )
    .await;
    t.drain(ws).await;
    let n = t.ok(&owner, "note.get", json!({"id": page})).await;
    assert!(n["content_md"].as_str().unwrap().ends_with("REVISED"));
    assert_eq!(t.freshness(&owner, page).await, "fresh");
    assert_eq!(
        t.ok(&owner, "doc.flags", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );
    assert_eq!(
        t.ok(&owner, "doc.proposals", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );
    let hist = t.ok(&owner, "note.history", json!({"id": page})).await;
    assert!(hist.to_string().contains("doc.proposal_decided"));
    // Approving twice is rejected.
    let e = t
        .run(
            &owner,
            "doc.proposal_decide",
            json!({"proposal_id": pid, "approve": true}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
}

#[tokio::test]
async fn reject_keeps_page_and_verify_resets() {
    let (t, ws, owner) = t().await;
    let space = t.space(&owner, "ENG", "open").await;
    let other = t.page(&owner, space, "Pricing", "v1").await;
    let page = t
        .page(&owner, space, "Handbook", "See [[Pricing]] for rates")
        .await;
    t.drain(ws).await;
    t.ok(
        &owner,
        "note.update",
        json!({"id": other, "content_md": "v2"}),
    )
    .await;
    t.drain(ws).await;
    assert_eq!(t.freshness(&owner, page).await, "stale");
    let props = t
        .ok(&owner, "doc.proposals", json!({"note_id": page}))
        .await;
    t.ok(
        &owner,
        "doc.proposal_decide",
        json!({"proposal_id": props[0]["id"], "approve": false}),
    )
    .await;
    assert_eq!(t.freshness(&owner, page).await, "stale");

    let member = add_member(&t.pool, ws).await;
    let e = t
        .run(&member, "doc.verify", json!({"note_id": page}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);

    t.sql(
        ws,
        "update core.notes set lapse_count = 1 where id = $1",
        page,
    )
    .await;
    let v = t.ok(&owner, "doc.verify", json!({"note_id": page})).await;
    assert_eq!(v["freshness"], "fresh");
    assert_eq!(t.freshness(&owner, page).await, "fresh");
    let n = t.ok(&owner, "note.get", json!({"id": page})).await;
    assert_eq!(t.lapses(ws, page).await, 0);
    assert!(n["verified_at"].is_string());
    assert_eq!(
        t.ok(&owner, "doc.flags", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );

    // Ownership transfer: the new owner can verify, the old (non-admin) one cannot.
    t.ok(
        &owner,
        "doc.set_owner",
        json!({"note_id": page, "owner_id": member.user_id}),
    )
    .await;
    t.ok(&member, "doc.verify", json!({"note_id": page})).await;
    let stranger = Uuid::new_v4();
    let e = t
        .run(
            &owner,
            "doc.set_owner",
            json!({"note_id": page, "owner_id": stranger}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn two_lapses_archive_and_hide_from_search() {
    let (t, ws, owner) = t().await;
    let space = t.space(&owner, "DOC", "open").await;
    let page = t
        .page(&owner, space, "Onboarding", "zebra onboarding checklist")
        .await;
    let sweep = |days: i64| {
        let (pool, reg) = (t.pool.clone(), t.reg.clone());
        async move {
            let mut ctx = Ctx {
                tx: cnote_db::begin_tenant(&pool, ws).await.unwrap(),
                principal: Principal::system(ws),
                registry: reg,
            };
            cnote_docs::freshness::sweep_workspace(
                &mut ctx,
                (Utc::now() + Duration::days(days)).date_naive(),
            )
            .await
            .unwrap();
            ctx.tx.commit().await.unwrap();
        }
    };
    sweep(1).await; // not yet due
    assert_eq!(t.freshness(&owner, page).await, "fresh");

    sweep(91).await;
    assert_eq!(t.freshness(&owner, page).await, "stale");
    assert_eq!(t.lapses(ws, page).await, 1);
    let hist = t.ok(&owner, "note.history", json!({"id": page})).await;
    assert!(hist.to_string().contains("doc.lapsed"));
    assert_eq!(
        t.ok(&owner, "doc.flags", json!({})).await[0]["reasons"][0],
        "date_lapsed"
    );
    let hits = t.ok(&owner, "search.notes", json!({"q": "zebra"})).await;
    assert_eq!(hits.as_array().unwrap().len(), 1);

    sweep(91).await; // same day again: the grace window has not elapsed
    assert_eq!(t.freshness(&owner, page).await, "stale");
    sweep(91 + 15).await;
    assert_eq!(t.freshness(&owner, page).await, "archived");
    let hits = t.ok(&owner, "search.notes", json!({"q": "zebra"})).await;
    assert_eq!(hits.as_array().unwrap().len(), 0);
    assert!(
        t.ok(&owner, "note.history", json!({"id": page}))
            .await
            .to_string()
            .contains("doc.archived")
    );

    t.ok(&owner, "doc.restore", json!({"note_id": page})).await;
    assert_eq!(t.freshness(&owner, page).await, "stale");
    assert_eq!(
        t.ok(&owner, "search.notes", json!({"q": "zebra"}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        t.run(&owner, "doc.restore", json!({"note_id": page}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::Conflict
    );
}

#[tokio::test]
async fn unclaimed_imports_are_archived_after_90_days() {
    let (t, ws, owner) = t().await;
    let space = t.space(&owner, "DOC", "open").await;
    let old = t.page(&owner, space, "Old import", "x").await;
    let fresh = t.page(&owner, space, "New import", "y").await;
    t.sql(ws, "update core.notes set freshness = 'unverified', created_at = now() - interval '91 days' where id = $1", old).await;
    t.sql(
        ws,
        "update core.notes set freshness = 'unverified' where id = $1",
        fresh,
    )
    .await;
    // The registered sweep walks every workspace.
    let (_, _, sweep) = cnote_docs::module().sweeps.remove(0);
    sweep(t.pool.clone(), t.reg.clone()).await.unwrap();
    assert_eq!(t.freshness(&owner, old).await, "archived");
    assert_eq!(t.freshness(&owner, fresh).await, "unverified");
    // Verifying claims an unverified page.
    t.ok(&owner, "doc.verify", json!({"note_id": fresh})).await;
    assert_eq!(t.freshness(&owner, fresh).await, "fresh");
}

#[tokio::test]
async fn ask_cites_pages_or_routes_a_capture_request() {
    let (t, ws, owner) = t().await;
    let member = add_member(&t.pool, ws).await;
    let space = t.space(&owner, "ENG", "open").await;
    let runbook = t
        .page(
            &member,
            space,
            "Pod runbook",
            "Restart kubernetes pods with the rollout command",
        )
        .await;

    let a = t
        .ok(
            &owner,
            "doc.ask",
            json!({"question": "How do I restart kubernetes pods?"}),
        )
        .await;
    assert_eq!(a["answered"], true);
    let c = &a["citations"][0];
    assert_eq!(c["title"], "Pod runbook");
    assert_eq!(c["note_id"], json!(runbook));
    assert_eq!(c["freshness"], "fresh");
    assert!(c.get("verified_at").is_some());

    // Nothing answers it, but a page is close: routed to that page's owner.
    let a = t
        .ok(
            &owner,
            "doc.ask",
            json!({"question": "What is the kubernetes zebra policy?"}),
        )
        .await;
    assert_eq!(a["answered"], false);
    assert_eq!(a["routed_to"], json!(member.user_id));
    let req = a["capture_request_id"].clone();
    // No relation at all: falls back to a workspace admin (the owner here).
    let b = t
        .ok(
            &member,
            "doc.ask",
            json!({"question": "quantum flux capacitor"}),
        )
        .await;
    assert_eq!(b["routed_to"], json!(owner.user_id));

    let mine = t.ok(&member, "capture.list", json!({})).await;
    assert_eq!(mine.as_array().unwrap().len(), 2); // routed one + the one they asked
    let answer = t
        .page(&member, space, "Zebra policy", "Zebras are fine")
        .await;
    t.ok(
        &member,
        "capture.answer",
        json!({"request_id": req, "note_id": answer}),
    )
    .await;
    let open = t.ok(&owner, "capture.list", json!({})).await;
    assert_eq!(open.as_array().unwrap().len(), 1);
    assert_eq!(
        t.run(
            &member,
            "capture.answer",
            json!({"request_id": req, "note_id": answer})
        )
        .await
        .unwrap_err()
        .code,
        ErrorCode::NotFound
    );
}

#[tokio::test]
async fn private_space_pages_never_leak_through_ask() {
    let (t, ws, owner) = t().await;
    let member = add_member(&t.pool, ws).await;
    let private = t.space(&owner, "SEC", "private").await;
    t.page(
        &owner,
        private,
        "Breach playbook",
        "Rotate the vault credentials immediately",
    )
    .await;

    let seen = t
        .ok(
            &owner,
            "doc.ask",
            json!({"question": "rotate vault credentials"}),
        )
        .await;
    assert_eq!(seen["answered"], true);

    let hidden = t
        .ok(
            &member,
            "doc.ask",
            json!({"question": "rotate vault credentials"}),
        )
        .await;
    assert_eq!(hidden["answered"], false);
    assert!(hidden["citations"].as_array().unwrap().is_empty());
    assert!(hidden["related"].as_array().unwrap().is_empty());
    assert!(!hidden.to_string().contains("Breach"));
    // The member sees neither the page's proposals nor its score.
    let score = t.ok(&member, "doc.freshness_score", json!({})).await;
    assert_eq!(score["total"], 0);
    assert_eq!(
        t.ok(&owner, "doc.freshness_score", json!({})).await["total"],
        1
    );
}

#[tokio::test]
async fn tenants_are_isolated_and_score_is_per_space() {
    let (t, ws_a, a) = t().await;
    let (_, b) = tenant(&t.pool, &t.reg).await;
    let s1 = t.space(&a, "AAA", "open").await;
    let s2 = t.space(&a, "BBB", "open").await;
    let p1 = t.page(&a, s1, "Alpha secret", "alpha unique-term").await;
    t.page(&a, s1, "Beta", "b").await;
    t.page(&a, s2, "Gamma", "c").await;
    t.sql(
        ws_a,
        "update core.notes set freshness = 'stale' where id = $1",
        p1,
    )
    .await;

    let score = t.ok(&a, "doc.freshness_score", json!({})).await;
    assert_eq!(
        (score["total"].as_i64(), score["verified"].as_i64()),
        (Some(3), Some(2))
    );
    let one = t
        .ok(&a, "doc.freshness_score", json!({"space_id": s1}))
        .await;
    assert_eq!(
        (one["total"].as_i64(), one["verified"].as_i64()),
        (Some(2), Some(1))
    );
    assert_eq!(one["spaces"].as_array().unwrap().len(), 1);

    assert_eq!(
        t.run(&b, "doc.verify", json!({"note_id": p1}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::NotFound
    );
    let ask = t
        .ok(&b, "doc.ask", json!({"question": "alpha unique-term"}))
        .await;
    assert_eq!(ask["answered"], false);
    assert_eq!(t.ok(&b, "doc.freshness_score", json!({})).await["total"], 0);
    assert_eq!(
        t.ok(&b, "doc.proposals", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        0
    );
}

#[tokio::test]
async fn capture_drafts_a_page_owned_by_the_caller() {
    let (t, ws, owner) = t().await;
    let member = add_member(&t.pool, ws).await;
    let space = t.space(&owner, "DOC", "open").await;
    let n = t
        .ok(
            &member,
            "doc.capture",
            json!({"source": "chat", "text": "We decided to ship on Friday", "space_id": space}),
        )
        .await;
    assert_eq!(n["owner_id"], json!(member.user_id));
    assert_eq!(n["title"], "We decided to ship on Friday");
    assert!(
        n["content_md"].as_str().unwrap().contains("REVISED")
            || !n["content_md"].as_str().unwrap().is_empty()
    );
    let e = t
        .run(
            &member,
            "doc.capture",
            json!({"source": "email", "text": "x", "space_id": space}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn pages_cannot_bind_to_notes_their_author_cannot_read() {
    let (t, ws, owner) = t().await;
    let member = add_member(&t.pool, ws).await;
    let private = t.space(&owner, "HID", "private").await;
    let secret = t.item(&owner, private, "HID-1").await;
    let open = t.space(&owner, "OPN", "open").await;
    let page = t
        .page(&member, open, "Notes", "Tracking HID-1 closely")
        .await;
    t.drain(ws).await;
    let mut tx = cnote_db::begin_tenant(&t.pool, ws).await.unwrap();
    let bound: i64 = sqlx::query_scalar(
        "select count(*) from core.relations where from_id = $1 and to_id = $2 and kind = 'binds'",
    )
    .bind(page)
    .bind(secret)
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert_eq!(
        bound, 0,
        "member must not bind to an item in a private space"
    );
}
