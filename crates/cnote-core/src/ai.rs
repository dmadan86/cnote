//! Model-agnostic AI interface (ADR-009). Adapters live in `cnote-ai`; modules only see this trait.
//! The deterministic `LocalProvider` is the default when no provider key is configured (dev, CI, tests).

use futures::future::BoxFuture;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChatMessage {
    /// "system" | "user" | "assistant"
    pub role: String,
    pub content: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CompletionRequest {
    /// Purpose label for the audit log and model routing: "classify", "draft", "answer", "propose_edit"
    pub purpose: String,
    pub messages: Vec<ChatMessage>,
    pub max_tokens: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Completion {
    pub text: String,
    pub provider: String,
    pub model: String,
    pub tokens_in: u32,
    pub tokens_out: u32,
}

pub const EMBEDDING_DIMS: usize = 1536;

pub trait ModelProvider: Send + Sync {
    fn name(&self) -> &str;
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>>;
    /// Embedding model id; indexes are versioned by it.
    fn embedding_model(&self) -> &str;
    /// Vectors of EMBEDDING_DIMS length.
    fn embed(&self, texts: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>>;
}

/// Deterministic offline provider: extractive "completions" and hashed bag-of-words embeddings.
pub struct LocalProvider;

impl ModelProvider for LocalProvider {
    fn name(&self) -> &str {
        "local"
    }
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>> {
        Box::pin(async move {
            let last = req
                .messages
                .iter()
                .rev()
                .find(|m| m.role == "user")
                .map(|m| m.content.clone())
                .unwrap_or_default();
            let text: String = last
                .chars()
                .take((req.max_tokens as usize * 4).min(2000))
                .collect();
            Ok(Completion {
                tokens_in: (last.len() / 4) as u32,
                tokens_out: (text.len() / 4) as u32,
                text,
                provider: "local".into(),
                model: "local-extractive".into(),
            })
        })
    }
    fn embedding_model(&self) -> &str {
        "local-hash-1536"
    }
    fn embed(&self, texts: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>> {
        Box::pin(async move { Ok(texts.iter().map(|t| hash_embed(t)).collect()) })
    }
}

/// Hashed bag-of-words, L2-normalised. Good enough for tests and offline dev; not semantic.
pub fn hash_embed(text: &str) -> Vec<f32> {
    use sha2::{Digest, Sha256};
    let mut v = vec![0f32; EMBEDDING_DIMS];
    for w in text
        .to_lowercase()
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| w.len() > 2)
    {
        let h = Sha256::digest(w.as_bytes());
        let idx = u16::from_le_bytes([h[0], h[1]]) as usize % EMBEDDING_DIMS;
        v[idx] += if h[2] & 1 == 0 { 1.0 } else { -1.0 };
    }
    let norm = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 0.0 {
        v.iter_mut().for_each(|x| *x /= norm);
    }
    v
}
