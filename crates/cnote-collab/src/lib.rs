//! Collaboration gateway (ADR-005): a Yjs-compatible WebSocket server. One in-memory room per note, y-sync
//! protocol (sync step 1/2, update, awareness relay). Every accepted update is committed to core.note_updates
//! before it is relayed, and a debounced snapshot into core.notes absorbs (and deletes) the logged updates, so a
//! crash loses nothing anyone has seen. Writes are fenced by core.notes.collab_epoch: after a restore the old room
//! can no longer write, and its clients are closed with code 4409 so they reload the restored page.
//! Each connection's latest awareness (presence, cursor) is kept in memory: replayed to anyone who joins and
//! cleared for everyone when that connection drops. Rooms live in one process; scale out by routing a note to one node.

mod md;
mod revisions;

pub use revisions::module;

use axum::Router;
use axum::extract::ws::{Message as Ws, WebSocket, WebSocketUpgrade};
use axum::extract::{Path, Query, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use cnote_core::access::assert_note_access;
use cnote_core::error::{CmdError, CmdResult, ErrorCode};
use cnote_core::{Ctx, Principal, Registry, auth, events};
use futures::{SinkExt, StreamExt};
use serde_json::json;
use sqlx::PgPool;
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::sync::mpsc;
use uuid::Uuid;
use yrs::sync::awareness::AwarenessUpdate;
use yrs::sync::{Message, SyncMessage};
use yrs::updates::decoder::Decode;
use yrs::updates::encoder::Encode;
use yrs::{ClientID, Doc, Options, ReadTxn, StateVector, Transact, Update};

pub const SESSION_COOKIE: &str = "cn_session";
const DEBOUNCE: Duration = Duration::from_secs(2);
const MAX_FRAME: usize = 8 << 20;
/// WebSocket close code for "this page was restored; reload it" (an old collab epoch).
pub const CLOSE_STALE_EPOCH: u16 = 4409;

#[derive(Clone)]
struct AppState {
    pool: PgPool,
    registry: Arc<Registry>,
    rooms: Arc<Mutex<HashMap<(Uuid, Uuid), Arc<Room>>>>,
    debounce: Duration,
}

struct Room {
    key: (Uuid, Uuid),
    /// core.notes.collab_epoch this room was loaded at; every write is fenced on it.
    epoch: i32,
    /// Flips to true when the room is fenced out (restored elsewhere): every connection then closes.
    stale: tokio::sync::watch::Sender<bool>,
    state: Mutex<RoomState>,
    /// Serialises persists so an older snapshot never overwrites a newer one.
    persist: tokio::sync::Mutex<()>,
}

struct RoomState {
    doc: Doc,
    peers: HashMap<u64, mpsc::UnboundedSender<Vec<u8>>>,
    next_peer: u64,
    /// Latest awareness per connection, so joiners see who is here at once.
    awareness: HashMap<u64, AwarenessUpdate>,
    dirty: bool,
    last_edit: Instant,
    editor: Option<Principal>,
    timer_running: bool,
    /// Ids in core.note_updates already applied to `doc`; the next snapshot deletes exactly these.
    applied: Vec<i64>,
}

pub fn router(pool: PgPool, registry: Arc<Registry>) -> Router {
    router_with_debounce(pool, registry, DEBOUNCE)
}

pub fn router_with_debounce(pool: PgPool, registry: Arc<Registry>, debounce: Duration) -> Router {
    Router::new()
        .route("/collab/{workspace_id}/{note_id}", get(ws_handler))
        .with_state(AppState {
            pool,
            registry,
            rooms: Default::default(),
            debounce,
        })
}

fn fail(e: CmdError) -> Response {
    let status =
        StatusCode::from_u16(e.code.http_status()).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
    (status, e.message).into_response()
}

fn cookie(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(';'))
        .find_map(|kv| {
            let (k, v) = kv.trim().split_once('=')?;
            (k == name).then(|| v.to_string())
        })
}

