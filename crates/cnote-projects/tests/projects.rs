//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use cnote_core::events::Event;
use cnote_core::{CmdResult, Ctx, ErrorCode, ExecOptions, Principal, Registry, auth};
use cnote_projects::automation;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

struct T {
    pool: PgPool,
    reg: Arc<Registry>,
    ws: Uuid,
    owner: Principal,
}

async fn setup() -> T {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 8)
        .await
        .unwrap();
    let reg = Registry::new(vec![cnote_core::module(), cnote_projects::module()]);
    let user = auth::sign_up(
        &pool,
        &format!("{}@test.local", Uuid::new_v4()),
        "Tess",
        &Uuid::new_v4().to_string(),
    )
    .await
    .unwrap();
    let ws = auth::create_workspace(&pool, &reg, user, "Acme")
        .await
        .unwrap();
    let owner = auth::user_principal(&pool, user, ws).await.unwrap();
    T {
        pool,
        reg,
        ws,
        owner,
    }
}

impl T {
    async fn try_as(&self, p: &Principal, name: &str, input: Value) -> CmdResult<Value> {
        self.reg
            .execute(&self.pool, name, input, p, &ExecOptions::default())
            .await
    }
    async fn as_(&self, p: &Principal, name: &str, input: Value) -> Value {
        self.try_as(p, name, input)
            .await
            .unwrap_or_else(|e| panic!("{name} failed: {e}"))
    }
    async fn run(&self, name: &str, input: Value) -> Value {
        self.as_(&self.owner.clone(), name, input).await
    }
    async fn space(&self, key: &str) -> Uuid {
        let s = self
            .run(
                "space.create",
                json!({"key": key, "name": key, "template": "software"}),
            )
            .await;
        s["id"].as_str().unwrap().parse().unwrap()
    }
    async fn item(&self, space: Uuid, title: &str, extra: Value) -> Value {
        let mut input = json!({"space_id": space, "title": title});
        input
            .as_object_mut()
            .unwrap()
            .extend(extra.as_object().unwrap().clone());
        self.run("item.create", input).await
    }
    async fn member(&self, role: &str) -> Principal {
        let m = self.run("member.add", json!({"email": format!("{}@test.local", Uuid::new_v4()), "name": "Mo", "role": role})).await;
        auth::user_principal(
            &self.pool,
            m["user_id"].as_str().unwrap().parse().unwrap(),
            self.ws,
        )
        .await
        .unwrap()
    }
    async fn last_event(&self) -> i64 {
        let mut tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        let id: Option<i64> = sqlx::query_scalar("select max(id) from core.events")
            .fetch_one(&mut *tx)
            .await
            .unwrap();
        id.unwrap_or(0)
    }
    /// What the worker does: run event handlers for events after `since`.
    async fn dispatch(&self, since: i64) {
        let tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        let mut ctx = Ctx {
            tx,
            principal: Principal::system(self.ws),
            registry: self.reg.clone(),
        };
        let events: Vec<Event> =
            sqlx::query_as("select * from core.events where id > $1 order by id")
                .bind(since)
                .fetch_all(&mut *ctx.tx)
                .await
                .unwrap();
        for e in &events {
            automation::on_event(&mut ctx, e).await.unwrap();
        }
        ctx.tx.commit().await.unwrap();
    }
    async fn runs(&self, automation_id: &Value) -> Vec<Value> {
        self.run("automation.runs", json!({"automation_id": automation_id}))
            .await
            .as_array()
            .unwrap()
            .clone()
    }
    async fn events_of(&self, note: &Value, kind: &str) -> Vec<(String, Option<String>)> {
        let mut tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        sqlx::query_as("select actor_type, actor_id from core.events where note_id = $1::text::uuid and kind = $2 order by id")
            .bind(note["id"].as_str().unwrap())
            .bind(kind)
            .fetch_all(&mut *tx)
            .await
            .unwrap()
    }
}

fn statuses(v: &Value) -> Vec<&str> {
    v.as_array()
        .unwrap()
        .iter()
        .map(|i| i["status"].as_str().unwrap())
        .collect()
}

