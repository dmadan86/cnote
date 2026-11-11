//! Template rendering against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use cnote_core::{ErrorCode, ExecOptions, Principal, Registry, Role, auth};
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
        let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
            .await
            .unwrap();
        let reg = Registry::new(vec![
            cnote_core::module(),
            cnote_docs::module(),
            cnote_finance::module(),
        ]);
        Self { pool, reg }
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
        self.run(p, cmd, input)
            .await
            .unwrap_or_else(|e| panic!("{cmd}: {e}"))
    }
    async fn user(&self) -> Uuid {
        auth::sign_up(
            &self.pool,
            &format!("{}@test.local", Uuid::new_v4()),
            "Tess",
            &Uuid::new_v4().to_string(),
        )
        .await
        .unwrap()
    }
    async fn tenant(&self) -> (Uuid, Principal) {
        let u = self.user().await;
        let ws = auth::create_workspace(&self.pool, &self.reg, u, "Acme")
            .await
            .unwrap();
        (ws, auth::user_principal(&self.pool, u, ws).await.unwrap())
    }
    async fn member(&self, ws: Uuid) -> Principal {
        let u = self.user().await;
        let mut tx = cnote_db::begin_tenant(&self.pool, ws).await.unwrap();
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
    async fn space(&self, p: &Principal, key: &str, visibility: &str) -> Value {
        self.ok(
            p,
            "space.create",
            json!({"key": key, "name": key, "visibility": visibility}),
        )
        .await["id"]
            .clone()
    }
    async fn template_id(&self, p: &Principal, name: &str) -> Value {
        let l = self.ok(p, "template.list", json!({})).await;
        l.as_array()
            .unwrap()
            .iter()
            .find(|t| t["name"] == name)
            .unwrap_or_else(|| panic!("no template {name}"))["id"]
            .clone()
    }
    /// Create a note with the body and save it as a template.
    async fn save(
        &self,
        p: &Principal,
        space: &Value,
        name: &str,
        kind: &str,
        body: &str,
        types: Value,
    ) -> Value {
        let n = self
            .ok(
                p,
                "note.create",
                json!({"space_id": space, "title": format!("{name} {{{{contact.name}}}}"), "content_md": body, "types": types}),
            )
            .await;
        self.ok(
            p,
            "template.save",
            json!({"note_id": n["id"], "name": name, "source_kind": kind}),
        )
        .await["id"]
            .clone()
    }
}

#[tokio::test]
async fn renders_from_contact_deal_and_invoice_with_indian_money() {
    let t = T::new().await;
    let (_, p) = t.tenant().await;
    let space = t.space(&p, "CRM", "open").await;
    let contact = t
        .ok(
            &p,
            "contact.create",
            json!({"space_id": space, "name": "Asha Rao", "email": "asha@example.test"}),
        )
        .await;
    let deal = t
        .ok(
            &p,
            "deal.create",
            json!({"space_id": space, "title": "Website", "contact_id": contact["id"], "amount_minor": 123_456_700_i64}),
        )
        .await;
    let inv = t
        .ok(
            &p,
            "invoice.create_draft",
            json!({"space_id": space, "contact_id": contact["id"], "lines": [{"description": "Work", "unit_price_minor": 100_000}]}),
        )
        .await;

    // Seeded by the workspace hook.
    let proposal = t.template_id(&p, "Proposal").await;
    let cover = t.template_id(&p, "Invoice cover letter").await;

    let r = t
        .ok(
            &p,
            "template.render",
            json!({"template_id": proposal, "source_id": deal["id"]}),
        )
        .await;
    let md = r["note"]["content_md"].as_str().unwrap();
    assert!(
        md.contains("Proposal for Asha Rao") && md.contains("₹12,34,567.00"),
        "{md}"
    );
    assert_eq!(r["note"]["title"], "Proposal: Website");
    assert_eq!(
        r["note"]["space_id"], space,
        "defaults to the source's space"
    );
    assert_eq!(r["unresolved"], json!([]));

    let r = t
        .ok(
            &p,
            "template.render",
            json!({"template_id": cover, "source_id": inv["id"]}),
        )
        .await;
    let md = r["note"]["content_md"].as_str().unwrap();
    assert!(
        md.contains("invoice DRAFT") && md.contains("Dear Asha Rao") && md.contains('₹'),
        "{md}"
    );

    // A contact template using every catalogue field leaves nothing unresolved.
    let fields = t
        .ok(&p, "template.fields", json!({"source_kind": "contact"}))
        .await;
    let body: String = fields
        .as_array()
        .unwrap()
        .iter()
        .map(|f| format!("{{{{{}}}}}\n", f["path"].as_str().unwrap()))
        .collect();
    let tid = t
        .save(
            &p,
            &space,
            "All contact fields",
            "contact",
            &format!("{body}{{{{made.up}}}} {{{{1+1}}}}"),
            json!([]),
        )
        .await;
    let r = t
        .ok(
            &p,
            "template.render",
            json!({"template_id": tid, "source_id": contact["id"]}),
        )
        .await;
    assert_eq!(r["unresolved"], json!(["made.up"]));
    assert!(
        r["note"]["content_md"]
            .as_str()
            .unwrap()
            .contains("{{made.up}}")
    );
    assert_eq!(r["note"]["title"], "All contact fields Asha Rao");
}

