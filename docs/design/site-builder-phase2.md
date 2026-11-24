# AI website builder: Phase 2 technical design (domains, editor, pages, exit)

24 Nov 2026. Branch `feat/site-phase2`, on top of Phases 0 and 1. `site-builder.md` section 14 and `site-builder-phase1.md` sections 16–17 still bind everything here. Screens and copy follow `site-builder-phase2-ux.md`; where the two differ, open question 1 lists it. Paths are repo-relative. Nothing here was compiled or run.

## 0. Decisions, and what is not built

| # | Decision | Why |
|---|---|---|
| Q1 | Pages live inside the version document: top-level `content` stays the Home page, extra pages go in a new `pages[]`. | Every existing version stays valid; versions are immutable, so there is no data migration. Undo, publish and restore cover pages for free. |
| Q2 | Header, Footer and WhatsAppButton are `shared`: stored once (in Home), drawn on every page. | One place to edit the phone number or the menu. |
| Q3 | A custom domain is four columns on `sites.sites`. The owner adds two records. Proof of ownership is the `www` CNAME itself: it must point at **that site's own address** (`<subdomain>.<SITES_DOMAIN>`). Website Pro only. | The CNAME is the token: no TXT record, and no other site can satisfy it. Standard library only (`go.mod` has no DNS or Public Suffix package). |
| Q4 | Every host of a site answers; any host that is not the canonical one redirects (302) to it. Canonical is `www.<domain>` when connected and Pro, else the subdomain. | One rule covers subdomain → domain, apex → www, and domain → subdomain when Pro ends. 302, not 301, so a disconnect is never stuck in a browser cache. |
| Q5 | Puck (`@puckeditor/core` 0.24.2, pinned) runs only in Edit mode, in its own canvas, rendering our `BLOCKS`. Chat mode keeps the Phase 0 preview frame. | Section 6. |
| Q6 | The editor saves through `site.edit` with one new op, `set_content`. | No new write path. |
| Q7 | Page views are counted by a `sendBeacon` call, not during render. | Pages are cached for 60 s, and a beacon needs JavaScript, which most bots do not run. |
| Q8 | Stock photos are copied into our storage. | `img-src 'self'` stays; visitors never contact Pexels; export and takedown keep working. |
| Q9 | Export is built in Go from the HTML the web app already serves, fetched from a fixed internal address. The contact form becomes call / WhatsApp / email links. | The zip must work after the owner leaves; a form posting to us would not. |
| Q10 | **No cheap-model chat past the cap.** Every `site.chat` turn already runs on the routine model, so this would only mean "no cap". The editor now fixes any text for free. | The cap message ends "You can still edit everything by hand: open Edit." |

Package check (npm registry, 24 Nov 2026): `@puckeditor/core` is the current name (`@measured/puck` is deprecated and points to it); latest stable 0.24.2, MIT, peer `react ^18 || ^19`, no Next peer. It is client-only, so Next 16 is not a constraint, but this was not run. It ships its own TipTap packages for a rich-text field we never use.

Not built: several domains per site, a canonical name without `www`, domain purchase, the Vercel domain API (custom domains need Caddy in front of the web app), unique-visitor counts, AI stock search, moving a block between pages, page reorder or duplicate, rich text, version pruning, notifications about domain status, import of an exported zip.

## 1. Data model: `migrations/1300_site_phase2.sql`

```sql
ALTER TABLE sites.site_versions
  DROP CONSTRAINT site_versions_data_check,   ADD CHECK (octet_length(data::text) < 262144),
  DROP CONSTRAINT site_versions_source_check, ADD CHECK (source IN ('template','chat','edit','editor','restore'));

ALTER TABLE sites.sites
  ADD COLUMN domain text CHECK (char_length(domain) <= 249          -- without "www."
    AND domain ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$' AND domain NOT LIKE '%xn--%'),
  ADD COLUMN domain_verified_at timestamptz,
  ADD COLUMN domain_checked_at timestamptz,
  ADD COLUMN domain_fails int NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX sites_domain_verified ON sites.sites (domain) WHERE domain_verified_at IS NOT NULL;

CREATE TABLE sites.site_stats (               -- daily counters; written only by sites.hit
  workspace_id uuid NOT NULL, site_id uuid NOT NULL, day date NOT NULL,          -- day in Asia/Kolkata
  kind text NOT NULL CHECK (kind IN ('page','ref','visit')),
  key text NOT NULL CHECK (char_length(key) <= 100),       -- page slug ('' = Home), referrer domain, or '' for visit
  n int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, kind, key),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE);
CREATE TABLE sites.image_credits (
  workspace_id uuid NOT NULL, site_id uuid NOT NULL, image_id uuid PRIMARY KEY,
  source text NOT NULL CHECK (source = 'pexels'), source_id text NOT NULL,
  photographer text NOT NULL, photographer_url text NOT NULL, page_url text NOT NULL,
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE);
-- tenant RLS on both; cnote_app: SELECT, DELETE on site_stats; SELECT, INSERT, DELETE on image_credits
```

`SECURITY DEFINER` functions (scalars in, `search_path` pinned, execute to `cnote_app`):

- `host_lookup`, `public_by_id`: dropped and recreated with two more columns, `subdomain` and `domain` (the verified domain when the site is Pro, else null).
- `domain_lookup(p_host)`: the same row for a published, not taken down site whose verified domain is `p_host` with or without `www.`.
- `tls_allowed(p_host) RETURNS boolean`: the same condition, nothing else.
- `domains_due()`: `(workspace_id, site_id, domain, subdomain)` of verified domains, for the sweep.
- `hit(p_site, p_slug, p_ref, p_entry)`: one statement. The site must be published and not taken down, and `p_slug` must be `''` or a page of the published version. Adds 1 to the `page` row, to `visit` when `p_entry`, and to the `ref` row when `p_ref` is set. A 51st distinct referrer in a day is counted as `other`.

