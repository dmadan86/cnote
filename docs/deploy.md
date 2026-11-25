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
- `API_URL` — the backend's public URL (the app rewrites `/api/*` to it so cookies stay first-party). Set it at build time (rewrites) and at run time (published sites read the API server-side).
- `NEXT_PUBLIC_COLLAB_URL` — `wss://<backend host>`
- `NEXT_PUBLIC_TLDRAW_LICENSE_KEY` — required by tldraw in production
Serve web and API under one site (e.g. `app.example.com` and `api.example.com`) so the session cookie is shared, or
route `/api`, `/mcp`, `/collab` to the backend through the same domain.

## Editor schema rollout (docs blocks)
The editor has a schema number (`DOC_SCHEMA`, now 2). A tab that does not know a block would delete it from the shared
document, so the gateway fences old tabs with `COLLAB_MIN_SCHEMA` (collab env, default 0). The web client asks the
gateway (`GET /collab/min-schema`, public) and offers the new blocks only when the minimum is at least its own `DOC_SCHEMA`.
Roll out in one window:
1. Deploy web and collab (new blocks stay hidden: the minimum is still below 2).
2. Raise `COLLAB_MIN_SCHEMA` to 2 in the same window: restart collab with the variable, or
   `POST /internal/min-schema` `{"min":2}` with `COLLAB_INTERNAL_SECRET`. Open sockets below 2 are closed at once.
3. Old tabs and old mobile apps (no `schema` parameter counts as 0) are refused or closed, and the tab shows
   "C Note was updated. Reload to keep editing." Between steps 1 and 2 an old tab still connected can delete new
   blocks, so keep the gap short. Never set the minimum without deploying web first.

## Tables public forms
A table form is a link, `/tf/<token>`, that anyone can open and submit without signing in (`GET` and `POST`
`/api/public/table-form/<token>` on the API). Forms take no file uploads. Settings, API:
- `CNOTE_MASTER_KEY` is **required**. It keys the hash of each visitor's address, so every replica must have the same
  value. Without it the form endpoints answer 503 (they never fall back to a per-process key) and startup logs a warning.
- `PUBLIC_TRUSTED_PROXIES` must list every proxy between the visitor and the API, including the Next server that
  rewrites `/api/*`. If it is empty, all visitors behind the rewrite share one address and one hourly limit (120 requests
  an hour per address), and startup logs a warning.
- **The outermost proxy (the edge: CDN, load balancer, Caddy) must replace `X-Forwarded-For` and `X-Real-IP`, not append
  to what the client sent.** The API takes the rightmost entry that is not a trusted proxy, so a client can only forge
  entries to the left of what the edge writes; an edge that passes client headers through, or a trusted range wider than
  your own proxies, lets a visitor pick a fresh address per request and defeats the limits.
- Limits live in Postgres (`core.rate_buckets`), so replicas agree: per address 120 requests an hour and 20 unknown
  links an hour, 6,000 an hour for the platform, and per form 120 an hour, 10 an hour per address, and a daily cap by plan.
- Tokens are 128 random bits, stored only as a sha256. Do not log request paths of `/api/public/table-form/` or
  `/tf/` at the edge or in analytics: the path is the credential. The API never logs it and sends
  `Referrer-Policy: no-referrer`.

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

### Custom domains (Website Pro)

Off unless `SITES_CUSTOM_DOMAINS=1` (API and web). Custom domains need Caddy in front of the web app (the self-hosted path);
they stay hidden on a Vercel-hosted web app. Environment:
- `SITES_APEX_IP` (API, web) — the public address or addresses of the edge, comma-separated (IPv4 and IPv6). Owners point the
  address without `www` at it. **It must not change**: owners' A records depend on it. Unset, only `www`-style names
  (anything with a CNAME) can be connected.
