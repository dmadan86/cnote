# Docs parity, Phase C "Depth and polish": build design (25 Nov 2026)

This is the build design for Phase C of `docs/design/docs-parity.md` (section 4). It is written against `feat/docs-c`, which is main with Phases A1, A2, B1 and B2 merged (last migration 1344).

Section 7 of the parity plan and sections 13 and 14 of `docs-parity-b.md` stay binding. "7.x" means the parity plan, "B 13.x" means the Phase B design, "Tables 18.x" means `docs/design/tables.md`.

## 1. Summary

Phase C ships in two releases.

| Release | Contents | Schema change | Mobile release needed first |
|---|---|---|---|
| **C1** | Highlight; table cell colour, numbered column, sticky header; cover image; lazy page tree; find and replace; trash purge with retention | None. `DOC_SCHEMA` stays 2 | No |
| **C2** | Layout columns; embeds (YouTube, Google Docs/Sheets/Slides, Figma); link cards; content-by-label; whole-space export; branded PDF; space starters | One bump, `DOC_SCHEMA` 2 to 3, for four new node types | Yes, one line (4.2) |

Seven choices keep the phase small. Each is explained in its item.

| Choice | Effect |
|---|---|
| Highlight is an attribute on the existing `textStyle` mark, and table colour and numbering are attributes on the existing table nodes | No new node or mark in C1, so no bump. A two-client run backs this (4.1) |
| Sticky header is always on and is not stored | No attribute, no converter change |
| An embed and a link card are one node, stored as an ordinary Markdown link with a title | Old clients, mobile and exported Markdown show a working link |
| A link card keeps its title and description in the document | Readers, public pages and exports never fetch anything. The server fetches once, when the card is inserted |
| Purge is `DELETE FROM core.notes` plus the Tables file queue | Every dependent table already cascades. Objects are removed by the existing sweep, with retries |
| Whole-space export is a Markdown zip built by a job | No server HTML or PDF renderer |
| Branded PDF stays on the browser's print | No headless browser in the image |

## 2. What the code says today, where it matters

| Area | Finding | Evidence | Consequence |
|---|---|---|---|
| Mobile and the schema number | The app sends the literal `schema: 2`, not the shared constant | `apps/mobile/src/components/pages.tsx:117` | The mobile-first step is a one-line change. The API accepts a higher number than its own (`*schema < DocSchema`), so the app can send 3 before the bump |
| Unknown attributes | A client that does not know an attribute on a node or mark it does know deletes nothing | Run on this branch, 4.1 | C1 needs no bump |
| Text colour | `ColorText` extends `TextStyle` and writes `<span style="color: #hex">`. The gateway models marks as one field each (`Marks.color`) | `editor/formats.ts`, `apps/collab/src/md.ts:10` | Highlight is one more field beside `color` |
| Tables | A table with a merged cell or a column width is stored as an HTML `<table>`; otherwise as a pipe table | `formats.ts` `HtmlTable`, `md.ts` `tableHtml` | Cell colour and numbering reuse the HTML form |
| Sidebar tree | The shell loads `note.list {limit: 500}` once: the 500 most recently updated notes of every type, each with its full `content_md` | `shell.tsx:423`, `notes/write.go:450` | A workspace past 500 loses pages from the tree, and the payload is large |
| Shell page list | Five places read the shell's `notes`: page breadcrumbs and sub-page count, the space page, Home, the docs dashboard, the move dialog | `useShell()` callers | The lazy tree must give each one its own source |
| Hard delete | Nothing hard-deletes `core.notes`. Every foreign key that points at it has `ON DELETE CASCADE` or `SET NULL` (48 found, none without) | `grep -ri "references core.notes" migrations` | Purge is one `DELETE`; a test pins the rule |
| Audit and purge | `core.events.note_id` is `ON DELETE SET NULL` | `migrations/0001_core.sql:155` | Events survive a purge but lose the page id, so the purge event carries the ids in its payload |
| File objects | `core.file_gc` and `files.RecordSweep` delete objects with retries. The trigger that fills the queue fires only for Tables files | `migrations/1326`, `files/record.go:604` | Purge widens the trigger to page files |
| Trash | `note.trash` and `note.undelete` handle plain pages only (`types = '{}'`) | 7.9 decision 2 | Purge never touches a typed page (invoice, expense, item) |
| App CSP | App routes send no `Content-Security-Policy`. Only published sites do | `apps/web/src/proxy.ts`, `next.config.ts` | C2 adds a `frame-src` header for app and public page routes |
| Site builder embeds | A provider plus an id, validated by shape; the frame URL is built from the id; the catalog feeds the CSP. It fetches nothing | `sites/phase2_helpers.go` `EmbedSrc`, `sites/catalog.json` | Page embeds follow the same pattern. There is no link preview code to reuse |
| SSRF guard | `netguard` checks every resolved address, re-checks the connected address at dial time and follows no redirect. It has a switch that allows private addresses | `apps/api/internal/netguard/netguard.go` | Link preview reuses the dialer and must ignore that switch |
| Export | One page, built in the browser from the live editor. No server renderer and no PDF library in `go.mod` | `components/docs/export.ts` | Space export is Markdown; PDF stays in the browser |
| Jobs | `core.jobs` handlers run as the system principal with role Owner | `worker/worker.go:334` | The export job must rebuild the requester's own access |
| Object store | `S3.Put` takes the whole object as bytes; without S3 the bytes live in Postgres | `files/s3.go`, `files/files.go` | The export zip is stored in 32 MB parts |
| Space templates | `core.spaces.template` picks a module layout (docs, software, crm). Page templates are code (`docs/builtin.go`) | | Starters are code too |
| Labels | `search.notes` takes a `label` filter with the access predicate in the query and needs no search text | `notes/search.go:57` | Content-by-label needs no new command |

## 3. Rules for every Phase C package

1. **Schema (7.3).** C1 adds no node and no mark. C2 adds exactly the four nodes in 4.2. A builder who needs another stops and asks the lead.
2. **Fixtures (7.4).** Every stored form here has a fixture in `apps/collab/test/fixtures/blocks.json`. The lead owns the file. Every converter in 4.3 must round-trip it.
3. **Validated values only.** Colour names, layouts and embed URLs are checked against fixed lists in both converters. Anything else becomes plain text or a plain link. No free-form attribute reaches stored Markdown.
4. **Access inside queries (7.6).** Tree rows, child markers, label lists and export page sets apply the full predicate (`core.note_access`, readable spaces) in the query, before counting or paging.
5. **Structural lock (7.10.1).** Purge takes `pg_advisory_xact_lock(hashtextextended('notes.tree:' || space_id::text, 0))` first, like every other structural writer.
6. **The generic refusal (7.9).** A subtree with a page the caller cannot see or edit answers `Some pages here are restricted`.
7. **Staged migrations (7.10.7).** `SET LOCAL lock_timeout = '5s'`, nullable columns and new tables only, changed checks `NOT VALID`, indexes `CONCURRENTLY` in their own file, `VALIDATE` in the next release and written in `docs/deploy.md`.
8. **Archived is read-only (B 13.2.2).** Cover, purge of an archived space's trash and starters go through `AssertNoteAccess(write)` or `AssertSpaceAccess(write)`. Find stays available on an archived page; Replace does not.
9. **Guests** never run a command that acts on a space, on other pages or on the outside world.
10. **AI scope.** The read-only AI scope holds group `read` only. Three commands here are also refused for every agent and API key, whatever their groups: `note.purge`, `link.preview`, `space.export_start`.
11. **Module boundaries.** `notes` does not import `files` or `publiclinks`; cross-module work goes through `c.Call`.
12. **Shared files stay with the lead:** migrations, `notes/notes.go`, `notes/write.go`, `command/*`, `access/*`, `apps/collab/src/server.ts`, `doc-schema.ts`, the fixtures, `worker/wire.go`, `server/server.go`, `netguard/*`, `next.config.ts`, `lib/api.ts`, `lib/docs.ts`, `e2e/helpers.ts`. `apps/collab/src/md.ts` has one owner per release (R1 in C1, S1 in C2).

## 4. The schema plan

### 4.1 C1: attributes, no bump

7.3 showed that a tab without a node or a mark deletes it for everyone. An attribute behaves differently.

**Observed** (25 Nov 2026, a throwaway two-client test on this branch, same harness and locked versions as `apps/web/src/test/unknown-node.test.ts`). Client A's `textStyle` mark has a `highlight` attribute and its paragraph has a `bg` attribute. Client B's have neither.

| Step | Result |
|---|---|
| B opens the page | Nothing is deleted. B shows the text without the formatting. The shared document keeps both attributes |
| B types in another block | Both attributes survive |
| B types in the block that carries them | Both attributes are removed from that block for everyone. The text stays |

Not run: a table cell. An attribute on the cell being edited, and on the table around it, is expected to be removed the same way. Step 0 commits the test with that case added.

**Consequence.** A tab that was open before the C1 deploy can remove a highlight or a cell colour from a block it edits. It can never remove text. That is acceptable without a fence. The window ends when the tab reloads, and C2's bump reloads every tab.

**Fallback.** If the committed test shows a deletion in the table case, table polish moves to C2. Nothing else changes.

Deploy order for C1: gateway, API, web, as in A1.

### 4.2 C2: one bump, 2 to 3

| New node | Kind | Stored as | A client that does not know it shows |
|---|---|---|---|
| `columns` | block, holds 2 or 3 `column` | `<div data-columns="2">` wrapper lines (6.1) | The tags as text around normal content |
| `column` | block, holds `block+` | `<div data-column>` wrapper lines | Same |
| `embed` | atom, attrs `url`, `view`, `title`, `description` | A line holding one link with the title `"embed"` or `"card"` (6.2, 6.3) | A working link |
| `labelList` | atom, attr `label` | A line holding only `[[pages:<label>]]` (6.4) | That text |

No new mark.

**Mobile first.**

1. **Mobile release M** changes the literal to `schema: 3`. The API accepts it today. Ship M while C1 is being built. The app edits raw Markdown, so it keeps the new forms untouched and needs nothing else.
2. **Wait.** C2 does not deploy until the owner's cut-off is met (section 13, question 2). An app older than M can still read; a save answers 426 and the app already says "Update the app to edit; you can still read."
3. **Deploy C2** in one change window: gateway (reads and writes the new forms, minimum still 2), API (`DocSchema = 3`), web (`DOC_SCHEMA = 3`), then raise `COLLAB_MIN_SCHEMA` to 3. Raising it closes every open socket with 4410 and old tabs show "Reload to keep editing".
4. Block insertion follows the gateway's minimum, as today.

Step 0 changes in the lead's files:

- `blocksEnabled()` takes a level. A1 blocks need 2 and C2 blocks need 3, so panels stay insertable during the few minutes between the web deploy and the raised minimum.
- `notes/schema_test.go` also reads the mobile literal and fails if it is below `DocSchema`.

### 4.3 Converters and fixtures

Every one of these reads the fixture file and must pass:

