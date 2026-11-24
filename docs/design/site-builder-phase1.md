# AI website builder: Phase 1 technical design (publish, serve, leads)

24 Nov 2026. Branch `feat/site-publish`, stacked on Phase 0. Builds on `site-builder.md`; its section 14 (Codex revisions) still binds everything here. Paths are repo-relative. Nothing here was compiled or run.

## 0. Decisions, and what is not built

| # | Decision | Why |
|---|---|---|
| P1 | Publishing is three columns on `sites.sites`: `subdomain`, `published_version_id`, `published_at`. Versions are already immutable, so "published" is a pointer. | No copy, no second snapshot table. |
| P2 | Published pages are rendered by Next.js from the Phase 0 block components, routed by `Host` in a new `src/proxy.ts`, and cached as full pages. | One renderer for preview and live. |
| P3 | Public traffic never opens a tenant by itself: `SECURITY DEFINER` functions resolve a subdomain, as `crm.inquiry_form_lookup` does for form tokens. | RLS hides every row from a request with no tenant. |
| P4 | The contact form creates a lead through a new internal command `inquiry.receive` in `leads`; images are stored through new internal commands in `files`. | No new module import (`TestModuleBoundaries`). |
| P5 | Image props hold file ids, never URLs. `SITE_IMAGE_HOSTS` is dropped. | Closes the exfiltration channel in Phase 0 section 7. |
| P6 | Website Pro and takedown are columns only a platform-admin command writes. They are **not** in `core.workspaces.settings`: `workspace.update` lets any workspace admin merge arbitrary settings keys. | Otherwise an admin grants themselves Pro. |
| P7 | No email verification exists today (password sign-up stores the user unverified; only OIDC checks `email_verified`). Phase 1 adds a 6-digit email code, and the **person who publishes** must be verified. | Smallest real gate; see open question 1. |
| P8 | The subdomain is fixed after the first publish. | No rename, redirect or squatting rules to design. |
| P9 | SEO title and description live in the page document (`root.props`), so chat edits, undo and the publish snapshot cover them for free. | No extra columns. |

Not built: custom domains and on-demand TLS, subdomain rename, a manual prop editor, several pages, analytics, a no-JavaScript form fallback, cheap-model fallback past the cap, payment, a platform-admin list screen, image cropping or deletion, WebP/GIF uploads, site delete.

## 1. Flow

```
site.publish ─► sites.sites.published_version_id = draft ─► job "site.revalidate" ─► POST web /site-revalidate
visitor ─► Caddy/Vercel ─► Next proxy.ts (Host = <sub>.SITES_DOMAIN)
   /            ─► rewrite /published/<sub>   (cached page; fetches GET /api/public/sites/<sub>)
   /_img/<id>   ─► rewrite API /api/public/sites/<sub>/img/<id>
   /_form POST  ─► rewrite API /api/public/sites/<sub>/form ─► inquiry.receive + notify.Send
   anything else ─► 404
```

## 2. Data model: `migrations/1290_site_publish.sql`

```sql
ALTER TABLE sites.sites
  ADD COLUMN subdomain text UNIQUE CHECK (subdomain ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(subdomain) BETWEEN 3 AND 40),
  ADD COLUMN published_version_id uuid,
  ADD COLUMN published_at timestamptz,
  ADD COLUMN published_by uuid REFERENCES core.users(id),
  ADD COLUMN form_count int NOT NULL DEFAULT 0,          -- accepted contact-form submissions
  ADD COLUMN pro boolean NOT NULL DEFAULT false,          -- Website Pro; written only by sites.platform_set
  ADD COLUMN taken_down_at timestamptz,
  ADD COLUMN taken_down_reason text,
  ADD FOREIGN KEY (workspace_id, id, published_version_id) REFERENCES sites.site_versions (workspace_id, site_id, id);

CREATE TABLE sites.platform_actions (                     -- audit; no RLS, no grant to cnote_app
  id bigserial PRIMARY KEY, actor_user_id uuid NOT NULL, actor_email text NOT NULL,
  workspace_id uuid NOT NULL, site_id uuid NOT NULL,
  action text NOT NULL CHECK (action IN ('takedown','restore','pro_on','pro_off')),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500), created_at timestamptz NOT NULL DEFAULT now());

-- SECURITY DEFINER, search_path = sites, core, pg_temp; EXECUTE granted to cnote_app
sites.host_lookup(p_subdomain text) RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, data jsonb,
  pro boolean, published_at timestamptz, published_by uuid)   -- only when published and not taken down
sites.subdomain_taken(p_subdomain text) RETURNS boolean
sites.platform_set(p_actor uuid, p_email text, p_subdomain text, p_workspace_slug text, p_action text, p_reason text)
  RETURNS TABLE(site_id uuid, subdomain text, pro boolean, taken_down boolean)  -- updates the site and inserts the audit row

-- images hang on a site instead of a page (files module, section 9)
ALTER TABLE core.attachments ALTER COLUMN note_id DROP NOT NULL, ADD COLUMN site_id uuid,
  ADD CHECK ((note_id IS NULL) <> (site_id IS NULL));
CREATE INDEX attachments_site ON core.attachments (workspace_id, site_id) WHERE site_id IS NOT NULL;

-- email verification (auth, P7)
ALTER TABLE core.users ADD COLUMN email_verified_at timestamptz;
CREATE TABLE core.email_codes (user_id uuid PRIMARY KEY REFERENCES core.users(id) ON DELETE CASCADE,
  code_hash text NOT NULL, expires_at timestamptz NOT NULL, attempts int NOT NULL DEFAULT 0);
GRANT SELECT, INSERT, UPDATE, DELETE ON core.email_codes TO cnote_app;

-- notifications_kind_check: drop and re-add with 'lead' appended to the 1140_chat.sql list
```

