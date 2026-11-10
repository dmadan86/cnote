//! AES-256-GCM envelope for BYO provider keys. Stored as nonce(12) || ciphertext; the workspace id is bound as AAD
//! so a blob copied to another workspace's row will not decrypt.

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::Engine;
use cnote_core::CmdError;
use rand::RngCore;
use uuid::Uuid;

const NONCE_LEN: usize = 12;

pub fn master_key() -> Result<[u8; 32], CmdError> {
    let raw = std::env::var("CNOTE_MASTER_KEY")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .ok_or_else(|| {
            CmdError::invalid("CNOTE_MASTER_KEY is not set; refusing to store provider keys")
        })?;
    parse_key(&raw)
}

pub fn parse_key(b64: &str) -> Result<[u8; 32], CmdError> {
    base64::engine::general_purpose::STANDARD
        .decode(b64.trim())
        .ok()
        .and_then(|b| <[u8; 32]>::try_from(b).ok())
        .ok_or_else(|| CmdError::invalid("CNOTE_MASTER_KEY must be 32 bytes, base64 encoded"))
}

pub fn encrypt(key: &[u8; 32], ws: Uuid, plaintext: &str) -> Result<Vec<u8>, CmdError> {
    let mut nonce = [0u8; NONCE_LEN];
    rand::rng().fill_bytes(&mut nonce);
    let ct = Aes256Gcm::new(key.into())
        .encrypt(
            &Nonce::from(nonce),
            Payload {
                msg: plaintext.as_bytes(),
                aad: ws.as_bytes(),
            },
        )
        .map_err(|_| CmdError::internal("encryption failed"))?;
    Ok([nonce.as_slice(), &ct].concat())
}

pub fn decrypt(key: &[u8; 32], ws: Uuid, blob: &[u8]) -> Result<String, CmdError> {
    let bad =
        || CmdError::internal("cannot decrypt the workspace provider key (wrong master key?)");
    if blob.len() <= NONCE_LEN {
        return Err(bad());
    }
    let (nonce, ct) = blob.split_at(NONCE_LEN);
    let pt = Aes256Gcm::new(key.into())
        .decrypt(
            &Nonce::try_from(nonce).map_err(|_| bad())?,
            Payload {
                msg: ct,
                aad: ws.as_bytes(),
            },
        )
        .map_err(|_| bad())?;
    String::from_utf8(pt).map_err(|_| bad())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip_and_binding() {
        let key = [7u8; 32];
        let ws = Uuid::new_v4();
        let secret = Uuid::new_v4().to_string();
        let blob = encrypt(&key, ws, &secret).unwrap();
        assert!(!blob.windows(secret.len()).any(|w| w == secret.as_bytes()));
        assert_eq!(decrypt(&key, ws, &blob).unwrap(), secret);
        assert!(
            decrypt(&key, Uuid::new_v4(), &blob).is_err(),
            "AAD binds the workspace"
        );
        assert!(decrypt(&[8u8; 32], ws, &blob).is_err(), "wrong key");
        let mut tampered = blob.clone();
        *tampered.last_mut().unwrap() ^= 1;
        assert!(decrypt(&key, ws, &tampered).is_err());
    }

    #[test]
    fn key_parsing() {
        use base64::Engine;
        let ok = base64::engine::general_purpose::STANDARD.encode([1u8; 32]);
        assert!(parse_key(&ok).is_ok());
        assert!(parse_key("c2hvcnQ=").is_err());
        assert!(parse_key("not base64!").is_err());
    }
}
