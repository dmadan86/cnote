# Docs parity with Confluence: gap analysis and build plan (25 Nov 2026)

Evidence for every row is in `docs/design/docs-parity-research.md` (Confluence catalogue with sources, C Note inventory with file references). Claims about Confluence marked UNVERIFIED there are not relied on here.

## 1. Summary

The owner's impression is right for what people see and touch every day, and wrong for the machinery underneath.

- **Already built, and solid:** real-time co-editing, inline comments with resolve and reopen, page restrictions with inheritance, page owner, version history with restore, templates bound to CRM and billing records, AI summary and cited Q&A, importers from Confluence and Notion. On page freshness (verify-by dates, stale flags, auto-archive) we are ahead: Confluence has no native review date.
- **Missing, and felt daily:** the editor has no panels, expand, status, date or table of contents; images cannot be uploaded; pages cannot be moved, deleted or restored from the UI; there are no labels, stars, true "recent" list, search filters, watch, comment notifications, version compare, export, or a template gallery beyond two entries.
- **Missing, and structural:** a private space cannot add members; wiki pages have no public link; mobile edits raw Markdown.

Three inventory findings changed the suggested Phase A:

| Suggested for Phase A | Finding | Result |
|---|---|---|
| Inline comments with resolve | Built and tested | Replaced by comment notifications |
| Page restrictions | Built and tested | Replaced by tree actions (move, delete, trash) |
| Page owner | Built | Only page status is added |

One constraint governs all editor work: **Markdown is the stored format and there are two converters** (the browser's TipTap Markdown and the collab gateway's `apps/collab/src/md.ts`). A block known to only one of them is flattened to plain text at the next snapshot. Every new block therefore costs a change in both, and the stored form must be chosen so that older clients and the mobile Markdown editor degrade readably.

## 2. Gap matrix

Value is for Indian SMB owners and their teams (5 to 50 people, mixed desktop and phone, few dedicated admins). Effort: S up to 1 day, M 2 to 4 days, L a week or more, for one builder. Phase letters refer to section 4.

### Editor

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Panels (info, note, success, warning, error) | Yes | Absent | High: SOPs and policies need "Important" and "Do not" boxes | M | Both converters | A |
| Expand | Yes | Absent | High: long SOPs and FAQs | M | Both converters | A |
| Status lozenge | Yes | Absent | High: status columns in tables are the commonest SMB tracker | S | Both converters | A |
| Date chip | Yes | Absent | Medium: due dates in notes and tables | S | Both converters | A |
| Table of contents | Yes | Absent | High: long policy and process pages | S | Both converters | A |
| Image upload, paste, drag-drop | Yes | Partial (URL only) | High: screenshots of bills, WhatsApp images, product photos | M | Existing `files` module | A |
| Code block language picker and copy | Yes | Partial | Low for most SMBs, medium for IT firms; cheap | S | None | A |
| Slash menu covering the above, grouped | Yes | Partial (16 items) | High: it is how the blocks are found | S | The blocks | A |
| Image resize, caption, alignment | Yes | Absent | Medium | M | Image upload | B |
| File attachments on a page (PDF and others), attachments list | Yes | Backend only | High: price lists, agreements, GST certificates | M | Image upload package | B |
| Task with assignee and due date; "My tasks" | Yes | Absent | High: meeting notes that produce actions | M | Mentions (built); notifications | B |
| Layout columns (2 and 3) | Yes (2 to 5) | Absent | Medium: landing-style pages and comparisons | L | Both converters; HTML block storage | C |
| Highlight colour, sub/superscript | Yes | Absent | Low to medium | S | Both converters | C |
| Table cell colour, numbered column, sticky header | Yes | Absent | Medium: trackers kept in tables | M | HTML table storage (exists) | C |
| Decision block and decision list | Yes | Absent | Low: a panel covers it | S | Panels | Not planned |
| Page link picker (`[[` or `@page`) | Smart links | Absent | High: wikis live on links; today a URL is typed | S | None | B |
| Child pages block | Yes | Absent | Medium: section landing pages | S | Both converters | B |
| Emoji `:shortcode` | Yes | Partial | Low | S | None | C |
| Find and replace | Yes | Absent | Low | S | None | C |
| Embeds: YouTube, Google Docs/Sheets, Figma, generic link card | Yes | Absent | Medium: training videos and Google Sheets are common | M | CSP review; both converters | C |
| Embed our records in a page (table view, project item, invoice, whiteboard) | Jira and database embeds | Absent | High: this is where a suite beats a wiki | L | Tables 1b; permission checks per embed | D |
| Excerpt, include page, synced blocks | Yes | Absent | Low for SMB team sizes | L | Block identity in Markdown | E |
| Content by label list | Yes | Absent | Low to medium | S | Labels | C |
| Page properties and report | Yes | Absent | Low: Tables does this better | M | None | Left out (section 3) |
| Charts from a table | Yes | Absent | Low to medium | M | Chart library choice | E |
| Math | Marketplace | Absent | Low | S | None | Left out |
| Diagrams | Marketplace | Whiteboard (separate) | Low: whiteboard embed covers it | - | Whiteboard embed | D |

### Page tree and page chrome

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Move page (picker) | Yes | API only | High | S | None | A |
| Delete to trash, restore from trash | Yes | Delete API only; no UI, no restore | High: people need to remove things, safely | M | None | A |
| Duplicate page | Yes (with children) | Absent | Medium: reuse last month's page | S | None | A (single page) |
| Stable tree order | Manual order | Sorted by last edit, so pages jump | High: the tree is disorienting today | S | None | A (creation order) |
| Drag to reorder and move | Yes | Absent | Medium | M | Position column | B |
| Manual archive | Yes | Automatic only | Medium | S | None | B |
| Page status | Rough draft, In progress, Ready for review, custom, Verified | Absent (Verified exists as freshness) | High: "is this final?" is the first question about any shared doc | S | None | A |
| Title emoji | Yes | Absent | Medium: makes the tree scannable | S | None | A |
| Page width | Narrow, Wide, Max | Fixed narrow | Medium: wide tables are cramped | S | None | A |
| Cover image | Yes | Absent | Low | S | Image upload | C |
| Tree beyond 500 pages | No stated limit | Truncated at 500 | Medium, later: few SMBs reach it in year one | M | Lazy-loading tree | C |
| Folders | Yes | Absent | Low: a page with children does the job | - | - | Left out |
| Drafts and publish | Pages only; live docs have none | Absent (live-doc model) | Low: page status plus restrictions covers "not ready yet" | - | - | Left out |
| Whiteboards and tables in the same tree | Yes | Separate sections | Medium | M | Shell tree refactor | D |

### Spaces and permissions

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Add and remove space members, change role | Roles | Absent: only the creator is a member | High: private spaces (HR, Accounts, Directors) are unusable without it | M | None | B |
| Space rename, description, archive | Yes | Absent | Medium | S | None | B |
| Space home (overview page, pinned pages) | Yes | Flat page list | Medium | S | Stars/pins | B |
| Public link for a page | Yes (Standard+) | Absent for pages | High: share an SOP or brochure with a customer or vendor on WhatsApp | M | `publiclinks` module; read-only renderer | B |
| Request access | UNVERIFIED | Absent | Medium | S | Notifications | B |
| Guests limited to one space | Yes | Workspace guest role through page shares | Low: page shares cover it | - | - | Not planned |
| Share with a team or group | Yes | Absent | Low at SMB sizes | M | Groups | E |
| Custom space roles | Yes | Absent | Low | - | - | Left out |

### Collaboration and history

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Notify on comments and replies | Yes | Mentions only | High: comments go unseen today | S | Notification kind | A |
| Watch a page; notify watchers of edits | Yes | Absent | High | M | Notification kind | A |
| Version compare | Yes | Absent | High: "what did you change?" | S | `diff` (already a dependency) | A |
| Named versions / change note | Yes | Absent | Low | S | None | C |
| Comment edit and delete; reactions | Yes | Absent | Medium | S | None | B |
| Email for mentions and comments; daily digest | Yes | Absent | High: owners live in email and WhatsApp, not our bell | M | Mailer; preferences | B |
| Watch a space | Yes | Absent | Low | S | Watch | C |
| Page views and viewers | Standard+ byline, Premium detail | Absent | Medium: "has the team read the new policy?" | S | View log from Phase A | B |
| Read acknowledgement ("I have read this") | Marketplace | Absent | High for policies; a differentiator | M | View log | D |
| Page approval before a status of "Approved" | Native approvals (detail UNVERIFIED) | Approvals engine exists, not wired to pages | Medium | M | `approvals` module | D |

### Search and organisation

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Labels on pages, label filter | Yes | Absent | High | M | None | A |
| Search filters: space, owner, label, updated date, title only | CQL | Absent | High: search is Confluence's most-criticised area; a clear filter bar is an advantage | M | Labels | A |
| Starred pages | Yes | Absent | High | S | None | A |
| Recently viewed | Yes | Recently updated by anyone | High | S | View log | A |
| Query language for pages (CNQL over pages), saved searches | CQL | CNQL for items only | Low: filters are enough | M | - | E |

### Templates, import, export

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Template gallery with categories and preview | Yes | Two starters, plain list | High: a blank page is the main reason a wiki stays empty | M | None | A |
| Free-text template prompts ("Meeting date", "Attendees") | Variables: text, multi-line, list | Absent (record-bound variables only) | Medium | S | Gallery | B |
| Space pre-filled from a template | Yes | Absent | Medium: "HR handbook", "Sales playbook" starters | M | Gallery | C |
| Export page to PDF, Word, Markdown | PDF, Word | Absent | High: send a policy to an auditor, a proposal to a client | S (browser print and file download) | Print stylesheet | A |
| Branded PDF (letterhead, header, footer) | Per-space CSS | Absent | Medium | M | Server-side renderer | C |
| Export a space | Yes | Absent | Low now; needed for trust ("can I leave?") | M | Page export | C |
| Import Word (.docx) | Yes | Absent | High: SMB knowledge lives in Word files | M | A docx-to-HTML step feeding `importers/html.go` | B |
| Import a Markdown folder / Google Docs | Markdown no; Docs via .docx | Absent | Low: .docx covers Docs | S | Word import | C |

### AI, automation, mobile, admin

