//! Collaboration gateway (ADR-005): a Yjs-compatible WebSocket server. One in-memory room per note, y-sync
//! protocol (sync step 1/2, update, awareness relay), debounced persistence into core.notes.
//! shortcut: awareness is relayed, not stored, so a late joiner sees other cursors on their next heartbeat
//! (y-websocket clients re-send every ~15s). Rooms live in one process; scale out by routing a note to one node.

mod md;

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
use yrs::sync::{Message, SyncMessage};
use yrs::updates::decoder::Decode;
use yrs::updates::encoder::Encode;
use yrs::{ClientID, Doc, Options, ReadTxn, StateVector, Transact, Update};

pub const SESSION_COOKIE: &str = "cn_session";
const DEBOUNCE: Duration = Duration::from_secs(2);
const MAX_FRAME: usize = 8 << 20;

#[derive(Clone)]
struct AppState {
    pool: PgPool,
    registry: Arc<Registry>,
    rooms: Arc<Mutex<HashMap<(Uuid, Uuid), Arc<Room>>>>,
    debounce: Duration,
}

struct Room {
    key: (Uuid, Uuid),
    state: Mutex<RoomState>,
    /// Serialises persists so an older snapshot never overwrites a newer one.
    persist: tokio::sync::Mutex<()>,
}

struct RoomState {
    doc: Doc,
    peers: HashMap<u64, mpsc::UnboundedSender<Vec<u8>>>,
    next_peer: u64,
    dirty: bool,
    last_edit: Instant,
    editor: Option<Principal>,
    timer_running: bool,
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

async fn load_doc(ctx: &mut Ctx, note: Uuid) -> CmdResult<Doc> {
    let (md_text, ydoc): (String, Option<Vec<u8>>) = sqlx::query_as(
        "select content_md, content_ydoc from core.notes where id = $1 and deleted_at is null",
    )
    .bind(note)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if let Some(bytes) = ydoc.filter(|b| !b.is_empty()) {
        let doc = Doc::new();
        let ok = Update::decode_v1(&bytes)
            .ok()
            .is_some_and(|u| doc.transact_mut().apply_update(u).is_ok());
        // Persisting writes content_md from the doc, so a mismatch means the note was edited through the API.
        if ok && md::to_markdown(&doc).trim() == md_text.trim() {
            return Ok(doc);
        }
    }
    let doc = Doc::with_options(Options {
        client_id: seed_client(note),
        ..Options::default()
    });
    md::seed(&doc, &md_text);
    Ok(doc)
}

#[derive(serde::Deserialize)]
struct Q {
    token: Option<String>,
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
    upgrade
        .max_message_size(MAX_FRAME)
        .on_upgrade(move |socket| serve(socket, s, room, principal, write))
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
    let scope_ok = p
        .scopes
        .as_ref()
        .is_none_or(|sc| sc.iter().any(|g| g == "*" || g == "write_notes"));
    let write = match assert_note_access(&mut ctx, note, true).await {
        Ok(_) => scope_ok,
        // Viewers and read-only guests may watch.
        Err(e) if e.code == ErrorCode::Forbidden => {
            assert_note_access(&mut ctx, note, false).await?;
            false
        }
        Err(e) => return Err(e),
    };
    let key = (ws, note);
    if let Some(r) = s.rooms.lock().unwrap().get(&key) {
        return Ok((r.clone(), write));
    }
    let doc = load_doc(&mut ctx, note).await?;
    ctx.tx.commit().await?;
    let room = Arc::new(Room {
        key,
        state: Mutex::new(RoomState {
            doc,
            peers: HashMap::new(),
            next_peer: 0,
            dirty: false,
            last_edit: Instant::now(),
            editor: None,
            timer_running: false,
        }),
        persist: Default::default(),
    });
    // Another connection may have created the room while this one was loading.
    let room = s.rooms.lock().unwrap().entry(key).or_insert(room).clone();
    Ok((room, write))
}

fn send_all(st: &RoomState, except: u64, frame: &[u8]) {
    for (id, tx) in &st.peers {
        if *id != except {
            let _ = tx.send(frame.to_vec());
        }
    }
}

async fn serve(socket: WebSocket, s: AppState, room: Arc<Room>, principal: Principal, write: bool) {
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
        id
    };
    let writer = tokio::spawn(async move {
        while let Some(frame) = rx.recv().await {
            if sink.send(Ws::Binary(frame.into())).await.is_err() {
                break;
            }
        }
    });

    while let Some(Ok(frame)) = stream.next().await {
        match frame {
            Ws::Binary(b) => on_frame(&s, &room, me, &tx, &principal, write, &b),
            Ws::Close(_) => break,
            _ => {}
        }
    }

    room.state.lock().unwrap().peers.remove(&me);
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

fn on_frame(
    s: &AppState,
    room: &Arc<Room>,
    me: u64,
    tx: &mpsc::UnboundedSender<Vec<u8>>,
    principal: &Principal,
    write: bool,
    frame: &[u8],
) {
    let Ok(msg) = Message::decode_v1(frame) else {
        return;
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
                return; // read-only: their edits never reach the shared doc
            }
            let Ok(update) = Update::decode_v1(&u) else {
                return;
            };
            {
                let mut st = room.state.lock().unwrap();
                let mut txn = st.doc.transact_mut();
                if txn.apply_update(update).is_err() || !txn.has_changes() {
                    return;
                }
                drop(txn);
                st.dirty = true;
                st.last_edit = Instant::now();
                st.editor = Some(principal.clone());
                send_all(&st, me, &Message::Sync(SyncMessage::Update(u)).encode_v1());
            }
            kick(s, room);
        }
        Message::Awareness(_) => send_all(&room.state.lock().unwrap(), me, frame),
        _ => {}
    }
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
    let (state, md_text, editor) = {
        let mut st = room.state.lock().unwrap();
        if !st.dirty {
            return Ok(());
        }
        st.dirty = false;
        let bytes = st
            .doc
            .transact()
            .encode_state_as_update_v1(&StateVector::default());
        (bytes, md::to_markdown(&st.doc), st.editor.clone())
    };
    let Some(editor) = editor else { return Ok(()) };
    let result: CmdResult<()> = async {
        let mut tx = cnote_db::begin_tenant(&s.pool, room.key.0).await?;
        let version: Option<i32> = sqlx::query_scalar(
            "update core.notes set content_ydoc = $2, content_md = $3, version = version + 1, updated_at = now()
             where id = $1 and deleted_at is null returning version",
        )
        .bind(room.key.1)
        .bind(&state)
        .bind(&md_text)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some(v) = version {
            events::emit(
                &mut tx,
                &editor,
                "note.updated",
                Some(room.key.1),
                json!({"changed": ["content"], "version": v}),
            )
            .await?;
        }
        tx.commit().await?;
        Ok(())
    }
    .await;
    if let Err(e) = &result {
        tracing::error!(note = %room.key.1, error = %e, "collab persist failed");
        room.state.lock().unwrap().dirty = true;
    }
    result
}