/// Browsers attach cookies to cross-site WebSocket handshakes, so a cookie login only counts for same-origin
/// pages or origins listed in COLLAB_ALLOWED_ORIGINS (comma separated, e.g. https://app.example.com).
fn origin_ok(headers: &HeaderMap) -> bool {
    let Some(origin) = headers.get(header::ORIGIN).and_then(|v| v.to_str().ok()) else {
        return true; // not a browser
    };
    let host = headers
        .get(header::HOST)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    let same = origin.split_once("://").is_some_and(|(_, h)| h == host);
    same || std::env::var("COLLAB_ALLOWED_ORIGINS")
        .is_ok_and(|l| l.split(',').any(|o| o.trim() == origin))
}

async fn authenticate(
    s: &AppState,
    headers: &HeaderMap,
    token: Option<String>,
    workspace_id: Uuid,
) -> CmdResult<Principal> {
    let unauth = |m: &str| CmdError::new(ErrorCode::Unauthenticated, m);
    let bearer = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.strip_prefix("Bearer "))
        .map(|t| t.trim().to_string())
        .or(token);
    if let Some(key) = bearer {
        let p = auth::principal_from_api_key(&s.pool, &key)
            .await?
            .ok_or_else(|| unauth("invalid API key"))?;
        if p.workspace_id != workspace_id {
            return Err(CmdError::forbidden("API key belongs to another workspace"));
        }
        return Ok(p);
    }
    let session = cookie(headers, SESSION_COOKIE).ok_or_else(|| unauth("sign in required"))?;
    if !origin_ok(headers) {
        return Err(CmdError::forbidden("cross-origin collaboration request"));
    }
    let user = auth::user_from_session(&s.pool, &session)
        .await?
        .ok_or_else(|| unauth("session expired"))?;
    auth::user_principal(&s.pool, user.id, workspace_id).await
}

/// Deterministic client id for seeding, so seeding the same note twice yields identical Yjs items and
/// clients that cached an earlier seed merge cleanly instead of duplicating the content.
fn seed_client(note: Uuid) -> ClientID {
    let b = note.as_bytes();
    let n = u64::from_be_bytes([0, 0, b[0] & 0x1f, b[1], b[2], b[3], b[4], b[5]]);
    ClientID::new(n.max(1))
}

/// The live document: the last snapshot (or a seed from Markdown) plus every logged update of the current
/// epoch. Returns the doc, the epoch, and the log ids it absorbed (the next snapshot deletes them).
pub(crate) async fn load_doc(
    conn: &mut sqlx::PgConnection,
    note: Uuid,
) -> CmdResult<(Doc, i32, Vec<i64>)> {
    let (md_text, ydoc, epoch): (String, Option<Vec<u8>>, i32) = sqlx::query_as(
        "select content_md, content_ydoc, collab_epoch from core.notes where id = $1 and deleted_at is null",
    )
    .bind(note)
    .fetch_optional(&mut *conn)
    .await?
    .ok_or_else(|| CmdError::not_found("note"))?;
    let log: Vec<(i64, Vec<u8>)> = sqlx::query_as(
        "select id, update from core.note_updates where note_id = $1 and epoch = $2 order by id",
    )
    .bind(note)
    .bind(epoch)
    .fetch_all(&mut *conn)
    .await?;
    let ids = log.iter().map(|(id, _)| *id).collect();
    let snapshot = ydoc.filter(|b| !b.is_empty());
    let mut replay = snapshot.is_none(); // a seed is deterministic, so updates made on it apply again
    let doc = 'doc: {
        if let Some(bytes) = snapshot {
            let doc = Doc::new();
            let ok = Update::decode_v1(&bytes)
                .ok()
                .is_some_and(|u| doc.transact_mut().apply_update(u).is_ok());
            // Persisting writes content_md from the doc, so a mismatch means the note was edited through the API;
            // then the API text wins and updates logged against the older snapshot are dropped.
            if ok && md::same(&md::to_markdown(&doc), &md_text) {
                replay = true;
                break 'doc doc;
            }
        }
        let doc = Doc::with_options(Options {
            client_id: seed_client(note),
            ..Options::default()
        });
        md::seed(&doc, &md_text);
        doc
    };
    if replay {
        let mut txn = doc.transact_mut();
        for (_, u) in &log {
            if let Ok(u) = Update::decode_v1(u) {
                let _ = txn.apply_update(u);
            }
        }
    }
    Ok((doc, epoch, ids))
}

