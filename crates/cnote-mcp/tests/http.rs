//! Streamable HTTP integration tests: the axum router on a random port, JSON-RPC over reqwest, real Postgres.
//! Run `scripts/setup-db.sh` first.

use cnote_core::{
    CmdResult, Command, Ctx, ExecOptions, Module, Principal, Registry, ToolGroup, auth, handler,
};
use schemars::JsonSchema;
use serde::Deserialize;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};
use uuid::Uuid;

#[derive(Deserialize, JsonSchema)]
struct SendIn {
    /// Recipient address.
    to: String,
}

async fn test_send(_: &mut Ctx, i: SendIn) -> CmdResult<Value> {
    Ok(json!({"sent_to": i.to}))
}

fn registry() -> Arc<Registry> {
    let outbound = Module {
        commands: vec![
            Command::new(
                "test.send",
                "Send a test message to someone.",
                ToolGroup::SendMessages,
                handler!(test_send),
            )
            .confirm(),
        ],
        ..Default::default()
    };
    Registry::new(vec![cnote_core::module(), outbound])
}

struct Env {
    pool: PgPool,
    reg: Arc<Registry>,
    url: String,
    http: reqwest::Client,
    user: Principal,
    agent_key: String,
    space: Value,
}

async fn run(env: &Env, p: &Principal, cmd: &str, input: Value) -> Value {
    env.reg
        .execute(&env.pool, cmd, input, p, &ExecOptions::default())
        .await
        .unwrap_or_else(|e| panic!("{cmd}: {e}"))
}

