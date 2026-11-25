# Docs parity, Phase B "Teams and sharing": build design (25 Nov 2026)

This is the build design for Phase B of `docs/design/docs-parity.md` (section 4). It is written against the code on `feat/docs-b` (main with Phase A1 merged) and treats Phase A2 as landed, relying only on what 7.9 and 7.10 of the parity plan promise.

Section 7 of the parity plan stays binding. Where this document says "7.x" it means that plan. "Tables 18.8" means `docs/design/tables.md` section 18.8.

Drag reorder was in the Phase B list. A2 already built it, so it is not here.

## 1. Summary

Phase B ships in two releases.

| Release | Contents | Schema change | Size |
|---|---|---|---|
| **B1** | Space members and settings; public page links; email notifications and daily digest; tasks with assignee and due date plus "My tasks"; comment edit, delete and reactions | None. `DOC_SCHEMA` does not move | About 1 week, 5 builders after step 0 |
| **B2** | Request access; file attachments in pages; page link picker; child pages block; manual archive; page views byline; Word import; template prompts | One new node (`children`). `DOC_SCHEMA` goes up by one | About 1 week, 3 builders after step 0 |

Five choices keep the phase small. Each is explained in its item.

| Choice | Effect |
|---|---|
| A task's assignee and due date are the mention link and the date chip that A1 already stores, inside an ordinary to-do item | Tasks need no new node, no converter change and no schema bump |
| A file attachment is an ordinary link to `/api/w/<ws>/files/<id>`, drawn as a file chip | Same: no new node |
| A page link is an ordinary link to `/w/<ws>/n/<id>`, inserted by a picker | Same: no new node |
| Public page links reuse `core.public_links` (token, hash, revoke) with a new purpose, behind the hardened public endpoint pattern of Tables 18.8 | No second link system |
| Email is driven from `core.notifications` rows by two worker sweeps | One delivery path, and access is checked again when the mail is built |

The only new block in all of Phase B is the child pages block, so there is exactly one schema bump, in B2.

## 2. What the code says today, where it differs from the plan

| Area | Finding | Evidence | Consequence |
|---|---|---|---|
| Preferences | There are no notification preferences. `core.ui_configs` holds per-person screen layouts only, and its own comment says the server does not read its shape | `migrations/1312_ui_config.sql` | B1 adds a small `core.notify_prefs` table |
| Mention email | An email for mentions already exists. It sends straight over SMTP on `mention.created`, has no preference, no retry, and does not check access when it sends | `worker/mail.go` `mentionEmail`, `worker/wire.go:34` | B1 replaces it with the sweep in 4.3 |
| Mentions while co-editing | The gateway snapshot emits `note.updated` but nothing on that path calls `notifyMentions`; only `note.update` in `write.go` does. It looks as if a mention typed while the gateway is connected never notifies. Not confirmed by a run | `apps/collab/src/server.ts:310`, `notes/write.go:406` | Task assignment (4.4) does not depend on it. The lead should confirm and fix it separately |
| Tasks | TipTap `TaskList` and `TaskItem` (nested) are in the editor. They are stored as `- [ ]` and `- [x]` lines, and the gateway parses and renders the same form | `note-editor.tsx:193`, `apps/collab/src/md.ts:502, 937` | Tasks build on this |
| Relative links | Both converters accept a link with no scheme, which is how mentions are stored | `md.ts` `safeHref` | File links and page links need no converter change |
| Files | `file.upload`, `file.list`, `file.download` and `file.delete` exist. Types are JPEG, PNG, GIF, WebP and PDF, 10 MB each, 10 per page. Images uploaded by the A1 editor count against the same 10 | `files/files.go:28-35` | B2 raises the count for plain pages only |
| Public links | `core.public_links` exists for quotations, invoices and receipts: a 32-byte token, only its SHA-256 stored, revoke, expiry, view count. Its limiter is an in-process map and it trusts one `X-Forwarded-For` switch | `publiclinks/links.go`, `public.go`, `limiter.go` | Page links reuse the table and token, but get a new endpoint built on the Tables forms limiter |
| Space roles | `core.space_members` has roles admin, editor and viewer. Only `space.create` ever inserts a row. Outside Tables the admin role grants nothing | `notes/write.go:97`, `access/access.go`, `tables/store.go:81` | B1 gives the admin role its meaning |
| Page views | `core.note_views` already holds one row per person per page | `migrations/1329_docs_parity_a1.sql` | The byline needs no table |
| Importers | `import.start` is admin only and takes pasted text. There is no file upload path and no Office parser, and no zip or docx dependency in `go.mod` | `modules/importers/importers.go` | Word import is a new, single-file command |
| Templates | `template.refresh` re-renders a page from its template | `docs/templates.go:729` | Prompt answers must be stored, or a refresh would wipe them |

## 3. Rules for every Phase B package

These restate section 7 for this phase. Builders read them first.

1. **Schema (7.3).** B1 adds no node and no mark. B2 adds one node. A builder who finds they need another node or mark stops and asks the lead.
2. **Fixtures (7.4).** Every stored form in this document has a fixture in `apps/collab/test/fixtures/blocks.json`, and every serializer, parser and the plain-text extractor must round-trip it. The lead owns the file.
3. **Access inside queries (7.6).** Every list applies the full access predicate (readable spaces, direct shares, `core.note_access`) inside the query, before grouping, counting or paging. Rows for pages the caller cannot see are ignored, never counted.
4. **Notifications are checked three times.** At send, in `notification.list` (both exist), and now again when an email is built.
5. **Structural lock (7.10.1).** A command that reads a subtree and must see it whole takes the space lock `pg_advisory_xact_lock(hashtextextended('notes.tree:' || space_id::text, 0))` first. In Phase B that is `doc.archive` only. Page creation by import goes through `note.create`, which takes it already.
6. **The generic refusal (7.9).** When a subtree holds a page the caller cannot see or edit, the answer is `forbidden` with `Some pages here are restricted` (`RESTRICTED_MESSAGE`), with no count and no name.
7. **Staged migrations (7.10.7).** A migration starts with `SET lock_timeout = '5s'`, adds nullable columns and new tables only, adds changed CHECK constraints `NOT VALID`, and builds indexes `CONCURRENTLY` in their own file. `VALIDATE CONSTRAINT` comes in the following release and is written down in `docs/deploy.md`.
8. **Guests.** A guest reaches a page only through a share. A guest never runs a command that acts on a space, on other pages, or on the outside world.
9. **AI scope.** The read-only AI scope holds commands in group `read` only. Every state-changing command here is in another group.
10. **Module boundaries.** `notes` does not import `files` or `publiclinks`; cross-module work goes through `c.Call` (`boundaries_test.go`).
11. **Shared files stay with the lead:** migrations, `notes/notes.go`, `notes/write.go`, `command/*`, `access/*`, `apps/collab/src/server.ts`, `apps/collab/src/doc-schema.ts`, the fixtures, `worker/wire.go`, `apps/web/src/lib/api.ts`, `apps/web/src/lib/docs.ts`, `e2e/helpers.ts`. Builders register handlers from `init()` in their own files.

### Migration numbers

(Superseded by 13.5: B1 is **1340 to 1342**, B2 takes the next free number.) Written here as **1337 and 1338 for B1, 1339 for B2**. That assumes A2's follow-ups from 7.10 (the consistency constraint, the recreated access functions) fit inside 1334 to 1336. If they took later numbers, B shifts up by the same count. 1327 stays reserved for invites. The lead confirms the numbers at step 0, as 7.7 requires.

## 4. Release B1

### 4.1 Space members and settings