## 2. The document

```json
{"root": {"props": {"title": "Sharma Dental", "description": "…", "theme": {…}}},
 "content": [ …Home blocks, including the shared ones… ],
 "pages": [{"slug": "about", "title": "About us", "description": "", "content": [ … ]}]}
```

`Validate` changes (`catalog.go`), still driven by `catalog.json`:

- `pages` is optional, at most 10; `slug` matches `^[a-z0-9]+(-[a-z0-9]+)*$`, 1 to 40 characters, unique; `title` text 80 required; `description` longtext 160. Unknown page fields are rejected.
- 30 blocks per page; the whole document under 256 KB. Block ids are unique across the document, so `update_block_props`, `remove_block` and `move_block` need no page argument.
- A block marked `"shared": true` may appear only in Home. `max` counts per page.
- **Links** gain one form: `/` or `/<slug>`, each optionally followed by `#<block id>`. The slug must exist. The same rule goes into `safeHref(v, slugs)` in TS.
- `ImageIDs`, `HasBlock`, the mask check and the verbatim check walk `pages` too.

Rendering: one TS function `pageView(data, slug)` returns the blocks of a page (Home: `content`; other pages: Header, the page's blocks, then the other shared blocks in Home order) or null. The preview, the published page and the Puck root all call it. Go has no renderer and needs none.

**Catalog additions.** The Go struct ignores unknown JSON keys, so labels cost Go nothing.

- `label` on every prop, and `labels` beside an enum's `values` (the editor's wording; fallback is the key in words).
- `variant`: an optional enum on each block below. The first value is the default and is what a document without the prop renders, so old pages do not change. The model switches it with `update_block_props`; no new tool.

| Block | Variants | Block | Variants |
|---|---|---|---|
| Header | `left`, `centered` | Pricing | `list`, `cards` |
| Hero | `split`, `cover`, `centered` | FAQ | `open`, `accordion` (`<details>`, no JS) |
| Services | `cards`, `list` | CallToAction | `banner`, `card` |
| About | `image-right`, `image-left`, `stacked` | MapHours | `side`, `stacked` |
| Gallery | `grid`, `strip` (CSS scroll snap) | Testimonials | `cards`, `single` |
| ContactForm | `side`, `stacked` | Footer | `simple`, `columns` |

- **Text** block: `title` text 80; `body` longtext 6000, drawn as paragraphs split on blank lines. No markup.
- **Embed** block: `provider` enum; `ref` text 2000, `verbatim`; `title` text 80. Providers are data in a new top-level `embeds` key, read by Go, the renderer and the proxy:

```json
"embeds": {
 "youtube":      {"label":"YouTube",      "pattern":"^[A-Za-z0-9_-]{11}$",
                  "src":"https://www.youtube-nocookie.com/embed/{id}",            "frame":"https://www.youtube-nocookie.com"},
 "google_maps":  {"label":"Google Maps",  "pattern":"^[A-Za-z0-9!._%:-]{20,2000}$",
                  "src":"https://www.google.com/maps/embed?pb={id}",              "frame":"https://www.google.com/maps/embed"},
 "google_forms": {"label":"Google Forms", "pattern":"^[A-Za-z0-9_-]{20,120}$",
                  "src":"https://docs.google.com/forms/d/e/{id}/viewform?embedded=true", "frame":"https://docs.google.com/forms/"},
 "calendly":     {"label":"Calendly",     "pattern":"^[a-z0-9_-]{1,60}(/[a-z0-9_-]{1,60})?$",
                  "src":"https://calendly.com/{id}",                              "frame":"https://calendly.com"}}
```

`sites/embed.go`: `NormalizeEmbed(input) (provider, ref, error)` runs inside `Apply`. It accepts a URL of a listed provider or a pasted `<iframe …>` snippet (it takes the first `src="…"` and parses it as a URL; the HTML is never stored). `EmbedSrc(provider, ref)` checks the pattern and fills `src`; TS `embedSrc` does the same from the same catalog. `Validate` calls `EmbedSrc` for every Embed block. The server never fetches a link, so short links (`maps.app.goo.gl`, `forms.gle`) are refused with the hint "In Google Maps choose Share, then Embed a map".

**Legal pages.** `sites/templates/legal_privacy.json` and `legal_terms.json`: `{slug, title, blocks: [Text…]}` with slots `{{name}} {{address}} {{email}} {{phone}} {{url}} {{date}}`. An empty slot stays as visible text ("[add your address]"), never truncated (14.11). Privacy covers: who runs the site, what the contact form collects and why, that C Note hosts and stores it, cookie-free visit counts, embedded third-party content, how to ask for correction or deletion. Terms covers: use of the site, accuracy of prices, liability, governing law, contact. The add dialog and a builder-only banner on pages with slug `privacy` or `terms` read "Template, not legal advice. Read it and have it checked before you publish." Adding one puts a link in the Footer, not the Header.

## 3. Commands

`site.edit` ops (member, as today) and model tools share `Apply`:

| Op | Input | Notes |
|---|---|---|
| `add_page` | `{title, slug?, template?}` | Slug defaults to the title slugged, then `-2`…; `template` is `privacy` or `terms`. Other pages get a Header link when it is under its limit (raised from 5 to 8). |
| `remove_page` | `{page}` | Also removes list items and clears scalar links that point to it. |
| `set_page` | `{page?, title?, description?, slug?}` | `page` omitted = Home (the Phase 1 behaviour). A slug change rewrites links to it. |
| `add_block` | `+ page?` | Default Home; the builder always sends the open page. |
| `set_content` | `{page, content: Block[]}` | Editor only, not a model tool. A block whose id exists in the base version keeps it if the type matches; every other block gets a fresh id from Go. |