| Feature | Confluence | Ours | Value | Effort | Depends on | Phase |
|---|---|---|---|---|---|---|
| Inline AI on a selection: rewrite, shorten, fix grammar, change tone | Rovo | Absent (dialog-level draft only) | High | M | `ai` module | D |
| Translate a page or selection (Hindi, Tamil, Telugu, Marathi and others) | Rovo | Absent | High for Indian teams: staff often read in a regional language | S after inline AI | Inline AI | D |
| Definitions of terms | Rovo | Absent | Low | - | - | Not planned |
| Automation rules for pages | Yes | Project automations only | Low: the freshness sweep covers the main use | L | - | E |
| Mobile: rendered page view, tree, comments, notifications | Yes | Raw Markdown editor, flat list | High: owners read on the phone | L | A shared Markdown renderer | D |
| Mobile: rich editing | Yes | Plain Markdown | Medium | L | - | E |
| Mobile offline reading | Limited | Absent | Medium: patchy networks | M | Mobile renderer | E |
| Retention for trash (auto-purge after N days) | UNVERIFIED | Absent | Low | S | Trash | C |
| Classification labels, admin key, IP allowlist | Premium+ | Absent | Low | - | - | Left out |

## 3. What we will not build, and why

| Left out | Reason |
|---|---|
| Jira issue, Jira chart and roadmap macros | Our Projects module is the equivalent; the right feature is embedding our own items (Phase D) |
| Forge / Connect marketplace | A platform business of its own. The needs its top apps meet (diagrams, PDF export, approvals, review reminders) are met natively: whiteboard, export, approvals engine, freshness |
| Drafts and publish | We chose the live-doc model, as Confluence itself now offers. Page status plus restrictions answers "not ready yet" without a second copy of every page |
| Folders | A page with children is a folder that can also hold a summary |
| Page properties and report, Confluence databases | Tables is our structured-data product; a second mechanism in pages would compete with it |
| Excerpt include and synced blocks (for now) | Needs stable block identity, which Markdown storage does not have. High cost, low SMB value. Revisit in Phase E only if asked for |
| Custom space roles, guest-per-space licensing, classification levels, admin key, sandboxes, release tracks, data residency options | Enterprise administration. Three space roles are enough for our customers |
| Blogs, team calendars, Questions | Chat and Projects cover announcements and schedules |
| Legacy macros (gallery, user list, profile, network, popular labels, change history macro) | Low use even in Confluence; 46 legacy macros are being retired there |
| Math, standalone chart macro, Loom recording | Rare in SMB use. Loom and YouTube links are covered by embeds in Phase C |
| Confluence Server XML space import | Format ends on 1 Dec 2026; our storage-format HTML import already exists |

## 4. Phases

| Phase | Theme | Contents | Rough size |
|---|---|---|---|
| **A** | Feels complete day to day | Panels, expand, status, date, TOC, code language; image upload; page status, emoji, width; move, delete, trash, duplicate, stable order; labels and search filters; stars and recently viewed; watch and comment notifications; version compare; template gallery; export | 1 week, 8 parallel packages |
| **B** | Teams and sharing | Space members and settings; public page links; request access; email notifications and digest; tasks with assignee and due date plus "My tasks"; file attachments; page link picker; child pages block; drag reorder; manual archive; comment edit, delete, reactions; page views byline; Word import; template prompts | 2 weeks |
| **C** | Depth and polish | Layout columns; table cell colour, numbered column, sticky header; highlight; embeds (YouTube, Google, Figma, link cards); cover image; branded PDF and space export; space starters; content-by-label; lazy tree for large workspaces; trash auto-purge; find and replace | 2 weeks |
| **D** | Suite advantages | Embed table views, project items, invoices and whiteboards in pages; inline AI and translation; read acknowledgements; page approvals; mobile rendered reading, comments and notifications; one tree for pages, boards and tables | 3 weeks |
| **E** | On demand | Synced blocks, CNQL over pages, page automation rules, charts, groups, mobile rich editing and offline | Not scheduled |

Order within B to E can change with customer feedback; A does not depend on any of them.

## 5. Phase A in detail

### 5.1 Stored forms for new blocks

Each form is plain Markdown or an addition to the existing HTML subset, and reads acceptably where it is not understood (mobile editor, older tab, exported `.md`).

| Block | Editor node or mark | Stored in `content_md` | Seen by a client that does not know it |
|---|---|---|---|
| Panel | node `panel`, attr `kind` in info, note, success, warning, error; content `block+` | Blockquote whose first line is `[!INFO]`, `[!NOTE]`, `[!SUCCESS]`, `[!WARNING]` or `[!ERROR]` (GitHub alert syntax) | A quote starting with `[!INFO]` |
| Expand | node `expand`, attr `title`; content `block+` | `<details><summary>Title</summary>` on its own line, blank line, Markdown blocks, blank line, `</details>` | The tags as text around normal content |
| Status | mark `status`, attr `color` in grey, blue, green, yellow, red, purple | `<span data-status="green">DONE</span>` | The text `DONE` |
| Date | mark `date`, attr `datetime` (`YYYY-MM-DD`) | `<time datetime="2026-12-01">1 Dec 2026</time>` | The text `1 Dec 2026` |
| Table of contents | atom node `toc`, no attrs; rendered from the document's headings by a node view | A line containing only `[[toc]]` | The text `[[toc]]` |
| Uploaded image | existing `image` node | `![alt](/api/w/<ws>/files/<id>)` | A broken image on a client without a session, alt text otherwise |

Rules:

- Status and date are **marks**, not inline nodes. The gateway's inline model is text runs with one attribute per mark (`md.ts` `Marks`, `markAttrs`), so marks are a small addition while an inline node type would be a new concept there. `shortcut: a status or date is editable text carrying a mark, not an atomic chip; upgrade to inline nodes if users break chips by typing inside them.`
- Attribute values are validated against the fixed lists above in both converters, as colours are today (`html-inline.ts` `hex`). Anything else is dropped to plain text. No free-form attribute reaches stored Markdown.
- `safeSrc` in `formats.ts` and `md.ts` additionally accepts a same-origin path matching `^/api/w/[0-9a-f-]{36}/files/[0-9a-f-]{36}$`. Nothing else changes about image safety.
- Panels and expands may nest lists, tables and each other one level deep; the gateway parser recurses with the same block parser it uses for blockquotes.

### 5.2 Keeping existing documents valid

- **Schema changes are additive.** No existing node, mark or attribute changes meaning, so every stored Yjs document and every Markdown snapshot still loads. No data migration.
- **Existing Markdown that happens to match.** A blockquote that literally begins with `[!NOTE]` becomes a panel, and a line that is exactly `[[toc]]` becomes a table of contents. Both are vanishingly unlikely and harmless.
- **Round trip is the test.** For every new form: Markdown to Yjs to Markdown in the gateway, Markdown to TipTap to Markdown in the browser, and gateway output equal to browser output byte for byte. The existing contract tests (`apps/collab/test/md.test.ts`, `e2e/formats.spec.ts`) are extended, not replaced.
- **Old tabs during rollout.** A tab loaded before the release has the old schema. y-prosemirror removes nodes it has no type for, and that removal would sync to everyone. Guard: the web client sends `schema=2` when opening the collab socket (beside the existing `epoch` parameter); the gateway accepts older clients as **read-only** (it already distinguishes `canEdit`) and the page shows "Reload to keep editing". The same number is sent as `schema` on `note.update`, and the API rejects a lower one with 409 so an old solo-mode tab cannot overwrite panels with flattened text. Deploy order: gateway, API, then web.
- **Mobile.** The app edits raw Markdown, so it preserves the new forms untouched and shows them as text. Acceptable for Phase A; a rendered view is Phase D. The app must send `schema: 2` with `note.update` (one line) in the same release, or its saves are refused.

### 5.3 Data model: `migrations/1330_docs_parity.sql`

1326 to 1328 belong to an in-flight branch and 1329 is left free for it; confirm the number against `migrations/` at merge time.

```sql
ALTER TABLE core.notes
  ADD COLUMN status text CHECK (status IN ('draft','in_progress','in_review')),   -- NULL = no status; "Verified" stays in freshness
  ADD COLUMN icon text CHECK (char_length(icon) <= 16),                           -- one emoji
  ADD COLUMN wide boolean NOT NULL DEFAULT false;
CREATE INDEX notes_trash ON core.notes(workspace_id, deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE core.note_labels (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  label text NOT NULL CHECK (label = lower(label) AND label ~ '^[^\s,]{1,40}$'),
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, label)
);
CREATE INDEX note_labels_ws_label ON core.note_labels(workspace_id, label);

-- One row per person per page: star, watch and last view together, so "recent" and "starred" are one index scan.
CREATE TABLE core.note_user_state (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  starred_at timestamptz,
  watching boolean NOT NULL DEFAULT false,
  last_viewed_at timestamptz,
  view_count integer NOT NULL DEFAULT 0,
  PRIMARY KEY (note_id, user_id)
);
CREATE INDEX note_user_state_recent ON core.note_user_state(user_id, last_viewed_at DESC);
CREATE INDEX note_user_state_watch ON core.note_user_state(note_id) WHERE watching;

SELECT core.enable_tenant_rls('core.note_labels');
SELECT core.enable_tenant_rls('core.note_user_state');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.note_labels, core.note_user_state TO cnote_app;

-- notifications_kind_check gains 'comment' and 'page_updated' (drop and re-add, as 1310 does).
```

Notes:

- `note_user_state.view_count` and `last_viewed_at` are the whole of page analytics for now. The Phase B "viewed by" byline reads this table; no event stream is needed.
- Trash is the existing `deleted_at`. Restore clears it. Sub-pages of a deleted page are deleted with it in one statement (recursive on `parent_id`) and stamped with the same `deleted_at`, so restore brings back exactly that set.
- Built-in templates are Go constants, not rows, so existing workspaces get the gallery without a backfill and a later wording fix reaches everyone. `docs.templates` is unchanged.
- `files.maxPerNote` rises from 10 to 50 for pages; the 10 MB per-file limit and the type allow-list stay.

### 5.4 Commands

Role is the minimum workspace role; "page edit" and "page view" mean `core.note_access` on that page, checked as today through `access.AssertNoteAccess`. Guests act only on pages shared with them.

