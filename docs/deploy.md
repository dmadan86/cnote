# Deploying C Note

Deployables: the Go API image and the collab image (both from `Dockerfile`, targets `api` and `collab`) and the Next.js
web app (`apps/web`, Vercel, or the `web` target).
No secrets live in the repo; every value below is set in the hosting provider's secret store.

## Postgres
Postgres 16+ with the `vector`, `pg_trgm` and `pgcrypto` extensions (Neon, Supabase, RDS and Railway Postgres all work).
1. Create the database and run migrations with the owner role:
   `DATABASE_ADMIN_URL=... api migrate` (in the API image; `MIGRATIONS_DIR` defaults to `/migrations` there)
2. Set a password for the app role created by the migrations (it is subject to row-level security):
   `ALTER ROLE cnote_app PASSWORD '<generated>';`
3. The app connects as `cnote_app` through `DATABASE_URL`. Never point `DATABASE_URL` at the owner role,
   or row-level security is bypassed.

## Backend (Railway, Fly, ECS, any container host)
Build the images from `Dockerfile` (`--target api` is the default, `--target collab`):
- `api` (HTTP API, `/mcp`, OAuth; proxies `/collab` to the collab service) — public, port `$PORT` (4000);
  `WITH_WORKER=1` also runs the worker (event dispatch, automations, freshness engine, imports, sweeps)
- `collab` (live editing, document rendering) — port `$COLLAB_PORT` (4300); set `COLLAB_URL` on the API to reach it,
  and the same `COLLAB_INTERNAL_SECRET` on both (note.restore renders documents through it)

Environment: `DATABASE_URL`, `DATABASE_ADMIN_URL` (migration job and API runtime for platform controls), `COOKIE_SECURE=true`,
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

## Websites (AI website builder)
Published sites are served at `<name>.<SITES_DOMAIN>` by the same web app; the app and the sites must be on different
registrable domains (the API refuses to start otherwise). Environment, API and web unless noted:
- `SITES_DOMAIN` (`host[:port]`, e.g. `cnote.site`). Unset means publishing is off and the builder says so. Read at run time.
- `SITES_SERVER_TOKEN` — shared secret (a long random string): the web server sends it as `x-sites-token` to
  `POST /api/sites/public_get`. Unset, that endpoint answers 404 and no site is served.
- `PUBLIC_TRUSTED_PROXIES` (API) — comma-separated CIDRs of the proxies in front of the API (Caddy, the Next server, a load
  balancer). The form's client address is the rightmost `X-Forwarded-For` entry that is not in this list, and only when
  the direct peer is in it; otherwise the peer address. Forms and verification limits are kept in Postgres, so replicas agree.
- `SMTP_URL`, `EMAIL_FROM` (API) — needed to mail the email verification code. The person who publishes must have a verified
  email (a code, or an OIDC sign-in with `email_verified=true`; SAML does not verify). Without SMTP nobody can verify and
  publishing stays unavailable (fails closed). `CNOTE_MASTER_KEY` also keys the code hash.
- `CNOTE_PLATFORM_ADMIN_USER_IDS` (API) — comma-separated user ids allowed to run `site.platform_set` (Website Pro,
  takedown, restore, rename). The email allowlist `CNOTE_PLATFORM_ADMINS` does not grant this.
- `SUPPORT_WHATSAPP`, `SUPPORT_EMAIL`, `SUPPORT_FORM_TOKEN` (web) — "Talk to a person" links and the "Report this site" form;
  unset hides them.
- `SITES_TEST_SKIP_VERIFY=1` is for automated tests only: it skips the email check. It is ignored, and the API refuses to
  start, when `PUBLIC_BASE_URL` is https. Never set it in production.

Self-hosted, a wildcard certificate needs the DNS-01 challenge, so Caddy must be built with a DNS provider plugin:

```
*.{$SITES_DOMAIN} {
  tls { dns cloudflare {env.CF_API_TOKEN} }
  reverse_proxy web:3000
}
```

On Vercel add `*.<SITES_DOMAIN>` to the web project. Pages and images are cached for 60 seconds (`max-age=60`); unpublish,
takedown, republish and rename take effect within a minute. Add `SITES_DOMAIN` to the Public Suffix List (operations task).
