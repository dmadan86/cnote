//! ModelProvider adapters (ADR-009): Anthropic Messages API and OpenAI-compatible chat + embeddings
//! (which also covers self-hosted endpoints). Request/response mapping is in pure functions so it can be
//! tested against recorded JSON without a network.

use anyhow::{Context, anyhow, bail};
use cnote_core::ai::{
    Completion, CompletionRequest, EMBEDDING_DIMS, LocalProvider, ModelProvider, hash_embed,
};
use futures::future::BoxFuture;
use serde_json::{Value, json};
use std::sync::Arc;

pub const DEFAULT_ANTHROPIC_MODEL: &str = "claude-sonnet-5-5";
const DEFAULT_OPENAI_MODEL: &str = "gpt-4o-mini";
const DEFAULT_EMBEDDING_MODEL: &str = "text-embedding-3-small";

async fn post_json(
    http: &reqwest::Client,
    url: &str,
    headers: &[(&str, &str)],
    body: &Value,
) -> anyhow::Result<Value> {
    let mut rq = http.post(url).json(body);
    for (k, v) in headers {
        rq = rq.header(*k, *v);
    }
    let res = rq.send().await.context("model request failed")?;
    let status = res.status();
    let text = res.text().await.context("reading model response")?;
    if !status.is_success() {
        bail!(
            "model API returned {status}: {}",
            text.chars().take(300).collect::<String>()
        );
    }
    serde_json::from_str(&text).context("model API returned non-JSON")
}

// ---------- Anthropic ----------

pub struct AnthropicProvider {
    http: reqwest::Client,
    base_url: String,
    api_key: String,
    model: String,
}

impl AnthropicProvider {
    pub fn new(api_key: String, base_url: Option<String>, model: Option<String>) -> Self {
        Self {
            http: reqwest::Client::new(),
            base_url: base_url
                .unwrap_or_else(|| "https://api.anthropic.com".into())
                .trim_end_matches('/')
                .to_string(),
            api_key,
            model: model.unwrap_or_else(|| DEFAULT_ANTHROPIC_MODEL.into()),
        }
    }
}

pub fn anthropic_request(model: &str, req: &CompletionRequest) -> Value {
    let system: Vec<&str> = req
        .messages
        .iter()
        .filter(|m| m.role == "system")
        .map(|m| m.content.as_str())
        .collect();
    let messages: Vec<Value> = req
        .messages
        .iter()
        .filter(|m| m.role != "system")
        .map(|m| json!({"role": m.role, "content": m.content}))
        .collect();
    let mut body = json!({"model": model, "max_tokens": req.max_tokens, "messages": messages});
    if !system.is_empty() {
        body["system"] = json!(system.join("\n\n"));
    }
    body
}

pub fn parse_anthropic(v: &Value) -> anyhow::Result<Completion> {
    let text: String = v["content"]
        .as_array()
        .ok_or_else(|| anyhow!("Anthropic response has no content"))?
        .iter()
        .filter(|b| b["type"] == "text")
        .filter_map(|b| b["text"].as_str())
        .collect();
    Ok(Completion {
        text,
        provider: "anthropic".into(),
        model: v["model"].as_str().unwrap_or_default().into(),
        tokens_in: v["usage"]["input_tokens"].as_u64().unwrap_or(0) as u32,
        tokens_out: v["usage"]["output_tokens"].as_u64().unwrap_or(0) as u32,
    })
}