`core.attachments` gets no foreign key into `sites` (a platform table must not depend on a module schema); note-less rows are invisible to the existing `file.*` commands because those all filter by `note_id`.

## 3. Commands

In `sites`. "person" refuses agents and API keys: putting content on the public internet is a person's act.

| Command | Group, role | Input | Output |
|---|---|---|---|
| `site.subdomain_check` | read, admin | `{id, subdomain}` | `{subdomain, available, reason?}`; reason `invalid`, `reserved` or `taken`. Input is lowercased and trimmed first. |
| `site.publish` | write_items, admin, person | `{id, expected_version_id, subdomain?}` | `{site, url}` |
| `site.unpublish` | write_items, admin, person | `{id}` | `{site}` |
| `site.image_upload` | write_items, member | `{id, filename, data_base64}` | `{image: {id, filename}}` |
| `site.image_get` | read, member | `{id, image_id}` | `{content_type, data_base64}`; the builder preview |
| `site.platform_set` | admin, person + platform admin | `{subdomain? \| workspace_slug?, action, reason}` | `{site_id, subdomain, pro, taken_down}` |

Changed:

- `site.get` adds to `site`: `subdomain, url, published_version_id, published_at, taken_down: {reason}?, pro, form_count`; and returns `images[]`, `ai_edits: {used, limit, resets_at}`, `can_publish: {configured, verified}`, `support_token?`.
- `site.chat` takes `image_ids?` (at most 4, just uploaded in the chat) and enforces the monthly cap (section 10).
- `site.edit` accepts the new `set_page` op.
- `site.templates` returns five templates with a `vertical`.

`site.publish` runs in one transaction, site row locked:
1. `SITES_DOMAIN` unset → `invalid` "Publishing is not set up on this server."
2. Taken down → `forbidden` with the reason.
3. `mailer.Configured()` and the caller's `email_verified_at` is null → `forbidden` "Verify your email to publish." (With no SMTP there is no way to verify, so a mail-less self-hosted server skips the gate.)
4. `expected_version_id` must be the draft and no turn may be active (Phase 0 rules).
5. First publish: `subdomain` required, checked (section 4), stored; the unique index decides races (`conflict` "That address was just taken"). Later publishes ignore `subdomain`.
6. `PublishCheck(data)`: `Validate`, plus every image id belongs to this site (`file.image_list`).
7. Set `published_version_id`, `published_at`, `published_by`; emit `site.published {site_id, version_id, subdomain}`; insert job `site.revalidate {subdomain}`.

`site.unpublish` nulls `published_version_id` (the subdomain stays reserved for this site), emits `site.unpublished`, queues the same job.

`site.platform_set` checks `CNOTE_PLATFORM_ADMINS`. That check is `platform()` in `marketplace`; move it to `command.(*Ctx).PlatformAdmin()` and call it from both. The handler calls `sites.platform_set(...)` and queues `site.revalidate`. Takedown hides the site, refuses publish, and shows a banner in the builder.

New commands outside the module, all `Internal: true`:

| Command | Module | Does |
|---|---|---|
| `inquiry.receive` | `leads` | `{name, phone, email, message, source}` → `{deal_id, contact_id}`. The body of `leads.Receive` on the caller's `Ctx`. Space and owner come from `crm.inquiry_forms` (whether or not the token link is enabled); with no row it creates one in the workspace's oldest space (not verified that this is the right default; open question 8), disabled, owner = the principal. `source` is stored on `crm.deals.source` (`website`). |
| `file.image_put` | `files` | `{site_id, filename, data_base64}` → `{id, filename, size_bytes}` (section 9) |
| `file.image_get` | `files` | `{site_id, id}` → `{content_type, data_base64}` |
| `file.image_list` | `files` | `{site_id}` → `[{id, filename}]` |

Auth (HTTP, session required): `POST /api/auth/verify/request` mails a 6-digit code (10 minutes, SHA-256 stored, at most 3 requests per 15 minutes per user through `core.auth_throttle`); `POST /api/auth/verify/confirm {code}` sets `email_verified_at` (5 wrong tries void the code). OIDC and SAML sign-in set `email_verified_at` when null. `/api/auth/me` adds `email_verified`.

## 4. Subdomain rules

`sites/subdomain.go`, pure and unit-tested: `CheckSubdomain(s) (normalised, reason)`.

- `^[a-z0-9]+(-[a-z0-9]+)*$`, 3 to 40 characters (so no leading, trailing or double hyphen, and no `xn--`).
- Blocklist `sites/blocklist.txt` (embedded, one token per line, two sections). Before matching, hyphens are removed and `0 1 3 5` become `o l e s`.
  - `[exact]`: `www app api admin mail smtp ftp ns1 ns2 cdn static assets img status help support login signup account billing blog docs dev staging test demo`.
  - `[contains]`: C Note words (`cnote`, `cnotes`), large brands and institutions (`google`, `facebook`, `instagram`, `whatsapp`, `amazon`, `flipkart`, `paytm`, `phonepe`, `gpay`, `upi`, `npci`, `sbi`, `hdfc`, `icici`, `axis`, `rbi`, `irctc`, `aadhaar`, `uidai`, `incometax`, `gov`, `microsoft`, `apple`, `netflix`), and an offensive-word list.