**Today.** A private space cannot gain a member. The table and the checks exist (`access.AssertSpaceAccess`, `ReadableSpaceIDs`, the gateway's copy in `apps/collab/src/access.ts`), but only the creator is ever inserted. Only `space.create` and `space.list` are registered. The space page is a 29-line list.

**Design.**

- Four commands: list members, set a member's role, update the space, archive it.
- The space admin role now means: may manage members and settings of that space. Workspace admins and owners can do the same everywhere, as today.
- An open space keeps its rule: every workspace member can edit unless a row says viewer. The members list says so in words at the top.
- Turning a space private keeps the listed members and adds the actor as admin. The dialog shows how many workspace members lose access before it confirms.
- An archived space is read-only for everyone and leaves the sidebar. It stays readable and searchable. This one change in `AssertSpaceAccess` covers pages, Tables and Projects in that space, because they all call it.
- The space key cannot be changed. Item keys such as `ENG-42` are built from it.

**Left out:** space delete, an overview page, pinned pages (the matrix row "Space home" moves to Phase C), adding guests to a space, groups.

**Data model (1337).** `core.spaces` gains `description text`, `archived_at timestamptz`, `public_links boolean NOT NULL DEFAULT true`. `core.space_members` is unchanged.

**Commands.**

| Command | Input | Output | Group, role | Checks |
|---|---|---|---|---|
| `space.members` | `{space_id}` | `{visibility, members: [{user_id, name, email, role, workspace_role}], can_manage}` | `read`, member | Space read access |
| `space.member_set` | `{space_id, user_id, role: "admin" \| "editor" \| "viewer" \| null}` | The member row, or `{removed: true}` | `write_notes`, member, `Confirm` for agents and API keys | Caller is a space admin or workspace admin. Target is a workspace member who is not a guest. A private space always keeps one admin |
| `space.update` | `{id, name?, description?, visibility?, public_links?}` | The space | `write_notes`, member, `Confirm` | Same caller rule. Name 1 to 80 characters, description at most 500 |
| `space.archive` | `{id, archived}` | The space | `write_notes`, member, `Confirm` | Same caller rule |

`space.list` gains `description`, `archived`, `public_links` and `my_role`, and leaves archived spaces out unless `include_archived` is true.

Events: `space.member_changed {space_id, user_id, role, previous}`, `space.updated {space_id, changed}`, `space.archived`, `space.unarchived`. A person added to a space gets a `shared` notification (4.3) titled with the space name.

**Access rules.**

- New helper `access.AssertSpaceAdmin(c, spaceID)`: the caller's row in `core.space_members` has role admin, or the caller's workspace role is admin or owner. If A2 already added an equivalent for its "space admin" rule, the lead keeps one.
- `AssertSpaceAccess(write = true)` refuses an archived space with `forbidden` "This space is archived". The gateway's `assertSpaceAccess` gets the same line, so its 10-second recheck drops editors to read-only.
- The last-admin rule is checked after the write, under `SELECT ... FROM core.spaces WHERE id = $1 FOR UPDATE`, so two concurrent removals cannot empty the list.
- Removal takes effect at once, because access is decided in queries. Watches go lazily (exists), notifications are filtered at read (exists), and the removed person's public page links stop resolving (4.2).

**Web.**

- A "Space settings" entry in the space row menu and on the space page, shown to people who can manage. Route `w/[ws]/s/[id]/settings`, with tabs Members, Details and Public links.
- Members: a table of name, email, role select and remove, with an "Add people" box that searches `member.list`. References: [Plane project members](https://mobbin.com/screens/c54d363a-19fa-4ab7-bc81-6f2f9213cb0c?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the table with a role column, and [Base44 members](https://mobbin.com/screens/06b69874-5889-4b5e-8f3b-bd23c6ccb4ec?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the inline role dropdown.
- Details: name, description, open or private, archive. The space page shows the description under the title.

**Mobile.** No call is required. `space.list` stops returning archived spaces, so they leave the app's lists.

**Tests.** A space editor cannot manage; a space admin who is a workspace member can; the last admin cannot be removed or demoted, also under two concurrent calls; a guest cannot be added; a removed member loses the pages, the search hits and the open editor; open to private keeps listed members only; an archived space refuses page, table and item writes and gateway edits; an agent call waits for confirmation. e2e: create a private space, add a colleague as viewer, the colleague reads but cannot edit, promote, remove.

### 4.2 Public page links

**Today.** `share.create` accepts quotations, invoices and receipts only. The token is 32 random bytes in base64url (43 characters) and only its SHA-256 is stored. The public handler serves a fixed billing document shape.

**Design.**

- One active link per page. The share dialog has a "Public link" row with a switch.
- Creating a link returns the URL once. It cannot be shown again, because only the hash exists. "Create new link" replaces it and the old one stops at once.
- Editors may create a link (7.1). A space admin can turn public links off for the space; doing so revokes every active page link in that space in the same transaction. Turning the switch back on revives nothing.
- A page with an active link shows a "Public" chip in its header to every reader, so nobody types into a public page unaware.
- The public page is read-only, at `/pg/<token>`, outside the workspace layout, with no session.

**Left out:** password on a link, per-link analytics beyond the view count, a workspace-wide switch, public links for a page tree, comments by outsiders, export from a public page.

**Data model (1337, 1338).**

- `core.public_links.purpose` accepts `page` (constraint re-added `NOT VALID`).
- A partial unique index, `(note_id) WHERE purpose = 'page' AND revoked_at IS NULL`, built `CONCURRENTLY` in 1338.
- `core.resolve_public_link` is recreated to exclude purpose `page`, so the billing endpoint can never answer for a page token.
- New `core.resolve_page_link(p_hash text)`, written to the Tables 18.8 rule: `SECURITY DEFINER`, `STABLE`, `SET search_path = pg_catalog, pg_temp`, every object fully qualified, execute granted to `cnote_app` only. It returns `(id, workspace_id, note_id, space_id, created_by)` only when the link is a page link, not revoked and not expired; the page is live, a plain page (`types = '{}'`) and not archived; and the space has `public_links` on and is not archived. Every other case is zero rows.

**Commands** (`publiclinks/page.go`).

| Command | Input | Output | Group, role | Checks |
|---|---|---|---|---|
| `page_link.get` | `{note_id}` | `{link: {id, status, created_by_name, created_at, expires_at, view_count, last_viewed_at} \| null, allowed, reason}` | `read`, member | Page view access. Never returns a token |
| `page_link.create` | `{note_id, expires_in_days?: 1..365 \| null}` | `{link, url}`, `ReturnsSecret` | `send_messages`, member, `Confirm` | Page edit access; plain page; not archived; space switch on; the rule below on restrictions. Revokes the page's earlier link first |
| `page_link.revoke` | `{note_id}` | `{revoked: n}` | `write_notes`, member | Page edit access, or space admin |
| `page_link.list` | `{space_id}` | `[{note_id, title, created_by_name, created_at, expires_at, view_count}]` | `read`, member | Space admin |
| `page_link.revoke_space` | `{space_id}` | `{revoked: n}` | Internal, called by `space.update` | None of its own |

No expiry by default (see the open questions). Events reuse the existing kinds: `share.created` with `purpose: "page"`, `share.revoked`, and `share.viewed` once per link per day. The token never reaches an event, a log line or the idempotency table.

**When a link serves.** All of these must hold at the moment of each request. The first group is in the resolver; the rest run inside the tenant transaction.

1. The link is not revoked and not expired; the page is live, plain and not archived; the space allows public links and is not archived.
2. The person who created the link is still a workspace member at Member or above, and still has edit access to the page (`core.note_access` with their own role and spaces). The request runs as a system principal acting for them, capped at Member, as public forms do.
3. Neither the page nor any ancestor has restriction `view`. A chain longer than 50 or a cycle counts as restricted (7.10.8). `page_link.create` refuses such a page with "Remove the view restriction to share this page publicly".

Rule 3 makes "restrict" a reliable way to take a page off the internet. Rule 2 means removing a person kills their links, as it does for invoices today.

Because validity is computed on every request, a page that is moved under a restricted parent, sent to the trash or archived stops being public without any extra code, and comes back if that is undone. The "Public" chip tells the truth either way, since `page_link.get` applies the same rules.

**What a public reader gets.** `GET /api/public/page/<token>` returns `PublicPage`, and a golden test pins its key set:

```json
{"title": "", "icon": null, "wide": false, "body_md": "", "updated_on": "2026-11-25", "workspace_name": ""}
```

`body_md` is the stored body after `PublicBody` has rewritten it:

| In the stored page | In the public body |
|---|---|
| An image or file link to a file of this page, `/api/w/<ws>/files/<id>` | Rewritten to `/api/public/page/<token>/files/<id>` |
| An image or file link to a file of another page | Image: its alt text. Link: its text |
| A mention, `[@Name](/w/<ws>/people/<id>)` | The text `@Name` |
| A link to another page or anything else under `/w/` or `/api/` | Its text |
| `[[children]]` (B2) | Removed |
| `http`, `https` and `mailto` links, external images | Kept |

The rewrite covers both Markdown destinations and `href` and `src` inside the HTML subset (merged tables). No identifier of a workspace, person or other page is left in the output.

Never returned: comments and their highlights, child pages, labels, page status, owner, editors, viewers, history, watchers, the space name, other pages' files.

**Files.** `GET /api/public/page/<token>/files/<id>` runs the same resolve and the same checks, and then requires that the file is attached to this page **and** that its id appears in the page's current body. A file that was removed from the text but still sits in the attachments panel is not served. The bytes are read with `c.Call("file.download")` as the capped principal, so the file guard runs too. Headers follow Tables 18.8: `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, an RFC 5987 filename, PDF always `attachment`, an image `inline` only for the four image types.

**Web.**

- Share dialog: a "Public link" row under "General access". Off: one line of explanation and "Create public link". Just created: the URL, Copy, WhatsApp, a QR code (`share/qr-svg.tsx` exists), and "This link is shown only once". On: who created it, when, the view count, "Create new link" and "Turn off". If the space switch is off, the row says so and has no button. References: [Plane publish popover](https://mobbin.com/screens/f6e00ee6-635c-41d4-b762-1876fc7ec11f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for regenerate with its warning and "Unpublish", and [Higgsfield share dialog](https://mobbin.com/screens/86ab7ca2-9e84-479e-addb-bfb91fefbeed?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the private and public choice above the link.
- Public page `app/pg/[token]/page.tsx`: a centred column (wide when the page is wide), the title with its emoji, the body, and a footer "Shared from <workspace name> with C Note" with "Report abuse". It renders with a read-only editor built from the same extension list minus collaboration, mentions, comments and uploads. Only this renderer accepts the public file path as an image source; the stored form never contains it. States: loading, unavailable (one sentence for 404 and network errors), busy (429).
- External links open with `rel="noopener noreferrer nofollow ugc"`.
- `next.config.ts` sends `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex, nofollow` for `/pg/:path*`.

**Mobile.** None. The public page is a web page and works at 390 px.

**Tests.** In section 9.1, which also covers the rate limits.

### 4.3 Email notifications and daily digest

**Today.** Notifications are in-app. One email exists, for mentions, described in section 2. `mailer.Enqueue` gives an outbox job with a fixed Message-ID and retries. There are no preferences.

**Design.**

- `core.notifications` stays the single source. Email never has its own recipient logic.
- Email covers the page kinds only: `mention`, `assigned` (tasks, 4.4), `shared`, `access_request` (5.1), `comment`, `page_updated`. Approvals, reminders, leads and chat keep what they have today.
- Each person chooses one of three modes per workspace:

| Mode | What is sent |
|---|---|
| `smart` (default) | Mentions, tasks assigned to you, shares and access requests within about five minutes. Comments and page updates in the daily digest |
| `digest` | Everything in the daily digest |
| `off` | Nothing |

- **Direct sweep,** every 2 minutes. For each person in `smart` mode it takes their direct-kind notifications that are unread, not yet emailed, at least 3 minutes old and at most 24 hours old, and sends **one** email listing them. Reading a notification in the app within those minutes cancels its email. A burst of ten mentions is one email.
- **Digest sweep,** hourly. For each person in `smart` or `digest` mode whose digest hour has come and who has not had today's digest, it takes their unread, un-emailed page notifications of the last 48 hours, plus their open tasks that are overdue or due today. Nothing to say means no email. At most 30 lines; the rest is "and 12 more" with a link.
- The digest hour is 08:00 IST by default and can be changed. `shortcut: IST only; add a time zone column when a workspace outside India asks.`
- `mentionEmail` and its line in `wire.go` are removed. The `mention.created` event stays, because hooks and agents read it.
- If SMTP is not configured, both sweeps do nothing.

**Left out:** email for non-page kinds, a per-kind matrix, a weekly digest, an "everything at once" mode, reply by email, one-click unsubscribe headers (`shortcut: a footer link to the settings; add RFC 8058 headers if these mails are ever sent in bulk`).

**Data model (1337, 1338).**

- `core.notifications.emailed_at timestamptz`: set when a row has been handled for email, whether it was sent or suppressed.
- `core.notifications` kind check gains `shared` and `access_request` (`NOT VALID`).
- `core.notify_prefs (workspace_id, user_id, email_mode text NOT NULL DEFAULT 'smart' CHECK (email_mode IN ('smart','digest','off')), digest_hour smallint NOT NULL DEFAULT 8 CHECK (digest_hour BETWEEN 0 AND 23), last_digest_on date, PRIMARY KEY (workspace_id, user_id))`, tenant RLS. No row means the defaults.
- A partial index on `core.notifications (workspace_id, created_at) WHERE emailed_at IS NULL`, built `CONCURRENTLY` in 1338.

**Commands** (`notifications/prefs.go`).

| Command | Input | Output | Group, role |
|---|---|---|---|
| `notify_prefs.get` | `{}` | `{email_mode, digest_hour, email_configured}` | `read`, guest |
| `notify_prefs.set` | `{email_mode?, digest_hour?}` | The same | `write_notes`, guest |

Both belong to people: a principal with no user is refused, as in `notification.list`.

**Access and idempotency.**

- Both sweeps run per workspace in a tenant transaction, as `reminders.Sweep` does.
- The selecting query includes `notify.ReadableExpr(n.user_id, n.note_id)` and a join to `core.memberships`. A row that fails is marked `emailed_at` and never sent.
- Marking rows and `mailer.Enqueue` happen in one transaction, so a rollback sends nothing and a retry cannot send twice. The digest claims its day with `UPDATE core.notify_prefs SET last_digest_on = $today WHERE last_digest_on IS DISTINCT FROM $today RETURNING` (an upsert when there is no row) in the same transaction.
- Delivery stays at-least-once at the SMTP step, as `worker/mailjob.go` documents.

What an email may contain is in section 9.2.

**Web.**

- Workspace settings gets a "Notifications" tab: three radio choices with a one-line explanation each, and the digest hour. If email is not configured on the server, the tab says so instead. References: [Klaviyo notification rules](https://mobbin.com/screens/9c81deb8-8d8d-4954-9a35-060e2e2304c1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for "immediately" against "on a daily basis" with a time, and [Sprig notification settings](https://mobbin.com/screens/9877b570-9f0b-4ddc-8142-957e8593f708?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for one plain card per choice.
- The bell learns the two new kinds and gets a link to the settings.

**Mobile.** None. The email links open the web page.

**Tests.** A mention made and read within three minutes sends nothing; ten mentions send one email; a person in `digest` mode gets no direct mail; `off` gets none; access removed between the notification and the sweep sends nothing and names nothing; a person who left the workspace gets nothing; the digest runs once a day under two concurrent sweeps and after a retry; an empty digest is not sent; the digest lists overdue tasks only from readable pages; a title with CR or LF cannot inject a header; a title with HTML is escaped in the HTML part; no email contains body or comment text.

### 4.4 Tasks with assignee and due date, and "My tasks"

**Today.** To-do lists exist as checkboxes. There is no assignee, no due date and no list across pages. The `assigned` notification kind exists and is used by Projects.

**Design.**

- A task is a to-do item. Its **assignee** is the first mention in the item. Its **due date** is the first date chip in the item. Both are forms A1 already stores:

  ```markdown
  - [ ] Send the GST summary [@Ravi Kumar](/w/<ws>/people/<user>) <time datetime="2026-12-01">1 Dec 2026</time>
  ```

- A client that knows nothing about tasks shows a to-do with a name and a date, which is correct.
- The server keeps an index, `core.note_tasks`, rebuilt for a page whenever its content changes. "My tasks" reads the index.
- A worker consumer on `note.created` and `note.updated` (with `content` in `changed`) parses the body with a pure Go function, `notes.Tasks(md, ws)`, and replaces the page's rows. Gateway snapshots emit the same event, so co-edited pages are covered. Only plain pages are indexed.
- When a rebuild finds a task whose assignee and text were not in the previous rows, and the assignee is not the actor and can read the page, it sends an `assigned` notification. This does not depend on the mention path.
- "My tasks" opens the page at the task. Ticking is done on the page.

**Left out:** ticking a task from "My tasks" (it needs a gateway edit endpoint; `shortcut: open the page to tick; add POST /internal/task on the gateway if people ask`), tasks assigned by me, a per-space task report, more than one assignee, reminders other than the digest lines in 4.3.

**Stored form and fixtures.** No new node or mark. Lead adds fixtures `task-assignee-due` (assignee and date, date only, assignee only, a nested task, a ticked task) and `task-lookalike` (a mention in a normal bullet is not a task). The existing round-trip tests must pass for them in both converters. If the gateway's task path drops the date mark or the link, that is a converter bug fixed in `md.ts` with no schema change.

**Data model (1337).**

`core.note_tasks (workspace_id, note_id, ord integer, text text, assignee_id uuid, due date, done boolean NOT NULL, PRIMARY KEY (note_id, ord))`, with a composite foreign key `(workspace_id, note_id)` to `core.notes` (cascade), tenant RLS, and an index `(workspace_id, assignee_id, done, due)`.

`ord` is the task's position in the document. `text` is plain text, at most 300 characters, with the mention and the date removed. An assignee who is not a member of the workspace is stored as null. A one-off worker sweep backfills pages that contain `- [`, in batches of 500, in the style of `SearchPlainBackfill`.

**Commands.**

| Command | Input | Output | Group, role |
|---|---|---|---|
| `task.mine` | `{done?: false, limit?: 100 (max 200)}` | `[{note_id, note_title, icon, space_id, ord, text, due, done}]`, overdue first, then by due date, undated last | `read`, guest |

There is no write command: tasks are written by editing the page.

**Access.** `task.mine` joins `core.notes` and applies `deleted_at IS NULL`, `freshness <> 'archived'` and `core.note_access(...) <> 'none'` inside the query, before the limit. A task on a page the assignee can no longer read is not listed and not counted. The consumer checks `notify.ReadableExpr` before it sends `assigned`.

**Web.**

- In the editor, a focused task item shows two small buttons at its end, "Assign" (opens the `@` picker) and "Due" (opens the date picker). The slash menu's to-do entry is unchanged.
- A "My tasks" entry in the sidebar under Recent and Starred, at `w/[ws]/tasks`. Groups: Overdue, Today, This week, Later, No date. Each row shows the task text, the page with its emoji, and the due date. A "Show completed" toggle. References: [Quo tasks](https://mobbin.com/screens/58ece968-d3e6-48b4-a975-912ddde3d491?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the "Past due" and "Due soon" chips beside the name, and [incident.io My tasks](https://mobbin.com/screens/0090294e-68ed-4851-a5c9-52475f30d3e8?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the open count and the "Show completed" switch.

**Mobile.** The raw Markdown editor shows the form above as text and keeps it. `task.mine` is available to the app but no screen is required in B.

**Tests.** The parser against the fixtures; a mention in a normal bullet is not a task; rebuild after a gateway snapshot; removing a task removes its row; an assignee who is not a member is null; `assigned` is sent once for a new task and not again when the page is edited elsewhere; the actor is not notified; a restricted page's tasks are missing from `task.mine` for someone without access, before the limit; a guest sees only tasks on pages shared with them. e2e: assign a task with a due date, the assignee sees it under Today and opens the page at the task.

### 4.5 Comment edit, delete and reactions

**Today.** `comment.add`, `comment.list` and `comment.resolve` in `notes/comments.go`. A comment cannot be changed or removed, and there are no reactions.

**Design.**

- The author can edit a comment. It then shows "edited".
- The author can delete a comment. So can the page owner, a space admin and a workspace admin. The body is erased at once. A top-level comment that still has replies stays as a "Comment deleted" line so the thread holds together; any other deleted comment disappears.
- Six reactions: 👍 ❤️ 😂 🎉 👀 ✅. A person can add each once per comment.
- Editing a comment notifies people newly mentioned in it. Edits, deletes and reactions notify nobody else.

**Left out:** reactions on pages, a custom emoji picker, names of who reacted (count and "mine" only), edit history of a comment.

**Data model (1337).**

- `core.comments` gains `edited_at timestamptz`, `deleted_at timestamptz`, `deleted_by uuid`.
- `core.comment_reactions (workspace_id, comment_id, user_id, emoji text CHECK (emoji IN (...the six...)), created_at, PRIMARY KEY (comment_id, user_id, emoji))`, composite foreign key `(workspace_id, comment_id)` to `core.comments` (cascade), tenant RLS.

**Commands.**

| Command | Input | Output | Group, role | Checks |
|---|---|---|---|---|
| `comment.edit` | `{id, body}` | The comment | `write_notes`, guest | Page view access; the caller is the author; the comment is not deleted |
| `comment.delete` | `{id}` | `{deleted: true}` | `write_notes`, guest, `Confirm` | Page view access; the caller is the author, or the page owner, a space admin or a workspace admin |
| `comment.react` | `{id, emoji, on}` | `{id, reactions}` | `write_notes`, guest | Page view access; the comment is not deleted; a person, not a bare key |

`comment.list` adds `edited_at`, `deleted` and `reactions: [{emoji, count, mine}]` to each comment, leaves out deleted comments with no live reply, and returns an empty body for a tombstone. Events: `comment.edited {comment_id}` and `comment.deleted {comment_id, by_author}`. Neither payload carries text. Reactions emit nothing.

**Access.** Every command resolves the comment to its page and calls `AssertNoteAccess` first, and a failure is `not_found`. So a person who lost access to the page can no longer edit, delete or react, and cannot tell whether the comment exists. Reaction counts are computed in the `comment.list` query, which already sits behind page access.

**Web.** In `comments-panel.tsx`: a "..." menu on your own comments with Edit and Delete, and Delete on others' for people who may moderate; an inline edit box; a row of reaction chips under each comment with a "+" that opens the six. References: [Canva comment thread](https://mobbin.com/screens/977e41d5-d8d5-46f5-803f-2b3936500f27?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the reaction bar and the per-comment menu, and [Productboard comments](https://mobbin.com/screens/071f04dc-4e43-482f-9f0d-81e654c68d70?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the count chip with "+".

**Mobile.** None. The app has no comments yet (Phase D).

**Tests.** Only the author edits; a moderator deletes but cannot edit; the deleted body is gone from the database row; a tombstone appears only while a reply lives; a reply to a tombstone still works; resolving a thread with a deleted root works; a reaction is unique per person and emoji; an emoji outside the list is refused; a person without page access gets `not_found` for all three; notification rows that point at a deleted thread still open the page.

## 5. Release B2

### 5.1 Request access

**Today.** An unreadable page shows "does not exist or you do not have access" (`n/[id]/page.tsx:128`).

**Design.**

- That screen gains a "Request access" button and an optional one-line message. The button is there whether or not the page exists.
- `note.request_access` always answers `{ok: true}`. If the page exists, is live, and the caller cannot read it, the people who can grant access get an `access_request` notification, which by 4.3 is also an email.
- Who is asked: the owner of the page (`owner_id`, else `created_by`) when they can still edit it; otherwise the owner of the nearest view-restricting ancestor; and when the block is a private space, that space's admins. At most five people.
- Granting is the existing `note.share`, or `space.member_set`. The notification opens the page's share dialog with the requester filled in. `note.share` now sends the requester a `shared` notification, which closes the loop.
- There is no request table and no pending list.

**Left out:** a list of pending requests, a "decline" action with a message, requesting edit when you can already view.

**Commands.**

| Command | Input | Output | Group, role |
|---|---|---|---|
| `note.request_access` | `{note_id, message?: at most 200 characters}` | `{ok: true}` always | `write_notes`, guest |

**Access and abuse.**

- The answer is the same for a missing page, a page in the trash, a page the caller can already read and a page they cannot. Nothing about the page is returned.
- One request per requester, page and recipient per day, using the existing `(user, source, source_day)` unique index with source `access:<note>:<requester>`.
- At most 10 requests per requester per hour (`db.Bump`), so page ids cannot be sprayed to pester owners. Over the limit the answer is still `{ok: true}`.
- Page ids are random UUIDs, so guessing one is not practical. Timing differences between the cases are accepted for that reason and written down here.
- The requester's message is shown as plain text.

**Web.** The not-found screen as above; after sending, "If this page exists, its owner has been asked." The bell row for `access_request` reads "<name> asked for access to <title>" with a "Share" button. Mobbin returned no true request-access screen; the closest was [Spellbook manage access](https://mobbin.com/screens/74839182-4752-440b-800a-5de8a2b1d0c6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), which matches the share dialog the owner lands in.

**Mobile.** None.

**Tests.** The response is byte-identical for the four cases; a second request the same day creates nothing; the hourly cap; recipients for an own restriction, an inherited one and a private space; a recipient who cannot read the page is never chosen; the notification title is visible only to the recipient; after `note.share` the requester is notified.

### 5.2 File attachments in pages

**Today.** The backend is complete (section 2). The A1 editor uploads images through it. Nothing in the web lets a person attach a PDF or see a page's files.

**Design.**

- An attachment in the text is an ordinary link whose target is one of the page's files:

  ```markdown
  [Price list 2026.pdf](/api/w/<ws>/files/<id>)
  ```

- The editor draws a link of that shape as a file chip (icon, name) through a decoration. Nothing new is stored, so an older tab, the gateway and the mobile editor all show a working link.
- Upload by the slash menu ("File"), the toolbar, drag and drop, and paste. An image becomes an image as today; a PDF becomes a file link.
- An "Attachments" panel on the page lists every file of the page with size, uploader and date, and offers download, "insert into page" and delete. Deleting a file that the text still uses warns that the link or image will break.
- Types stay JPEG, PNG, GIF, WebP and PDF. Office files stay refused: Tables 18.8 deferred them until there is a package validator, a download warning and a separate download origin, and none of that is in Phase B.
- A plain page may hold 50 files. Other notes keep 10. The 10 MB size stays.

**Left out:** Office, CSV and zip attachments; inline PDF preview; image resize, caption and alignment (matrix row, moves to Phase C); automatic removal of files the text no longer uses; storage quotas.

**Stored form and fixtures.** No new node or mark. Lead adds fixture `file-link` (a file link in a paragraph, in a list and in a merged table) with lookalikes that must stay ordinary links: `/api/files/<id>` and a file path under another workspace id. The plain-text extractor keeps the file name, so search finds a page by the name of its attachment.

**Data model.** None. `files.maxPerNote` becomes a function of the note: 50 when `types = '{}'`, else 10.

**Commands.** The existing four. `file.list` output gains `created_by_name`.

**Access.** Unchanged and already inside the commands: `guard` calls `AssertNoteAccess` and the finance rules. A file link pasted into another page works only for readers of the page that owns the file, which is the right answer. Duplicate (A2) already copies files and rewrites these paths, because it rewrites the path text.

**Web.** The panel sits in the page's right rail with comments and versions. References: [Square attachments list](https://mobbin.com/screens/177ad118-6e5e-4426-bf0c-da3b20d17ba6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for name, upload time and size on one row, and [Productboard upload block](https://mobbin.com/screens/8ab94932-4f11-4526-8665-250ec601b9b1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for the Upload and Link tabs.

**Mobile.** A file link is a normal Markdown link in the raw editor. Opening it needs a session, so it opens in the in-app browser. No change required.

**Tests.** The fixture in both converters and the extractor; a PDF upload inserts a link and the chip renders; the 51st file is refused; a typed note still stops at 10; a reader without page access gets 404 for the file; deleting a used file warns; export (A2) leaves file links as links.

### 5.3 Page link picker

**Today.** A link is typed into `window.prompt` (`editor/menus.tsx` `promptLink`).

**Design.**

- Typing `[[` opens a picker that searches page titles, in the style of the `@` picker. Choosing a page inserts an ordinary link, `[Title](/w/<ws>/n/<id>)`.
- The link dialog replaces the prompt: one box that takes a URL or searches pages.
- The search is `search.notes` with `title_only` (A1), which is already filtered by access.

**Left out:** link text that follows a renamed page (`shortcut: the title is copied when the link is made; upgrade to a page-mention node, with a schema bump, if stale titles become a complaint`), backlinks, links to a heading.

**Stored form and fixtures.** No new node or mark. Lead adds fixture `page-link`, with the text `[[` left alone inside code and when no page is chosen.

**Data model and commands.** None.

**Access.** The picker lists only pages the writer can read. A reader who cannot read the target gets the normal not-found screen, and from B2 the request button.

**Web.** Reference: [Plane inline picker](https://mobbin.com/screens/37e5ad48-6e1d-436e-8169-2280c6575f1b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) for a trigger typed in the text that opens a filtered list under the caret.

**Mobile.** A normal Markdown link. No change.

**Tests.** `[[` then a title inserts the link; Escape leaves the text; the picker never shows a restricted page; `[[` inside a code block does nothing; the link dialog accepts a pasted URL unchanged.

### 5.4 Child pages block

**Today.** Absent. `[[toc]]` from A1 is the same kind of thing: an atom node stored as a marker line and drawn by a node view.

**Design.**

- A block that lists the page's direct sub-pages, in tree order (`position, created_at, id` from A2), each with its emoji and title.
- It lists what the reader can see. The data is the tree the shell has already loaded with `note.list`, so there is no new query and no new access rule.
- For editors, an empty block shows "No sub-pages yet" and "New sub-page".

**Left out:** depth beyond one level, excerpts, sort options, listing another page's children.

**Stored form.**

| Block | Editor node | Stored in `content_md` | Seen by a client that does not know it |
|---|---|---|---|
| Child pages | atom node `children`, no attributes | A line containing only `[[children]]` | The text `[[children]]` |

**Fixtures (lead).** `children-block` (alone, between paragraphs, inside a panel and inside an expand) and `children-lookalike` (the text inside a paragraph, inside inline code, and `[[ children ]]` with spaces, all of which stay text).

**Every converter, per 7.3 and 7.4.**

| Where | Change |
|---|---|
| `editor/blocks.ts` | The node, its Markdown tokenizer and renderer, the node view |
| `apps/collab/src/md.ts` | Parse and render, on the same path as `toc` |
| `notes/plaintext.go` | Strip the marker line |
| `docs/export.ts` (A2) | Replace the block with a static list of the visible titles, as it does for the table of contents. Export still contains no child page content |
| `publiclinks` `PublicBody` | Remove the line |
| `importers/html.go` | Map the Confluence `children` macro to the marker line |
| Mobile | Shows the marker as text. No change |

**Schema bump plan.**

1. `DOC_SCHEMA` goes from its value after A2 (2 today) to the next number, in `apps/collab/src/doc-schema.ts`; `notes.DocSchema` follows and `schema_test.go` keeps them equal.
2. The mobile app sends a literal `schema: 2` (`apps/mobile/src/components/pages.tsx:117`). It must ship first with the new number, or its saves get HTTP 426. Step 0 changes the literal to an import of the shared constant.
3. Release order as in 7.8: gateway with the minimum unchanged, API, web, then raise `COLLAB_MIN_SCHEMA` the same day. Inserting the block is disabled until the gateway reports the new minimum (`blocksEnabled()` in `blocks.ts` already does this).
4. The rollout rehearsal from A1 is repeated: an old tab open during the raise is closed with the reload message and changes nothing.

This bump is the whole schema cost of Phase B. A cheaper alternative exists and is recorded for the lead: show the sub-pages list as page chrome under the body of any page that has children, with no block, no bump and no mobile release. The lead may choose it at B2 step 0 if the mobile release cannot go first.

**Data model and commands.** None.

**Mobile.** As in the table; plus the release in step 2.

**Tests.** The fixtures in both converters, the extractor, export and the public body; the unknown-node rule holds (covered by the fence, the rehearsal proves it); the list hides a restricted child from a reader who lacks access and shows it to one who has it; order follows a reorder; a new sub-page appears without reload.

### 5.5 Manual archive

**Today.** Pages are archived only by the freshness sweep (`freshness = 'archived'`, `archived_at`). `doc.archived` lists them and `doc.restore` brings one back. Archived pages are already left out of `note.list`, search and the docs dashboard counts.

**Design.**

- `doc.archive` sets the same two columns by hand. No new state.
- It takes the page **and its live sub-pages**. Archiving a parent alone would leave its children at the space root, because the tree shows a page whose parent is not listed at the root.
- `doc.restore` gains `with_children`, which restores the archived pages below the one named.
- An archived page stays readable by its link, shows an "Archived" banner, and can still be edited. That is today's behaviour for auto-archived pages and it does not change.

**Left out:** an archive reason, archiving typed pages, auto-delete of old archives.

**Data model.** None.

**Commands** (`docs` module).

| Command | Input | Output | Group, role | Checks |
|---|---|---|---|---|
| `doc.archive` | `{id}` | `{archived: n}` | `write_notes`, member | Plain pages only. Edit access to every page of the subtree, taken under the space lock; at most 500 pages (the A2 subtree rule and message); any failure is the generic refusal |
| `doc.restore` | `{id, with_children?: false}` | As today, plus `restored: n` | `write_notes`, member | As today, plus edit access to every archived descendant it restores; those the caller cannot edit are skipped silently |

Event: the existing `doc.archived`, one per page, with `{reason: "manual", root_id}`.

**Access.** The subtree is read and checked under the lock, with rows taken `FOR UPDATE` in `id` order, exactly as A2's delete does. A hidden page in the subtree gives the generic refusal and names nothing. An archived page's public link stops resolving (4.2, resolver).

**Web.** "Archive" in the page "..." menu with a confirmation that states the number of sub-pages. The docs dashboard's Archived list gets "Restore with sub-pages".

**Mobile.** Archived pages already leave `note.list`. Nothing to do.

**Tests.** A subtree is archived in one call; a hidden descendant refuses the whole call with the generic text; a typed page is refused; restore with and without children; a racing move into the subtree is serialised by the lock; the page's public link answers 404 while archived and works again after restore.

### 5.6 Page views byline

**Today.** `note.viewed` records the caller's last opening in `core.note_views`, and the page header calls it on open. Nothing shows the result.

**Design.**

- The byline under the title gains "Seen by 7".
- People who can edit the page can open the list: names and when each person last opened it.
- The count is the number of current workspace members with a row. There is no total view count, as A1 decided.

**Left out:** views over time, a "has everyone read this" report (read acknowledgement is Phase D), views through public links (those are in the share dialog).

**Data model.** None.

**Commands.**

| Command | Input | Output | Group, role |
|---|---|---|---|
| `note.viewers` | `{note_id}` | `{count, viewers?: [{user_id, name, last_viewed_at}]}`, newest first, at most 100 | `read`, guest |

**Access.** Page view access for the count. `viewers` is present only when the caller has edit access. People who left the workspace are not counted or listed.

**Web.** In `page-header.tsx`, beside owner and status, as in the [Confluence byline](https://mobbin.com/screens/f3d6c865-feec-4563-839f-1162b0aa8fdc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) already cited in the research file. The list is a small popover; [Canva visitor list](https://mobbin.com/screens/c567db9c-6b9e-4555-a190-2ccb8798e6f9?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) shows the row shape (person, last visited).

**Mobile.** None. The app does not call `note.viewed` today, so phone readers are not counted until Phase D.

**Tests.** The count ignores former members; a viewer-only reader gets the count and no names; a guest gets the count only; a page with no views shows nothing.

### 5.7 Word (.docx) import

**Today.** Importers take pasted text for Confluence, Notion, Trello, Jira and CSV, are admin only, and convert HTML to Markdown in `importers/html.go`. Pasting from Word into the editor already keeps structure.

**Why Office files were deferred, and why this is different.** Tables 18.8 put Excel and Word **attachments** off until there is a hardened package validator, a formula policy, a download warning and a separate download origin. Those are risks of storing an Office file and serving it back to other people. Import does neither: the file is read once on the server, turned into a page, and dropped. What remains is the risk of parsing a hostile file, which the limits below address.

**Design.**

- One `.docx` becomes one page. Any member who can create a page in the space may import.
- The server reads the file with the Go standard library only (`archive/zip`, `encoding/xml`), builds HTML, and passes it through the existing `htmlToMD`. No new dependency.
- Embedded pictures are uploaded as page files through `file.upload`, so the normal type check applies.
- The result is ordinary blocks. No new node.
- The answer lists what was dropped, and the web shows it once.

| Kept | Dropped, with a warning |
|---|---|
| Headings, paragraphs, bold, italic, underline, strike, links (`http`, `https`, `mailto`), bulleted and numbered lists with nesting, simple tables, line breaks, JPEG, PNG, GIF and WebP pictures (at most 20) | Merged table cells (flattened), footnotes, comments, tracked deletions, headers and footers, text boxes, EMF, WMF and SVG pictures, embedded objects, fields |

**Left out:** `.doc`, `.docm`, `.dotx`, several files at once, a folder, Google Docs (export it as `.docx`), styles and colours, page layout.

**Commands.**

| Command | Input | Output | Group, role |
|---|---|---|---|
| `import.docx` | `{space_id, parent_id?, filename, data_base64}` | `{id, title, warnings: [string]}` | `write_notes`, member |

A raw endpoint, `POST /api/w/{ws}/import/docx?space_id=&parent_id=&filename=`, wraps it for the browser, in the way `server/files.go` wraps `file.upload`. The title is the first Title or Heading 1 paragraph, else the file name without its extension. The page is created with `c.Call("note.create")`, which carries the access check, the structural lock and the position trigger.

**Hostile-file limits.**

| Risk | Rule |
|---|---|
| Size | 10 MB, refused from `Content-Length` before reading, and the body is read through a limit |
| Zip bomb | At most 2,000 entries. Every entry is read through a limit that does not trust the declared size: `word/document.xml` 30 MB, relationship and numbering parts 5 MB, each picture 10 MB, 100 MB in total |
| Not a Word document | `[Content_Types].xml` must declare the `wordprocessingml.document.main+xml` type and `word/document.xml` must exist. Macro-enabled types, templates and a `vbaProject.bin` entry are refused. A file that is not a zip (legacy `.doc`, an encrypted file) is refused. The file name and extension decide nothing |
| XML attacks | `encoding/xml` does not load DTDs or external entities, and an undefined entity is an error. Parsing is a token loop, with a depth limit of 256, a token limit, and a 10-second deadline |
| Server-side requests | A relationship with `TargetMode="External"` is never fetched. An external picture is dropped. An external link is kept as link text and target only |
| Smuggled HTML | `altChunk` parts, embedded objects and field instructions are ignored. Every text run is HTML-escaped before `htmlToMD`, so text that looks like a tag stays text |
| Storage | The `.docx` is never stored and never served |
| Load | 20 imports per person per hour (`db.Bump`) |

**Access.** `AssertSpaceAccess(write)` on the space and, when given, edit access to the parent, both through `note.create`. Pictures go through `file.upload` on the new page.

**Web.** "Import Word file" in the template gallery's footer and on the space page. A file picker limited to `.docx`, a progress line, then the new page opens with the warnings in a dismissible notice.

**Mobile.** None.

**Tests.** A real document with every kept element; each refused kind (a `.docm`, a zip renamed `.docx`, a legacy `.doc`, a template); a zip bomb whose declared sizes lie; deeply nested XML; an external picture relationship with a listener that fails the test if it is contacted; an `altChunk` with a script; a text run containing `<script>`; a picture that is really SVG; the 21st picture; a viewer of the space cannot import; a fuzz target over the parser with these files as its seed corpus.

### 5.8 Template prompts

**Today.** Template placeholders are bound to records (`{{deal.title}}`), and a missing one is left in the text and reported as unresolved. There is no way to ask the person for a value.

**Design.**

- A placeholder in the `ask.` namespace is a prompt: `{{ask.meeting_date}}`, `{{ask.attendees}}`. The label is the key with underscores turned into spaces and a capital first letter.
- "Use template" shows one text box per prompt before it creates the page. An unanswered prompt stays in the page as its placeholder, as unresolved fields do today.
- Answers are inserted as plain text. Markdown and HTML characters in an answer are escaped, so an answer cannot add a link, a block or a tag.
- Answers are stored with the page's template binding, because `template.refresh` renders the page again from the template and would otherwise lose them.
- The twelve built-in templates gain prompts where they have obvious blanks (meeting notes: date and attendees).

**Left out:** typed prompts (date, multi-line, list), default values, required prompts, instruction text that disappears when typed over.

**Data model (1339).** `docs.template_bindings.answers jsonb` (nullable).

**Commands.**

| Command | Change |
|---|---|
| `template.list` | Each template gains `prompts: [{key, label}]`, at most 20, in order of first appearance in title and body |
| `template.render` | Accepts `answers: {key: string}`. Each answer is at most 500 characters and one line. Unknown keys are refused. Writes the binding row with the answers when there are any |
| `template.refresh` | Substitutes the stored answers again |
| `template.save` | Unchanged. The save dialog gets a line explaining `{{ask.name}}` |

Groups and roles are unchanged.

**Access.** Unchanged: the gallery and render keep their space filtering (7.6). Answers live on the binding of a page and are read only by `template.refresh`, which already requires edit access to that page.

**Web.** In `templates/use-template.tsx`, a "Fill in" step when the chosen template has prompts.

**Mobile.** None.

**Tests.** Prompts are found in title and body; an answer with `**`, `[x](y)`, `<b>` and a pipe inside a table cell comes out as text; an unanswered prompt is listed as unresolved; refresh keeps the answers; a 501-character answer is refused; a built-in template with prompts renders.

## 6. Roles, guests and AI scope at a glance

"AI" is the read-only AI scope. "Confirm" means an agent or API key call waits for a person's approval, as `note.share` does today.

| Command | Release | Group | Min role | Guest | AI | Confirm |
|---|---|---|---|---|---|---|
| `space.members` | B1 | read | member | no | yes | |
| `space.member_set` | B1 | write_notes | member (space admin) | no | no | yes |
| `space.update` | B1 | write_notes | member (space admin) | no | no | yes |
| `space.archive` | B1 | write_notes | member (space admin) | no | no | yes |
| `page_link.get` | B1 | read | member | no | yes | |
| `page_link.create` | B1 | send_messages | member | no | no | yes |
| `page_link.revoke` | B1 | write_notes | member | no | no | |
| `page_link.list` | B1 | read | member (space admin) | no | yes | |
| `notify_prefs.get` | B1 | read | guest | yes | yes | |
| `notify_prefs.set` | B1 | write_notes | guest | yes | no | |
| `task.mine` | B1 | read | guest | yes | yes | |
| `comment.edit` | B1 | write_notes | guest | yes, own | no | |
| `comment.delete` | B1 | write_notes | guest | yes, own | no | yes |
| `comment.react` | B1 | write_notes | guest | yes | no | |
| `note.request_access` | B2 | write_notes | guest | yes | no | |
| `note.viewers` | B2 | read | guest | count only | yes | |
| `doc.archive` | B2 | write_notes | member | no | no | |
| `import.docx` | B2 | write_notes | member | no | no | |
| `template.render` (answers) | B2 | write_notes | as today | as today | no | |

Agents and API keys have the same page and space checks as the person they act for. Commands that belong to a person (`notify_prefs.*`, `comment.react`, `task.mine`) refuse a principal with no user. Refusals are written as `command.denied` by the gate; `core.events` is the audit trail and no new audit table is added.

## 7. Data model in one place

| Migration | Contents |
|---|---|
| `1337_docs_parity_b1.sql` | `core.spaces`: `description`, `archived_at`, `public_links`. `core.public_links`: purpose `page` (`NOT VALID`). Functions `core.resolve_public_link` (recreated) and `core.resolve_page_link` (new). `core.notifications`: `emailed_at`, kinds `shared` and `access_request` (`NOT VALID`). Tables `core.notify_prefs`, `core.note_tasks`, `core.comment_reactions`. `core.comments`: `edited_at`, `deleted_at`, `deleted_by` |
| `1338_docs_parity_b1_indexes.sql` | `CONCURRENTLY`: the one-active-page-link unique index; the un-emailed notifications index |
| `1339_docs_parity_b2.sql` | `docs.template_bindings.answers` |

Every new table has tenant RLS, composite foreign keys that carry `workspace_id` (`TestQAEveryTenantForeignKeyIncludesWorkspace`) and explicit grants to `cnote_app`. The two `VALIDATE CONSTRAINT` statements go into the release after B1 and are noted in `docs/deploy.md`.

## 8. Fixtures and schema, in one place

| Release | Fixture | Covers | New node or mark | `DOC_SCHEMA` |
|---|---|---|---|---|
| B1 | `task-assignee-due`, `task-lookalike` | Tasks | None | Unchanged |
| B2 | `file-link` | Attachments | None | |
| B2 | `page-link` | Page links | None | |
| B2 | `children-block`, `children-lookalike` | Child pages | Node `children` | Plus one |

Each fixture has `md` and `plain`, as the A1 fixtures do, and is exercised by `apps/collab/test/fixtures.test.ts`, `apps/web/src/test/fixtures.test.ts`, `notes/plaintext_test.go` and `importers/blocks_test.go`.

## 9. Security

### 9.1 Public page links

**Token.**

- 32 bytes from `crypto/rand`, base64url without padding, 43 characters: 256 bits. Public forms use 128; this reuses the existing `newToken`.
- Only `hex(sha256(token))` is stored, under a unique index. A lost link cannot be shown again, only replaced.
- The token appears in the URL and in the one command result marked `ReturnsSecret`. It is never in an event, a log line, an error, the idempotency table or analytics.
- Guessing is impractical at this size. Identical 404 bodies do not hide timing differences; that residual is accepted, as in Tables 18.8.

**Revocation.** A link stops serving at once when any of these happens, and each has a test:

| Cause | How |
|---|---|
| "Turn off" or "Create new link" | `revoked_at` set; the resolver returns nothing |
| The space switch is turned off | Every active page link of the space is revoked in the same transaction, and the resolver checks the flag as well |
| The space is archived; the page is trashed, archived or becomes a typed page | Resolver |
| A view restriction on the page or an ancestor | Check in the request transaction; fails closed past depth 50 |
| The creator left, became a guest, or lost edit access to the page | Check in the request transaction |
| Expiry, when one was set | Resolver |

The request transaction reads the link row `FOR SHARE` and checks the presented hash and `revoked_at` again; revoke and replace take it `FOR UPDATE`. A request that began before a revoke therefore fails with the one 404.

**The single 404.** Malformed token, unknown, revoked, replaced, expired, switched off, archived, trashed, restricted, creator gone, and a file id that is not this page's all answer the same status, the same headers and the same bytes: `{"error":{"code":"not_found","message":"This page is not available."}}` with `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex, nofollow`. A test compares them byte for byte.

**Order of work for both public routes**, taken from Tables 18.2 and 18.8:

1. A method other than GET: 405.
2. The client address through `sites.ClientIP(r, sites.TrustedProxies())`, hashed with the shared master key. No master key means 503, never a per-process key.
3. Bump the per-address counter, before any token work and for malformed tokens too. Refuse with 429 over the limit or when the miss counter is full.
4. Token not 43 characters of `[A-Za-z0-9_-]`: bump the miss counter, the 404.
5. Resolve. No row: bump the miss counter, the 404.
6. Bump the per-link counter. Then the tenant transaction and the checks above.

Counters use `db.Bump` and `db.Peek` on the pool in their own statements, so they are shared by all replicas and a failed request still counts. Step 0 moves the address-key helper out of `server/tablesform.go` into a shared file so forms and pages use one copy.

| Counter | Limit per hour | Purpose |
|---|---|---|
| `pg:ip:<address>` | 300 page requests | One reader |
| `pg:ipf:<address>` | 1,500 file requests | A page's images are separate requests |
| `pg:miss:<address>` | 20 | Token guessing |
| `pg:tok:<link>` | 2,000 page requests | One link going viral or being scraped |
| `pg:tokf:<link>` | 20,000 file requests | The same, for its files |
| `pg:all` | 20,000 page requests | The platform |

The numbers are placeholders for the lead to tune. Startup logs a warning when `PUBLIC_TRUSTED_PROXIES` is empty, as the forms endpoint does.

**What a reader can never get.** Comments; child pages and any restricted page; files of other pages; files of this page that the text no longer uses; names behind mentions as links; ids of the workspace, people or other pages; history; who viewed. Section 4.2 has the full table. Three layers enforce the file rule: the body rewrite, the file route's own ownership check, and the public renderer, which accepts only the public file path of its own token.

**Script and content safety.** The public page renders Markdown through the same validated converter as the app. There is no raw HTML path. The page loads no third-party script and no analytics. External images are kept, so a reader's browser contacts those hosts, as it would in the app.

**Abuse.** A public page is text under our domain that anyone can be sent to, so it can carry a scam. The footer names the workspace and has "Report abuse" (`shortcut: a mailto to ABUSE_EMAIL; move to the Sites abuse queue of migration 1310 if reports arrive`). An environment switch, `PUBLIC_PAGES=0`, makes every page link answer the 404.

**Tests that must exist.** The byte-identical 404 for every cause above; limits run before the token is looked at; spoofed and malformed `X-Forwarded-For` behind a trusted and an untrusted proxy; 503 without the master key; the golden key set of `PublicPage`; the body rewrite over the fixtures plus a page with another page's image, a mention, a page link, the same inside a merged table, and `[[children]]`; a file of another page and an unused file of this page are refused; PDF is served as an attachment; the token is absent from events and logs; the definer function run as `cnote_app` with no tenant returns one row for a good hash and none otherwise; the billing endpoint answers 404 for a page token and the page endpoint for an invoice token; revoke racing a request; RLS and composite-FK sweeps. e2e: create a link, open it signed out at 390 px, an image shows, turn it off, the same URL says unavailable.

### 9.2 Email privacy

- **Checked when built.** A notification is put in an email only if, in the sweep's own transaction, the recipient is still a member and can still read the page (`notify.ReadableExpr`). This is the third check after send and list. A row that fails is marked handled and never mentioned, not even as a count.
- **No content.** An email holds the actor's name, a fixed sentence for the kind, the page title, and a link. It never holds page text, comment text or a file. So nothing in an inbox outlives a later loss of access except a title the person could read on the day it was sent. An email already delivered cannot be recalled; this is stated in the settings tab.
- **Restricted pages.** For a page under a view restriction or in a private space, the subject line leaves the title out ("Ravi mentioned you on a restricted page"). The title is in the body only. A digest subject never contains a title ("3 updates in <workspace>").
- **Space notifications** have no page to check. A person added to a space and removed again within the five-minute window may still be told they were added. Only the space name is in that mail.
- **Batching.** Direct mail is grouped per person per sweep. The digest is one mail per person per workspace per day, claimed in the same transaction as the outbox job, capped at 30 lines, and skipped when empty.
- **Injection.** Titles and names go through the mailer's one-line rule in headers and are HTML-escaped in the HTML part. Links are built from `PUBLIC_WEB_URL` and ids only.
- **Tasks in the digest** come from `core.note_tasks` joined to the same access predicate.
- **Agents.** When an agent acts for a person, the mail names the person, as the bell does.

### 9.3 Other points

- **Space membership** is the widest grant in this phase. `space.member_set` and `space.update` need confirmation from agents, write events, and re-check the admin rule under a row lock.
- **Request access** cannot be used to learn whether a page exists (5.1).
- **Word import** limits are in 5.7.
- **Comment deletion** erases the body in the row; the event carries no text.
- **Viewer names** are shown to editors only (5.6).

## 10. Releases and work packages

Each release starts with a step 0 by the lead, then packages in parallel with disjoint files, then integration by the lead. A file not listed for a package belongs to the lead (section 3, rule 11).

### 10.1 B1

**Step 0 (lead).**

- Migrations 1337 and 1338.
- Command stubs with schemas and roles, in the A1 pattern: `notes/b1_commands.go` (`space.*`, `comment.edit`, `comment.delete`, `comment.react`, `task.mine`), and stub registrations in `publiclinks` and `notifications`. Web types and wrappers in `lib/docs.ts`.
- `access.AssertSpaceAdmin`, the archived rule in `AssertSpaceAccess`, and the same in `apps/collab/src/access.ts`. `space.list` fields in `notes/notes.go`.
- The shared public limiter file under `server/`.
- Fixtures `task-assignee-due` and `task-lookalike`.
- `worker/wire.go`: the task consumer, the two sweeps, the backfill sweep; `mentionEmail` line removed.
- Notification kinds `shared` and `access_request` known to `notify` and to `lib/docs.ts`.

| | P1 Spaces | P2 Public links | P3 Email and digest | P4 Tasks | P5 Comments |
|---|---|---|---|---|---|
| **Creates** | `notes/spaces.go`, `components/docs/space-settings.tsx`, `app/w/[ws]/s/[id]/settings/page.tsx`, `server/docs_b1_spaces_test.go`, `e2e/space-members.spec.ts` | `publiclinks/page.go`, `publiclinks/pagebody.go` and its test, `server/pagepublic.go`, `server/pagepublic_test.go`, `app/pg/[token]/page.tsx`, `components/docs/public-page.tsx`, `components/share/public-link.tsx`, `components/docs/space-public-links.tsx`, `e2e/public-page.spec.ts` | `worker/notifymail.go` and its test, `notifications/prefs.go`, `components/notification-settings.tsx`, `server/docs_b1_email_test.go` | `notes/tasks.go` and its test, `worker/pagetasks.go`, `components/editor/task-controls.tsx`, `components/docs/my-tasks.tsx`, `app/w/[ws]/tasks/page.tsx`, `server/docs_b1_tasks_test.go`, `e2e/tasks.spec.ts` | `notes/reactions.go`, `server/docs_b1_comments_test.go`, `e2e/comment-actions.spec.ts` |
| **Edits** | `app/w/[ws]/s/[id]/page.tsx`, `components/shell.tsx` (space menu entry, archived spaces hidden, the "My tasks" link for P4), `components/space-dialog.tsx` | `share/share-dialog.tsx` (mounts the row), `components/docs/page-header.tsx` (the Public chip), `apps/web/next.config.ts`, `publiclinks/links.go` (registration only) | `worker/mail.go` (remove `mentionEmail`), `components/notifications.tsx`, `app/w/[ws]/settings/page.tsx` (mounts the tab), `sharing/sharing.go` (`shared` notification in `note.share`) | `editor/slash.tsx`, `components/note-editor.tsx` (mounts the controls), `apps/collab/src/md.ts` only if the fixture fails | `notes/comments.go`, `components/comments-panel.tsx` |
| **Must not touch** | Editor, comments, publiclinks | Editor, shell, worker | Editor, shell, `pagenotify.go` | Shell, comments, notifications | Editor, shell, worker |
| **Depends on** | Step 0. Calls `page_link.revoke_space` by its stub signature. Mounts `<SpacePublicLinks>` from P2's file in its settings page | Step 0. Reads `spaces.public_links` | Step 0. Reads `core.note_tasks`, which P4 fills | Step 0. P1 adds its sidebar link | Step 0 |

**Integration (lead).** Merge P1, then P2, P5, P4, P3. Full e2e. Check the three cross-package paths: turning the space switch off kills a live link; removing a space member stops their link, their watch mails and their task rows from showing; a task assigned in a co-edited page reaches the assignee's bell and, three minutes later, their inbox.

**Release order.** API and worker, then web. No gateway change beyond the `access.ts` line, no schema raise, no mobile release.

### 10.2 B2

**Step 0 (lead).**

- Migration 1339.
- `DOC_SCHEMA` and `notes.DocSchema` raised; the mobile literal replaced by the shared constant; the mobile release cut first.
- Fixtures `file-link`, `page-link`, `children-block`, `children-lookalike`.
- Command stubs: `note.request_access`, `note.viewers`, `doc.archive`, `import.docx`; the `answers` field in the `template.render` schema; web types in `lib/docs.ts`.
- The raw route for `import/docx` in `server/server.go`.
- Mount points in `app/w/[ws]/n/[id]/page.tsx` for the attachments panel and the request button.

| | Q1 Access, archive, views | Q2 Editor: attachments, page links, child pages | Q3 Word import and template prompts |
|---|---|---|---|
| **Creates** | `sharing/request.go`, `docs/archive.go`, `notes/viewers.go`, `components/docs/request-access.tsx`, `components/docs/viewers.tsx`, `server/docs_b2_access_test.go`, `e2e/request-access.spec.ts`, `e2e/archive.spec.ts` | `components/editor/file-link.ts`, `components/editor/page-link.tsx`, `components/docs/attachments-panel.tsx`, `apps/web/src/test/b2-blocks.test.ts`, `e2e/attachments.spec.ts`, `e2e/page-link.spec.ts`, `e2e/children.spec.ts` | `importers/docx.go`, `importers/docx_test.go`, `importers/testdata/docx/*`, `modules/importers/docx.go`, `server/importdocx.go`, `components/import/docx-button.tsx`, `server/docs_b2_import_test.go`, `e2e/docx-import.spec.ts`, `e2e/template-prompts.spec.ts` |
| **Edits** | `components/docs/page-header.tsx` (byline, Archive entry), `docs/docs.go` (`doc.restore` with children), `components/docs/dashboard.tsx`, `components/notifications.tsx` (the access request row) | `editor/blocks.ts`, `editor/slash.tsx`, `editor/menus.tsx`, `editor/inserts.tsx`, `editor/toolbar.tsx`, `components/note-editor.tsx`, `app/globals.css`, `apps/collab/src/md.ts` and its tests, `notes/plaintext.go` and its test, `files/files.go` (the per-page count, `created_by_name`), `components/docs/export.ts` (resolve the child pages block), `publiclinks/pagebody.go` (remove the marker) | `docs/templates.go`, `docs/builtin.go`, `components/templates/use-template.tsx`, `components/templates/gallery.tsx`, `components/templates/save-template.tsx`, `app/w/[ws]/s/[id]/page.tsx` (the import button), `importers/html.go` (the `children` macro) |
| **Must not touch** | Editor folder, gateway, importers | API outside the three files named, page header, templates | Editor folder, gateway, page header |
| **Depends on** | Step 0. B1's `shared` and `access_request` kinds | Step 0. Nothing from Q1 or Q3 | Step 0. The `children` mapping in `html.go` needs Q2's stored form, which is fixed in this document |

**Integration (lead).** Merge Q2 first, then Q1, Q3. Full e2e, and the rollout rehearsal of 7.3 with an old tab.

**Release order.** Mobile app first. Then gateway with the minimum unchanged, API, web, and `COLLAB_MIN_SCHEMA` raised the same day.

## 11. Decisions taken here (the lead may override)

1. Tasks, attachments and page links are stored as existing links and marks, with no new node.
2. Public page links are bound to their creator, and also refuse any page under a view restriction.
3. Turning the space switch off revokes links for good.
4. A public link does not come back by "enable"; it does come back when a trash, archive or move is undone, because validity is computed per request.
5. Email covers page notifications only, and never contains page or comment text.
6. Manual archive takes the subtree.
7. Word import runs on the server with the standard library and stores nothing.
8. Guests cannot be space members in Phase B.
9. The child pages block is built as planned and carries the one schema bump; the page-chrome alternative is on record in 5.4.
10. Migration numbers 1337 to 1339, subject to 7.7.

## 12. Open questions for the owner

1. **Email on by default.** At release every member starts in `smart` mode and begins to receive mail. Is that right, or should people opt in?
2. **Comment text in email.** The design sends no page or comment text, for privacy. Reading a comment then takes a click. Do you want the comment text in the mail anyway?
3. **Public link lifetime.** Links do not expire unless the creator sets a date, because SOPs and brochures are printed as QR codes. Would you rather have a default of 30 or 90 days?
4. **When the creator leaves.** A person's public links stop when they leave the workspace or lose access to the page. Someone else must make a new link with a new address. Accept that, or should links belong to the page?
5. **Who sees who viewed a page.** Proposed: everyone sees the count, people who can edit see the names. Some staff will read this as monitoring.
6. **Guests in a space.** Today a guest sees single shared pages only. Should a space admin be able to add a guest to a whole space?
7. **Attachment limits.** 50 files per page at 10 MB each, images and PDF only, and no storage quota per plan yet. Word and Excel attachments stay off until the follow-up that Tables 18.8 describes. Do you need plan quotas before B2 ships?
8. **Public page footer.** It shows your customer's workspace name and "Shared ... with C Note". Is our name on customers' public pages wanted?

## 13. Mobbin

References are linked in each item. Mobbin returned no usage notice with any of these results, so there is none to pass on. No true "request access" screen and no Confluence share screen with a public link came back; the research file's share dialog references stand in for the second.

## 13. Binding revisions (owner defaults and design review, 2026-11-25)

This section overrides sections 1–12 wherever they disagree.

### 13.1 Owner questions (lead defaults; the owner may override)
- Email is on by default.
- No comment text in emails.
- Public links never expire, and die when their creator leaves.
- Viewer names are shown to editors only.
- No guests on a whole space.
- No storage quotas yet.
- A small "Shared with C Note" footer.

### 13.2 Fixes

1. **Space admin never implies page access (review 1).**
   - Space roles grant only what the space already grants. Page restrictions (`note_access`) still apply to every space member, admins included.
   - Making a space open, adding members, or promoting someone never reveals a restricted page, because the restriction check is independent of space membership.
   - Space admins manage membership and settings; they get no edit access to restricted pages.
   - Tests:
     - a space admin cannot read or edit a restricted descendant;
     - opening a private space does not expose restricted pages;
     - a promotion does not either.
2. **Archived spaces are read-only on every write path (review 2).**
   - The check lives in the central write gate: notes write commands, the gateway write admission and comments. It runs whatever the access route, direct shares included.
   - Audit every command that writes a note, comment, file or task, and list them in the step-0 notes.
3. **Link inventory (review 3).** `page_link.list` filters by the caller's full page access before counting or returning. A space admin gets "Turn off all public links in this space", which only flips the space switch and returns nothing about pages.
4. **Revocation guarantee (review 4).**
   - A request authorized before the revocation commits may complete. Any request authorized after the commit fails.
   - The authorization and the body or file read happen inside one transaction that holds `FOR SHARE` on the link row.
   - Tests cover both lock orders, including file delivery.
5. **Email is rebuilt at send time (review 5, 6).**
   - The outbox stores only references: user, kind, notification ids.
   - Immediately before SMTP, the mail job rebuilds the message. It rechecks workspace membership, page access (or space membership for space notifications) and current preferences, drops items that fail, and sends nothing if nothing is left.
   - Default-on mail needs one-click unsubscribe:
     - a signed, per-user, per-kind token link;
     - `List-Unsubscribe` and `List-Unsubscribe-Post` headers;
     - a landing page that confirms without needing a login.
   - The sweeps claim candidate notifications first (`FOR UPDATE SKIP LOCKED`), check eligibility per kind, and in one statement mark each one `mailed` or `suppressed`.
6. **Task index (review 7).**
   - Rebuilds run per page under `pg_advisory_xact_lock(hashtextextended('notes.tasks:'||note_id, 0))` and apply only when the event's version is newer than `note_tasks.indexed_version`.
   - An assignment's identity is `(note_id, assignee, sha256(normalised task text))`. An assignment notification is sent once per identity, recorded in `note_task_notified`.
   - Tests cover reordered and retried events, edits to the task text, and a backfill racing live edits.
   - `task.mine` and the digest keep the access predicate inside the query.
7. **Request access routes to someone who can grant it (review 8, 9).**
   - The server computes the actual blocking grant: either the private space or the restricting page or ancestor. Recipients are the people allowed to change that grant: space admins for a space, and people with edit access on the restricting page for a restriction.
   - Guests are routed only to a page share. If no eligible recipient exists, the requester sees "Ask your workspace admin for access", never a false "Request sent".
   - Limits:
     - per requester: 10 per hour;
     - per requester, page and recipient: 1 per day;
     - per recipient: 20 per day;
     - per workspace: 200 per day.
   - Limits are stored durably even when delivery is suppressed. Access-request email is batched into the digest after the first one each day. The response is identical either way.
8. **Word import limits in the shared path (review 10).**
   - Limits enforced in the parser package, shared by the command and the HTTP route:
     - decoded input ≤ 10 MB;
     - entries ≤ 1,000;
     - cumulative decompressed bytes ≤ 50 MB;
     - XML depth ≤ 64 and tokens ≤ 2M;
     - context cancellation checked inside the read loops;
     - output ≤ 2 MB of Markdown;
     - at most 2 imports per workspace at a time.
   - Relationship targets are normalised and kept inside the package. External links are allowed only for http, https and mailto. Images become uploads through the existing files path.
   - Tests cover the command path as well as HTTP: zip bomb, deep nesting, an external image, a `javascript:` link and an oversize file.
9. **Migrations (review 11).**
   - Numbers are allocated at B1 step 0 after A2 has merged (use the next free number then; 1327 stays reserved).
   - `spaces.public_links` is nullable with no default, and null means on. No backfill is needed.
   - Concurrent indexes use the existing `-- no-transaction` single-statement files. docs/deploy.md already describes recovery from an invalid index.
10. **Mentions from live editing (review 12).** This moves into B1, step 0, owned by the lead.
    - On `note.updated` from the gateway, the worker computes the mention diff against the previously processed version (stored per note) under the same per-note lock. It is idempotent per version.
    - The API path and the gateway path share one function, so a mention notifies once.
    - Task assignment uses the same deduplication, so mail is never doubled.
    - Includes a co-edit integration test.
11. **Ownership (review 13).**
    - Step 0, the lead, owns:
      - the gateway mention path;
      - the shared mail-delivery check (the job that rebuilds mail at send time);
      - the central write gate for archived spaces;
      - the public body rewriter interface, which P2 implements and Q2 extends through a registered hook rather than by editing P2's file.
    - Import in B2 is one package that owns the parser, the command, the raw route and cleanup.

### 13.3 Write-path audit for archived spaces (step 0, 2026-11-25)

The gate. `access.AssertSpaceAccess(write=true)` refuses an archived space for every role, workspace admins and owners included
(message `This space is archived`, `access.ArchivedMessage`). `access.AssertNoteAccess(write=true)` refuses it too, and does so
whether or not the caller can read the space, so a page reached by a direct share is covered. `access.AssertSpaceWritable` is the
same check for code that writes without either call. The gateway has the same two rules in `apps/collab/src/access.ts`
(`assertSpaceAccess`, `assertNoteAccess`, so `mayWrite` is false: connect-time admission and the 10 second recheck). Every writing
command below reaches one of them; the second column says which.

| Writer | Commands and routes | Reaches the gate through | Test |
|---|---|---|---|
| Page create, edit, delete | `note.create`, `note.update`, `note.delete`, `note.duplicate`, `note.move`, `note.reorder`, `note.undelete`, `note.restore` (revisions) | `InsertNote` and `AssertSpaceAccess(true)`; `AssertNoteAccess(true)`; trash and lifecycle `AssertSpaceAccess(true)` | `TestArchivedSpaceRefusesWritesOnEveryRoute` |
| Page access and metadata | `note.share`, `note.unshare`, `note.restrict`, `label.set`, `relation.add`, `relation.remove`, `view.create`, `doc.verify`, `doc.set_owner`, `doc.restore`, `doc.proposal_decide`, `template.render` (into a space or under a page), `template.refresh` | `AssertNoteAccess(true)` or `AssertSpaceAccess(true)`. `view.create` had no space check at all; step 0 added one | same test; `view.create` in `notes_write_test.go` |
| Comments | `comment.add`, `comment.resolve` (reopen) | Read-level page access only, by design (anyone who can read may comment), so step 0 added `AssertSpaceWritable` with the page's space to both | same test, also through a direct share |
| Files | `file.upload`, `file.delete`, `file.copy_for_note` | `guard()` calls `AssertNoteAccess(write)` | same test (`file.upload`) |
| Tasks | The task index and `task.mine` are derived from page text; there is no task write command | A task is edited by editing the page: `note.update` or the gateway | gateway tests below |
| Tables | `base.*`, `field.*`, `record.*`, `file.record_*` | `tables.spaceRole` caps the caller's role at read when the space is archived (it did not call the write rule) | `base.create` in the same test |
| Projects, planning, CRM, billing, quotes, receipts, purchases, expenses, recurring, time log, leads | `item.*`, `sprint.*`, `version.*`, `component.*`, `automation.*`, `deal.*`, `invoice.*`, `quote.*`, ... | Each handler calls `AssertNoteAccess(true)` or `AssertSpaceAccess(true)` before it writes (static scan of every function that writes `core.*`, `billing.*`, `projects.*`; the helpers it found are reached only from guarded handlers) | `item.create` in the same test |
| Import | `import.start` | `AssertSpaceAccess(true)` at start; step 0 added a re-check in the `import.run` job, because a space can be archived while an import is queued | none yet (Q3 and P1 may add one) |
| Live editing | Gateway connect, the 10 second recheck, `persist` | `access.ts`, as above. An edit sent in the window before the recheck is still saved: at most 10 seconds | `apps/collab/test/b1-step0.test.ts` (open, live recheck, direct share) |
| Automations | The `comment.add` action in `worker/automation.go` | Runs `comment.add` through the registry as the system principal, so it is refused too and the automation run records the failure | covered by the comment gate |

Deliberately not gated, so nobody adds them by mistake:

- `note.star`, `note.watch`, `note.viewed`, `notification.*`, `notify_prefs.set`: a person's own state, not a write to the page.
- `template.save`, `template.delete`, `capture.answer`, `doc.capture`: they write a template or a capture request that points at a page and leave the page alone.
- `data.erase_subject` (privacy erasure by an owner) and the system sweeps (`docs.freshness_sweep`, `docs.search_plain_backfill`,
  `docs.a2_backfill`, `docs.mention_seed`, binding sync, the task index, mail marking): they keep derived data and metadata right and
  must keep running in an archived space.
- Support tickets, inquiries and the customer portal write `support.*` and `leads.*` rows, not pages or comments. They are not
  covered; whether an archived project space should still receive customer mail is an owner decision.

Rules for the packages (each is checked in review):

1. A command that writes after only a read-level check calls `access.AssertSpaceWritable(c, spaceID)`. P5's `comment.edit`,
   `comment.delete` and `comment.react` do, with the space `AssertNoteAccess` returned. P4's task rebuild and P3's mail marking are
   derived data and do not.
2. `space.archive`, `space.update` and `space.member_set` use `access.AssertSpaceAdmin`, which reads the space and never writes a
   page, so they work on an archived space (an admin must be able to unarchive it).
3. Q1's `doc.archive` and any other subtree command answer the generic `Some pages here are restricted` for an archived space as well
   (that is what `note.delete`, `note.duplicate`, `note.move` and `note.reorder` do today). It names nothing, but a person may read it as a
   restriction; Q1 may check `AssertSpaceWritable` first to give the real reason, which leaks nothing a reader of the space does not know.

### 13.4 Work packages P1 to P5 (as built after step 0)

Step 0 is in. Packages work in parallel on disjoint files. A file not listed for a package belongs to the lead. **No package edits a
migration, `notes/notes.go`, `notes/write.go`, `command/*`, `access/*`, `apps/collab/src/*`, `worker/wire.go`, `worker/notifydeliver.go`,
`worker/mentions.go`, `mailer/*`, `unsub/*`, `schemautil/*`, `server/server.go`, `apps/web/src/lib/api.ts`, `apps/web/src/lib/docs.ts`, the
fixtures or `e2e/helpers.ts`.** Where the design said a package edits one of these, step 0 did it (below) or gave the package a hook.

How a package plugs in:

- **Commands.** Each module has a stub list (`b1Specs`) and a registry map (`b1Handlers`). A package registers its handler from an `init()`
  in its own file: `b1Handlers["space.members"] = spaceMembers`. The contract (group, role, confirm, secret, schemas) is fixed by
  `modules/b1_contract_test.go`; changing it is a lead decision.
- **Worker.** `worker.RegisterEvent(prefix, handler)`, `worker.RegisterJob(kind, handler)` and `worker.RegisterSweep(sweep)` from an
  `init()` in the package's own file. `wire.go` is not edited.
- **Public body.** `publiclinks.RegisterCoreBodyPass(p)` (P2, exactly once) and `publiclinks.RegisterBodyPass(order, p)` (later phases:
  B2's `[[children]]` line is Q2's pass, in Q2's file). `PublicBody` refuses to run without the core pass.
- **Mail.** Anything that wants a notification emailed sends it with `notify.Send` and one of `worker.MailKinds`. The sweeps and the
  mail job already exist (below). A space notification has `NoteID` nil and `Source: notify.SpaceSource(spaceID)`.

| | P1 Spaces | P2 Public links | P3 Email and digest | P4 Tasks | P5 Comments |
|---|---|---|---|---|---|
| **Creates** | `notes/spaces.go` (handlers `space.members`, `space.member_set`, `space.update`, `space.archive`; `space.update` calls `page_link.revoke_space` through `c.Call`), `components/docs/space-settings.tsx`, `app/w/[ws]/s/[id]/settings/page.tsx`, `server/docs_b1_spaces_test.go`, `e2e/space-members.spec.ts` | `publiclinks/page.go` (handlers `page_link.*`), `publiclinks/pagebody.go` and `pagebody_test.go` (the core pass), `server/pagepublic.go` (replaces the step 0 placeholder wholesale), `server/pagepublic_test.go`, `app/pg/[token]/page.tsx`, `components/docs/public-page.tsx`, `components/share/public-link.tsx`, `components/docs/space-public-links.tsx`, `e2e/public-page.spec.ts` | `worker/notifymail.go` and `notifymail_test.go` (the digest sweep and its tests; sets `worker.DigestExtras`), `notifications/prefs.go` (handlers `notify_prefs.get`, `notify_prefs.set`), `components/notification-settings.tsx`, `server/docs_b1_email_test.go` | `notes/tasks.go` and `tasks_test.go` (`notes.Tasks`, handler `task.mine`), `worker/pagetasks.go` (event consumer and backfill sweep, registered with `worker.Register*`), `components/editor/task-controls.tsx`, `components/docs/my-tasks.tsx`, `app/w/[ws]/tasks/page.tsx`, `server/docs_b1_tasks_test.go`, `e2e/tasks.spec.ts` | `notes/reactions.go` (handlers `comment.edit`, `comment.delete`, `comment.react`), `server/docs_b1_comments_test.go`, `e2e/comment-actions.spec.ts` |
| **Edits** | `app/w/[ws]/s/[id]/page.tsx`, `components/shell.tsx` (space menu entry, archived spaces hidden, the "My tasks" link for P4), `components/space-dialog.tsx` | `components/share/share-dialog.tsx` (mounts the row), `components/docs/page-header.tsx` (the Public chip), `apps/web/next.config.ts` | `components/notifications.tsx` (sentences for `shared` and `access_request`, link to settings), `app/w/[ws]/settings/page.tsx` (mounts the tab), `sharing/sharing.go` (`shared` notification in `note.share`) | `components/editor/slash.tsx`, `components/note-editor.tsx` (mounts the controls) | `notes/comments.go` (`comment.list` fields, edit notifications), `components/comments-panel.tsx` |
| **Must not touch** | Editor, comments, `publiclinks/*` except through `c.Call` | Editor, shell, worker | Editor, shell, `worker/pagenotify.go`, `worker/notifydeliver.go` | Shell, comments, notifications | Editor, shell, worker |
| **Depends on** | Step 0. Mounts `<SpacePublicLinks>` from P2's file; sends the space `shared` notification with `notify.SpaceSource` | Step 0. Reads `spaces.public_links` (null is on). Its resolver is `core.resolve_page_link` (migration 1340) | Step 0. `worker.DigestExtras` reads `core.note_tasks`, which P4 fills, with the access predicate in the query | Step 0. P1 adds its sidebar link. Uses `core.note_tasks`, `core.note_task_state`, `core.note_task_notified` | Step 0. Calls `access.AssertSpaceWritable` in every write |

Merge order: P1, then P2, P5, P4, P3 (unchanged). Cross-package checks for the lead at integration: turning the space switch off kills a
live link; removing a space member stops their link, their watch mail and their task rows; a task assigned in a co-edited page reaches
the assignee's bell and, three minutes later, their inbox; one mention in a co-edited page makes one notification and one mail.

### 13.5 What step 0 built, and what changed in the plan

Migrations (online-safe, `SET LOCAL lock_timeout`, nullable columns, `NOT VALID` checks, tenant RLS, composite foreign keys, explicit grants):
**1340** `docs_parity_b1` (all B1 tables and columns, both functions), **1341** `public_links_one_active_page` (unique partial index,
`CONCURRENTLY`), **1342** `notifications_unmailed` (partial index, `CONCURRENTLY`). B2's migration takes **1343**. 1327 stays reserved. The two
`VALIDATE CONSTRAINT` groups for the release after B1 are in `docs/deploy.md`.

Built by the lead:

- **Mentions** (13.2.10): `notes.NotifyPageMentions` (`notes/mentions.go`), called by `note.update` and by the worker consumer
  `mentionsOnNoteUpdated` (`worker/mentions.go`, registered before `pageNotifyOnEvent`). Per-note advisory lock, `core.note_mention_state`
  holds the last handled version and the set of people mentioned then. A sweep (`docs.mention_seed`) seeds pages that predate the table.
  Co-edit integration test in `apps/collab/test/b1-step0.test.ts`.
- **Archived spaces** (13.2.2): column `core.spaces.archived_at`, the gate in `access`, `tables.spaceRole`, comments, the import job, `view.create`,
  and the gateway. Audit in 13.3.
- **Mail** (13.2.5): `worker/notifydeliver.go` (the `notify.mail` job that rebuilds the message, the direct sweep every 2 minutes,
  `EnqueueNotifyMail` for the digest sweep), `mailer.Mail.Headers`, `unsub` (signed token), `server/unsubscribe.go` (landing page and RFC 8058
  POST at `/api/public/unsubscribe/<token>`, no login), `core.notify_prefs` (with `direct_off` and `digest_off`, which the unsubscribe links set),
  `core.notifications.emailed_at` and `email_state` (`queued`, `mailed`, `suppressed`). `worker/mail.go` and `mentionEmail` are gone. Mail needs `SMTP_URL`,
  `EMAIL_FROM` and `CNOTE_MASTER_KEY`; without the key nothing is sent.
- **Public body** (13.2.11): `publiclinks/bodyhook.go`, with tests. `server/publicaddr.go` is the shared address key (`publicIPKey`); the forms use it.
  `server/pagepublic.go` is a placeholder that answers the one 404.
- **Contract**: stubs and schemas for all 15 commands (`notes/b1_commands.go`, `publiclinks/b1_commands.go`, `notifications/b1_commands.go`, built with the new
  `schemautil` package), `space.list` fields (`description`, `archived`, `public_links`, `my_role`, `include_archived`), web types in `lib/docs.ts`.
- **Fixtures**: `task-assignee-due` and `task-lookalike`. The gateway's Markdown reader flattened a nested to-do (the sub-task moved out of its parent on the
  next save) and wrote its children six spaces in; fixed in `apps/collab/src/md.ts` (`parseTasks`), so a to-do's children are two spaces in, as the editor writes
  them. The web editor, the plain-text extractor and the importer already passed.

Changes to the plan:

1. **The task index version lives in its own table.** `core.note_task_state (note_id, indexed_version)` holds it, because a page with no tasks has no row to carry it;
   `core.note_tasks` also keeps `indexed_version` per row. P4 applies a rebuild only when the event's version is newer than `note_task_state`.
2. **Unsubscribe is per kind and does not rewrite the chosen mode.** Two flags, `direct_off` and `digest_off`, so a mail client's one-click POST cannot change what a person picked in settings.
3. **The direct sweep is already built** (in `notifydeliver.go`), so P3 builds the digest only, plus the settings tab and the preferences commands.
4. **No `wire.go` edits for packages**: `worker.Register*` replaces them.
5. **Mail is suppressed, not queued, without `CNOTE_MASTER_KEY`.** Dev environments need the key.
6. The first co-edit of a page with mentions that predate the mention store notifies only people not already notified, until the seeding sweep has covered it (about a minute per 10,000 pages).
7. Archived-space refusals from subtree commands carry the generic restricted text (13.3, rule 3).