| Command | Group | Who | Behaviour |
|---|---|---|---|
| `note.update` (extended) | write_notes | page edit | Accepts `status` (or null), `icon`, `wide`, `schema`. Emits `note.updated` with the changed field names |
| `note.delete` (extended) | destructive | page edit; member | Also soft-deletes descendants with the same timestamp. Returns the count |
| `note.trash` | read | member | Deleted pages the caller could edit, newest first, with deleted-by and date |
| `note.undelete` | write_notes | page edit on the page; member | Clears `deleted_at` for the page and the descendants deleted with it; if the parent is gone, the page returns at the space root. Emits `note.undeleted` |
| `note.duplicate` | write_notes | page view on the source, write on the target space | Copies title (with " (copy)"), body, icon, width and labels to a new page beside the source. Not comments, versions, shares or restriction |
| `label.set` | write_notes | page edit | Replaces the page's labels (max 20). Emits `note.labels_changed` |
| `label.list` | read | member | Labels in the workspace with counts, limited to pages the caller can read; optional `prefix` for autocomplete |
| `note.star` | read | page view; guest allowed | `{note_id, starred}` |
| `note.watch` | read | page view; guest allowed | `{note_id, watching}` |
| `note.viewed` | read | page view; guest allowed | Upserts `last_viewed_at`, increments `view_count` at most once per 30 minutes per person |
| `note.mine` | read | any | `{list: "recent" \| "starred" \| "watching", limit}`; filters by current access, so a page later restricted disappears |
| `search.notes` (extended) | read | member | New optional filters `space_id`, `owner_id`, `label`, `updated_after`, `title_only`, `status`; `q` may be empty when a filter is given. Same permission predicate |
| `template.list` (extended) | read | member | Also returns built-ins with `builtin: true`, `category`, `description` |
| `template.render` (extended) | write_notes | member | Accepts a built-in id (`builtin:meeting-notes`) |

`note.star`, `note.watch` and `note.viewed` change only the caller's own row, which is why they sit in the read group; agents with read scope may call them harmlessly.

Notifications (all through `notify.Send`, never to the actor):

| Trigger | Recipients | Kind | Limit |
|---|---|---|---|
| `comment.add`, top level | Page owner and watchers | `comment` | One per comment |
| `comment.add`, reply | Thread author, earlier repliers, watchers | `comment` | One per comment |
| `note.updated` with content changed | Watchers | `page_updated` | One per person per page per day, using the existing `source` + `source_day` uniqueness |

People start watching a page automatically when they create it, become its owner or comment on it. Recipients who can no longer read the page are skipped at send time.

### 5.5 Web components

| Area | Change |
|---|---|
| Editor extensions | New `editor/blocks.ts`: `Panel`, `Expand`, `Toc` nodes and `Status`, `DateChip` marks, each with `parseMarkdown` and `renderMarkdown` beside the node as `formats.ts` does. No new dependency: TipTap's Details and Table-of-contents packages are not installed and these nodes are small |
| Slash menu | Groups (Basic, Lists, Callouts, Insert); new entries Info, Note, Success, Warning, Error panel, Expand, Status, Date, Table of contents, Image upload |
| Block controls | Panel: type switcher on a small floating bar. Status: colour and text popover. Date: native date input popover. Code block: language select and copy button |
| Images | `ImageDialog` gains an Upload tab; paste and drop of image files upload through `POST /api/w/{ws}/files` and insert the returned path. Progress and the 10 MB error are shown inline |
| Page header | Byline under the title: owner, status lozenge (click to change), freshness pill, "Updated". A "..." menu holds Move, Duplicate, Wide page, Export, Delete. Star and Watch are icon toggles. Emoji picker on the title reuses `EMOJI` from `inserts.tsx`. The header already wraps on narrow screens (`e2e/narrow.spec.ts`) and must keep doing so |
| Labels | Chips under the title with add and remove and autocomplete from `label.list`; a chip links to search filtered by that label |
| Tree | Emoji in place of the file icon; siblings ordered by creation time; row "..." menu with the same page actions; Starred and Recent sections above Spaces; a Trash link under each space |
| Move dialog | Tree picker limited to the same space (the API rule today), excluding the page's own subtree |
| Trash page | `/w/{ws}/trash`: list with Restore; reached from the sidebar |
| Search page | Filter bar: space, owner, label, updated (7, 30, 90 days), title only, status. Filters live in the URL |
| Versions | "Compare" on a version opens a wide read-only view with the version list on the right and a "Highlight changes" toggle, comparing with the previous version or with now. Uses `diffWords` as `Proposals` does |
| Template gallery | Modal with a category rail and preview cards, replacing the plain list in `use-template.tsx` for pages. Categories: Meetings, Operations, HR, Sales, Projects, Your templates |
| Export | Menu: PDF (print view), Word, Markdown. See 5.6 |
| Notifications | Bell renders the two new kinds; a comment notification opens the page with the thread focused |
| Home | "Recent pages" becomes the caller's recently viewed pages, falling back to recently updated for a new user |

Built-in templates (first set, Indian SMB wording, each using panels, status and tables so new users meet the blocks): Meeting notes (MoM), Standard operating procedure, Company policy, Employee onboarding checklist, Leave and attendance policy, Project plan, Decision record, How-to guide, Weekly team report, Client requirements brief, Vendor evaluation, Sales call notes. Proposal and Invoice cover letter remain as they are.

### 5.6 Export

- **PDF:** a print stylesheet on the page view (`@media print` hides shell, toolbar and panel; expands are opened; A4 margins) and `window.print()`. `shortcut: the browser's "Save as PDF", no letterhead or page numbers; upgrade to a server renderer with branded PDF in Phase C.`
- **Word:** the rendered page HTML downloaded as `.doc` with a Word-compatible HTML wrapper. `shortcut: opens in Word, Google Docs and LibreOffice but is not a true .docx; upgrade with the Phase C renderer.`
- **Markdown:** `content_md` as a `.md` file.

All three are client-side and expose only what the reader already sees, so no new permission is involved.

### 5.7 Mobile impact

- Send `schema: 2` on `note.update` (required, see 5.2).
- Show the title emoji in the page list; accept `status` in the `Note` type.
- Nothing else. New blocks appear as readable Markdown text.

### 5.8 Tests

| Layer | Tests |
|---|---|
| Gateway (`apps/collab/test`) | Round trip for each new form, nested panel and expand, invalid attribute values dropped, upload image path accepted and other relative paths refused, old-schema client admitted read-only |
| API (`apps/api/internal/server`) | Trash and undelete with descendants and a missing parent; duplicate respects access; labels validation and permission-filtered `label.list`; `note.mine` hides pages after a restriction; each search filter alone and combined, with the private-space and restricted-page cases; notification recipients for comment, reply and watched edit, including the once-a-day rule and no self-notification; `schema` below 2 refused; built-in template render |
| Web unit | Markdown output of each extension equals the gateway's fixtures (one shared fixture file read by both test suites) |
| e2e (new specs) | `blocks.spec.ts`: insert each block from the slash menu, reload, and co-edit with a second user. `image-upload.spec.ts`: paste and upload. `page-actions.spec.ts`: status, emoji, wide, move, duplicate, delete, restore. `labels-search.spec.ts`. `stars-recent.spec.ts`. `watch.spec.ts`: second user comments, first user's bell. `version-compare.spec.ts`. `template-gallery.spec.ts`. `export.spec.ts`: Markdown download content and the print view's visible elements |
| Existing specs that must stay green | `formats`, `qa-editor` (ED-017 unknown content opens safely), `collab`, `versions`, `inline-comments`, `narrow`, `templates` |

Run `pnpm lint` at the repository root before each commit; it covers `e2e/` as well as the apps.

### 5.9 Work packages

Eight packages with no shared files. P1 and P2 share only the fixture file, which P2 writes first (day 1) and P1 reads. P0 lands first because everything else needs the migration and the regenerated types.

| # | Package | Owns (creates or edits) | Must not touch |
|---|---|---|---|
| P0 | Migration and types (half a day, lead) | `migrations/1330_docs_parity.sql`; `apps/web/src/lib/api.ts` (`Note` gains `status`, `icon`, `wide`); `apps/mobile/src/lib/types.ts`, `apps/mobile/src/components/pages.tsx` (schema field, emoji); `notes/notes.go` (`noteCols`, `Note` struct) | Everything else |
| P1 | Gateway converter | `apps/collab/src/md.ts`, `apps/collab/src/server.ts` (schema parameter, read-only admission), `apps/collab/test/*` | Web, API |
| P2 | Editor blocks (web) | New `apps/web/src/components/editor/blocks.ts`, `block-controls.tsx`; edits `editor/slash.tsx`, `editor/formats.ts` (`safeSrc` only), `editor/html-inline.ts`, `editor/inserts.tsx` (upload tab), `note-editor.tsx` (register extensions, schema parameter); shared fixture `apps/collab/test/fixtures/blocks.md`; e2e `blocks`, `image-upload` | `n/[id]/page.tsx`, shell |
| P3 | Page lifecycle API | `notes/write.go` (`note.update` fields, `note.delete`), new `notes/lifecycle.go` (`note.trash`, `note.undelete`, `note.duplicate`); `files/files.go` (limit); `server/docs_lifecycle_test.go` | `searchNotes`, comment functions |
| P4 | Labels, stars, recents, search API | New module `apps/api/internal/modules/pagemeta` (`label.*`, `note.star`, `note.watch`, `note.viewed`, `note.mine`), registered in `modules.go`; new `notes/search.go` holding the moved and extended `searchNotes`; tests | `notes/write.go` except removing `searchNotes` (agree the cut line with P3 on day 1) |
| P5 | Watch and comment notifications | New `notes/notify_page.go` (recipient logic), a three-line call from `commentAdd` and `noteUpdate` agreed with P3; `notifications/notifications.go` if list shaping is needed; `apps/web/src/components/notifications.tsx`; e2e `watch` | Everything else |
| P6 | Page header, tree and actions (web) | `app/w/[ws]/n/[id]/page.tsx`; new `components/docs/page-header.tsx`, `page-menu.tsx`, `move-dialog.tsx`, `labels.tsx`, `export.ts`; `components/shell.tsx` (tree order, emoji, row menu, Starred and Recent, Trash link); new `app/w/[ws]/trash/page.tsx`; print rules in `app/globals.css`; `components/home/page.tsx` (recent); e2e `page-actions`, `stars-recent`, `export` | Editor folder, search page, versions panel |
| P7 | Search filters, version compare, template gallery (web and docs API) | `app/w/[ws]/search/page.tsx`; `components/versions-panel.tsx`, new `components/version-compare.tsx`; `components/templates/use-template.tsx`, new `components/templates/gallery.tsx`; `modules/docs/templates.go` and new `modules/docs/builtin.go` (template text); e2e `labels-search`, `version-compare`, `template-gallery` | Page header, shell |

