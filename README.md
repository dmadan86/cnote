# C Note

Notes, projects, docs, CRM and GST invoicing on one object model, with an MCP-first API.
Architecture decisions: `docs/adr/` (ADR-001 to ADR-014 from the design doc; ADR-015 is the retired Rust backend).

## Layout
```
migrations/          SQL migrations (RLS on every tenant table)
apps/api             Go API: commands, auth, OAuth, MCP, worker, `api migrate` (binary `api`)
apps/collab          TypeScript live-editing service (y-websocket) and document renderer
apps/web             Next.js client
apps/mobile          Expo client
e2e/                 Playwright end-to-end tests
```

## Local setup
Requires Go (see `apps/api/go.mod`), Node 22+, pnpm, Postgres 17 with pgvector.
```sh
cp .env.example .env            # fill in values; .env is gitignored, never commit secrets
scripts/setup-db.sh             # creates DB, migrates, sets the app role password
set -a; . ./.env; set +a
(cd apps/api && COLLAB_URL=http://localhost:4300 WITH_WORKER=1 go run ./cmd/api)   # API on :4000, worker included
pnpm install
pnpm --filter @cnote/collab start   # live editing on :4300 (COLLAB_INTERNAL_SECRET shared with the API)
pnpm dev:web                        # web on :3000
(cd apps/api && go test ./...)      # unit + integration tests (needs the DB and the collab service)
pnpm e2e                            # Playwright
```
`api migrate` applies `migrations/*.sql` with `DATABASE_ADMIN_URL` (directory: `MIGRATIONS_DIR`, default `migrations`).
`docker build .` builds the API image; `--target collab` and `--target web` build the other two.
