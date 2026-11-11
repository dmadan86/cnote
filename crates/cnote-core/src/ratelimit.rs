use crate::error::{CmdError, CmdResult, ErrorCode};
use std::collections::HashMap;
use std::sync::{LazyLock, Mutex};
use std::time::Instant;

// Per-seat fair-use limit (ADR-011): throttled, never billed.
// shortcut: in-process token bucket per instance; move to Postgres/Redis when running several API instances.
static BUCKETS: LazyLock<Mutex<HashMap<String, (f64, Instant)>>> = LazyLock::new(Default::default);

fn capacity() -> f64 {
    std::env::var("FAIR_USE_BURST")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(240.0)
}
fn refill() -> f64 {
    std::env::var("FAIR_USE_PER_SEC")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(20.0)
}

pub fn check(key: &str) -> CmdResult<()> {
    check_with(key, capacity(), refill())
}

/// Same bucket store with explicit burst and refill/sec (unauthenticated endpoints need tighter limits).
pub fn check_with(key: &str, cap: f64, refill: f64) -> CmdResult<()> {
    let key = &key[..key.len().min(128)]; // attacker-influenced keys must not grow memory per request
    let mut map = BUCKETS.lock().unwrap();
    let now = Instant::now();
    // Idle for 10 minutes means every bucket in use has long refilled, so dropping it loses nothing.
    if map.len() > 1024 {
        map.retain(|_, (_, last)| now.duration_since(*last).as_secs() < 600);
    }
    let entry = map.entry(key.to_string()).or_insert((cap, now));
    entry.0 = (entry.0 + now.duration_since(entry.1).as_secs_f64() * refill).min(cap);
    entry.1 = now;
    if entry.0 < 1.0 {
        return Err(CmdError::new(
            ErrorCode::RateLimited,
            "fair-use rate limit reached, retry shortly",
        ));
    }
    entry.0 -= 1.0;
    Ok(())
}