Sequence: day 1 P0 merged and the P1/P2 fixture agreed; days 2 to 4 all packages in parallel against the command contracts in 5.4 (web packages stub commands that are not merged yet); day 5 integration, the full e2e suite, and the rollout rehearsal for 5.2 (old tab open during deploy).

Risks:

| Risk | Mitigation |
|---|---|
| The two converters disagree on a new form and content flips between saves | The shared fixture and the byte-equality test are written first and block merge |
| An old tab strips new blocks | Schema parameter with read-only admission and the API check (5.2); rehearsed on day 5 |
| `notes/write.go` is edited by P3, P4 and P5 | New code goes in new files; the three touch points in existing functions are agreed on day 1 and P3 merges first |
| A week is tight for P2 and P6 | If P2 slips, expand ships after panels (they are independent nodes). If P6 slips, duplicate and the Trash page ship first in Phase B; delete with restore is not shipped half-done |

## 6. Open questions for the owner

1. **Status names.** Confluence suggests Rough draft, In progress, Ready for review. Do you want those, or wording your customers use (for example Draft, In review, Final)? "Verified" stays as it is today.
2. **Email for page activity.** Phase A notifies in the app only. Email for comments and mentions, and a daily digest, are planned for Phase B. Should email move into Phase A at the cost of dropping the template gallery to Phase B?
3. **Public links for wiki pages (Phase B).** Should anyone with edit access be able to create one, or only workspace admins? Confluence allows editors, with a space-level off switch.
4. **Who can empty or see the trash.** Proposed: members see and restore what they could edit; nothing is purged automatically until Phase C. Is there a retention period you must promise customers?
5. **Mobile priority.** Rendered reading and comments on the phone are placed in Phase D, after sharing and editor depth. If your customers mostly read on phones, it should swap with Phase C.
6. **Starter templates.** The twelve listed in 5.5 are a first guess at Indian SMB needs. Add, remove or rename any before they are written.

## 7. Binding revisions (lead decisions and design review, 2026-11-25)

This section overrides sections 4–6 wherever they disagree. Builders read it first.

### 7.1 Owner questions, decided by the lead (the owner may override)

1. **Page status:** Draft, In review, Final; Verified stays as today.
2. **Notifications:** in-app only in Phase A. Email and a daily digest come in Phase B.
3. **Wiki public links (Phase B):** editors may create them; admins can turn them off per space.
4. **Trash:** members see and restore what they could edit. There is no purge in Phase A; purge is designed in Phase C (7.5).
5. **Mobile:** stays in Phase D.
6. **Templates:** the twelve in 5.5.

### 7.2 Split: Phase A1, then A2 (review 17)

- **A1 — format and everyday wins:**
  - Blocks: panels, expand, status, date, TOC, code language, image upload.
  - Page: status, title emoji, wide page.
  - Organisation: labels and search filters, stars, recently viewed.
  - Watch and comment notifications.
  - Version compare.
  - The template gallery.
  - Schema fencing (7.3).
- **A2 — page lifecycle and export:**
  - Move, delete with trash, restore, duplicate, stable tree order.
  - Export to PDF (print) and Markdown/HTML for one page.
- **Deferred:** Word export, view counts, and raising the global attachment limit.

### 7.3 Rollout fence (review 1, 2, 15)

- **Schema number:**
  - The editor schema has a number, `DOC_SCHEMA = 2`, in one shared module that web and the collab gateway both import.
  - The gateway has `COLLAB_MIN_SCHEMA`, read from env.
  - A socket that gives no `schema` param counts as 0.
  - When the minimum rises, the gateway closes every open connection with a "reload" close code and refuses connections below it. The web client shows "C Note was updated. Reload to keep editing."
- **Every content write path checks the schema:**
  - socket, `note.update` body saves, mobile, import, AI tools.
  - Body saves also require `expected_epoch`.
  - An old mobile app gets a clear "Update the app to edit; you can still read" (HTTP 426 on body save only).
- **Verify unknown-node handling with a real test.**
  - Before building blocks, P0 writes a two-client test against the locked `y-prosemirror` and `@tiptap` versions. One client knows a node and a mark; the other does not.
  - It records what happens on initial sync, local edit and reconnect.
  - The fence is required whatever the result.
  - **Observed** (test: `apps/web/src/test/unknown-node.test.ts`, run in CI as `pnpm --filter @cnote/web test`; `@tiptap/y-tiptap` 3.3.0, `yjs` 13.6.33, `@tiptap/*` 3.33.1). Client A's schema has a `panel` node and a `status` mark, client B's has neither. B does not show the content read-only or keep it hidden: it **deletes it from the shared document**, and the deletion is an ordinary Yjs update.
    - *Initial sync.* When B renders A's document it removes the whole `panel` element, children included, and the whole paragraph that held the status mark, including the plain text beside the mark (`DONE after` leaves an empty paragraph). The update reaches A and the gateway at once, so the blocks are gone for everyone, not only for B.
    - *Local edit in B.* The removal happens when B renders, not when it types. If B holds the document without a live link, A is untouched until B reconnects; B's own typing is kept.
    - *Reconnect.* B returning from offline removes panels that A added meanwhile and A's edits inside them, keeps its own offline edit, and both converge on the flattened document.
    - *Consequence.* y-tiptap has no read-only fallback, so an old tab must never render content with blocks it does not know. The fence therefore has two halves: refuse a connection below the minimum, and close open connections when the minimum is raised. One window stays: between the web deploy and the minimum being raised, an old tab that is still connected would delete blocks a new tab just wrote. Raise `COLLAB_MIN_SCHEMA` to 2 in the same change window as the web deploy (restart with the env var, or `POST /internal/min-schema` with the internal secret), and do not enable block insertion earlier.

### 7.4 Format contract (review 7, 8, 10)

- **One fixture set,** `apps/collab/test/fixtures/blocks.json` (no `packages/` workspace; the web, Go and gateway tests read this one file), holds every new block. It covers: nested panels and expands, an expand title with markdown characters, a status inside an ordinary table and inside a merged table, links and other marks next to a status, code fences with a language and with backticks inside, literal lookalike text such as `[[toc]]` written in a paragraph, and images.
- **Every serializer and parser must round-trip the fixtures semantically:** TipTap markdown, `formats.ts` `inlineHtml` for merged tables, collab `md.ts` including its table path, import, and the plain-text extractor below.
- **Images:**
  - Uploads use the existing endpoint contract: `note_id`, `filename` and the raw body. The returned metadata builds the URL `/api/files/<id>`.
  - Both parsers accept this one relative form and nothing else.
  - **Correction found in step 0:** the code serves uploaded files at `GET /api/w/<ws>/files/<id>` (`apps/api/internal/server/files.go`; the web already uses it in `billing/expenses.tsx`). There is no `/api/files/<id>` route, so the stored form is `![alt](/api/w/<ws>/files/<id>)` as in 5.1. The fixtures use that form, and `/api/files/<id>` is in them as a refused lookalike.
  - Export embeds images as data URIs (A2).
- **Plain-text extraction:** one function per runtime, with the same fixtures, gives the text for search snippets, search indexing and AI context. Markers are stripped; expand content is kept as text; image alt text is kept; code is kept. New blocks only appear in new edits, so no reindex is needed. Existing content is unchanged.

### 7.5 Lifecycle and permissions, A2 (review 3, 4, 5, 6, 9)

- **Move.** The actor must be able to edit every page in the subtree and both the old and the new parent, or the space root.
  - If the move removes an inherited restriction, so the pages become more visible, the actor must also be able to manage restrictions on the root: page owner or space admin.
  - Otherwise the call fails with "Some pages here are restricted", and hidden pages are never named.
- **Delete.**
  - The actor must be able to edit every live page in the subtree.
  - The call writes `deletion_id` (uuid), `deleted_by` and `deleted_at` onto exactly the pages it deletes, under row locks. Pages already in the trash keep their own deletion.
  - The gateway receives `note.deleted`, flushes the room and closes it.
- **Restore.**
  - Restore works by `deletion_id`. It goes back under the original parent if that parent is live.
  - If the parent is gone, the pages go to the space root, but only when they were not under an inherited restriction or the actor is a space admin. Otherwise restore is refused with a reason.
  - Restore bumps the epoch, which fences old rooms and old mobile saves.
- **Duplicate.**
  - Duplicate only makes a sibling, in the same parent context. It copies the page's own restrictions and inherits by position, so access never widens.
  - Attachments are copied as new attachment rows for the new note, pointing at new object copies made server side.
  - Duplicate never includes children in A2.
- **Purge.** No purge UI in A. Phase C designs purge: who may purge, the subtree, object-store cleanup with retries, revisions, public links. Section 6 of Tables shows the pattern.
- **Export.**
  - Export covers one page only, built from the reader's live editor state on the client, so it is current.
  - Collapsed expands are printed open, the TOC is resolved, code keeps its language label, and images are embedded.
  - Export never includes child pages and never runs through public links in A2.

### 7.6 Notifications, history, organisation (review 11, 12, 13, 14)

- **One delivery path.** A worker consumer on `note.updated`, `comment.created` and `comment.resolved` events fans out notifications. Those events are emitted both by Go commands and by gateway snapshots. Recipients are deduplicated per page and actor per 10 minutes. The notification stores `note_id` and an optional `thread_id`.
- **Access is checked twice.** Once at send time, and again in `notification.list`, which drops entries for pages the reader can no longer see. Watches of people who lost access are removed lazily.
- **Version history follows current access, as in Confluence.** This is the stated policy for list, get, compare and restore. Compare needs read access, and restore needs edit access.
- **Labels, stars, recents and search filters** apply the full access predicate (readable spaces, direct shares, `note_access` restrictions) inside the query, before grouping, counting or paging. Rows for pages that are now invisible are ignored.
  - **Guests:** they see labels only on pages they can read. `label.list` returns labels from readable pages only.
  - **AI:** state-changing commands (star, watch, label edit) are not in the read-only AI scope.
