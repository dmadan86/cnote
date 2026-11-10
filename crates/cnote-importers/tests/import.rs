//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use cnote_core::auth;
use cnote_core::{Ctx, ErrorCode, ExecOptions, Principal, Registry, Role};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use uuid::Uuid;

const JIRA: &str = include_str!("fixtures/jira_export.json");
const TRELLO: &str = include_str!("fixtures/trello_board.json");
const CONFLUENCE: &str = include_str!("fixtures/confluence_pages.json");
const CSV: &str = include_str!("fixtures/tasks.csv");

struct T {
    pool: PgPool,
    reg: Arc<Registry>,
    ws: Uuid,
    admin: Principal,
    space: Uuid,
    /// Unique stand-ins for the fixture people (users are global, so emails must not repeat across runs).
    ada: (Uuid, String),
}

async fn setup() -> (PgPool, Arc<Registry>) {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
        .await
        .unwrap();
    (
        pool,
        Registry::new(vec![cnote_core::module(), cnote_importers::module()]),
    )
}

async fn add_member(pool: &PgPool, ws: Uuid, name: &str) -> (Uuid, String) {
    let email = format!("{}@example.com", Uuid::new_v4());
    let id = auth::sign_up(pool, &email, name, &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let mut tx = cnote_db::begin_tenant(pool, ws).await.unwrap();
    sqlx::query(
        "insert into core.memberships (workspace_id, user_id, role) values ($1,$2,'member')",
    )
    .bind(ws)
    .bind(id)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    (id, email)
}

async fn tenant() -> T {
    let (pool, reg) = setup().await;
    let email = format!("{}@example.com", Uuid::new_v4());
    let owner = auth::sign_up(&pool, &email, "Owner", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let ws = auth::create_workspace(&pool, &reg, owner, "Acme")
        .await
        .unwrap();
    let admin = auth::user_principal(&pool, owner, ws).await.unwrap();
    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "ENG", "name": "Engineering"}),
            &admin,
            &ExecOptions::default(),
        )
        .await
        .unwrap()["id"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    let ada = add_member(&pool, ws, "Ada").await;
    T {
        pool,
        reg,
        ws,
        admin,
        space,
        ada,
    }
}

impl T {
    fn jira(&self) -> String {
        JIRA.replace("ada@example.com", &self.ada.1)
    }

    async fn exec(&self, name: &str, input: Value) -> Result<Value, cnote_core::CmdError> {
        self.reg
            .execute(
                &self.pool,
                name,
                input,
                &self.admin,
                &ExecOptions::default(),
            )
            .await
    }

    async fn start(&self, source: &str, content: &str, extra: Value) -> Uuid {
        let mut input = json!({"source": source, "space_id": self.space, "content": content});
        input
            .as_object_mut()
            .unwrap()
            .extend(extra.as_object().unwrap().clone());
        let out = self.exec("import.start", input).await.unwrap();
        out["id"].as_str().unwrap().parse().unwrap()
    }

    /// Run the next queued `import.run` batch for this import, the way the worker would. False when none is queued.
    async fn run_batch(&self, import: Uuid) -> bool {
        let mut tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        let job: Option<(i64, Value)> = sqlx::query_as(
            "update core.jobs set status = 'done' where id = (
               select id from core.jobs where kind = 'import.run' and status = 'queued' and payload->>'import_id' = $1
               order by id limit 1) returning id, payload",
        )
        .bind(import.to_string())
        .fetch_optional(&mut *tx)
        .await
        .unwrap();
        let Some((_, payload)) = job else {
            return false;
        };
        let mut ctx = Ctx {
            tx,
            principal: Principal::system(self.ws),
            registry: self.reg.clone(),
        };
        let h = self.reg.job_handlers["import.run"];
        h(&mut ctx, payload).await.unwrap();
        ctx.tx.commit().await.unwrap();
        true
    }

    async fn run_all(&self, import: Uuid) -> usize {
        let mut n = 0;
        while self.run_batch(import).await {
            n += 1;
        }
        n
    }

    async fn status(&self, import: Uuid) -> Value {
        self.exec("import.status", json!({"id": import}))
            .await
            .unwrap()
    }

    async fn q<
        O: for<'r> sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres> + Send + Unpin,
    >(
        &self,
        sql: &'static str,
    ) -> O {
        let mut tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        sqlx::query_scalar(sql).fetch_one(&mut *tx).await.unwrap()
    }
}

