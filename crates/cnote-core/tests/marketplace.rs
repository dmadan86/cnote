//! App marketplace (migration 0900) against a real Postgres. Run `scripts/setup-db.sh` first.
//! Secrets and codes are generated per run.

use cnote_core::auth;
use cnote_core::marketplace::exchange_token;
use cnote_core::{ErrorCode, ExecOptions, Principal, Registry, Role};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

struct T {
    pool: PgPool,
    reg: Arc<Registry>,
}

impl T {
    async fn new() -> Self {
        let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
        // Every test user is @test.local; members added as @other.local are not platform admins.
        // SAFETY: same value in every test of this binary, set before any command runs.
        unsafe { std::env::set_var("CNOTE_PLATFORM_ADMINS", "@test.local") };
        let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
            .await
            .unwrap();
        Self {
            pool,
            reg: Registry::new(vec![cnote_core::module()]),
        }
    }

    /// A fresh workspace and its owner.
    async fn ws(&self) -> Principal {
        let user = auth::sign_up(
            &self.pool,
            &format!("{}@test.local", Uuid::new_v4()),
            "Olive",
            &Uuid::new_v4().to_string(),
        )
        .await
        .unwrap();
        let ws = auth::create_workspace(&self.pool, &self.reg, user, "Acme")
            .await
            .unwrap();
        auth::user_principal(&self.pool, user, ws).await.unwrap()
    }

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
        self.run(p, cmd, input).await.unwrap()
    }

    async fn err(&self, p: &Principal, cmd: &str, input: Value) -> ErrorCode {
        self.run(p, cmd, input).await.unwrap_err().code
    }

    async fn member(&self, owner: &Principal, role: &str, domain: &str) -> Principal {
        let m = self
            .ok(
                owner,
                "member.add",
                json!({"email": format!("{}@{domain}", Uuid::new_v4()), "role": role}),
            )
            .await;
        let id: Uuid = m["user_id"].as_str().unwrap().parse().unwrap();
        Principal::user(id, owner.workspace_id, Role::parse(role))
    }
}

fn app(slug: &str) -> Value {
    json!({
        "slug": slug, "name": "Sync Bot", "tagline": "Syncs things", "description": "Longer text",
        "category": "developer", "redirect_url": "http://127.0.0.1:9/callback?x=1",
        "scopes": ["read", "write_items"],
    })
}

fn slug() -> String {
    format!("app-{}", &Uuid::new_v4().simple().to_string()[..12])
}

/// Publishes an app from `publisher`; returns (app_id, client_secret).
async fn publish(t: &T, publisher: &Principal) -> (String, String) {
    let saved = t.ok(publisher, "dev.app_save", app(&slug())).await;
    let id = saved["app_id"].as_str().unwrap().to_string();
    t.ok(publisher, "dev.app_submit", json!({"id": id})).await;
    t.ok(
        publisher,
        "market.review",
        json!({"id": id, "approve": true}),
    )
    .await;
    (id, saved["client_secret"].as_str().unwrap().to_string())
}

fn code_of(redirect_to: &str) -> String {
    let u = redirect_to.split("code=").nth(1).unwrap();
    u.split('&').next().unwrap().to_string()
}

#[tokio::test]
async fn install_exchange_call_and_uninstall() {
    let t = T::new().await;
    let publisher = t.ws().await;
    let (app_id, secret) = publish(&t, &publisher).await;
    let admin = t.ws().await;

    let listed = t.ok(&admin, "market.list", json!({})).await;
    let me = listed
        .as_array()
        .unwrap()
        .iter()
        .find(|a| a["id"] == app_id)
        .unwrap();
    assert_eq!(me["installed"], false);

    let inst = t
        .ok(&admin, "market.install", json!({"app_id": app_id}))
        .await;
    let redirect = inst["redirect_to"].as_str().unwrap();
    assert!(redirect.starts_with("http://127.0.0.1:9/callback?x=1&code=cnic_"));
    assert!(redirect.ends_with(&format!("&workspace_id={}", admin.workspace_id)));
    assert_eq!(
        t.err(&admin, "market.install", json!({"app_id": app_id}))
            .await,
        ErrorCode::Conflict
    );
    let code = code_of(redirect);

    // wrong secret / wrong client are rejected and do not burn the code
    assert!(
        exchange_token(&t.pool, &app_id, "cnas_wrong", &code)
            .await
            .unwrap()
            .is_none()
    );
    assert!(
        exchange_token(&t.pool, &Uuid::new_v4().to_string(), &secret, &code)
            .await
            .unwrap()
            .is_none()
    );

    let tok = exchange_token(&t.pool, &app_id, &secret, &code)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(tok["token_type"], "bearer");
    assert_eq!(tok["workspace_id"], admin.workspace_id.to_string());
    // replay
    assert!(
        exchange_token(&t.pool, &app_id, &secret, &code)
            .await
            .unwrap()
            .is_none()
    );

    let key = tok["access_token"].as_str().unwrap();
    let app_p = auth::principal_from_api_key(&t.pool, key)
        .await
        .unwrap()
        .unwrap();
    t.ok(&app_p, "market.installed", json!({})).await; // read: allowed
    assert_eq!(
        t.err(&app_p, "note.create", json!({"title": "x"})).await,
        ErrorCode::Forbidden
    );
    assert_eq!(
        t.err(&app_p, "market.uninstall", json!({"app_id": app_id}))
            .await,
        ErrorCode::Forbidden
    );
    let installed = t.ok(&admin, "market.installed", json!({})).await;
    assert_eq!(installed[0]["app_id"], app_id);

    let un = t
        .ok(&admin, "market.uninstall", json!({"app_id": app_id}))
        .await;
    assert_eq!(un["uninstalled"], true);
    assert!(
        auth::principal_from_api_key(&t.pool, key)
            .await
            .unwrap()
            .is_none()
    );
    assert_eq!(
        t.ok(&admin, "market.uninstall", json!({"app_id": app_id}))
            .await["uninstalled"],
        false
    );
}