- **Template gallery.**
  - Built-in ids are `builtin:<key>` and are checked before UUID parsing.
  - The gallery and render keep space filtering.
  - Saving a restricted page as a template stays refused.

### 7.7 Ownership and migration (review 16)

- **Migration:** the next free number when step 0 lands (1329 if still free; 1327 is reserved for invites). The lead confirms it again at merge.
- **Shared files:** `notes/write.go` command registration, event emission, the schema module, fixtures and `e2e/helpers` belong to the step-0 owner (the lead). Builders add handlers in their own files and register them from `init()`, as the Tables module does.

### 7.8 Phase A1 work packages (step 0 is done; four builders in parallel)

**What step 0 left in place.** Builders rely on these and must not redo them.

- **Fence.** `DOC_SCHEMA = 2` and `CLOSE_RELOAD = 4410` in `apps/collab/src/doc-schema.ts` (web re-exports it from `apps/web/src/lib/doc-schema.ts`; Go has `notes.DocSchema`, kept equal by `schema_test.go`). The gateway takes `COLLAB_MIN_SCHEMA` (default 0), refuses and closes below it, and raises it at runtime with `POST /internal/min-schema {"min":2}` and the internal secret. A person's body save through `note.update` needs `schema >= 2` and `expected_epoch`, else HTTP 426 `upgrade_required` or 400. API keys, AI employees and nested `Ctx.Call` writes are exempt: they run no editor converter and cannot flatten a block; import, template render, restore and erase write server side, and restore bumps `collab_epoch`. Details in `notes/schema.go`.
- **Migration `1329_docs_parity_a1.sql`:** `core.notes.page_status` (`draft`, `in_review`, `final`, null), `icon`, `wide`; `core.labels`, `core.note_labels`, `core.note_stars`, `core.note_views` (recent), `core.note_watches` (reason `manual`, `creator`, `owner`, `commenter`); `core.notifications.thread_id` and kinds `comment`, `page_updated`. All tenant RLS with composite FKs. The JSON field for the page status is `status`; the column is `page_status`.
- **Command stubs** in `notes/a1_commands.go` (inputs, outputs, roles; web types in `apps/web/src/lib/docs.ts`): `label.set`, `label.list`, `note.star`, `note.watch`, `note.viewed`, `note.mine`. A package replaces a stub with `a1Handlers["label.set"] = labelSet` from an `init()` in its own file. Extended in place by their owners: `note.update` (`status`, `icon`, `wide`), `search.notes` (filters), `template.list` and `template.render` (built-ins).
- **Files split out of `notes/write.go`** so owners do not share it: `notes/comments.go` (W3) and `notes/search.go` (W2).
- **Fixtures and extractor.** `apps/collab/test/fixtures/blocks.json`; Go `notes.PlainText` and `notes.Snippets` (wired into `search.notes`, hybrid search, AI summaries and AI indexing); tests in `apps/collab/test/fixtures.test.ts`, `apps/web/src/test/fixtures.test.ts`, `notes/plaintext_test.go`, `importers/blocks_test.go`. Block-specific assertions are skipped behind `BLOCKS_LANDED = false` in the two TS files; **W1 flips both** and fixes what they then report.

**Rules for all four.** Touch only the files listed for your package. `notes/write.go`, `notes/notes.go`, `notes/a1_commands.go`, `command/*`, `apps/collab/src/server.ts`, `.github/workflows/ci.yml`, `e2e/helpers.ts`, `apps/web/src/lib/api.ts` and migrations are the lead's: ask for a change, or (W2 only, below) own the one marked exception. Register commands from `init()`. New Go files, new tests. Use Tailwind classes; `apps/web/src/app/globals.css` is W1's. Run `pnpm lint` at the root before handing back. No DB changes: if one is needed, ask the lead for `1330_*`.

| | W1 Editor blocks and converters | W2 Page chrome, labels, stars, recents, search filters | W3 Watch, notifications, version compare | W4 Template gallery |
|---|---|---|---|---|
| **Goal** | Panels, expand, status, date, TOC, code language and copy, image upload and paste; slash menu; both converters agree on every fixture | Status, title emoji, wide page; labels; stars; recent; search filters (API and web) | `note.watch`; one worker consumer for comment and page notifications; the bell; version compare | Gallery with 12 built-in templates |
| **Creates** | `apps/web/src/components/editor/blocks.ts`, `editor/block-controls.tsx`, `apps/web/src/test/blocks.test.ts`, `e2e/blocks.spec.ts`, `e2e/image-upload.spec.ts` | `notes/labels.go` (`label.set`, `label.list`), `notes/mine.go` (`note.star`, `note.viewed`, `note.mine`), `apps/web/src/components/docs/page-header.tsx`, `docs/labels.tsx`, `apps/api/internal/server/docs_a1_test.go`, `e2e/page-chrome.spec.ts`, `e2e/labels-search.spec.ts`, `e2e/stars-recent.spec.ts` | `notes/watch.go` (`note.watch`, auto-watch rules), `apps/api/internal/worker/pagenotify.go` (the consumer), `apps/web/src/components/version-compare.tsx`, `apps/api/internal/server/docs_watch_test.go`, `e2e/watch.spec.ts`, `e2e/version-compare.spec.ts` | `apps/api/internal/modules/docs/builtin.go` (the 12 templates as Go constants), `builtin_test.go`, `apps/web/src/components/templates/gallery.tsx`, `e2e/template-gallery.spec.ts` |
| **Edits** | `editor/formats.ts` (`safeSrc` for the upload path), `editor/html-inline.ts` (status and date in merged tables), `editor/slash.tsx`, `editor/inserts.tsx` (upload tab), `editor/menus.tsx`, `editor/toolbar.tsx`, `components/note-editor.tsx` (extension list only), `apps/web/src/app/globals.css`, `apps/collab/src/md.ts`, `apps/collab/test/md.test.ts`, `apps/collab/test/fixtures.test.ts` and `apps/web/src/test/fixtures.test.ts` (flip `BLOCKS_LANDED`), `apps/collab/test/fixtures/blocks.json` (only to fix a canonical form, and say so), `notes/plaintext.go` and its test | `notes/write.go` (**exception**: `note.update` fields `status`, `icon`, `wide` and their `changed` names only), `notes/notes.go` (`Note` and `noteCols`: the same three fields), `notes/search.go`, `apps/web/src/app/w/[ws]/n/[id]/page.tsx`, `components/shell.tsx`, `components/home/page.tsx`, `apps/web/src/app/w/[ws]/search/page.tsx`, `apps/web/src/lib/docs.ts`, `apps/mobile/src/components/pages.tsx` (emoji in the list, optional) | `notes/comments.go`, `modules/notifications/notifications.go` (`thread_id` in the list, second access check, lazy removal of watches), `internal/notify/notify.go` (thread id), `internal/worker/wire.go` (one `OnEvent` line per event), `modules/revisions/revisions.go` (history follows current access), `components/notifications.tsx`, `components/versions-panel.tsx` | `modules/docs/templates.go` (`template.list`, `template.render`: `builtin:<key>` checked before UUID parsing, space filtering kept), `components/templates/use-template.tsx` |
| **Does not touch** | Anything under `apps/api` except `notes/plaintext.go`; page header, shell, search page | Editor folder, `note-editor.tsx`, notifications, versions panel, templates | Editor folder, page header, shell, search | Editor folder, page header, shell |
| **Depends on** | Nothing. Lands first | Migration only. Reads `?thread=<comment id>` in `page.tsx` and focuses that thread (W3 links to it). Star and Watch toggles in the header call `noteStar` and `noteWatch` from `lib/docs.ts` | Migration; W2's `note.mine` reads `core.note_watches` | Template text uses the stored forms in 5.1 and the fixtures; its e2e runs after W1 |

Package notes.

- **W1.**
  - Stored forms are those of 5.1, with the image form corrected in 7.4. Both converters, the plain-text extractor and the fixtures must agree. If a fixture's canonical form is wrong, change the fixture first and say so.
  - Findings the fixtures already show (fixtures `status-next-to-other-marks` and `image-upload-and-external`): `SafeImage` drops any non-http image together with its alt text; the browser converter turns `<span data-status>[text](url)</span>` into escaped text that changes on every save; and the Markdown importer treats a page whose first character is `<` as HTML, so an exported page that starts with an expand loses the tags (`importers/blocks_test.go`). W1 fixes the first two; the third goes to the importer's owner after A1.
  - Reaching the block insertion is gated on `COLLAB_MIN_SCHEMA` being 2 (7.3): ship it behind the release step, not before.
- **W2.**
  - `note.update` fields are validated (`status` in the list or null, `icon` at most 16 characters, `wide` boolean) and reported in `changed`. Labels, stars, recents and search filters apply the full access predicate inside the query, before grouping, counting and paging (7.6). Guests: labels from readable pages only. `search.notes` takes `q` empty when a filter is given, and keeps its snippet path through `Snippets`.
  - Tree order and the whole of 5.5's A2 items (move, trash, duplicate, export) are out of A1.
- **W3.**
  - The consumer listens to `note.updated`, `comment.added`, `comment.resolved` and `comment.reopened`. These are the event names in the code today (7.6 calls the first of them `comment.created`; do not rename it, `activity`, hooks and agents read it). Gateway snapshots emit `note.updated` too, with the actor in the event row, so one consumer covers both.
  - It dedupes per page and actor for 10 minutes, never notifies the actor, checks read access at send time, and `notification.list` checks again. Auto-watch (creator, owner, commenter) is done in the same consumer from `note.created`, `note.updated` with `owner` changed, and `comment.added`; no change to `write.go` is needed.
  - Compare is client side with `diffWords` over `note.revision` and the current body; it needs read access, restore needs edit (7.6). `note.watch` needs page view access; guests may watch pages shared with them.
- **W4.**
  - Built-in ids are `builtin:<key>` and are checked before UUID parsing. Saving a restricted page as a template stays refused. The first twelve are the list in 5.5; Proposal and Invoice cover letter stay as they are. The text is Go constants, so a wording fix reaches every workspace.