- The modal pre-fills `slug(business name)`; when that is blocked or taken it tries `-<city>` then `-2`…`-9`.

## 5. Serving

**Environment.** `SITES_DOMAIN` (API and web) is `host[:port]`: `cnote.site` in production, `localhost:3000` locally. The scheme is `http` when the host is `localhost`, else `https`. It must be a different registrable domain from the app's. `SITES_WEB_URL` (API: where the worker reaches the web app, default `http://localhost:3000`), `SITES_REVALIDATE_SECRET` (both), `SITES_SUPPORT_FORM_TOKEN` (API, section 10). `PUBLIC_TRUST_PROXY=1` as the inquiry form already needs.

**`apps/web/src/proxy.ts`** (Next 16 names middleware "proxy"; read `node_modules/next/dist/docs` first, per `apps/web/AGENTS.md`). Matcher: everything except `/_next/static`.

- Host is `<label>.<SITES_DOMAIN host>` (port ignored, lowercased):
  - label fails the regex → the 404 page.
  - `GET /` → rewrite to `/published/<label>`; `/robots.txt` and `/sitemap.xml` → `/published/<label>/robots.txt`, `/sitemap.xml`.
  - `GET /_img/<uuid>` and `POST /_form` → rewrite to the API (section 6) with the `cookie` header removed.
  - Anything else, including `/api/*`, `/w/*`, `/login`, `/_next/image` and `/site-revalidate` → 404. The app is unreachable on a site host.
  - Every response gets the headers below.
- Any other host: `/published/*` → 404, so a tenant page never renders on the app origin.

**Route `app/published/[sub]/page.tsx`**, a server component:

```ts
export const revalidate = 300
export const generateStaticParams = async () => []          // render on first visit, then cache
const r = await fetch(`${API_URL}/api/public/sites/${sub}`, { next: { tags: [`site:${sub}`] } })
if (!r.ok) notFound()
```

It renders `<Published data pro />` (`components/sites/published.tsx`): the same `BLOCKS` and theme variables, no selection chrome, no `'use client'`. The theme-to-style code in `renderer.tsx` moves to a shared `themeStyle(theme)` in `lib/sites.ts`. The only client component is `contact-form.tsx`. Next still ships its runtime, but the page works with JavaScript off, except the form (the phone and email beside it remain). `not-found.tsx` is one plain 404 for unknown, unpublished and taken-down sites.

**Caching.** Full route cache keyed by subdomain, tagged `site:<sub>`. `sites.RevalidateJob` (worker, 5 attempts) posts `{subdomain}` to `SITES_WEB_URL/site-revalidate` with header `x-sites-secret`; the route handler compares the secret in constant time and calls `revalidateTag('site:<sub>', { expire: 0 })`. The job is inserted in the publishing transaction, so a publish cannot commit without its purge being queued. `revalidate = 300` heals a purge that never arrives. Images and the form are not cached by Next and stop the moment a site is unpublished or taken down.

`// shortcut: random unknown subdomains each cost one cached 404; rate-limit at Caddy or the CDN if it is abused.`

**Headers** (set in the proxy for site hosts):

```
Content-Security-Policy: default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';
  img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff        Referrer-Policy: strict-origin-when-cross-origin
X-Frame-Options: DENY                  Cross-Origin-Opener-Policy: same-origin
Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=()
Strict-Transport-Security: max-age=31536000; includeSubDomains     (https only)
```

`script-src` keeps `'unsafe-inline'`: Next's hydration scripts are inline, and a nonce would force a render per request and end full-page caching. Accepted because the origin holds no cookie or credential and no tenant markup exists; the rest of the policy still blocks third-party loads, cross-origin form posts and framing.

**Local.** Browsers resolve `*.localhost` to loopback, so `http://acme.localhost:3000` reaches `next dev` or `next start` with no hosts file. `next.config.ts` adds `allowedDevOrigins: ['*.localhost']`. The app's session cookie is host-only on `localhost` and is not sent to `acme.localhost`.

**Self-hosted.** A wildcard certificate needs the DNS-01 challenge, so Caddy must be built with a DNS provider plugin. Add to `docs/deploy.md`:

```
*.{$SITES_DOMAIN} {
  tls { dns cloudflare {env.CF_API_TOKEN} }
  reverse_proxy web:3000
}
```

On Vercel, add `*.cnote.site` to the project. On-demand TLS arrives with custom domains in Phase 2.

## 6. Public endpoints (Go, `server/sitespublic.go`)

No session. Subdomain validated by the regex before any query; unknown, unpublished and taken-down answer the same 404. Responses carry `nosniff`.

| Endpoint | Does |
|---|---|
| `GET /api/public/sites/{sub}` | `{site_id, data, version_id, published_at, pro, url}` from `sites.host_lookup`. Called by the Next server, cached there. |
| `GET /api/public/sites/{sub}/img/{id}` | 404 unless `id` is in `sites.ImageIDs(published data)`. Then `Registry.WithTx` as a System principal and `c.Call("file.image_get")`. `Content-Type` as stored, `Cache-Control: public, max-age=3600`, `Content-Security-Policy: default-src 'none'; sandbox`. Per-IP bucket 300 burst, 5/s. |
| `POST /api/public/sites/{sub}/form` | Section 7. |