#[tokio::test]
async fn only_admin_people_install_and_settings_merge() {
    let t = T::new().await;
    let publisher = t.ws().await;
    let (app_id, _) = publish(&t, &publisher).await;
    let owner = t.ws().await;
    let member = t.member(&owner, "member", "other.local").await;
    assert_eq!(
        t.err(&member, "market.install", json!({"app_id": app_id}))
            .await,
        ErrorCode::Forbidden
    );

    // An admin agent (admin tool group enabled) is still not a person.
    t.ok(
        &owner,
        "workspace.update",
        json!({"settings": {"agent_tool_groups": ["read", "admin"]}}),
    )
    .await;
    let a = t
        .ok(
            &owner,
            "agent.create",
            json!({"name": "bot", "scopes": ["read", "admin"]}),
        )
        .await;
    let agent = auth::principal_from_api_key(&t.pool, a["api_key"].as_str().unwrap())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        t.err(&agent, "market.install", json!({"app_id": app_id}))
            .await,
        ErrorCode::Forbidden
    );

    // settings.modules merges without wiping agent_tool_groups
    let w = t
        .ok(
            &owner,
            "workspace.update",
            json!({"settings": {"modules": ["docs"]}}),
        )
        .await;
    assert_eq!(w["settings"]["agent_tool_groups"], json!(["read", "admin"]));
    let got = t.ok(&owner, "market.get", json!({"slug": t.ok(&owner, "market.list", json!({})).await.as_array().unwrap().iter().find(|x| x["id"] == app_id).unwrap()["slug"]})).await;
    assert_eq!(got["blocked_scopes"], json!(["write_items"]));
}

#[tokio::test]
async fn validation_and_review_rules() {
    let t = T::new().await;
    let owner = t.ws().await;
    let bad = |f: &dyn Fn(&mut Value)| {
        let mut v = app(&slug());
        f(&mut v);
        v
    };
    for v in [
        bad(&|v| v["scopes"] = json!(["read", "admin"])),
        bad(&|v| v["scopes"] = json!(["read", "destructive"])),
        bad(&|v| v["scopes"] = json!(["write_items"])),
        bad(&|v| v["redirect_url"] = json!("http://example.com/cb")),
        bad(&|v| v["icon_url"] = json!("http://example.com/i.png")),
        bad(&|v| v["slug"] = json!("A!")),
        bad(&|v| v["category"] = json!("nope")),
    ] {
        assert_eq!(t.err(&owner, "dev.app_save", v).await, ErrorCode::Invalid);
    }
    let saved = t.ok(&owner, "dev.app_save", app(&slug())).await;
    let id = saved["app_id"].as_str().unwrap();
    // drafts are invisible to others, and installable only by their publisher
    let other = t.ws().await;
    assert_eq!(
        t.err(&other, "market.install", json!({"app_id": id})).await,
        ErrorCode::NotFound
    );
    t.ok(&owner, "dev.app_submit", json!({"id": id})).await;

    // review needs a platform admin: not a member from another domain, nor an agent
    let outsider = t.member(&owner, "admin", "other.local").await;
    assert_eq!(
        t.err(
            &outsider,
            "market.review",
            json!({"id": id, "approve": true})
        )
        .await,
        ErrorCode::Forbidden
    );
    assert_eq!(
        t.err(&outsider, "market.review_queue", json!({})).await,
        ErrorCode::Forbidden
    );
    let q = t.ok(&owner, "market.review_queue", json!({})).await;
    assert!(q.as_array().unwrap().iter().any(|a| a["id"] == id));
    t.ok(&owner, "market.review", json!({"id": id, "approve": true}))
        .await;

    // editing a published app sends it back to review; delete works with zero installs
    let mut edit = app(&slug());
    edit["id"] = json!(id);
    t.ok(&owner, "dev.app_save", edit).await;
    let mine = t.ok(&owner, "dev.apps", json!({})).await;
    assert_eq!(mine[0]["status"], "review");
    assert!(mine[0].get("client_secret_hash").is_none());
    assert_eq!(
        t.ok(&owner, "dev.app_delete", json!({"id": id})).await["deleted"],
        true
    );
}
