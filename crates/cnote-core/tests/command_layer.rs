//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use cnote_core::auth::{self, create_api_key};
use cnote_core::{ExecOptions, Principal, Registry};
use serde_json::json;
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

async fn setup() -> (PgPool, Arc<Registry>) {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
        .await
        .unwrap();
    (pool, Registry::new(vec![cnote_core::module()]))
}

async fn tenant(pool: &PgPool, reg: &Registry) -> (Uuid, Principal) {
    let email = format!("{}@test.local", Uuid::new_v4());
    let user = auth::sign_up(pool, &email, "Tess", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let ws = auth::create_workspace(pool, reg, user, "Acme")
        .await
        .unwrap();
    (user, auth::user_principal(pool, user, ws).await.unwrap())
}

#[tokio::test]
async fn notes_are_isolated_between_tenants() {
    let (pool, reg) = setup().await;
    let (_, a) = tenant(&pool, &reg).await;
    let (_, b) = tenant(&pool, &reg).await;
    let opts = ExecOptions::default();
    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "ENG", "name": "Engineering"}),
            &a,
            &opts,
        )
        .await
        .unwrap();
    let note = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "Runbook", "content_md": "restart the pods"}),
            &a,
            &opts,
        )
        .await
        .unwrap();
    assert_eq!(
        note["owner_id"],
        json!(a.user_id),
        "owner defaults to creator"
    );
    assert!(note["verify_by"].is_string(), "pages get a verify-by date");

    // Tenant B cannot read A's note even with its id: RLS hides it.
    let err = reg
        .execute(&pool, "note.get", json!({"id": note["id"]}), &b, &opts)
        .await
        .unwrap_err();
    assert_eq!(err.code, cnote_core::ErrorCode::NotFound);
    let hits = reg
        .execute(&pool, "search.notes", json!({"q": "runbook"}), &b, &opts)
        .await
        .unwrap();
    assert_eq!(hits.as_array().unwrap().len(), 0);
    let hits = reg
        .execute(&pool, "search.notes", json!({"q": "runbook"}), &a, &opts)
        .await
        .unwrap();
    assert_eq!(hits.as_array().unwrap().len(), 1);

    // Raw SQL without a tenant sees nothing.
    let n: i64 = sqlx::query_scalar("select count(*) from core.notes")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(n, 0);
}

#[tokio::test]
async fn optimistic_concurrency_and_history() {
    let (pool, reg) = setup().await;
    let (_, a) = tenant(&pool, &reg).await;
    let o = ExecOptions::default();
    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "DOC", "name": "Docs"}),
            &a,
            &o,
        )
        .await
        .unwrap();
    let n = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "A"}),
            &a,
            &o,
        )
        .await
        .unwrap();
    reg.execute(
        &pool,
        "note.update",
        json!({"id": n["id"], "title": "B", "expected_version": 1}),
        &a,
        &o,
    )
    .await
    .unwrap();
    let e = reg
        .execute(
            &pool,
            "note.update",
            json!({"id": n["id"], "title": "C", "expected_version": 1}),
            &a,
            &o,
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, cnote_core::ErrorCode::Conflict);
    let h = reg
        .execute(&pool, "note.history", json!({"id": n["id"]}), &a, &o)
        .await
        .unwrap();
    let kinds: Vec<_> = h
        .as_array()
        .unwrap()
        .iter()
        .map(|e| e["kind"].as_str().unwrap().to_string())
        .collect();
    assert_eq!(kinds, vec!["note.updated", "note.created"]);
}

#[tokio::test]
async fn agents_are_gated_by_tool_groups_and_idempotent() {
    let (pool, reg) = setup().await;
    let (_, a) = tenant(&pool, &reg).await;
    let o = ExecOptions::default();
    let created = reg
        .execute(&pool, "agent.create", json!({"name": "Coder"}), &a, &o)
        .await
        .unwrap();
    let agent = auth::principal_from_api_key(&pool, created["api_key"].as_str().unwrap())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(agent.kind, cnote_core::PrincipalKind::Agent);
    assert_eq!(
        agent.user_id, a.user_id,
        "agent acts on behalf of its creator"
    );

    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "AG", "name": "Agents"}),
            &agent,
            &o,
        )
        .await
        .unwrap();
    // Destructive group is off by default for agents.
    let n = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "x"}),
            &agent,
            &o,
        )
        .await
        .unwrap();
    let e = reg
        .execute(&pool, "note.delete", json!({"id": n["id"]}), &agent, &o)
        .await
        .unwrap_err();
    assert_eq!(e.code, cnote_core::ErrorCode::Forbidden);

    // Idempotency key returns the first result.
    let io = ExecOptions {
        idempotency_key: Some("k1".into()),
        ..Default::default()
    };
    let r1 = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "once"}),
            &agent,
            &io,
        )
        .await
        .unwrap();
    let r2 = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "once"}),
            &agent,
            &io,
        )
        .await
        .unwrap();
    assert_eq!(r1["id"], r2["id"]);

    // Event attributed to the agent, on behalf of the human.
    let h = reg
        .execute(&pool, "note.history", json!({"id": r1["id"]}), &a, &o)
        .await
        .unwrap();
    assert_eq!(h[0]["actor_type"], "agent");
    assert_eq!(h[0]["on_behalf_of"], json!(a.user_id));

    // Personal API key without explicit scopes still respects workspace tool groups.
    let (_, key) = create_api_key(&pool, &a, "cli", &[], None).await.unwrap();
    let kp = auth::principal_from_api_key(&pool, &key)
        .await
        .unwrap()
        .unwrap();
    assert!(
        reg.execute(
            &pool,
            "member.add",
            json!({"email": "x@y.z", "role": "member"}),
            &kp,
            &o
        )
        .await
        .is_err()
    );
}