#[derive(serde::Deserialize)]
struct Q {
    token: Option<String>,
    /// The collab_epoch the client's document belongs to (note.get). A different one means the page was restored
    /// since, so merging the client's state would bring the replaced content back: it is refused with 4409.
    epoch: Option<i32>,
}

async fn ws_handler(
    State(s): State<AppState>,
    Path((ws, note)): Path<(Uuid, Uuid)>,
    Query(q): Query<Q>,
    headers: HeaderMap,
    upgrade: WebSocketUpgrade,
) -> Response {
    let principal = match authenticate(&s, &headers, q.token, ws).await {
        Ok(p) => p,
        Err(e) => return fail(e),
    };
    let prepared = prepare(&s, &principal, ws, note).await;
    let (room, write) = match prepared {
        Ok(r) => r,
        Err(e) => return fail(e),
    };
    let upgrade = upgrade.max_message_size(MAX_FRAME);
    if q.epoch.is_some_and(|e| e != room.epoch) {
        return upgrade.on_upgrade(|mut socket| async move { close_stale(&mut socket).await });
    }
    upgrade.on_upgrade(move |socket| serve(socket, s, room, principal, write))
}

async fn close_stale(socket: &mut WebSocket) {
    let _ = socket
        .send(Ws::Close(Some(axum::extract::ws::CloseFrame {
            code: CLOSE_STALE_EPOCH,
            reason: "page restored".into(),
        })))
        .await;
}

/// Fence a room out: drop it from the map (if still there) and close all of its connections.
fn retire(s: &AppState, room: &Arc<Room>) {
    let mut rooms = s.rooms.lock().unwrap();
    if rooms.get(&room.key).is_some_and(|r| Arc::ptr_eq(r, room)) {
        rooms.remove(&room.key);
    }
    room.stale.send_replace(true);
}

/// Access check plus room lookup/creation. Returns the room and whether this caller may edit.
async fn prepare(
    s: &AppState,
    p: &Principal,
    ws: Uuid,
    note: Uuid,
) -> CmdResult<(Arc<Room>, bool)> {
    let tx = cnote_db::begin_tenant(&s.pool, ws).await?;
    let mut ctx = Ctx {
        tx,
        principal: p.clone(),
        registry: s.registry.clone(),
    };
    let write = may_write(&mut ctx, note).await?;
    let epoch: i32 = sqlx::query_scalar("select collab_epoch from core.notes where id = $1")
        .bind(note)
        .fetch_one(&mut *ctx.tx)
        .await?;
    let key = (ws, note);
    let open = s.rooms.lock().unwrap().get(&key).cloned();
    if let Some(r) = open {
        if r.epoch == epoch {
            return Ok((r, write));
        }
        retire(s, &r); // restored since this room loaded
    }
    let (doc, epoch, applied) = load_doc(&mut ctx.tx, note).await?;
    ctx.tx.commit().await?;
    let recovered = !applied.is_empty();
    let room = Arc::new(Room {
        key,
        epoch,
        stale: tokio::sync::watch::channel(false).0,
        state: Mutex::new(RoomState {
            doc,
            peers: HashMap::new(),
            next_peer: 0,
            awareness: HashMap::new(),
            dirty: false,
            last_edit: Instant::now(),
            editor: None,
            timer_running: false,
            applied,
        }),
        persist: Default::default(),
    });
    // Another connection may have created the room while this one was loading.
    let room = s.rooms.lock().unwrap().entry(key).or_insert(room).clone();
    if recovered {
        // Updates logged before a crash: fold them into a snapshot soon.
        room.state.lock().unwrap().dirty = true;
        kick(s, &room);
    }
    Ok((room, write))
}