## 7. Contact form → CRM lead

The form (rendered from the ContactForm block; labels are fixed in the renderer, not props): name (required), phone (required), email (optional), message, a consent checkbox that is unchecked and required, and a hidden honeypot input `company_website`. The Phase 0 "Phone or email" field becomes two fields because `leads.Clean` needs a valid phone.

Consent text, fixed, with the business name from the page: "I agree that {business} may use these details to reply to my enquiry." Below it: "Your details go to {business} and are stored by C Note, which hosts this site. Ask {business} to correct or delete them at any time."

`POST …/form`, JSON `{name, phone, email, message, consent, company_website}`, body limit 16 KB:
1. `host_lookup`; 404 unless the published version has a ContactForm block (`sites.HasBlock`). A site without a form cannot receive leads.
2. Honeypot filled → `200 {"ok":true}`, nothing stored.
3. `consent !== true` → 400.
4. Limits through `command.CheckRateWith`: `siteform:ip:<ip>` 5 burst, 1 per 2 minutes; `siteform:site:<id>` 60 burst, 1 per minute. Over either → 429 "Too many messages right now. Please call us instead." (In-process buckets: the existing shortcut.)
5. `Registry.WithTx` with `Principal{Kind: System, WorkspaceID, UserID: published_by, Role: Admin}`: `c.Call("inquiry.receive", {…, source: "website"})`; `form_count + 1`; `notify.Send` one row per active owner and admin (at most 20) with kind `lead`, title "New website enquiry from {name}", `NoteID` = the deal. `ResolveContact` already stamps `consent_at`.
6. `200 {"ok":true}`; the form is replaced by "Thank you. {business} has your message."

No email is sent. The lead, with the message as its first activity, is the record. The form has no password, card or file field and the catalog offers none.

## 8. Catalog and templates

`catalog.json` stays the single source; the byte-for-byte web copy and `TestWebCatalogInSync` are unchanged. `BLOCKS: Record<keyof catalog.blocks, Component>` still fails `tsc` when a block has no component. The `add_block` tool's `type` enum is now generated from the catalog keys.

New and changed blocks (13 in all):

| Block | Props |
|---|---|
| Header | + `logo` image (its alt text is `brand`) |
| Hero | + `image_alt` text 120 |
| About | `title` text 80; `body` longtext 800 required; `image` image; `image_alt` text 120 |
| Gallery (max 1) | `title` text 80; `items` list ≤12 of `{image image required, caption text 80}`; the caption is the alt text |
| Pricing | `title` text 80; `note` text 120; `items` list ≤12 of `{name text 60 required, price text 30 required, description longtext 200}` |
| FAQ (max 1) | `title` text 80; `items` list ≤12 of `{question text 120 required, answer longtext 500 required}` |
| CallToAction | `title` text 80 required; `text` longtext 200; `cta_label` text 30; `cta_href` link |
| MapHours (max 1) | `title` text 80; `address` longtext 200; `map_url` link; `hours` list ≤7 of `{days text 30 required, time text 30 required}` |
| WhatsAppButton (max 1) | `phone` phone; `message` text 120; `label` text 30 |
| Testimonials (max 1) | `title` text 80; `items` list ≤6 of `{quote longtext 300 required verbatim, name text 60 required verbatim, role text 60}` |

- **Root**: `root.props.description` (longtext 160) joins `title`. New tool `set_page {title?, description?}`.
- **`image`** now means: empty, or the lowercase UUID of an image uploaded to this site. `Validate` checks the shape; `PublishCheck` and `addVersion` check ownership.
- **MapHours** renders the address, the hours and a plain "Open in Maps" link. No iframe, no third-party script.
- **WhatsAppButton** renders `https://wa.me/<digits>?text=<encoded message>`; ten digits get `91` in front; anything that is not 8 to 15 digits renders nothing. The same table of cases is tested in Go (`WaDigits`) and TS (`waHref`).
- **Blocks with nothing to show** (Gallery or Testimonials with no items, WhatsAppButton with no phone) render nothing when published and a dashed "Add photos" style placeholder in the builder.
- **Block context**: every block component receives `assetBase: string`; an image renders as `${assetBase}/${id}`. The builder passes `/api/w/<ws>/sites/img` (a server route wrapping `site.image_get`, like `fileGet`); the published page passes `/_img`. ContactForm also takes `form?: ReactNode`, which the published page fills with the live client form and the preview leaves as the inert one.

**Testimonials rule** (`"verbatim": true` in the catalog, enforced in `RunTurn`). After a model tool call, every `verbatim` string in the result must either already be in that block before the turn, or occur in this turn's user message. Comparison is on NFC text with whitespace collapsed, surrounding quote marks removed and case ignored. Anything else fails the call with "Testimonials must be the customer's exact words. Ask the user to paste them." So the model can add what the owner pasted, reorder and remove, but cannot write, polish or translate one. `site.edit` by a person is not restricted. Templates may carry a Testimonials block, always with no items.