#[tokio::test]
async fn jira_import_keeps_identity_history_and_relations() {
    let t = tenant().await;
    let id = t.start("jira", &t.jira(), json!({})).await;
    t.run_all(id).await;
    let st = t.status(id).await;
    assert_eq!(st["status"], "done");
    let r = &st["report"];
    assert_eq!(
        (
            r["imported"].as_u64(),
            r["updated"].as_u64(),
            r["skipped"].as_u64()
        ),
        (Some(3), Some(0), Some(0))
    );
    assert_eq!(r["comments"], 2);
    assert_eq!(r["history_events"], 3);
    assert_eq!(r["attachments"], 1);
    assert_eq!(r["views"], 2);
    let warnings = r["warnings"].as_object().unwrap();
    let has = |needle: &str| warnings.keys().any(|k| k.contains(needle));
    assert!(has("schemes are flattened"), "{warnings:?}");
    assert!(has("unknown field 'customfield_10099'"));
    assert!(has("JQL not translatable for filter 'My open bugs'"));
    assert!(!has("Payments backlog"));
    assert!(has("link to ENG-99 skipped"));
    assert!(has("only the first component"));
    assert!(has("sprint history flattened"));
    assert!(has("dave@example.com is not a workspace member") || has("is not a workspace member"));
    assert!(has("disabled draft"));

    let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
    // Item 2 carries everything.
    let row: (String, String, String, String, Option<Uuid>, Option<Uuid>, Vec<String>, Option<sqlx::types::Uuid>) =
        sqlx::query_as(
            "select i.key, i.item_type, i.status_category, i.priority, i.assignee_id, i.reporter_id, i.labels, i.sprint_id
             from projects.items i join core.notes n on n.id = i.note_id where n.source_system = 'jira' and n.source_id = 'ENG-2'",
        )
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert_eq!(
        (
            row.0.as_str(),
            row.1.as_str(),
            row.2.as_str(),
            row.3.as_str()
        ),
        ("ENG-2", "story", "done", "urgent")
    );
    assert_eq!(row.4, Some(t.ada.0), "assignee matched by email");
    assert_eq!(row.5, None, "bob is not a member");
    assert_eq!(row.6, vec!["payments", "india"]);
    let (sprint, version, component, points): (String, String, String, f64) = sqlx::query_as(
        "select sp.name, v.name, c.name, i.estimate::float8 from projects.items i
         join projects.sprints sp on sp.id = i.sprint_id join projects.versions v on v.id = i.fix_version_id
         join projects.components c on c.id = i.component_id where i.key = 'ENG-2'",
    )
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert_eq!(
        (
            sprint.as_str(),
            version.as_str(),
            component.as_str(),
            points
        ),
        ("Sprint 2", "1.4", "Payments", 5.0)
    );
    let (source_url, created): (String, String) = sqlx::query_as(
        "select source_url, to_char(created_at at time zone 'utc', 'YYYY-MM-DD HH24:MI') from core.notes where source_id = 'ENG-2'",
    )
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert_eq!(source_url, "https://acme.atlassian.net/browse/ENG-2");
    assert_eq!(created, "2024-01-11 04:30", "original timestamp preserved");
    let (custom, prio3): (Value, String) =
        sqlx::query_as("select custom, priority from projects.items where key = 'ENG-3'")
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    assert_eq!(custom["source_assignee"], "Dave Unknown");
    assert_eq!(prio3, "low");

    // History arrives as import events carrying the original author and time.
    let ev: Vec<(String, String, Value)> = sqlx::query_as(
        "select e.kind, e.actor_type, e.payload from core.events e join core.notes n on n.id = e.note_id
         where n.source_id = 'ENG-2' and e.actor_type = 'import' and e.kind = 'item.transitioned' order by e.created_at",
    )
    .fetch_all(&mut *tx)
    .await
    .unwrap();
    assert_eq!(ev.len(), 2);
    assert_eq!(ev[0].2["from"], "To Do");
    assert_eq!(ev[0].2["author"], "Ada Example");
    let first_at: String = sqlx::query_scalar(
        "select to_char(e.created_at at time zone 'utc', 'YYYY-MM-DD HH24:MI') from core.events e join core.notes n on n.id = e.note_id
         where n.source_id = 'ENG-2' and e.kind = 'item.transitioned' order by e.created_at limit 1",
    )
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert_eq!(first_at, "2024-01-20 03:30");
    let comments: Vec<(Option<String>, String)> = sqlx::query_as(
        "select c.author_name, c.body from core.comments c join core.notes n on n.id = c.note_id where n.source_id = 'ENG-2' order by c.created_at",
    )
    .fetch_all(&mut *tx)
    .await
    .unwrap();
    assert_eq!(
        comments[0],
        (Some("Bob Sample".into()), "Which PSP are we using?".into())
    );
    assert_eq!(comments[1].0.as_deref(), Some("Carol Nobody"));
    let key: String = sqlx::query_scalar(
        "select a.storage_key from core.attachments a join core.notes n on n.id = a.note_id where n.source_id = 'ENG-2'",
    )
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert!(key.ends_with("/attachment/content/30001"));

    // Relations: epic -child-> story, story -blocks-> bug; nothing for the missing ENG-99.
    let rels: Vec<(String, String, String)> = sqlx::query_as(
        "select a.source_id, r.kind, b.source_id from core.relations r
         join core.notes a on a.id = r.from_id join core.notes b on b.id = r.to_id order by r.kind",
    )
    .fetch_all(&mut *tx)
    .await
    .unwrap();
    assert_eq!(
        rels,
        vec![
            ("ENG-2".into(), "blocks".into(), "ENG-3".into()),
            ("ENG-1".into(), "child".into(), "ENG-2".into())
        ]
    );
    // Saved filter keeps its JQL; the automation is a disabled draft.
    let jql: String =
        sqlx::query_scalar("select query from core.views where name = 'My open bugs'")
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    assert!(jql.contains("currentUser()"));
    let (draft, enabled): (bool, bool) =
        sqlx::query_as("select draft, enabled from projects.automations")
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    assert!(draft && !enabled);
    let seq: i32 = sqlx::query_scalar("select item_seq from core.spaces where key = 'ENG'")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert_eq!(seq, 3);
}