/// Whether the caller may edit the note; errors when they may not even read it. View-only users (space
/// viewers, read-only guests, restricted or shared-as-viewer pages) may watch.
async fn may_write(ctx: &mut Ctx, note: Uuid) -> CmdResult<bool> {
    let scope_ok = ctx
        .principal
        .scopes
        .as_ref()
        .is_none_or(|sc| sc.iter().any(|g| g == "*" || g == "write_notes"));
    match assert_note_access(ctx, note, true).await {
        Ok(_) => Ok(scope_ok),
        Err(e) if e.code == ErrorCode::Forbidden => {
            assert_note_access(ctx, note, false).await?;
            Ok(false)
        }
        Err(e) => Err(e),
    }
}

/// Sharing can change while a socket is open: re-check periodically so a revoked user stops reading and a
/// downgraded user stops writing.
async fn recheck(s: &AppState, p: &Principal, room: &Room) -> CmdResult<bool> {
    let tx = cnote_db::begin_tenant(&s.pool, room.key.0).await?;
    let mut ctx = Ctx {
        tx,
        principal: p.clone(),
        registry: s.registry.clone(),
    };
    let w = may_write(&mut ctx, room.key.1).await?;
    let epoch: Option<i32> =
        sqlx::query_scalar("select collab_epoch from core.notes where id = $1")
            .bind(room.key.1)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    if epoch != Some(room.epoch) {
        return Err(CmdError::new(ErrorCode::Conflict, "page restored"));
    }
    Ok(w)
}

fn send_all(st: &RoomState, except: u64, frame: &[u8]) {
    for (id, tx) in &st.peers {
        if *id != except {
            let _ = tx.send(frame.to_vec());
        }
    }
}

/// How often an open connection re-checks its page access.
const ACCESS_RECHECK: Duration = Duration::from_secs(10);

async fn serve(socket: WebSocket, s: AppState, room: Arc<Room>, principal: Principal, write: bool) {
    let mut write = write;
    let (mut sink, mut stream) = socket.split();
    let (tx, mut rx) = mpsc::unbounded_channel::<Vec<u8>>();
    let me = {
        let mut st = room.state.lock().unwrap();
        let id = st.next_peer;
        st.next_peer += 1;
        st.peers.insert(id, tx.clone());
        let step1 =
            Message::Sync(SyncMessage::SyncStep1(st.doc.transact().state_vector())).encode_v1();
        let _ = tx.send(step1);
        for u in st.awareness.values() {
            let _ = tx.send(Message::Awareness(u.clone()).encode_v1());
        }
        id
    };
    let writer = tokio::spawn(async move {
        while let Some(frame) = rx.recv().await {
            if frame.is_empty() {
                // Sentinel from the fenced path (Yjs frames are never empty).
                let close = axum::extract::ws::CloseFrame {
                    code: CLOSE_STALE_EPOCH,
                    reason: "page restored".into(),
                };
                let _ = sink.send(Ws::Close(Some(close))).await;
                break;
            }
            if sink.send(Ws::Binary(frame.into())).await.is_err() {
                break;
            }
        }
    });

    let mut tick = tokio::time::interval(ACCESS_RECHECK);
    tick.tick().await;
    let mut stale = room.stale.subscribe();
    let mut fenced = *stale.borrow();
    while !fenced {
        tokio::select! {
            frame = stream.next() => match frame {
                Some(Ok(Ws::Binary(b))) => match on_frame(&s, &room, me, &tx, &principal, write, &b).await {
                    Ok(()) => {}
                    Err(e) if e.code == ErrorCode::Conflict => fenced = true,
                    Err(e) => {
                        // Not logged, so not relayed: the client still has the edit and resends it on reconnect.
                        tracing::error!(note = %room.key.1, error = %e, "collab update not persisted");
                        break;
                    }
                },
                Some(Ok(Ws::Close(_))) | None | Some(Err(_)) => break,
                _ => {}
            },
            _ = stale.changed() => fenced = true,
            _ = tick.tick() => match recheck(&s, &principal, &room).await {
                Ok(w) => write = write && w,
                Err(e) if e.code == ErrorCode::Conflict => fenced = true,
                Err(e) if e.code == ErrorCode::NotFound || e.code == ErrorCode::Forbidden => break,
                Err(_) => {} // transient database error: keep the connection, retry next tick
            },
        }
    }
    if fenced {
        retire(&s, &room);
        room.state.lock().unwrap().peers.remove(&me);
        let _ = tx.send(Vec::new()); // the writer turns this into a 4409 close
        drop(tx);
        let _ = tokio::time::timeout(Duration::from_secs(2), writer).await;
        return;
    }

    {
        let mut st = room.state.lock().unwrap();
        st.peers.remove(&me);
        // Tell the others this connection's people left, instead of waiting for their 30s timeout.
        if let Some(mut gone) = st.awareness.remove(&me) {
            for e in gone.clients.values_mut() {
                e.clock += 1;
                e.json = "null".into();
            }
            send_all(&st, me, &Message::Awareness(gone).encode_v1());
        }
    }
    writer.abort();
    let empty = room.state.lock().unwrap().peers.is_empty();
    if empty {
        let _ = persist(&s, &room).await;
        let mut rooms = s.rooms.lock().unwrap();
        let st = room.state.lock().unwrap();
        // Keep the room if someone joined meanwhile or the final write failed (the timer retries).
        if st.peers.is_empty() && !st.dirty {
            rooms.remove(&room.key);
        }
    }
}

