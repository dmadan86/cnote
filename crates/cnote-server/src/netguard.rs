//! SSRF guard for fetching OAuth client metadata documents (client-supplied URLs). Adapted from
//! cnote-ai's netguard, but it also pins the connection to the addresses it checked, closing the
//! DNS-rebinding gap between "check" and "connect".

use std::net::{IpAddr, SocketAddr};

/// Test/dev escape hatch: lets the fetch reach http and private addresses. Never set in production.
fn private_allowed() -> bool {
    std::env::var("OAUTH_CIMD_ALLOW_PRIVATE").is_ok_and(|v| v == "1" || v == "true")
}

pub fn is_public(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v) => {
            let o = v.octets();
            !(v.is_loopback()
                || v.is_private()
                || v.is_link_local()
                || v.is_unspecified()
                || v.is_broadcast()
                || v.is_documentation()
                || o[0] == 0
                || (o[0] == 100 && (64..=127).contains(&o[1])) // CGNAT
                || (o[0] == 198 && (o[1] == 18 || o[1] == 19)))
        }
        IpAddr::V6(v) => {
            if let Some(m) = v.to_ipv4_mapped() {
                return is_public(IpAddr::V4(m));
            }
            let s = v.segments();
            !(v.is_loopback()
                || v.is_unspecified()
                || (s[0] & 0xfe00) == 0xfc00 // unique local
                || (s[0] & 0xffc0) == 0xfe80) // link local
        }
    }
}

/// GET a JSON document with no redirects, a short timeout and a hard size cap.
pub async fn fetch_json(raw: &str, max_bytes: usize) -> Result<serde_json::Value, String> {
    let url = url::Url::parse(raw).map_err(|_| "invalid URL".to_string())?;
    if !url.username().is_empty() || url.password().is_some() {
        return Err("URL must not contain credentials".into());
    }
    let allow_private = private_allowed();
    if url.scheme() != "https" && !(allow_private && url.scheme() == "http") {
        return Err("URL must use https".into());
    }
    let host = url.host_str().ok_or("URL has no host")?.to_string();
    let port = url.port_or_known_default().ok_or("URL has no port")?;
    let addrs: Vec<SocketAddr> = tokio::net::lookup_host((host.as_str(), port))
        .await
        .map_err(|_| "host does not resolve".to_string())?
        .collect();
    if addrs.is_empty() || (!allow_private && addrs.iter().any(|a| !is_public(a.ip()))) {
        return Err("host must resolve to public addresses only".into());
    }
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(std::time::Duration::from_secs(5))
        .resolve_to_addrs(&host, &addrs)
        .build()
        .map_err(|e| e.to_string())?;
    let mut res = client
        .get(url)
        .header("accept", "application/json")
        .send()
        .await
        .map_err(|_| "metadata fetch failed".to_string())?;
    if !res.status().is_success() {
        return Err(format!("metadata fetch returned {}", res.status()));
    }
    let mut body = Vec::new();
    while let Some(chunk) = res.chunk().await.map_err(|_| "metadata read failed")? {
        body.extend_from_slice(&chunk);
        if body.len() > max_bytes {
            return Err("metadata document too large".into());
        }
    }
    serde_json::from_slice(&body).map_err(|_| "metadata is not valid JSON".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_addresses() {
        assert!(is_public("8.8.8.8".parse().unwrap()));
        assert!(!is_public("127.0.0.1".parse().unwrap()));
        assert!(!is_public("169.254.169.254".parse().unwrap()));
        assert!(!is_public("100.64.0.1".parse().unwrap()));
        assert!(!is_public("::ffff:10.0.0.1".parse().unwrap()));
        assert!(!is_public("fd00::1".parse().unwrap()));
    }

    #[tokio::test]
    async fn rejects_internal_and_insecure_urls() {
        for bad in [
            "http://example.com/c.json",
            "https://127.0.0.1/c.json",
            "https://169.254.169.254/latest",
            "https://[::1]/c.json",
            "https://localhost/c.json",
            "https://u@example.com/c.json",
        ] {
            assert!(fetch_json(bad, 1024).await.is_err(), "{bad}");
        }
    }
}