#[tokio::test]
async fn concurrent_creates_get_sequential_keys() {
    let t = Arc::new(setup().await);
    let space = t.space("ENG").await;
    let mut tasks = vec![];
    for n in 0..8 {
        let t = t.clone();
        tasks.push(tokio::spawn(async move {
            t.item(space, &format!("task {n}"), json!({})).await
        }));
    }
    let mut keys = vec![];
    for h in tasks {
        keys.push(h.await.unwrap()["key"].as_str().unwrap().to_string());
    }
    keys.sort_by_key(|k| k[4..].parse::<u32>().unwrap());
    assert_eq!(
        keys,
        (1..=8).map(|n| format!("ENG-{n}")).collect::<Vec<_>>()
    );
    let first = t.run("item.get", json!({"key": "eng-1"})).await;
    assert_eq!(first["status"], "Backlog");
    assert_eq!(first["status_category"], "todo");
    assert_eq!(first["item_type"], "task");
    assert_eq!(first["reporter_id"], json!(t.owner.user_id));
    let e = t
        .try_as(
            &t.owner,
            "item.create",
            json!({"space_id": space, "title": "x", "item_type": "deal"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
    assert!(
        e.message.contains("epic, story, task, bug, subtask"),
        "{}",
        e.message
    );
}

#[tokio::test]
async fn transition_guards() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let mut def = t.run("workflow.get", json!({"space_id": space})).await["definition"].clone();
    for tr in def["transitions"].as_array_mut().unwrap() {
        if tr["name"] == "Start" {
            tr["guards"] = json!([{"type": "required_fields", "fields": ["estimate", "assignee"]}]);
        }
        if tr["name"] == "Complete" {
            tr["guards"] = json!([{"type": "min_role", "role": "admin"}]);
        }
    }
    let member = t.member("member").await;
    // Only admins edit workflows.
    let e = t
        .try_as(
            &member,
            "workflow.update",
            json!({"space_id": space, "definition": def}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);
    let up = t
        .run(
            "workflow.update",
            json!({"space_id": space, "definition": def, "migrate_items": true}),
        )
        .await;
    assert_eq!(up["version"], 2);

    let item = t.item(space, "guarded", json!({})).await;
    assert_eq!(item["workflow_version"], 2);
    t.run("item.transition", json!({"key": "ENG-1", "to": "Todo"}))
        .await;
    let e = t
        .try_as(
            &t.owner,
            "item.transition",
            json!({"key": "ENG-1", "to": "In Progress"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
    assert!(
        e.message.contains("estimate") && e.message.contains("assignee"),
        "{}",
        e.message
    );
    t.run("item.update", json!({"key": "ENG-1", "estimate": 3.0}))
        .await;
    let e = t
        .try_as(
            &t.owner,
            "item.transition",
            json!({"key": "ENG-1", "to": "In Progress"}),
        )
        .await
        .unwrap_err();
    assert!(
        e.message.contains("assignee") && !e.message.contains("estimate"),
        "{}",
        e.message
    );
    t.run(
        "item.assign",
        json!({"key": "ENG-1", "user_id": member.user_id}),
    )
    .await;
    let moved = t
        .run(
            "item.transition",
            json!({"key": "ENG-1", "to": "In Progress"}),
        )
        .await;
    assert_eq!(moved["status"], "In Progress");
    assert_eq!(moved["status_category"], "in_progress");

    // Role guard: members cannot complete, the owner can.
    let e = t
        .try_as(
            &member,
            "item.transition",
            json!({"key": "ENG-1", "to": "Done"}),
        )
        .await
        .unwrap_err();
    assert!(e.message.contains("requires role admin"), "{}", e.message);
    let done = t
        .run("item.transition", json!({"key": "ENG-1", "to": "Done"}))
        .await;
    assert!(done["resolved_at"].is_string());
    // Undefined transition names the allowed targets.
    let e = t
        .try_as(
            &t.owner,
            "item.transition",
            json!({"key": "ENG-1", "to": "In Review"}),
        )
        .await
        .unwrap_err();
    assert!(e.message.contains("allowed targets"), "{}", e.message);
    let ev = t.run("note.history", json!({"id": done["id"]})).await;
    let tr = ev
        .as_array()
        .unwrap()
        .iter()
        .find(|e| e["kind"] == "item.transitioned")
        .unwrap();
    assert_eq!(tr["payload"]["to"], "Done");
    assert_eq!(tr["payload"]["category"], "done");
    assert_eq!(tr["payload"]["from"], "In Progress");
}

#[tokio::test]
async fn workflow_versions_and_drift() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let old = t.item(space, "old", json!({})).await;
    let d = t.run("space.drift", json!({"space_id": space})).await;
    assert_eq!(d["in_sync"], true);
    assert_eq!(
        t.run("space.realign", json!({"space_id": space})).await["changed"],
        false
    );

    let mut def = t.run("workflow.get", json!({"space_id": space})).await["definition"].clone();
    def["statuses"]
        .as_array_mut()
        .unwrap()
        .push(json!({"name": "QA", "category": "in_progress"}));
    def["transitions"]
        .as_array_mut()
        .unwrap()
        .push(json!({"name": "To QA", "from": ["In Review"], "to": "QA"}));
    let up = t
        .run(
            "workflow.update",
            json!({"space_id": space, "definition": def}),
        )
        .await;
    assert_eq!(up["version"], 2);
    let d = t.run("space.drift", json!({"space_id": space})).await;
    assert_eq!(d["in_sync"], false);
    let diffs = d["differences"].to_string();
    assert!(
        diffs.contains("'QA' is not in the template") && diffs.contains("To QA"),
        "{diffs}"
    );

    // The old item keeps v1 (no QA); a new item follows v2.
    let new = t.item(space, "new", json!({})).await;
    assert_eq!(
        (
            old["workflow_version"].as_i64(),
            new["workflow_version"].as_i64()
        ),
        (Some(1), Some(2))
    );
    for k in ["ENG-1", "ENG-2"] {
        t.run("item.transition", json!({"key": k, "to": "In Progress"}))
            .await;
        t.run("item.transition", json!({"key": k, "to": "In Review"}))
            .await;
    }
    let e = t
        .try_as(
            &t.owner,
            "item.transition",
            json!({"key": "ENG-1", "to": "QA"}),
        )
        .await
        .unwrap_err();
    assert!(e.message.contains("no transition"), "{}", e.message);
    assert_eq!(
        t.run("item.transition", json!({"key": "ENG-2", "to": "QA"}))
            .await["status"],
        "QA"
    );

    // Invalid definitions are rejected.
    let mut bad = def.clone();
    bad["transitions"][0]["to"] = json!("Nowhere");
    let e = t
        .try_as(
            &t.owner,
            "workflow.update",
            json!({"space_id": space, "definition": bad}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);

    // Realign applies the template as v3 and migrates items whose status still exists.
    let r = t
        .run(
            "space.realign",
            json!({"space_id": space, "migrate_items": true}),
        )
        .await;
    assert_eq!(r["version"], 3);
    assert_eq!(
        t.run("space.drift", json!({"space_id": space})).await["in_sync"],
        true
    );
    assert_eq!(
        t.run("item.get", json!({"key": "ENG-1"})).await["workflow_version"],
        3
    );
    // ENG-2 sits in QA, which the template lacks, so it stays on its version.
    assert_eq!(
        t.run("item.get", json!({"key": "ENG-2"})).await["workflow_version"],
        2
    );
}

#[tokio::test]
async fn pr_merged_moves_in_review_to_done() {
    let t = setup().await;
    let space = t.space("ENG").await;
    t.item(space, "Login page", json!({})).await;
    t.item(space, "Other", json!({})).await;
    for to in ["In Progress", "In Review"] {
        t.run("item.transition", json!({"key": "ENG-1", "to": to}))
            .await;
    }
    let r = t
        .run("evidence.record", json!({"source": "github", "kind": "pr_merged", "item_keys": ["ENG-1", "eng-99"], "url": "https://example.test/pr/7"}))
        .await;
    assert_eq!(r["recorded"], 1);
    assert_eq!(r["unknown"], json!(["eng-99"]));
    assert_eq!(r["transitions"][0]["from"], "In Review");
    assert_eq!(r["transitions"][0]["to"], "Done");
    let item = t.run("item.get", json!({"key": "ENG-1"})).await;
    assert_eq!(item["status"], "Done");
    assert_eq!(item["evidence"][0]["kind"], "pr_merged");
    assert_eq!(item["evidence"][0]["url"], "https://example.test/pr/7");

    // pr_opened pulls a Todo item into review; unrelated evidence changes nothing.
    let r = t
        .run(
            "evidence.record",
            json!({"source": "github", "kind": "pr_opened", "item_keys": ["ENG-2"]}),
        )
        .await;
    assert_eq!(r["transitions"][0]["to"], "In Review");
    let r = t
        .run(
            "evidence.record",
            json!({"source": "ci", "kind": "deploy_succeeded", "item_keys": ["ENG-2"]}),
        )
        .await;
    assert_eq!(r["transitions"].as_array().unwrap().len(), 0);
    assert_eq!(
        t.run("item.get", json!({"key": "ENG-2"})).await["status"],
        "In Review"
    );
    let e = t
        .try_as(
            &t.owner,
            "evidence.record",
            json!({"source": "x", "kind": "Bad Kind", "item_keys": []}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn webhook_verifies_signature_and_records_evidence() {
    let t = setup().await;
    let space = t.space("ENG").await;
    t.item(space, "Ship it", json!({})).await;
    t.run(
        "item.transition",
        json!({"key": "ENG-1", "to": "In Progress"}),
    )
    .await;
    let body = serde_json::to_vec(&json!({
        "action": "closed",
        "pull_request": {"merged": true, "title": "ENG-1: ship it", "head": {"ref": "feature/eng-1"}, "body": "", "html_url": "https://example.test/pr/1"}
    }))
    .unwrap();
    let sign = |secret: &str, body: &[u8]| {
        format!(
            "sha256={}",
            hex::encode(cnote_projects::github::hmac_sha256(secret.as_bytes(), body))
        )
    };

    // No secret configured: nothing is accepted.
    let e = cnote_projects::handle_github_webhook(&t.pool, &t.reg, t.ws, &sign("x", &body), &body)
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Unauthenticated);

    let secret = Uuid::new_v4().to_string();
    let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
    sqlx::query("update core.workspaces set settings = settings || jsonb_build_object('github_webhook_secret', $1::text) where id = $2")
        .bind(&secret)
        .bind(t.ws)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();

    for bad in [
        sign("wrong-secret", &body),
        String::new(),
        "sha256=00".into(),
        sign(&secret, b"{}"),
    ] {
        let e = cnote_projects::handle_github_webhook(&t.pool, &t.reg, t.ws, &bad, &body)
            .await
            .unwrap_err();
        assert_eq!(e.code, ErrorCode::Unauthenticated, "{bad}");
    }
    assert_eq!(
        t.run("item.get", json!({"key": "ENG-1"})).await["status"],
        "In Progress",
        "rejected calls change nothing"
    );

    let r =
        cnote_projects::handle_github_webhook(&t.pool, &t.reg, t.ws, &sign(&secret, &body), &body)
            .await
            .unwrap();
    assert_eq!(r["recorded"], 1);
    assert_eq!(r["transitions"][0]["to"], "Done");
    // Events without item keys or of other kinds are acknowledged and ignored.
    let ping = b"{\"zen\":\"hi\"}";
    let r =
        cnote_projects::handle_github_webhook(&t.pool, &t.reg, t.ws, &sign(&secret, ping), ping)
            .await
            .unwrap();
    assert_eq!(r["ignored"], true);
    // Another tenant cannot use this workspace's secret.
    let other = setup().await;
    let e = cnote_projects::handle_github_webhook(
        &other.pool,
        &other.reg,
        other.ws,
        &sign(&secret, &body),
        &body,
    )
    .await
    .unwrap_err();
    assert_eq!(e.code, ErrorCode::Unauthenticated);
}

#[tokio::test]
async fn cnql_list_respects_private_spaces_and_filters() {
    let t = setup().await;
    let open = t.space("ENG").await;
    let private: Uuid = t
        .run("space.create", json!({"key": "SEC", "name": "Secret", "template": "software", "visibility": "private"}))
        .await["id"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    let member = t.member("member").await;
    t.item(
        open,
        "Public login bug",
        json!({"item_type": "bug", "priority": "high", "labels": ["auth"], "estimate": 2.0}),
    )
    .await;
    t.item(
        open,
        "Public docs",
        json!({"labels": ["docs"], "assignee_id": member.user_id}),
    )
    .await;
    t.item(private, "Secret login plan", json!({})).await;

    let keys = |v: &Value| -> Vec<String> {
        v.as_array()
            .unwrap()
            .iter()
            .map(|i| i["key"].as_str().unwrap().to_string())
            .collect()
    };
    let all = t.run("item.list", json!({"cnql": "text ~ login"})).await;
    assert_eq!(
        keys(&all),
        vec!["ENG-1", "SEC-1"],
        "owner sees private spaces"
    );
    let theirs = t
        .as_(&member, "item.list", json!({"cnql": "text ~ login"}))
        .await;
    assert_eq!(
        keys(&theirs),
        vec!["ENG-1"],
        "member does not see the private space"
    );
    let theirs = t
        .as_(&member, "item.list", json!({"cnql": "project = SEC"}))
        .await;
    assert!(theirs.as_array().unwrap().is_empty());
    let e = t
        .try_as(&member, "item.get", json!({"key": "SEC-1"}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
    let e = t
        .try_as(
            &member,
            "item.update",
            json!({"key": "SEC-1", "priority": "low"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
    let e = t
        .try_as(
            &member,
            "item.create",
            json!({"space_key": "SEC", "title": "sneaky"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);

    // Filters and ordering.
    let q = |s: &str| json!({ "cnql": s });
    assert_eq!(
        keys(&t.run("item.list", q("type = bug")).await),
        vec!["ENG-1"]
    );
    assert_eq!(
        keys(
            &t.run(
                "item.list",
                q("labels IN (docs, nothing) AND project = ENG")
            )
            .await
        ),
        vec!["ENG-2"]
    );
    assert_eq!(
        keys(
            &t.as_(&member, "item.list", q("assignee = currentUser()"))
                .await
        ),
        vec!["ENG-2"]
    );
    assert_eq!(
        keys(
            &t.run("item.list", q("assignee IS EMPTY AND project = ENG"))
                .await
        ),
        vec!["ENG-1"]
    );
    assert_eq!(
        keys(&t.run("item.list", q("priority >= high")).await),
        vec!["ENG-1"]
    );
    assert_eq!(
        keys(
            &t.run("item.list", q("project = ENG ORDER BY key DESC"))
                .await
        ),
        vec!["ENG-2", "ENG-1"]
    );
    assert_eq!(
        keys(
            &t.run(
                "item.list",
                q("created >= -1d AND NOT (type = bug) AND project = ENG")
            )
            .await
        ),
        vec!["ENG-2"]
    );
    assert_eq!(
        keys(
            &t.run(
                "item.list",
                q("statusCategory = \"To Do\" AND status != Done AND project = ENG")
            )
            .await
        )
        .len(),
        2
    );
    assert!(
        t.run("item.list", q("statusCategory = Done"))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
    assert_eq!(
        t.run("item.list", json!({"cnql": "project = ENG", "limit": 1}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );
    // Values are binds: an injection attempt is just a string that matches nothing.
    let r = t
        .run("item.list", q("status = \"x'; drop table core.notes; --\""))
        .await;
    assert!(r.as_array().unwrap().is_empty());
    // Errors carry the position.
    let e = t
        .try_as(&t.owner, "item.list", q("project = ENG AND stauts = Done"))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
    assert!(
        e.message.contains("unknown field 'stauts'") && e.message.contains("column 19"),
        "{}",
        e.message
    );
    // Another tenant sees nothing.
    let other = setup().await;
    assert!(
        other
            .run("item.list", json!({}))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn sprints_move_leftovers_and_report() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let s1: Value = t
        .run(
            "sprint.create",
            json!({"space_id": space, "name": "Sprint 1"}),
        )
        .await;
    let s2: Value = t
        .run(
            "sprint.create",
            json!({"space_id": space, "name": "Sprint 2"}),
        )
        .await;
    let s1id = s1["id"].clone();
    let a = t
        .item(
            space,
            "done one",
            json!({"sprint_id": s1id, "estimate": 5.0}),
        )
        .await;
    t.item(
        space,
        "left over",
        json!({"sprint_id": s1id, "estimate": 3.0}),
    )
    .await;
    t.item(space, "also left", json!({"sprint_id": s1id})).await;
    // An item from another space cannot be put in this sprint.
    let other_space = t.space("OPS").await;
    let e = t
        .try_as(
            &t.owner,
            "item.create",
            json!({"space_id": other_space, "title": "x", "sprint_id": s1id}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);

    let e = t
        .try_as(&t.owner, "sprint.complete", json!({"id": s1id}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict, "only active sprints complete");
    let started = t.run("sprint.start", json!({"id": s1id})).await;
    assert_eq!(started["state"], "active");
    let e = t
        .try_as(&t.owner, "sprint.start", json!({"id": s2["id"]}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict, "one active sprint per space");

    t.run("item.transition", json!({"key": "ENG-1", "to": "Todo"}))
        .await;
    t.run("item.transition", json!({"key": "ENG-1", "to": "Done"}))
        .await;
    let burn = t.run("report.burndown", json!({"sprint_id": s1id})).await;
    assert_eq!(burn["total_points"], 9.0);
    let last = burn["points"].as_array().unwrap().last().unwrap();
    assert_eq!(last["remaining"], 4.0);

    let done = t.run("sprint.complete", json!({"id": s1id})).await;
    assert_eq!(done["completed_count"], 1);
    assert_eq!(done["completed_points"], 5.0);
    assert_eq!(done["moved_count"], 2);
    assert_eq!(done["moved_to"], s2["id"]);
    let moved = t
        .run(
            "item.list",
            json!({"cnql": format!("sprint = \"{}\"", "Sprint 2")}),
        )
        .await;
    assert_eq!(moved.as_array().unwrap().len(), 2);
    let kept = t.run("item.get", json!({"id": a["id"]})).await;
    assert_eq!(
        kept["sprint_id"], s1id,
        "finished work stays in the closed sprint"
    );
    let closed = t
        .run("item.list", json!({"cnql": "sprint IN closedSprints()"}))
        .await;
    assert_eq!(closed.as_array().unwrap().len(), 1);

    // No future sprint left: leftovers go back to the backlog.
    t.run("sprint.start", json!({"id": s2["id"]})).await;
    let open = t
        .run(
            "item.list",
            json!({"cnql": "sprint IN openSprints() AND statusCategory != Done"}),
        )
        .await;
    assert_eq!(open.as_array().unwrap().len(), 2);
    let done2 = t.run("sprint.complete", json!({"id": s2["id"]})).await;
    assert_eq!(done2["moved_count"], 2);
    assert!(done2["moved_to"].is_null());
    let backlog = t.run("item.list", json!({"cnql": "sprint IS EMPTY"})).await;
    assert_eq!(backlog.as_array().unwrap().len(), 2);

    let v = t.run("report.velocity", json!({"space_id": space})).await;
    assert_eq!(v["sprints"].as_array().unwrap().len(), 2);
    assert_eq!(v["average_points"], 2.5);
    assert_eq!(v["sprints"][1]["completed_points"], 5.0);
    assert_eq!(v["sprints"][1]["committed_points"], 9.0);
    // Burndown of the closed sprint still counts what was carried over.
    let burn = t.run("report.burndown", json!({"sprint_id": s1id})).await;
    assert_eq!(burn["total_points"], 9.0);
}

#[tokio::test]
async fn versions_components_rank_and_triage() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let v = t
        .run("version.create", json!({"space_id": space, "name": "1.0"}))
        .await;
    let c = t
        .run(
            "component.create",
            json!({"space_id": space, "name": "api"}),
        )
        .await;
    for n in 0..3 {
        t.item(
            space,
            &format!("t{n}"),
            json!({"fix_version_id": v["id"], "component_id": c["id"]}),
        )
        .await;
    }
    assert_eq!(
        t.run(
            "item.list",
            json!({"cnql": "fixVersion = \"1.0\" AND component = api"})
        )
        .await
        .as_array()
        .unwrap()
        .len(),
        3
    );
    let rel = t.run("version.release", json!({"id": v["id"]})).await;
    assert_eq!(rel["released"], true);
    assert_eq!(
        t.try_as(&t.owner, "version.release", json!({"id": v["id"]}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::Conflict
    );
    let order = |v: Value| -> Vec<String> {
        v.as_array()
            .unwrap()
            .iter()
            .map(|i| i["key"].as_str().unwrap().to_string())
            .collect()
    };
    assert_eq!(
        order(t.run("item.list", json!({})).await),
        vec!["ENG-1", "ENG-2", "ENG-3"]
    );
    t.run("item.rank", json!({"key": "ENG-3", "before": "ENG-1"}))
        .await;
    assert_eq!(
        order(t.run("item.list", json!({})).await),
        vec!["ENG-3", "ENG-1", "ENG-2"]
    );
    t.run("item.rank", json!({"key": "ENG-3", "after": "ENG-1"}))
        .await;
    assert_eq!(
        order(t.run("item.list", json!({})).await),
        vec!["ENG-1", "ENG-3", "ENG-2"]
    );
    assert_eq!(
        t.try_as(&t.owner, "item.rank", json!({"key": "ENG-3"}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::Invalid
    );

    // Items made by agents land in triage until accepted.
    let created = t.run("agent.create", json!({"name": "Triager"})).await;
    let agent = auth::principal_from_api_key(&t.pool, created["api_key"].as_str().unwrap())
        .await
        .unwrap()
        .unwrap();
    let filed = t
        .as_(
            &agent,
            "item.create",
            json!({"space_id": space, "title": "from agent"}),
        )
        .await;
    assert_eq!(filed["triage"], true);
    assert_eq!(
        t.run("triage.list", json!({}))
            .await
            .as_array()
            .unwrap()
            .len(),
        1
    );
    let acc = t
        .run(
            "triage.accept",
            json!({"id": filed["id"], "priority": "urgent"}),
        )
        .await;
    assert_eq!(
        (acc["triage"].as_bool(), acc["priority"].as_str()),
        (Some(false), Some("urgent"))
    );
    assert!(
        t.run("triage.list", json!({}))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
    assert_eq!(
        t.try_as(&t.owner, "triage.accept", json!({"id": filed["id"]}))
            .await
            .unwrap_err()
            .code,
        ErrorCode::Conflict
    );
    // update: clear and merge custom fields
    let u = t.run("item.update", json!({"key": "ENG-1", "due_date": "2030-01-02", "custom": {"team": "core"}, "title": "renamed"})).await;
    assert_eq!(
        (
            u["due_date"].as_str(),
            u["custom"]["team"].as_str(),
            u["title"].as_str()
        ),
        (Some("2030-01-02"), Some("core"), Some("renamed"))
    );
    let u = t
        .run(
            "item.update",
            json!({"key": "ENG-1", "clear": ["due_date"]}),
        )
        .await;
    assert!(u["due_date"].is_null());
    assert_eq!(u["custom"]["team"], "core");
}

#[tokio::test]
async fn agent_assignee_is_recorded_as_actor() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let created = t.run("agent.create", json!({"name": "Coder"})).await;
    let agent_id = created["agent_id"].as_str().unwrap().to_string();
    let agent = auth::principal_from_api_key(&t.pool, created["api_key"].as_str().unwrap())
        .await
        .unwrap()
        .unwrap();
    let item = t.item(space, "Implement feature", json!({})).await;

    let assigned = t
        .run("item.assign", json!({"key": "ENG-1", "agent_id": agent_id}))
        .await;
    assert_eq!(assigned["assignee_agent_id"], json!(agent_id));
    assert!(assigned["assignee_id"].is_null());
    assert_eq!(t.events_of(&item, "item.assigned_to_agent").await.len(), 1);
    assert!(
        t.run("item.list", json!({"cnql": "assignee = Coder"}))
            .await
            .as_array()
            .unwrap()
            .len()
            == 1
    );

    // The agent works its own item: events are attributed to the agent, on behalf of its owner.
    t.as_(
        &agent,
        "item.transition",
        json!({"key": "ENG-1", "to": "In Progress"}),
    )
    .await;
    let ev = t.events_of(&item, "item.transitioned").await;
    assert_eq!(ev, vec![("agent".to_string(), Some(agent_id.clone()))]);
    t.as_(
        &agent,
        "item.assign",
        json!({"key": "ENG-1", "agent_id": agent_id}),
    )
    .await;
    let ev = t.events_of(&item, "item.assigned").await;
    assert_eq!(ev[1], ("agent".to_string(), Some(agent_id.clone())));
    // Assigning a person afterwards clears the agent.
    let back = t
        .run(
            "item.assign",
            json!({"key": "ENG-1", "user_id": t.owner.user_id}),
        )
        .await;
    assert!(back["assignee_agent_id"].is_null());
    let e = t
        .try_as(
            &t.owner,
            "item.assign",
            json!({"key": "ENG-1", "agent_id": Uuid::new_v4()}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
    let e = t
        .try_as(
            &t.owner,
            "item.assign",
            json!({"key": "ENG-1", "agent_id": agent_id, "user_id": t.owner.user_id}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn automations_run_log_and_loop_protection() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let mk = |name: &str, trigger: Value, conds: Value, actions: Value, draft: bool| json!({"space_id": space, "name": name, "trigger": trigger, "conditions": conds, "actions": actions, "draft": draft});
    let on_created = json!({"kind": "event", "event": "item.created", "cnql": "type = bug"});
    let comment = json!([{"type": "comment", "body": "Triage {{item.key}}"}]);
    let a_bug = t
        .run(
            "automation.create",
            mk(
                "bug comment",
                on_created.clone(),
                json!([]),
                comment.clone(),
                false,
            ),
        )
        .await;
    let a_draft = t
        .run(
            "automation.create",
            mk(
                "imported",
                on_created.clone(),
                json!([]),
                comment.clone(),
                true,
            ),
        )
        .await;
    let a_cond = t
        .run(
            "automation.create",
            mk(
                "urgent only",
                json!({"kind": "event", "event": "item.created"}),
                json!([{"cnql": "priority = urgent"}]),
                comment.clone(),
                false,
            ),
        )
        .await;
    let a_fail = t
        .run(
            "automation.create",
            mk(
                "bad target",
                json!({"kind": "event", "event": "item.created", "cnql": "type = story"}),
                json!([]),
                json!([{"type": "transition", "to": "Nowhere"}]),
                false,
            ),
        )
        .await;
    // Validation at the door.
    for bad in [
        mk(
            "x",
            json!({"kind": "event", "event": "e", "cnql": "nope ="}),
            json!([]),
            comment.clone(),
            false,
        ),
        mk(
            "x",
            json!({"kind": "schedule", "every_minutes": 0}),
            json!([]),
            comment.clone(),
            false,
        ),
        mk(
            "x",
            json!({"kind": "event", "event": "e"}),
            json!([]),
            json!([]),
            false,
        ),
        mk(
            "x",
            json!({"kind": "event", "event": "e"}),
            json!([]),
            json!([{"type": "command", "name": "no.such", "input": {}}]),
            false,
        ),
    ] {
        assert_eq!(
            t.try_as(&t.owner, "automation.create", bad)
                .await
                .unwrap_err()
                .code,
            ErrorCode::Invalid
        );
    }

    let since = t.last_event().await;
    let bug = t.item(space, "crash", json!({"item_type": "bug"})).await;
    t.item(space, "plain", json!({"priority": "urgent"})).await;
    let story = t.item(space, "story", json!({"item_type": "story"})).await;
    t.dispatch(since).await;

    let runs = t.runs(&a_bug["id"]).await;
    assert_eq!(
        runs.len(),
        1,
        "trigger cnql filters out the task and the story"
    );
    assert_eq!(runs[0]["status"], "success");
    let comments = t.run("comment.list", json!({"note_id": bug["id"]})).await;
    assert_eq!(comments[0]["body"], "Triage ENG-1");
    assert!(t.runs(&a_draft["id"]).await.is_empty(), "drafts never run");
    let cond_runs = t.runs(&a_cond["id"]).await;
    let mut statuses: Vec<_> = cond_runs
        .iter()
        .map(|r| r["status"].as_str().unwrap())
        .collect();
    statuses.sort();
    assert_eq!(
        statuses,
        vec!["skipped", "skipped", "success"],
        "condition checked per event"
    );
    let failed = t.runs(&a_fail["id"]).await;
    assert_eq!(failed[0]["status"], "failed");
    assert!(
        failed[0]["log"].to_string().contains("no transition"),
        "{}",
        failed[0]["log"]
    );
    assert_eq!(
        t.run("item.get", json!({"id": story["id"]})).await["status"],
        "Backlog"
    );

    // Publishing a draft makes it run.
    t.run(
        "automation.update",
        json!({"id": a_draft["id"], "draft": false}),
    )
    .await;
    let since = t.last_event().await;
    t.item(space, "second crash", json!({"item_type": "bug"}))
        .await;
    t.dispatch(since).await;
    assert_eq!(t.runs(&a_draft["id"]).await.len(), 1);
    t.run(
        "automation.update",
        json!({"id": a_draft["id"], "enabled": false}),
    )
    .await;

    // An automation that keeps firing is blocked after 10 runs in a minute.
    let looper = t
        .run(
            "automation.create",
            mk(
                "noisy",
                json!({"kind": "event", "event": "item.created"}),
                json!([]),
                comment.clone(),
                false,
            ),
        )
        .await;
    let since = t.last_event().await;
    for n in 0..12 {
        t.item(space, &format!("n{n}"), json!({"item_type": "story"}))
            .await;
    }
    t.dispatch(since).await;
    let runs = t.runs(&looper["id"]).await;
    let count = |s: &str| runs.iter().filter(|r| r["status"] == s).count();
    assert_eq!((count("success"), count("loop_blocked")), (10, 2));
    let blocked = runs.iter().find(|r| r["status"] == "loop_blocked").unwrap();
    assert!(blocked["log"].to_string().contains("possible loop"));
    let listed = t.run("automation.list", json!({"space_id": space})).await;
    assert_eq!(listed.as_array().unwrap().len(), 5);
}

#[tokio::test]
async fn automation_actions_use_the_command_layer_with_creator_permissions() {
    let t = setup().await;
    let space = t.space("ENG").await;
    let member = t.member("member").await;
    // A member cannot save a rule that runs an admin-only command.
    let e = t
        .try_as(&member, "automation.create", json!({
            "space_id": space, "name": "escalate",
            "trigger": {"kind": "event", "event": "item.created"},
            "actions": [{"type": "command", "name": "workflow.update", "input": {"space_id": space, "definition": {"statuses": [{"name": "X", "category": "todo"}], "transitions": []}}}]
        }))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);
    // A workspace-wide rule needs an admin.
    let e = t.try_as(&member, "automation.create", json!({
        "name": "wide", "trigger": {"kind": "event", "event": "item.created"}, "actions": [{"type": "comment", "body": "x"}]
    })).await.unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);
    // The owner's rule transitions the item through ctx.call.
    let b = t
        .run("automation.create", json!({
            "space_id": space, "name": "auto start",
            "trigger": {"kind": "event", "event": "item.assigned", "cnql": "statusCategory = \"To Do\""},
            "actions": [{"type": "transition", "to": "In Progress"}, {"type": "command", "name": "item.update", "input": {"key": "{{item.key}}", "labels": ["auto"]}}]
        }))
        .await;
    let since = t.last_event().await;
    t.item(space, "work", json!({})).await;
    t.run(
        "item.assign",
        json!({"key": "ENG-1", "user_id": t.owner.user_id}),
    )
    .await;
    t.dispatch(since).await;
    let item = t.run("item.get", json!({"key": "ENG-1"})).await;
    assert_eq!(item["status"], "In Progress");
    assert_eq!(item["labels"], json!(["auto"]));
    assert_eq!(t.runs(&b["id"]).await[0]["status"], "success");
    let ev = t.events_of(&item, "item.transitioned").await;
    assert_eq!(
        ev[0].0, "system",
        "automation changes are attributed to the system, not a person"
    );
    // Machines cannot author automations (they would run unattended without tool-group limits).
    let agent = t.run("agent.create", json!({"name": "bot"})).await;
    let agent_p =
        cnote_core::auth::principal_from_api_key(&t.pool, agent["api_key"].as_str().unwrap())
            .await
            .unwrap()
            .unwrap();
    let e = t
        .try_as(&agent_p, "automation.create", json!({
            "space_id": space, "name": "bot rule", "trigger": {"kind": "event", "event": "item.created"},
            "actions": [{"type": "comment", "body": "x"}]
        }))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Forbidden);
    assert_eq!(
        t.run("workflow.get", json!({"space_id": space})).await["version"],
        1
    );
}

#[tokio::test]
async fn scheduled_automations_sweep() {
    let t = setup().await;
    let space = t.space("ENG").await;
    t.item(space, "stale", json!({"priority": "low"})).await;
    t.item(space, "hot", json!({"priority": "urgent"})).await;
    let a = t
        .run("automation.create", json!({
            "space_id": space, "name": "nag urgent",
            "trigger": {"kind": "schedule", "every_minutes": 60, "cnql": "priority = urgent AND statusCategory != Done"},
            "actions": [{"type": "comment", "body": "still urgent: {{item.key}}"}]
        }))
        .await;
    assert_eq!(
        automation::sweep_workspace(&t.pool, &t.reg, t.ws)
            .await
            .unwrap(),
        1
    );
    assert_eq!(
        automation::sweep_workspace(&t.pool, &t.reg, t.ws)
            .await
            .unwrap(),
        0,
        "not due again yet"
    );
    let runs = t.runs(&a["id"]).await;
    assert_eq!(
        (runs.len(), runs[0]["status"].as_str()),
        (1, Some("success"))
    );
    let hot = t.run("item.get", json!({"key": "ENG-2"})).await;
    let c = t.run("comment.list", json!({"note_id": hot["id"]})).await;
    assert_eq!(c[0]["body"], "still urgent: ENG-2");
    let stale = t.run("item.get", json!({"key": "ENG-1"})).await;
    assert!(
        t.run("comment.list", json!({"note_id": stale["id"]}))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
    assert_eq!(statuses(&t.run("item.list", json!({})).await).len(), 2);
}