/// Commit one update to the log, fenced on the room's epoch. Conflict means the page was restored since.
async fn log_update(s: &AppState, room: &Room, update: &[u8]) -> CmdResult<i64> {
    let mut tx = cnote_db::begin_tenant(&s.pool, room.key.0).await?;
    let id: Option<i64> = sqlx::query_scalar(
        "insert into core.note_updates (workspace_id, note_id, epoch, update)
         select workspace_id, id, collab_epoch, $3 from core.notes
         where id = $1 and collab_epoch = $2 and deleted_at is null
         returning id",
    )
    .bind(room.key.1)
    .bind(room.epoch)
    .bind(update)
    .fetch_optional(&mut *tx)
    .await?;
    tx.commit().await?;
    id.ok_or_else(|| CmdError::new(ErrorCode::Conflict, "page restored"))
}

async fn on_frame(
    s: &AppState,
    room: &Arc<Room>,
    me: u64,
    tx: &mpsc::UnboundedSender<Vec<u8>>,
    principal: &Principal,
    write: bool,
    frame: &[u8],
) -> CmdResult<()> {
    let Ok(msg) = Message::decode_v1(frame) else {
        return Ok(());
    };
    match msg {
        Message::Sync(SyncMessage::SyncStep1(sv)) => {
            let diff = room
                .state
                .lock()
                .unwrap()
                .doc
                .transact()
                .encode_diff_v1(&sv);
            let _ = tx.send(Message::Sync(SyncMessage::SyncStep2(diff)).encode_v1());
        }
        Message::Sync(SyncMessage::SyncStep2(u) | SyncMessage::Update(u)) => {
            if !write {
                return Ok(()); // read-only: their edits never reach the shared doc
            }
            let Ok(update) = Update::decode_v1(&u) else {
                return Ok(());
            };
            {
                let st = room.state.lock().unwrap();
                let mut txn = st.doc.transact_mut();
                if txn.apply_update(update).is_err() || !txn.has_changes() {
                    return Ok(());
                }
            }
            // Durable before anyone else sees it. If this fails the connection closes; the sender still holds
            // the edit and resends it on reconnect (sync step 2), and the in-memory doc reaches the next snapshot.
            let id = log_update(s, room, &u).await?;
            {
                let mut st = room.state.lock().unwrap();
                st.applied.push(id);
                st.dirty = true;
                st.last_edit = Instant::now();
                st.editor = Some(principal.clone());
                send_all(&st, me, &Message::Sync(SyncMessage::Update(u)).encode_v1());
            }
            kick(s, room);
        }
        Message::Awareness(u) => {
            let mut st = room.state.lock().unwrap();
            send_all(&st, me, frame);
            let kept = st.awareness.entry(me).or_insert_with(|| AwarenessUpdate {
                clients: HashMap::new(),
            });
            for (client, e) in u.clients {
                kept.clients.insert(client, e);
            }
        }
        _ => {}
    }
    Ok(())
}

