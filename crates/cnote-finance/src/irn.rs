//! e-invoice IRN through a GSP (ADR-008). `SandboxGsp` is deterministic and offline; real GSPs implement `GspClient`.

use base64::Engine;
use chrono::{DateTime, NaiveDate, Utc};
use sha2::{Digest, Sha256};
use std::sync::{Arc, OnceLock};

#[derive(Debug, Clone)]
pub struct IrnRequest {
    pub supplier_gstin: String,
    /// "INV" or "CRN"
    pub doc_type: &'static str,
    pub number: String,
    pub financial_year: String,
    pub invoice_date: NaiveDate,
    pub buyer_gstin: String,
    pub total_minor: i64,
}

#[derive(Debug, Clone)]
pub struct IrnResponse {
    pub irn: String,
    pub ack_no: String,
    pub ack_date: DateTime<Utc>,
    pub signed_qr: String,
}

#[async_trait::async_trait]
pub trait GspClient: Send + Sync {
    async fn generate_irn(&self, req: &IrnRequest) -> anyhow::Result<IrnResponse>;
    async fn cancel_irn(&self, irn: &str, reason: &str) -> anyhow::Result<()>;
}

/// IRN = sha256(supplier GSTIN + doc type + doc number + financial year), as in the NIC spec.
pub fn irn_hash(r: &IrnRequest) -> String {
    let s = format!(
        "{}{}{}{}",
        r.supplier_gstin, r.doc_type, r.number, r.financial_year
    );
    hex::encode(Sha256::digest(s.as_bytes()))
}

pub struct SandboxGsp;

#[async_trait::async_trait]
impl GspClient for SandboxGsp {
    async fn generate_irn(&self, r: &IrnRequest) -> anyhow::Result<IrnResponse> {
        let irn = irn_hash(r);
        let ack_no = (u64::from_str_radix(&irn[..12], 16)? % 1_000_000_000_000_000).to_string();
        // Not a real NIC-signed JWT: payload.signature where the signature is a plain hash.
        let payload = serde_json::json!({
            "SellerGstin": r.supplier_gstin, "BuyerGstin": r.buyer_gstin, "DocNo": r.number,
            "DocTyp": r.doc_type, "DocDt": r.invoice_date.format("%d/%m/%Y").to_string(),
            "TotInvVal": r.total_minor, "Irn": irn,
        })
        .to_string();
        let b64 = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(&payload);
        let sig = hex::encode(Sha256::digest(format!("sandbox.{b64}")));
        Ok(IrnResponse {
            irn,
            ack_no,
            ack_date: Utc::now(),
            signed_qr: format!("{b64}.{sig}"),
        })
    }
    async fn cancel_irn(&self, _irn: &str, _reason: &str) -> anyhow::Result<()> {
        Ok(())
    }
}

static GSP: OnceLock<Arc<dyn GspClient>> = OnceLock::new();

/// Install a production GSP once at startup; otherwise the sandbox is used.
pub fn set_gsp(c: Arc<dyn GspClient>) -> bool {
    GSP.set(c).is_ok()
}

pub fn gsp() -> Arc<dyn GspClient> {
    GSP.get_or_init(|| Arc::new(SandboxGsp)).clone()
}

#[derive(Debug, PartialEq, Eq)]
pub enum Window {
    NotEnforced,
    Open,
    /// Day 20-28: report soon.
    Warn {
        days_left: i64,
    },
    /// Day 29 onward: too close to the 30-day limit to risk it.
    Blocked,
}

pub const WARN_DAY: i64 = 20;
pub const BLOCK_DAY: i64 = 29;

/// 30-day IRN reporting window for taxpayers with AATO above Rs 10 crore.
pub fn window(invoice_date: NaiveDate, today: NaiveDate, enforced: bool) -> Window {
    if !enforced {
        return Window::NotEnforced;
    }
    let days = (today - invoice_date).num_days();
    match days {
        d if d >= BLOCK_DAY => Window::Blocked,
        d if d >= WARN_DAY => Window::Warn { days_left: 30 - d },
        _ => Window::Open,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn day(n: i64) -> (NaiveDate, NaiveDate) {
        let d = NaiveDate::from_ymd_opt(2026, 11, 1).unwrap();
        (d, d + chrono::Duration::days(n))
    }

    #[test]
    fn window_boundaries() {
        let w = |n, e| {
            let (inv, today) = day(n);
            window(inv, today, e)
        };
        assert_eq!(w(0, true), Window::Open);
        assert_eq!(w(19, true), Window::Open);
        assert_eq!(w(20, true), Window::Warn { days_left: 10 });
        assert_eq!(w(28, true), Window::Warn { days_left: 2 });
        assert_eq!(w(29, true), Window::Blocked);
        assert_eq!(w(45, true), Window::Blocked);
        assert_eq!(w(45, false), Window::NotEnforced);
        // an invoice dated in the future is simply open
        assert_eq!(w(-3, true), Window::Open);
    }

    #[tokio::test]
    async fn sandbox_is_deterministic() {
        let r = IrnRequest {
            supplier_gstin: "27AAPFU0939F1ZV".into(),
            doc_type: "INV",
            number: "INV/2026-27/0001".into(),
            financial_year: "2026-27".into(),
            invoice_date: NaiveDate::from_ymd_opt(2026, 11, 1).unwrap(),
            buyer_gstin: "29AABCT1332L1ZS".into(),
            total_minor: 118_000,
        };
        let (a, b) = (
            SandboxGsp.generate_irn(&r).await.unwrap(),
            SandboxGsp.generate_irn(&r).await.unwrap(),
        );
        assert_eq!(a.irn, b.irn);
        assert_eq!(a.ack_no, b.ack_no);
        assert_eq!(
            a.irn,
            hex::encode(Sha256::digest(b"27AAPFU0939F1ZVINVINV/2026-27/00012026-27"))
        );
        let mut r2 = r.clone();
        r2.doc_type = "CRN";
        assert_ne!(SandboxGsp.generate_irn(&r2).await.unwrap().irn, a.irn);
        assert!(a.signed_qr.contains('.'));
    }
}