#[tokio::test]
async fn refresh_rerenders_pages_and_boards_are_one_shot() {
    let t = T::new().await;
    let (_, p) = t.tenant().await;
    let space = t.space(&p, "CRM", "open").await;
    let contact = t
        .ok(
            &p,
            "contact.create",
            json!({"space_id": space, "name": "Old Name"}),
        )
        .await;
    let tid = t
        .save(
            &p,
            &space,
            "Hello",
            "contact",
            "Hello {{contact.name}}",
            json!([]),
        )
        .await;
    let r = t
        .ok(
            &p,
            "template.render",
            json!({"template_id": tid, "source_id": contact["id"]}),
        )
        .await;
    let note = r["note"]["id"].clone();
    t.ok(
        &p,
        "contact.update",
        json!({"id": contact["id"], "name": "New Name"}),
    )
    .await;
    t.ok(&p, "template.refresh", json!({"note_id": note})).await;
    let n = t.ok(&p, "note.get", json!({"id": note})).await;
    assert_eq!(n["content_md"], "Hello New Name");

    let snap = "```tldraw\n{\"store\":{\"a\":{\"text\":\"For {{contact.name}}\"}}}\n```";
    let bid = t
        .save(&p, &space, "Board", "contact", snap, json!(["whiteboard"]))
        .await;
    let r = t
        .ok(
            &p,
            "template.render",
            json!({"template_id": bid, "source_id": contact["id"]}),
        )
        .await;
    assert_eq!(r["note"]["types"], json!(["whiteboard"]));
    assert!(
        r["note"]["content_md"]
            .as_str()
            .unwrap()
            .contains("For New Name")
    );
    let e = t
        .run(&p, "template.refresh", json!({"note_id": r["note"]["id"]}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);

    t.ok(&p, "template.delete", json!({"template_id": tid}))
        .await;
    let e = t
        .run(&p, "template.refresh", json!({"note_id": note}))
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);
}