async fn setup() -> Env {
    let _ = dotenvy::from_path(concat!(env!("CARGO_MANIFEST_DIR"), "/../../.env"));
    let pool = cnote_db::connect(&std::env::var("DATABASE_URL").expect("DATABASE_URL"), 5)
        .await
        .unwrap();
    let reg = registry();
    let user_id = auth::sign_up(
        &pool,
        &format!("{}@test.local", Uuid::new_v4()),
        "Tess",
        &Uuid::new_v4().to_string(),
    )
    .await
    .unwrap();
    let ws = auth::create_workspace(&pool, &reg, user_id, "Acme")
        .await
        .unwrap();
    let user = auth::user_principal(&pool, user_id, ws).await.unwrap();

    let app = cnote_mcp::router::<()>(pool.clone(), reg.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

    let mut env = Env {
        pool,
        reg,
        url: format!("http://{addr}/mcp"),
        http: reqwest::Client::new(),
        user: user.clone(),
        agent_key: String::new(),
        space: Value::Null,
    };
    env.space = run(
        &env,
        &user,
        "space.create",
        json!({"key": "ENG", "name": "Engineering"}),
    )
    .await;
    env.agent_key = run(&env, &user, "agent.create", json!({"name": "claude"})).await["api_key"]
        .as_str()
        .unwrap()
        .to_string();
    env
}

static NEXT_ID: AtomicI64 = AtomicI64::new(1);

/// JSON-RPC call. Returns (HTTP status, response headers, parsed body).
async fn rpc(
    env: &Env,
    key: Option<&str>,
    method: &str,
    params: Value,
) -> (u16, reqwest::header::HeaderMap, Value) {
    let mut rq = env
        .http
        .post(&env.url)
        .header("Accept", "application/json, text/event-stream")
        .json(&json!({"jsonrpc": "2.0", "id": NEXT_ID.fetch_add(1, Ordering::SeqCst), "method": method, "params": params}));
    if let Some(k) = key {
        rq = rq.bearer_auth(k);
    }
    let res = rq.send().await.unwrap();
    let (status, headers) = (res.status().as_u16(), res.headers().clone());
    let text = res.text().await.unwrap();
    let body = serde_json::from_str(&text).unwrap_or_else(|_| {
        // SSE fallback: take the last data frame.
        text.lines()
            .filter_map(|l| l.strip_prefix("data:"))
            .filter_map(|d| serde_json::from_str::<Value>(d.trim()).ok())
            .next_back()
            .unwrap_or(Value::Null)
    });
    (status, headers, body)
}

async fn call(env: &Env, tool: &str, args: Value) -> Value {
    let key = env.agent_key.clone();
    let (status, _, body) = rpc(
        env,
        Some(&key),
        "tools/call",
        json!({"name": tool, "arguments": args}),
    )
    .await;
    assert_eq!(status, 200, "{body}");
    assert!(body["error"].is_null(), "protocol error: {body}");
    body["result"].clone()
}

#[tokio::test]
async fn requires_a_bearer_api_key() {
    let env = setup().await;
    let init = json!({"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "t", "version": "1"}});
    let (status, headers, _) = rpc(&env, None, "initialize", init.clone()).await;
    assert_eq!(status, 401);
    assert!(
        headers["www-authenticate"]
            .to_str()
            .unwrap()
            .starts_with("Bearer")
    );
    let (status, headers, _) = rpc(&env, Some("cn_nope_nope"), "initialize", init).await;
    assert_eq!(status, 401);
    assert!(
        headers["www-authenticate"]
            .to_str()
            .unwrap()
            .contains("invalid_token")
    );
}

#[tokio::test]
async fn initialize_list_and_call_over_http() {
    let env = setup().await;
    let key = env.agent_key.clone();
    let (status, _, init) = rpc(
        &env,
        Some(&key),
        "initialize",
        json!({"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "t", "version": "1"}}),
    )
    .await;
    assert_eq!(status, 200, "{init}");
    assert!(init["result"]["capabilities"]["tools"].is_object());
    assert!(init["result"]["capabilities"]["resources"].is_object());

    let (_, _, list) = rpc(&env, Some(&key), "tools/list", json!({})).await;
    let tools = list["result"]["tools"].as_array().unwrap();
    let find = |n: &str| {
        tools
            .iter()
            .find(|t| t["name"] == n)
            .unwrap_or_else(|| panic!("tool {n} missing"))
    };
    assert_eq!(find("note_get")["annotations"]["readOnlyHint"], true);
    assert_eq!(find("note_create")["inputSchema"]["type"], "object");
    assert!(find("note_create")["inputSchema"]["properties"]["space_id"].is_object());
    find("discover");
    find("invoke");
    assert!(
        tools
            .iter()
            .all(|t| t["name"] != "note_delete" && !t["name"].as_str().unwrap().contains('.'))
    );

    // note_create as the agent, then read it back as a tool, a resource and via the resource listing.
    let created = call(
        &env,
        "note_create",
        json!({"space_id": env.space["id"], "title": "Launch plan", "content_md": "Ship it."}),
    )
    .await;
    assert_eq!(created["isError"], false, "{created}");
    let note = &created["structuredContent"];
    assert_eq!(note["title"], "Launch plan");
    let id = note["id"].as_str().unwrap();

    let got = call(&env, "note_get", json!({"id": id})).await;
    assert_eq!(got["structuredContent"]["content_md"], "Ship it.");

    let ws = env.user.workspace_id;
    let uri = format!("cnote://ws/{ws}/note/{id}");
    let (_, _, read) = rpc(&env, Some(&key), "resources/read", json!({"uri": uri})).await;
    let text = read["result"]["contents"][0]["text"].as_str().unwrap();
    assert!(text.starts_with("---\ntitle: \"Launch plan\"\n"), "{text}");
    assert!(
        text.contains("owner: \"Tess\"")
            && text.contains("freshness: \"fresh\"")
            && text.contains("verify_by:")
    );
    assert!(text.ends_with("Ship it."));
    let (_, _, listed) = rpc(&env, Some(&key), "resources/list", json!({})).await;
    assert!(
        listed["result"]["resources"]
            .as_array()
            .unwrap()
            .iter()
            .any(|r| r["uri"] == uri.as_str())
    );
    // Another workspace's URI does not resolve.
    let (_, _, other) = rpc(
        &env,
        Some(&key),
        "resources/read",
        json!({"uri": format!("cnote://ws/{}/note/{id}", Uuid::new_v4())}),
    )
    .await;
    assert!(other["error"].is_object());

    // Saved view: no item.list command here, so the query text comes back.
    let view = run(
        &env,
        &env.user,
        "view.create",
        json!({"name": "Open", "layout": "table", "query": "project = ENG"}),
    )
    .await;
    let (_, _, v) = rpc(
        &env,
        Some(&key),
        "resources/read",
        json!({"uri": format!("cnote://ws/{ws}/view/{}", view["id"].as_str().unwrap())}),
    )
    .await;
    assert!(
        v["result"]["contents"][0]["text"]
            .as_str()
            .unwrap()
            .contains("project = ENG")
    );

    // Bad input surfaces as a tool error with the CmdError code, not a protocol error.
    let bad = call(&env, "note_get", json!({"id": Uuid::new_v4()})).await;
    assert_eq!(bad["isError"], true);
    assert_eq!(bad["structuredContent"]["error"]["code"], "not_found");
}

#[tokio::test]
async fn discover_and_invoke_with_idempotency() {
    let env = setup().await;
    let found = call(&env, "discover", json!({"query": "delete note"})).await;
    let cmds = found["structuredContent"]["commands"].as_array().unwrap();
    assert_eq!(cmds[0]["name"], "note.delete");
    assert_eq!(cmds[0]["input_schema"]["type"], "object");

    let args = json!({"command": "note.create", "arguments": {"space_id": env.space["id"], "title": "Once"}, "idempotency_key": Uuid::new_v4().to_string()});
    let a = call(&env, "invoke", args.clone()).await;
    let b = call(&env, "invoke", args).await;
    assert_eq!(
        a["structuredContent"]["id"], b["structuredContent"]["id"],
        "same key, same result"
    );
    let all = run(
        &env,
        &env.user,
        "note.list",
        json!({"space_id": env.space["id"]}),
    )
    .await;
    assert_eq!(
        all.as_array()
            .unwrap()
            .iter()
            .filter(|n| n["title"] == "Once")
            .count(),
        1
    );

    let missing = call(&env, "invoke", json!({"command": "nope.nothing"})).await;
    assert_eq!(missing["isError"], true);
    assert_eq!(missing["structuredContent"]["error"]["code"], "not_found");
}

#[tokio::test]
async fn destructive_tools_are_refused_for_agents_by_default() {
    let env = setup().await;
    let note = run(
        &env,
        &env.user,
        "note.create",
        json!({"space_id": env.space["id"], "title": "Keep me"}),
    )
    .await;
    let r = call(
        &env,
        "invoke",
        json!({"command": "note.delete", "arguments": {"id": note["id"]}}),
    )
    .await;
    assert_eq!(r["isError"], true);
    assert_eq!(r["structuredContent"]["error"]["code"], "forbidden");
    // Not callable directly by tool name either: only advertised core tools are.
    let key = env.agent_key.clone();
    let (_, _, direct) = rpc(
        &env,
        Some(&key),
        "tools/call",
        json!({"name": "note_delete", "arguments": {"id": note["id"]}}),
    )
    .await;
    assert!(direct["error"].is_object());
    let still = run(&env, &env.user, "note.get", json!({"id": note["id"]})).await;
    assert_eq!(still["title"], "Keep me");
}

#[tokio::test]
async fn outbound_commands_need_human_approval_in_c_note() {
    let env = setup().await;
    let user = env.user.clone();
    run(
        &env,
        &user,
        "workspace.update",
        json!({"settings": {"agent_tool_groups": ["read", "write_notes", "send_messages"]}}),
    )
    .await;
    let mailer = run(
        &env,
        &user,
        "agent.create",
        json!({"name": "mailer", "scopes": ["read", "send_messages"]}),
    )
    .await;
    let mut env = env;
    env.agent_key = mailer["api_key"].as_str().unwrap().to_string();

    let args = json!({"command": "test.send", "arguments": {"to": "ravi@example.com"}});
    let first = call(&env, "invoke", args.clone()).await;
    let pending = &first["structuredContent"];
    assert_eq!(pending["confirmation_required"], true);
    assert!(pending["message"].as_str().unwrap().contains("approve"));
    let cid = pending["confirmation_id"].as_str().unwrap().to_string();

    // Without approval, retrying with the id does not run it.
    let early = call(
        &env,
        "invoke",
        json!({"command": "test.send", "confirmation_id": cid}),
    )
    .await;
    assert_eq!(early["isError"], true);
    assert_eq!(early["structuredContent"]["error"]["code"], "forbidden");

    run(
        &env,
        &user,
        "confirmation.decide",
        json!({"id": cid, "approve": true}),
    )
    .await;
    let done = call(
        &env,
        "invoke",
        json!({"command": "test.send", "confirmation_id": cid}),
    )
    .await;
    assert_eq!(
        done["structuredContent"]["sent_to"], "ravi@example.com",
        "the approved payload runs"
    );
    let again = call(
        &env,
        "invoke",
        json!({"command": "test.send", "confirmation_id": cid}),
    )
    .await;
    assert_eq!(again["isError"], true, "approval is single use");
}

#[tokio::test]
async fn elicitation_capable_client_is_asked_but_its_answer_is_not_approval() {
    let env = setup().await;
    let user = env.user.clone();
    run(
        &env,
        &user,
        "workspace.update",
        json!({"settings": {"agent_tool_groups": ["read", "send_messages"]}}),
    )
    .await;
    let mailer = run(
        &env,
        &user,
        "agent.create",
        json!({"name": "mailer", "scopes": ["read", "send_messages"]}),
    )
    .await;
    let key = mailer["api_key"].as_str().unwrap().to_string();
    let meta = json!({
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientInfo": {"name": "t", "version": "1"},
        "io.modelcontextprotocol/clientCapabilities": {"elicitation": {}}
    });
    let send = |extra: Value| {
        let mut params = json!({"name": "invoke", "arguments": {"command": "test.send", "arguments": {"to": "a@example.com"}}, "_meta": meta});
        params
            .as_object_mut()
            .unwrap()
            .extend(extra.as_object().unwrap().clone());
        env.http
            .post(&env.url)
            .bearer_auth(&key)
            .header("Accept", "application/json, text/event-stream")
            .header("MCP-Protocol-Version", "2026-07-28")
            .header("Mcp-Method", "tools/call")
            .header("Mcp-Name", "invoke")
            .json(&json!({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}))
            .send()
    };
    let first: Value = send(json!({})).await.unwrap().json().await.unwrap();
    assert_eq!(first["result"]["resultType"], "input_required", "{first}");
    let ask = &first["result"]["inputRequests"]["approval"];
    assert!(
        ask["params"]["message"]
            .as_str()
            .unwrap()
            .contains("approve"),
        "{first}"
    );
    let state = first["result"]["requestState"]
        .as_str()
        .unwrap()
        .to_string();
    assert!(state.starts_with("confirmation:"));

    // The client "accepts" and retries: that yields the pending confirmation again, never an execution.
    let retry: Value =
        send(json!({"requestState": state, "inputResponses": {"approval": {"action": "accept"}}}))
            .await
            .unwrap()
            .json()
            .await
            .unwrap();
    let pending = &retry["result"]["structuredContent"];
    assert_eq!(pending["confirmation_required"], true, "{retry}");
    assert!(pending.get("sent_to").is_none());
}