- `SITES_ASK_ADDR` (API, default `127.0.0.1:4199`) — the listener for Caddy's on-demand TLS question
  (`GET /internal/tls-ask?domain=`). Keep it on loopback or a private network; never route it publicly.
- `APP_HOSTS` (API, web) — the app's own hostnames beyond `PUBLIC_BASE_URL`; they and `SITES_DOMAIN` can never be claimed.
- The API must run the worker (`WITH_WORKER=1`): the daily `sites.domains` sweep re-checks every verified hostname.
- `SITES_TEST_DNS=<suffix>` is for automated tests only: names under the suffix pass both DNS checks. Ignored when
  `PUBLIC_BASE_URL` is https.

How a hostname goes live: the owner adds it (`www.example.in` and/or `example.in`; each is its own claim), publishes the
TXT record `_cnote-verify.<hostname>` = `cnote-verify=<token>` (a fresh 128-bit token per claim) and the routing record
(CNAME to `<name>.<SITES_DOMAIN>`, or an A/AAAA record to `SITES_APEX_IP` for the apex). It is routed and approved for
TLS only while verified, routed, published, not taken down and Pro. Re-checked daily; two failed days in a row make it
`lost`. Disconnecting, unpublishing, takedown and a Pro lapse stop routing and certificates at once. Limits: 5 hostnames
per site per 30 days, 50 newly verified hostnames per hour platform-wide, 120 DNS checks per site per hour.

