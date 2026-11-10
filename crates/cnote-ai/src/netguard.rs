//! SSRF guard for workspace-supplied (BYO-LLM) endpoints.

use std::net::IpAddr;

/// Self-hosted editions may point at private model servers; multi-tenant SaaS must not.
fn private_allowed() -> bool {
    std::env::var("AI_ALLOW_PRIVATE_ENDPOINTS").is_ok_and(|v| v == "1" || v == "true")
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

/// Validate a BYO base URL: https, no credentials, and every resolved address public.
/// shortcut: checks DNS at save time and before each call but does not pin the IP; pin with
/// reqwest's `resolve` if DNS-rebinding becomes a concern.
pub async fn check_endpoint(raw: &str) -> Result<(), String> {
    let url = url::Url::parse(raw).map_err(|e| format!("invalid base_url: {e}"))?;
    if !url.username().is_empty() || url.password().is_some() {
        return Err("base_url must not contain credentials".into());
    }
    if private_allowed() {
        return matches!(url.scheme(), "https" | "http")
            .then_some(())
            .ok_or_else(|| "base_url must be http(s)".into());
    }
    if url.scheme() != "https" {
        return Err("base_url must use https".into());
    }
    let host = url.host_str().ok_or("base_url has no host")?;
    let port = url.port_or_known_default().unwrap_or(443);
    let addrs: Vec<_> = tokio::net::lookup_host((host, port))
        .await
        .map_err(|_| "base_url host does not resolve".to_string())?
        .collect();
    if addrs.is_empty() || addrs.iter().any(|a| !is_public(a.ip())) {
        return Err("base_url must resolve to public addresses only".into());
    }
    Ok(())
}

/// HTTP client for model APIs: no redirects (a 3xx must not bounce us to an internal host) and a timeout.
pub fn client() -> reqwest::Client {
    reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(std::time::Duration::from_secs(120))
        .build()
        .expect("reqwest client")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn rejects_internal_and_insecure_endpoints() {
        for bad in [
            "http://api.example.com",
            "https://127.0.0.1/v1",
            "https://169.254.169.254/latest",
            "https://10.0.0.5",
            "https://[::1]/v1",
            "https://user:pw@api.openai.com",
            "https://localhost:8080",
        ] {
            assert!(check_endpoint(bad).await.is_err(), "{bad} should be rejected");
        }
    }

    #[test]
    fn classifies_addresses() {
        assert!(is_public("8.8.8.8".parse().unwrap()));
        assert!(!is_public("192.168.1.1".parse().unwrap()));
        assert!(!is_public("100.64.0.1".parse().unwrap()));
        assert!(!is_public("::ffff:10.0.0.1".parse().unwrap()));
        assert!(!is_public("fd00::1".parse().unwrap()));
    }
}