async fn counts(t: &T) -> [i64; 7] {
    [
        t.q::<i64>("select count(*) from core.notes").await,
        t.q::<i64>("select count(*) from projects.items").await,
        t.q::<i64>("select count(*) from core.comments").await,
        t.q::<i64>("select count(*) from core.events where actor_type = 'import'")
            .await,
        t.q::<i64>("select count(*) from core.relations").await,
        t.q::<i64>("select count(*) from core.attachments").await,
        t.q::<i64>("select count(*) from core.views").await,
    ]
}

#[tokio::test]
async fn rerun_updates_without_duplicating() {
    let t = tenant().await;
    let first = t.start("jira", &t.jira(), json!({})).await;
    t.run_all(first).await;
    let before = counts(&t).await;

    let changed = t
        .jira()
        .replace("Add UPI payment option", "Add UPI and cards")
        .replace(
            "Support UPI collect and intent flows.",
            "Support UPI and card flows.",
        );
    let second = t.start("jira", &changed, json!({})).await;
    t.run_all(second).await;
    let r = &t.status(second).await["report"];
    assert_eq!(
        (r["imported"].as_u64(), r["updated"].as_u64()),
        (Some(0), Some(3))
    );
    assert_eq!(counts(&t).await, before, "no duplicates anywhere");

    let (title, body, key, version): (String, String, String, i32) = {
        let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
        sqlx::query_as(
            "select n.title, n.content_md, i.key, n.version from core.notes n join projects.items i on i.note_id = n.id where n.source_id = 'ENG-2'",
        )
        .fetch_one(&mut *tx)
        .await
        .unwrap()
    };
    assert_eq!(
        (title.as_str(), body.as_str(), key.as_str()),
        ("Add UPI and cards", "Support UPI and card flows.", "ENG-2")
    );
    assert_eq!(version, 2);
}