**Release order.** Gateway (min 0), API, web (all four packages), then `COLLAB_MIN_SCHEMA=2` the same day (7.3). Mobile sends `schema: 2` and `expected_epoch` already (step 0), so the app release can go first.

**Integration (lead, after the four land).** Merge W1 first, then W2, W3, W4. Run the full e2e suite and the rollout rehearsal: an old tab (no `schema`) open during the minimum raise must be closed with the reload message and must not change the page.

### 7.9 A2 contracts (binding)

Overrides 5.3 to 5.6 and extends 7.5 for Phase A2. Builders L1, L2 and L3 work in parallel against this text. Step 0 (done) left in place: migrations 1334 to 1336, the five command stubs in `notes/a2_commands.go`, the internal stub `file.copy_for_note` in `files/a2_copy.go`, `Note.position` in `notes.go` and `lib/api.ts`, the web wrappers and types in `apps/web/src/lib/docs.ts`, and `note.delete` already writing `deletion_id` and `deleted_by` (L1 replaces it). Command names follow 5.4, with one change: restore is `note.undelete`, because `note.restore` is already version restore in the revisions module.

**Decisions the owner may override (also listed in the hand-off):**

1. **No cross-space move in A2.** The API refuses it today ("a page's parent must be in the same space") and keeps refusing. A cross-space move changes who can read the page, its files and its restrictions, so it needs its own design. `note.move` has no `space_id`.
2. **Typed pages (invoice, deal, expense and the like) are not part of A2.** `note.duplicate` refuses a page with a non-empty `types`; `note.delete` refuses a subtree that holds one below its root; `note.trash` and `note.undelete` handle plain pages only (`types = '{}'`). A typed page deleted by `note.delete` behaves exactly as it does today, except that it now gets a `deletion_id`, so it can be listed later.
3. **Guests never run these commands**, even on pages shared with them: each one acts on other pages (children, siblings, parents).

#### Data model (migrations 1334 to 1336)

| Column or object | Meaning |
|---|---|
| `core.notes.position integer NOT NULL DEFAULT 0` | Order among siblings, i.e. rows with the same `(space_id, parent_id)`. Sort key everywhere: `position, created_at, id`. Gaps of 1024 |
| `core.notes.deletion_id uuid`, `deleted_by uuid` | Set together with `deleted_at` on exactly the pages one delete took; all three are null on live pages (`notes_deletion_consistent` CHECK). Pages deleted before A2 got one `deletion_id` each, `deleted_by` null |
| Trigger `notes_default_position` | On insert, `position = 0` becomes `max(sibling position) + 1024`. Every insert path (create, duplicate, templates, importers) lands last without knowing about order. Legacy pages stay at 0 and sort by creation time until a reorder renumbers their siblings |
| `core.note_access_any(note, uid, is_admin, space_readable)` | Same as `note_access` but the starting page may be in the trash. Trash and restore ask "could this person edit it as it was". Ancestors are walked the same way, deleted ones included |
| Indexes `notes_siblings (space_id, parent_id, position) WHERE deleted_at IS NULL`, `notes_deletion (workspace_id, deletion_id, deleted_at DESC) WHERE deletion_id IS NOT NULL` | Both built `CONCURRENTLY` in their own files |

`parent_id` stays untouched when a page is deleted, so restore can find its original parent. A live page whose parent is in the trash can only exist from pre-A2 deletes (A2 delete takes the subtree); the tree shows such a page at the space root and does not change its row.

#### Shared rules

- **Edit check on a page** = `access.AssertNoteAccess(c, id, true)` semantics: `core.note_access(...) = 'edit'` plus `AssertSpaceAccess(space, write)` (a space viewer cannot edit). **On the space root** = `AssertSpaceAccess(space, true)`.
- **Subtree** = the page and all live descendants (recursive on `parent_id`, `deleted_at IS NULL`), at most 500 pages. More than that fails with "This page has too many sub-pages for one step; move or delete them in parts" (`invalid`). The query takes the rows `FOR UPDATE` in `id` order, then checks access on the locked set.
- **The generic refusal.** Whenever any page of the subtree, or a parent involved, fails the edit check or is invisible to the caller, the command fails with `forbidden` and the message `Some pages here are restricted` (constant `RESTRICTED_MESSAGE` in `docs.ts`). The message, the code and the response never name, count or hint at which page or how many; the same text is returned for "cannot see" and "cannot edit", so a hidden page cannot be probed. A missing id is `not_found` only when the caller could not tell it from a hidden one anyway (the page the caller named is checked first with `AssertNoteAccess` and a failure there is `not_found`, as today).
- **Row locks and order.** Take locks in this order everywhere: the space's parent chain is never locked; lock subtree rows ordered by `id`; then, for a move, the new parent row `FOR SHARE`. A move also locks all siblings of the destination parent (`FOR UPDATE`, ordered by `id`) only when it renumbers.
- **Epoch.** A move or reorder does not touch `collab_epoch` or the Yjs document. Delete does not bump it (the room is closed); restore bumps it.
- **No purge.** Hooks left for Phase C: `deleted_by` and `deletion_id` are kept forever, `core.events` keeps `note.deleted` and `note.undeleted`, and nothing hard-deletes in A2. The DB `ON DELETE SET NULL` on `parent_id` is not used by A2 code.

#### `note.move` and `note.reorder` (L1)

`note.move {id, parent_id: uuid|null, before_id?: uuid|null}` returns `{id, space_id, parent_id, position}`. Group `write_notes`, member.

1. Load the page (`not_found` if not live or not readable). Reject a `parent_id` in another space with the existing "a page's parent must be in the same space"; reject a parent inside the page's own subtree with the existing cycle message (the shared `checkMove` stays; L1 moves it to a new file and keeps its name).
2. Edit check on every page of the subtree, on the old parent (or the space root), and on the new parent (or the space root). Any failure is the generic refusal.
3. **Inherited restrictions.** Let `A` be the restricted ancestors of the old parent, including the old parent (restriction `edit` or `view`), and `B` the same for the new parent. For each ancestor in `A` that is not in `B`, the pages become more visible or editable, and the actor must be that ancestor's owner (`owner_id`, or `created_by` when null) or a space admin or workspace admin. If not, the generic refusal. Moving into a restricted parent needs nothing extra beyond the new-parent edit check. A page's own restriction and its `note_permissions` and shares never change in a move. `restrictions_released` is true in the event when `A` minus `B` is not empty.
4. **Position.** `before_id` null or omitted: `position = max(sibling position) + 1024` among live siblings in the new parent. `before_id` set: it must be a live sibling in the new parent (else `invalid`), and the page gets the midpoint between the previous sibling's position (0 when first) and `before_id`'s position. If the integer gap is below 2, renumber all live siblings of that parent to 1024, 2048, and so on, in `(position, created_at, id)` order, in the same transaction, then place the page. Siblings the caller cannot see are renumbered too (order only, nothing exposed).
5. Update `parent_id`, `position`, `updated_at`, `version + 1`. Emit `note.moved` on the page: `{from_parent, to_parent, space_id, position, restrictions_released}`. Moving to the same parent with no `before_id` is allowed and acts like "send to end".
6. `note.update` with `parent_id` must call the same function (one code path) so edit-only callers cannot bypass the subtree check. A `parent_id: null` in `note.update` keeps meaning "unchanged"; moving to the root is `note.move` only.

`note.reorder {id, before_id: uuid|null}` returns `{id, parent_id, position}`. Group `write_notes`, member. Edit check on the page and on its parent (or the space root); not on the whole subtree (the children do not change). Position rule as step 4, within the current parent. `before_id` must be a live sibling or `invalid`. Emits `note.reordered` `{parent_id, position}`. A page that is not moving between parents never triggers the restriction rule.

**Web:** drag-and-drop is `note.move` (drop on a page = last child; drop between rows = `before_id` of the row below, with that row's parent). The keyboard alternative is the context menu and the page menu: "Move to..." opens a tree picker (same space, excluding the page's own subtree), and "Move up" and "Move down" call `note.reorder` with the neighbour. Keyboard: Alt+Up and Alt+Down on a focused tree row do the same.

#### `note.delete` (L1; replaces `noteDelete` in `write.go`)

Input `{id}`; output `{deleted: true, deletion_id, count}` (the old `deleted: true` key stays, so current callers keep working). Group `destructive`, member; agents have it off by default.

1. Page must be live and editable by the caller (`AssertNoteAccess(write)`); else `not_found` or `forbidden` as today.
2. Lock the subtree rows `FOR UPDATE`. Check the edit rule on every live page; a failure is the generic refusal. A typed page below the root fails with "This page has items that cannot be deleted with it" (decision 2).
3. One statement: `UPDATE core.notes SET deleted_at = now(), deletion_id = $new, deleted_by = $actor, updated_at = now() WHERE id = ANY(subtree)` (same `now()` for all). Pages already in the trash are not in the subtree set and keep their own `deletion_id`, `deleted_by` and `deleted_at`.
4. **Events.** One `note.deleted` per deleted page, `note_id` set, payload `{deletion_id, root_id, count}` (emitted in subtree order, root first). There is no `pg_notify`: the gateway finds out through the fence poll and the periodic recheck (below). **As built:** the LISTEN design was dropped.
5. Related rows stay: comments, relations, revisions, watches, stars, labels, attachments and shares are not touched. Search, AI context, `note.mine`, labels and mentions already filter `deleted_at IS NULL`; L1 greps for any reader that does not and fixes it in its own files only (otherwise reports to the lead).
6. A checkpoint revision is written for each page that has unsaved changes (`checkpoint(c, id, actor, 0)` with reason `edit`), so the content at deletion is in history.

