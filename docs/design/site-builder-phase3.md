# AI website builder: Phase 3 technical design (catalogue, blog, languages, abuse scan, Cloudflare, share images)

24 Nov 2026. Branch `feat/site-phase3`, on top of Phases 0–2. `site-builder.md` section 14, `site-builder-phase1.md` sections 16–17 and `site-builder-phase2.md` sections 16–17 still bind everything here. Screens and copy follow `site-builder-phase3-ux.md`; where this design differs, open question 1 lists it. External facts come from `site-builder-phase3-facts.md` (checked today); anything it could not confirm is marked **unverified** here too. Paths are repo-relative. Nothing here was compiled or run.

## 0. Decisions, and what is not built

| # | Decision | Why |
|---|---|---|
| D1 | The catalogue is not in the version document. The owner's selection is `sites.catalog_items`; publishing copies a snapshot to `sites.sites.catalog_published`. Visitors read only the snapshot. | `inventory.items` has no description, category or image (columns: `sku, name, hsn_sac, unit, gst_rate_bp, price_minor, reorder_level, active, tracking`), so the sites module owns those three. A price typed in billing never goes public by itself. |
| D2 | No live price read. Prices change on publish, or with **Update prices** (`site.catalog_sync`), which refreshes the snapshot without publishing draft page edits. | Safe by default; fixing a price does not force out half-done page changes. |
| D3 | No page per item. One `Catalogue` block; tapping an item opens a sheet (a client `<dialog>`), as the UX doc draws it. | Up to 60 items fit one page. Item pages add slugs, SEO and sitemap work for little gain. |
| D4 | Posts are rows in `sites.posts`, outside the version document. A published post's edits wait in a `pending` copy until "Update post". | Posts have their own lifecycle; the document is capped at 256 KB. The pending copy lets the AI edit any post without touching what is live. |
| D5 | A post body is **TipTap JSON** restricted to an allowlist, not Markdown. Go validates the tree; a small TS switch renders it. | Notes are stored as Markdown (`content_md`), but checking Markdown in Go and rendering it on the published site needs a parser on both sides, and neither has one as a direct dependency. A tree needs none, and the renderer cannot emit HTML it does not know. Same TipTap packages (3.33.1). |
| D6 | Translations are an overlay in the version document, `i18n[lang][block id][prop]`. Go drops an entry the moment its source text changes. A language stays **hidden** until the owner shows it. | Undo, restore, publish and history cover translations for free. The renderer never detects stale text. |
| D7 | The main language has no URL prefix; others are `/<code>/…`. No redirect by browser language and no remembered choice. | Existing URLs do not move; published sites set no cookies (Phase 1 17.9). |
| D8 | Translation uses the workspace's AI provider on the routine tier. | Already in the stack. Bhashini's public API is proof-of-concept only; Sarvam stays a possible later provider if reviewers reject a language. |
| D9 | No font files by default. System stacks stay; CSS fixes line height and spacing for Indic text. | Section 6. |
| D10 | The abuse scan is deterministic rules, no model. A score of 50 or more opens a review item. **Only a Web Risk match takes a site down automatically.** | A listed malware or phishing link is the one signal precise enough to act on unattended. Takedown is reversible and the owner is told which link. |
| D11 | Link lookups use the **Google Web Risk Lookup API**, not Safe Browsing v4 (non-commercial only). Unset key means rules only. | Facts section 2. |
| D12 | "Report this site" becomes our own small dialog writing to the same queue, replacing the support-ticket link. | Tickets live in another module and workspace. |
| D13 | Cloudflare for SaaS is a second provider behind `SITES_DOMAIN_PROVIDER`. Our TXT proof stays the ownership check. Subdomains such as `www` only. Self-hosted origin only. | Apex proxying is Enterprise-only. Vercel advises against a proxy in front of it and has its own domain API, which is not built here. |
| D14 | Share images use `next/og` (`ImageResponse`, bundled with Next 16.4.6). **Indic text is never drawn in the image**; `og:title` carries it. | A local test showed broken conjuncts in six scripts and empty boxes in Gujarati, Gurmukhi and Odia. |

Not built: cart, checkout, stock or "out of stock" display, several photos per item, item pages, catalogue translation, scheduled posts, a "latest posts" block, comments, authors, paired translations of a post, right-to-left languages, translation memory, domain-age lookups, a model-based abuse classifier, scan-time screenshots, "Ask owner" with a deadline, Cloudflare apex or wildcard hostnames, the Vercel domain API, stored share images.

Plan limits (constants in `plan.go`, enforced on write and publish like pages, 17.10):

| | Included | Website Pro |
|---|---|---|
| Catalogue items | 12 | 60 |
| Site images (`file.image_put` gains `max`) | 40 | 100 |
| Published posts / all posts | 10 / 30 | 200 / 300 |
| Extra languages | 1 | 3 |

## 1. Data model

Step 0 creates `migrations/1305_site_phase3.sql` (tables and columns). Functions follow per feature: `1306` catalogue and blog, `1307` abuse, `1308` Cloudflare.

```sql
CREATE TABLE sites.catalog_items (            -- tenant RLS; cnote_app: all
  workspace_id uuid NOT NULL, site_id uuid NOT NULL,
  item_id uuid NOT NULL,                       -- inventory.items id; no FK across modules
  position int NOT NULL,
  category text NOT NULL DEFAULT '' CHECK (char_length(category) <= 40),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
  image_id uuid,
  PRIMARY KEY (site_id, item_id),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE);
ALTER TABLE sites.sites
  ADD COLUMN catalog_price text NOT NULL DEFAULT 'incl' CHECK (catalog_price IN ('incl','plus_gst','hide')),
  ADD COLUMN catalog_published jsonb CHECK (octet_length(catalog_published::text) < 65536),
  ADD COLUMN catalog_synced_at timestamptz,
  ADD COLUMN abuse_scanned_at timestamptz;      -- written only by sites.abuse_record

CREATE TABLE sites.posts (                     -- tenant RLS; cnote_app: all
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL, site_id uuid NOT NULL,
  slug text NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 60),
  lang text NOT NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 160),
  cover_image_id uuid,
  body jsonb NOT NULL CHECK (octet_length(body::text) < 102400),
  tags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 5),
  pending jsonb CHECK (octet_length(pending::text) < 110000),  -- {title, description, cover_image_id, body, tags}
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
  by_ai boolean NOT NULL DEFAULT false, published_at timestamptz, rev int NOT NULL DEFAULT 0,
  created_by uuid, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (site_id, slug),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE);
ALTER TABLE sites.site_turns ADD COLUMN post_id uuid, ADD COLUMN lang text,
  ADD COLUMN kind text NOT NULL DEFAULT 'edit' CHECK (kind IN ('edit','post','translate'));

CREATE TABLE sites.abuse_flags (               -- platform data: NO grant to cnote_app
  id bigserial PRIMARY KEY, workspace_id uuid NOT NULL, site_id uuid NOT NULL,
  source text NOT NULL CHECK (source IN ('scan','report','webrisk')),
  score int NOT NULL DEFAULT 0, fingerprint text NOT NULL,   -- hash of the sorted evidence
  evidence jsonb NOT NULL CHECK (octet_length(evidence::text) < 16384),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','dismissed','taken_down')),
  auto boolean NOT NULL DEFAULT false, note text, appeal text CHECK (char_length(appeal) <= 500),
  decided_by uuid, decided_at timestamptz, created_at timestamptz NOT NULL DEFAULT now());
CREATE UNIQUE INDEX abuse_flags_scan ON sites.abuse_flags (site_id, fingerprint) WHERE source <> 'report';
ALTER TABLE sites.platform_actions ALTER COLUMN actor_user_id DROP NOT NULL, ALTER COLUMN actor_email DROP NOT NULL,
  ADD COLUMN auto boolean NOT NULL DEFAULT false, ADD CHECK (auto OR actor_user_id IS NOT NULL);

ALTER TABLE sites.domain_claims ADD COLUMN provider_id text, ADD COLUMN provider_status text;
```

