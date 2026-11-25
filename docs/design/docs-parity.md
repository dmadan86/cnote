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