#[tokio::test]
async fn import_resumes_batch_by_batch() {
    let t = tenant().await;
    let id = t.start("jira", &t.jira(), json!({"batch_size": 2})).await;
    assert!(t.run_batch(id).await);
    // Stop here: one batch done, the job remembers where it was.
    let st = t.status(id).await;
    assert_eq!(st["status"], "running");
    assert_eq!(st["cursor"], json!({"phase": "records", "offset": 2}));
    assert_eq!(t.q::<i64>("select count(*) from projects.items").await, 2);
    assert_eq!(
        t.q::<i64>("select count(*) from core.relations").await,
        0,
        "relations wait for every record"
    );

    // Resume: the re-enqueued job finishes the rest.
    t.run_all(id).await;
    let st = t.status(id).await;
    assert_eq!(st["status"], "done");
    assert_eq!(st["report"]["imported"], 3);
    assert_eq!(t.q::<i64>("select count(*) from projects.items").await, 3);
    assert_eq!(t.q::<i64>("select count(*) from core.relations").await, 2);
    assert!(!t.run_batch(id).await, "nothing left to run");
    assert_eq!(
        t.q::<i64>("select count(*) from core.events where kind = 'import.completed'")
            .await,
        1
    );
}

#[tokio::test]
async fn confluence_and_notion_pages_start_unverified_with_a_flag() {
    let t = tenant().await;
    let id = t.start("confluence", CONFLUENCE, json!({})).await;
    t.run_all(id).await;
    let r = &t.status(id).await["report"];
    assert_eq!(r["imported"], 2);
    assert!(
        r["warnings"]
            .as_object()
            .unwrap()
            .keys()
            .any(|k| k.contains("macro"))
    );
    let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
    let rows: Vec<(String, String, String, Option<Uuid>, bool)> = sqlx::query_as(
        "select n.title, n.freshness, n.content_md, n.parent_id,
           exists(select 1 from docs.freshness_flags f where f.note_id = n.id and f.reason = 'imported' and f.resolved_at is null)
         from core.notes n where n.source_system = 'confluence' order by n.source_id",
    )
    .fetch_all(&mut *tx)
    .await
    .unwrap();
    assert_eq!(rows.len(), 2);
    for (_, freshness, _, _, flagged) in &rows {
        assert_eq!(freshness, "unverified");
        assert!(flagged);
    }
    assert!(
        rows[0].2.contains("- Be kind\n- Ship small")
            && rows[0].2.contains("[policy](https://example.com/policy)")
    );
    assert!(
        rows[1].2.contains("```bash\nmake deploy ENV=prod\n```")
            && rows[1].2.contains("| prod | https://example.com |")
    );
    let parent: Uuid = sqlx::query_scalar("select id from core.notes where source_id = '9001'")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert_eq!(rows[1].3, Some(parent), "hierarchy preserved");
    let verify_by: Option<chrono::NaiveDate> =
        sqlx::query_scalar("select verify_by from core.notes where source_id = '9002'")
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    assert!(verify_by.is_some(), "90-day clock is running");
    drop(tx);

    // Re-running does not add a second flag.
    let again = t.start("confluence", CONFLUENCE, json!({})).await;
    t.run_all(again).await;
    assert_eq!(
        t.q::<i64>("select count(*) from docs.freshness_flags")
            .await,
        2
    );
    assert_eq!(t.q::<i64>("select count(*) from core.notes").await, 2);

    let notion = t
        .start(
            "notion",
            "# Onboarding\n\nWelcome to **Acme**.\n\n- step one\n",
            json!({}),
        )
        .await;
    t.run_all(notion).await;
    let (title, body, freshness): (String, String, String) = {
        let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
        sqlx::query_as(
            "select title, content_md, freshness from core.notes where source_system = 'notion'",
        )
        .fetch_one(&mut *tx)
        .await
        .unwrap()
    };
    assert_eq!(
        (title.as_str(), freshness.as_str()),
        ("Onboarding", "unverified")
    );
    assert!(body.starts_with("Welcome to **Acme**."));
}