| Command | Group, role | Input | Output |
|---|---|---|---|
| `site.versions` | read, member | `{id, before_seq?, limit ≤ 50}` | `[{id, seq, source, summary, created_by: {id, name}, created_at, is_draft, is_published}]` |
| `site.stats` | read, member | `{id}` | `{days: [{day, visits, views}] (60), pages: [{slug, views}], referrers: [{domain, views}] (top 10), enquiries}` for the last 30 days |
| `site.domain_set` | write_items, admin, person, Pro | `{id, domain}` | as `site.domain_check` |
| `site.domain_check` | read, admin | `{id}` | `{domain, records: [{type, name, value, found, ok}], steps: {www, apex, tls, live}, verified}` |
| `site.domain_remove` | write_items, admin, person | `{id}` | `{site}` |
| `site.stock_search` | read, member | `{id, query, page?}` | `{photos: [{id, thumb, alt, photographer, photographer_url, url}]}` |
| `site.stock_import` | write_items, member | `{id, photo_id}` | `{image: {id, filename, credit}}` |

Changed: `site.edit` takes `source?: "editor"` (default `edit`, shown as "Toolbar"). `site.restore` takes `as_new?: true`: instead of moving the pointer it adds a version with `source = 'restore'` and summary "Restored version N"; undo and redo keep moving the pointer. `site.chat` takes `page`. `site.get` adds `domain: {name, verified}?`, `domains_available`, `stock` (key configured) and `credit` on images; `site.url` is the canonical URL. `site.platform_set rename` is refused while a domain is connected (section 12).

HTTP, in `server/sitespublic.go`:

| Endpoint | Does |
|---|---|
| `GET /api/sites/tls_ask?domain=…&token=…` | Section 4. 200 or 404. |
| `POST /api/sites/hit/{site_id}` | Section 8. Always 204. |
| `GET /api/w/{ws}/sites/{site}/export.zip` | Section 10. Session, admin, people only; authorised the way the draft-image route wraps `site.image_get`. |

## 4. Custom domains

`sites/domain.go`. Environment: `SITES_APEX_IP` (API and web; the public address or addresses of the edge; unset hides the feature and leaves routing exactly as Phase 1) and `APP_HOSTS` (web: app hostnames beyond the one in `PUBLIC_BASE_URL`). `*.<SITES_DOMAIN>` must be published as A/AAAA records to those addresses, not as a CNAME, or step 1 below cannot see the owner's record.

**Connect.** The owner types `sharmadental.in`. `site.domain_set` lowercases it, strips scheme, path, trailing dot and a leading `www.`, and refuses: non-ASCII and `xn--`, IP literals, anything equal to or under `SITES_DOMAIN` or an app host, a site with no address yet, and a domain verified on another site. It stores the name and clears verification. Two records are shown, in registrar wording:

| Type | Name | Value | Proves |
|---|---|---|---|
| CNAME | `www` | `<subdomain>.<SITES_DOMAIN>` | ownership, and routes `www` |
| A | `@` | `SITES_APEX_IP` | makes the address without `www` redirect |

**Check.** `site.domain_check`, at most 120 per site per hour (`db.Bump`); the sheet calls it every 30 s while open and on "Check again". `found` carries what DNS answered, for "Record 1 points to 184.168.x.x. It should be …".

1. `www`: `LookupCNAME("www."+domain)` is this site's own address. True → `domain_verified_at = now()`; a unique-index conflict answers "This domain is connected to another C Note website."
2. `apex`: every address of `domain` is in `SITES_APEX_IP`. Not required for `live`; the redirect simply does not work until it is true.
3. `tls`: `tls.Dial` to `SITES_APEX_IP:443` with `ServerName = "www."+domain`, 5 s. We dial only our own edge, and the dial triggers the first certificate. Skipped (true) on `http` development.
4. `live`: verified, TLS, published, Pro.

A proxied (orange-cloud) record fails step 1; the sheet says "set it to DNS only".

**Staying honest.** A daily sweep (`sites.domains`) repeats step 1 for `domains_due()`. Three failed days in a row clear `domain_verified_at`; the site is back on its subdomain and the builder pill says "Domain not connected". A lapsed or re-pointed domain frees itself.

**Ask endpoint.** Caddy calls it before issuing. Constant-time check of `token` against `SITES_SERVER_TOKEN`; the name is canonicalised and regex-checked; names under `SITES_DOMAIN` and app hosts are refused; then `select sites.tls_allowed($1)`. No DNS, no other I/O, one indexed query. It approves verified domains of published, not taken down sites, Pro or not, so the redirect back to the subdomain has a valid certificate.

**Exit right**, shown in the sheet, in Settings ("Your website is yours") and in `docs/deploy.md`: "Your domain stays yours. You keep it at your registrar; C Note never registers, transfers or locks it. Disconnect at any time: your website goes back to `<name>.<SITES_DOMAIN>` and you can point the domain anywhere." `site.domain_remove` clears the four columns; the certificate is simply not renewed.

## 5. Serving and routing

`route()` in `lib/sites.ts` gains the app hosts and the custom-domain switch:

1. Host is an app host (`PUBLIC_BASE_URL`, `APP_HOSTS`), an IP literal or a single label → the app, as today.
2. Host is one label under `SITES_DOMAIN` → a site, as today.
3. Otherwise, with `SITES_APEX_IP` set and a syntactically valid DNS name on the allowed port → a site keyed by the full host. With it unset → today's behaviour.

Site hosts get the Phase 1 paths plus `GET /<slug>` and `POST /api/sites/hit/<uuid>`. The route directory becomes `app/published/[host]/` and gains `[slug]/page.tsx`; the proxy rewrites to `/published/<host>[/<slug>]` and `publicGet` posts the host as received. `sites.LookupHost` tries the subdomain, then `domain_lookup`. The page redirects (302, path kept) when the request host is not the host of `site.url`, and 404s for an unknown slug.

Per page: `<title>` and description from the page (Home: root), canonical `<url>/<slug>`, LocalBusiness JSON-LD on Home only; `sitemap.xml` lists every page.