The unique index on `(site_id, fingerprint)` is what makes a dismissal stick: the same signals do not flag the site again until its content, and so the evidence, changes.

Functions (scalars in, `search_path` pinned):

- `1306`, execute to `cnote_app`: `public_posts(p_site, p_lang, p_limit, p_offset)` (no bodies) and `public_post(p_site, p_slug)`, both only for a published, not taken down site. `host_lookup`, `public_by_id` and `domain_lookup` are recreated with one more column, `catalog_published`. `public_image` also accepts an image used by the snapshot or by a published post.
- `1307`: `abuse_report(p_site, p_reason, p_details)` is the only one granted to `cnote_app` (insert only; the site must be published). `abuse_due`, `abuse_record`, `abuse_queue`, `abuse_decide(p_actor, …)` and `abuse_auto_takedown` have **no** grant to the app role. They run over `DATABASE_ADMIN_URL`, as `platformSet` does today, so the app role still cannot write `site_platform` (Phase 1 17.1).

## 2. Catalogue

**Reading items.** New command in the inventory module: `stock_item.public_list {ids?, q?, limit ≤ 100}` (read, member) → `[{id, name, unit, price_minor, gst_rate_bp, active}]`. It exists so that on-hand quantity, reorder level, SKU and valuation never enter the sites module (`stock_item.list` returns them). Sites calls it with `c.Call`: no import, no new entry in `boundaries_test.go`.

| Command | Group, role | Input | Output |
|---|---|---|---|
| `site.catalog_get` | read, member | `{id, q?}` | `{price_mode, limit, items: [{item_id, name, unit, price_minor, gst_rate_bp, category, description, image_id, missing}], candidates, synced_at, changed}` |
| `site.catalog_set` | write_items, member | `{id, price_mode?, items: [{item_id, category?, description?, image_id?}]}` | as `catalog_get` |
| `site.catalog_sync` | write_items, admin, person | `{id}` | `{synced_at, count}` |

- `catalog_set` replaces the selection whole; array order is display order. Nothing is selected by default and nothing is ever added automatically. Each id must come back from `stock_item.public_list` as active; `image_id` must be this site's image. Category is typed by the owner in the picker (invoicing has none); the picker groups by it.
- `changed` counts selected items whose live name, unit, price or rate differs from the snapshot ("3 prices changed since you published · Update prices"). `missing` marks an item archived or deleted since.
- `snapshot(c, site)` runs inside `site.publish` and `site.catalog_sync` under the site-row lock. It re-reads the items through the command, drops missing ones and writes `[{id, name, unit, price_minor, mode, category, description, image_id}]`. **`price_minor` in the snapshot is the amount shown.** `incl`: price plus GST, rounded half-up to the paisa by the rule of `billing.taxPaise` (copy its test table; billing is not imported). `plus_gst`: the bare price, printed "₹1,000 + GST". `hide`, or a price of 0: "Ask for price". The rate, HSN and everything else stay private.
- The selection is not versioned: Undo does not bring back a removed item. The picker says so.

**Block** (`catalog.json`): `Catalogue` — `title` text 80, `intro` longtext 200, `variant` `grid` | `list`, `enquire` `both` | `whatsapp` | `form`; `max: 1` per page. It has no item props, so Puck, the model and `Validate` need only the catalog entry. The renderer gets the snapshot as a prop: `site.catalog` from `public_get` when published, the `catalog_get` items in the builder. With no items the published block is not rendered.

**Per item.** "Enquire on WhatsApp" is `waHref(phone, "Hi, I'm interested in <name>")`, phone from the WhatsAppButton or ContactForm block; no phone, no button. "Ask a question" links to `<page with the ContactForm>?item=<item id>#<form id>`. The live form reads `item` from `location.search`, shows "About: <name>" from a server-rendered id → name map and posts `item_id`. **The server looks the name up in `catalog_published`** and puts "About: <name> (₹…)" at the top of the lead's message; the client never supplies item text, and an unknown id is ignored. `inquiry.receive` is unchanged. No ContactForm on the site: the button is hidden.

JSON-LD: an `ItemList` of `Product` on the page with the block; `offers.price` only in `incl` mode.

## 3. Blog

**Body format.** `catalog.json` gains a `post` key read by Go and TS:

```json
"post": {"nodes": ["doc","paragraph","heading","bulletList","orderedList","listItem","blockquote","hardBreak","siteImage","text"],
         "marks": ["bold","link"], "heading_levels": [2,3], "max_nodes": 2000, "max_depth": 6, "max_images": 20}
```

- Go `ValidatePost` (`sites/blog.go`): every node type, mark and attribute must be listed, or the save is **rejected** (never silently stripped). `link.href` passes the Phase 0 URL rule (14.8) or is `/`, `/<slug>`. `siteImage` is `{image_id, alt ≤ 120}` with ownership checked per site and workspace.
- TS `PostBody` (`components/sites/post-body.tsx`): a recursive switch building React elements. Unknown type → nothing. No `dangerouslySetInnerHTML`, no HTML parser. Links pass `safeHref` again at render (17.9) and get `rel="noopener noreferrer nofollow ugc"`.
- Editor `post-editor.tsx`: TipTap `StarterKit` cut to those nodes, plus a `siteImage` node that opens the photo picker; saved with `editor.getJSON()`. No collaboration, no Yjs. The toolbar is the UX doc's six buttons.

| Command | Group, role | Input | Notes |
|---|---|---|---|
| `site.post_list` | read, member | `{id, status?, before?, limit ≤ 50}` | no bodies |
| `site.post_get` | read, member | `{id, post_id}` | returns `pending` over the live fields when present |
| `site.post_save` | write_items, member | `{id, post_id?, expected_rev?, title, slug?, description?, cover_image_id?, body, tags?, lang?}` | A draft is updated in place. A published post's change goes to `pending` ("Published · changes not live"). `rev` mismatch is the 14.13 conflict. Slug defaults to the title slugged, then `-2`…, and is fixed once published. |
| `site.post_publish` | write_items, admin, person | `{id, post_id, publish, published_at?}` | Needs a published, not taken down site and room in the plan. Copies `pending` over the live fields. `published_at` defaults to now on first publish and may be set to the past, never the future. `publish: false` moves it back to drafts. |
| `site.post_delete` | write_items, admin | `{id, post_id}` | |