**Gateway (L1, `apps/collab/src/server.ts` and `apps/collab/test`).**
- **As built (LISTEN dropped):** no `LISTEN`. A fenced fold (`POST /internal/fold` with `fence: true`, body `{note_id, workspace_id, fence}`; `workspace_id` is required) marks the room closed to writes for up to 60 s and starts a short poll of the fenced pages. When the poll (or the periodic recheck) sees the page deleted, it runs steps (b) to (d) below. An unfinished delete lifts the fence when it expires.
- When a note with an open room is found deleted: (a) stop applying new frames to the room; (b) persist (fold logged updates into `content_ydoc` and `content_md`) **allowing a deleted page**: the snapshot `UPDATE` guard `deleted_at is null` is relaxed to `(deleted_at is null OR deletion_id IS NOT NULL)` for the final snapshot only, and the fold-on-start path also folds trashed pages; live edits (`note_updates` inserts) stay guarded by `deleted_at is null`; (c) close every socket with code `4411` (`CLOSE_DELETED`, reason `page deleted`); (d) drop the room and release its advisory lock.
- The periodic recheck (`recheckMs`) also tests `deleted_at`; a room found deleted is flushed and closed the same way. A socket that connects to a deleted page is closed with 4411 immediately.
- The web editor treats 4411 as final: no reconnect loop; it shows "This page was moved to the trash" with a link to Trash.
- Tests (gateway): delete with two peers closes both with 4411 after the last edit is saved in `content_md`; restore later shows that edit; reconnect to a deleted page is refused; a delete the poll missed is caught by the recheck.

#### `note.trash` (L1)

Input `{space_id?, limit?, before?}`; output in `docs.ts` `TrashEntry[]`. Group `read`, member (set explicitly; guests excluded). One row per `deletion_id`, newest `deleted_at` first, `limit` default 50, max 200, the cursor is `before` (the last row's `deleted_at`) plus `before_id` (its `deletion_id`); with only `before`, everything strictly older. 7.10 item 9 describes the scan.

- A deletion is **listed** only if `note_access_any(page, ...)` is `edit` for every page in it (plain pages only) and `AssertSpaceAccess(space, write)` passes for its space. A deletion with any page the caller cannot edit is not listed at all, and nothing says it exists.
- `root` is the page whose `parent_id` is not in the same deletion (for a legacy single-page deletion, that page). `page_count` counts the deletion's pages. `deleted_by` is null for legacy deletions or when the person left.
- `restore_to` and `blocked_reason` are computed exactly as restore would decide them (below), so the UI can disable Restore with the reason: `original` when the original parent is live, `space_root` when it is gone but allowed, `blocked` otherwise with reason text from the table below.
- Indexed by `notes_deletion`; the access check runs on the paged set (at most `limit` deletions), not on the whole trash.

#### `note.undelete` (L1; named `note.undelete` because `note.restore` is version restore)

Input `{deletion_id}`; output `{deletion_id, root_id, space_id, count, placed: "original" | "space_root"}`. Group `write_notes`, member.

1. Lock the deletion's rows `FOR UPDATE` (order by `id`). None found: `not_found` ("note"). Pages must all be plain pages; the caller needs `note_access_any = 'edit'` on every page and space write access, else the generic refusal (a deletion the caller could not list is `not_found`, never `forbidden`).
2. Find the root (as in `note.trash`). If its `parent_id` is null: `placed = original` (the space root is always live). If the parent is live (`deleted_at IS NULL`): `placed = original`, `parent_id` unchanged; the caller also needs the edit check on that parent (generic refusal otherwise).
3. If the parent is gone (in the trash under another deletion, or no longer there): the root goes to the space root (`parent_id = null`, `placed = space_root`), with `position` last, but **only if** the root was not under an inherited restriction (no ancestor of the old parent, deleted or not, has `restriction <> 'none'`; ancestors are read through `parent_id` as stored) **or** the caller is a space admin or workspace admin. Otherwise fail with `conflict`: `blocked_reason` text from this table.

   | Case | `blocked_reason` and error text |
   |---|---|
   | Parent in the trash, under a restriction, caller not admin | "The parent page is in the trash and this page was inside a restricted area. Restore the parent first, or ask a space admin." |
   | Parent no longer exists, same restriction rule | "The parent page no longer exists and this page was inside a restricted area. Ask a space admin." |
   | Pages are still being saved | "This page is still being saved. Try again in a moment." (see step 5) |

4. Update the whole set in one statement: `deleted_at = null, deletion_id = null, deleted_by = null, collab_epoch = collab_epoch + 1, version = version + 1, updated_at = now()`; plus the root's `parent_id` and `position` when it moved or the old position collides (then `max + 1024`). Own restrictions come back with the pages untouched.
5. **No unsaved edits may be orphaned by the epoch bump.** If any page has rows in `core.note_updates` with `epoch = collab_epoch` (the gateway has not folded them), fail with `conflict` "This page is still being saved. Try again in a moment." The gateway's final flush on delete normally makes this empty.
6. Emit `note.undeleted` per page (`{deletion_id, root_id, placed}`). No notification is sent. Gateways close rooms whose epoch no longer matches (existing 4409 path).

#### `note.duplicate` (L1)

Input `{id}`; output `{id, space_id, parent_id, title, position, attachments_copied}`. Group `write_notes`, member. **Sibling only**: same space, same `parent_id`, `position` = `max + 1024` (last), no `before_id`.

- Needs view access to the source (`AssertNoteAccess(read)`) and edit access to the parent (or `AssertSpaceAccess(write)` for the space root); else the generic refusal. A page with `types` refuses (decision 2): "Only plain pages can be duplicated".
- **Copied:** `title || ' (copy)'` (cap 200 chars as `note.create`), `content_md`, `icon`, `wide`, `page_status` is not copied (null), labels, the page's own `restriction` and its `note_permissions` rows (copied as `granted_by` = the actor; the actor is added with `edit` when `restriction <> 'none'` so nobody is locked out, as `note.restrict` does). The copy lies under the same parent, so it inherits the parent's restrictions by position and access never widens. **Not copied:** children, comments, revisions (the copy starts with version 1 and gets its first revision on first save), shares (`note.share` rows), stars, watches (the actor watches as creator), views, relations other than the `child` link to the parent, verify and freshness state (`freshness 'fresh'`, default `verify_by`), `content_ydoc` (null; the gateway builds the room from `content_md`, as for imports). Owner and `created_by` are the actor.
- **Attachments.** After the insert, call the internal command `file.copy_for_note {from_note_id, to_note_id}` with `c.Call` (the files module owns `core.attachments` and the object store; `notes` must not import it, per `boundaries_test.go` and the service-boundary rule). It runs the same guards as an upload on both pages, inserts new `core.attachments` rows for the copy (new ids, same filename, content type and size, `created_by` = actor) and copies the bytes: the `data` column for Postgres storage, or a server-side object copy to a new key `<workspace>/<new id>` for object storage (a failed copy rolls the whole transaction back). It returns `[{from_id, to_id}]`. The count obeys the per-page limit of the files module. L1 then rewrites every occurrence of `/api/w/<workspace>/files/<from_id>` in the copy's `content_md` to the `to_id`, with an exact-id match (a simple `strings.NewReplacer` over the pairs), and updates `content_md` and `search_plain` through the same helper `note.update` uses. Attachments the source does not own are left as they are.
- Events: `note.created` for the copy with `{title, types: [], space_id, duplicate_of: <source id>}` (so search and index consumers behave as for a new page), and nothing else.
- **Live edits shortcut:** the source's `content_md` is the last gateway snapshot, which can lag typing by about two seconds. `shortcut: duplicate copies the saved snapshot; upgrade to a gateway-rendered copy if people duplicate pages while typing in them (the web flushes by waiting for the editor's "saved" state before calling).`

#### Roles, guests, AI scope, events and audit (every command)

| Command | Group | Minimum role | Guest | Page checks | In read-only AI scope | Events (audit trail `core.events`) |
|---|---|---|---|---|---|---|
| `note.move` | `write_notes` | member | no | edit on subtree, old and new parent; manage rule for released restrictions | no | `note.moved` |
| `note.reorder` | `write_notes` | member | no | edit on page and parent | no | `note.reordered` |
| `note.delete` | `destructive` | member | no | edit on the whole subtree | no (agents: group off by default) | `note.deleted` per page |
| `note.trash` | `read` | member | no | edit on every page of a listed deletion | yes (lists only what the caller could restore; a read scope adds no reach) | none |
| `note.undelete` | `write_notes` | member | no | edit on every page as it was, parent edit when restored in place | no | `note.undeleted` per page |
| `note.duplicate` | `write_notes` | member | no | view on source, edit on parent | no | `note.created` |

- **Audit.** `core.events` is the append-only audit trail and carries the actor, an agent's `on_behalf_of` and the page id; the audit module already lists every kind. No new audit table. Commands refused for an agent or API key are written as `command.denied` by the gate. There is no separate audit write in the handlers.
- **Agent and API key principals** have the same page checks as the person they act for. Approval and confirmation rules are unchanged (`note.delete` stays unconfirmed for people; an approval rule can still hold it).
- **Event consumers.** The gateway does not read a notification channel (see the fence poll above); the worker consumers from 7.6 treat `note.deleted` as "stop notifying about this page" (they already skip deleted pages at send time). Nothing in A2 consumes `note.moved`; it is there for audit and for Phase B.

#### Export (L3, client only)

One page, built on the client from the live editor state; no server command, no new permission, no children, no public links, no share links.

- **Entry point.** The page "..." menu "Export" with "PDF (print)", "Markdown (.md)", "HTML (.html)". Available whenever the reader can see the page; the editor state in the open tab is the source (`editor.getJSON()`, `getHTML()`, and the existing Markdown serializer), so it reflects unsaved typing and every co-editor's change. A reader without a live editor (view-only open) uses the same read-only editor instance.
- **One builder** `apps/web/src/components/docs/export.ts`: `buildExportDom(editor, opts) -> HTMLElement` produces a detached, fully resolved copy used by all three outputs.
  - **Collapsed expands are opened**: each `details` gets `open` and its summary stays a heading line.
  - **TOC resolved**: each Toc block is replaced by a static nested list of the page's current headings (text and level, with `#id` anchors on the headings, as in the page), not the live component.
  - **Code keeps its language**: each `pre code` keeps `class="language-x"` and a small visible label with the language name on top of the block (print and HTML both).
  - **Images embedded**: every `img` whose source is `/api/w/<ws>/files/<id>` or `data:` is converted to a `data:` URI by fetching with the user's session cookie; a fetch failure keeps a visible placeholder with the alt text and shows a notice listing how many images could not be embedded. External `http(s)` images are fetched with `fetch` first; if the browser blocks it (CORS), they are left as links. Total embedded image bytes are capped at 25 MB: past the cap the remaining images become links and the notice says so.
  - Panels, status chips, date chips, tables and task lists render as in the page, with colours inlined (`style` attributes), because the downloaded HTML has no app stylesheet.
  - Comment highlights, cursors, selection and editor chrome are removed.