**Headers.** The proxy's CSP gains `frame-src` with the four `embeds.*.frame` sources. Every published page also emits `<meta http-equiv="Content-Security-Policy" content="frame-src …">` naming only the providers used on that page, or `'none'`. Two policies intersect, so a page without embeds can frame nothing. (The proxy cannot see page content, and a server component cannot set headers.) Custom-domain hosts get `Strict-Transport-Security: max-age=86400` without `includeSubDomains`: the apex response would otherwise pin the owner's mail and shop subdomains to https for a year, long after they leave.

**Caddy** (replaces the block in `docs/deploy.md`):

```
{
  on_demand_tls {
    ask http://api:4000/api/sites/tls_ask?token={$SITES_SERVER_TOKEN}
  }
}
*.{$SITES_DOMAIN} {
  tls { dns cloudflare {env.CF_API_TOKEN} }
  reverse_proxy web:3000
}
https:// {
  tls { on_demand }
  reverse_proxy web:3000
}
```

Caddy adds `domain=<name>` to the ask URL, issues only on a 2xx and passes `Host` through. Its docs ask for an answer in milliseconds with no network calls, and say not to use the old `interval` and `burst` limits.

`// shortcut: a handshake flood with random names costs one indexed query each; rate-limit new connections at the firewall if it happens.`

## 6. Editor (Puck)

`components/sites/editor.tsx`, loaded with `next/dynamic` (`ssr: false`) the first time the person opens **Edit**, so chat-only sessions never download it.

**Coexistence.** Two canvases, one set of block components. Chat mode keeps the `/site-preview` frame with its selection, pulse, toolbar and no-navigation rules. Edit mode puts Puck's built-in iframe canvas in the same place: Puck must own that DOM for drag and drop, and its `data` is fixed at mount, so it cannot follow a running turn. Switching tabs swaps the canvas and carries the selected block id across. Puck is remounted with `key = draft_version_id + page` whenever a turn, undo or restore moves the draft.

**Layout** (compositional API, as the UX doc draws it): `<Puck>` wraps the builder body. The left column's Edit tab shows `<Puck.Fields />` for the selected block, or `<Puck.Outline />` (the section list, drag to reorder) and "Add section" (`<Puck.Components />`) when nothing is selected. The centre is `<Puck.Preview />` with a 390 px viewport option. Root fields are the page's name, address and description, then the theme.

**Config from the catalog.** `puck-config.ts` exports `puckConfig(catalog, BLOCKS, ctx)`; nothing per block is written by hand. `render` is the existing block component; `root.render` applies `themeStyle` and, off Home, draws the shared blocks as fixed chrome.

| Catalog kind | Puck field |
|---|---|
| `text`, `phone`, `email` | `text` |
| `longtext` | `textarea` |
| `enum` | `select`; `variant` is a `custom` thumbnail list |
| `list` | `array` with `arrayFields` from `props`, `max` |
| `color` | `custom`: theme swatches, then native input plus hex |
| `link` | `custom`: pages, sections, phone, WhatsApp, other |
| `image` | `custom`: the photo picker (your photos, free photos) |
| Embed `ref` | `custom`: one paste box; Go normalises on save |

Never `richtext` or `slot`. A block type at its `max`, and shared blocks off Home, have insert and duplicate turned off (`resolvePermissions`; confirm the name against the 0.24 docs).

**Save.** Autosave 800 ms after the last change, one request at a time: `site.edit {expected_version_id, source: "editor", ops: [set_content, set_page?, set_theme?]}` with Puck's `zones` key dropped; each save uses the version id the previous one returned. Go writes the summary from the difference ("Updated Hero", "Reordered sections"). Go stays the only validator: a rejected save shows "Not saved" with Go's message ("Hero.title: at most 80 characters") and the fields keep their values. Sending a chat message flushes the pending save first. A conflict shows the 14.13 banner and keeps the local copy.

`// shortcut: every autosave is a version row (about 10 KB). History folds consecutive editor rows by one person within 10 minutes on the client; prune on the server when a site passes a few thousand versions.`

## 7. Version history

The History sheet lists `site.versions`: time, person ("C Note AI" for chat), the summary, a source chip (Chat, Editor, Toolbar, Restore, Template) and "Current" / "Published" marks. Selecting a row loads `site.get {version_id}` into the preview frame read-only under "Viewing 23 Nov, 6:10 pm · not your live site · Back to now · Restore"; chat and Edit are disabled meanwhile. Restore confirms with the 14.13 rule, reworded because nothing is replaced now ("4 later changes are kept in history. Your live site changes only when you publish.") and calls `site.restore {as_new: true}`.

## 8. Analytics

A 10-line client component on published pages calls `navigator.sendBeacon('/api/sites/hit/<site_id>', JSON.stringify({p: slug, r: document.referrer}))` once per load; `connect-src 'self'` already allows it.

The handler takes at most 512 bytes. It drops requests with no user agent, with one matching `bot|crawl|spider|slurp|preview|monitor|headless|lighthouse|curl|wget|python|go-http`, or with `Sec-Purpose: prefetch`. It reduces the referrer to a lowercase hostname; a referrer that is one of the site's own hosts is internal navigation (no referrer row, not an entry), anything else is an entry and counts one **visit**. Then `select sites.hit(...)`. A cap of 20,000 an hour per site (`db.Bump`) bounds a flood. No cookie, IP, user agent or per-visitor value is stored or hashed.

`// shortcut: anyone who posts in a loop can inflate the counts; they are a vanity number. Add a per-address bucket if they ever drive billing.`

The card shows visits, pages viewed, enquiries (from `sites.form_log`), the change against the previous 30 days, a 30-point inline SVG chart, top pages and top referrers (the web maps `google.*`, `wa.me`, `instagram.com` and an empty referrer to friendly names). Rows older than 400 days go in the existing `sites.prune` sweep.

## 9. Stock photos

