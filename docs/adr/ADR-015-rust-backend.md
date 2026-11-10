# ADR-015 Rust backend, TypeScript only in the browser (amends ADR-010)

Status: Accepted, 11 Nov 2026 (Madan: "use Rust as much as possible").

## Decision
All server-side code is Rust: one Cargo workspace, one binary `cnote` with three entrypoints
(`serve`, `worker`, `migrate`), matching ADR-001's "same image, different entrypoint".
The browser client stays Next.js/React/TypeScript because the editor (Tiptap/ProseMirror),
CRDT bindings (Yjs) and canvas SDK (tldraw) are TypeScript-native. The collaboration gateway is
Rust using `yrs` (the Rust port of Yjs), so the wire protocol stays Yjs-compatible.

| Concern | Choice |
|---|---|
| HTTP | axum 0.8 |
| Database | Postgres 17 + pgvector, sqlx 0.9 (runtime queries, no ORM), SQL migrations in `/migrations` |
| Tenancy | RLS on every tenant table, `app.workspace_id` set per transaction (ADR-003) |
| Command layer | `cnote-core::Registry`: typed commands with JSON Schema (schemars), tool groups, idempotency, confirmations |
| MCP | `rmcp` (official Rust SDK), Streamable HTTP at `/mcp` + stdio |
| CRDT | `yrs` gateway at `/collab/{note}`; Markdown snapshot written to `core.notes.content_md` |
| AI | `ModelProvider` trait, reqwest adapters (Anthropic, OpenAI-compatible), deterministic local provider |
| Jobs | Postgres queue (`core.jobs`, SKIP LOCKED) + transactional outbox from `core.events` |
| Web | Next.js 16 App Router; `/api/*` rewritten to the Rust API so cookies stay first-party |

## Module boundaries (ADR-001)
Each module is a crate (`cnote-finance`, `cnote-projects`, `cnote-docs`, `cnote-ai`, `cnote-mcp`,
`cnote-importers`, `cnote-collab`). Modules depend on `cnote-core` and `cnote-db` only; only
`cnote-server` depends on everything. The Cargo dependency graph enforces the boundary, no lint rule needed.
Each module owns a Postgres schema and a migration number range:
0001-0099 core, 0100 finance+crm, 0200 projects+workflow, 0300 docs, 0400 ai, 0500 importers.

## Hosting
Web on Vercel. Rust API + worker + Postgres on Railway (or Fly/AWS ap-south-1 later); Neon is an
alternative Postgres (pgvector + RLS supported). Decided when we deploy.

## Consequences
Two toolchains (cargo, pnpm) instead of one. Shared types between client and server come from the
JSON Schemas served at `GET /api/commands`, not from shared TypeScript.