impl ModelProvider for AnthropicProvider {
    fn name(&self) -> &str {
        "anthropic"
    }
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>> {
        Box::pin(async move {
            if req.messages.iter().all(|m| m.role == "system") {
                bail!("no user message to send");
            }
            let v = post_json(
                &self.http,
                &format!("{}/v1/messages", self.base_url),
                &[
                    ("x-api-key", &self.api_key),
                    ("anthropic-version", "2023-06-01"),
                ],
                &anthropic_request(&self.model, &req),
            )
            .await?;
            parse_anthropic(&v)
        })
    }
    // Anthropic has no embeddings endpoint; `Composite` supplies them.
    fn embedding_model(&self) -> &str {
        "local-hash-1536"
    }
    fn embed(&self, texts: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>> {
        Box::pin(async move { Ok(texts.iter().map(|t| hash_embed(t)).collect()) })
    }
}

// ---------- OpenAI-compatible ----------

pub struct OpenAiProvider {
    http: reqwest::Client,
    base_url: String,
    api_key: Option<String>,
    model: String,
    embedding_model: String,
}

impl OpenAiProvider {
    pub fn new(
        api_key: Option<String>,
        base_url: Option<String>,
        model: Option<String>,
        embedding_model: Option<String>,
    ) -> Self {
        Self {
            http: reqwest::Client::new(),
            base_url: base_url
                .unwrap_or_else(|| "https://api.openai.com/v1".into())
                .trim_end_matches('/')
                .to_string(),
            api_key: api_key.filter(|k| !k.is_empty()),
            model: model.unwrap_or_else(|| DEFAULT_OPENAI_MODEL.into()),
            embedding_model: embedding_model.unwrap_or_else(|| DEFAULT_EMBEDDING_MODEL.into()),
        }
    }
    fn auth(&self) -> Vec<(&str, String)> {
        self.api_key
            .iter()
            .map(|k| ("authorization", format!("Bearer {k}")))
            .collect()
    }
}

pub fn openai_chat_request(model: &str, req: &CompletionRequest) -> Value {
    json!({
        "model": model,
        "max_tokens": req.max_tokens,
        "messages": req.messages.iter().map(|m| json!({"role": m.role, "content": m.content})).collect::<Vec<_>>(),
    })
}

pub fn parse_openai_chat(v: &Value) -> anyhow::Result<Completion> {
    let text = v["choices"][0]["message"]["content"]
        .as_str()
        .ok_or_else(|| anyhow!("OpenAI-compatible response has no message content"))?;
    Ok(Completion {
        text: text.into(),
        provider: "openai".into(),
        model: v["model"].as_str().unwrap_or_default().into(),
        tokens_in: v["usage"]["prompt_tokens"].as_u64().unwrap_or(0) as u32,
        tokens_out: v["usage"]["completion_tokens"].as_u64().unwrap_or(0) as u32,
    })
}

pub fn openai_embed_request(model: &str, texts: &[String]) -> Value {
    let mut body = json!({"model": model, "input": texts});
    // text-embedding-3-* are Matryoshka models: pin them to the column width.
    if model.starts_with("text-embedding-3") {
        body["dimensions"] = json!(EMBEDDING_DIMS);
    }
    body
}

pub fn parse_openai_embeddings(v: &Value, expected: usize) -> anyhow::Result<Vec<Vec<f32>>> {
    let mut rows: Vec<(u64, Vec<f32>)> = v["data"]
        .as_array()
        .ok_or_else(|| anyhow!("embeddings response has no data"))?
        .iter()
        .map(|d| {
            let e = d["embedding"]
                .as_array()
                .ok_or_else(|| anyhow!("embedding missing"))?
                .iter()
                .map(|x| {
                    x.as_f64()
                        .map(|f| f as f32)
                        .ok_or_else(|| anyhow!("bad float"))
                })
                .collect::<anyhow::Result<Vec<f32>>>()?;
            Ok((d["index"].as_u64().unwrap_or(0), e))
        })
        .collect::<anyhow::Result<_>>()?;
    rows.sort_by_key(|r| r.0);
    if rows.len() != expected {
        bail!("expected {expected} embeddings, got {}", rows.len());
    }
    if let Some((_, e)) = rows.iter().find(|(_, e)| e.len() != EMBEDDING_DIMS) {
        bail!(
            "embedding has {} dimensions, the index needs {EMBEDDING_DIMS}",
            e.len()
        );
    }
    Ok(rows.into_iter().map(|r| r.1).collect())
}

impl ModelProvider for OpenAiProvider {
    fn name(&self) -> &str {
        "openai"
    }
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>> {
        Box::pin(async move {
            let auth = self.auth();
            let headers: Vec<(&str, &str)> = auth.iter().map(|(k, v)| (*k, v.as_str())).collect();
            let v = post_json(
                &self.http,
                &format!("{}/chat/completions", self.base_url),
                &headers,
                &openai_chat_request(&self.model, &req),
            )
            .await?;
            parse_openai_chat(&v)
        })
    }
    fn embedding_model(&self) -> &str {
        &self.embedding_model
    }
    fn embed(&self, texts: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>> {
        Box::pin(async move {
            let auth = self.auth();
            let headers: Vec<(&str, &str)> = auth.iter().map(|(k, v)| (*k, v.as_str())).collect();
            let v = post_json(
                &self.http,
                &format!("{}/embeddings", self.base_url),
                &headers,
                &openai_embed_request(&self.embedding_model, &texts),
            )
            .await?;
            parse_openai_embeddings(&v, texts.len())
        })
    }
}

// ---------- composition ----------

/// Chat from one provider, embeddings from another (Anthropic chat + OpenAI-compatible embeddings).
pub struct Composite {
    pub chat: Arc<dyn ModelProvider>,
    pub embedder: Arc<dyn ModelProvider>,
}

impl ModelProvider for Composite {
    fn name(&self) -> &str {
        self.chat.name()
    }
    fn complete(&self, req: CompletionRequest) -> BoxFuture<'_, anyhow::Result<Completion>> {
        self.chat.complete(req)
    }
    fn embedding_model(&self) -> &str {
        self.embedder.embedding_model()
    }
    fn embed(&self, texts: Vec<String>) -> BoxFuture<'_, anyhow::Result<Vec<Vec<f32>>>> {
        self.embedder.embed(texts)
    }
}