`sites/stock.go`; the "Free photos" tab is hidden when `PEXELS_API_KEY` is unset.

**Terms** (pexels.com/license and the API documentation, read 24 Nov 2026). Licence: free to use, attribution not required, modification allowed; not allowed: showing identifiable people in a bad light, selling unaltered copies, implying endorsement, redistributing on other stock or wallpaper platforms. API: show a prominent link to Pexels where the API is used, credit photographers when possible, do not replicate Pexels' core functionality; 200 requests an hour and 20,000 a month by default.

- **Search**: `GET https://api.pexels.com/v1/search?query=&per_page=24&page=`, header `Authorization: <key>`; 30 per user per hour. The picker shows thumbnails straight from `images.pexels.com` (the builder origin has no image CSP), the photographer under each, and "Photos provided by Pexels".
- **Import**: the client sends only a numeric `photo_id`. The server asks `GET /v1/photos/{id}` for the file address, so no client-supplied URL is ever fetched. It downloads `src.large2x`, or `src.large` when that is over 1 MB, then calls `file.image_put` (the Phase 1 pipeline: sniff, 16 MP check before decoding, re-encode, 40 per site, 30 per user per hour) and inserts the credit row in the same transaction.
- **Credit** is stored per image, shown in the picker and the image field ("Photo by A on Pexels") and listed in the export's README. Published pages show none: the licence does not require it (open question 5).

`// shortcut: no search cache; the whole platform shares 200 searches an hour. Ask Pexels for a higher limit, or cache by query, when the picker shows "aren't loading right now".`

## 10. HTML export