**Templates are data**: `sites/templates/<key>.json` as `{key, name, description, vertical, data}`, embedded with `go:embed templates/*.json`; `template_services.json` moves there. `business`, `fillTemplate` and the `site.templates` handler move from `commands.go` to `sites/templates.go`. Slots are unchanged (`{{name}}`, `{{phone}}`, `{{email}}`, `{{address}}`), never truncated (14.11).

| Key | Vertical | Blocks |
|---|---|---|
| `services` | Services | Header, Hero, Services, About, FAQ, ContactForm, Footer |
| `shop` | Shop | Header, Hero, Services ("Products"), Gallery, MapHours, WhatsAppButton, ContactForm, Footer |
| `clinic` | Clinic | Header, Hero, Services ("Treatments"), About, MapHours, FAQ, ContactForm, Footer |
| `restaurant` | Restaurant | Header, Hero, Pricing ("Menu"), Gallery, MapHours, WhatsAppButton, ContactForm, Footer |
| `trader` | Manufacturer / trader | Header, Hero, About, Services ("Products"), CallToAction (to `#ContactForm-…`), ContactForm, Footer |

Placeholder text stays visibly placeholder; prices, hours and FAQ answers ship empty or as "Add your …".

**Prompt.** The catalog is still embedded whole. Added rules: images are only ids listed in `<images>`, never URLs; testimonials are only the user's exact words from this message; `set_page.description` is one plain sentence about the business from facts on the page. `TurnInput` gains `Images []Image{ID, Name}`, sent as a JSON-encoded `<images>` block (14.7); ids in `image_ids` are marked "attached now".

**Eval.** A case may name a `fixture` (one of the five filled templates; default the Phase 0 fixture) and `images`. About 40 new cases: add each new block (8); place an attached image as logo, hero and gallery item (4); `set_page` (3); hours and WhatsApp (4); testimonials (4: a pasted one is accepted; "write three", "make it sound better" and "translate it to Hindi" change nothing); must-refuse (3: an image URL, a `javascript:` map link, an image id not in `<images>`); a four-case smoke on each of the four new templates.

## 9. Images

Upload path: the builder draws the chosen file on a canvas at no more than 1600 px on the long edge and exports JPEG (PNG when it has transparency), then calls `site.image_upload`. The server does not trust that.

`file.image_put` (`files/image.go`, standard library only):
1. At most 1 MB decoded. The command body limit is 2 MiB and base64 adds a third.
2. `Sniff` must say `image/jpeg` or `image/png`.
3. `image.DecodeConfig` first: each side at most 2400 px, so a decompression bomb is refused before decoding.
4. Decode and re-encode (JPEG quality 85, or PNG). This drops EXIF, GPS and any appended payload.
5. Store through the existing Postgres/S3 switch with `site_id` set; at most 40 images per site.

`// shortcut: re-encoding ignores EXIF orientation; the browser path has already rotated the pixels. Read the orientation tag if API uploads become common.`

An image is public only through `/_img/<id>` on a site whose **published** version references it; draft images are reachable only by workspace members through `site.image_get`. Ids are random UUIDs.

Entry points: the paperclip in the chat composer ("add our logo" with the file attached; the web uploads first, then sends `image_ids`) and "Replace image" on the toolbar of a block with an image prop (`site.edit` with `update_block_props`).

## 10. SEO, plan rules, human help

**SEO**, all from the published document, in `generateMetadata` and two route handlers:
- `<title>` = `root.title`; description = `root.description`, else the Hero subtitle; canonical and `og:url` = the site URL; `og:title`, `og:description`, `og:type=website`, `og:image` = the Hero image when there is one.
- `robots.txt`: allow all, plus the sitemap line. `sitemap.xml`: the one URL with `lastmod = published_at`.
- LocalBusiness JSON-LD: `name` (Header brand, else title), `url`, `telephone`, `email`, `address` (ContactForm, else Footer, else MapHours), `openingHours` (MapHours), `image`. It is built from the **page**, not from `company.get`: the page is what the owner chose to make public, and a profile field they removed from it must not reappear in markup. Written as `JSON.stringify(x).replace(/</g, '\\u003c')` in a `<script type="application/ld+json">`. This is the one `dangerouslySetInnerHTML` in the feature, and it lives in the page file, not the renderer.

**Plan** (constants in `sites/plan.go`):

| | Included | Website Pro (`sites.pro`) |
|---|---|---|
| Sites | 1 on a subdomain | 1 on a subdomain |
| "Made with C Note" badge | yes | no |
| AI edits per month | 30 | 250 |

- An AI edit is a chat turn that changed the page (`version_id` set), plus any turn still queued or running. Refusals and failures are free. The month is the calendar month in `Asia/Kolkata`.
- The Phase 0 limits (200 turns a day, the workspace token budget) still apply underneath.
- At the cap `site.chat` answers `plan_limit`: "You've used all 30 AI edits for this month. You can still move, duplicate and delete sections and replace images. Edits come back on 1 December." The builder shows it as an assistant message, not an error.
- The composer shows "12 edits left this month". No credits, no price. The settings panel says "Website Pro: more edits, no badge. Upgrade (coming soon)."
- The badge is drawn by `published.tsx` (and the preview) when `pro` is false. It is not in the document, so chat cannot remove it.