| Converter | File |
|---|---|
| Browser Markdown | `editor/formats.ts`, `editor/blocks.ts`, new node files |
| Inline HTML for aligned blocks and HTML tables | `editor/html-inline.ts` |
| Gateway Markdown, including its table path | `apps/collab/src/md.ts` |
| Plain text for search, snippets and AI | `notes/plaintext.go` |
| Confluence import | `importers/html.go` |
| Public body | `publiclinks/pagebody.go` and registered passes |
| Page export | `components/docs/export.ts` |
| Built-in templates and starters | `docs/builtin.go`, `docs/starters.go` through `builtin-templates.test.ts` |

New fixtures (Tabnine drafts them, the lead merges):

| Release | Fixture ids |
|---|---|
| C1 | `highlight-each-color`, `highlight-with-marks` (bold, link, text colour, status beside it), `highlight-in-html-table`, `highlight-lookalike` (bare `<mark>`, an unknown colour, `==text==`, `<mark>` inside code), `table-cell-color`, `table-cell-color-merged`, `table-numbered`, `table-attr-lookalike` (unknown `data-bg`, `data-numbered` on a cell) |
| C2 | `columns-two`, `columns-three`, `columns-sidebar`, `columns-with-blocks` (panel, list, table, image inside), `columns-lookalike` (the tags in a paragraph and in a code fence, columns inside a column), `embed-each-provider`, `embed-refused` (title `"embed"` on a URL off the list), `link-card`, `link-card-escapes` (quotes and brackets in title and description), `label-list`, `label-list-lookalike` |

## 5. Release C1

### 5.1 Highlight

**Today.** Text colour only. No background colour.

**Design.** Seven named tints: yellow, green, blue, purple, red, orange, grey. Names map to theme tokens, so a highlight stays readable in dark mode. A free hex would not.

**Left out:** custom colours, highlight inside inline code.

**Stored form.** `<mark data-color="yellow">text</mark>`. A bare `<mark>` reads as yellow, so Markdown from elsewhere works; the serializer always writes the attribute. An unknown colour drops the tag and keeps the text.

- Editor: attribute `highlight` on the existing `textStyle` mark (`ColorText` in `formats.ts`). Not TipTap's `Highlight` extension, which is a new mark.
- Gateway: `Marks.highlight: string | null`; `markAttrs` writes `textStyle: {color, highlight}`.
- Nesting, outer to inner: status, date, link, bold, italic, strike, code, `<mark>`, colour `<span>`.

**Data, commands, access.** None. It is page content, saved by `note.update` and the gateway.

**Web.** The toolbar's text colour button gets a second row, "Highlight", with the seven tints and "None". Shortcut `Ctrl/Cmd+Shift+H` applies the last tint. References: [Productboard block menu with separate Text color and Background color lists](https://mobbin.com/screens/643385a6-0948-4475-9cf0-e334cf532129?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Gamma colour popover with a text tab and a highlighter tab](https://mobbin.com/screens/e4d6e3d7-1545-4a9f-afae-97e47133ece5?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp).

**Public page and export.** The public renderer shows it. Page export inlines the colour as a `style`, as it does for status chips.

**Mobile.** The raw editor shows the `<mark>` tags around the text and keeps them.

**Tests.** The fixtures in every converter; toolbar apply, change and clear; the committed two-client test (4.1); an e2e round trip through a co-edit.

### 5.2 Table cell colour, numbered column, sticky header

**Today.** Size picker, row and column moves, sort, merge, header row, resize. No colour, numbering or sticky header.

**Design.**

| Feature | How | Stored |
|---|---|---|
| Cell colour | Attribute `background` on `tableCell` and `tableHeader`, same seven tints | `<td data-bg="yellow">` |
| Numbered column | Attribute `numbered` on `table`. Numbers are drawn by a CSS counter in a gutter beside the first cell, body rows only. There are no real cells, so sort, move and paste never renumber data | `<table data-numbered>` |
| Sticky header | A small view plugin moves the header row with the scroll while its table crosses the top of the page. Always on. Off when a header cell spans rows (Confluence has the same limit) | Not stored |

A table with a coloured cell or numbering is stored in the HTML table form, as a merged table is today. `isMerged` in `formats.ts` becomes "needs HTML".

`shortcut: sticky header has no per-table switch; add a table attribute in a later bump if someone asks to turn it off.`

**Left out:** header column, distribute columns, table width and alignment, chart from a table.

**Data, commands, access.** None.

**Web.** The cell menu in `table-controls.tsx` gains "Cell colour" (tints and "None"; it applies to the selected cells) and the table menu gains a "Numbered" toggle. Sort and move stay disabled only for merged tables.

**Public page and export.** Both show colour and numbers. The exported HTML carries the counter rule in its own stylesheet. Sticky is screen only.

**Mobile.** A coloured or numbered table appears as HTML in the raw editor, as merged tables do today.

**Tests.** Fixtures in every converter, including a merged table with colour; colour survives sort and row move; numbering skips the header row; sticky engages past the header and is off for a row-spanning header; the table case of the two-client test.

### 5.3 Cover image

**Today.** Title emoji and wide page exist (`notes/page_chrome.go`). No cover.

**Design.** A cover is an image already uploaded to the page with `file.upload`, plus a vertical position. It is page chrome, not content, like the emoji.

**Left out:** Unsplash or any gallery, cropping, a cover in the tree or on cards, a cover in exports.

**Data (1345).** `core.notes.cover_file_id uuid` and `cover_y smallint`, both nullable. A `CHECK (cover_y BETWEEN 0 AND 100)` added `NOT VALID`.

`shortcut: no foreign key to core.attachments; a cover whose file was deleted does not render. Add the key if dangling ids ever matter.`

**Commands.** `note.update` gains `cover: {file_id, y?} | null`, parsed in `page_chrome.go` beside `icon`. Checks: the file is attached to this page and is one of the four image types; `y` is 0 to 100, default 50. Group, role and guest rule are those of `note.update` today. `note.get` and the tree do not change shape beyond the two fields on `Note`.

`note.duplicate` maps the cover through the `{from_id, to_id}` pairs that `file.copy_for_note` already returns.

**Access.** Edit access on the page to set or clear. Anyone who can read the page sees it. The cover counts against the page's 50 files.

**Public page.** `PublicPage` gains `cover_url` and `cover_y`; the golden key test is updated. The public file route today serves a file only when its id is in the body; it also accepts the page's `cover_file_id`. Nothing else about that route changes.