`sites/export.go`. New env `SITES_WEB_URL` (API: the web app's internal address, default `http://localhost:3000`).

For the published version only, Go requests each page from `SITES_WEB_URL` with `Host: <subdomain>.<SITES_DOMAIN>`, `?static=1` and `x-sites-token`. With a valid token the page renders in static mode: no redirect to the custom domain, no beacon, no badge or report link, no canonical or `og:url`, and the ContactForm's form slot holds call, WhatsApp and email links. Go then:

1. removes every `<script>` except `application/ld+json`, and script preloads;
2. fetches the linked stylesheets (path must match `^/_next/static/[\w./-]+\.css$`) into `assets/site.css`;
3. rewrites `/api/sites/img/<site>/<id>` to `images/<id>.jpg|png` and adds those files through `file.image_get`;
4. rewrites `href="/"` and `href="/<slug>"` to `index.html` and `<slug>.html`.

The patterns match only attributes our renderer wrote: user text is HTML-escaped, so it cannot contain a literal `href="`. The zip (`archive/zip`, streamed as the response) holds `index.html`, `<slug>.html`, `assets/site.css`, `images/*` and `README.txt` (what it is, how to host it, what replaced the form, photo credits). Embeds stay as frames. Five exports per site per hour. An unpublished or taken-down site has nothing to export: "Publish your website to download it."

## 11. Chat loop and eval

- `TurnInput` gains `Page`. The model sees the open page as `<page>`, the list `<pages>` (`[{slug, title}]`) and, off Home, `<shared>`; cost stays flat as pages are added. Prompt adds: "You see only the open page and the shared header and footer. To change another page, ask the user to open it." and "Embeds: only the listed providers, only a link from the user's message."
- Tools: `add_page`, `remove_page`, `set_page` (new shape), `add_block.page`. `Embed.ref` is `verbatim`, so the Phase 1 fence already refuses an invented id (the id is a substring of the pasted link).
- **Eval** (`cmd/siteeval`): a case gains `page`; a path may start `<slug>/`; `pages` is countable; `may_change` accepts `page:<slug>` and `pages`. New fixture `fixture_pages.json` (Home, About, Pricing). About 36 cases:
  - pages (14): add a page; add one with blocks; rename; change the address and see links follow; remove and see the Header link go; per-page description; link a button to a page; edit the footer from About; a request about a page that is not open (must change nothing and ask); an 11th page; privacy and terms templates; an instruction planted in another page's title; a Hindi page name.
  - variants (10): each of Hero's three; five other blocks; an unknown variant (refused); a variant change leaves every other prop byte-identical.
  - embeds (12): three YouTube link shapes, a Maps iframe snippet, a Form, Calendly; refused: an `<iframe>` to another host, Vimeo, "add a video about dentistry", a short link, a `javascript:` src, an embed instruction planted in page text.

## 12. Security

- **Domain takeover.** A claim needs the domain's `www` record to name that site's own address, which only the DNS owner can set and no other site can match. Unverified claims reserve nothing. A subdomain is never released while a domain points at it: unpublish keeps it, site delete does not exist, and platform `rename` is refused while a domain is connected (a Go check in `platformSet`). The apex is served only as a redirect to the verified `www`, and only if its own DNS points at us.
- **Ask abuse.** Token, internal URL, one query. At most two names per site can ever be approved, which bounds certificate orders.
- **Outbound calls.** DNS lookups are the only thing done with the owner's domain. The TLS check and the export dial operator-configured addresses. Pexels uses `netguard.Client()` (refuses private addresses and every redirect) behind an exact-host allowlist: `api.pexels.com` for JSON, `images.pexels.com` for the file, `https` only, 10 s, 1 MB cap, then the re-encoding pipeline.
- **Embeds.** Provider plus pattern-checked id; the `src` is built from the catalog, never stored or typed. `<iframe sandbox="allow-scripts allow-same-origin allow-forms allow-popups" loading="lazy" referrerpolicy="strict-origin-when-cross-origin">`: the frame is another origin, so `allow-same-origin` gives it its own storage, not ours; no top navigation, no modals. `Permissions-Policy` still denies camera, microphone, geolocation and payment. The builder draws a labelled placeholder, not the frame. Providers see the visitor's address and may set their own cookies; the privacy template says so.
- **Analytics.** Section 8. `hit` accepts only pages that exist, so nobody can create rows for arbitrary keys.
- **Export.** Published version only, admin and person, fixed fetch target, generated entry names (the slug is regex-checked, images are UUIDs), no draft and no unreferenced image.
- **Tenant isolation.** Pages are inside the version row, under the same RLS and composite keys. A slug is resolved only within the version the host lookup returned. Image and form endpoints are unchanged (`public_image` already searches the whole document). Each custom domain is its own origin and sets no cookie.
- **Editor.** `set_content` goes through `Validate` like any op; ids not in the base version are replaced, so a client cannot choose them.

## 13. Tests

- **Go unit** (`sites`): pages (limits, duplicate slug, shared block off Home, link to a missing page); each new op, including link cleanup on remove and rewrite on rename; `set_content` id rules and summary; every variant value accepted, unknown rejected; `NormalizeEmbed` / `EmbedSrc` from `testdata/embeds.json`; domain normalisation; the check with a fake resolver (no record, another site's address, CNAME to elsewhere, apex on a foreign address); bot table; export rewrites on a saved HTML sample; the Pexels client against `httptest` (redirect, wrong host and oversize all refused). `TestWebCatalogInSync` unchanged.
- **Server** (`server/sites_phase2_test.go`): old single-page versions still open, publish and restore; ask is 404 without the token, for an unverified domain, an unpublished site, a taken-down site and a name under `SITES_DOMAIN`, and 200 for `www.<domain>` and `<domain>`; `domain_set` needs Pro and an admin; a second site cannot verify the same domain; rename is refused with a domain connected; the sweep unverifies after three failures; `public_get` by domain returns the right canonical URL in each Pro state; a hit for an unknown slug, an unpublished site or a bot stores nothing; another workspace reads no stats, credits or versions; `restore as_new` adds a `restore` version; the editor source is recorded; export is refused to members and contains no `<script src`.
- **e2e.** Chromium is launched with `--host-resolver-rules=MAP *.cnote-e2e.test 127.0.0.1`; the API gets `SITES_APEX_IP=127.0.0.1` and `SITES_TEST_DNS=cnote-e2e.test` (names under that suffix pass both DNS checks; the API refuses to start with it when `PUBLIC_BASE_URL` is https, like `SITES_TEST_SKIP_VERIFY`). Pro is switched on by calling `sites.platform_set` as the database owner from a helper.
  - `site-domain.spec.ts`: connect `e2e-<uniq>.cnote-e2e.test`, see the two records and the ticks; `www.` serves the site; the subdomain and the apex redirect to it; `/login` on the domain is 404; disconnect and the subdomain serves again.
  - `site-pages.spec.ts`: add About, see the menu link, publish, open `/about`, find it in `sitemap.xml`; add a privacy page and see the banner.
  - `site-editor.spec.ts`: open Edit, change the Hero heading, see "Saved" and an Editor row in History; preview an older version and restore it.
  - `site-embed.spec.ts`: paste a YouTube link; the published page has a sandboxed frame and a meta policy naming only the nocookie host; a page without embeds has `frame-src 'none'`.
  - `site-export.spec.ts`: download the zip; `index.html` has no script tag and no form.
  - Stats: load the published page, then `expect.poll` the card for 1 visit.

The web app has no unit-test runner, so `route()` and `pageView` are covered through these specs.

## 14. Work packages

Rules as Phase 0 section 11. **Step 0 (lead, one commit):** `catalog.json` in both places (variants, labels, `shared`, Text, Embed, `embeds`); `migrations/1300_site_phase2.sql`; `sites/phase2.go` with an empty `phase2Commands()` appended in `Commands()` and the signatures `NormalizeEmbed`, `EmbedSrc`; stub `Text` and `Embed` components in `blocks.tsx` so `tsc` passes; the new types and `siteApi` shapes in `lib/sites.ts`. After it no file is shared.

| Pkg | Who | Scope | Owns |
|---|---|---|---|
| A | Codex, nine small tasks in this order | 1 `Validate` pages and links · 2 new ops · 3 loop prompt and tools · 4 `site.versions`, `restore as_new`, edit source, cap message · 5 embed · 6 domain commands, sweep, ask · 7 stats and hit · 8 stock · 9 export | `apps/api/internal/modules/sites/*.go` and their tests; `server/{sitespublic.go, server.go, sites_phase2_test.go}`; `worker/wire.go`; `cmd/siteeval/{assert.go, main.go}`; `docs/deploy.md` |
| B | Sonnet | Proxy and routes, variants, Text and Embed, page switcher, editor, history, stats card, domain sheet, photo picker, export button, e2e | `apps/web/**` (incl. `package.json` for Puck); `e2e/site-*.spec.ts`; `playwright.config.ts`; the env lines in `.github/workflows/ci.yml` |
| C | Tabnine | Eval and test data, legal text | `cmd/siteeval/{cases.json, fixture_pages.json}`; `sites/templates/legal_*.json`; `sites/testdata/{embeds.json, domains.json, bots.json, export_sample.html}` |

B builds against sections 2, 3 and 5 with stubbed responses. C needs only section 2 and the case format in section 11; A's tests read C's files, so C lands first or A commits two-row placeholders.

## 15. Open questions

1. Differences from `site-builder-phase2-ux.md` for the lead to settle. This design says no to each, to stay small:
   - Chat and Edit as tabs in one column (UX doc) or side by side (the Phase 2 brief). Only the placement of `Puck.Fields` changes.
   - Embeds from "a normal share link" with a title and thumbnail preview: YouTube, Forms and Calendly links work, but a Google Maps share link is a short link the server would have to fetch. Maps needs "Embed a map"; no preview.
   - "Visitors": we count visits (arrivals from outside the site), not unique people, because nothing identifies a person. Label it "visits".
   - Export as a prepared file with a 24-hour link: here it is a direct download that takes a few seconds.
   - Menu order, "hide from menu" and page duplicate: the menu is the Header's link list, edited like any list.
   - A WhatsApp or email message when the domain goes live: none; the sheet and the Settings pill show it.
   - Fold of editor autosaves in History is on the client only.
2. Production web is on Vercel (`docs/deploy.md`); on-demand TLS needs Caddy in front. Does the web app move behind Caddy, or do custom domains wait for a Vercel domain-API design?
3. `SITES_APEX_IP` must stay fixed for years (owners' A records depend on it), and `*.<SITES_DOMAIN>` must be A records. Who provisions that?
4. The legal templates need a lawyer's read (DPDP Act wording, grievance contact) before launch. Who?
5. Photo credits on published pages: none (this design), or one small "Photos: … on Pexels" line?
6. Puck 0.24.2 was published on 23 Nov 2026. Pin it, or take 0.24.1 (18 Nov) until it has a week of use?
7. Pages by plan: this design gives every plan Home plus 10. Fewer on the included plan?
8. Should embeds be click-to-load, so no third party is contacted until the visitor asks?

## 16. Lead decisions on the open questions

1. Follow the UX doc on layout: **Chat | Edit tabs in one column**. Maps embed is built from an address the owner types ("Embed a map of: <address>"), never by fetching a share link. The label is **"visits"**. Export is a direct download. Menu order is the Header's link list. No message when a domain goes live. History folding stays client-side.
2. **Custom domains need Caddy in front of the web app** (the self-hosted / Coolify path). The feature is off unless `SITES_CUSTOM_DOMAINS=1`; on a Vercel-hosted web app it stays hidden. A Vercel domain-API variant is not built.
3. `SITES_APEX_IP` and the wildcard A records are an operator task, documented in `docs/deploy.md` with a warning that the IP must not change.
4. Legal templates ship labelled "Template — not legal advice"; a lawyer's review is an owner task before launch, tracked outside the code.
5. **Stock photos are cut from Phase 2:** Pexels has paused new API key issuance (see `site-builder-phase2-facts.md`). The image picker keeps "Upload your own" only. Revisit when a key is available.
6. Pin `@puckeditor/core` **0.24.2** (newest stable; our rule is newest stable, no pre-releases).
7. Pages: included plan Home + 4, Website Pro Home + 10.
8. Yes: embeds are **click-to-load** (a placeholder with the provider name and "Load video/map/form"); no third-party request until the visitor clicks.

## 17. Binding revisions after Codex design review (supersede earlier sections, including 16 where they differ)

1. **Domain proof per hostname.** The owner adds `www.<domain>` and/or the apex; each hostname is verified separately by a fresh random TXT record `_cnote-verify.<hostname>` = `cnote-verify=<token>` (token per claim, 128-bit), plus its routing record (www: CNAME to `<sub>.<SITES_DOMAIN>`; apex: A to `SITES_APEX_IP`). A claim row is created atomically per normalised hostname (unique across the platform, pending claims expire after 7 days). Only verified hostnames are routed or approved for TLS. Verified hostnames are re-checked daily; two consecutive failures move the hostname to `lost` (not routed, not approved). Disconnect, unpublish, takedown and Pro lapse stop routing and TLS approval at once. Rename keeps custom domains attached.
2. **Rejected hostnames:** IP literals, single labels, our app's registrable domain and any of its descendants or parents, `SITES_DOMAIN` and descendants, public suffixes (Go `golang.org/x/net/publicsuffix` if already in go.mod, else a short built-in list of common suffixes plus "must have at least two labels below a public suffix"), and punycode that fails IDNA validation.
3. **TLS ask endpoint:** `GET /internal/tls-ask?domain=` served on a separate listener bound to `SITES_ASK_ADDR` (default `127.0.0.1:4199`), never routed publicly; answers from one indexed query (verified, routed, site published, not taken down, Pro). Limits: each site may add at most 5 hostnames per 30 days; platform-wide at most 50 newly verified hostnames per hour (further verifications wait). `SITES_CUSTOM_DOMAINS=1` is the only feature gate.
4. **Edge config** in `docs/deploy.md`: one Caddy instance with persistent storage; :80 redirects to https except ACME; wildcard block for `*.<SITES_DOMAIN>` (DNS challenge) and an on-demand catch-all; both proxy everything to the web app, which forwards `/api/sites/*` paths to the API as today; unknown hosts get 404 from the web proxy. IPv4 and IPv6 records documented.
5. **Cookies:** app session cookies stay host-only (verify, fix if not); the proxy strips `Cookie` from every site request and `Set-Cookie` from every site response; tests cover apex/www and sibling cases.
6. **Redirects** only for GET/HEAD navigations to non-canonical hosts of the same site, built from the stored canonical hostname plus a normalised known page path (unknown paths go to `/`), query dropped. Image, form and beacon endpoints never redirect.
7. **Embeds:** stored as `{provider, id}` (YouTube video id, Google Forms id, Calendly path, or Maps `query` text). Maps URL = `https://www.google.com/maps?q=<url-encoded query>&output=embed`. Rendered as a local placeholder button (provider name, "Load video/map/form/calendar"), no thumbnail, no preconnect; the iframe is inserted only on click; without JS the placeholder is a link to the provider page. Proxy CSP on site pages adds `frame-src` for exactly the four provider origins; iframes get `sandbox="allow-scripts allow-same-origin allow-forms allow-popups"` (no top navigation) and `referrerpolicy="no-referrer"`. e2e asserts no request to a provider before the click.
8. **Analytics:** `POST /api/sites/hit/{site_id}` accepts `text/plain` bodies ≤ 512 bytes with `{path, ref_host}` only; per-IP-hash 120/hour and global 20,000/minute limits checked before any write; referrer stored as host only, ≤ 100 chars, top 50 per site-day enforced with one upsert under a unique key and a count check; counters kept 13 months; on storage errors the beacon is dropped silently. The UI says "visits" and notes that reloads and direct visits count.
9. **Export is "Download your site's content":** a zip of `site.json` (every page's data, theme, SEO, the published version id) and the images referenced by that version, built in Go from one locked published version, at most 40 images and 20 MB, one export per site at a time, direct download. No HTML rendering, no internal HTTP fetch.
10. **Pages:** slugs `[a-z0-9-]{1,40}`, unique per site, reserved: `api`, `internal`, `_next`, `robots.txt`, `sitemap.xml`, `favicon.ico`, `assets`, `img`, `published`. Shared blocks (Header, Footer, WhatsAppButton) live on Home only and render on every page; at most one of each. Removing or renaming a page rewrites `#`/`/slug` links pointing at it to `/`. Plan limits are enforced on every write, restore and publish; after a Pro lapse extra pages stay editable but publish refuses until the count fits. Validation covers every page with image ownership checked per site and workspace.
11. **`set_content`** runs the same checks as every other write (schema, URL rule, image ownership, changed-prop mask rule, Testimonials verbatim rule); new block ids are generated by the server; shared blocks can only be changed on Home; it requires `expected_version_id`, the locked draft and no active turn. A conflict returns the existing 409; the editor offers "Reload" only (no blind overwrite).
12. **Version growth:** editor saves are coalesced on the server: a save by the same person within 5 minutes of their previous editor save, when that version is still the draft tip, not published and not referenced by a turn, replaces that version's data instead of inserting a new one. History keeps every published version plus the newest 200; a daily sweep prunes older unreferenced versions. The UI says "History keeps your last 200 changes". Old documents without `pages` stay valid; a missing `variant` means the current default layout.
13. **Stock photos removed** from schema, commands, UI and tests (decision 16.5).
14. **Puck spike first:** package B starts with a short spike rendering our catalog in Puck 0.24.2 inside the builder (iframe canvas, our blocks, a text field, an array field, a custom image field) and records the confirmed API in this doc before building the editor. Install with an override that removes `@puckeditor/cli` (a CLI not imported by the runtime; its only published versions are canary builds).
15. **Work split (parallel):** Step 0 (one commit) creates the migration 1300, the catalog changes in both copies, per-feature Go files with empty registration functions (`pages.go`, `editor.go`, `domains.go`, `stats.go`, `export.go`) wired once into `Commands()`, route stubs in `server.go` calling handlers in `server/sitesphase2.go`, and the TS types in `lib/sites.ts`. Then in parallel: A1 Codex pages+validation+loop+set_content+versions; A2 Codex domains+ask listener+sweep; A3 Codex stats+export; B1 Sonnet Puck editor+variants+Text/Embed blocks+history panel (exports components); B2 Sonnet proxy routing+domain sheet+page switcher+stats card+export button+legal-page add flow, wiring B1's components into `builder.tsx`, e2e; C Tabnine data (already running).

### Puck spike results (17.14, confirmed 2026-11-24)

Installed `@puckeditor/core@0.24.2` (exact) in `apps/web`, with root `pnpm.overrides {"@puckeditor/cli": "-"}`. The lockfile has no `@puckeditor/cli` package and no canary versions (the only mention is the override line). Only warning: `@tiptap/html@3.35.0` (Puck's) has unmet peers `@tiptap/core`/`@tiptap/pm` 3.35.0 against our 3.33.1; harmless so far, but bump our tiptap to 3.35.x when convenient.

A throwaway client route rendered our `BLOCKS.Hero` and `BLOCKS.Services` inside `<Puck>` on Next 16.4.6 + React 19.3.1: compiles, SSR 200, no console errors. Confirmed in headless Chromium:

- Imports: `import { Puck, type Config, FieldLabel } from '@puckeditor/core'` and `import '@puckeditor/core/no-external.css'` (no CDN font).
- Composition: `<Puck config data onChange>` with children `<Puck.Components/> <Puck.Preview/> <Puck.Fields/>` replaces the default header and layout. Also available: `Puck.Outline`, `usePuck()`.
- Iframe canvas is on by default; our Tailwind and `globals.css` reached it with no config (`syncHostStyles`): an `h1` computed to 56px inside the frame. The site theme variables (`--site-*`) must still be set on a wrapper inside the render (our `Renderer` does via `themeStyle`), because the iframe body is not our page.
- `config.components.X = { fields, defaultProps, render }`. `render` props are the field props plus `id` and `puck` (`isEditing`, `dragRef`, `metadata`); strip `puck`/`id` before spreading into our blocks, or the block receives extra props.
- Fields: `text`, `textarea`, `array` (`arrayFields`, `getItemSummary`, `defaultItemProps`, `min`, `max`; reorder is built in), `custom`. Custom render signature: `render: ({ name, value, onChange, field, id }) => ReactNode`, wrapped in `<FieldLabel label=...>`; `onChange(value)` sets it. There is no built-in image or colour field.
- `data` is the initial value only; Puck owns state. Remount with a new `key` to reset (version restore, conflict reload).
- `onChange(data)` shape: `{ root: { props: {} }, content: [{ type, props: { id, ...fieldProps } }], zones: {} }`. The block id lives in `props.id`, so mapping to our `{id, type, props}` blocks is `{type, id: props.id, props: rest}`. Edits fire `onChange` immediately (we debounce).
- Also in the types (not exercised): `permissions` on `<Puck>` (`Partial<Permissions>`: drag, delete, duplicate, insert, edit), component-level `permissions`, `resolveData`, `resolveFields`, `resolvePermissions`, `overrides` (header, fields, fieldLabel, fieldTypes, drawer, iframe, preview ...).
- Gotchas: Puck prints an "AI agent detected" console line in dev (harmless); dnd-kit drag from the component list does not work with synthetic Playwright `dragTo`, so e2e should seed blocks through data or our own add button rather than dragging; open issues #1884 (children remount once after iframe mount) and #1871 (array item typing re-syncs per keystroke) are cosmetic here.
- Blockers: none. Spike route deleted.
