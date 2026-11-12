//! GitHub webhook -> evidence. The caller (cnote-server) passes the raw body and the X-Hub-Signature-256 header.

use cnote_core::{CmdError, CmdResult, ErrorCode, ExecOptions, Principal, Registry};
use regex::Regex;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::collections::BTreeSet;
use std::sync::{Arc, OnceLock};
use uuid::Uuid;

pub use cnote_core::auth::hmac_sha256;

fn signature_ok(secret: &str, header: &str, body: &[u8]) -> bool {
    let Some(sig) = header
        .trim()
        .strip_prefix("sha256=")
        .and_then(|h| hex::decode(h).ok())
    else {
        return false;
    };
    let want = hmac_sha256(secret.as_bytes(), body);
    // Constant-time compare: fold every byte difference.
    sig.len() == want.len()
        && sig
            .iter()
            .zip(want.iter())
            .fold(0u8, |a, (x, y)| a | (x ^ y))
            == 0
}

/// Item keys like ENG-12 in free text or a branch name (feature/eng-12-login), uppercased and deduplicated.
pub fn extract_keys(texts: &[&str]) -> Vec<String> {
    static RE: OnceLock<Regex> = OnceLock::new();
    let re = RE.get_or_init(|| Regex::new(r"(?i)\b([a-z][a-z0-9]{0,9})-(\d+)\b").unwrap());
    let keys: BTreeSet<String> = texts
        .iter()
        .flat_map(|t| re.captures_iter(t))
        .map(|c| format!("{}-{}", c[1].to_uppercase(), &c[2]))
        .collect();
    keys.into_iter().collect()
}

fn s<'a>(v: &'a Value, path: &[&str]) -> &'a str {
    path.iter()
        .try_fold(v, |v, k| v.get(*k))
        .and_then(Value::as_str)
        .unwrap_or("")
}

/// Map a webhook payload to (evidence kind, item keys, url). None = not something we act on.
fn interpret(p: &Value) -> Option<(&'static str, Vec<String>, String)> {
    if p.get("pull_request").is_some() {
        let pr = &p["pull_request"];
        let kind = match (s(p, &["action"]), pr["merged"].as_bool().unwrap_or(false)) {
            ("opened" | "reopened" | "ready_for_review", _) => "pr_opened",
            ("closed", true) => "pr_merged",
            _ => return None,
        };
        let keys = extract_keys(&[s(pr, &["title"]), s(pr, &["head", "ref"]), s(pr, &["body"])]);
        return Some((kind, keys, s(pr, &["html_url"]).to_string()));
    }
    if p.get("deployment_status").is_some() && s(p, &["deployment_status", "state"]) == "success" {
        let keys = extract_keys(&[
            s(p, &["deployment", "ref"]),
            s(p, &["deployment", "description"]),
            s(p, &["deployment_status", "description"]),
        ]);
        let url = match s(p, &["deployment_status", "target_url"]) {
            "" => s(p, &["deployment_status", "log_url"]),
            u => u,
        };
        return Some(("deploy_succeeded", keys, url.to_string()));
    }
    None
}

/// Verify the signature with the workspace's `github_webhook_secret` and record evidence for the items the
/// event mentions. Bad or missing signatures are rejected before the body is parsed.
pub async fn handle_github_webhook(
    pool: &PgPool,
    registry: &Arc<Registry>,
    workspace_id: Uuid,
    signature_header: &str,
    body: &[u8],
) -> CmdResult<Value> {
    let mut tx = cnote_db::begin_tenant(pool, workspace_id).await?;
    let secret: Option<String> = sqlx::query_scalar(
        "select settings->>'github_webhook_secret' from core.workspaces where id = $1",
    )
    .bind(workspace_id)
    .fetch_optional(&mut *tx)
    .await?
    .flatten();
    tx.commit().await?;
    let unauth = |m: &str| CmdError::new(ErrorCode::Unauthenticated, m);
    let secret = secret
        .filter(|s| !s.is_empty())
        .ok_or_else(|| unauth("github webhook is not configured for this workspace"))?;
    if !signature_ok(&secret, signature_header, body) {
        return Err(unauth("invalid webhook signature"));
    }
    let payload: Value = serde_json::from_slice(body)
        .map_err(|e| CmdError::invalid(format!("body is not JSON: {e}")))?;
    let Some((kind, keys, url)) = interpret(&payload) else {
        return Ok(json!({"ignored": true}));
    };
    if keys.is_empty() {
        return Ok(json!({"ignored": true, "reason": "no item keys found"}));
    }
    registry
        .execute(
            pool,
            "evidence.record",
            json!({"source": "github", "kind": kind, "item_keys": keys, "url": url}),
            &Principal::system(workspace_id),
            &ExecOptions::default(),
        )
        .await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hmac_matches_rfc4231() {
        let mac = hmac_sha256(b"Jefe", b"what do ya want for nothing?");
        assert_eq!(
            hex::encode(mac),
            "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
        );
        // key longer than the block size is hashed first (RFC 4231 case 6)
        let mac = hmac_sha256(
            &[0xaa; 131],
            b"Test Using Larger Than Block-Size Key - Hash Key First",
        );
        assert_eq!(
            hex::encode(mac),
            "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54"
        );
    }

    #[test]
    fn signature_verification() {
        let body = b"{}";
        let sig = format!("sha256={}", hex::encode(hmac_sha256(b"k", body)));
        assert!(signature_ok("k", &sig, body));
        assert!(!signature_ok("other", &sig, body));
        assert!(!signature_ok("k", &sig, b"{ }"));
        assert!(!signature_ok("k", "", body));
        assert!(!signature_ok("k", "sha256=zz", body));
        assert!(!signature_ok("k", &sig.replace("sha256=", "sha1="), body));
    }

    #[test]
    fn key_extraction() {
        assert_eq!(
            extract_keys(&["Fix ENG-12 and eng-12, also OPS-3"]),
            vec!["ENG-12", "OPS-3"]
        );
        assert_eq!(extract_keys(&["feature/eng-7-login-page"]), vec!["ENG-7"]);
        assert!(extract_keys(&["no keys here", "v1.2"]).is_empty());
    }

    #[test]
    fn payloads() {
        let merged = json!({"action": "closed", "pull_request": {"merged": true, "title": "ENG-5 add x", "head": {"ref": "b"}, "body": null, "html_url": "https://example.test/pr/1"}});
        let (k, keys, url) = interpret(&merged).unwrap();
        assert_eq!(
            (k, keys, url.as_str()),
            (
                "pr_merged",
                vec!["ENG-5".to_string()],
                "https://example.test/pr/1"
            )
        );
        let closed =
            json!({"action": "closed", "pull_request": {"merged": false, "title": "ENG-5"}});
        assert!(interpret(&closed).is_none());
        let opened =
            json!({"action": "opened", "pull_request": {"title": "t", "head": {"ref": "eng-9-x"}}});
        assert_eq!(interpret(&opened).unwrap().0, "pr_opened");
        let dep = json!({"deployment_status": {"state": "success", "target_url": "https://x.test"}, "deployment": {"ref": "release/ENG-2"}});
        assert_eq!(interpret(&dep).unwrap().0, "deploy_succeeded");
        let fail =
            json!({"deployment_status": {"state": "failure"}, "deployment": {"ref": "ENG-2"}});
        assert!(interpret(&fail).is_none());
        assert!(interpret(&json!({"zen": "hi"})).is_none());
    }
}