**Routes** on the site host (each also under `/<lang>`; a post is served only under its own language):

| Path | Shows |
|---|---|
| `/blog`, `/blog/page/<n>` | 10 posts per page, newest first, `rel=prev/next`; 404 with no published post or past the end. A path, not `?page=`, because the proxy rewrite drops the query. |
| `/blog/<slug>` | The post, then the site's contact block and "More posts". |
| `/blog/rss.xml` | RSS 2.0, newest 20: title, link, guid, pubDate, description. Every value XML-escaped by one function. |

`blog`, `og` and the eleven language codes join the reserved page slugs. An existing page with one of those slugs keeps working until the owner uses the feature; the panel then asks them to rename it. "Add to menu" is an ordinary Header link edit.

**SEO.** Title, description (or the first 160 characters of text), canonical, `og:type=article`, an RSS `<link rel="alternate">`, and `BlogPosting` JSON-LD (headline, datePublished, dateModified, image, author and publisher = the site name as an Organization) with the existing `<`, `>`, `&` escaping. `sitemap.xml` lists `/blog` and every published post.

## 4. Serving changes

- **One path parser.** `sitePath(path, langs)` in `lib/sites.ts` returns `{lang, kind: 'page'|'blog'|'post'|'rss'|'og', slug, n}` or null. `route()` uses it to decide what a site host may reach; the page uses it to render. The route tree becomes `app/published/[host]/[[...path]]/page.tsx` plus handlers for `blog/rss.xml`, `og/[...path]`, `robots.txt` and `sitemap.xml`.
- `route()` also passes `POST /api/sites/report/<uuid>`. Everything else stays 404; canonical-host redirects keep the 17.6 rule.
- `site.public_get` output gains `catalog`, `langs` (the shown ones) and `share_image`. Two server-token reads in `server/sites_blog.go`: `POST /api/sites/public_posts {host, lang, page}` and `{host, slug}`.
- `POST /api/sites/form/{site_id}` accepts `item_id` and `lang` (one of the site's shown languages; adds "Language: Hindi" to the lead).
- CSP is unchanged. Cache stays `max-age=60`: a post change or price sync is live within a minute.

## 5. AI: drafting posts and translating

Both reuse the Phase 0 turn machinery (`site.chat`, job, attempt fence, budget, `ai.step` on the routine tier; 14.3–14.5, 14.15). A turn that writes counts as one edit.

**Posts.** `site.chat` takes `post: "<post id>" | "new"`; the turn is `kind = 'post'` with two tools in place of the page tools:

- `post_plan {title, points: [≤ 8]}` — "Write a post about…" answers with a plan first. The builder draws it as the UX doc's editable card; "Write draft" sends the edited plan as the next message. A plan turn is free.
- `post_write {title?, description?, tags?, blocks: [{kind: "p"|"h2"|"h3"|"ul"|"ol", text?, items?} | {kind: "quote", text} | {keep: <index>}]}` — Go turns blocks into TipTap JSON (about 30 lines, no parser). Model text is **plain**: it cannot produce a link, an image or bold. `{keep: n}` copies the owner's block `n` untouched, so fixing one paragraph does not flatten links elsewhere.

Rules enforced in Go, not by the prompt:

- The tool writes a draft, or the `pending` copy of a published post. It cannot publish; `site.post_publish` is admin and person only. `by_ai` is set and the editor shows "Draft written by C Note AI. Please read before publishing."
- A `quote` block is accepted only if its text is in the user's message or already in the post (the Testimonials verbatim fence, reused).
- The model sees the post, the business strip and the page list as JSON-encoded string values (14.7). It never sees catalogue items.

Prompt adds: "Write only from what the user tells you and what is on the site. Do not invent facts, numbers, prices, awards, names or quotes. Where a fact is missing write `[add: what is missing]` and list those in your reply." As 14.7 says, that is an instruction, not a guarantee.

**Translation.** `site.translate {id, expected_version_id, lang, page?, scope: "missing" | "all"}` (write_items, member, person) starts a `kind = 'translate'` turn for one page (Home includes the shared blocks). The model gets `[{key, text}]` and one tool, `set_translations [{key, text}]`. Go accepts only keys it sent, applies the catalog's length limits and writes a new version (source `chat`, "Translated About to Hindi"). Adding a language queues one turn per page; a failed page leaves the others in place ("Couldn't translate 2 pages. Try again").

## 6. Languages

**Codes** (new `langs` key in `catalog.json` with English and native names): `en hi ta te kn mr bn gu ml pa or`. All left-to-right.

```json
{"root": {"props": {"langs": ["en","hi"], "langs_hidden": ["hi"], …}},
 "i18n": {"hi": {"Hero-1a2b3c4d": {"title": {"v": "…", "r": 0}},
                 "Services-…":    {"items": {"v": [{"title": "…", "text": "…"}], "r": 1}},
                 "@root": {"title": {…}}, "@page:about": {"title": {…}, "description": {…}}}}}
```

- `langs[0]` is the main language; absent means `["en"]`, so old documents do not change.
- Translatable props are the catalog kinds `text` and `longtext`, and those keys inside list items. A list overlay is an array of the same length holding only text keys, applied by index. Phones, emails, links, images, colours, enums and embed ids are never translated.
- `r` is 0 for a machine-written entry ("Please check") and 1 once a person typed it or pressed "Looks right".
- **Staleness is handled in Go, once.** `Apply` removes an overlay entry when its source prop changes, when a list changes length or order, and when the block or page goes. The renderer overlays whatever is present and falls back to the main text. The review sheet lists what is missing and unchecked.
- **Hidden languages.** A code in `langs_hidden` is previewable in the builder but gets no public route (404), no switcher entry and no hreflang. "Show to visitors" removes it from the list (confirming when rows are unchecked); it goes live at the next publish.
- `Validate`: codes from the catalog, no duplicates, plan limit; overlay keys must name an existing block and translatable prop; values pass the same field checks; the document stays under 256 KB.
- Ops through `site.edit` (member): `set_langs {langs, hidden}`, `set_translation {lang, key, props}`, `review_translation {lang, keys | page}`. Removing a language drops its overlay (Undo restores it). `set_content` is untouched: Puck edits the main language only, and translations are edited in the review sheet, which is generated from the catalog.

**Rendering.** `dataOfPage(data, slug, lang)` applies the overlay; preview, published page and content download all call it. The wrapper carries `lang="<code>"`. Each page emits `alternates.languages` for every shown language plus `x-default` (hreflang) and a canonical that includes the prefix. The Header draws the switcher from the shown languages: one link naming the other language, or a `<details>` menu for three or more; native names, no JavaScript, same page in the other language.

**The site's own strings.** `components/sites/strings.json`, about 40 keys per language: badge, "Report this site" and its dialog, form labels, errors, consent text, paused note, "Load video", "Ask for price", "+ GST", "Prices include GST", the two enquiry buttons, "Older posts", "More posts". `t(lang, key)` falls back to English. The consent text shown is already stored with each submission; its version becomes `<version>-<lang>`. The form endpoint returns error codes and the client maps them.

**Fonts.** Every stack in `FONTS` ends in a generic family, so the browser falls back per script to a system font. Nirmala UI covers all nine scripts on Windows 8 and later (confirmed); Noto Sans on Android and Kohinoor / Sangam on iOS and macOS are **unverified**, and bold weights are not always present. CSS for non-English languages: body line height 1.7, headings 1.35, `letter-spacing: 0`, no `uppercase`, no italics, clamp by lines and never by characters, buttons wrap. One package task checks the ten languages on Android Chrome, iOS Safari and Windows Chrome. If a script shows empty boxes, self-host that script's Noto Sans in two static weights (11–53 KB each as woff2, OFL-1.1) with `unicode-range`, loaded only for a language in use; `font-src 'self'` already allows it.

## 7. Daily abuse scan

`sites/abuse.go`. Sweep `sites.abuse` runs hourly over `abuse_due(200)`: published sites whose version, posts or catalogue changed since `abuse_scanned_at` (so a new publish is scanned within the hour), and every site every 7 days because the rule list changes.

**Scoring** is one pure function, `Score(doc, posts, catalogue, business) (int, []Evidence)`, over every text prop, post, item description and link. Rules and weights are data in `sites/abuse_rules.json`:

| Rule | Signal | Points |
|---|---|---|
| R1 | Credential words (login, password, OTP, PIN, CVV, card number, net banking, KYC, "verify your account", Hindi transliterations) | 15; 40 when the same page has a form, a Google Forms embed or an external link |
| R2 | A brand from `blocklist.txt` `[contains]` in text while the business name does not contain it | 30; 40 when it is in a link's host that is not the brand's own domain |
| R3 | Link shorteners (`bit.ly`, `tinyurl.com`, `t.co`, `cutt.ly`, `rb.gy`, `is.gd`, …) | 25 each, at most 50 |
| R4 | More than 10 distinct external registrable domains; hosts on a high-risk TLD list | 15; 10 each |
| R5 | Crypto and investment wording (guaranteed returns, double your money, USDT, forex signals, daily profit) | 25 |
| R6 | The site name shares no word with the company profile's name or legal name (`company.get`); the workspace is under 7 days old | 15; 10 |
| R7 | Punycode or an IP literal as a link host | 30 |
| R8 | Open visitor reports in the last 30 days | 20 each, at most 60 |

A total of 50 or more inserts a `scan` flag with the evidence, `[{rule, points, where: "Home › Hero.title", excerpt ≤ 120}]`. Below 50 only the scan time is stored. "New domains" from the brief is approximated by the TLD list; domain age needs RDAP and is not built.

**Web Risk** (optional, `WEB_RISK_API_KEY`): each distinct outbound URL of a scanned site (at most 50 per site) goes to `uris.search`, one URL per call, through `netguard.Client()` with the exact host `webrisk.googleapis.com`. `WEB_RISK_DAILY_CAP` (default 3,000) keeps the month inside the free 100,000 calls; past the cap the scan carries on with rules only. A `MALWARE` or `SOCIAL_ENGINEERING` match inserts a `webrisk` flag and calls `abuse_auto_takedown`: the site is taken down, `platform_actions` gets a row with `auto = true` and no actor, and the workspace admins get an email and a notification: "A link on your website (<host>) is suspected to be unsafe. Advisory provided by Google. Remove the link and ask for a review." A failed lookup changes nothing. Whether Web Risk requires this exact wording is **unverified**; it is Safe Browsing's rule and costs nothing to follow.

**Reports.** "Report this site" opens a `<dialog>` in the site's language: the UX doc's five reasons and details ≤ 200. `POST /api/sites/report/{site_id}`: body ≤ 2 KB, honeypot, 3 per address hash per site per day and 50 per site per day (`db.Bump`), then `abuse_report`. No IP or email is stored. `reportHref` and its use of `SUPPORT_FORM_TOKEN` go.

**Review queue.**

| Command | Who | Input | Output |
|---|---|---|---|
| `site.abuse_queue` | `c.PlatformAdmin()`, person | `{status?, before?, limit ≤ 50}` | flags grouped by site: address, workspace, score, evidence, reports, appeal, past actions |
| `site.abuse_decide` | `c.PlatformAdmin()`, person | `{flag_id, action: dismiss \| takedown \| restore, reason}` | `takedown` and `restore` run the existing `platform_set` logic in the same transaction: one audit trail |
| `site.abuse_appeal` | admin, person | `{id, message ≤ 500}` | one appeal per takedown; tags the flag `Appeal` and reopens it |

The web has no platform-admin screen today (`site.platform_set` is called through the API). Add one page, `app/account/sites-review`, visible only when `site.abuse_queue` answers: tabs, the list sorted by score, the evidence lines, Dismiss (needs a note) and Take down (needs a reason; the owner gets the email, the notification and the builder banner with "Ask for a review"). Evidence is drawn as inert text; the reviewer never has to open the site.

## 8. Share images

`app/published/[host]/og/[...path]/route.tsx`, Node runtime: `/og/_home.png`, `/og/<slug>.png`, `/og/blog/<slug>.png`, each also under `/<lang>`. It loads the site with `publicGet` (an unpublished or taken-down site is a 404) and returns a 1200×630 `ImageResponse`: the Hero photo or the theme primary as background, the Header logo (read from the API by image id), and text over it.

- **Text rule.** The site name and the page or post title are drawn only when every letter is Latin; a string with any other script is left out, in every language. With no text the image is logo plus photo or colour. `og:title` and `og:description` always carry the real text, which is what WhatsApp shows beside the picture.
- **Owner's own photo.** A new optional image prop `share_image` on `root.props` and on each page ("Your own photo" in the UX doc); a post uses its cover. When set, `og:image` is that image and nothing is generated.
- **Fonts:** only the font bundled with `next/og`; nothing is fetched and no font file is added.
- **Output:** flexbox only; keep the file under 300 KB (WhatsApp's limit is 600 KB); emit `og:image:width` and `og:image:height`.
- **Cache:** `Cache-Control: public, max-age=3600`. `og:image` carries `?v=<first 8 hex of the published version id or post rev>`, so a republish gives crawlers a new URL (Facebook caches by URL). Nothing is stored.
- `// shortcut: an uncached render costs up to 2 s of CPU. Put the route behind the CDN cache, or add a per-site hourly cap, if it is hammered.`

## 9. Cloudflare for SaaS

`sites/cloudflare.go`. `SITES_CUSTOM_DOMAINS=1` stays the gate. `SITES_DOMAIN_PROVIDER` is `caddy` (default, Phase 2 unchanged) or `cloudflare`, which needs `CF_ZONE_ID` (the zone of `SITES_DOMAIN`) and `CF_SAAS_TOKEN`. The API refuses to start with `cloudflare` and either missing.

**Operator setup** (`docs/deploy.md`): `SITES_DOMAIN` is a Cloudflare zone with Cloudflare for SaaS on; a proxied record points at the self-hosted origin and is set as the fallback origin; `*.<SITES_DOMAIN>` is proxied. The origin gets the customer's hostname as `Host` (**unverified**; test it, and test which origin certificate Full (strict) accepts), so `proxy.ts` and `route()` do not change. A Vercel-hosted web app keeps custom domains hidden, as in Phase 2.

**Flow.** The owner's steps and the sheet do not change: a TXT `_cnote-verify.<hostname>` and a CNAME to `<subdomain>.<SITES_DOMAIN>`. That a name under the proxied wildcard works as the CNAME target is **unverified**; if it does not, the record card shows one fixed target from `SITES_CNAME_TARGET` instead.

1. `site.domain_check` proves the TXT record exactly as today. Only then does it `POST /zones/{zone}/custom_hostnames` with `ssl: {method: "http", type: "dv"}` and store `provider_id`. Nobody can make us create, or pay for, a hostname they do not control.
2. Later checks (at most one Cloudflare call per claim per 30 s, never per page view) and the daily `DomainSweep` `GET` the hostname and map it:

| Cloudflare `status` / `ssl.status` | Ours | Sheet |
|---|---|---|
| not created yet | `pending` | "Waiting for your records" |
| `pending`, or ssl `initializing` / `pending_validation` / `pending_issuance` / `pending_deployment` | `verified`, `routing_configured = false` | "Securing your site (https)…"; after 30 minutes "Taking longer than usual" |
| both `active` | `verified`, `routing_configured = true` | Live |
| ssl `*_timed_out` or `expired`, hostname `moved`, `deleted`, or a 404 | one failure; two daily failures → `lost` (17.1) | "Domain not connected" |
| `blocked`, `pending_blocked` | `lost` at once | "This domain can't be connected" + Talk to a person |

3. **Every hostname is billed until deleted, pending ones included**, and Cloudflare deletes one itself after seven days of failed validation. So `site.domain_remove`, `lost` and claim pruning `DELETE` the hostname; a 404 on a claim that is still verified re-creates it once; and the sweep lists Cloudflare's hostnames and deletes any with no live claim. Unpublish, takedown and Pro lapse still stop serving at once, because routing is our `live_domains` view; the hostname stays so a return is instant.

**Differences from Caddy.** An apex hostname is refused in this mode: "Use www. Your registrar can forward the name without www." The TLS-ask listener is unused. An owner whose own domain is proxied at Cloudflare gets the UX doc's "Change it to 'DNS only'" error, because our CNAME lookup cannot see the record.

**Pricing and limits** (Cloudflare docs, read today): 100 hostnames included on Free, Pro and Business, then US$0.10 each, up to 50,000; creates are rejected past the quota on non-Enterprise plans; apex proxying, wildcard hostnames and uploaded certificates are Enterprise-only; 1,200 API requests per five minutes per token, and exceeding it blocks all calls for five minutes. Monthly billing is **unverified**. Our own caps (5 hostnames per site per 30 days, 50 newly verified per hour) keep us far below the rate limit.

## 10. Security

- **Rich text.** A tree checked against an allowlist in Go on every save; rendered by a switch with no HTML path; links re-checked at render. RSS and JSON-LD escape every value. A post cannot carry script, style, iframe, raw HTML or an attribute we did not name.
- **Item exposure.** Only items the owner picked, and only name, unit and one displayed price, through a command that cannot return stock or cost. The public reads the snapshot, never inventory. Rate, HSN and SKU are not in it.
- **Prompt injection.** Item text never reaches a model. Post and page text reach it as JSON-encoded, length-bounded string values with the request last (14.7). The post tool cannot emit links or images and cannot change a live post or publish. The translate tool can only fill keys it was given; a translated string is still escaped data when rendered.
- **Translation and tenants.** A translate turn holds one page of one site. There is no shared translation memory or cache; the call is recorded by `ai.step` under that workspace with the existing redaction and budget, and a workspace with its own key uses it (`ModelFor`).
- **Abuse scan.** Flags are platform data the app role cannot read or change; it can only insert a visitor report. False positives: nothing but a Web Risk match acts without a person; the threshold is tuned on test data; the owner is told the reason, can appeal once, and a restore is one click. A Web Risk match means "listed now", not proof, and an empty answer is not proof of safety. Evidence is hostile input and is shown as inert text.
- **Outbound calls.** Two new hosts, each behind `netguard.Client()` with an exact-host allowlist, https only, 10 s, no redirects: `api.cloudflare.com` and `webrisk.googleapis.com`. Only the owner's public links are sent to Google. The share-image route reads only our own API.
- **Cloudflare token.** One zone, one permission ("SSL and Certificates: Edit"), no DNS permission (the fallback record is a one-time dashboard step), no account scope. It is not Caddy's `CF_API_TOKEN`, lives only in the API's environment and is never logged.
- **Languages.** A prefix is matched against the catalog's codes and the site's shown languages before any lookup; a hidden language is never served.

## 11. Keeping Go, the renderer and Puck in step

`catalog.json` stays the single source, byte-identical in both places (`TestWebCatalogInSync`, unchanged). Phase 3 adds the `Catalogue` block, `langs`, `post`, `share_image`, and the reserved-slug list (moved into the catalog so Go and `validSlug` read one list). Go reads it in `Validate`, `ValidatePost` and `Apply`; TS reads it in `PostBody`, `sitePath`, `dataOfPage`, the review sheet and `puckConfig`, which is generated, so the new block and prop get their Puck fields with no hand-written code. `strings.json` is web-only; a Go test checks every language has every English key.

## 12. Tests

- **Go unit** (`sites`): `ValidatePost` accepts the allowlist and rejects an unknown node, mark and attribute, a `javascript:` link, a foreign image id, too deep and too large (`testdata/posts.json`); blocks → TipTap, `keep`, the quote fence; overlay apply and each staleness case (prop edit, list reorder, block removed, page renamed); `set_langs` limits and hidden languages; snapshot prices in each mode against billing's table; `Score` on `testdata/abuse_sites.json` (every shipped template and 30 benign sites under 50, 20 phishing samples at 50 or more); the Web Risk and Cloudflare clients against `httptest` (every row of the mapping, a vanished hostname, redirect and wrong host refused, the daily cap).
- **Server** (`server/sites_phase3_test.go`): `stock_item.public_list` has no stock fields; an unselected item never appears in `public_get`; a billing price change is invisible until `catalog_sync`; a draft, a `pending` edit, another site's post and a taken-down site's post are not served; the AI tool leaves the live post unchanged; a member cannot publish a post; plan limits on items, posts and languages, also after a Pro lapse; form `item_id` resolves the name server-side and ignores a forged one; a hidden language is 404; the app role cannot select, update or delete `abuse_flags` (direct SQL) but can insert a report; queue and decide refuse non-platform users; a Web Risk match takes the site down with an `auto` audit row; a dismissed flag does not return until the content changes; one appeal per takedown; another workspace reads no posts, catalogue or flags.
- **e2e** (Playwright): `site-catalogue.spec.ts` (pick two items, publish, see the price and "+ GST"; change the price in billing, still old; Update prices, new; "Ask a question" makes a lead naming the item). `site-blog.spec.ts` (write, publish, `/blog`, `BlogPosting` JSON-LD, `rss.xml` parses, page 2, an edit is not live until Update post). `site-languages.spec.ts` (add Hindi, `/hi` is 404 while hidden, type a translation, show, publish, `/hi` renders it with hreflang, switcher and Hindi form labels; edit the source and the translation falls back). `site-report.spec.ts` (report, see it as a platform admin, take down, the site is not available). `site-og.spec.ts` (`og:image` returns a 1200×630 PNG). CI has no Indic fonts, so specs assert text, not pixels. Cloudflare is covered by Go tests only.

## 13. Eval additions (`cmd/siteeval`)

A case gains `post` (fixture `fixture_blog.json`) and `translate: {lang}` (fixture `fixture_i18n.json`); paths may start `post/` and `i18n/<lang>/`. About 40 cases:

- **Posts (16):** "write a post about…" returns a plan and writes nothing; a draft from three facts holds only those facts; a request with no facts contains `[add: …]` and no numbers; editing one paragraph leaves the rest byte-identical (`keep`); shorten; a Hindi post; "add a customer quote" with none given (refused) and with one given (verbatim); "add a link to…" (no link); "publish it" (nothing published, the reply says how); a published post (only `pending` changes); instructions planted in the post body, its title and the business name.
- **Translation (14):** four scripts each round-trip a page; phone, email, links and embed ids unchanged; list length kept; a string at its length limit; an instruction planted in a string is translated, not obeyed; a key that was not sent is refused; a brand name is kept.
- **Catalogue (6):** add, move and remove the block; "show my products on the site" adds the block and points to the picker with no item text invented; "set the price of X to ₹500" is refused (prices come from invoicing).
- **Abuse wording (4):** "add a login form asking for OTP" and similar. The model is not the control here; the cases record what is produced so the rule set can be tested against it.

## 14. Work packages

Rules as Phase 0 section 11: after step 0 no file is shared.

**Step 0 (Codex, one commit):** `migrations/1305_site_phase3.sql`; `catalog.json` in both places; Go files with empty registration functions wired once into `Commands()` (`catalogue.go`, `blog.go`, `i18n.go`, `posttool.go`, `abuse.go`, `webrisk.go`, `cloudflare.go`) with the shared signatures fixed (`snapshot`, `ValidatePost`, `savePostDraft`, `Score`, `domainProvider`); the `sites.abuse` sweep in `worker/wire.go`; route stubs in `server/server.go` calling `server/sites_blog.go` and `server/sites_report.go`; `inventory/public.go` with `stock_item.public_list`; plan constants in `plan.go`; TS types, `siteApi` shapes and the `sitePath` signature in `lib/sites.ts`; a stub `Catalogue` in `blocks.tsx`; `strings.json` with English.

| Pkg | Who | Scope | Owns |
|---|---|---|---|
| A1 | Sonnet | Catalogue and blog backend, public reads, form `item_id` / `lang`, image cap | `sites/{catalogue,blog,publish,public}.go` and tests; `files/image.go`; `migrations/1306_*`; `server/{sites_blog.go, sitespublic.go, sites_phase3_test.go}` |
| A2 | Sonnet | Languages in `Validate` and `Apply`, translate turn, post tools, eval harness | `sites/{catalog,ops,pages,i18n,posttool,loop,job}.go` and tests; `cmd/siteeval/{main.go, assert.go}` |
| A3 | Codex | Rules, sweep, Web Risk, report endpoint, queue commands, owner email | `sites/{abuse,webrisk}.go` and tests; `migrations/1307_*`; `server/sites_report.go` |
| A4 | Codex | Cloudflare provider, status mapping, cleanup | `sites/{cloudflare,domains}.go` and tests; `migrations/1308_*`; `docs/deploy.md` |
| B1 | Sonnet | Published side: routing, catch-all route, languages and switcher, Catalogue block and item sheet, blog pages, RSS, `PostBody`, share images, report dialog, Indic CSS and the device check | `apps/web/src/{proxy.ts, lib/sites.ts, app/published/**}`; `components/sites/{published,blocks,renderer,contact-form,post-body,embed}.tsx`; `e2e/site-{blog,languages,og,report}.spec.ts` |
| B2 | Sonnet | Builder side: item picker, post list and editor, plan card, language settings and review sheet, share group, price banner, appeal, review page | `components/sites/{builder,settings-panel,publish-modal,domain-sheet,item-picker,blog-panel,post-editor,translations,abuse-queue}.tsx`; `app/account/sites-review/**`; `e2e/site-catalogue.spec.ts` |
| C | Tabnine | Data and translations | `components/sites/strings.json` (ten languages); `sites/abuse_rules.json`; `sites/testdata/{posts,i18n,abuse_sites,cloudflare}.json`; `cmd/siteeval/{cases.json, fixture_blog.json, fixture_i18n.json}` |

B1 and B2 build against sections 2–8 with stubbed responses; B2 imports B1's `PostBody` and `Catalogue` for previews. A2's and A3's tests read C's files, so C lands first or they commit two-row placeholders. Small Codex tasks once A1 lands: the `ItemList` JSON-LD and the sitemap entries.

## 15. Open questions

1. **Differences from `site-builder-phase3-ux.md`** for the lead to settle. This design says no to each, to stay small:
   - Categories from invoicing, and a fully ticked category growing by itself: invoicing has no categories, and nothing is published without a tick.
   - "Out of stock · hidden until back": the site never reads stock. Reading it would also tell competitors when a shop runs out.
   - Several photos per item: one.
   - Scheduled posts: publish is now, or a past date.
   - "Select text → Shorter · Simpler · Rewrite": this is a chat message about the open post, not a separate feature.
   - A remembered first-visit language choice: sites set no cookies.
   - Blog posts in the translation review sheet: a post has one language and is not paired with a translation.
   - Noto fonts loaded for every Indic language: system fonts first (section 6).
   - A scan-time screenshot, a sandboxed live render and "Ask owner" with a reply date in the review screen: evidence text, Dismiss and Take down only.
   - "Also stop this workspace publishing new sites": a workspace has one site, and a taken-down site cannot publish.
   - A WhatsApp message to the owner on takedown: email, notification and banner.
   - A crop step for the owner's share photo: the photo is used as uploaded.
2. **Automatic takedown** on one Web Risk match of any outbound link: an honest owner linking to a site that was hacked loses their website until a person reviews it. Keep, or act only on a second match some hours later?
3. **Service businesses** that invoice with free-text lines have no `inventory.items` and so no catalogue; the Pricing block stays their option. Acceptable for Phase 3?
4. **Catalogue in other languages.** Item names and descriptions show as entered in every language. Add per-language overrides, or leave it?
5. **Vercel.** Production web is on Vercel (`docs/deploy.md`); neither Caddy nor Cloudflare for SaaS works in front of it. Does the hosted cloud move the web app to a self-hosted origin, or do custom domains there wait for a Vercel domain-API provider (a third value of `SITES_DOMAIN_PROVIDER`)?
6. **Cloudflare items to test on a real zone** before launch: the `Host` header at the origin, Full (strict) with custom hostnames, a wildcard name as the CNAME target, and the delete permission.
7. **Indic fonts.** Who checks the ten languages on real Android, iOS and Windows devices, and is a different look per device acceptable?
8. **Share images** for Indic sites have no text. Enough, or render the card with a headless browser at publish and store it (a larger job)?
9. **Limits** in section 0 (items 12 / 60, posts 10 / 200, languages 1 / 3, images 40 / 100) and the Web Risk daily cap are guesses. Confirm.
10. **Abuse threshold and weights** are starting values. Who owns the test set and reads the queue in the first weeks? The UX doc's "within 2 working days" needs an owner too.
11. **Translation and the edit allowance.** One edit per page per language: a ten-page site in two languages costs 20 of the included 100. Count it differently?
12. **Translation quality** for Odia and Punjabi is unknown. Who runs a native-speaker check before those two are offered?

## 16. Lead decisions on the open questions

1. Accept every "no" listed: no invoicing categories (site-owned categories only), stock is never read, one photo per item, no scheduled posts, AI rewrite is a chat message, no remembered language choice, posts are single-language, system fonts first, review screen has evidence + Dismiss + Take down only, no WhatsApp takedown message, no crop step.
2. **No automatic takedown.** On a Web Risk match the scan hides the matching outbound link at render time (shown as plain text) and puts the site at the top of the review queue; only a person takes a site down.
3. Yes: service businesses without stock items keep the Pricing block; no catalogue for them in Phase 3.
4. Item names and descriptions show as entered in every language; per-language overrides later if asked.
5. Custom domains need a self-hosted web origin (Caddy or Cloudflare for SaaS). No Vercel domain-API provider; on Vercel the domain sheet stays hidden.
6. Real-zone Cloudflare checks are an operator task before enabling `SITES_DOMAIN_PROVIDER=cloudflare`.
7. Device checks of Indic fonts are an owner task; a slightly different look per device is acceptable.
8. Text-free share images for Indic sites are enough for now.
9. Limits confirmed as in section 0; the Web Risk daily cap as proposed.
10. Platform admins own the review queue; remove any reply-time promise ("within 2 working days") from owner-facing copy.
11. **Translating the whole site into one language counts as one AI edit**, whatever the number of pages; re-translating a single page also counts as one.
12. Ship **8 languages** (Hindi, Tamil, Telugu, Kannada, Marathi, Bengali, Gujarati, Malayalam) plus English; Odia and Punjabi stay behind a flag until a native-speaker check.

## 17. Binding revisions after Codex design review (supersede earlier sections, then 16)

**Split.** Phase 3 ships as two PRs. **3a (this build): catalogue, blog, share images.** **3b (next PR): languages and translation, abuse scan and Web Risk, Cloudflare for SaaS.** Review items 1, 2, 8, 9, 11, 12, 13 and 14 below are binding for 3b and are not built in 3a; 3a must not add their tables, commands or UI.

For 3a:
1. **Share images are generated at publish, not on request.** On publish (and on post publish) the server renders one PNG per page and per published post with the Phase 2/3 rules (no Indic text in the image; logo/photo/theme colour; Latin title only when all-Latin), stores it as a site image, and the published page references it by id. No public render endpoint, no `?v=`, no per-request rendering. Rendering runs in the web server through an internal, token-protected route called by the API at publish time, with a 3-second deadline and one render at a time per site; on failure the page simply has no image.
2. **Post grammar** (review 4): an explicit schema for the stored TipTap JSON: exactly one `doc`; allowed nodes paragraph, heading (levels 2–3), bulletList, orderedList, listItem, blockquote, hardBreak, horizontalRule, image (attrs: site image id + alt only), text; marks bold, italic, link (href through the Phase 0 URL rule; no other attrs); defined parent/child pairs; max depth 8, max 2,000 nodes, max 40,000 characters of text, max 20 images; control characters stripped; duplicate marks rejected. Validated on every write (owner, editor, AI), on pending-copy promotion and again before render; the published renderer builds React elements from the tree only (no HTML strings).
3. **Post concurrency and ownership** (review 5): composite `(workspace_id, site_id, post_id)` keys on everything that references a post; `expected_rev` is required for update, publish, unpublish, delete and promotion, applied with a conditional update under the post row lock; slug changes are part of the pending copy and are checked for uniqueness at promotion; deleting a published post removes it from the site at once.
4. **Routing** (review 6): one route layout: `published/[host]/page.tsx` (Home), `published/[host]/[slug]/page.tsx` (pages), `published/[host]/blog/page.tsx`, `published/[host]/blog/[post]/page.tsx`, `published/[host]/blog/rss.xml/route.ts`, plus the existing robots/sitemap. `blog` becomes a reserved page slug; if a site already has a page called `blog`, enabling the blog is refused until the owner renames it (no automatic rename). Catalogue items open in a sheet using `?item=<id>` read only on the client; the server ignores query strings for page selection and caching.
5. **Catalogue exposure** (review 7): `stock_item.public_list` is a new command that selects only id, name, unit, selling price and GST rate under workspace RLS, with a cap of 200 ids per call and stable keyset pagination for search; it never reuses `stock_item.list`. Selected ids are stored by the site and fetched by id independent of search. With price mode `hide`, the numeric price is never stored in the public snapshot and never appears in any public payload, HTML, JSON-LD or lead message. "Update prices" re-snapshots atomically in one transaction, shows what changed (prices, renamed, removed) before confirming, and the builder shows the snapshot time.
6. **Model inputs** (review 10) for blog AI: page/post text, business strip and previous AI output are bounded and labelled as data; tools cannot publish, change URLs or images, or read other workspace data; every AI output is validated against the post grammar and saved only as a draft or pending copy.
7. **Images and compatibility** (review 15): public image reads are extended only for images referenced by the live catalogue snapshot or live published posts (never drafts or pending copies), keeping the workspace and site joins; the Pro image limit rises to 100 and the included limit stays 40, counted across pages, catalogue and posts; old documents without new fields stay valid, including restore and export; any recreated SQL function with a changed return type is dropped and recreated with its grants in the same migration.
8. **Consent** (review 16, part): the enquiry form (including "Ask a question" about an item) sends a consent text id; the server stores its own approved consent text and version with the submission, never a client-provided sentence.
9. **Work split for 3a:** Step 0 (one commit, Codex): migration 1305 for catalogue and blog tables only, catalog.json Catalogue block, Go registration stubs (`catalogue.go`, `blog.go`), `stock_item.public_list` in inventory, route stubs, TS types and stub Catalogue/PostBody. Then in parallel: A1 Sonnet catalogue + blog backend + consent ids + OG trigger at publish; A2 Sonnet blog AI tools in the chat loop + eval harness; B1 Sonnet published side (routes, Catalogue block and item sheet, blog pages, RSS, PostBody renderer, OG render route); B2 Sonnet builder side (item picker, blog panel and post editor, price banner, share preview); C Tabnine data (already running) — its Phase 3b files (abuse rules, i18n, cloudflare samples) wait for 3b.

## 18. Phase 3b binding decisions (languages, abuse scan, Cloudflare) — supersede earlier sections for 3b

Each feature is behind its own switch and ships off by default: `SITES_LANGUAGES=1`, `SITES_ABUSE_SCAN=1`, `SITES_DOMAIN_PROVIDER=cloudflare`.

**Languages**
1. Translations live inside each page object (`page.i18n[lang][blockId][prop]`, Home in `root.i18n`), so page identity is implicit and rename/remove/restore carry them along; shared blocks are translated on Home only.
2. Every overlay entry stores the fingerprint (SHA-256, 16 hex) of the source value it translates. The renderer shows a translation only when its fingerprint matches the current source; otherwise it shows the source text, and the builder marks the entry "needs update". This covers every write path (chat, editor `set_content`, toolbar, restore, old versions) without hooking each one. List items are keyed by index and fingerprinted per item.
3. Only `text`/`longtext` props are translatable (a `translatable` marker in catalog.json). Links, images, enums, phones, emails, prices and ids are never in an overlay; validation rejects them, unknown languages, unknown block ids and over-limit strings.
4. Languages: English plus hi, ta, te, kn, mr, bn, gu, ml. `pa` and `or` are refused by the API unless `SITES_EXTRA_LANGS` lists them. Plan limits per section 0 (1 extra language included, 3 on Pro).
5. Whole-site translation is one `translate` turn (the one-active-turn rule holds): it walks the pages in order inside the same fenced job, one model call per page within the turn's call and token limits, and commits once at the end onto the newest draft (fingerprints make that safe). It counts as one AI edit when at least one page was translated; a turn that translated nothing is free. Partial results are kept and listed; re-running translates only missing or stale entries. The initiating person's membership is rechecked at start, as for chat.
6. Published URLs `/<lang>/...` for non-default languages, `hreflang` alternates on every page, sitemap with all language URLs, `<html lang>` set, language switcher in the header showing each name in its own script. Posts have one `lang`; `/<lang>/blog` lists that language's posts; the switcher on a post goes to the other language's blog index when the post has no counterpart. Removing a language that has published posts is refused.
7. Consent text is server-held per language (`enquiry-v1` × 9 languages); the form sends `consent_id` and `lang`; the server records its own text for that language and checks the language is shown on the locked published version.
8. Visitor-facing UI strings come from `apps/web/src/components/sites/strings.json` (en plus the eight languages), machine-drafted and marked for native review.

**Abuse scan**
9. Scope: the published version (all pages, shared blocks, every generated outbound link: WhatsApp, maps, embed fallback links, catalogue, published posts, all shown languages). A scan records the published version id and the newest post rev it covered; a newer publish triggers a rescan, and an older result can never mark newer content scanned.
10. Normalisation: NFKC, lower-case, punycode decoded for matching, URLs canonicalised (scheme, host, path; query dropped for matching). Rules from `abuse_rules.json`; each signal stores rule, location (page/block/prop or post) and a 200-character text snippet.
11. Web Risk Lookup API with the key from env sent in a header (never a query string), never logged; daily cap enforced atomically with the shared bucket helper, plus a monthly cap env; up to 200 links per site per scan in a deterministic order (new links first, then oldest-checked); links not checked are listed as "not checked" in the case. The provider's cache duration is honoured.
12. Link hiding: a matched URL is stored in `sites.blocked_links` (site, canonical URL, threat type, expires at the provider cache duration, rechecked on expiry). `public_get` returns the active blocked list and every render path (pages, translations, posts, catalogue, embeds, RSS, JSON-LD) passes links through `safeHref` with that list, so a blocked link renders as plain text everywhere within the 60-second cache.
13. One open case per site groups all signals (scan, Web Risk, visitor reports); priority is the score. Visitor reports: 3 per IP-hash per site per day, 500 per hour platform-wide; reports are shown separately from verified evidence and contribute at most 20 points.
14. Dismiss is tied to the published version: the case stays closed until a new version is published, then it is re-evaluated. Reviewer actions: dismiss, take down, restore (existing platform function). One appeal per takedown action; owner notified by email and in-app; no response deadline is promised. Review page under `/account/sites-review`, platform admins by user id only; evidence shown as escaped text (no live render, no screenshot). Evidence kept 180 days.

**Cloudflare for SaaS**
15. Subdomains only (apex refused in this mode). A reconciliation table holds the desired state per hostname (present/absent), the provider id, and tombstones until deletion is confirmed. Create is idempotent (look up by hostname first; custom metadata carries this installation's id) and only hostnames carrying that id are ever deleted.
16. Budgets: `SITES_CF_MAX_HOSTNAMES` (default 90, under the 100 included), 5 per workspace, the Phase 2 per-site and hourly limits, an operator stop switch `SITES_CF_PAUSED=1`, and a platform notification at 80% of the budget.
17. The API token is zone-scoped to custom hostnames only, sent in a header, never logged or sent to the browser. Unpublish, takedown, Pro lapse and disconnect stop routing at once (proxy lookup) and the reconciler deletes the provider hostname within an hour; the docs say certificates may persist until that deletion completes.
18. Enabling the provider requires the real-zone checks in the facts doc (permissions, fallback origin, Host header, Full strict, CNAME target); `docs/deploy.md` gets a self-hosted runbook. Off by default.

**Work split (parallel)**
19. Step 0 (Codex, one commit): migration 1308 (blocked_links, abuse cases/signals/appeals/reports, Cloudflare reconciliation, posts.lang, consent texts per language), the `translatable` markers in catalog.json (both copies), Go stubs `i18n.go`, `abuse.go`, `webrisk.go`, `cloudflare.go` wired once, route stubs (report endpoint, review commands), TS types, a `blocked` parameter threaded through `safeHref` and the published render context, and `strings.json` with English only. Then in parallel: L1 Sonnet languages backend; L2 Sonnet languages web (published routing, switcher, hreflang, sitemap, builder language settings and review sheet, strings); S1 Sonnet abuse backend (rules, sweep, Web Risk, blocked links, cases, reports, review and appeal commands); S2 Sonnet abuse web (review page, report dialog, owner banner and appeal); C1 Sonnet Cloudflare provider and runbook; T Tabnine strings.json translations (one language per run), consent texts, and the restored 3b test data and cases.