#[tokio::test]
async fn security_regressions() {
    let (pool, reg) = setup().await;
    let (_, owner) = tenant(&pool, &reg).await;
    let o = ExecOptions::default();

    // Idempotency keys never replay another principal's result.
    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "SEC", "name": "Sec"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let admin = reg
        .execute(
            &pool,
            "member.add",
            json!({"email": format!("{}@t.local", Uuid::new_v4()), "role": "admin"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let admin_p = Principal::user(
        admin["user_id"].as_str().unwrap().parse().unwrap(),
        owner.workspace_id,
        cnote_core::Role::Admin,
    );
    let io = ExecOptions {
        idempotency_key: Some("same".into()),
        ..Default::default()
    };
    let a = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "mine"}),
            &owner,
            &io,
        )
        .await
        .unwrap();
    let b = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "theirs"}),
            &admin_p,
            &io,
        )
        .await
        .unwrap();
    assert_ne!(a["id"], b["id"]);

    // Secrets are never cached for replay.
    let k1 = reg
        .execute(&pool, "apikey.create", json!({"name": "k"}), &owner, &io)
        .await
        .unwrap();
    let k2 = reg
        .execute(&pool, "apikey.create", json!({"name": "k"}), &owner, &io)
        .await
        .unwrap();
    assert_ne!(k1["api_key"], k2["api_key"]);

    // An admin cannot demote the owner.
    let e = reg
        .execute(
            &pool,
            "member.add",
            json!({"email": "x", "role": "guest"}),
            &admin_p,
            &o,
        )
        .await;
    assert!(e.is_ok(), "adding a new guest is fine");
    let owner_email: String = {
        let mut tx = cnote_db::begin_tenant(&pool, owner.workspace_id)
            .await
            .unwrap();
        sqlx::query_scalar("select u.email from core.users u join core.memberships m on m.user_id = u.id where m.role = 'owner'")
            .fetch_one(&mut *tx)
            .await
            .unwrap()
    };
    let e = reg
        .execute(
            &pool,
            "member.add",
            json!({"email": owner_email, "role": "guest"}),
            &admin_p,
            &o,
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, cnote_core::ErrorCode::Forbidden);

    // Relations to private notes don't leak titles.
    let private = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "PRV", "name": "Private", "visibility": "private"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let secret = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": private["id"], "title": "Secret plan"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let open_note = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "Open"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    reg.execute(
        &pool,
        "relation.add",
        json!({"from_id": open_note["id"], "to_id": secret["id"], "kind": "relates"}),
        &owner,
        &o,
    )
    .await
    .unwrap();
    let member = reg
        .execute(
            &pool,
            "member.add",
            json!({"email": format!("{}@t.local", Uuid::new_v4()), "role": "member"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let member_p = Principal::user(
        member["user_id"].as_str().unwrap().parse().unwrap(),
        owner.workspace_id,
        cnote_core::Role::Member,
    );
    let seen = reg
        .execute(
            &pool,
            "note.get",
            json!({"id": open_note["id"]}),
            &member_p,
            &o,
        )
        .await
        .unwrap();
    assert_eq!(seen["relations"].as_array().unwrap().len(), 0);
}

#[tokio::test]
async fn deactivated_member_is_no_longer_a_principal() {
    let (pool, reg) = setup().await;
    let (_, owner) = tenant(&pool, &reg).await;
    let email = format!("{}@test.local", Uuid::new_v4());
    let user = auth::sign_up(&pool, &email, "Dev", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let opts = ExecOptions::default();
    reg.execute(
        &pool,
        "member.add",
        json!({"email": email, "role": "member"}),
        &owner,
        &opts,
    )
    .await
    .unwrap();
    let ws = owner.workspace_id;
    assert!(auth::user_principal(&pool, user, ws).await.is_ok());
    let mut tx = cnote_db::begin_tenant(&pool, ws).await.unwrap();
    sqlx::query("update core.memberships set deactivated_at = now() where user_id = $1")
        .bind(user)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    assert!(auth::user_principal(&pool, user, ws).await.is_err());
    let mut tx = cnote_db::begin_tenant(&pool, ws).await.unwrap();
    sqlx::query("update core.memberships set deactivated_at = null where user_id = $1")
        .bind(user)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    assert!(auth::user_principal(&pool, user, ws).await.is_ok());
}