**Web.** New `components/docs/cover.tsx`, mounted by `page-header.tsx`. With no cover, "Add cover" appears beside "Add icon" on hover. With a cover: a band 200 px high (280 on a wide page), `object-fit: cover`, and on hover "Change", "Reposition", "Remove". Reposition is a vertical drag with "Save position" and "Cancel". Upload reuses `uploadImage` from `blocks.ts`. References: [Notion cover with "Drag image to reposition", Save position, Cancel](https://mobbin.com/screens/bfddb457-f6ba-4160-811a-dbc93713e20e?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion cover menu with Change, Reposition, Remove](https://mobbin.com/screens/cf5c2543-9ace-4b28-8a6a-80eea1d04d43?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp). We take the three actions and the drag, not the gallery.

**Mobile.** The app ignores the two fields.

**Tests.** Set, reposition, clear; a file of another page is refused; a PDF is refused; a view-only reader cannot set; an archived page cannot set; duplicate carries the cover; the public page serves the cover and still refuses an attachment that is in neither the body nor the cover.

### 5.4 Lazy page tree

**Today.** One `note.list {limit: 500}` call with full bodies. Pages past 500 are missing from the sidebar, the move dialog and the breadcrumbs.

**Design.** The tree loads one level at a time.

**Command `note.tree`** (new, `notes/tree.go`). Group `read`; role and guest rule as `note.list`; in the read-only AI scope.

| Input | Output |
|---|---|
| `{space_id, parent_id: uuid \| null, after?, limit?}` (limit default 100, max 200) | `{rows: TreeRow[], next}` |
| `{reveal: note_id}` | `{levels: [{parent_id, rows, next}]}`: the sibling level of each ancestor, root first, each cut at 200 rows around the path |

`TreeRow` is `{id, space_id, parent_id, title, icon, position, status, has_children}`. No body.

Rules:

- Plain, live, non-archived pages, with the access predicate in the query.
- Order `position, created_at, id`. The cursor is that triple (keyset), so a reorder during paging neither repeats nor skips a row that did not move.
- `has_children` is an `EXISTS` over children that pass the same predicate. A page whose only children are hidden shows no arrow.
- The root level is pages with no parent, plus live pages whose parent is in the trash or archived. That is the rule `tree-model.ts` applies in the browser today. The second group is found from the trashed and archived parents of the space, not by scanning every page.
- With `parent_id` from a space the caller cannot read (a shared page), the level is still served, as `note.list` does today.
- The existing index `notes_siblings (space_id, parent_id, position)` serves it. No migration.

**Left out:** virtual scrolling, a "search by title" box in the tree (the command palette and the page link picker already search titles), a count of all descendants.

**Web.** `shell.tsx` keeps `notes` in its context, but it now holds only loaded `TreeRow`s.

| Reader of the shell list today | After |
|---|---|
| Sidebar tree | Loads a space's root level when the space is open, a page's level when its arrow is opened, and `reveal` for the open page. A level with more rows ends in "Show more" |
| Breadcrumbs | From the `reveal` result |
| Shared with me | Roots from `note.shared_with_me`, children through `note.tree` when opened. The four-level preload goes away |
| Sub-page count in the delete dialog | "This page and its sub-pages" when `has_children`; the exact count comes back from `note.delete`, as today |
| Space page (`s/[id]`) | `note.tree` root level with "Show more" |
| Home and the docs dashboard | Their own commands (`note.mine`, `search.notes`), which already return titles |
| Move dialog | The same level loader inside the dialog, plus title search with `search.notes {title_only}` |
| Drag and drop | Works inside loaded levels. A drop on a closed page puts the page last in it |

A tree change (`TREE_EVENT`) reloads the open levels only. Reference: [Confluence sidebar content tree](https://mobbin.com/screens/f3d6c865-feec-4563-839f-1162b0aa8fdc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) (from the research file).

**Mobile.** None. The app keeps `note.list {limit: 200}`.

**Tests.** API: a space with 1,200 pages pages through without a repeat or a gap, including a reorder between two calls; a restricted child is absent and its parent has no arrow; a page under a trashed parent is at the root; `reveal` on a page 30 levels deep; a guest; another workspace. Web: open a deep link and the tree opens to it; "Show more"; move into a page that was never opened. e2e `tree-large.spec.ts` with 600 pages.

### 5.5 Find and replace

**Today.** The browser's own find, which cannot replace and misses collapsed expands.

**Design.** A client-only editor extension, `editor/find-replace.ts`, about 120 lines. No dependency, no command, no stored form.

- State: the query, a case switch, the matches, the current index. Matches are drawn as decorations.
- A match lies inside one text block and may cross marks. Atoms are skipped. Text inside a date chip is found but not replaced, because the chip's text must agree with its date.
- Replace keeps the marks of the replaced text. "Replace all" is one transaction: one undo step, and one ordinary update while co-editing.
- A match inside a collapsed expand opens it.
- Replace is hidden when the page is not editable (view access, archived, the read-only public page). Find still works.

**Left out:** regular expressions, whole-word, replace across pages.

**Web.** `Ctrl/Cmd+F` with focus in the editor opens a small bar at the top right of the page: Find field with "2 of 7", previous and next, a "Match case" box, and a Replace row with "Replace" and "Replace all". `Enter` is next, `Shift+Enter` previous, `Esc` closes and returns focus. Pressing `Ctrl/Cmd+F` again while the bar is open lets the browser's own find through. The page "..." menu gets "Find and replace". References: [Evernote "Find in note" panel with count, Replace with, Match case, Replace and Replace all](https://mobbin.com/screens/5daa0a1a-b42f-4abd-8d3c-2cbd13dd3aa2?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Craft find and replace with a result list](https://mobbin.com/screens/5f37df10-fd50-42cc-be3c-34c0ecb5b8fe?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp). We take the Evernote shape, without the list.

An inline comment anchored to replaced text loses its anchor, as it does after any edit to that text.

**Mobile.** None.

**Tests.** Unit: matches across bold and link runs, case switch, a match in a table cell, a code block and a collapsed expand, replace keeps marks, replace all is one undo step, the date chip is skipped. e2e: find, step, replace one, replace all, read-only page shows Find only.

### 5.6 Trash purge and retention

**Today.** Trash and restore exist (7.9, 7.10). Nothing is ever removed. 7.5 deferred purge to here.

**Design.** Pages in the trash are deleted for good after a retention period. A workspace or space admin can also delete one trash entry at once.

**The unit** is one `deletion_id`: the set of pages one delete took, which is what Trash lists and restores.

**What is never purged.**

- A typed page (`types <> '{}'`). Invoices, bills, expenses and items are business records. A deletion that holds one is skipped whole.
- A page with no `deletion_id` (deleted before A2 and not yet backfilled).
- A page with edits still in `core.note_updates` for its current epoch. The fenced fold at delete time normally leaves none; the sweep tries again the next day.

**Retention.**

| Setting | Value |
|---|---|
| Default | 30 days, the same as Tables |
| Choices | 30, 90, 365 days, or never |
| Where | Workspace setting `trash_retention_days`, set with the existing `workspace.update` (group `admin`, role Admin). 0 means never |
| Clock | A deletion is due when `greatest(deleted_at, workspaces.trash_purge_from) + retention < now()` |

`trash_purge_from` is what stops the first sweep from destroying everything older than 30 days on release day. Migration 1345 sets it to `now()` for every existing workspace. `workspace.update` sets it to `now()` again whenever the retention changes. So nothing is ever removed sooner than one full period after the rule that removes it took effect.

**Who can purge.**

| Path | Who | Checks |
|---|---|---|
| Retention sweep | The system | The rules above |
| `note.purge {deletion_id}` | A person who is a workspace admin, or an admin of that deletion's space | The deletion must be one the caller could list in Trash (edit on every page as it was, 7.9). A space admin's role grants no page access (B 13.2.1), so a restricted deletion stays out of their reach. A deletion the caller cannot list answers `not_found` |

`note.purge`: group `destructive`, `Confirm: true`, people only. An agent or API key is refused by the gate and logged as `command.denied`. Guests never. There is no "Empty trash" command; retention does that.

**What one purge does** (one function, `purgeDeletion`, used by the command and the sweep; one transaction per deletion):

1. Take the space's structural lock. A restore of the same deletion takes the same lock, so one of them wins cleanly.
2. Lock the deletion's rows `FOR UPDATE` in `id` order. Re-check that every page is still in the trash and plain. If not, stop.
3. Count pages, files, bytes and public links for the audit event.
4. `DELETE FROM core.notes WHERE id = ANY(...)`. The cascades remove revisions, logged updates, comments and reactions, labels, stars, watches, views, permissions, shares, notifications, the task and mention indexes, search and AI rows, attachments rows and **public links**. A page link stopped serving when the page went to the trash (`core.resolve_page_link` needs a live page); the row goes now.
5. A live child of a purged page (possible only for deletes older than A2) gets `parent_id` null through the existing foreign key and shows at the space root.
6. Emit one `note.purged` event.

**File objects.** Migration 1345 recreates the trigger `attachments_gc` so it also fires for page files in object storage. Each cascaded attachment row puts its key into `core.file_gc`. `files.RecordSweep` (hourly, exists) deletes the object, treats "already gone" as done, retries with a growing delay, logs from the tenth failure and never drops a row. Without object storage the bytes are in the deleted row. `file.delete` no longer deletes its object inline (13.3.1): it queues the key through the same trigger and the sweep removes the object.

**Audit.** `core.events` is the audit trail. Because `events.note_id` is nulled by the purge, the `note.purged` event carries everything in its payload:

```json
{"deletion_id": "", "space_id": "", "root_id": "", "root_title": "", "page_ids": [], "count": 0,
 "files": 0, "bytes": 0, "public_links": 0, "reason": "retention | manual", "retention_days": 30,
 "deleted_at": "", "deleted_by": ""}
```

The actor is the admin for a manual purge and the system for retention. Earlier events for those pages keep `deletion_id` and `root_id` in their payloads, so the history of a purged page can still be followed. The root title stays in the audit log on purpose: it answers "what was removed". A retention change is already audited as `workspace.settings_updated`.

**The sweep.** `docs.trash_purge`, daily, registered from `worker/trashpurge.go`. Per workspace: up to 200 due deletions a run, oldest first, each in its own transaction with `lock_timeout = '5s'`. A deletion that fails or times out is logged and tried again the next day. A large trash drains over several days, as in Tables.

**Restore after a purge.** Already decided in 7.10.5: if a parent or ancestor was purged, restore goes to the space root for a space or workspace admin only.

**Irreversible means:** the only undo is a database backup, and the content stays in backups until they rotate. `docs/deploy.md` says both.

**Data (1345).** `core.workspaces.trash_purge_from timestamptz` (nullable; set for existing rows in the migration, one small table); the recreated trigger. No new index: the sweep reads through `notes_deletion`.

**Commands.** `note.purge` as above. `note.trash` rows gain `purge_at` (null when retention is "never") and `can_purge`.

**Web.**

- Trash page: a banner, "Pages in the trash are deleted for good after 30 days." Each row shows "Deleted for good in 12 days". An admin's row menu has "Delete forever", with a dialog that names the root page and the count, says it cannot be undone and needs the word DELETE typed.
- Settings, Docs: "Keep deleted pages for" with the four choices, and one sentence on what a change does to pages already in the trash.

References: [Rows trash with a "deleted after 30 days" banner](https://mobbin.com/screens/324fa198-1594-4423-b1c9-1fb8636ca52f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Squarespace deleted pages with the retention sentence and a permanent-delete confirm](https://mobbin.com/screens/2d1a87bd-df2e-485a-a71b-e620c026864f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp).

**Mobile.** None. A purged page opened from a stale list is `not_found`, which the app already shows.

**Tests.**

- A deletion with sub-pages, files, revisions, comments, a public link, labels and tasks is purged: no row with those page ids is left in any table.
- A schema test: every foreign key that references `core.notes` cascades or sets null, and every column named `note_id` or `*_note_id` has such a key or is on a short allow-list.
- Object storage (the fake S3 of `files_s3_test.go`): the keys are queued, the sweep deletes them, a failing delete is retried and kept.
- Not purged: a typed page, a deletion with one, a deletion with unfolded edits, anything before `trash_purge_from` plus the period, retention "never".
- Changing the retention resets the clock.
- Purge racing restore of the same deletion: exactly one succeeds.
- `note.purge` refused for a member, a guest, an agent, an API key, a space admin on a restricted deletion (as `not_found`), an archived space's write gate.
- The event payload; earlier events keep their payloads with `note_id` null.
- A page link token answers the one 404 before and after the purge.
- Restore of a child whose parent was purged follows 7.10.5.

## 6. Release C2

### 6.1 Layout columns

**Today.** One column of blocks. The Confluence importer flattens `ac:layout`.

**Design.** A `columns` block with a fixed layout: two equal, three equal, narrow left with wide right, wide left with narrow right. Widths are CSS; nothing is dragged.

**Left out:** four and five columns, dragging a block beside another to make a column, resizing by hand, columns inside a column.

**Stored form.**

```
<div data-columns="2">
<div data-column>

Left **content**

</div>
<div data-column>

Right content

</div>
</div>
```

- `data-columns` is `2`, `3`, `1-2` or `2-1`. Anything else is not a columns block.
- Each tag is alone on its line, with a blank line between a tag and Markdown blocks, as `<details>` is written today.
- The gateway reads it with a `columnsAt` beside `expandAt` and parses each column with the block parser.
- A column holds any block except columns. Columns found inside a column, a list or a table cell are flattened to their blocks by both converters. Columns inside a panel or an expand are allowed.
- Changing 3 to 2 moves the third column's blocks to the end of the second. Changing 2 to 3 adds an empty column.

**Data, commands, access.** None.

**Web.** Slash menu "Columns" with the four layouts as small icons. It is not offered inside a column or a table. A floating bar on the block switches layout and deletes the block. `Tab` at the end of a column does not jump columns; arrow keys do. Below 640 px the columns stack. Mobbin returned no usable screen for a column block in a document editor, so none is cited.

**Public page and export.** The public renderer shows columns. Print and exported HTML keep them side by side when the paper is wide enough and stack otherwise. Plain text reads the columns in order. The Confluence importer maps `ac:layout-section` with two or three cells to this block.

**Mobile.** The raw editor shows the `div` lines around normal Markdown.

**Tests.** The fixtures in every converter; layout change keeps content; nested columns flatten; a panel, a table and an image inside a column round-trip; stacking at 390 px.

### 6.2 Embeds

**Today.** None in pages. The site builder embeds by provider and id.

**Design.** An embed is a link the editor recognises. The document stores the provider's ordinary page URL. The frame URL is never stored: it is built at render time from the parts the allow-list extracted. A URL that matches no row is a plain link. No HTML and no `<iframe>` is ever accepted from a user, an import or the AI.

**Stored form.** One line, alone in its block:

```
[YouTube video](https://www.youtube.com/watch?v=dQw4w9WgXcQ "embed")
```

The link text is a caption (the provider's name by default). The title `"embed"` is the marker. Both converters check the URL against the allow-list when they read the line. If it does not match, the line is an ordinary paragraph with an ordinary link.

**Allow-list.** One file, `apps/collab/src/embed-providers.json`, read by the web and the gateway, with a copy under the Go module and a test that the two are equal (the site catalog does the same). Patterns use only syntax that JavaScript and Go's RE2 share.

| Provider | Stored URL (https only, no credentials, no port) | Frame URL |
|---|---|---|
| YouTube | `www.youtube.com/watch?v=<id>`, id `[A-Za-z0-9_-]{11}` | `https://www.youtube-nocookie.com/embed/<id>` |
| Google Docs | `docs.google.com/document/d/<id>`, id `[A-Za-z0-9_-]{25,80}` | `…/document/d/<id>/preview` |
| Google Sheets | `docs.google.com/spreadsheets/d/<id>` | `…/spreadsheets/d/<id>/preview` |
| Google Slides | `docs.google.com/presentation/d/<id>` | `…/presentation/d/<id>/embed` |
| Figma | `www.figma.com/(design\|file\|board\|proto)/<key>`, key `[A-Za-z0-9]{22,128}`, optional `node-id` of digits, `:` and `-` | `https://www.figma.com/embed?embed_host=cnote&url=<the stored URL, encoded>` |

On insert the editor normalises what people paste (`youtu.be/<id>`, `/shorts/<id>`, `/embed/<id>`, `m.youtube.com`, Google's `/edit`, `/view` and `?usp=sharing`) to the stored form and drops every other query and fragment. Step 0 checks each frame URL against the provider's current embed documentation; they were not fetched for this design.

Loom is one more row if the lead wants it; it is not in the plan's list.

**The frame.**

```html
<iframe src="…" title="YouTube video"
  sandbox="allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox allow-presentation"
  allow="fullscreen; picture-in-picture; encrypted-media"
  referrerpolicy="no-referrer" loading="lazy"></iframe>
```

- `allow-same-origin` lets the provider use its own cookies and storage. The frame is always another origin, so it cannot reach ours.
- No `allow-top-navigation`, no `allow-forms`, no `allow-modals`, no camera, microphone or location.
- `no-referrer` keeps our page URL, which holds workspace and page ids, away from the provider.
- Under the frame there is always "Open in YouTube" (or the provider), because a Google file the reader cannot open shows Google's sign-in screen inside the frame.
- Fixed sizes: 16:9 for YouTube, Slides and Figma, 600 px high for Docs and Sheets.

**CSP.** `next.config.ts` adds, for `/w/:path*` and `/pg/:path*`:

`Content-Security-Policy: frame-src 'self' <origins of the provider file> <origins of the site catalog>`

A header with only `frame-src` limits frames and nothing else. It is the backstop: if a converter bug ever let another URL into a frame, the browser refuses it. The site catalog's origins are included because the site editor previews its embeds inside the app. Step 0 greps every `<iframe`, `<object` and `<embed` in `apps/web/src` and runs the e2e suite with the header on before the packages start.

**Public pages.** The stored line is an https link, which the core body pass keeps. The public renderer shows an embed as a placeholder ("Load YouTube video") and loads the frame only on a click, so an anonymous reader is not reported to a third party by opening our link.

**Export.** Exported HTML and PDF show a bordered box with the caption and the URL. No frame: the file has no script and must work offline. Exported Markdown and the space zip keep the stored line.

**Elsewhere.** Plain text for search and AI is the caption. Mail carries no page body. An AI that writes the line with an allowed URL creates an embed, which is fine: the allow-list decides.

**Data, commands, access.** None.

**Web.** Pasting an allowed URL on an empty line inserts the embed and shows a small "Show as: Embed, Card, Link" menu. The slash menu has "Embed" with one URL field and the line "Works with YouTube, Google Docs, Sheets and Slides, and Figma". A URL that is not on the list offers Card and Link only. References: [Coda "Display as" URL, Embed, Card menu](https://mobbin.com/screens/0ddd1f91-69b6-4d6f-9949-3f1ac081e35c?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [GitBook "Embed a URL" dialog naming the supported providers](https://mobbin.com/screens/f1cccc31-036b-4042-8344-bd45897c73e6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp).

**Mobile.** The raw editor shows a normal Markdown link.

**Tests.** Tabnine's URL list (`apps/collab/test/fixtures/embed-urls.json`): accepted shapes per provider, and refused ones: other hosts, look-alike hosts (`youtube.com.evil.example`, `docs.google.com@evil.example`), `http`, a port, credentials, `javascript:` and `data:`, an over-long id, path traversal, extra query. The same list runs in the web, the gateway and Go. Also: a raw `<iframe>` in pasted HTML, imported HTML and AI-written Markdown never becomes a frame; the frame carries exactly the sandbox and referrer attributes above; the CSP header is present and names exactly the listed origins; the public page loads no frame before the click; the export has no frame.

### 6.3 Link cards

**Today.** A link is text.

**Design.** A card shows a title, one line of description and the site's host. All three are stored in the document. The server fetches the page once, when the card is inserted, to fill them in. Readers never cause a fetch.

**Left out:** thumbnails and favicons (they would need an image proxy, or they leak each reader's address to the linked site), refreshing a card, cards for pages behind a login.

**Stored form.** The same node as an embed, with view `card`:

```
[Annual filing checklist](https://example.com/filing "card: What to file and when")
```

`"card"` alone means no description. The URL may be `http` or `https`. `\` and `"` are escaped in the title, and `\`, `[` and `]` in the text, as image lines do today. A client that does not know the form shows a link with a tooltip.

**Command `link.preview`** (new module `linkpreview`).

| | |
|---|---|
| Input | `{url}` |
| Output | `{title, description, site}`; all empty when there is no preview |
| Group, role | `write_notes`, Member |
| Principal | People only. Agents and API keys are refused: an agent that can read pages and make our server fetch a URL of its choosing could send page content out in that URL |
| Guests | No |
| Limits | 20 a minute per person, 300 an hour per workspace |

The SSRF rules are in 9.2. If the fetch fails for any reason the card is still inserted, with the host as its title, and the person can type a title.

**Data.** None. The cache is in process: 1,000 entries, one hour for a result, ten minutes for a failure, keyed by the normalised URL. `shortcut: per-process cache; move to a table if preview traffic matters.`

**Public pages and export.** A card is static text and a link. Both show it as it is. External links on a public page already open with `rel="noopener noreferrer nofollow ugc"`.

**Web.** "Card" in the paste menu and the slash menu. The card is a bordered row: title in bold, description in muted text, host below. Its menu has "Edit title and description", "Show as link" and "Remove".

**Mobile.** A normal Markdown link.

**Tests.** Fixtures; the SSRF list in 9.2; an agent and an API key are refused; the rate limits; a failed fetch still inserts a card; HTML in a fetched title is stored as text and rendered as text.

### 6.4 Content-by-label

**Today.** Labels and a label filter in search exist (A1). No list inside a page.

**Design.** A block that lists the pages carrying one label. It is drawn by a node view that calls the existing `search.notes {label, limit: 50}`, so each reader sees only the pages they can read.

**Left out:** several labels, a space filter, sort options, more than 50 rows (the block ends in "See all", a link to search with the label filter).

**Stored form.** A line holding only `[[pages:policy]]`. The label must pass the existing label rule and hold no square bracket. A literal `[[pages:x]]` typed in a paragraph is escaped, as `[[toc]]` is.

The form holds no workspace id, so a starter (6.7) can carry it.

**Data, commands.** None new.

**Access.** The predicate is in `search.notes`. A reader with no matching page sees "No pages with this label yet".

**Public pages.** A public reader must not see workspace page titles. A new pass, registered with `publiclinks.RegisterBodyPass` from its own file, removes the line, as the core pass removes `[[children]]`.

**Export.** Page export resolves the block to a static list of what the exporting reader sees, as it does for the table of contents. The space zip keeps the line as text.

**Web.** Slash menu "Pages by label" with a label picker (`label.list`). The block shows the label as a chip and the pages as links with their emoji.

**Mobile.** The text `[[pages:policy]]`.

**Tests.** Fixtures; a restricted page is absent for a reader without access and present for one with it; the public body has no trace of the line; export lists only readable pages; the look-alike stays text.

### 6.5 Whole-space export

**Today.** One page at a time, in the browser.

**Design.** A space admin asks for an export. A job builds one zip of Markdown files and attachments. The admin is told when it is ready and downloads it from the app within 24 hours.

**Left out:** HTML and PDF for a whole space (they need a server renderer), comments, history, restrictions and members in the export, typed pages, trashed pages, scheduled exports, mailing the file.

**Who.** A workspace admin, or an admin of the space. People only; agents, API keys and guests are refused. One export per space and three per workspace may run at once.

**Access filter. This is the security rule.**

- The job runs as the system principal, so it must not use its own access. It re-reads the requester's membership, role and readable spaces when it starts and builds the principal the way `publiclinks/page.go` does for a link's creator.
- The page set comes from one query with `core.note_access(id, requester, is_admin, readable) <> 'none'` in it: live, plain pages of the space, archived ones included. A page the requester cannot read is not in the zip, not in `index.md`, not counted, and no link to it keeps its id.
- If the requester has left the workspace or lost the admin role by the time the job runs, it fails with nothing written.
- The zip is what the requester could read when the job ran. The download checks the requester again (below) but does not re-filter the zip.

**The zip.**

- `index.md`: the page tree as links. `pages/…/<Title>.md` for each page, in folders that mirror the tree. `files/<page>/<filename>` for attachments.
- File names are built from cleaned segments (`[A-Za-z0-9 _.-]`, 60 characters, `-2` for a duplicate), never from a raw title, so no entry can contain `..` or start with `/`. A path longer than 180 characters is written under `pages/_deep/` and linked from the index.
- Each page starts with a small front matter: title, last updated, labels.
- Bodies are rewritten the way the public body is: a link to an exported page becomes a relative path; a file or image of an exported page becomes a relative path; a mention becomes `@Name`; any other link under `/w/` or `/api/` becomes its text. Step 0 decides whether to lift the link scanner out of `pagebody.go` into a small shared package or to copy the approach.
- Size: the job adds up bodies and file sizes first. Over 2 GB it fails with "This space is too large to export with its files. Export without files." The dialog has that switch.

**Storage.** The zip is written as a stream and cut into 32 MB parts, so the worker never holds more than one part. A part is an object in the file store when one is configured (through the `core.file_objects` intent steps that `file.copy_for_note` uses), or a `bytea` row otherwise. The download joins the parts back into one file.

**Data (1346).**

- `core.space_exports (id, workspace_id, space_id, requested_by, include_files, status, page_count, bytes, error, created_at, finished_at, expires_at)`, status `queued`, `running`, `ready`, `failed` or `expired`.
- `core.space_export_parts (export_id, n, storage_key, data, size_bytes)`.
- Tenant RLS, composite foreign keys with `workspace_id`, explicit grants, as in 1340.

**Commands and route.**

| | Input | Output | Group, role |
|---|---|---|---|
| `space.export_start` | `{space_id, include_files?: true}` | `{export_id}` | `write_notes`, Member, `Confirm`; space or workspace admin; people only |
| `space.export_list` | `{space_id}` | The caller's own exports of the space, newest first | `read`, Member; same admin check |
| `GET /api/w/<ws>/space-exports/<id>/download` | session cookie | The zip | Requester only, and still a workspace member and still an admin of the space or workspace |

The download link is an ordinary signed-in app URL, not a secret link. It is sent with `Content-Disposition: attachment`, an RFC 5987 file name, `nosniff` and `Cache-Control: private, no-store`.

**Job and sweep.** Job `docs.space_export`: `max_attempts` 2; a retry starts again from empty parts. When it finishes it sends the requester an in-app notification with `notify.SpaceSource`. Sweep `docs.space_export_expire`, hourly: for an export past `expires_at`, delete the part rows, put object keys into `core.file_gc`, and mark it `expired`. It also marks an export `failed` when it has been `running` for over an hour.

**Audit.** Events `space.export_started`, `space.exported` (`page_count`, `bytes`, `include_files`) and `space.export_downloaded`, each with the actor.

**Web.** Space settings gets an "Export" section: one sentence on what is included, the "Include files" switch, "Start export", and a list of the caller's exports with status, size, "Download" and "Expires in 20 hours". The bell says "Your export of <space> is ready". References: [GitLab export dialog that says where the result will arrive](https://mobbin.com/screens/aa7cec13-3f01-45db-af47-f74c79cb2bc6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Gamma "Your export is ready" notice with a download link](https://mobbin.com/screens/dfe8916d-fe87-475c-8159-9acacbba83f6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp).

**Mobile.** None.

**Tests.**

- A space with a page restricted away from the requester: the page, its files and its title appear nowhere in the zip, and a link to it from another page is text only.
- A workspace admin gets the restricted page; a space admin without page access does not.
- The requester is removed between start and run: the job fails and no part exists.
- Download by another admin, by the requester after losing the role, and after expiry: all refused.
- Titles with `../`, slashes, control characters, duplicates and 300 characters; a tree 50 deep. Every entry name is inside the zip root.
- Over the size cap; without files; the part boundary (a file larger than one part).
- Expiry removes parts and queues objects; a stuck export is failed.
- Agent, API key, guest and member refused.

### 6.6 Branded PDF

**Today.** "PDF (print)" opens the browser's print dialog on a clean copy of the page. No header, footer or page numbers. 7.9 marked a server renderer as the Phase C upgrade.

**Design.** Stay on the browser's print and add the brand to the print copy.

- A first block: the workspace name (and its logo, if the company profile has one; none was found in `modules/company`, step 0 confirms), the page emoji and title, the owner, the page status, and "Exported 25 Nov 2026 by <name>".
- A footer on every page: the workspace name on the left and "Page 3 of 12" on the right, through CSS `@page` margin boxes. Chromium-based browsers print them. Firefox and Safari print the document without the footer. Step 0 confirms the browser versions.
- The exported HTML file gets the same first block.

`shortcut: browser print; no footer in Firefox and Safari. Move to a server renderer only if customers need identical PDFs from every browser, or PDFs by mail.` A server renderer means a headless browser in the image and a job queue for it, to draw page numbers Chrome already draws.

**Left out:** per-space PDF styling, a cover page with an image, landscape, a PDF of a whole space.

**Data, commands, access.** None. Anyone who can read the page can print it, as today.

**Web.** No new option. "PDF (print)" simply prints the branded copy.

**Mobile.** Same path in the mobile browser.

**Tests.** Unit: the print copy has the first block with the right fields, and no editor chrome. e2e: the print view shows the block; the stylesheet holds the `@page` rule.

### 6.7 Space starters

**Today.** A new space is empty. `core.spaces.template` picks a module layout, not pages.

**Design.** A starter is a small tree of pages created with the space. Starters are code, in `docs/starters.go`, like the built-in templates, so a wording fix reaches everyone.

| Starter | Pages |
|---|---|
| Team wiki | Home, Meeting notes, How-to guides, Policies, Team directory |
| Company handbook | Welcome, Working hours and leave, Holiday list, Expense policy, Onboarding checklist |
| Project docs | Overview, Requirements, Decisions, Meeting notes, Retrospective |
| Sales playbook | Home, Pitch and pricing, Objection handling, Proposal checklist, Client onboarding |
| SOP library | Home, How to write an SOP, Billing and collections, Purchases, Month-end close |

- Each page has a title, an emoji, a body, labels and children.
- Bodies may use every stored form, including columns and `[[pages:<label>]]`, so a Home page lists its section's pages from the first minute. They use the common placeholders (`{{user.name}}`, `{{workspace.name}}`, `{{today}}`) and never `{{ask.*}}`, because nobody is there to answer a prompt. A test enforces it.
- Tabnine writes the bodies; the lead reviews the wording.

**Left out:** saving a space as a starter, applying a starter to an existing space, starters with typed pages or Tables.

**Commands.**

| | Input | Output | Group, role |
|---|---|---|---|
| `starter.list` | none | `[{key, name, description, pages: [{title, icon, children}]}]` | `read`, Member |
| `space.create` | gains `starter?: key` | as today | as today |
| `space.apply_starter` | `{space_id, starter}` | `{created: n}` | Internal; called by `space.create` through `c.Call` in the same transaction |

`space.apply_starter` creates the pages with `notes.InsertNote` under the structural lock, sets labels, and refuses a space that already has a page. The creator is the owner of every page. Guests and the AI read scope cannot create a space today and still cannot.

**Data.** None.

**Web.** The new-space dialog gains a "Start with" row: "Blank" (selected) and the five starters as cards. Picking one shows its page tree on the right. References: [Postman "Create your workspace" with Blank first, a template list and a preview](https://mobbin.com/screens/91e98851-fc9b-4fbd-8753-18f043aa186d?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Wrike "Create a new space" with categories and "Create blank"](https://mobbin.com/screens/a87a47b0-f144-472f-8da9-e1f893456685?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp). We take Postman's shape: a short list with a preview, no gallery.

**Rollout.** Starters contain schema 3 forms, so the dialog offers them only once C2's minimum is raised (4.2 step 3).

**Mobile.** None. Starter pages are ordinary pages.

**Tests.** Every starter renders with no unresolved placeholder and round-trips through the gateway (`builtin-templates.test.ts`); no `{{ask.` in any body; pages, order, labels and owner are right; a failure rolls the space back; a second apply is refused; `starter.list` matches the code.

## 7. Roles, guests and AI scope at a glance

| Command | Release | Group | Minimum role | Extra check | Guest | Read-only AI scope | Agents and API keys |
|---|---|---|---|---|---|---|---|
| `note.update` with `cover` | C1 | `write_notes` | as today | Edit on the page; the file is this page's image | As `note.update` today | No | As today |
| `note.tree` | C1 | `read` | as `note.list` | Access predicate in the query | As `note.list` today | Yes | As today |
| `note.purge` | C1 | `destructive` | Member | Workspace or space admin; could list the deletion; `Confirm` | No | No | Refused |
| `workspace.update` (`trash_retention_days`) | C1 | `admin` | Admin | Value in 0, 30, 90, 365 | No | No | As today |
| `note.trash` (two new fields) | C1 | `read` | Member | Unchanged | No | Yes | As today |
| `link.preview` | C2 | `write_notes` | Member | Rate limits | No | No | Refused |
| `space.export_start` | C2 | `write_notes` | Member | Space or workspace admin; `Confirm` | No | No | Refused |
| `space.export_list` | C2 | `read` | Member | Space or workspace admin; own exports only | No | Yes (lists the caller's own exports; no content) | As today |
| `starter.list` | C2 | `read` | Member | None | No | Yes | As today |
| `space.create` with `starter` | C2 | as today | as today | As today | No | No | As today |
| `space.apply_starter` | C2 | internal | | Only through `c.Call` | | | |

Highlight, table polish, columns, embeds, cards and label lists are page content. They are written by `note.update` and the gateway under the rules those already have.

## 8. Data model in one place

| Migration | Release | Contents |
|---|---|---|
| **1345** `docs_c1.sql` | C1 | `core.notes.cover_file_id uuid`, `cover_y smallint` (nullable); `CHECK (cover_y BETWEEN 0 AND 100) NOT VALID`; `core.workspaces.trash_purge_from timestamptz` (nullable) and `UPDATE core.workspaces SET trash_purge_from = now()`; trigger `attachments_gc` recreated to fire for page files too |
| **1346** `docs_c2.sql` | C2 | `core.space_exports`, `core.space_export_parts`, with tenant RLS, composite keys and grants |

Each starts with `SET LOCAL lock_timeout = '5s'`. Neither adds an index, so there is no `CONCURRENTLY` file. The release after C1 runs `VALIDATE CONSTRAINT` for the `cover_y` check; `docs/deploy.md` gets the line. 1327 stays reserved. The lead confirms both numbers at each step 0.

Settings, not columns: `trash_retention_days` in the workspace settings JSON.

## 9. Security

### 9.1 Embeds

1. **Allow-list only.** A frame is made only for a URL that matches a row of the provider file. Host, path and id are matched whole; nothing is matched by prefix or substring.
2. **The frame URL is derived, never stored and never taken from input.** Stored Markdown cannot hold an `<iframe>`; both converters treat one as text, as they do today.
3. **Sandbox and referrer** as in 6.2. No top navigation, no forms, no device access.
4. **CSP `frame-src`** on app and public page routes names exactly the provider and site catalog origins.
5. **Public pages** load a frame only after a click.
6. **Exports** contain no frame.
7. **Three copies of the list cannot drift:** one file, a copy for Go, an equality test, and one URL list that runs in all three runtimes.

Residual: a provider frame learns the reader's IP address and that they opened some C Note page. It does not learn which page.

### 9.2 Link preview fetch

The fetch uses a new `netguard.PublicClient`, built on the existing dialer. It never honours `AI_ALLOW_PRIVATE_ENDPOINTS`.

| Rule | Value |
|---|---|
| Scheme and port | `https` on 443 only. No credentials in the URL. The host is a name, not an IP literal |
| Addresses | Every address the name resolves to must be public (`netguard.IsPublic`: no loopback, private, link-local, CGNAT, documentation, unique-local or metadata ranges) |
| Rebinding | The dialer checks the address it actually connects to, so a name that resolves differently the second time fails |
| Redirects | At most 3, followed by hand. Each target passes every rule above again, and its connection passes the dial check |
| Time | 5 seconds for the whole exchange |
| Size | Reads stop at 512 KB. Only `text/html` is parsed |
| Request | `GET`, a fixed `User-Agent`, no cookies, no `Authorization`, no `Referer`, no proxy from the environment |
| Parsing | A tokenizer reads `<title>`, `og:title`, `og:description`, `og:site_name` and `meta name=description` from `<head>`, and nothing else. Control characters are removed. Title is cut at 200 characters and description at 300 |
| Answer | The three strings or three empty strings. Never a status code, a header, an error text or a byte of the body, so the command cannot be used to probe a network |
| Who | Signed-in people at Member or above. No agents, API keys or guests |
| Limits | 20 a minute per person, 300 an hour per workspace; a failure is cached for 10 minutes |
| Switch | `LINK_PREVIEW=off` makes the command answer empty without fetching |

Fetched text is stored and rendered as text everywhere. It never passes through an HTML parser on our side.

Tests (Tabnine's list plus a local test server): `127.0.0.1`, `10.x`, `192.168.x`, `169.254.169.254`, `[::1]`, `fc00::/7`, decimal and octal IP forms, a name that resolves to a private address, a public name that redirects to a private one, a redirect loop, a fourth redirect, `http`, another port, credentials, a slow server, a 5 MB body, a non-HTML type, a title with markup, and the private-endpoint switch turned on.

Residual: the linked site sees a request from our server's address for that URL. A person can make our server fetch a public page of their choice, 20 times a minute.

### 9.3 Purge

- **Irreversible.** The only undo is a backup. The dialog and `docs/deploy.md` say so.
- **Who:** the retention sweep, and people who are workspace or space admins and could have restored the deletion. Never a member, guest, agent or API key.
- **Notice:** nothing is purged sooner than one retention period after the rule took effect (`trash_purge_from`). Trash shows the date for every row.
- **Scope:** plain pages only; whole deletions only; never a page with unsaved edits.
- **Audit:** one `note.purged` event per deletion with ids, title of the root, counts, reason and actor. `core.events` is append-only.
- **Files:** queued and retried; a failure is logged and kept, never dropped.
- **Public links:** dead since the page was trashed; the rows go with the page.

### 9.4 Whole-space export

- **Access is applied in the page query, as the requester**, when the job runs. The job's own system access is never used for selection.
- **Admins only, people only**, with `Confirm`.
- **The download is a signed-in route** bound to the requester and re-checked. There is no token link to leak.
- **24 hours**, then the parts are removed.
- **Names are safe by construction**; there is a zip-slip test.
- **Audited** at start, finish and each download.

Residual: a zip already downloaded is outside our control, as any export is.

## 10. Releases and work packages

Tools follow B 14.3. Codex does step 0 and every review; the lead checks its migrations on a scratch database. Tabnine writes fixtures and test data. Sonnet builders take the packages, in parallel, on disjoint files. A file not listed belongs to the lead.

### 10.1 C1

**Step 0 (Codex, then the lead).**

- Migration 1345.
- `notes/c1_commands.go` with stubs and schemas for `note.tree` and `note.purge`; `modules/c1_contract_test.go`.
- `Note` fields and `noteCols` in `notes/notes.go`; cover parsing in `page_chrome.go`.
- `admin.go`: validate `trash_retention_days` and reset `trash_purge_from` on change.
- Types and wrappers in `lib/docs.ts` and `lib/api.ts` (`TreeRow`, the level loader's interface, the two `TrashEntry` fields).
- The committed two-client attribute test, with the table case (4.1). Its result decides whether table polish stays in C1.
- The foreign-key test for `core.notes`.
- `docs/deploy.md`: the `VALIDATE`, the backup sentence, the retention note.

**Tabnine.** The C1 fixtures in 4.3; a generator for a 1,200-page space with restrictions and a deep chain; purge test data (deletions with files, revisions, comments, links, a typed page, unfolded edits).

| | R1 Highlight and tables | R2 Cover and find | R3 Lazy tree | R4 Purge |
|---|---|---|---|---|
| **Creates** | `editor/sticky-header.ts`, `apps/web/src/test/c1-format.test.ts`, `publiclinks/c1_body_test.go`, `e2e/highlight-table.spec.ts` | `components/docs/cover.tsx`, `editor/find-replace.ts`, `components/docs/find-bar.tsx`, tests, `e2e/cover.spec.ts`, `e2e/find-replace.spec.ts` | `notes/tree.go`, `components/docs/tree-loader.ts`, `server/docs_c1_tree_test.go`, `e2e/tree-large.spec.ts` | `notes/purge.go`, `worker/trashpurge.go` and its test, `components/docs/trash-settings.tsx`, `server/docs_c1_purge_test.go`, `e2e/trash-purge.spec.ts` |
| **Edits** | `editor/formats.ts`, `editor/html-inline.ts`, `editor/toolbar.tsx`, `editor/table-controls.tsx`, `apps/collab/src/md.ts`, `apps/collab/test/md.test.ts`, `notes/plaintext.go`, `components/docs/export.ts`, the table and tint block of `app/globals.css` | `components/docs/page-header.tsx`, `components/note-editor.tsx` (mount the extension and the bar), `components/docs/public-page.tsx`, `publiclinks/page.go` and `server/pagepublic.go` (cover fields and the file rule), `notes/duplicate.go` (cover id) | `components/shell.tsx`, `docs/tree-model.ts`, `docs/tree-dnd.tsx`, `docs/move-dialog.tsx`, `docs/page-actions.tsx`, `docs/sub-pages.tsx`, `app/w/[ws]/s/[id]/page.tsx`, `app/w/[ws]/n/[id]/page.tsx`, `components/home/page.tsx`, `docs/dashboard.tsx` | `notes/trash.go` (two fields), `app/w/[ws]/trash/page.tsx` |
| **Must not touch** | Shell, API commands | `md.ts`, shell | Editor, `md.ts` | Editor, shell |

The lead mounts R4's settings component in `app/w/[ws]/settings/page.tsx`. If R2 needs a line in `n/[id]/page.tsx`, R3 adds the mount point.

Merge order: R3, R4, R1, R2. Checks for the lead at integration: a purged page disappears from an open lazy tree; a cover survives duplicate and shows on the public page; a highlight inside a coloured cell of a numbered table round-trips through a co-edit and an export.

### 10.2 C2

**Before step 0.** The mobile one-line change (4.2 step 1) ships in the next app release, during C1.

**Step 0 (Codex, then the lead).**

- Migration 1346.
- `embed-providers.json`, its Go copy and the equality test; each frame URL checked against the provider's documentation.
- `netguard.PublicClient` with its tests.
- The CSP header in `next.config.ts`, after the frame inventory.
- `blocksEnabled(level)`; the mobile literal check in `schema_test.go`.
- Stubs and schemas for `link.preview`, `space.export_start`, `space.export_list`, `starter.list`, `space.apply_starter`; `modules/c2_contract_test.go`; route registration for the download.
- `DOC_SCHEMA = 3` and `DocSchema = 3`, merged last.
- The decision on a shared link scanner for S3.

**Tabnine.** The C2 fixtures; `embed-urls.json`; the SSRF URL list; starter bodies (`docs/starters_content.go`); export seed data (odd titles, duplicates, deep trees, a restricted branch).

| | S1 Format, columns, label list | S2 Embeds and cards | S3 Space export | S4 Starters and branded PDF |
|---|---|---|---|---|
| **Creates** | `editor/c2-nodes.ts` (the four nodes and their Markdown forms), `editor/columns-view.tsx`, `editor/label-list.tsx`, `publiclinks/c2_labelpass.go`, tests, `e2e/columns.spec.ts` | `editor/embed-view.tsx` (frame, card, paste menu), `lib/embeds.ts`, `modules/linkpreview/*`, `server/docs_c2_preview_test.go`, `e2e/embeds.spec.ts` | `docs/space_export.go`, `worker/spaceexport.go` and its test, `server/spaceexport.go`, `components/docs/space-export.tsx`, `server/docs_c2_export_test.go`, `e2e/space-export.spec.ts` | `docs/starters.go`, `docs/starters_test.go`, `components/docs/export-brand.ts`, `e2e/starters.spec.ts` |
| **Edits** | `editor/blocks.ts` (register the nodes), `editor/slash.tsx`, `apps/collab/src/md.ts`, `apps/collab/test/md.test.ts`, `notes/plaintext.go`, `importers/html.go`, `components/docs/export.ts` | `components/docs/public-page.tsx` (click to load) | `components/docs/space-settings.tsx` (mount the section), `components/notifications.tsx` (one sentence) | `components/space-dialog.tsx`, `components/docs/export-menu.tsx`, the print block of `app/globals.css`, `apps/collab/test/builtin-templates.test.ts` |
| **Must not touch** | API commands, shell | `md.ts`, `blocks.ts`, `export.ts` | Editor | Editor, `md.ts`, `export.ts` |
| **Depends on** | Step 0 | S1's `embed` node: S2 supplies the node view and a static form that S1's `export.ts` calls | Step 0 | S1's forms for starter bodies |

S1 owns both converters for all four nodes, so two builders never edit `md.ts`. S2 fills in how an embed looks and behaves.

Merge order: S1, S2, S4, S3. Checks for the lead at integration: a starter Home page with columns and a label list renders in a new space; an embed inside a column works and exports as a box; the space zip of a starter space opens with working relative links; raising the minimum to 3 reloads an old tab and a phone on the new app can still save.

## 11. Decisions taken here (the lead may override)

1. Highlight and table colours use seven named tints, not free colours.
2. Sticky header is always on, with no switch.
3. Table polish stays in C1 only if the committed two-client test shows no deletion in the table case.
4. Embeds have fixed sizes and no resize.
5. Link cards have no image.
6. `link.preview`, `note.purge` and `space.export_start` refuse every agent and API key.
7. Manual purge is for workspace and space admins only, one trash entry at a time; there is no "Empty trash".
8. The audit event for a purge keeps the root page's title.
9. A change of retention restarts the clock for everything in the trash.
10. Whole-space export is Markdown and files only, for space and workspace admins, kept 24 hours, capped at 2 GB.
11. Branded PDF stays on browser print; Firefox and Safari get no footer.
12. Starters are offered only at space creation.
13. Loom is not in the embed list.

## 12. Mobbin

References are cited in each item. No usable screen came back for a column layout block. Mobbin attached this notice to every result and asks that it be passed on as written:

---

**Your Mobbin AI usage is high this month**

Usage is unlimited during beta. If your usage stays at this level, you may need to purchase more credits once limits take effect. [View usage and plan options](https://mobbin.com/settings/billing-and-usage?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp)

## 13. Open questions for the owner

1. **Trash retention.** Pages in the trash are deleted for good after 30 days by default, and an admin can choose 90 days, a year or never. This cannot be undone. Is 30 days the right default, and is there a period you must promise customers?
2. **Old phones at C2.** After C2, a phone on an app older than the release that goes out during C1 can read pages but cannot edit until it updates. How long after that app release should C2 wait: a fixed 14 days, or until a share of active phones (say 95%) has updated?
3. **Outside services.** Two things reach outside C Note. An embedded YouTube, Google or Figma frame tells that company a reader's IP address; on public pages we load it only after a click. And when someone inserts a link card, our server fetches that web page once. Are both acceptable, and does the privacy policy need a line?
4. **Starters.** The five in 6.7 (Team wiki, Company handbook, Project docs, Sales playbook, SOP library) are a first guess at what Indian SMBs set up first. Add, remove or rename any before the content is written.

## 13. Binding revisions (lead decisions and design review, 2026-11-25)

This section overrides sections 1–12 wherever they disagree. Builders read it first.

### 13.1 Owner questions, decided by the lead (the owner may override)

1. **Trash retention:** 30 days by default; 90 days, 365 days or never can be chosen per workspace.
2. **C2 timing:** C2 ships at least 14 days after the mobile app sends schema 3.
3. **Embeds and link cards:** approved. Add a privacy-policy line about embedded third-party content and fetching link previews to the lawyer-review list.
4. **Starters:** the five proposed starters are accepted.

### 13.2 Scope change

Whole-space export (6.5) moves out of Phase C to a later phase with its own design. It needs a job lease and fencing, a gateway fold before each page, a re-check of access at download, and its own cleanup of the export parts (review 4, 5, 6, 11). Branded PDF stays in C2 as a print stylesheet over the existing single-page export.

### 13.3 Fixes

1. **File deletion is enqueue-only** (review 1). `file.delete` must never delete the stored object inside the transaction. It deletes the row, which queues the key through the gc trigger, and the existing sweep removes the object after commit, keeping its check that nothing still references the key.
   - This is a fix to code that exists today. It lands in C1 step 0 with tests: rollback after delete keeps the object, and concurrent deletes are safe.
2. **The retention clock** (review 2).
   - When a workspace is created, `trash_purge_from` is set to its creation time.
   - Null means purge is disabled, and the migration fills existing workspaces with `now()`.
   - A page is eligible only when `deleted_at < now() - retention` and `now() >= trash_purge_from + retention`.
   - Eligibility and the current setting are re-read inside the purge transaction, under the structural lock.
   - Changing the retention to a shorter period never purges sooner than one full new period after the change. Store `trash_purge_from = now()` whenever the setting changes.
3. **Purge safety** (review 3).
   - Under the space lock, re-read the full deletion set and every eligibility rule: still deleted, same deletion_id, plain page, no pending updates.
   - Step 0 lists every incoming FK and every non-FK reference to `core.notes`, with what each means. Records owned independently of the page are preserved or refused. Example: a page that is the source of a template binding (`docs.template_bindings.source_id` cascades) is skipped, not purged. See 13.5 for the inventory and for the table name, which the first text had wrong.
   - Tests: restore wins before purge takes the lock, purge wins before restore, and a template source survives.
4. **Lazy tree with access** (review 9).
   - The full access predicate runs before LIMIT, before the cursor is built, and before `has_children` is computed.
   - A page the reader can see whose ancestors are hidden appears under a synthetic "Shared with you" root for that space. Hidden titles or counts never appear.
   - `reveal` returns only readable ancestors, plus the synthetic root when the chain breaks.
   - Paging around a revealed row works in both directions.
   - Tests: restricted ancestors, shared descendants, and levels with more than 200 siblings.
5. **Compatibility of attributes without a schema bump** (review 10). This replaces 2 and 4.1.
   - An old tab that edits a block drops the new attributes on that block: highlight, cell colour, numbering, sticky header. The text is never lost.
   - The lead accepts this formatting-only loss during the rollout window. It needs no mobile release, and the mobile app edits raw markdown, which keeps the stored forms.
   - C1 is gated on tests showing that an old-client edit of highlighted text, coloured plus highlighted text, a coloured cell and a numbered or sticky table keeps all of its text.
   - Colour and highlight together on one `textStyle` mark must round-trip through every converter, collab md.ts included.
   - If any test shows text being deleted, that feature moves to C2.
6. **Embeds** (review 8).
   - Parse URLs structurally. Reject credentials, non-default ports, ambiguous encodings and unknown query parameters.
   - The frame URL is built from parsed parts and never taken from the input string.
   - The same check runs in the web renderer, the public page, export and the gateway converter.
   - The sandbox gets only the permissions each provider needs; `allow-same-origin` only where the provider needs it and the frame is provably cross-origin.
   - `frame-src` lists only provider origins, never `'self'`.
   - Test: an embed URL that resolves to the app origin is refused.
7. **SSRF for link cards** (review 7).
   - Add a new strict policy in netguard, used without the private-endpoint switch. It allows only global unicast IPv4 and IPv6 and refuses:
     - private, loopback, link-local and CGNAT addresses
     - documentation, benchmarking, multicast, reserved and 6to4/Teredo ranges
     - IPv4-mapped and NAT64 forms of any refused address
     - non-canonical numeric hosts (decimal, octal, hex, short forms)
   - The policy is checked at every dial, after DNS resolution, including on redirects.
   - Tests: mixed public and private DNS answers, rebinding between lookups, mapped IPv6, special-use IPv6 and every numeric form.
   - Docs must not claim the endpoint cannot be used for probing. It is rate-limited and members-only.
8. **Content-by-label in public pages** (review 11). The public body pass removes label-list blocks by parsing blocks. It never removes literal text or code that looks like the marker.
9. **Indexes, sweeps and interfaces** (review 12).
   - Step 0 checks the real query plans for the tree level query (including the root and the synthetic root) and for the purge due-sweep. It adds any needed indexes as staged `-- no-transaction` concurrent files.
   - New sweeps are registered explicitly in `worker/wire.go`. Remember that sweeps run once at worker start.
   - Step 0 defines the shared interfaces used across packages, such as node names, attribute names and the stored forms of the C2 nodes, before any parallel work starts.

### 13.4 C1 packages (unchanged apart from the above)

| Package | Work |
|---|---|
| Step 0 (Codex, verified by the lead) | The file-delete fix, migration 1345, the FK and reference inventory, the query plans, the shared interfaces, command stubs, and the attribute compatibility tests |
| R1 | Highlight and table polish |
| R2 | Cover image and find and replace (editor transactions only) |
| R3 | Lazy tree |
| R4 | Purge and retention |

Tabnine writes fixtures and test data. Codex reviews the PR.

### 13.5 Step 0 findings: reference inventory, query plans, index files

Measured on a scratch database with migrations 1345 to 1347 applied (PostgreSQL 17.11): 31 workspaces, 15,540 pages, 4,500 of them in the trash; the main space has 5,000 live pages in a fan-out of 8, one page with about 1,000 children, 300 roots, 40 live pages under trashed parents, every 50th page view-restricted, every 997th archived, and a reader with 40 direct shares.

**File deletion (13.3.1) is done.** `file.delete` now only deletes the row. Migration 1345 recreates `attachments_gc` without the `field_id IS NOT NULL` condition, so a page file in object storage queues `workspace/id` in `core.file_gc` through the trigger, in the same transaction as the delete (a rollback queues nothing). The existing sweep (`files.sweepWorkspace`) already skips any key whose id still has a row in `core.attachments`, which is the same table for page files, so a rolled-back or re-created id keeps its object. The cascade of a future purge goes through the same trigger. Tests: `docs_c1_step0_test.go` (rollback keeps the object; delete queues and the sweep removes; eight files deleted twice at once, then three racing sweeps).

**The inventory of incoming foreign keys to `core.notes`** (48 constraints, read from `pg_constraint`). "Purge" is what `DELETE FROM core.notes` does to the row. Typed pages are never purged (5.6), so the typed-record groups are listed for completeness: a deletion that holds a typed page is skipped whole before the delete runs.

| Group | Tables (column) | Delete rule | Meaning | What purge does |
|---|---|---|---|---|
| Page body and history | `core.note_revisions`, `core.note_updates` (note_id) | CASCADE | Versions and logged collab updates | Removed with the page. A pending update (unfolded edit) makes the deletion ineligible first |
| Page chrome and social | `core.comments`, `core.note_labels`, `core.note_stars`, `core.note_views`, `core.note_watches`, `core.note_permissions`, `core.notifications`, `core.relations` (from_id, to_id) | CASCADE | Things about the page | Removed |
| Task and mention indexes | `core.note_tasks`, `core.note_task_state`, `core.note_task_notified`, `core.note_mention_state` | CASCADE | Derived indexes | Removed |
| Files | `core.attachments` (note_id) | CASCADE | Page files | Rows removed; the `attachments_gc` trigger queues each S3 key; the sweep deletes the objects |
| Sharing | `core.public_links` | CASCADE | Page links, already dead while trashed | Removed; counted in the audit event |
| Search and AI | `ai.chunks` | CASCADE | Embeddings of the body | Removed |
| Freshness and capture | `docs.freshness_flags` (note_id), `docs.edit_proposals`, `docs.external_bindings` | CASCADE | Living-page state | Removed |
| Freshness and capture, pointing at another page | `docs.freshness_flags.source_note_id`, `docs.capture_requests.answer_note_id` | SET NULL | The flag or capture survives | Pointer cleared |
| **Template provenance** | `docs.template_bindings.note_id` | CASCADE | This page was made from a template | Removed |
| **Template provenance, as source** | `docs.template_bindings.source_id` | CASCADE | Other pages are bound to this page as their source; cascading deletes **their** binding rows | **Skipped**: a deletion that holds a page which is the `source_id` of a binding whose `note_id` is outside the deletion is not purged (13.3.3). The design text called this `core.templates.source_id`; there is no such table. Saved templates (`docs.templates`) hold their own copy of the text and have no foreign key to a page |
| Tree | `core.notes.parent_id` | SET NULL (parent_id) | Live child of a purged page | Child moves to the space root (5.6 step 5) |
| Audit | `core.events.note_id`, `core.confirmations.note_id`, `core.data_requests.subject_note_id` | SET NULL | History that must outlive the page | Pointer cleared. **Events needed a trigger change, see below** |
| Typed records, owned by the page | `crm.contacts`, `crm.deals`, `crm.tickets`, `crm.activities`, `billing.invoices`, `billing.quotations`, `billing.recurring_invoices`, `billing.purchase_bills`, `billing.receipts`, `billing.expenses`, `billing.reminder_prefs`, `projects.items`, `projects.time_entries`, `projects.evidence` | CASCADE | Business records | Never reached: typed pages are not purged. If one were, the cascade would destroy money and CRM data, so `purgeDeletion` must re-check `types = '{}'` under the row lock |
| Typed records that point at an item page | `billing.invoice_lines`, `quotation_lines`, `recurring_invoice_lines`, `purchase_bill_lines` (item_note_id), `billing.quotations.converted_item_id` | SET NULL | A line keeps its text and price, loses the item link | Not reached (items are typed) |

**Non-FK references.** None of these blocks a purge; each is a dangling id after it.

| Reference | Meaning | After purge |
|---|---|---|
| `core.notes.deleted_parent_id` (other trashed pages) | Where a restore puts the page back | Dangling; restore goes to the space root (7.10.5) |
| `core.notes.cover_file_id` | A file, not a page | Not a page reference; the cover goes with its page |
| `core.agent_runs.note_id`, `billing.reminder_attempts.invoice_note_id`, `billing.reminder_log.invoice_note_id`, `billing.journal_entries.source_id` | Run and ledger history | Kept as history; the last three are invoices (typed, never purged) |
| Page ids inside JSON: `core.events.payload`, `core.dashboards.tiles`, `core.views.options`, `core.hooks.arguments`, `core.spaces.config`, `core.ui_configs.config`, `core.agent_runs.context`, `projects.*` and `tables.*` config | Saved views, tiles and hooks that name a page | Not enumerated; a page that cannot be found renders as missing, as a trashed page does today |
| `/n/<id>` links and `[[page]]` mentions in other pages' bodies | Text links | Dead link, as for a trashed page today |

**Finding that changes the plan: events cannot be nulled.** `core.events` has an append-only trigger on UPDATE and DELETE (`forbid_mutation`), and `events.note_id` is `ON DELETE SET NULL`, which is an UPDATE. So today any `DELETE FROM core.notes` of a page with a single event fails with `events is append-only` (tested on the scratch database; `qa_integrity_test.go` already notes that "a note with history cannot be hard-deleted"). Purge could never have worked. Migration 1345 replaces the trigger function for `core.events` with one that allows exactly the foreign key's own update, `note_id` from a value to NULL with every other column unchanged. DELETE and every other UPDATE stay refused (test `TestC1EventsStayAppendOnlyExceptTheForeignKeyNulling`). The weakening is that someone with UPDATE on the table could now detach an event from its page; `cnote_app` already had UPDATE and the previous trigger was the only guard, so this is accepted and noted for the security review.

**Migration 1345** is online-safe: `SET LOCAL lock_timeout = '5s'`; nullable `cover_file_id`, `cover_y` (no rewrite) and a `NOT VALID` range check; nullable `trash_purge_from` filled once for the existing workspaces (one small table); the `attachments_gc` and `events_append_only` triggers swapped in one transaction. It adds no table, so there is no RLS, composite key or grant to add. `trash_purge_from` is stamped in Go: `auth.CreateWorkspace` sets it to `now()`. A workspace created by an older build during the rollout keeps NULL, which disables its purge and is the safe direction. There is no column default on purpose. The retention itself is `settings.trash_retention_days` (absent means 30, 0 means never) and is changed only through `trash.retention_set`; `workspace.update` refuses that key, so the clock cannot be bypassed.

**Query plans** (cnote_app role with the tenant setting, so the row-level-security condition is in the plan; the reader holds no space access unless stated; `note_access` is the page access function and is the dominant cost: about 7 microseconds per page, a recursive walk up the ancestors).

*Tree level.* The rule of 13.3.4 is that the access predicate runs before LIMIT, so a level costs one `note_access` per candidate page until 101 have passed.

| Query | Plan | Time |
|---|---|---|
| Root level, first form (`has_children` as a correlated EXISTS in the select list) | The planner hashed the EXISTS and ran `note_access` on every page of the space (4,824 calls, 48,202 buffers); the roots came from `notes_ws_space` (6,540 rows read) | 155 ms |
| Root level, page-first form: an inner `ORDER BY position, created_at, id LIMIT 101` with the predicate, and `has_children` computed only for those 101 rows | Bitmap scan on `notes_ws_space`, top-N sort of 334 candidates, 101 index probes for the arrows; trashed and archived parents through `notes_hidden_parents` (208 down to 65 buffers) | 32 to 46 ms, 2,500 buffers |
| Root level, two ordered branches (roots by `notes_siblings`; children of hidden parents by `notes_hidden_parents` joined to `notes_siblings`), `UNION ALL`, merge append, LIMIT 101 | Both branches stop early | 21 ms |
| Children of the page with about 1,000 children, keyset page 2 | Page-first form: bitmap scan on `notes_tree_level`, sort, `note_access` on 641 rows | 34 ms |
| Same, with the planner pushed to the ordered index scan (`enable_bitmapscan = off`, or `note_access` declared `COST 2000`) | Index scan on `notes_tree_level`, no sort, stops after 101 | 7 to 15 ms |
| Shared level (the synthetic root), candidates from the reader's own rows in `core.note_permissions` | Seq scan of the 40 share rows, primary-key probes, `note_access` twice per candidate | 3.5 ms |
| Shared existence by scanning the space (`note_access` on every page and its parent) | 4,740 rows, 44,384 buffers | 94 ms, **rejected** |

What R3 must do with this:

1. Always write the level as an inner page query with the predicate and the LIMIT, and add `has_children` in an outer query over at most 101 rows. The first form is 5 times slower.
2. The ordered plan wins only when `note_access` looks expensive to the planner. Step 0 does not change the function's cost (it would change every caller's plans). R3 should either set `COST` in a migration of its own after measuring the other callers, or keep the sort, which is 15 to 35 ms at 1,000 siblings and grows linearly with the sibling count. The e2e test with 600 pages and the API test with 1,200 siblings must record the time.
3. **The Shared root needs no space scan.** A page can be readable while its parent is not only when the reader has a direct row in `core.note_permissions` for that page itself: every ancestor of the parent is also an ancestor of the child, so any restriction that hides the parent hides the child, and a share on an ancestor makes the parent readable too. The Shared with you level is therefore the reader's own permission rows (index `note_permissions_user`), filtered to the space, with the child readable and the parent not. The root response's `shared` flag comes from the same query with LIMIT 1. R3 must test the invariant (a share on a grandchild, on a child under a view-restricted parent, and on a page under an archived parent).

*Purge due sweep.* `SELECT deletion_id, min(deleted_at) ... WHERE workspace_id = $1 AND deletion_id IS NOT NULL AND deleted_at < now() - retention GROUP BY deletion_id ORDER BY min(deleted_at) LIMIT 200` is an index-only scan on `notes_deletion` (`workspace_id, deletion_id, deleted_at`): 1,111 entries of 1,500, 18 buffers, 0.2 ms, no heap fetch. The check "which workspaces have anything due" is a semi-join through the same index: 0.013 ms per workspace. No index is added. The scan reads every trashed entry of the workspace because `deleted_at` is not the leading column after the equality; that is fine up to tens of thousands of trashed pages per workspace and is the number to watch.

**Staged index files** (each `-- no-transaction`, one statement, `CONCURRENTLY IF NOT EXISTS`; an invalid leftover index is dropped and the file rerun, as in docs/deploy.md). Deploy order: 1345 first (the app can run on it alone), then 1346 and 1347 before the R3 release.

| File | Index | Why |
|---|---|---|
| `1346_docs_c1_tree_level_index.sql` | `notes_tree_level (space_id, parent_id, position, created_at, id) WHERE deleted_at IS NULL` | Keyset order of a level without a sort, and the arrow probes |
| `1347_docs_c1_hidden_parents_index.sql` | `notes_hidden_parents (space_id) INCLUDE (id) WHERE deleted_at IS NOT NULL OR freshness = 'archived'` | Finds the trashed and archived parents of a space without reading the space |

`notes_siblings` stays: other queries use it. Dropping it later is a separate decision.

**Registered sweeps.** The purge sweep (R4) is registered in `worker/wire.go` explicitly. Sweeps run once at worker start, so a restart runs it at once; the per-deletion lock timeout and the 200-deletion cap keep that safe.

### 13.6 C1 shared attribute interfaces

C1 keeps schema 2. `textStyle` carries both `color` and `highlight`; highlight is one of yellow, green, blue, purple, red, orange and grey, stored as `<mark data-color="yellow">text</mark>` around the colour span. A bare mark reads as yellow; an unknown tint keeps its text. `tableCell` and `tableHeader` carry `background`, stored as `data-bg` on the cell. `table` carries boolean `numbered`, stored as `data-numbered` on the HTML table. Sticky headers are always on and have no stored attribute. A coloured or numbered table uses HTML storage. Covers are page chrome: nullable `cover_file_id` and nullable `cover_y` (0–100); the file must be an image attached to that page, and an absent file does not render.

**Attribute names and stored forms (fixed in step 0; R1 builds to them).**

| Feature | Editor (Yjs, TipTap) attribute | Stored Markdown |
|---|---|---|
| Highlight | `textStyle.highlight`: one of `yellow`, `green`, `blue`, `purple`, `red`, `orange`, `grey`; absent or null for none. `textStyle.color` is unchanged and may be set on the same mark | `<mark data-color="yellow">text</mark>`, outside the colour span: `<mark data-color="yellow"><span style="color: #c0392b">text</span></mark>`. A bare `<mark>` reads as `yellow`; an unknown `data-color` keeps its text and no tint |
| Cell colour | `tableCell.background` and `tableHeader.background`, same seven names, default null | `data-bg="green"` on the `<td>` or `<th>`; the table is stored as HTML |
| Numbered column | `table.numbered`, boolean, default false | `data-numbered="true"` on the `<table>`; the table is stored as HTML. The number column is drawn, never stored as cells |
| Sticky header | none | none; always on |
| Cover | `core.notes.cover_file_id uuid`, `cover_y smallint` (0 to 100); `Note.cover` is `{file_id, y} \| null` | Not in the body |

Fixtures: `apps/collab/test/fixtures/blocks.json`, seven entries marked `"pending": "c1"` (every tint; colour plus highlight; bare and unknown marks; highlight beside bold, link and italic; coloured cells; numbered table; numbered, coloured, merged table with a highlight inside). Today the text and second-pass-stability assertions run for them in both `apps/collab/test/fixtures.test.ts` and `apps/web/src/test/fixtures.test.ts`; the byte-for-byte assertion is skipped until `C1_LANDED` is set to true in both files by R1.

**Command contracts (stubs registered, answer "not implemented" until the owning package lands).** Contract test: `modules/c1_contract_test.go`. Web types: `apps/web/src/lib/docs.ts`.

| Command | Group (AI scope) | Minimum role and guest rule | Owner |
|---|---|---|---|
| `note.tree {space_id, parent_id \| null \| "shared", after, limit}` or `{reveal}` | `read` (in the read-only AI scope) | Guest; access predicate in the query | R3 |
| `note.cover_set {note_id, file_id \| null, y}` | `write_notes` (not read-only) | Guest, edit access on the page checked in the handler; archived pages refused | R2 |
| `note.purge {deletion_id}` | `destructive`, `Confirm` (never read-only; an agent or API key waits for a person) | Member; the handler requires workspace admin or admin of the deletion's space and that the caller could list it in Trash; guests never | R4 |
| `trash.retention_get` | `read` | Member | R4 |
| `trash.retention_set {days: 0 \| 30 \| 90 \| 365}` | `admin` | Admin; resets `trash_purge_from` | R4 |

Two differences from the earlier text: the cover is its own command `note.cover_set` instead of a field of `note.update` (so its schema, scope and error cases are separate and `note.update` stays unchanged); and the retention has a get and a set command, with `workspace.update` refusing the key `trash_retention_days`. `note.trash` rows still gain `purge_at` and `can_purge` (R4). The tree's synthetic root is a request value (`parent_id: "shared"`) and a `shared` flag on the root level, not a fake row.

**Compatibility result (13.3.5), observed.** `apps/web/src/test/c1-compat.test.ts` joins a client with the new attributes (A) to a client with today's editor extensions (B) over Yjs, B edits inside the block, the result syncs back:

| Old tab edits | Text lost? | What is lost |
|---|---|---|
| Highlighted text | No | The highlight on the edited block |
| Colour and highlight on one mark | No | The highlight; the colour stays |
| A coloured cell | No | The background of the edited cell; cells nobody touched keep theirs |
| A numbered table (and a coloured cell in it) | No | `numbered` on the table, and the edited cell's background |

`apps/collab/test/c1-step0.test.ts` shows today's gateway folding a document that already holds these attributes to Markdown without losing text; it drops the attributes, as expected before R1. **No feature moves to C2 on the evidence of these tests.**

**One finding, text-preserving but not clean.** The fixture `highlight-next-to-other-marks` (a link inside a `<mark>`) is not stable in today's browser Markdown converter: the editor has no `<mark>`, so the Markdown inside it is escaped and the link's URL is wrapped again, `[a link](url)` becoming `[a link]([url](url))`, at every save by an old web tab. No word is lost. It is the same defect the status mark had before A1 (7.8). It affects only links and other Markdown inside a highlight, in a tab that has not reloaded, and stops once the tab reloads and R1 is live. The fixtures test skips that one stability assertion until `C1_LANDED`. If the lead wants zero risk, R1 can refuse to apply highlight over a link until the rollout window ends; otherwise it is accepted with the other formatting losses.
