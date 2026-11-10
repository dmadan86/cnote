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
    let mut map = BUCKETS.lock().unwrap();
    let now = Instant::now();
    let cap = capacity();
    let entry = map.entry(key.to_string()).or_insert((cap, now));
    entry.0 = (entry.0 + now.duration_since(entry.1).as_secs_f64() * refill()).min(cap);
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