One Caddy instance with persistent storage (certificates and ACME state live there; losing it re-issues everything and can
hit Let's Encrypt limits):

```
{
  on_demand_tls { ask http://api:4199/internal/tls-ask }
}
# :80 redirects to https; ACME HTTP-01 challenges are answered by Caddy before the redirect
http:// {
  redir https://{host}{uri} permanent
}
# the sites' own names: one wildcard certificate (DNS-01)
*.{$SITES_DOMAIN} {
  tls { dns cloudflare {env.CF_API_TOKEN} }
  reverse_proxy web:3000
}
# every other name: a certificate on demand, only if the ask endpoint says yes
https:// {
  tls { on_demand }
  reverse_proxy web:3000
}
```

Both blocks send everything to the web app, which forwards `/api/sites/*` to the API as it does today; the web proxy
answers 404 for a host it does not know. DNS to publish: `*.<SITES_DOMAIN>` as A (and AAAA) records to the edge, not a
CNAME; the edge's IPv4 and IPv6 addresses in `SITES_APEX_IP`.

Your domain stays yours. You keep it at your registrar; C Note never registers, transfers or locks it. Disconnect at any
time: your website goes back to `<name>.<SITES_DOMAIN>` and you can point the domain anywhere.

### Custom domains through Cloudflare for SaaS (self-hosted, optional)

An alternative to Caddy's on-demand certificates: Cloudflare terminates TLS for each customer hostname and proxies to your
origin. Off unless `SITES_DOMAIN_PROVIDER=cloudflare` **and** `SITES_CUSTOM_DOMAINS=1`; with the provider off, nothing in
this section runs. Subdomains only: the address without `www` is refused in this mode (apex proxying is an Enterprise add-on).

How it works: ownership is still proved by the TXT record. Once a claim is verified, the worker's reconciler
(`sites.cloudflare` sweep) creates a custom hostname in your zone, validated over HTTP, tagged with this installation's id
in its custom metadata. A hostname is routed (and approved for the site's public lookup) only while Cloudflare reports both
`status` and `ssl.status` as `active`. Customers point a CNAME at the proxied record you publish (`SITES_CF_CNAME_TARGET`).
A hostname Cloudflare blocks is lost at once; a moved, deleted, expired or timed-out one costs a strike (two strikes about an
hour apart lose it); Cloudflare deleting a hostname that never validated (it gives up after seven days) counts as a strike
and the hostname is created again.

Environment (API and worker):
- `SITES_DOMAIN_PROVIDER=cloudflare` - the switch.
- `SITES_CF_API_TOKEN` - a token scoped to the one zone with "SSL and Certificates: Edit" and nothing else. Sent only in the
  `Authorization` header; never logged, never sent to the browser.
- `SITES_CF_ZONE_ID` - the zone that holds the proxied fallback and CNAME target records.
- `SITES_CF_INSTALLATION_ID` - any stable unique string for this installation (for example a UUID). Written into every
  hostname's custom metadata; the reconciler deletes only hostnames that carry it. **Never change it** after hostnames exist,
  and give a staging installation another id if it shares the zone.
- `SITES_CF_CNAME_TARGET` - the name customers CNAME to (the proxied record below). Unset, it is `<name>.<SITES_DOMAIN>`.
- `SITES_CF_MAX_HOSTNAMES` - the most hostnames held at Cloudflare, default 90 (Cloudflare includes 100 on Free, Pro and
  Business, then charges $0.10 each; every hostname counts until deleted, pending ones too). New claims are refused and
  creates wait at this number, counting hostnames in the zone that are not ours. At 80% the API logs a warning once a day.
- `SITES_CF_PAUSED=1` - the stop switch: no sweep call is made (no create, poll or delete) and new domains are refused. Routing
  here keeps working from what is stored. Hostnames of sites that go offline while paused are deleted once you unpause.
- Also a workspace can hold 5 hostnames, a site 5 per 30 days, 50 hostnames can verify per hour platform-wide, and 120
  checks per site per hour (as for every domain).
- The worker must run the sweep (`WITH_WORKER=1`, `sites.cloudflare`, every minute or two is cheap: it makes no call when
  nothing is due).

When a site is unpublished, taken down, loses Pro, or a hostname is disconnected or lost, routing stops at once (the proxy
lookup no longer finds it) and the reconciler deletes the Cloudflare hostname on its next run (within an hour even after
failures; a failed delete stays on a tombstone row and is retried with backoff, 429s and bad tokens stop the sweep). The
certificate may still be served by Cloudflare until that delete completes. Requests are retried twice on 5xx and network
errors and never on 429 (Cloudflare blocks the token for five minutes).

Real-zone checklist, once, before you set the switch (the API shapes are in `docs/design/site-builder-phase3-facts.md`
section 1; the delete permission, the `Host` header and Full (strict) there were not checked against a live zone):
1. Zone plan has Cloudflare for SaaS enabled and its hostname quota covers `SITES_CF_MAX_HOSTNAMES`.
2. Create the token above and check it can create, read and **delete** a custom hostname (the delete permission was not
   confirmed in the docs):
   `curl -X DELETE -H "Authorization: Bearer $T" https://api.cloudflare.com/client/v4/zones/$ZONE/custom_hostnames/$ID`.
3. Check custom metadata round-trips on your plan: create a test hostname with `"custom_metadata":{"cnote_installation":"x"}`
   and read it back with `GET .../custom_hostnames/$ID`. If it comes back empty, **do not enable the provider**: the
   reconciler would never delete anything (it refuses to delete a hostname without our id).
4. Publish a proxied `A`/`AAAA`/`CNAME` record for the origin (the VPS or the Caddy edge), then set it as the fallback origin:
   `PUT .../custom_hostnames/fallback_origin` with `{"origin":"<that record's name>"}`, and wait until it reads `active`.
5. Publish the proxied CNAME target customers will use (for example `customers.<zone>`, pointing at the fallback record) and
   put its name in `SITES_CF_CNAME_TARGET`.
6. The origin must accept the customer's name in the `Host` header and answer for it (the web app picks the site from `Host`),
   and its certificate must satisfy SSL mode **Full (strict)** (use a Cloudflare Origin CA certificate on the origin).
7. Connect one real test subdomain end to end (claim, TXT, CNAME to the target, wait for `active`), open it over https, then
   disconnect it and confirm the hostname disappears from the Cloudflare dashboard within a few minutes.