/// Start the idle timer for this room unless one is already running.
fn kick(s: &AppState, room: &Arc<Room>) {
    {
        let mut st = room.state.lock().unwrap();
        if st.timer_running {
            return;
        }
        st.timer_running = true;
    }
    let (s, room) = (s.clone(), room.clone());
    tokio::spawn(async move {
        loop {
            let wait = {
                let st = room.state.lock().unwrap();
                s.debounce.saturating_sub(st.last_edit.elapsed())
            };
            if !wait.is_zero() {
                tokio::time::sleep(wait).await;
                continue;
            }
            let ok = persist(&s, &room).await.is_ok();
            let mut st = room.state.lock().unwrap();
            if !st.dirty {
                st.timer_running = false;
                return;
            }
            if !ok {
                st.last_edit = Instant::now(); // back off before retrying a failed write
            }
        }
    });
}

/// Write the doc and its Markdown snapshot, bump the version, and emit note.updated as the last editor.
async fn persist(s: &AppState, room: &Arc<Room>) -> CmdResult<()> {
    let _guard = room.persist.lock().await;
    let (state, md_text, editor, applied) = {
        let mut st = room.state.lock().unwrap();
        if !st.dirty {
            return Ok(());
        }
        st.dirty = false;
        let bytes = st
            .doc
            .transact()
            .encode_state_as_update_v1(&StateVector::default());
        // Taken under the same lock as the state: the snapshot contains exactly these logged updates.
        let applied = std::mem::take(&mut st.applied);
        (bytes, md::to_markdown(&st.doc), st.editor.clone(), applied)
    };
    let result: CmdResult<bool> = async {
        let mut tx = cnote_db::begin_tenant(&s.pool, room.key.0).await?;
        let version: Option<i32> = sqlx::query_scalar(
            "update core.notes set content_ydoc = $2, content_md = $3, version = version + 1, updated_at = now()
             where id = $1 and deleted_at is null and collab_epoch = $4 returning version",
        )
        .bind(room.key.1)
        .bind(&state)
        .bind(&md_text)
        .bind(room.epoch)
        .fetch_optional(&mut *tx)
        .await?;
        let Some(v) = version else {
            return Ok(false); // restored (or deleted) since this room loaded: fenced out
        };
        sqlx::query("delete from core.note_updates where id = any($1)")
            .bind(&applied)
            .execute(&mut *tx)
            .await?;
        let actor = editor.as_ref().and_then(|e| e.user_id);
        cnote_core::notes::checkpoint(&mut tx, room.key.1, actor, cnote_core::notes::CHECKPOINT_EVERY_SECS)
            .await?;
        if let Some(editor) = &editor {
            events::emit(
                &mut tx,
                editor,
                "note.updated",
                Some(room.key.1),
                json!({"changed": ["content"], "version": v}),
            )
            .await?;
        }
        tx.commit().await?;
        Ok(true)
    }
    .await;
    match result {
        Ok(true) => Ok(()),
        Ok(false) => {
            retire(s, room);
            Ok(())
        }
        Err(e) => {
            tracing::error!(note = %room.key.1, error = %e, "collab persist failed");
            let mut st = room.state.lock().unwrap();
            st.dirty = true;
            st.applied.extend(applied);
            Err(e)
        }
    }
}