/// Process-wide provider from the environment. With no keys it is the deterministic `LocalProvider`.
pub fn provider_from_env() -> Arc<dyn ModelProvider> {
    let env = |k: &str| std::env::var(k).ok().filter(|v| !v.trim().is_empty());
    let model = env("AI_MODEL");
    let openai = (env("OPENAI_API_KEY").is_some() || env("OPENAI_BASE_URL").is_some()).then(|| {
        OpenAiProvider::new(
            env("OPENAI_API_KEY"),
            env("OPENAI_BASE_URL"),
            model.clone(),
            env("OPENAI_EMBEDDING_MODEL"),
        )
    });
    match (env("ANTHROPIC_API_KEY"), openai) {
        (Some(key), embeddings) => {
            let chat = Arc::new(AnthropicProvider::new(
                key,
                env("ANTHROPIC_BASE_URL"),
                model,
            ));
            match embeddings {
                Some(o) => Arc::new(Composite {
                    chat,
                    embedder: Arc::new(o),
                }),
                None => chat,
            }
        }
        (None, Some(o)) => Arc::new(o),
        (None, None) => Arc::new(LocalProvider),
    }
}

/// A workspace's own endpoint (BYO-LLM). Anthropic workspaces get hashed embeddings: no embeddings API there.
pub fn provider_for(
    provider: &str,
    api_key: Option<String>,
    base_url: Option<String>,
    model: Option<String>,
    embedding_model: Option<String>,
) -> Arc<dyn ModelProvider> {
    match provider {
        "anthropic" => Arc::new(AnthropicProvider::new(
            api_key.unwrap_or_default(),
            base_url,
            model,
        )),
        _ => Arc::new(OpenAiProvider::new(
            api_key,
            base_url,
            model,
            embedding_model,
        )),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use cnote_core::ai::ChatMessage;

    fn req() -> CompletionRequest {
        CompletionRequest {
            purpose: "draft".into(),
            max_tokens: 256,
            messages: vec![
                ChatMessage {
                    role: "system".into(),
                    content: "Be brief.".into(),
                },
                ChatMessage {
                    role: "user".into(),
                    content: "Summarise the note.".into(),
                },
            ],
        }
    }

    #[test]
    fn anthropic_maps_system_and_messages() {
        let b = anthropic_request("claude-sonnet-5-5", &req());
        assert_eq!(
            b,
            json!({
                "model": "claude-sonnet-5-5",
                "max_tokens": 256,
                "system": "Be brief.",
                "messages": [{"role": "user", "content": "Summarise the note."}]
            })
        );
    }

    #[test]
    fn anthropic_response_recorded() {
        let recorded = json!({
            "id": "msg_01", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
            "content": [{"type": "text", "text": "Short "}, {"type": "tool_use", "id": "x", "name": "n", "input": {}}, {"type": "text", "text": "summary."}],
            "stop_reason": "end_turn", "usage": {"input_tokens": 41, "output_tokens": 7}
        });
        let c = parse_anthropic(&recorded).unwrap();
        assert_eq!(
            (c.text.as_str(), c.tokens_in, c.tokens_out),
            ("Short summary.", 41, 7)
        );
        assert_eq!(
            (c.provider.as_str(), c.model.as_str()),
            ("anthropic", "claude-sonnet-5-5")
        );
        assert!(parse_anthropic(&json!({"error": {}})).is_err());
    }

    #[test]
    fn openai_chat_round_trip() {
        let b = openai_chat_request("gpt-4o-mini", &req());
        assert_eq!(b["messages"][0]["role"], "system");
        assert_eq!(b["max_tokens"], 256);
        let recorded = json!({
            "id": "chatcmpl-1", "model": "gpt-4o-mini-2024-07-18",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "Done."}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 30, "completion_tokens": 2, "total_tokens": 32}
        });
        let c = parse_openai_chat(&recorded).unwrap();
        assert_eq!(
            (c.text.as_str(), c.tokens_in, c.tokens_out),
            ("Done.", 30, 2)
        );
        assert!(parse_openai_chat(&json!({"choices": []})).is_err());
    }

    #[test]
    fn openai_embeddings_sorted_and_validated() {
        let vec_of = |x: f32| vec![x; EMBEDDING_DIMS];
        let recorded = json!({"data": [
            {"index": 1, "embedding": vec_of(0.2)},
            {"index": 0, "embedding": vec_of(0.1)},
        ], "usage": {"prompt_tokens": 4}});
        let out = parse_openai_embeddings(&recorded, 2).unwrap();
        assert_eq!((out[0][0], out[1][0]), (0.1, 0.2));
        assert!(
            parse_openai_embeddings(&recorded, 3).is_err(),
            "count mismatch"
        );
        let short = json!({"data": [{"index": 0, "embedding": [0.1, 0.2]}]});
        assert!(parse_openai_embeddings(&short, 1).is_err(), "wrong width");

        let texts = vec!["a".to_string()];
        assert_eq!(
            openai_embed_request("text-embedding-3-small", &texts)["dimensions"],
            1536
        );
        assert!(
            openai_embed_request("nomic-embed", &texts)
                .get("dimensions")
                .is_none()
        );
    }

    #[test]
    fn env_without_keys_is_local() {
        // Only meaningful on a machine with no provider keys; skip otherwise.
        if ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL"]
            .iter()
            .any(|k| std::env::var(k).is_ok())
        {
            return;
        }
        assert_eq!(provider_from_env().name(), "local");
    }
}
