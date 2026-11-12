//! Outbound hooks end to end: the real `cnote serve --with-worker` binary, a local receiver, and C Note's own
//! MCP endpoint as the remote MCP server. Run `scripts/setup-db.sh` first. Credentials are generated per run.

use axum::http::HeaderMap;
use base64::Engine;
use serde_json::{Value, json};
use std::process::Stdio;
use std::time::Duration;
use tokio::process::{Child, Command};
use tokio::sync::mpsc;
use uuid::Uuid;

struct Srv {
    base: String,
    http: reqwest::Client,
    cookie: String,
    ws: Uuid,
    _child: Child,
}

impl Srv {
    async fn start() -> Srv {
        let port = std::net::TcpListener::bind("127.0.0.1:0")
            .unwrap()
            .local_addr()
            .unwrap()
            .port();
        let base = format!("http://127.0.0.1:{port}");
        // One key for every server in this run: any of their workers may claim any delivery job.
        static KEY: std::sync::OnceLock<String> = std::sync::OnceLock::new();
        // A local dev server's worker may claim jobs too, so prefer the environment's key.
        let key = KEY.get_or_init(|| {
            std::env::var("CNOTE_MASTER_KEY")
                .ok()
                .filter(|k| !k.is_empty())
                .unwrap_or_else(|| {
                    base64::engine::general_purpose::STANDARD.encode(rand::random::<[u8; 32]>())
                })
        });
        let child = Command::new(env!("CARGO_BIN_EXE_cnote"))
            .args(["serve", "--port", &port.to_string(), "--with-worker"])
            .current_dir(concat!(env!("CARGO_MANIFEST_DIR"), "/../.."))
            .env("CNOTE_MASTER_KEY", key)
            .env("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
            .env("PUBLIC_BASE_URL", &base)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let http = reqwest::Client::new();
        for _ in 0..100 {
            if http.get(format!("{base}/healthz")).send().await.is_ok() {
                break;
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
        let res = http
            .post(format!("{base}/api/auth/signup"))
            .json(
                &json!({"email": format!("{}@test.local", Uuid::new_v4()), "name": "Olive",
                          "password": Uuid::new_v4().to_string(), "workspace_name": "Acme"}),
            )
            .send()
            .await
            .unwrap();
        let cookie = res.headers()[reqwest::header::SET_COOKIE]
            .to_str()
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .to_string();
        let ws = res.json::<Value>().await.unwrap()["workspace_id"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        Srv {
            base,
            http,
            cookie,
            ws,
            _child: child,
        }
    }

    async fn run(&self, name: &str, body: Value) -> Value {
        let res = self
            .http
            .post(format!("{}/api/w/{}/commands/{name}", self.base, self.ws))
            .header("cookie", &self.cookie)
            .json(&body)
            .send()
            .await
            .unwrap();
        let status = res.status();
        let v = res.json().await.unwrap_or(Value::Null);
        assert!(status.is_success(), "{name}: {status} {v}");
        v
    }
}

/// A receiver on a random port that forwards (headers, body) of every POST.
async fn receiver() -> (String, mpsc::UnboundedReceiver<(HeaderMap, String)>) {
    let (tx, rx) = mpsc::unbounded_channel();
    let app = axum::Router::new().route(
        "/hook",
        axum::routing::post(move |h: HeaderMap, body: String| {
            let tx = tx.clone();
            async move {
                let _ = tx.send((h, body));
                "ok"
            }
        }),
    );
    let l = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}/hook", l.local_addr().unwrap());
    tokio::spawn(async move { axum::serve(l, app).await.unwrap() });
    (url, rx)
}

#[tokio::test]
async fn http_hook_delivers_signed_events() {
    let s = Srv::start().await;
    let (url, mut rx) = receiver().await;
    let created = s
        .run(
            "hook.create",
            json!({"name": "Notes feed", "target": "http", "url": url, "events": ["note.created"]}),
        )
        .await;
    let secret = created["signing_secret"].as_str().unwrap().to_string();
    assert!(secret.starts_with("whsec_"));
    // The secret is never listed.
    assert!(
        !s.run("hook.list", json!({}))
            .await
            .to_string()
            .contains(&secret)
    );

    let space = s.run("space.list", json!({})).await[0]["id"].clone();
    let note = s
        .run("note.create", json!({"space_id": space, "title": "Hooked"}))
        .await;

    let (h, body) = tokio::time::timeout(Duration::from_secs(20), rx.recv())
        .await
        .expect("no delivery")
        .unwrap();
    let ts: i64 = h["x-cnote-timestamp"].to_str().unwrap().parse().unwrap();
    assert_eq!(
        h["x-cnote-signature"].to_str().unwrap(),
        cnote_ai::hooks::signature(&secret, ts, &body)
    );
    let v: Value = serde_json::from_str(&body).unwrap();
    assert_eq!(v["type"], "event");
    assert_eq!(v["event"]["kind"], "note.created");
    assert_eq!(v["event"]["note_id"], note["id"]);

    let id = created["id"].clone();
    s.run("hook.set_enabled", json!({"id": id, "enabled": false}))
        .await;
    s.run(
        "note.create",
        json!({"space_id": space, "title": "Not hooked"}),
    )
    .await;
    assert!(
        tokio::time::timeout(Duration::from_secs(3), rx.recv())
            .await
            .is_err(),
        "paused hook delivered"
    );
    assert_eq!(
        s.run("hook.delete", json!({"id": id})).await["deleted"],
        true
    );
}

#[tokio::test]
async fn mcp_hook_calls_a_remote_tool_and_records_failures() {
    let s = Srv::start().await;
    let key = s.run("apikey.create", json!({"name": "mcp hook"})).await["api_key"]
        .as_str()
        .unwrap()
        .to_string();
    let ok = s
        .run("hook.create", json!({"name": "Self MCP", "target": "mcp", "url": format!("{}/mcp", s.base),
                                   "tool": "discover", "arguments": {"query": "note"}, "token": key, "every_minutes": 60}))
        .await;
    assert!(ok["signing_secret"].is_null());
    assert_eq!(
        s.run("hook.test", json!({"id": ok["id"]})).await["ok"],
        true
    );

    let bad = s
        .run(
            "hook.create",
            json!({"name": "Wrong token", "target": "mcp", "url": format!("{}/mcp", s.base),
                                   "tool": "discover", "token": "cn_nope", "every_minutes": 60}),
        )
        .await;
    let r = s.run("hook.test", json!({"id": bad["id"]})).await;
    assert_eq!(r["ok"], false, "{r}");
    let list = s.run("hook.list", json!({})).await;
    let row = list
        .as_array()
        .unwrap()
        .iter()
        .find(|h| h["id"] == bad["id"])
        .unwrap();
    assert_eq!(row["last_status"], "failed");
    assert!(row["next_run_at"].is_string());
}
