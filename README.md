# C Note

Notes, projects, docs, CRM and GST invoicing on one object model, with an MCP-first API.
Architecture decisions: `docs/adr/` (ADR-001 to ADR-014 from the design doc, ADR-015 Rust backend).

## Layout
```
migrations/          SQL migrations (RLS on every tenant table)
crates/cnote-db      pool, tenant transactions, migrator
crates/cnote-core    command layer, principals, auth, events, notes/spaces/relations
crates/cnote-*       modules: finance, projects, docs, ai, mcp, importers, collab
crates/cnote-server  `cnote` binary: serve | worker | migrate
apps/web             Next.js client
e2e/                 Playwright end-to-end tests
```

## Local setup
Requires Rust (stable), Node 22+, pnpm, Postgres 17 with pgvector.
```sh
cp .env.example .env            # fill in values; .env is gitignored, never commit secrets
scripts/setup-db.sh             # creates DB, migrates, sets the app role password
cargo run -p cnote-server -- serve --with-worker   # API on :4000
pnpm install && pnpm dev:web    # web on :3000
cargo test                      # unit + integration tests (needs the DB)
pnpm e2e                        # Playwright
```