#[tokio::test]
async fn csv_mapping_and_trello_board() {
    let t = tenant().await;
    let csv = CSV.replace("ada@example.com", &t.ada.1);
    let mapping = json!({"id": "Ref", "title": "Summary", "description": "Details", "status": "State", "priority": "Severity",
                         "assignee_email": "Owner", "labels": "Tags"});
    let id = t.start("csv", &csv, json!({"mapping": mapping})).await;
    t.run_all(id).await;
    let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
    let rows: Vec<(String, String, String, String, Option<Uuid>, Vec<String>, Value)> = sqlx::query_as(
        "select n.source_id, i.status, i.status_category, i.priority, i.assignee_id, i.labels, i.custom
         from projects.items i join core.notes n on n.id = i.note_id order by n.source_id",
    )
    .fetch_all(&mut *tx)
    .await
    .unwrap();
    assert_eq!(rows.len(), 3);
    assert_eq!(
        (rows[0].0.as_str(), rows[0].2.as_str(), rows[0].3.as_str()),
        ("T-1", "in_progress", "high")
    );
    assert_eq!(rows[0].4, Some(t.ada.0));
    assert_eq!(rows[0].5, vec!["auth", "web"]);
    assert_eq!(rows[0].6["Team"], "Platform");
    assert_eq!((rows[1].2.as_str(), rows[1].3.as_str()), ("done", "low"));
    assert_eq!(rows[2].3, "urgent");
    drop(tx);

    // A mapping that names a missing column is refused before anything is queued.
    let err = t
        .exec("import.start", json!({"source": "csv", "space_id": t.space, "content": csv, "mapping": {"title": "Nope"}}))
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::Invalid);

    let trello = t.start("trello", TRELLO, json!({})).await;
    t.run_all(trello).await;
    let r = &t.status(trello).await["report"];
    assert_eq!(
        (
            r["imported"].as_u64(),
            r["skipped"].as_u64(),
            r["comments"].as_u64()
        ),
        (Some(2), Some(1), Some(1))
    );
    let (status, cat, body): (String, String, String) = {
        let mut tx = cnote_db::begin_tenant(&t.pool, t.ws).await.unwrap();
        sqlx::query_as(
            "select i.status, i.status_category, n.content_md from projects.items i join core.notes n on n.id = i.note_id where n.source_id = 'c1'",
        )
        .fetch_one(&mut *tx)
        .await
        .unwrap()
    };
    assert_eq!(
        (status.as_str(), cat.as_str()),
        ("In Progress", "in_progress")
    );
    assert!(body.contains("- [x] Logo\n- [ ] Screenshots"));
    assert_eq!(t.q::<i64>("select count(*) from core.events where kind = 'item.transitioned' and actor_type = 'import'").await, 1);
}

#[tokio::test]
async fn imports_are_admin_only_and_tenant_isolated() {
    let a = tenant().await;
    let b = tenant().await;
    let id = a.start("jira", &a.jira(), json!({})).await;
    a.run_all(id).await;

    // Another tenant cannot see the job or its notes, and the same source ids import independently there.
    let err = b
        .exec("import.status", json!({"id": id}))
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::NotFound);
    assert_eq!(b.exec("import.list", json!({})).await.unwrap(), json!([]));
    let bid = b.start("jira", &b.jira(), json!({})).await;
    b.run_all(bid).await;
    assert_eq!(a.q::<i64>("select count(*) from projects.items").await, 3);
    assert_eq!(b.q::<i64>("select count(*) from projects.items").await, 3);
    assert_eq!(
        a.exec("import.list", json!({}))
            .await
            .unwrap()
            .as_array()
            .unwrap()
            .len(),
        1
    );

    // A member cannot start an import; the space must belong to the caller's tenant.
    let member = auth::user_principal(&a.pool, a.ada.0, a.ws).await.unwrap();
    assert_eq!(member.role, Role::Member);
    let err = a
        .reg
        .execute(
            &a.pool,
            "import.start",
            json!({"source": "csv", "space_id": a.space, "content": "x"}),
            &member,
            &ExecOptions::default(),
        )
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::Forbidden);
    let err = b
        .exec(
            "import.start",
            json!({"source": "notion", "space_id": a.space, "content": "# Hi"}),
        )
        .await
        .unwrap_err();
    assert_eq!(err.code, ErrorCode::NotFound);
}
