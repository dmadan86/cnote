-- ADR-009: BYO-LLM per workspace and hybrid-search indexes.

-- A workspace's own model endpoint. The key is AES-256-GCM encrypted (nonce || ciphertext) with CNOTE_MASTER_KEY
-- and is never returned by any command. With a row here C Note does not meter tokens.
CREATE TABLE ai.provider_config (
  workspace_id uuid PRIMARY KEY REFERENCES core.workspaces(id) ON DELETE CASCADE,
  provider text NOT NULL CHECK (provider IN ('anthropic','openai')),  -- 'openai' = any OpenAI-compatible endpoint
  base_url text,
  model text,
  embedding_model text,
  key_enc bytea,
  updated_by uuid REFERENCES core.users(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);
SELECT core.enable_tenant_rls('ai.provider_config');

-- Cosine ANN for the vector half of search.hybrid; the btree serves the per-model replace on re-index.
CREATE INDEX chunks_embedding_hnsw ON ai.chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX chunks_note_model ON ai.chunks(note_id, model);
CREATE INDEX audit_ws_created ON ai.audit(workspace_id, created_at);
