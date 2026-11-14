//! Page restrictions and sharing (migration 0800) against a real Postgres. Run `scripts/setup-db.sh` first.

use cnote_core::auth;
use cnote_core::{ErrorCode, ExecOptions, Principal, Registry, Role};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

struct W {
    pool: PgPool,
    reg: Arc<Registry>,
    owner: Principal,
}

impl W {
    async fn new() -> Self {
        let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
        let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
            .await
            .unwrap();
        let reg = Registry::new(vec![cnote_core::module()]);
        let user = auth::sign_up(
            &pool,
            &format!("{}@test.local", Uuid::new_v4()),
            "Olive",
            &Uuid::new_v4().to_string(),
        )
        .await
        .unwrap();
        let ws = auth::create_workspace(&pool, &reg, user, "Acme")
            .await
            .unwrap();
        let owner = auth::user_principal(&pool, user, ws).await.unwrap();
        Self { pool, reg, owner }
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

    /// Add a workspace member with the given role; returns (principal, email).
    async fn member(&self, role: &str) -> (Principal, String) {
        let email = format!("{}@t.local", Uuid::new_v4());
        let m = self
            .ok(
                &self.owner,
                "member.add",
                json!({"email": email, "role": role}),
            )
            .await;
        let id: Uuid = m["user_id"].as_str().unwrap().parse().unwrap();
        (
            Principal::user(id, self.owner.workspace_id, Role::parse(role)),
            email,
        )
    }

    async fn space(&self, key: &str, visibility: &str) -> Uuid {
        let s = self
            .ok(
                &self.owner,
                "space.create",
                json!({"key": key, "name": key, "visibility": visibility}),
            )
            .await;
        s["id"].as_str().unwrap().parse().unwrap()
    }

    async fn page(&self, space: Uuid, title: &str, parent: Option<Uuid>, body: &str) -> Uuid {
        let n = self
            .ok(
                &self.owner,
                "note.create",
                json!({"space_id": space, "title": title, "parent_id": parent, "content_md": body}),
            )
            .await;
        n["id"].as_str().unwrap().parse().unwrap()
    }

    fn uid(p: &Principal) -> Uuid {
        p.user_id.unwrap()
    }
}

fn ids(v: &Value) -> Vec<String> {
    v.as_array()
        .unwrap()
        .iter()
        .map(|n| n["id"].as_str().unwrap().to_string())
        .collect()
}

#[tokio::test]
async fn view_restriction_hides_the_page_everywhere_and_children_inherit() {
    let w = W::new().await;
    let (bob, _) = w.member("member").await;
    let sp = w.space("VR", "open").await;
    let secret = w.page(sp, "Layoff plan", None, "zebrafish budget").await;
    let child = w
        .page(sp, "Layoff child", Some(secret), "zebrafish details")
        .await;
    let open = w.page(sp, "Lunch menu", None, "zebrafish tacos").await;
    w.ok(
        &w.owner,
        "comment.add",
        json!({"note_id": secret, "body": "hush"}),
    )
    .await;
    w.ok(
        &w.owner,
        "relation.add",
        json!({"from_id": open, "to_id": secret, "kind": "relates"}),
    )
    .await;

    // Before restricting, Bob sees everything.
    assert_eq!(
        ids(&w.ok(&bob, "note.list", json!({"space_id": sp})).await).len(),
        3
    );

    w.ok(
        &w.owner,
        "note.restrict",
        json!({"note_id": secret, "restriction": "view"}),
    )
    .await;

    for id in [secret, child] {
        assert_eq!(
            w.err(&bob, "note.get", json!({"id": id})).await,
            ErrorCode::NotFound
        );
        assert_eq!(
            w.err(&bob, "comment.list", json!({"note_id": id})).await,
            ErrorCode::NotFound
        );
        assert_eq!(
            w.err(&bob, "note.history", json!({"id": id})).await,
            ErrorCode::NotFound
        );
        assert_eq!(
            w.err(&bob, "note.update", json!({"id": id, "title": "x"}))
                .await,
            ErrorCode::NotFound
        );
        assert_eq!(
            w.err(&bob, "comment.add", json!({"note_id": id, "body": "x"}))
                .await,
            ErrorCode::NotFound
        );
    }
    let list = ids(&w.ok(&bob, "note.list", json!({"space_id": sp})).await);
    assert_eq!(list, vec![open.to_string()]);
    // Listing children of the hidden page returns nothing either.
    assert!(ids(&w.ok(&bob, "note.list", json!({"parent_id": secret})).await).is_empty());
    let hits = w.ok(&bob, "search.notes", json!({"q": "zebrafish"})).await;
    assert_eq!(ids(&hits), vec![open.to_string()]);
    // A relation from a readable page does not reveal the hidden page's title.
    let detail = w.ok(&bob, "note.get", json!({"id": open})).await;
    assert!(detail["relations"].as_array().unwrap().is_empty());
    // Cannot hang a new child under it or move a page under it.
    assert_eq!(
        w.err(
            &bob,
            "note.create",
            json!({"space_id": sp, "title": "t", "parent_id": secret})
        )
        .await,
        ErrorCode::NotFound
    );
    assert_eq!(
        w.err(
            &bob,
            "note.update",
            json!({"id": open, "parent_id": secret})
        )
        .await,
        ErrorCode::NotFound
    );
    assert_eq!(
        w.err(&bob, "note.permissions", json!({"note_id": secret}))
            .await,
        ErrorCode::NotFound
    );
    assert_eq!(
        w.err(
            &bob,
            "note.share",
            json!({"note_id": secret, "user_id": W::uid(&bob), "level": "edit"})
        )
        .await,
        ErrorCode::NotFound
    );

    // The owner (listed as editor) still sees all of it.
    assert_eq!(
        ids(&w.ok(&w.owner, "note.list", json!({"space_id": sp})).await).len(),
        3
    );
}

#[tokio::test]
async fn edit_restriction_allows_reading_but_blocks_writing() {
    let w = W::new().await;
    let (bob, bob_email) = w.member("member").await;
    let sp = w.space("ER", "open").await;
    let page = w.page(sp, "Policy", None, "rules").await;
    let child = w.page(sp, "Policy FAQ", Some(page), "faq").await;
    w.ok(
        &w.owner,
        "note.restrict",
        json!({"note_id": page, "restriction": "edit"}),
    )
    .await;

    w.ok(&bob, "note.get", json!({"id": page})).await;
    assert_eq!(
        w.err(&bob, "note.update", json!({"id": page, "title": "x"}))
            .await,
        ErrorCode::Forbidden
    );
    // Inherited by the child.
    assert_eq!(
        w.err(&bob, "note.update", json!({"id": child, "title": "x"}))
            .await,
        ErrorCode::Forbidden
    );
    assert_eq!(
        w.err(&bob, "note.delete", json!({"id": child})).await,
        ErrorCode::Forbidden
    );
    // Commenting is reading, not editing.
    w.ok(
        &bob,
        "comment.add",
        json!({"note_id": page, "body": "question"}),
    )
    .await;
    // Bob cannot change the restriction or sharing either.
    assert_eq!(
        w.err(
            &bob,
            "note.restrict",
            json!({"note_id": page, "restriction": "none"})
        )
        .await,
        ErrorCode::Forbidden
    );
    let perms = w
        .ok(&bob, "note.permissions", json!({"note_id": child}))
        .await;
    assert_eq!(perms["can_manage"], json!(false));
    assert_eq!(perms["inherited_from"]["id"], json!(page));

    // Sharing as editor lets him write.
    w.ok(
        &w.owner,
        "note.share",
        json!({"note_id": page, "email": bob_email.to_uppercase(), "level": "edit"}),
    )
    .await;
    w.ok(&bob, "note.update", json!({"id": child, "title": "FAQ v2"}))
        .await;
}

#[tokio::test]
async fn share_into_private_space_grants_only_that_page_and_its_children() {
    let w = W::new().await;
    let (bob, _) = w.member("member").await;
    let (guest, guest_email) = w.member("guest").await;
    let (carol, _) = w.member("member").await;
    let sp = w.space("PR", "private").await;
    let shared = w.page(sp, "Roadmap", None, "quokka plan").await;
    let kid = w.page(sp, "Roadmap Q3", Some(shared), "quokka q3").await;
    let other = w.page(sp, "Payroll", None, "quokka salaries").await;

    for p in [&bob, &guest] {
        assert_eq!(
            w.err(p, "note.get", json!({"id": shared})).await,
            ErrorCode::NotFound
        );
        assert!(
            w.ok(p, "note.shared_with_me", json!({}))
                .await
                .as_array()
                .unwrap()
                .is_empty()
        );
    }
    w.ok(
        &w.owner,
        "note.share",
        json!({"note_id": shared, "user_id": W::uid(&bob), "level": "view"}),
    )
    .await;
    w.ok(
        &w.owner,
        "note.share",
        json!({"note_id": shared, "email": guest_email, "level": "view"}),
    )
    .await;
    w.ok(
        &w.owner,
        "note.share",
        json!({"note_id": shared, "user_id": W::uid(&carol), "level": "edit"}),
    )
    .await;

    for p in [&bob, &guest, &carol] {
        w.ok(p, "note.get", json!({"id": shared})).await;
        w.ok(p, "note.get", json!({"id": kid})).await;
        assert_eq!(
            w.err(p, "note.get", json!({"id": other})).await,
            ErrorCode::NotFound
        );
        assert!(ids(&w.ok(p, "note.list", json!({"space_id": sp})).await).is_empty());
        let mine = w.ok(p, "note.shared_with_me", json!({})).await;
        assert_eq!(ids(&mine), vec![shared.to_string()]);
        // The shared page's children are listed through the parent, nothing else.
        assert_eq!(
            ids(&w.ok(p, "note.list", json!({"parent_id": shared})).await),
            vec![kid.to_string()]
        );
        assert_eq!(
            ids(&w.ok(p, "search.notes", json!({"q": "quokka"})).await),
            vec![shared.to_string()]
        );
        assert!(!ids(&w.ok(p, "space.list", json!({})).await).contains(&sp.to_string()));
        assert!(
            w.run(p, "note.create", json!({"space_id": sp, "title": "x"}))
                .await
                .is_err()
        );
    }
    // View level cannot write, edit level can (without any access to the space itself).
    assert_eq!(
        w.err(&bob, "note.update", json!({"id": kid, "title": "x"}))
            .await,
        ErrorCode::Forbidden
    );
    w.ok(
        &carol,
        "note.update",
        json!({"id": kid, "title": "edited by carol"}),
    )
    .await;
    assert_eq!(
        w.err(&carol, "note.update", json!({"id": other, "title": "x"}))
            .await,
        ErrorCode::NotFound
    );

    // Unshare revokes immediately.
    w.ok(
        &w.owner,
        "note.unshare",
        json!({"note_id": shared, "user_id": W::uid(&bob)}),
    )
    .await;
    assert_eq!(
        w.err(&bob, "note.get", json!({"id": kid})).await,
        ErrorCode::NotFound
    );
    assert!(
        w.ok(&bob, "note.shared_with_me", json!({}))
            .await
            .as_array()
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn share_rules_lockout_membership_and_admin_bypass() {
    let w = W::new().await;
    let (alice, _) = w.member("member").await;
    let (admin, _) = w.member("admin").await;
    let sp = w.space("LK", "open").await;
    let page = w.page(sp, "Plan", None, "alpaca").await;

    // Alice restricting a view page keeps herself in as editor; so does the owner.
    w.ok(
        &alice,
        "note.restrict",
        json!({"note_id": page, "restriction": "view"}),
    )
    .await;
    let perms = w
        .ok(&alice, "note.permissions", json!({"note_id": page}))
        .await;
    assert_eq!(perms["restriction"], json!("view"));
    assert_eq!(perms["people"][0]["level"], json!("edit"));
    assert_eq!(perms["can_manage"], json!(true));

    // The owner is a plain member of the space but not listed: not an admin bypass? Owners are admins.
    w.ok(&w.owner, "note.get", json!({"id": page})).await;
    w.ok(
        &admin,
        "note.update",
        json!({"id": page, "title": "admin edit"}),
    )
    .await;

    // Last editor cannot be removed or downgraded while restricted.
    assert_eq!(
        w.err(
            &alice,
            "note.unshare",
            json!({"note_id": page, "user_id": W::uid(&alice)})
        )
        .await,
        ErrorCode::Conflict
    );
    assert_eq!(
        w.err(
            &alice,
            "note.share",
            json!({"note_id": page, "user_id": W::uid(&alice), "level": "view"})
        )
        .await,
        ErrorCode::Conflict
    );
    // Rolled back: still editor.
    w.ok(
        &alice,
        "note.update",
        json!({"id": page, "title": "still mine"}),
    )
    .await;

    // Unknown email and non-members are rejected.
    assert_eq!(
        w.err(
            &alice,
            "note.share",
            json!({"note_id": page, "email": "nobody@t.local", "level": "view"})
        )
        .await,
        ErrorCode::Invalid
    );
    let stranger = auth::sign_up(
        &w.pool,
        &format!("{}@t.local", Uuid::new_v4()),
        "S",
        &Uuid::new_v4().to_string(),
    )
    .await
    .unwrap();
    assert_eq!(
        w.err(
            &alice,
            "note.share",
            json!({"note_id": page, "user_id": stranger, "level": "view"})
        )
        .await,
        ErrorCode::Invalid
    );
    assert_eq!(
        w.err(
            &alice,
            "note.restrict",
            json!({"note_id": page, "restriction": "bogus"})
        )
        .await,
        ErrorCode::Invalid
    );

    // Lifting the restriction opens the page again (and lets the last editor go).
    w.ok(
        &alice,
        "note.restrict",
        json!({"note_id": page, "restriction": "none"}),
    )
    .await;
    let (bob, _) = w.member("member").await;
    w.ok(
        &bob,
        "note.update",
        json!({"id": page, "title": "open again"}),
    )
    .await;
}

#[tokio::test]
async fn raw_access_function_and_saved_views() {
    let w = W::new().await;
    let (bob, _) = w.member("member").await;
    let sp = w.space("AG", "open").await;
    let page = w.page(sp, "Salary bands", None, "wombat").await;
    w.ok(
        &w.owner,
        "note.restrict",
        json!({"note_id": page, "restriction": "view"}),
    )
    .await;
    let views = w.ok(&bob, "view.list", json!({})).await;
    assert!(views.as_array().unwrap().is_empty());
    // Raw function check: no uid means no access except for admins.
    let mut tx = cnote_db::begin_tenant(&w.pool, w.owner.workspace_id)
        .await
        .unwrap();
    let lvl: String =
        sqlx::query_scalar("select core.note_access($1, $2, false, array[$3]::uuid[])")
            .bind(page)
            .bind(W::uid(&bob))
            .bind(sp)
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    assert_eq!(lvl, "none");
    let lvl: String = sqlx::query_scalar("select core.note_access($1, null, true, '{}')")
        .bind(page)
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert_eq!(lvl, "edit");
}

#[tokio::test]
async fn mentions_notify_once_and_only_people_who_can_read() {
    let w = W::new().await;
    let (bob, _) = w.member("member").await;
    let (carol, _) = w.member("member").await;
    let sp = w.space("MN", "open").await;
    let page = w.page(sp, "Plan", None, "").await;
    let ws = w.owner.workspace_id;
    let link = |p: &Principal| format!("[@x](/w/{ws}/people/{})", W::uid(p));
    let count = |p: &Principal| {
        let p = p.clone();
        let w = &w;
        async move {
            w.ok(&p, "notification.unread_count", json!({})).await["count"]
                .as_i64()
                .unwrap()
        }
    };
    let save = |body: String| {
        w.ok(
            &w.owner,
            "note.update",
            json!({"id": page, "content_md": body}),
        )
    };

    // Bob once (re-saving does not duplicate); the editor mentioning themself gets nothing.
    let body = format!("hi {} and {}", link(&bob), link(&w.owner));
    save(body.clone()).await;
    save(format!("{body} more")).await;
    assert_eq!(count(&bob).await, 1);
    assert_eq!(count(&w.owner).await, 0);
    let list = w.ok(&bob, "notification.list", json!({})).await;
    assert_eq!(list[0]["note_title"], "Plan");

    // Carol cannot read a view-restricted page: no notification.
    w.ok(
        &w.owner,
        "note.restrict",
        json!({"note_id": page, "restriction": "view"}),
    )
    .await;
    save(format!("{body} {}", link(&carol))).await;
    assert_eq!(count(&carol).await, 0);

    // Marking read clears the badge.
    w.ok(&bob, "notification.read", json!({})).await;
    assert_eq!(count(&bob).await, 0);
}

#[tokio::test]
async fn inline_comments_reply_resolve_and_respect_access() {
    let w = W::new().await;
    let (bob, _) = w.member("member").await;
    let sp = w.space("IC", "open").await;
    let page = w.page(sp, "Plan", None, "We ship on Friday.").await;

    let c = w
        .ok(&bob, "comment.add", json!({"note_id": page, "body": "Which Friday?", "anchor_text": "Friday", "anchor_prefix": "We ship on "}))
        .await;
    assert_eq!(c["anchor_text"], "Friday");
    let mention = format!(
        "The 20th, [@Bob](/w/{}/people/{})",
        w.owner.workspace_id,
        W::uid(&bob)
    );
    let reply = w
        .ok(
            &w.owner,
            "comment.add",
            json!({"note_id": page, "body": mention, "parent_id": c["id"]}),
        )
        .await;
    assert_eq!(reply["parent_id"], c["id"]);
    let n = w.ok(&bob, "notification.unread_count", json!({})).await;
    assert_eq!(
        n["count"], 1,
        "a mention in a comment notifies like one in the page"
    );
    // Replies are one level deep and never anchored.
    assert_eq!(
        w.err(
            &bob,
            "comment.add",
            json!({"note_id": page, "body": "x", "parent_id": reply["id"]})
        )
        .await,
        ErrorCode::Invalid
    );
    assert_eq!(
        w.err(
            &bob,
            "comment.add",
            json!({"note_id": page, "body": "x", "parent_id": c["id"], "anchor_text": "ship"})
        )
        .await,
        ErrorCode::Invalid
    );
    assert_eq!(
        w.err(
            &bob,
            "comment.add",
            json!({"note_id": page, "body": "x", "anchor_text": "y".repeat(501)})
        )
        .await,
        ErrorCode::Invalid
    );

    let r = w.ok(&bob, "comment.resolve", json!({"id": c["id"]})).await;
    assert!(r["resolved_at"].is_string());
    assert!(r["resolved_by_name"].is_string());
    let r = w
        .ok(
            &w.owner,
            "comment.resolve",
            json!({"id": c["id"], "resolved": false}),
        )
        .await;
    assert!(r["resolved_at"].is_null());
    assert_eq!(
        w.err(&bob, "comment.resolve", json!({"id": reply["id"]}))
            .await,
        ErrorCode::NotFound
    );

    // Once the page is restricted, Bob can no longer resolve its threads.
    w.ok(
        &w.owner,
        "note.restrict",
        json!({"note_id": page, "restriction": "view"}),
    )
    .await;
    assert_eq!(
        w.err(&bob, "comment.resolve", json!({"id": c["id"]})).await,
        ErrorCode::NotFound
    );
    let all = w
        .ok(&w.owner, "comment.list", json!({"note_id": page}))
        .await;
    assert_eq!(all.as_array().unwrap().len(), 2);
}

#[tokio::test]
async fn guest_shared_as_editor_can_save_the_page() {
    let w = W::new().await;
    let (guest, guest_email) = w.member("guest").await;
    for vis in ["open", "private"] {
        let sp = w.space(if vis == "open" { "GO" } else { "GP" }, vis).await;
        let page = w.page(sp, "Brief", None, "draft").await;
        w.ok(
            &w.owner,
            "note.share",
            json!({"note_id": page, "email": guest_email, "level": "edit"}),
        )
        .await;
        let got = w.ok(&guest, "note.get", json!({"id": page})).await;
        assert_eq!(got["level"], json!("edit"), "{vis}");
        w.ok(
            &guest,
            "note.update",
            json!({"id": page, "title": "Brief v2", "content_md": "guest wrote this"}),
        )
        .await;
        w.ok(
            &guest,
            "comment.add",
            json!({"note_id": page, "body": "looks good"}),
        )
        .await;
        // Not shared, or shared view-only: still no write.
        let other = w.page(sp, "Other", None, "x").await;
        assert_eq!(
            w.err(&guest, "note.update", json!({"id": other, "title": "x"}))
                .await,
            ErrorCode::NotFound,
            "{vis}"
        );
        w.ok(
            &w.owner,
            "note.share",
            json!({"note_id": other, "email": guest_email, "level": "view"}),
        )
        .await;
        assert_eq!(
            w.err(&guest, "note.update", json!({"id": other, "title": "x"}))
                .await,
            ErrorCode::Forbidden,
            "{vis}"
        );
    }
}

#[tokio::test]
async fn moves_cannot_create_cycles_or_cross_spaces() {
    let w = W::new().await;
    let a = w.space("MA", "open").await;
    let b = w.space("MB", "open").await;
    let top = w.page(a, "Top", None, "x").await;
    let mid = w.page(a, "Mid", Some(top), "x").await;
    let leaf = w.page(a, "Leaf", Some(mid), "x").await;
    let other = w.page(b, "Elsewhere", None, "x").await;
    for (id, parent) in [(top, top), (top, leaf), (mid, leaf), (top, other)] {
        assert_eq!(
            w.err(
                &w.owner,
                "note.update",
                json!({"id": id, "parent_id": parent})
            )
            .await,
            ErrorCode::Invalid,
            "{id} under {parent}"
        );
    }
    assert!(
        w.run(
            &w.owner,
            "note.create",
            json!({"space_id": b, "title": "x", "parent_id": top})
        )
        .await
        .is_err()
    );
    // A legal move still works.
    w.ok(
        &w.owner,
        "note.update",
        json!({"id": leaf, "parent_id": top}),
    )
    .await;
}