- **Markdown**: the editor's Markdown serializer output (`.md`), with embedded images left as the stored `/api/w/<ws>/files/<id>` form (a note in the file header comment is not added); file name `<title>.md`, title cleaned to `[A-Za-z0-9 _-]` and capped at 80 characters, `page` when empty.
- **HTML**: one self-contained file, `<!doctype html>` with `<meta charset>`, the title, the resolved DOM, a small embedded stylesheet (the print stylesheet below plus screen basics) and data URIs; no script. File name `<title>.html`. Built with a `Blob` and an `a[download]`.
- **PDF**: the resolved DOM is shown in a hidden print container (`#print-root`, in `app/globals.css`, which L3 owns for this block only) and `window.print()` runs; `@media print` hides everything else, sets A4 with 18 mm margins, avoids breaks inside headings, tables rows, panels and images, and prints link URLs after link text only for external links. Mobile browsers use the same path. `shortcut: the browser's "Save as PDF"; no running header or page numbers; upgrade to a server renderer with branded PDF in Phase C.`
- **Safe by construction**: the export DOM is made from the editor's own output, never from raw stored HTML; any text goes through the DOM, not string concatenation, and the filename is sanitised as above.
- **Tests (L3)**: unit tests over fixtures from `apps/collab/test/fixtures/blocks.json` (collapsed expand, TOC, nested panel, status inside a table, code with a language, image), an e2e `export.spec.ts` (download Markdown, download HTML with a data-URI image, print view shows an opened expand and a resolved TOC and hides the shell).

#### Mobile impact

- Lists from `note.list` and `note.mine` already exclude trashed pages; a trashed page opened from a stale list returns `not_found`. Show the existing "page not found" state.
- The gateway closes a room with 4411 when the page is deleted while open; the mobile editor must treat 4411 as "page deleted" (no reconnect) when mobile gets the collab editor in Phase D. Mobile does not use `note.delete` today.
- `Note` gains `position`; the mobile page list keeps its current order (`updated_at`) and may ignore it. No A2 command is required of mobile and none is called from it. Mobile stays in Phase D.

#### Work packages (disjoint file ownership; step 0 owns everything else)

| | L1 backend lifecycle and gateway | L2 web tree, Trash, restore, duplicate | L3 export |
|---|---|---|---|
| **Creates** | `notes/lifecycle.go` (move, reorder, delete, shared checks), `notes/trash.go` (`note.trash`, `note.undelete`), `notes/duplicate.go`, `files/a2_copy.go` body (`file.copy_for_note`, edit in place), `apps/api/internal/server/docs_a2_test.go`, `apps/collab/test/lifecycle.test.ts` | `apps/web/src/components/docs/page-actions.tsx` (menu items and dialogs), `docs/move-dialog.tsx`, `docs/tree-dnd.tsx`, `app/w/[ws]/trash/page.tsx`, `e2e/page-tree.spec.ts`, `e2e/trash.spec.ts` | `apps/web/src/components/docs/export.ts`, `docs/export-menu.tsx`, `apps/web/src/test/export.test.ts`, `e2e/export.spec.ts` |
| **Edits** | `notes/write.go` (`noteDelete` replaced; `noteUpdate` parent branch calls the shared move check; `checkMove` moves out; nothing else), `notes/a2_commands.go` (swap stub for `a2Handlers[...]` only through init()), `apps/collab/src/server.ts` (listener, final flush, 4411), `apps/collab/src/doc-schema.ts` (`CLOSE_DELETED = 4411` beside the other codes) | `components/shell.tsx` (tree order by `bySiblingOrder`, orphan at root, drag and drop, row menu, Trash link under each space, TREE_EVENT reload), `app/w/[ws]/n/[id]/page.tsx` (menu entries Move, Duplicate, Delete, 4411 notice), `components/note-editor.tsx` (4411 handling only) | `components/docs/page-header.tsx` (add the Export menu entry only), `app/globals.css` (the print block only) |
| **Must not touch** | web | API, gateway | API, gateway, `shell.tsx` |

If L2 and L3 both need a line in `n/[id]/page.tsx` or `page-header.tsx`, L2 adds the Export menu mount point once (`<ExportMenu editor={...} />` imported from L3's file) and L3 only fills the component. The shared files stay with the lead: `a2_commands.go` schemas, `docs.ts`, `api.ts`, migrations, `notes/notes.go`, `command/*`, `e2e/helpers.ts`. A change there is a request to the lead.

#### Tests (what must exist before merge)

- **API (`docs_a2_test.go`)**: move with a restricted new parent, a restricted old parent (released restriction needs owner or admin), a hidden page in the subtree (generic refusal, same text for hidden and read-only), a cycle, another space; reorder with the gap exhausted (renumber) and with siblings the caller cannot see; legacy all-zero positions then reorder; delete with a subtree, with an already-trashed child (keeps its own deletion), with a hidden descendant (refused), with a typed descendant; trash visibility (not listed when one page is not editable); undelete in place, with a trashed parent (blocked and allowed variants, admin override), epoch bump, with unfolded `note_updates` (refused); duplicate (restrictions copied, permissions copied, attachments new rows and new bytes, body links rewritten, children not copied, typed page refused, guest refused); the AI read scope cannot call the write commands; events and `deleted_by` written; two concurrent deletes of overlapping subtrees (no page gets two deletions); two concurrent moves into each other (one refused as a cycle).
- **QA**: `TestQARLSSweep`, the foreign-key tests and the module boundary test stay green (no `notes -> files` import).
- **Gateway**: as listed under `note.delete`.
- **e2e**: drag to move and reorder, the keyboard alternative, context menu, delete then restore from Trash with sub-pages, restore blocked with the reason, duplicate with an image, export downloads and the print view.

### 7.10 A2 binding revisions (design review of 7.9)

This section overrides 7.9 wherever they disagree. The numbers in brackets point to the review findings.

1. **One structural lock (review 1, 9).** Every structural writer takes `pg_advisory_xact_lock(hashtextextended('notes.tree:' || space_id::text, 0))` first, then works out the subtree, ancestry, permissions, cycle check and positions under that lock.
   - Structural writers are: create with a parent, `note.update` changing `parent_id`, move, reorder, delete, undelete and duplicate.
   - Lock order: the space lock first, then note rows by `id`.
   - The insert trigger that assigns `position` runs under the same lock, because the create path takes it before inserting.
   - Tests: opposing moves (A under B, B under A) leave no cycle; an insert into a subtree racing a delete of that subtree; concurrent reorders keep positions unique.
2. **Parent changes only through move (review 2).** `note.update` with a `parent_id` change calls the move implementation, and also requires everything `note.move` requires: a person at Member or above, never a guest, and never the read-only AI scope. Otherwise it is refused. Direct `parent_id` writes are removed from `write.go`. Tests go through `note.update` for a guest, an API key and an AI employee.
3. **Duplicate permissions (review 3, 8).**
   - If the source has its own or an inherited restriction, the caller must be able to manage that restriction (owner of the restricting page, or a space or workspace admin). Without a restriction, view access is enough.
   - Permission rows are copied server side and never returned.
   - Attachments: the source is checked with the file **read** guard and the destination with the **write** guard. Object copies use the Tables `core.file_objects` intent lifecycle (write the intent, copy the object, mark it live in the record transaction; the sweep collects intents left by rollbacks).
4. **Coordinated fold before delete and duplicate (review 4, 5, 7).** The API calls a new internal gateway endpoint, `POST /internal/fold`, with `{note_id, fence: true|false}`.
   - The gateway drains in-flight frames for that note, folds the snapshot plus every committed log update for the current epoch into `content_md` and the snapshot (using the existing render path, so it works with no open room), and returns the folded version.
   - With `fence: true`, it first marks the room closed to writes, so no update can be accepted after the fold.
   - Delete calls fold with the fence for each page in the subtree that has pending updates, then commits the deletion with a revision checkpoint taken from the folded content.
   - If the fold fails or the gateway is unreachable while updates are pending, delete is refused with "Couldn't save the latest edits. Try again." Nothing is stranded.
   - Duplicate calls fold without the fence and copies the folded content.
   - Undelete is allowed only when no pending updates exist for the page, which always holds after a successful fenced fold. It bumps the epoch.
   - Tests: an owner crash between edit and delete, a failed fold, no open room, and a restart.
5. **Restore keeps restriction evidence (review 6).**
   - At deletion, store per deleted page `deleted_parent_id` and `deleted_restricted_by uuid[]`: the ancestors whose restrictions applied at that time.
   - Restore in place is allowed only if the current inherited restriction set contains the stored one. Otherwise it is treated as a move that releases a restriction, and needs that authority.
   - If the parent or an ancestor has been purged, or the evidence is missing (pages deleted before A2), restore goes to the space root only for a space or workspace admin. Otherwise it is refused.
6. **Positions (review 9).**
   - `position` is a bigint with gaps of 1024.
   - Positions are renumbered when a gap is smaller than 2, under the structural lock.
   - `before_id == id` is a no-op.
   - An empty sibling set starts at 1024.
7. **Staged migration (review 10).**
   - 1334 only adds nullable columns and the new tables. It adds no constraint and no backfill, and starts with `SET lock_timeout = '5s'`.
   - The application writes the new columns from this release on.
   - A worker sweep fills `deletion_id` for already-deleted rows in batches of 500.
   - The consistency constraint is added `NOT VALID` in a later migration in this same release only if every writer sets the columns. `VALIDATE CONSTRAINT` comes in a following release. Write that follow-up release down in docs/deploy.md.
8. **Tree depth (review 11).**
   - Structural writes refuse a result deeper than 50 levels.
   - Both access functions treat a chain longer than 50, or a cycle, as **restricted** (fail closed). Make this change in a new migration that recreates the functions.
   - Check existing data for deeper trees in a test fixture and say so.
9. **Trash paging (review 12).** The cursor is `(deleted_at, deletion_id)`. The server scans in batches until it has the requested number of entries the caller can restore, or the input is exhausted. Filtered entries are never counted or revealed.
