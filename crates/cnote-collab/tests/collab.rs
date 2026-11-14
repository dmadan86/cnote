//! Integration tests against a real Postgres (DATABASE_URL as cnote_app). Run `scripts/setup-db.sh` first.

use cnote_core::{ExecOptions, Principal, Registry, auth};
use futures::{SinkExt, StreamExt};
use serde_json::json;
use sqlx::PgPool;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::Message as Ws;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::{MaybeTlsStream, WebSocketStream, connect_async};
use uuid::Uuid;
use yrs::sync::{Message, SyncMessage};
use yrs::updates::decoder::Decode;
use yrs::updates::encoder::Encode;
use yrs::{Doc, GetString, ReadTxn, Text, Transact, Update, XmlFragment, XmlOut};

type Sock = WebSocketStream<MaybeTlsStream<TcpStream>>;

struct Env {
    pool: PgPool,
    addr: std::net::SocketAddr,
    ws: Uuid,
    owner: Principal,
    note: Uuid,
}

async fn env() -> (Env, Arc<Registry>) {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").unwrap(), 5)
        .await
        .unwrap();
    let reg = Registry::new(vec![cnote_core::module(), cnote_collab::module()]);
    let app =
        cnote_collab::router_with_debounce(pool.clone(), reg.clone(), Duration::from_millis(400));
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

    let email = format!("{}@example.com", Uuid::new_v4());
    let user = auth::sign_up(&pool, &email, "Owner", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let ws = auth::create_workspace(&pool, &reg, user, "Acme")
        .await
        .unwrap();
    let owner = auth::user_principal(&pool, user, ws).await.unwrap();
    let o = ExecOptions::default();
    let space = reg
        .execute(
            &pool,
            "space.create",
            json!({"key": "DOC", "name": "Docs", "visibility": "private"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let note = reg
        .execute(
            &pool,
            "note.create",
            json!({"space_id": space["id"], "title": "Plan", "content_md": "hello world"}),
            &owner,
            &o,
        )
        .await
        .unwrap();
    let note = note["id"].as_str().unwrap().parse().unwrap();
    (
        Env {
            pool,
            addr,
            ws,
            owner,
            note,
        },
        reg,
    )
}

impl Env {
    fn url(&self) -> String {
        format!("ws://{}/collab/{}/{}", self.addr, self.ws, self.note)
    }

    async fn connect_session(&self, user: Uuid) -> Sock {
        let token = auth::create_session(&self.pool, user).await.unwrap();
        let mut req = self.url().into_client_request().unwrap();
        req.headers_mut()
            .insert("cookie", format!("cn_session={token}").parse().unwrap());
        connect_async(req).await.unwrap().0
    }

    async fn note_row(&self) -> (String, i32, bool) {
        let mut tx = cnote_db::begin_tenant(&self.pool, self.ws).await.unwrap();
        sqlx::query_as(
            "select content_md, version, content_ydoc is not null from core.notes where id = $1",
        )
        .bind(self.note)
        .fetch_one(&mut *tx)
        .await
        .unwrap()
    }
}

async fn recv(s: &mut Sock) -> Option<Message> {
    loop {
        match tokio::time::timeout(Duration::from_millis(1500), s.next()).await {
            Ok(Some(Ok(Ws::Binary(b)))) => return Some(Message::decode_v1(&b).unwrap()),
            Ok(Some(Ok(_))) => continue,
            _ => return None,
        }
    }
}

async fn send(s: &mut Sock, m: Message) {
    s.send(Ws::Binary(m.encode_v1().into())).await.unwrap();
}

/// y-sync handshake: answer the server's step 1 with ours and apply its step 2.
async fn sync(s: &mut Sock, doc: &Doc) {
    let mut got_step2 = false;
    send(
        s,
        Message::Sync(SyncMessage::SyncStep1(doc.transact().state_vector())),
    )
    .await;
    while !got_step2 {
        match recv(s).await.expect("server message") {
            Message::Sync(SyncMessage::SyncStep1(sv)) => {
                let diff = doc.transact().encode_diff_v1(&sv);
                send(s, Message::Sync(SyncMessage::SyncStep2(diff))).await;
            }
            Message::Sync(SyncMessage::SyncStep2(u)) => {
                doc.transact_mut()
                    .apply_update(Update::decode_v1(&u).unwrap())
                    .unwrap();
                got_step2 = true;
            }
            _ => {}
        }
    }
}

fn text(doc: &Doc) -> String {
    let txn = doc.transact();
    txn.get_xml_fragment("default")
        .map(|f| f.get_string(&txn))
        .unwrap_or_default()
}

/// Append to the first paragraph and return the update that carries the change.
fn append(doc: &Doc, s: &str) -> Vec<u8> {
    let before = doc.transact().state_vector();
    {
        let mut txn = doc.transact_mut();
        let f = txn.get_xml_fragment("default").unwrap();
        let Some(XmlOut::Element(p)) = f.get(&txn, 0) else {
            panic!("no paragraph")
        };
        let Some(XmlOut::Text(t)) = p.get(&txn, 0) else {
            panic!("no text")
        };
        let len = t.len(&txn);
        t.insert(&mut txn, len, s);
    }
    doc.transact().encode_diff_v1(&before)
}

#[tokio::test]
async fn edits_flow_between_clients_and_persist() {
    let (e, reg) = env().await;
    let user = e.owner.user_id.unwrap();
    let (a, b) = (Doc::new(), Doc::new());
    let mut ca = e.connect_session(user).await;
    sync(&mut ca, &a).await;
    // A second client joins with an API key in the query string.
    let (_, key) = auth::create_api_key(&e.pool, &e.owner, "collab", &[], None)
        .await
        .unwrap();
    let mut cb = connect_async(format!("{}?token={key}", e.url()))
        .await
        .unwrap()
        .0;
    sync(&mut cb, &b).await;
    assert!(
        text(&a).contains("hello world"),
        "seeded from content_md: {}",
        text(&a)
    );
    assert!(text(&b).contains("hello world"));

    let u = append(&a, ", team");
    send(&mut ca, Message::Sync(SyncMessage::Update(u))).await;
    let Some(Message::Sync(SyncMessage::Update(u))) = recv(&mut cb).await else {
        panic!("no broadcast")
    };
    b.transact_mut()
        .apply_update(Update::decode_v1(&u).unwrap())
        .unwrap();
    assert!(text(&b).contains("hello world, team"));

    // Persisted after the idle window: markdown snapshot, ydoc, version bump, note.updated event.
    tokio::time::sleep(Duration::from_millis(1200)).await;
    let (md, version, has_ydoc) = e.note_row().await;
    assert_eq!(md, "hello world, team");
    assert_eq!(version, 2);
    assert!(has_ydoc);
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    let (changed, actor): (serde_json::Value, Option<String>) = sqlx::query_as(
        "select payload->'changed', actor_id from core.events where note_id = $1 and kind = 'note.updated'",
    )
    .bind(e.note)
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert_eq!(changed, json!(["content"]));
    assert_eq!(actor, Some(user.to_string()));
    drop(tx);

    // Last disconnect flushes immediately: edit, then leave before the debounce fires.
    let u = append(&b, "!");
    send(&mut cb, Message::Sync(SyncMessage::Update(u))).await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    drop(ca);
    cb.close(None).await.unwrap();
    tokio::time::sleep(Duration::from_millis(300)).await;
    assert_eq!(e.note_row().await.0, "hello world, team!");

    // A new session loads the saved Yjs state (no reseed) and sees the same text.
    let c = Doc::new();
    let mut cc = e.connect_session(user).await;
    sync(&mut cc, &c).await;
    assert!(text(&c).contains("hello world, team!"));
    drop(reg);
}

#[tokio::test]
async fn viewers_cannot_edit_and_strangers_are_rejected() {
    let (e, _reg) = env().await;
    let user = e.owner.user_id.unwrap();

    // No credentials, bad key, bad session: refused before the upgrade.
    let err = connect_async(e.url()).await.unwrap_err();
    assert!(matches!(err, tokio_tungstenite::tungstenite::Error::Http(r) if r.status() == 401));
    let err = connect_async(format!("{}?token=cn_nope_nope", e.url()))
        .await
        .unwrap_err();
    assert!(matches!(err, tokio_tungstenite::tungstenite::Error::Http(r) if r.status() == 401));
    let mut req = e.url().into_client_request().unwrap();
    req.headers_mut()
        .insert("cookie", "cn_session=bogus".parse().unwrap());
    assert!(connect_async(req).await.is_err());

    // A member of the workspace who is not in the private space cannot even see the note (404).
    let other_email = format!("{}@example.com", Uuid::new_v4());
    let other = auth::sign_up(&e.pool, &other_email, "Other", &Uuid::new_v4().to_string())
        .await
        .unwrap();
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    sqlx::query(
        "insert into core.memberships (workspace_id, user_id, role) values ($1,$2,'member')",
    )
    .bind(e.ws)
    .bind(other)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    let token = auth::create_session(&e.pool, other).await.unwrap();
    let mut req = e.url().into_client_request().unwrap();
    req.headers_mut()
        .insert("cookie", format!("cn_session={token}").parse().unwrap());
    let err = connect_async(req).await.unwrap_err();
    assert!(matches!(err, tokio_tungstenite::tungstenite::Error::Http(r) if r.status() == 404));

    // Make them a space viewer: they can watch, but their edits are ignored.
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    sqlx::query(
        "insert into core.space_members (workspace_id, space_id, user_id, role)
         select $1, space_id, $2, 'viewer' from core.notes where id = $3",
    )
    .bind(e.ws)
    .bind(other)
    .bind(e.note)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();

    let (a, v) = (Doc::new(), Doc::new());
    let mut ca = e.connect_session(user).await;
    sync(&mut ca, &a).await;
    let mut cv = e.connect_session(other).await;
    sync(&mut cv, &v).await;
    assert!(text(&v).contains("hello world"), "viewers can read");

    let u = append(&v, " HACKED");
    send(&mut cv, Message::Sync(SyncMessage::Update(u))).await;
    assert!(
        recv(&mut ca).await.is_none(),
        "viewer update must not be relayed"
    );
    tokio::time::sleep(Duration::from_millis(1000)).await;
    assert_eq!(
        e.note_row().await,
        ("hello world".to_string(), 1, false),
        "nothing persisted"
    );

    // Editors' changes still reach the viewer.
    let u = append(&a, "!");
    send(&mut ca, Message::Sync(SyncMessage::Update(u))).await;
    assert!(matches!(
        recv(&mut cv).await,
        Some(Message::Sync(SyncMessage::Update(_)))
    ));
}

#[tokio::test]
async fn page_restrictions_and_shares_bind_websocket_writers() {
    let (e, reg) = env().await;
    let o = ExecOptions::default();
    let owner_id = e.owner.user_id.unwrap();
    // A space editor, who could normally write.
    let email = format!("{}@example.com", Uuid::new_v4());
    let m = reg
        .execute(
            &e.pool,
            "member.add",
            json!({"email": email, "role": "member"}),
            &e.owner,
            &o,
        )
        .await
        .unwrap();
    let other: Uuid = m["user_id"].as_str().unwrap().parse().unwrap();
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    sqlx::query(
        "insert into core.space_members (workspace_id, space_id, user_id, role)
         select $1, space_id, $2, 'editor' from core.notes where id = $3",
    )
    .bind(e.ws)
    .bind(other)
    .bind(e.note)
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    let run = |cmd: &'static str, input: serde_json::Value| {
        let (pool, reg, owner) = (e.pool.clone(), reg.clone(), e.owner.clone());
        async move {
            reg.execute(&pool, cmd, input, &owner, &ExecOptions::default())
                .await
        }
    };

    // View restriction: the editor is turned away like a stranger.
    run(
        "note.restrict",
        json!({"note_id": e.note, "restriction": "view"}),
    )
    .await
    .unwrap();
    let token = auth::create_session(&e.pool, other).await.unwrap();
    let mut req = e.url().into_client_request().unwrap();
    req.headers_mut()
        .insert("cookie", format!("cn_session={token}").parse().unwrap());
    let err = connect_async(req).await.unwrap_err();
    assert!(matches!(err, tokio_tungstenite::tungstenite::Error::Http(r) if r.status() == 404));

    // Shared as viewer: may watch, edits never reach the shared doc.
    run(
        "note.share",
        json!({"note_id": e.note, "user_id": other, "level": "view"}),
    )
    .await
    .unwrap();
    let (a, v) = (Doc::new(), Doc::new());
    let mut ca = e.connect_session(owner_id).await;
    sync(&mut ca, &a).await;
    let mut cv = e.connect_session(other).await;
    sync(&mut cv, &v).await;
    assert!(text(&v).contains("hello world"));
    let u = append(&v, " HACKED");
    send(&mut cv, Message::Sync(SyncMessage::Update(u))).await;
    assert!(
        recv(&mut ca).await.is_none(),
        "view-only update must not be relayed"
    );
    tokio::time::sleep(Duration::from_millis(1000)).await;
    assert_eq!(e.note_row().await, ("hello world".to_string(), 1, false));

    // Edit-restricted page: same for a space editor who is not listed.
    run("note.unshare", json!({"note_id": e.note, "user_id": other}))
        .await
        .unwrap();
    run(
        "note.restrict",
        json!({"note_id": e.note, "restriction": "edit"}),
    )
    .await
    .unwrap();
    let mut cv2 = e.connect_session(other).await;
    let v2 = Doc::new();
    sync(&mut cv2, &v2).await;
    let u = append(&v2, " HACKED");
    send(&mut cv2, Message::Sync(SyncMessage::Update(u))).await;
    assert!(recv(&mut ca).await.is_none());

    // Listed as editor: now the change is relayed.
    run(
        "note.share",
        json!({"note_id": e.note, "user_id": other, "level": "edit"}),
    )
    .await
    .unwrap();
    let mut cv3 = e.connect_session(other).await;
    let v3 = Doc::new();
    sync(&mut cv3, &v3).await;
    let u = append(&v3, "?");
    send(&mut cv3, Message::Sync(SyncMessage::Update(u))).await;
    assert!(matches!(
        recv(&mut ca).await,
        Some(Message::Sync(SyncMessage::Update(_)))
    ));

    // Access revoked while connected: the socket is dropped at the next re-check.
    run(
        "note.restrict",
        json!({"note_id": e.note, "restriction": "view"}),
    )
    .await
    .unwrap();
    run("note.unshare", json!({"note_id": e.note, "user_id": other}))
        .await
        .unwrap();
    let closed = tokio::time::timeout(Duration::from_secs(25), async {
        while let Some(Ok(_)) = cv3.next().await {}
    })
    .await;
    assert!(closed.is_ok(), "revoked user must be disconnected");
}

/// A second gateway on the same database: a fresh process after a crash (no rooms in memory).
async fn gateway(pool: &PgPool, reg: &Arc<Registry>, debounce: Duration) -> std::net::SocketAddr {
    let app = cnote_collab::router_with_debounce(pool.clone(), reg.clone(), debounce);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    addr
}

async fn connect_at(e: &Env, addr: std::net::SocketAddr, user: Uuid, query: &str) -> Sock {
    let token = auth::create_session(&e.pool, user).await.unwrap();
    let mut req = format!("ws://{addr}/collab/{}/{}{query}", e.ws, e.note)
        .into_client_request()
        .unwrap();
    req.headers_mut()
        .insert("cookie", format!("cn_session={token}").parse().unwrap());
    connect_async(req).await.unwrap().0
}

/// The close code the server ends this socket with (None if it stays open).
async fn close_code(s: &mut Sock) -> Option<u16> {
    loop {
        match tokio::time::timeout(Duration::from_millis(3000), s.next()).await {
            Ok(Some(Ok(Ws::Close(f)))) => return f.map(|f| u16::from(f.code)),
            Ok(Some(Ok(_))) => continue,
            _ => return None,
        }
    }
}

async fn scalar<
    T: for<'r> sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres> + Send + Unpin,
>(
    e: &Env,
    sql: &str,
) -> T {
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    sqlx::query_scalar(sqlx::AssertSqlSafe(sql.to_string()))
        .bind(e.note)
        .fetch_one(&mut *tx)
        .await
        .unwrap()
}

#[tokio::test]
async fn relayed_edits_survive_a_crash_before_the_snapshot() {
    let (e, reg) = env().await;
    let user = e.owner.user_id.unwrap();
    // This gateway never snapshots on its own (1 hour debounce): only the update log can save the edit.
    let first = gateway(&e.pool, &reg, Duration::from_secs(3600)).await;
    let (a, b) = (Doc::new(), Doc::new());
    let mut ca = connect_at(&e, first, user, "").await;
    sync(&mut ca, &a).await;
    let mut cb = connect_at(&e, first, user, "").await;
    sync(&mut cb, &b).await;
    send(
        &mut ca,
        Message::Sync(SyncMessage::Update(append(&a, ", team"))),
    )
    .await;
    assert!(
        matches!(
            recv(&mut cb).await,
            Some(Message::Sync(SyncMessage::Update(_)))
        ),
        "relayed"
    );
    // Relayed means committed: the edit is in the log while the page row still has the old text.
    let logged: i64 = scalar(
        &e,
        "select count(*) from core.note_updates where note_id = $1",
    )
    .await;
    assert!(logged >= 1);
    assert_eq!(e.note_row().await.0, "hello world");

    // "Crash": a new gateway with no rooms in memory serves the page from snapshot + log.
    let second = gateway(&e.pool, &reg, Duration::from_millis(300)).await;
    let c = Doc::new();
    let mut cc = connect_at(&e, second, user, "").await;
    sync(&mut cc, &c).await;
    assert!(text(&c).contains("hello world, team"), "{}", text(&c));
    // It folds the recovered updates into a snapshot and clears exactly those from the log.
    tokio::time::sleep(Duration::from_millis(1200)).await;
    assert_eq!(e.note_row().await.0, "hello world, team");
    let left: i64 = scalar(
        &e,
        "select count(*) from core.note_updates where note_id = $1",
    )
    .await;
    assert_eq!(left, 0);
}

#[tokio::test]
async fn restore_fences_open_editors_and_keeps_every_version() {
    let (e, reg) = env().await;
    let user = e.owner.user_id.unwrap();
    let o = ExecOptions::default();
    let a = Doc::new();
    let mut ca = e.connect_session(user).await;
    sync(&mut ca, &a).await;
    send(
        &mut ca,
        Message::Sync(SyncMessage::Update(append(&a, " v2"))),
    )
    .await;
    tokio::time::sleep(Duration::from_millis(1000)).await; // snapshot + first version
    send(
        &mut ca,
        Message::Sync(SyncMessage::Update(append(&a, " v3"))),
    )
    .await;
    tokio::time::sleep(Duration::from_millis(200)).await; // logged, not yet snapshotted

    let revs = reg
        .execute(
            &e.pool,
            "note.revisions",
            json!({"note_id": e.note}),
            &e.owner,
            &o,
        )
        .await
        .unwrap();
    assert_eq!(revs.as_array().unwrap().len(), 1, "{revs}");
    let v2 = revs[0]["id"].as_str().unwrap().to_string();

    // A stale reviewer is refused; the real restore succeeds.
    let stale = reg
        .execute(
            &e.pool,
            "note.restore",
            json!({"note_id": e.note, "revision_id": v2, "expected_epoch": 7}),
            &e.owner,
            &o,
        )
        .await;
    assert!(stale.is_err());
    let r = reg
        .execute(
            &e.pool,
            "note.restore",
            json!({"note_id": e.note, "revision_id": v2, "expected_epoch": 1}),
            &e.owner,
            &o,
        )
        .await
        .unwrap();
    assert_eq!(r["collab_epoch"], json!(2));

    // The open editor is closed with 4409 (on its next edit or access re-check, whichever comes first).
    send(
        &mut ca,
        Message::Sync(SyncMessage::Update(append(&a, " lost"))),
    )
    .await;
    assert_eq!(
        close_code(&mut ca).await,
        Some(cnote_collab::CLOSE_STALE_EPOCH)
    );
    tokio::time::sleep(Duration::from_millis(800)).await;
    assert_eq!(
        e.note_row().await.0,
        "hello world v2",
        "the fenced room did not write"
    );

    // History: v2, the live state before the restore (with the unsnapshotted v3), and the restore.
    let revs = reg
        .execute(
            &e.pool,
            "note.revisions",
            json!({"note_id": e.note}),
            &e.owner,
            &o,
        )
        .await
        .unwrap();
    let reasons: Vec<_> = revs
        .as_array()
        .unwrap()
        .iter()
        .map(|r| r["reason"].clone())
        .collect();
    assert_eq!(
        reasons,
        vec![json!("restore"), json!("before_restore"), json!("edit")]
    );
    let before = reg
        .execute(
            &e.pool,
            "note.revision",
            json!({"id": revs[1]["id"]}),
            &e.owner,
            &o,
        )
        .await
        .unwrap();
    assert_eq!(before["content_md"], json!("hello world v2 v3"));
    assert_eq!(revs[0]["restored_from"], json!(v2));

    // A tab still on the old epoch cannot save its stale copy over the restore through the API either.
    let stale_save = reg
        .execute(
            &e.pool,
            "note.update",
            json!({"id": e.note, "content_md": "stale", "expected_epoch": 1}),
            &e.owner,
            &o,
        )
        .await;
    assert!(stale_save.is_err());

    // Versions are immutable for the app role.
    let mut tx = cnote_db::begin_tenant(&e.pool, e.ws).await.unwrap();
    let edit = sqlx::query("update core.note_revisions set content_md = 'x' where note_id = $1")
        .bind(e.note)
        .execute(&mut *tx)
        .await;
    assert!(edit.is_err(), "revisions must not be editable");
    drop(tx);

    // A client still holding the old document is refused; one on the new epoch sees the restored text.
    let mut old = connect_at(&e, e.addr, user, "?epoch=1").await;
    assert_eq!(
        close_code(&mut old).await,
        Some(cnote_collab::CLOSE_STALE_EPOCH)
    );
    let n = Doc::new();
    let mut cn = connect_at(&e, e.addr, user, "?epoch=2").await;
    sync(&mut cn, &n).await;
    assert!(
        text(&n).contains("hello world v2") && !text(&n).contains("v3"),
        "{}",
        text(&n)
    );
}