**Talk to a person.** The Support module already turns a public POST into a ticket: `/api/public/ticket/{token}` (`leads.ReceiveTicket`), with replies emailed by `ticket.reply`. C Note runs its own support workspace; its inquiry-form token goes in `SITES_SUPPORT_FORM_TOKEN` and `site.get` returns it as `support_token`. The chat's "Talk to a person" opens a small form (name and email from the account, phone from the page, message) and posts `{subject: "Website help: {site name}", …}` to that endpoint. Copy: "Our team replies Monday to Saturday, 10 am to 7 pm IST." No new backend code. With the variable unset the action is hidden.

## 11. Builder UX

Layout, copy and states follow `site-builder-phase1-ux.md`. What the web needs from this design:

- **Pill**: `Draft` (never published), `Published`, `Unpublished changes` (draft ≠ published), `Offline` (taken down).
- **Publish**: "Publish", or "Publish changes"; disabled with nothing to publish, and for members ("Ask an admin to publish"). The first publish opens the modal: a "Verify your email" step when `can_publish.verified` is false, then the address field checked live with `site.subdomain_check` (debounced). Republishing skips the modal.
- **Success**: the URL with Copy link, Share on WhatsApp (`https://wa.me/?text=<url>`), Visit.
- **Settings panel**: address (read-only, Copy), search title and description (`site.edit` with `set_page`), the plan note, Unpublish with a confirm.
- **Leads**: "{form_count} enquiries" linking to CRM leads.
- **Platform admins**: `/w/[ws]/site/platform`, one form (address or workspace, action, reason) calling `site.platform_set`.

## 12. Security

- **Host routing.** Four allowlisted paths on site hosts, 404 for the rest; `/published/*` is 404 on the app host; the subdomain is regex-checked in the proxy and again in Go before any query.
- **Cookie isolation.** A separate registrable domain that sets and reads no cookie; the proxy strips `cookie` before calling the API. `SITES_DOMAIN` should also go on the Public Suffix List (operations, not a blocker: no tenant script exists to set a cookie).
- **No tenant code.** Every value is a React text child or a validated href, colour or file id. No third-party script, frame, font or image host.
- **Phishing and abuse.** Blocklist, verified publisher, no credential, card or file input in the catalog, `form-action 'self'`, takedown.
- **Takedown.** Gated by `CNOTE_PLATFORM_ADMINS`; the audit row is written in the same statement; page, image and form all read `host_lookup`, which hides taken-down sites.
- **Definer functions** take scalars only, pin `search_path`, and return only published, non-taken-down rows (`platform_set` aside, which Go gates).
- **Lead injection.** Honeypot, consent, two rate limits, 16 KB body cap, the ContactForm check, `leads.Clean`.
- **Model.** Still only page JSON; new reach is this site's image ids and `set_page`. Testimonials are fenced in Go.

## 13. Tests

- **Go unit.** `sites`: `CheckSubdomain` table (format, leetspeak brand, exact, offensive); each new block and kind accepts and rejects; all five templates validate filled and with every slot empty; `ImageIDs`, `HasBlock`, `WaDigits`; the verbatim rule (pasted, pre-existing, reordered, invented, polished, translated); `TestWebCatalogInSync`. `files`: PNG and JPEG round trip, EXIF GPS is gone after upload, oversize bytes and dimensions refused, PDF and GIF refused.
- **Server** (`server/sites_publish_test.go`; `newEnv`, `startWorker`, `startSMTP`):
  - publish needs an admin, a person, a verified email (request, wrong code, right code) and the current draft; reserved and taken names; two workspaces racing for one name;
  - the public GET returns the published version, not a later draft; unpublish and takedown give the identical 404 for page, image and form;
  - an image is 404 publicly until a published version references it; another site's image id is refused by `site.edit`;
  - form: contact and deal with source `website`, a notification per admin, `form_count` bumped; honeypot stores nothing; no consent is 400; the sixth post from one IP is 429; no ContactForm is 404;
  - `site.platform_set` is forbidden to ordinary admins and writes the audit row; `workspace.update` cannot set `pro`;
  - the 31st changing turn in a month is `plan_limit` while `site.edit` still works, and Pro lifts it;
  - internal commands answer not found over HTTP; another workspace reads nothing (RLS).
- **Web unit.** `waHref`, `themeStyle`, and the proxy's decision as a pure function `route(host, path, method)`.
- **e2e** (`e2e/site-publish.spec.ts`). CI sets `SITES_DOMAIN=localhost:3000` and `SITES_REVALIDATE_SECRET` on API and web; there is no SMTP, so the email gate is off. Chromium resolves `*.localhost` itself, so the test navigates the browser instead of using `page.request`:
  1. Sign up, create a site, see "Not allowed" for `google-pay` in the modal, publish as `e2e-<uniq>`.
  2. `page.goto('http://e2e-<uniq>.localhost:3000/')`: business name in the `h1`, no iframe, the badge, a CSP response header, `context.cookies(siteUrl)` empty.
  3. `/login` and `/api/auth/me` on the site host are 404; `/published/e2e-<uniq>` on the app host is 404; `nope-<uniq>.localhost:3000` is the 404 page.
  4. Submit the contact form, see the thank-you, find "Inquiry from …" in CRM leads and the notification.
  5. Change the Hero title with `/tool`; the pill says "Unpublished changes" and the live site keeps the old title; "Publish changes", then `expect.poll` the live site for the new one.
  6. Unpublish; the site is 404.

## 14. Work packages

