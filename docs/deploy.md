# Deploying C Note

Two deployables: the Rust backend image (`Dockerfile`) and the Next.js web app (`apps/web`).
No secrets live in the repo; every value below is set in the hosting provider's secret store.

## Postgres
Postgres 16+ with the `vector`, `pg_trgm` and `pgcrypto` extensions (Neon, Supabase, RDS and Railway Postgres all work).
1. Create the database and run migrations with the owner role:
   `DATABASE_ADMIN_URL=... cnote migrate`
2. Set a password for the app role created by the migrations (it is subject to row-level security):
   `ALTER ROLE cnote_app PASSWORD '<generated>';`
3. The app connects as `cnote_app` through `DATABASE_URL`. Never point `DATABASE_URL` at the owner role,
   or row-level security is bypassed.

## Backend (Railway, Fly, ECS, any container host)
Build from `Dockerfile`. Run two services from the same image:
- `cnote serve` (HTTP API, `/mcp`, `/collab` WebSockets) — public, port `$PORT`
- `cnote worker` (event dispatch, automations, freshness engine, imports, sweeps)
For a single small instance, `cnote serve --with-worker` runs both.

Environment: `DATABASE_URL`, `DATABASE_ADMIN_URL` (migrate job only), `COOKIE_SECURE=true`,
`CNOTE_MASTER_KEY` (base64 32 bytes, encrypts BYO-LLM keys), optional `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
`OPENAI_BASE_URL`, `AI_MODEL`, `COLLAB_ALLOWED_ORIGINS` (the web origin), `GSP_MODE=sandbox` until a GSP is contracted.
Do not set `AI_ALLOW_PRIVATE_ENDPOINTS` on multi-tenant deployments.
MCP OAuth: `PUBLIC_BASE_URL` (the public origin of API/MCP) is **required**; without it OAuth and its metadata are served only
for loopback hosts. Set `TRUST_PROXY=1` only behind a proxy that sets `X-Forwarded-For`, so rate limits key on the real
client IP. Never set `OAUTH_CIMD_ALLOW_PRIVATE`. See docs/mcp-oauth.md.

## Web (Vercel)
Project root `apps/web` (vercel.json installs from the monorepo root). Environment:
- `API_URL` — the backend's public URL (the app rewrites `/api/*` to it so cookies stay first-party)
- `NEXT_PUBLIC_COLLAB_URL` — `wss://<backend host>`
- `NEXT_PUBLIC_TLDRAW_LICENSE_KEY` — required by tldraw in production
Serve web and API under one site (e.g. `app.example.com` and `api.example.com`) so the session cookie is shared, or
route `/api`, `/mcp`, `/collab` to the backend through the same domain.