#[tokio::test]
async fn render_is_access_checked() {
    let t = T::new().await;
    let (ws, owner) = t.tenant().await;
    let member = t.member(ws).await;
    let open = t.space(&owner, "OPEN", "open").await;
    let private = t.space(&owner, "SEC", "private").await;
    let secret = t
        .ok(
            &owner,
            "contact.create",
            json!({"space_id": private, "name": "Secret Co"}),
        )
        .await;
    let public = t
        .ok(
            &owner,
            "contact.create",
            json!({"space_id": open, "name": "Public Co"}),
        )
        .await;
    let deal = t
        .ok(
            &owner,
            "deal.create",
            json!({"space_id": open, "title": "D", "contact_id": secret["id"]}),
        )
        .await;
    let tid = t
        .save(
            &owner,
            &open,
            "Contact card",
            "contact",
            "{{contact.name}}",
            json!([]),
        )
        .await;

    // Member cannot render from a contact in a private space.
    let e = t
        .run(
            &member,
            "template.render",
            json!({"template_id": tid, "source_id": secret["id"], "space_id": open}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
    t.ok(
        &member,
        "template.render",
        json!({"template_id": tid, "source_id": public["id"]}),
    )
    .await;

    // A deal the member can read does not leak the private contact linked to it.
    let proposal = t.template_id(&member, "Proposal").await;
    let r = t
        .ok(
            &member,
            "template.render",
            json!({"template_id": proposal, "source_id": deal["id"]}),
        )
        .await;
    let md = r["note"]["content_md"].as_str().unwrap();
    assert!(
        !md.contains("Secret Co") && md.contains("{{contact.name}}"),
        "{md}"
    );
    assert!(
        r["unresolved"]
            .as_array()
            .unwrap()
            .contains(&json!("contact.name"))
    );

    // Wrong kind of record.
    let e = t
        .run(
            &owner,
            "template.render",
            json!({"template_id": tid, "source_id": deal["id"]}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Invalid);

    // Another workspace sees neither the template nor the records.
    let (_, other) = t.tenant().await;
    let e = t
        .run(
            &other,
            "template.render",
            json!({"template_id": tid, "source_id": public["id"]}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::NotFound);
    let own_space = t.space(&other, "X", "open").await;
    let own_tid = t
        .save(
            &other,
            &own_space,
            "Mine",
            "contact",
            "{{contact.name}}",
            json!([]),
        )
        .await;
    assert!(
        t.run(
            &other,
            "template.render",
            json!({"template_id": own_tid, "source_id": public["id"]})
        )
        .await
        .is_err()
    );
}

#[tokio::test]
async fn templates_from_private_spaces_stay_private() {
    let t = T::new().await;
    let (ws, owner) = t.tenant().await;
    let member = t.member(ws).await;
    let private = t.space(&owner, "HUSH", "private").await;
    let tid = t
        .save(
            &owner,
            &private,
            "Board pack",
            "none",
            "salary bands",
            json!([]),
        )
        .await;
    let names = |l: Value| {
        l.as_array()
            .unwrap()
            .iter()
            .map(|x| x["name"].as_str().unwrap().to_string())
            .collect::<Vec<_>>()
    };
    assert!(names(t.ok(&owner, "template.list", json!({})).await).contains(&"Board pack".into()));
    assert!(!names(t.ok(&member, "template.list", json!({})).await).contains(&"Board pack".into()));
    let open = t.space(&owner, "OPN", "open").await;
    for (cmd, input) in [
        (
            "template.render",
            json!({"template_id": tid, "space_id": open}),
        ),
        ("template.delete", json!({"template_id": tid})),
    ] {
        assert!(t.run(&member, cmd, input).await.is_err(), "{cmd} must fail");
    }
    // Same name from an open page must not overwrite the private template.
    let n = t
        .ok(
            &member,
            "note.create",
            json!({"space_id": open, "title": "x", "content_md": "mine"}),
        )
        .await;
    let e = t
        .run(
            &member,
            "template.save",
            json!({"note_id": n["id"], "name": "Board pack", "source_kind": "none"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
}

#[tokio::test]
async fn only_author_or_admin_can_overwrite_or_delete() {
    let t = T::new().await;
    let (ws, owner) = t.tenant().await;
    let a = t.member(ws).await;
    let b = t.member(ws).await;
    let open = t.space(&owner, "ALL", "open").await;
    let starter = t.template_id(&a, "Proposal").await;
    assert!(
        t.run(&a, "template.delete", json!({"template_id": starter}))
            .await
            .is_err()
    );
    let mine = t.save(&a, &open, "A's card", "none", "v1", json!([])).await;
    assert!(
        t.run(&b, "template.delete", json!({"template_id": mine}))
            .await
            .is_err()
    );
    let n = t
        .ok(
            &b,
            "note.create",
            json!({"space_id": open, "title": "x", "content_md": "hijack"}),
        )
        .await;
    let e = t
        .run(
            &b,
            "template.save",
            json!({"note_id": n["id"], "name": "A's card", "source_kind": "none"}),
        )
        .await
        .unwrap_err();
    assert_eq!(e.code, ErrorCode::Conflict);
    // The author can replace it; an admin can delete anything.
    t.save(&a, &open, "A's card", "none", "v2", json!([])).await;
    t.ok(&owner, "template.delete", json!({"template_id": starter}))
        .await;
    t.ok(
        &a,
        "template.delete",
        json!({"template_id": t.template_id(&a, "A's card").await}),
    )
    .await;
}