Rules as Phase 0 section 11. **Step 0 (C, one commit, before A and B branch):** the new `catalog.json` (both copies), `templates/` with `templates.go` moved out of `commands.go`, and the exported signatures `ImageIDs(data) []string`, `HasBlock(data, type) bool`, `TurnInput.Images`. After it, no file is shared.

| Pkg | Scope | Owns |
|---|---|---|
| A | Migration, publish, platform, public endpoints, lead and file commands, email verification | `migrations/1290_site_publish.sql`; `sites/{publish.go, subdomain.go, blocklist.txt, plan.go, public.go, subdomain_test.go}`, `sites/commands.go`, `sites/job.go`; `leads/inquiry.go` and one line in `leads/leads.go`; `files/{image.go, image_test.go}` and the command list in `files/files.go`; `auth/verify.go`, `server/authapi.go`, one line each in `auth/oidc.go` and `auth/saml.go`; `command/platform.go` and its call in `marketplace/marketplace.go`; `server/{sitespublic.go, server.go, sites_publish_test.go}`; `worker/wire.go`; `docs/deploy.md` |
| B | Proxy, published route, builder UI, e2e | `apps/web/src/proxy.ts`; `app/published/[sub]/**`; `app/site-revalidate/route.ts`; `app/w/[ws]/site/**`; `app/site-preview/page.tsx`; `components/sites/{published.tsx, contact-form.tsx, publish-modal.tsx, settings-panel.tsx, upload.tsx, help.tsx, builder.tsx, start.tsx, renderer.tsx}`; `lib/sites.ts`; `components/notifications.tsx` (the `lead` kind); `next.config.ts`; `e2e/site-publish.spec.ts`; the environment lines in `.github/workflows/ci.yml` |
| C | Catalog, blocks, templates, loop rules, eval | `sites/{catalog.json, catalog.go, ops.go, loop.go, templates.go, templates/*.json, catalog_test.go, ops_test.go, loop_test.go}`; `apps/web/src/components/sites/{catalog.json, blocks.tsx}`; `apps/api/cmd/siteeval/**` |

B builds against sections 3, 6 and 8 with stubbed responses; `blocks.tsx` (C) and `renderer.tsx` / `published.tsx` (B) meet at the block context in section 8.

## 15. Open questions

1. The gate checks the person publishing, not the workspace owner. Is that right, or must the owner also be verified?
2. Subdomain fixed after first publish: acceptable for launch, or is one rename needed for typos?
3. Is `cnote.site` (or which domain) bought, and who files the Public Suffix List request?
4. Support hours copy (Mon–Sat, 10 am–7 pm IST) and which workspace is the support workspace.
5. With 30 edits and no manual text editor, an owner at the cap cannot fix a typo until next month. Should typo-sized edits be free, or the cap wait for the Phase 2 prop editor?
6. Should the blocklist's offensive-word list include Hindi and other Indian-language transliterations at launch?
7. Differences from `site-builder-phase1-ux.md` for the lead to settle. This design says no to each, to stay small:
   - "Change address" after publishing (P8 fixes the address).
   - Web fonts per template (Inter, Poppins, Mukta…): Phase 0 uses system font stacks and the CSP has no font host; self-hosted font files would be needed.
   - Alt text written by the assistant: the model never sees the image. Here alt text is the owner's (`image_alt`, caption) and empty means decorative.
   - QR code download on the success state (`lib/qr.ts` exists, so it is cheap to add).
   - Template names and block lists differ from the table in section 8; the UX table should win on content, this one on which blocks exist.
8. Which space should website leads land in when the workspace has never set up its inquiry form: the oldest space, or ask in the publish modal?

## 16. Lead decisions on the open questions

1. Gate on the person publishing (must have a verified email); the owner is not checked separately.
2. Subdomain stays fixed after first publish in Phase 1; support (platform admin) can change it on request.
3. `SITES_DOMAIN` is an env setting; the domain purchase and Public Suffix List request are an owner task tracked outside the code. With it unset, publishing is unavailable and the builder says so.
4. Support hours Mon–Sat, 10 am–7 pm IST; the support workspace is given by its public form token in an env variable (`SUPPORT_FORM_TOKEN`); unset hides "Talk to a person".
5. Caps raised so a typo never waits a month: included tier 100 AI edits/month, Website Pro 500. Only chat turns that change the page count. (At ~₹0.26 per edit with buffer, 100 edits cost ~₹26/month.)
6. Yes: the blocklist ships with a short list of common Hindi transliterations of offensive words alongside English.
7. As this design says: no address change, system font stacks only (the restaurant heading uses the system serif stack), alt text is the owner's. QR download on the success state: yes (`lib/qr.ts` exists). Template content (names, block order, palettes, copy) follows the UX doc's table; which blocks exist follows section 8 here.
8. Oldest space; the publish modal states where enquiries will go ("New enquiries appear in Leads, in <space>"), no picker.

## 17. Binding revisions after Codex design review (supersede earlier sections, including 16 where they differ)

1. **Platform admin by user id.** `site.platform_set` is allowed only for user ids listed in `CNOTE_PLATFORM_ADMIN_USER_IDS` (not the email allowlist). Pro, takedown and rename live in a separate table `sites.site_platform` with no INSERT/UPDATE/DELETE grant to the app role; the only writer is a `SECURITY DEFINER` function with `revoke all ... from public` and execute granted to the app role, taking the actor from the command principal, never from a parameter. Actions: `pro_on`, `pro_off`, `takedown`, `restore`, `rename` (old name released at once, old links 404; audit row stores old and new). Tests include direct SQL attempts as the app role.
2. **Host routing.** `SITES_DOMAIN` and the app host are runtime env, read by `proxy.ts` per request (never frozen at build). The host is canonicalised (lowercase, trailing dot removed, port must be in an allowlist) and must be exactly one label under `SITES_DOMAIN`; anything else on the sites domain is a 404; `X-Forwarded-Host` is ignored. The API refuses to start if `SITES_DOMAIN` equals or is a suffix/parent of the app host. The proxy runs before all rewrites; a sites-domain request can only reach the published-site route and its asset paths (tested for encoded paths, RSC/data requests and HEAD).
3. **No full-route cache.** Published pages render per request from one API read and are served with `Cache-Control: public, max-age=60`; images likewise `max-age=60`. Unpublish, takedown, republish and rename take effect within one minute everywhere; the UI says "within a minute". No tag invalidation, no shared cache, no purge jobs.
4. **One public read.** `site.public_get {host}` (internal, called by the web server with a server token) returns the published version's data, plan badge and form status after checking site published, not taken down. Images are served by `GET /api/sites/img/{site_id}/{image_id}` which checks, in one query, that the image belongs to that site and workspace and is referenced by the current published version. Site attachments get composite FKs `(workspace_id, site_id)`.
5. **Trusted proxies.** New `PUBLIC_TRUSTED_PROXIES` (CIDR list). The client IP is the rightmost `X-Forwarded-For` entry not in that list, used only when the direct peer is trusted; otherwise the peer address. Form and verification limits are stored in Postgres (shared across replicas, rows bucketed by hour and deleted after a day), keyed by a keyed hash of the IP.
6. **Contact form.** One endpoint `POST /api/sites/form/{site_id}`; body ≤ 4 KB, field limits from the catalog. Limits: 5 per IP per site per hour, 200 per site per day; site admins can pause the form (setting on the site). Same email + message within 10 minutes is a no-op success. The insert runs in a transaction that locks the site row and rechecks published/not taken down. Consent is stored with each submission (timestamp + text version) on the lead's activity, not only on the contact. No CORS headers (same-origin form post from the site page).
7. **Lead destination.** At first publish the site stores `lead_space_id` = the workspace's oldest space; the publish modal names it. The internal `inquiry.receive` takes that space id explicitly (no `crm.inquiry_forms` row). If the space no longer exists at submit time, the current oldest space is used; if none, the submission is rejected with a friendly error and admins are notified. Notifications go only to workspace admins who can read that space.
8. **Email verification.** 6-digit code from `crypto/rand`, stored as HMAC (master key) with the target email, 10-minute expiry, 5 attempts, consumed atomically; 5 sends per user per hour. OIDC `email_verified=true` counts; SAML does not. No SMTP configured means fail closed: publishing is unavailable and the builder says why. `SITES_TEST_SKIP_VERIFY=1` exists for tests only and is refused when `PUBLIC_BASE_URL` is https.
9. **Rendering.** Every link-like prop, including generated `wa.me` links and the "Get directions" link (`https://www.google.com/maps/search/?api=1&query=<encoded address>`), passes the Phase 0 URL rule; data is re-validated at publish and again before render. JSON-LD is serialised with `<`, `>` and `&` escaped. The live ContactForm is a separate client component from the inert preview one. CSP keeps `script-src 'self' 'unsafe-inline'` because Next injects inline bootstrap scripts; documented residual risk, acceptable because every value is escaped data and no tenant script exists. Published sites never set or read cookies.
10. **Abuse.** Every published page (Pro too) has a small "Report this site" link to a public report form that creates a ticket in the support workspace (`SUPPORT_FORM_TOKEN`) with the site address; takedown is a platform-admin action. Blocklist stays as designed.
11. **Images.** Browser resizes to ≤ 2000 px JPEG before upload; server accepts JPEG/PNG ≤ 1 MB, rejects decoded size over 16 MP using `image.DecodeConfig` before decoding, re-encodes (strips EXIF and appended data, applies EXIF orientation), output ≤ 1 MB, at most 2 concurrent decodes, 40 images per site enforced under a site-row lock, 30 uploads per user per hour. Object is written before the row; a daily sweep deletes objects with no row older than a day. Image props hold image ids only; the server never fetches URLs.
12. **Edit allowance.** Included 100, Pro 500 AI edits per calendar month (IST). `site.chat` refuses when changed turns this month plus active turns reach the allowance; a turn counts when it commits a change. The builder shows "N edits left this month".
13. **Help.** "Talk to a person" opens a small card with hours (Mon–Sat, 10 am–7 pm IST) and two links built from env: `SUPPORT_WHATSAPP` (wa.me) and `SUPPORT_EMAIL` (mailto). No backend, no promise of reply time. Unset env hides it.
14. **UX reconciliation.** QR download on success: yes. No owner-facing address change. Restaurant template includes a ContactForm titled for reservations. Upload copy: "JPG or PNG; large photos are resized for you." Alt text is owner-written. The "new enquiries" chip shows website leads created in the last 7 days and links to Leads; the CRM shows the source "Website" on those leads (add the label if missing); no new CRM filter UI.
