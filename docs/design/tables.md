# Tables, Phase 1: technical and UX design

25 Nov 2026. Scope is Phase 1 of the research plan (`airtable-research.md`, section 6): a spreadsheet-database that is
useful on its own. Formulas, lookups, rollups, calendar and gallery views, record comments, XLSX import, report
datasets, record-level rules, portal guests, "C Note record" fields and table automations are later phases and are
not designed here.

Facts in this document were read from the code on `feat/tables-phase0` (main at `bcac892`) unless marked
**[proposal]** (a number or choice the owner should confirm) or **[spike]** (Phase 0 must measure it).

## 1. Decisions in one page

| Topic | Decision |
|---|---|
| Module | New Go module `apps/api/internal/modules/tables`, Postgres schema `tables`. It imports no other module and no module imports it (`boundaries_test.go` stays unchanged). |
| Records | One `tables.records` table, `data jsonb` keyed by field UUID. Linked records are rows in `tables.links`, not JSON. |
| Files | Stay in the files module (`core.attachments`). Tables reaches them only through new internal `file.record_*` commands. |
| Views | Own table `tables.views` (shared config). `core.views` belongs to notes and holds CNQL text, so it is not reused. Per-person changes go to the existing `ui_config` store, screen `tables.view:<view id>`. |
| Public forms | Same pattern as `core.public_links` (hashed token, `SECURITY DEFINER` resolver, one 404 body), in an own table. `core.public_links` cannot be reused: `note_id` is `NOT NULL` with a foreign key to notes, and the `publiclinks` package imports `billing`. |
| CSV import | `internal/dataimport` (neutral package) for parsing; the web wizard's step, column-match and review pieces are exported and reused. The `data_import.*` commands are admin-only finance commands with three fixed kinds, so Tables gets its own `table_import.*`. |
| Grid | `@tanstack/react-table` 9.3.1 + `@tanstack/react-virtual` 3.14.28, DOM cells, `role="grid"`. |
| Automations | `record.*` events fire webhooks, MCP hooks and AI-employee triggers on day one. The project automation engine is **not** extended: its conditions are CNQL over items and its actions are `transition`, `assign`, `comment` on an item (`worker/automation.go`). Table triggers and actions stay in Phase 4. |
| Mobile | Responsive web (card list and full-screen record below 640 px). The Expo app gets Tables in Phase 3. |
| Migrations | `1320_tables.sql`, `1321_record_files.sql`. 1322 to 1329 are reserved for this module. |

## 2. Design references (Mobbin)

Baserow, NocoDB, Smartsheet, monday, Coda and Lark Base returned no screens for these searches; Airtable, Notion,
ClickUp, Zapier Tables, Attio, Twenty, Retool, Height, Slack Lists and Zoho CRM did.

| Area | References | What we take |
|---|---|---|
| Grid | [Airtable grid and view menu](https://mobbin.com/screens/115bb780-e94d-457a-9f8e-92a7e9c15312?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Airtable toolbar](https://mobbin.com/screens/4487b4e4-4b62-464c-a4d0-0560ad1c689c?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [ClickUp grouped list with "Add task" per group](https://mobbin.com/screens/1fd3b5e1-94ac-47ca-8f46-280e43e685cd?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [ClickUp bulk bar](https://mobbin.com/screens/3fde8302-c073-4b10-8857-73613555af70?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion column menu](https://mobbin.com/screens/f58cc5eb-41a3-4f0a-b1e0-57f40d76b786?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | One toolbar row: Hide fields, Filter, Group, Sort, row height. Type icon in every header. Row number and checkbox in the first, frozen column. "+" as the last header and the last row. Column menu holds edit, sort, filter, hide, insert, delete. Footer shows the record count. A bottom bar appears when rows are selected. |
| Field dialog | [Airtable type picker](https://mobbin.com/screens/8bd54805-6395-4c63-83a5-b74a7e56770b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion type menu](https://mobbin.com/screens/4e6e1f98-a567-4370-b392-ac28802481c7?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion select options](https://mobbin.com/screens/841ae11d-d9e0-43ce-9d45-0405415af97f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Zapier Tables field panel](https://mobbin.com/screens/a0364422-36fb-482a-89cb-849292c2b17c?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Popover anchored to the header. Name first, then a searchable type list with a one-line description, then the type's options. Options are edited in place with a colour dot. |
| Linked records | [Airtable expanded record with linked cards](https://mobbin.com/screens/b00bbec2-ff84-4b01-a48b-d8085efba633?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Airtable record search list](https://mobbin.com/screens/6705170e-829c-4342-a6cc-954d213ed968?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Attio "Choose record"](https://mobbin.com/screens/3d3a3b51-40bb-4201-b526-e69acf4540ba?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Salesforce multi-select](https://mobbin.com/screens/e0b1b27c-db0f-4cc1-9619-6ea591e48b2d?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Search box with focus, rows show the primary field plus two more fields, arrow keys and Enter, chosen records as removable chips, "Create new" at the bottom. |
| Kanban | [Notion board and view settings](https://mobbin.com/screens/0b10f8d0-2acb-4d7f-87f4-0bfd14e463bc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Slack Lists board](https://mobbin.com/screens/50a69d82-266a-4785-8cfe-7bc829b1c3b2?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Height "Section by"](https://mobbin.com/screens/5093d432-bf5f-4f1b-9a3a-ff1542be4efa?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Column header is the option chip and a count. "+ Add" at the foot of each column. Cards show the primary field and up to four chosen fields with small labels. A first column for records with no value. |
| Form builder | [Airtable form builder](https://mobbin.com/screens/e7733296-d529-4d44-94b5-15df5687c6ba?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Airtable add field to form](https://mobbin.com/screens/a75979fe-c558-4df9-a27a-373b57297532?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | The builder is the form itself, with a settings panel on the right. Computed fields cannot be added. Groups, cover images, logos and conditional rules are left out. |
| Public form | [Airtable public form](https://mobbin.com/screens/06788e74-5811-41d5-bd19-1f254d289410?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Zapier form](https://mobbin.com/screens/f516f03b-bd43-409e-83be-86910023c0f1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [n8n form](https://mobbin.com/screens/49c3b208-0b11-4d96-9d7c-e4ec76b0d519?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | One column, 480 px wide, labels above inputs, "(required)" in words, one full-width submit button, a "do not enter passwords" line in the footer. |
| Create and empty | [Zapier "Create a new Table"](https://mobbin.com/screens/ec8b31ac-5248-4350-85f1-90471d5765cc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion new database](https://mobbin.com/screens/e00ca642-dcfd-4797-aec7-9ae0d780ec3d?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Attio empty list](https://mobbin.com/screens/fc16f33c-52ee-4b44-87d9-f3910c31ed33?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Three starts side by side: blank, template, CSV. An empty table keeps its headers and offers "Import CSV" and "Add a record" in the body. |
| Mobile record | [Airtable iOS record](https://mobbin.com/screens/0dedd973-a7b2-4483-8774-c60217ebb605?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Airtable iOS hidden fields](https://mobbin.com/screens/97d57222-7468-427d-b262-e9589dd9de97?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion iOS page properties](https://mobbin.com/screens/dd06bc90-912a-4c12-aa35-c53fed682cad?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Full-screen record: small-caps label with type icon above each value, camera and gallery buttons on attachment fields, "N hidden fields" collapsed at the end. |

## 3. Data model

All tables carry `workspace_id`, composite `(workspace_id, id)` keys for child foreign keys (as `1280_sites.sql`
does), `SELECT core.enable_tenant_rls(...)` and DML-only grants to `cnote_app`.

```sql
-- 1320_tables.sql (sketch)
tables.bases   (id, workspace_id, space_id → core.spaces, name, icon, template, record_count int, attachment_bytes bigint,
                created_by, created_at, updated_at, deleted_at)
tables.tables  (id, workspace_id, base_id, name, position, primary_field_id, next_seq bigint default 1, created_at, deleted_at)
tables.fields  (id, workspace_id, table_id, name, type, config jsonb, position, locked bool default false,
                created_at, deleted_at,  unique (table_id, lower(name)) where deleted_at is null)
tables.records (id, workspace_id, table_id, seq bigint, data jsonb check (octet_length(data::text) <= 65536),
                created_by null, created_at, updated_by null, updated_at, deleted_at,  unique (table_id, seq))
tables.links   (workspace_id, field_id, from_record, to_record, position,
                primary key (field_id, from_record, to_record), both records ON DELETE CASCADE)
tables.views   (id, workspace_id, table_id, name, type in ('grid','kanban','form'), config jsonb ≤ 16 KB, position, created_by)
tables.form_links (id, workspace_id, view_id, token_hash unique, revoked_at, submissions int, created_by, created_at)
tables.record_history (id bigint identity, workspace_id, table_id, record_id, actor_type, actor_id, at, change jsonb)
tables.imports (id, workspace_id, table_id, filename, data bytea, mapping jsonb, status, counts jsonb, record_ids uuid[],
                created_by, created_at, undo_until)
```

- **Base belongs to a space**, so space visibility and roles apply through `access.AssertSpaceAccess` with no new
  permission tables.
- **`seq`** is allocated from `tables.next_seq` in the insert transaction
  (`update ... set next_seq = next_seq + $n returning`). It is the row number, the default order, the pagination
  tiebreak and the value of every autonumber field. One counter per table, so two autonumber fields show the same
  number. A second independent sequence is not built.
- **Empty means the key is absent.** Handlers never store `null` or `""`, so "is empty" is `not (data ? key)`.
- **Created/modified time and by** are the record's columns. The four field types only display them.
- **Soft delete** on bases, tables, fields and records. Trash is restorable for 30 days; a daily sweep
  (`tables.purge`) hard-deletes, and for a purged field strips its key from records in batches of 1,000.
- **History, minimal:** every update, delete and restore writes one row, `change = {field_id: [old, new]}`. It is
  shown as a plain list in the record panel and backs paste undo. No diff view, no revert button. A daily sweep
  applies the plan's retention.

### Field types and stored values

| Type | Stored in `data[field_id]` | Config | Notes |
|---|---|---|---|
| `text`, `long_text` | string (≤ 10,000 / 100,000 chars) | | Long text is plain text with line breaks |
| `number`, `percent` | JSON number | `decimals` 0–4 | Percent stores what is shown: 12.5 means 12.5% |
| `currency` | integer paise | `decimals` 0 or 2 | ₹ only; shown with `Intl.NumberFormat('en-IN')`, so 1,18,000.00 |
| `date` | `"YYYY-MM-DD"` | | Date only. Shown as DD-MM-YYYY. No time, no time zones |
| `checkbox` | `true` (absent = unchecked) | | |
| `single_select` | option id (`"o3"`) | `options [{id, name, color}]` ≤ 200 | Renaming an option touches no record |
| `multi_select` | array of option ids ≤ 50 | same | |
| `phone` | string, 7–15 digits, optional `+` | | Cell offers "Call" and "WhatsApp" links |
| `email`, `url` | string, validated | | URL must be http or https |
| `user` | array of user ids | `multiple` | Must be members of the workspace |
| `attachment` | array ≤ 10 of `{id, name, size, type}` | | Bytes live in the files module |
| `link` | nothing; rows in `tables.links` | `table_id`, `multiple`, `inverse_of` | Same base only. Cell shows chips and a count |
| `autonumber` | nothing; `records.seq` | `prefix` (e.g. `ORD-`) | |
| `created_time`, `modified_time`, `created_by`, `modified_by` | nothing; record columns | | Read-only |

The **primary field** is the first field, is frozen in the grid, names the record everywhere (link chips, kanban
cards, events) and must be `text`, `number`, `phone`, `email` or `autonumber`. It cannot be deleted or hidden.

**Links.** A link field reads `tables.links` where `field_id = it`. Ticking "Also show in *Customers*" creates a
field in the target table with `inverse_of = <this field>`; it reads the same rows in the other direction through
the `(to_record, field_id)` index, so there are no double writes to keep in step. `record.list` returns, for each
link field on the page, `[{id, title}]` in one extra query. Limit: 100 links per cell.

**Attachments.** `1321_record_files.sql` adds `record_id uuid` to `core.attachments` with a cascading foreign key
to `tables.records` and widens the existing `(note_id IS NULL) <> (site_id IS NULL)` check to "exactly one of three",
exactly as `1290_site_publish.sql` did for `site_id`. The files module gains four internal commands in a new file
`files/record.go`, modelled on `file.image_*`: `file.record_put`, `file.record_get`, `file.record_list`,
`file.record_delete`. Tables checks base access and plan limits, then calls them with `c.Call`. Types stay on the
files allow-list (JPEG, PNG, GIF, WebP, PDF, judged from the bytes) and the 10 MB cap.

### Validation and type changes

One Go function is the only place text becomes a typed value: `coerce(field, raw string) (value any, err error)`.
Grid paste, CSV import, public forms and type conversion all call it. It accepts Indian number formats
(`1,18,000.50`, `₹ 500`, `12%`), dates through `dataimport.ParseDate`, checkbox words (`yes`, `y`, `true`, `1`, `✓`)
and select options by name, case-insensitive. JSON writes from commands go through `validate(field, value)`.

Changing a field's type:

| From → to | Rule |
|---|---|
| anything → `text`, `long_text` | The displayed text. Never loses data |
| text → `number`, `currency`, `percent`, `date`, `checkbox`, `phone`, `email`, `url` | `coerce`; a value that fails is cleared and counted |
| text → `single_select`, `multi_select` | Distinct values become options (multi splits on commas). Refused above 200 distinct values |
| `single_select` ↔ `multi_select` | Multi to single keeps the first option |
| `number` ↔ `currency` ↔ `percent` | Same number; currency multiplies or divides by 100 for paise |
| to or from `link`, `attachment`, `user`, `autonumber`, created/modified | Not offered: add a new field instead |

`field.update` with a new `type` and no `confirm` changes nothing and returns
`{kept, changed, cleared, samples: [{seq, from, to}]}` (five samples). The dialog shows "37 values cannot become a
number and will be cleared". With `confirm: true` it converts: in the same transaction up to 10,000 records, above
that as a `tables.convert` job, during which the field is read-only. Cleared values are written to
`record_history`, so nothing is lost without a trace.

## 4. Querying and indexes

`record.list` takes `{table_id, view_id?, filters?, sorts?, group?, search?, cursor?, limit ≤ 200}`. A filter is
`{match: "and" | "or", rules: [{field, op, value}]}`: one level, at most 10 rules, no nesting. Operators follow the
report builder's by-type idea (the `reports` package cannot be imported): text `contains`, `is`, `is_not`, `empty`,
`not_empty`; number, currency, percent, date and autonumber `=`, `!=`, `<`, `<=`, `>`, `>=`, `empty`; date also
`today`, `this_week`, `this_month`, `past_days:N`, `next_days:N` (IST); selects and user `any_of`, `none_of`,
`empty` (user accepts `"me"`); checkbox `is`; link and attachment `empty`, `not_empty`. Up to three sorts. Every
field id is checked against the table's fields, and both keys and values are bound parameters (`data ->> $3`): no
user text is ever concatenated into SQL. Pagination is keyset on `(sort values, seq)`. With `group` set, the group
field becomes the first sort and the response includes `groups: [{value, count}]` from one aggregate query.

Indexes shipped in 1320:

| Index | Serves |
|---|---|
| `records (table_id, seq) where deleted_at is null` | Default order, pagination, every per-table scan |
| `records using gin (data jsonb_path_ops) where deleted_at is null` | Equality and membership: `data @> '{"<field>": "o3"}'` for selects, checkbox, user, exact text |
| `links (to_record, field_id)` plus the primary key | Both link directions |
| `record_history (record_id, at desc)`, `form_links (token_hash)` | |

UUID field keys are deliberate. `jsonb_path_ops` hashes path plus value, and a field id is unique across all
tenants, so a containment probe on the one shared GIN index only ever returns rows of that table.

**[spike] RLS and the GIN index.** Under row-level security Postgres uses an index for a caller's condition only
when its operator is `LEAKPROOF`. `uuid =` is, so the `(table_id, seq)` scan is safe; the built-in jsonb `@>` and
`->>` are not, so the GIN index may be ignored for the `cnote_app` role. The spike in progress
(`apps/api/cmd/tablespike`, untracked) is measuring leakproof wrapper functions and operator classes for this. This
design has not verified the outcome: if wrappers are needed, the query compiler emits them and 1320 creates them;
if they are not acceptable, equality filters fall back to the same per-table scan as sorts.

**Sort and range filters have no per-field index in Phase 1.** They scan the table's rows through
`(table_id, seq)` and do a top-N sort. The research proposed B-tree expression indexes per sorted field. Reading the
schema shows two costs it did not weigh: an expression index needs the key as a literal, so each one is DDL at run
time (the `cnote_app` role has DML only, and `CREATE INDEX CONCURRENTLY` cannot run in a command's transaction);
and every partial index on the one shared records table adds planning time to every tenant's query and a predicate
check to every insert. So the order of preference is:

1. Nothing more, if the spike meets the target with the scan.
2. Expression indexes `((data->>'<field>')) where table_id = '<table>'`, built with `CREATE INDEX CONCURRENTLY` by a
   privileged ops job only for tables above 25,000 records, capped at 200 across the cluster.
3. If the cap is not enough: a DML-only side table `(table_id, field_id, num, txt, record_id)` filled for fields
   flagged as sorted.

**Spike target [proposal, to be agreed]:** on a table of 100,000 records with 20 fields, inside a `tables.records`
holding 5 million rows of other tenants, the first page (100 rows) with one filter and one sort returns in
**300 ms p95 warm and 1 s cold**, server time. A single cell update takes 50 ms p95; a paste of 1,000 cells 2 s.
The spike reports the scan numbers first and tries option 2 only if they miss. Until it says otherwise, a table is
capped at 100,000 records on every plan.

## 5. Views and per-person state

`tables.views.config` (shared, written by editors and managers):

```jsonc
{ "v": 1,
  "filters": {"match": "and", "rules": []}, "sorts": [], "group": null,
  "order": ["<field id>"], "hidden": [], "widths": {"<field id>": 180}, "row_height": "short",   // 32, 56 or 88 px
  "kanban": {"stack": "<single_select field>", "card_fields": []},                                 // type kanban
  "form": {"title": "", "description": "", "submit_label": "Submit", "success": "",
           "fields": [{"field": "<id>", "label": "", "help": "", "required": false}]} }            // type form
```

Per-person overrides use the existing store with no server change: `ui_config.set {screen: "tables.view:<view id>",
config: {v: 1, hidden?, order?, widths?, row_height?, sorts?, filters?, group?, collapsed?}}`. The key is 48
characters and fits `^[a-z0-9_.:-]{1,64}$`. The web merges the override over the shared config through
`useScreenConfig`. Any change a person makes in the toolbar is saved to their override at once and shown as a dot
on the view tab with two actions: **Save for everyone** (editors; writes the view and resets the override) and
**Reset** (`ui_config.reset`). Viewers can therefore filter and sort for themselves without changing the view.
Limits inherited from `uiconfig`: 100 array items (hence 100 fields per table), 200 characters per string, 200
saved screens per person.

## 6. Permissions

| Base role | Who | May |
|---|---|---|
| Manager | Workspace owner or admin; space admin | Everything: structure (tables, fields, options), field locks, forms and public links, import, delete and restore |
| Editor | Space editor; any member in an open space | Create, edit and delete records; create and edit views; upload files |
| Viewer | Space viewer; a guest who is a member of the space | Read; personal view overrides |

A private space's non-members get "not found", as elsewhere. **Per-field edit lock:** `fields.locked`. Only
managers change values in a locked field or its configuration; the grid shows a lock icon in the header and the
cells are read-only for editors. The check lives in the one function every write path calls
(`writable(c, table, fieldIDs)`), so create, update, paste, import and kanban drag cannot miss it. A public form may
write a locked field because a manager put it on the form. Agents and API keys pass through the same checks plus
their tool groups: reads are `read`, record writes `write_items`, structure `admin`, base and table deletion
`destructive` with `Confirm`.

## 7. Public form

A manager publishes a form view with `table_form.publish`, which returns the link once
(`/tf/<token>`; `/f`, `/p` and `/t` are taken). Only the SHA-256 of the token is stored, and
`tables.resolve_form(hash)` is the single `SECURITY DEFINER` lookup. The HTTP handler is
`server/tablesform.go` (`GET` and `POST /api/public/table-form/{token}`), the same split as `server/inquiry.go`.

- `GET` returns only the form: title, description, and for each field its label, help, type, required flag and
  select option names. No field ids of fields that are not on the form, no record data, no member names.
- Allowed field types: text, long text, number, currency, percent, date, checkbox, selects, phone, email, URL,
  attachment. Link and user fields cannot be added to a public form (they would list records and people).
- `POST` is `multipart/form-data`: a JSON `payload` (≤ 64 KB) and up to 3 files of ≤ 5 MB each, images or PDF by
  sniffed bytes; the body is capped at 16 MB before parsing.
- **No captcha.** A hidden `hp` field: a filled one gets the same 200 and nothing is stored (as inquiry and site
  forms do). Limits use `db.Bump`, which is shared across API instances: 10 submissions per address per form per
  hour and a per-form daily cap by plan. Unknown, revoked and malformed tokens share one 404 body and a failed-lookup
  limiter per address.
- The record is created by a system principal acting for the form's creator, capped at Member. If the creator has
  left the workspace or lost write access, the form answers 404. `created_by` is null and renders as "Form".
- Emits `record.created` with `via: "form"`.

## 8. Import, paste, events

**CSV import.** `table_import.upload` (into an existing table or a new one) parses with `dataimport.ReadCSV(data, "")`
(with no known kind the first row is the header) under the same limits, 5,000 rows and 10 MB. The preview returns the
`Preview` shape the wizard already renders: columns with samples and a proposed target each (an existing field
matched by name, or a new field with a type guessed from the samples), and row counts for "ready" and "needs
fixing" with reasons from `coerce`. `table_import.run` queues a `tables.import` job; only ready rows are added.
Undo within 7 days soft-deletes the import's `record_ids`. Web: `Steps`, the column-match table and the review tabs
are exported from `components/import/wizard.tsx` unchanged, and `components/tables/import.tsx` composes them.

**Paste from Excel.** Excel and Google Sheets put tab-separated text on the clipboard. The grid parses it (quoted
cells, line breaks inside quotes), lays it over the visible columns from the active cell and sends raw strings in
one call: `record.paste {table_id, field_ids, rows: [{id?, cells: [string]}]}`. Rows with an `id` are updated; rows
past the last record are created. The server coerces each cell; a cell that fails, or that targets a locked or
computed column, is left unchanged and reported (`errors: [{row, col, reason}]`, first 50). Limit 1,000 rows by 50
columns; larger pastes are told to use Import. Copy writes the selection as TSV. Ctrl+Z undoes the last 20 edits or
pastes by sending the old values back.

**Events.** `record.created`, `record.updated`, `record.deleted`, `record.restored`, with `note_id` null and payload
`{base_id, table_id, record_id, seq, title, changed: [field ids], values: {field id: new value}, via}`. Each
`c.Emit` writes an event row and a dispatch job, so a command that touches more than 20 records emits
`record.batch_created`, `record.batch_updated` or `record.batch_deleted` instead, in chunks of 500
(`{table_id, record_ids, count, via}`). A hook subscribed to the prefix `record.` receives both. Nothing else in the
codebase uses that prefix today.

## 9. Commands

Every command sets `Description` and `Schema` inline in the module, as `sites/commands.go` does, so it appears in
MCP `discover` and can be put on an AI employee's allowlist. V, E, M are the base roles above. All workspace
minimum roles are Guest for reads and Member for writes; the base role is checked in the handler.

| Command | Input | Output | Role |
|---|---|---|---|
| `base.list` | `{space_id?}` | bases with table count, record count | V |
| `base.get` | `{id}` | base, tables, each table's fields and views, caller's role, plan limits and usage | V |
| `base.create` | `{space_id, name, template?}` | base | space editor |
| `base.update` | `{id, name?, icon?}` | base | M |
| `base.delete` / `base.restore` | `{id}` | `{}` | M, confirm |
| `table_template.list` | `{}` | `[{key, name, description, tables: [names]}]` | any member |
| `table.create` | `{base_id, name, fields?}` | table with a primary field and a grid view | M |
| `table.update` | `{id, name?, position?}` | table | M |
| `table.delete` / `table.restore` | `{id}` | `{}` | M, confirm |
| `field.create` | `{table_id, name, type, config?, position?, inverse?}` | field | M |
| `field.update` | `{id, name?, config?, position?, locked?, type?, confirm?}` | field, or the conversion preview | M |
| `field.delete` / `field.restore` | `{id}` | `{}` | M |
| `record.list` | section 4 | `{records, links, groups?, next_cursor, total}` | V |
| `record.get` | `{id}` | record with links and file metadata | V |
| `record.create` | `{table_id, values, links?}` | record | E |
| `record.update` | `{id, values?, clear?: [field ids], links?}` | record | E |
| `record.bulk_update` | `{table_id, updates: [{id, values, clear?}]}` ≤ 1,000 | `{updated}` | E |
| `record.paste` | section 8 | `{updated, created, errors}` | E |
| `record.delete` / `record.restore` | `{ids}` ≤ 1,000 | `{count}` | E |
| `record.search` | `{table_id, q, limit ≤ 20}` | `[{id, title, preview: [two values]}]` for the link picker | V |
| `record.history` | `{id}` | last 100 changes | V |
| `record.attach` | `{id, field_id, filename, data_base64}` | file metadata | E |
| `record.detach` | `{id, field_id, file_id}` | `{}` | E |
| `record.file` | `{id, file_id}` | file bytes | V |
| `table_view.create` | `{table_id, name, type, config?}` | view | E (form: M) |
| `table_view.update` | `{id, name?, config?, position?}` | view | E (form: M) |
| `table_view.delete` | `{id}` | `{}` | E; the last grid view cannot be deleted |
| `table_form.publish` | `{view_id}` | `{url}`, shown once (`ReturnsSecret`) | M |
| `table_form.revoke` | `{view_id}` | `{}` | M |
| `table_import.upload` | `{base_id, table_id? \| name, filename, data_base64}` | `{id}` | M |
| `table_import.preview` | `{id, mapping?}` | `Preview` | M |
| `table_import.run` / `.status` / `.undo` | `{id}` | `DataJob` shape | M (`undo` confirm) |

`record.*` writes are in group `write_items`; `field.*`, `table.*`, `table_form.*` and `table_import.*` in `admin`;
`base.delete` and `table.delete` in `destructive`. No command is a finance command.

## 10. Templates and limits

Templates are data: ten JSON files in `modules/tables/templates/`, embedded like `sites/templates`. Each has
`{key, name, description, tables: [{name, fields, views, records}]}`, with link fields named by table and five to
eight sample records using Indian names, cities and rupee amounts. `base.create {template}` instantiates one.

| Key | Tables | Main view |
|---|---|---|
| `order_tracker` | Orders → Customers | Kanban by Status; order form |
| `client_followups` | Clients, Follow-ups → Clients | Grid grouped by Next call |
| `stock_list` | Items | Grid sorted by Quantity; low-stock filter |
| `dealer_network` | Dealers, Visits → Dealers | Grid grouped by Region |
| `service_tickets` | Customers, Machines → Customers, Visits → Machines | Kanban by Status |
| `attendance` | Workers, Attendance → Workers | Daily entry form |
| `real_estate_inventory` | Projects, Units → Projects | Kanban by Status (Available, Hold, Booked, Sold) |
| `school_records` | Students, Fees → Students | Grid grouped by Batch |
| `event_rsvps` | Guests | Public RSVP form; kanban by Reply |
| `content_calendar` | Posts | Kanban by Stage; grid sorted by Publish date |

Limits by `core.workspaces.plan` **[proposal: numbers for the owner to set]**, in one function `limits(plan)`.
Counts are kept on `tables.bases` and enforced atomically in the write
(`update ... set record_count = record_count + $n where record_count + $n <= $cap`).

| | free | team | business | enterprise |
|---|---|---|---|---|
| Bases per workspace | 3 | 50 | 200 | 1,000 |
| Tables per base | 5 | 20 | 50 | 50 |
| Records per base | 1,000 | 25,000 | 100,000 | 250,000 |
| Attachments per base | 250 MB | 5 GB | 25 GB | 100 GB |
| Form submissions per form per day | 100 | 1,000 | 5,000 | 5,000 |
| History kept | 14 days | 1 year | 1 year | 1 year |

Fixed on every plan: 100 fields per table, 100,000 records per table, 10 MB per file, 20 views per table. Hitting a
limit returns a conflict with the number and what to do ("This base has reached 1,000 records on the Free plan").

## 11. UX

Tokens and parts come from `globals.css` and `components/ui.tsx` (`btn`, `btnPrimary`, `inputCls`, `Modal`, `Pill`,
`Avatar`, `EmptyState`, `ErrorText`). No new colours except eight option colours built from the existing
`ok`, `warn`, `bad`, `accent` pairs plus four neutral tints, each checked in light and dark.

Routes: `/w/[ws]/tables` (base home), `/w/[ws]/tables/[base]?t=<table>&v=<view>&r=<record>`, `/tf/[token]`.
`MODULES` gains `{key: 'tables', label: 'Tables', icon: Table2}`.

**Base home**

```
 Tables                                                        [+ New base]
 +----------------------+ +----------------------+ +----------------------+
 | [#] Orders 2026      | | [#] Stock list       | | [#] Dealer network   |
 | Sales space          | | Operations space     | | Sales space          |
 | 2 tables · 412 rows  | | 1 table · 1,208 rows | | 2 tables · 96 rows   |
 +----------------------+ +----------------------+ +----------------------+

 New base                                                            [x]
 Name [ Orders 2026            ]   Space [ Sales v ]
 ( Blank )   ( From a template )   ( Import a CSV )
 +------------------+ +------------------+ +------------------+
 | Order tracker    | | Client follow-ups| | Stock list       |
 | Orders, Customers| | Clients, Calls   | | Items            |
 +------------------+ +------------------+ +------------------+  ...10
                                                   [Cancel] [Create base]
```

Empty state: "No bases yet. A base holds your tables: orders, stock, follow-ups." with the same three starts.

**Grid**

```
 Orders 2026 v   | Orders | Customers | +            (table tabs)
 [Grid: All orders v] [Kanban: By status] [Form: New order] [+ View]
 [Hide fields 2] [Filter 1] [Group] [Sort 1] [Row height]        [Search]  [Import] [...]
 +----+---------------+-------------+-----------+------------+--------------+---+
 | [] | A Order       | o Status    | Rs Amount | [] Due     | <> Customer  | + |
 +----+---------------+-------------+-----------+------------+--------------+---+
 |  1 | ORD-0001      | (Packed)    |  48,000.00| 28-11-2026 | (Sharma Tr.) |   |
 |  2 | ORD-0002      | (New)       |1,18,000.00| 02-12-2026 | (Gupta & S.) |   |
 |  3 | [ORD-0003__]  | (New)       |           |            |              |   |   <- editing
 +----+---------------+-------------+-----------+------------+--------------+---+
 |  + Add a record                                                              |
 +------------------------------------------------------------------------------+
 412 records                              * Your changes to this view [Save for everyone] [Reset]
```

- First column (checkbox, row number, primary field) is frozen. Header shows a type icon, the name and a lock icon
  when locked. Header menu: Edit field, Sort A→Z / Z→A, Filter by this field, Hide, Insert left / right, Lock, Delete.
- Click selects a cell; Enter, F2 or typing edits; Esc cancels; Tab and arrows move; Shift+arrows extend the
  selection; Delete clears; Ctrl+C / Ctrl+V copy and paste; Space on the row number opens the record panel. A select
  cell opens a list with type-to-filter and "Create option" for managers. A failed save turns the cell border `bad`
  and keeps the typed text with the reason beneath.
- Group shows a header row per value with its count and collapse arrow, and "+ Add a record" at each group's foot,
  pre-filled with the group's value.
- Selecting rows shows a bottom bar: "3 selected", Delete, Copy.
- Empty table: headers stay; the body offers "Add a record", "Paste from Excel (Ctrl+V)" and "Import CSV".
- No filter results: "No records match these filters." and "Clear filters".

**Field dialog** (popover under the header)

```
 +--------------------------------------+
 | Name  [ Status                     ] |
 | Type  [ o Single select          v ] |
 |       One choice from a list         |
 | Options                              |
 |  [=] (o) [ New        ] [x]          |
 |  [=] (o) [ Packed     ] [x]          |
 |  [=] (o) [ Delivered  ] [x]          |
 |  + Add option                        |
 | [ ] Only managers can edit values    |
 |              [Cancel] [Save field]   |
 +--------------------------------------+
```

A type change shows the conversion preview line and relabels the button "Convert 412 records". For a link field the
options are: table, "Allow several records", "Also show in *Customers*".

**Linked-record picker**

```
 +------------------------------------------+
 | Link to Customers                    [x] |
 | [ Search customers...                 ]  |
 |  Sharma Traders     Pune     98765 43210 |
 |  Gupta & Sons       Indore   91234 56780 |
 |  + Create "Shar..."                      |
 | Linked: (Sharma Traders x)               |
 |                                   [Done] |
 +------------------------------------------+
```

**Kanban**

```
 [Stack by: Status v] [Card fields 3] [Filter] [Sort]
 No status  2     (New)  14          (Packed)  6       (Delivered)  390
 +------------+   +--------------+   +--------------+   +--------------+
 | ORD-0007   |   | ORD-0002     |   | ORD-0001     |   | ORD-0003     |
 | Amount     |   | Rs 1,18,000  |   | Rs 48,000    |   | ...          |
 | Rs 9,500   |   | Gupta & Sons |   | Sharma Tr.   |   | Showing 50   |
 +------------+   +--------------+   +--------------+   | [Show more]  |
 + Add            + Add              + Add              +--------------+
```

Stacks are the options of one single-select field, in option order. Dragging a card (dnd-kit, as in
`projects/space-view.tsx`) calls `record.update`; the card menu has "Move to" for keyboard and touch. Each column
loads 50 cards at a time. Viewers and locked stack fields disable dragging.

**Form builder**

```
 +---------------------------------------------+---------------------------+
 |  New order                                  | Form                      |
 |  Tell us what you need.                     | Title   [ New order     ] |
 |  +---------------------------------------+  | Button  [ Submit        ] |
 |  | Your name (required)        [=] [x]   |  | After submit              |
 |  | [                                   ] |  | [ Thank you. We will... ] |
 |  +---------------------------------------+  |                           |
 |  | Phone (required)            [=] [x]   |  | Selected field            |
 |  | Items                       [=] [x]   |  | Label   [ Your name     ] |
 |  | Photo of the order          [=] [x]   |  | Help    [               ] |
 |  +---------------------------------------+  | [x] Required              |
 |  [+ Add a field v]                          |                           |
 |                                             | Public link: Off          |
 |                                             | [Publish and copy link]   |
 +---------------------------------------------+---------------------------+
```

**Public form** (no app shell, 480 px column)

```
            Sharma Traders
            New order
            Tell us what you need.

            Your name (required)
            [                            ]
            Phone (required)
            [                            ]
            Items
            [                            ]
            Photo of the order
            [ Choose files ]  up to 3, 5 MB each, photos or PDF

            [          Submit           ]
            Do not enter passwords in this form.
```

Errors appear under the field and in a summary linked to each field. After submit: the success message and "Submit
another". Revoked or unknown link: "This form is not available."

**Mobile record** (below 640 px the grid becomes a card list; a tap opens this)

```
 <  ORD-0002                    [...]
 ------------------------------------
 o STATUS
 (New)                              >
 Rs AMOUNT
 1,18,000.00
 [] DUE
 02-12-2026
 <> CUSTOMER
 (Gupta & Sons)                     >
 @ PHOTOS
 [img] [img]            [Camera] [+]
 v 4 hidden fields
 ------------------------------------
 Changed by Asha, 24 Nov, 4:10 pm
```

Each value is a 44 px target that opens the right editor; phone values show Call and WhatsApp; the attachment
field's input uses `capture` so the camera opens directly.

## 12. Web components

**Grid library.** `@tanstack/react-table` 9.3.1 (published 12 Oct 2026, peer `react >= 18`) and
`@tanstack/react-virtual` 3.14.28 (22 Nov 2026, peers include `^19.0.0`), both MIT, both the `latest` tag, no beta.
The web app is on React 19.3.1. `home-dashboard-widgets.md` already names Table 9.3.1 as the library to adopt when
a list needs resizing and pinning. Rejected: Glide Data Grid 6.0.3, whose peers stop at React 18 and pull in
`lodash`, `marked` and `react-responsive-carousel`, and whose canvas cells cannot use our tokens or be read by a
screen reader without a second tree; AG Grid Community, where grouping is an Enterprise feature. Table is used only
for column state (sizing, pinning, visibility, order); sorting, filtering and grouping are server-side. Rows are
virtualised, columns are not (100 at most). **[spike]** confirm the v9 feature opt-in API and 60 fps scrolling with
100,000 rows paged 200 at a time.

Files under `apps/web/src/components/tables/`:

| File | Holds |
|---|---|
| `lib/tables.ts` (in `src/lib`) | Types, command wrappers, `mergeViewConfig(shared, mine)` |
| `base-home.tsx`, `new-base.tsx` | Base cards, the three starts, template picker |
| `base-page.tsx` | Table tabs, view tabs, routing by search params, record panel host |
| `view-bar.tsx`, `filter-menu.tsx`, `sort-menu.tsx`, `group-menu.tsx`, `fields-menu.tsx` | Toolbar and its popovers; write through `useScreenConfig` |
| `grid/grid.tsx`, `grid/use-rows.ts`, `grid/selection.ts`, `grid/clipboard.ts` | Grid shell, paged loading, keyboard and range selection, TSV parse and write |
| `cells.tsx` | One display and one editor per field type; shared by grid, record panel, kanban cards and forms |
| `field-dialog.tsx`, `link-picker.tsx`, `record-panel.tsx` | Section 11 |
| `kanban.tsx`, `form-builder.tsx`, `import.tsx` | Section 11 and 8 |
| `app/tf/[token]/page.tsx`, `public-form.tsx` | Public form |

No live collaboration in Phase 1: a cell edit is last-write-wins per field (the server merges
`data || changed - cleared`), and the grid refetches the visible pages on window focus and after its own writes.

## 13. Tests

- **Go unit:** `coerce` and `validate` table tests per type (Indian number formats, bad dates, option names); the
  conversion matrix; the filter compiler (golden SQL plus "an unknown field id is refused"); type guessing; every
  template instantiates within the free plan's limits; `limits`.
- **Go integration** (`server/tables_test.go`, real Postgres as the other suites): tenant isolation (workspace B
  cannot read or link workspace A's records); the role matrix of section 6 including a private space; a locked
  field refused on update, paste and import; link and inverse reads; soft delete, restore and purge; event kinds
  and the batch threshold; plan limits at the boundary; import run and undo; public form (honeypot stores nothing,
  rate limit, revoked and unknown tokens share one body, oversize and wrong-type files refused, link and user fields
  never exposed); every `tables` command has a description and schema; `TestModuleBoundaries` still passes.
- **Benchmark:** `go test -bench` behind `TABLES_BENCH=1` with the 100,000-record seed; prints p95 against the
  section 4 target.
- **Playwright** (`e2e/tables-*.spec.ts`; `table.spec.ts` is the editor's): build an order tracker from blank; grid
  editing by keyboard; paste a 3×4 block copied from a spreadsheet, including a quoted cell with a line break, then
  undo; change a field's type with the preview; kanban drag and "Move to"; publish a form, submit it signed out, see
  the record; CSV import and undo; a viewer cannot edit but can filter; the record page at 390 px.
- Run root `pnpm lint` before every commit.

## 14. Work packages

Step 0 is one commit that touches every shared file, so the later packages own disjoint files and can run in
parallel.

| # | Builder | Owns (creates or edits only these) | Content |
|---|---|---|---|
| 0 | Codex | `migrations/1320_tables.sql`, `1321_record_files.sql`; `modules/tables/tables.go` (command list with stub handlers and all schemas), `types.go`, `store.go` (load base/table/fields, role check, `writable`); one line each in `modules/modules.go`, `worker/wire.go`, `server/server.go`, `files/files.go`; `web/package.json` (two dependencies), `lib/modules.ts`, `lib/tables.ts`, route files with placeholders; `wizard.tsx` exports | Scaffolding that compiles, migrates and passes existing tests |
| 1 | Sonnet | `tables/bases.go`, `structure.go`, `fields.go`, `coerce.go`, `convert.go`, `limits.go`, `templates.go` and tests | Bases, tables, fields, validation, conversion, limits, template instantiation |
| 2 | Sonnet | `tables/records.go`, `query.go`, `links.go`, `history.go`, `paste.go`, `events.go`, `purge.go` and tests | Record commands, filter and sort compiler, links, history, events, sweeps |
| 3 | Sonnet | `tables/views.go`, `forms.go`, `imports.go`, `files.go`; `server/tablesform.go`; `files/record.go` and tests | Views, public form, CSV import job, attachment commands |
| 4 | Sonnet | `components/tables/grid/*`, `cells.tsx` | Grid, editors, clipboard, undo |
| 5 | Sonnet | `base-home.tsx`, `new-base.tsx`, `base-page.tsx`, `view-bar.tsx`, the four menus, `field-dialog.tsx`, `link-picker.tsx`, `record-panel.tsx`, route pages | Shell, toolbar, dialogs, mobile record |
| 6 | Sonnet | `kanban.tsx`, `form-builder.tsx`, `public-form.tsx`, `app/tf/[token]/page.tsx`, `import.tsx` | Kanban, forms, import |
| 7 | Tabnine | `tables/templates/*.json` (10), `tables/testdata/*.csv`, `tables/testdata/seed.sql` (100,000-record generator), `e2e/fixtures/tables/*` | Templates and test data |
| 8 | Lead | `server/tables_test.go`, `e2e/tables-*.spec.ts`, `docs/hooks.md` (the `record.*` events) | Integration, e2e, review |

Waves: 0, then 1, 2, 3, 4 and 7 together (4 works against the stubbed commands and `lib/tables.ts` types), then 5
and 6, then 8. Packages 2 and 3 call `coerce` and `validate`, whose signatures are fixed in step 0's `types.go`.

## 15. Left out of Phase 1

Formula, lookup, rollup and a separate count field; date with time; unique or required constraints on fields
(required exists on forms only); nested filter groups; calendar, gallery and timeline views; record comments;
XLSX import and any export except copy; file types beyond images and PDF; links across bases; live multi-user
cursors; table triggers in the automation engine; the Expo app; Hindi.

## 16. Open questions

1. **Spike target.** Is 300 ms p95 warm (1 s cold) for filter plus sort on 100,000 records the agreed number? It
   decides whether Phase 1 ships with no per-field index.
2. **Who may change structure.** This design keeps fields and tables to managers (workspace admins and space
   admins). In a five-person team that may mean only the owner can add a column. Should space editors be allowed to
   add fields but not delete them?
3. **Plan limits.** The numbers in section 10 are placeholders; the research proposed per-table caps, the brief
   per-base caps. Which unit is sold?
4. **File types.** Small businesses will want to attach Excel and Word files. Widening the files allow-list is a
   decision for the files module (it affects every upload), so it is not made here.
5. **Automations in Phase 1.** Webhooks and AI employees react to `record.*`; the rule builder does not. Is that
   enough for "automations work", or should "when a record matches, notify a person" be pulled forward?
6. **Public form identity.** Should a form be able to require an OTP on the phone field (attendance and RSVP
   templates would benefit)? Not designed.
7. **Batch events.** Above 20 records a command emits one batch event with ids, not values. Do webhook users need
   per-record payloads for imports?
8. **Where a base lives.** Bases sit in spaces and reuse space roles. The alternative, bases at workspace level with
   their own member list, is more familiar to Airtable users but needs a new permission table.

## 17. Binding revisions (owner decisions, design review, spike)

This section overrides anything above that disagrees with it. Builders read it first.

### 17.1 Owner decisions (2026-11-25)

1. **Scope split.** Phase 1a: bases, tables, grid view, scalar field types, linked records, kanban view, CSV
   import, paste, record panel, templates. Phase 1b: public forms and attachments. Later (Phase 2+): conversions
   that can lose data, import and paste undo, AI employees triggered by `record.*`, per-field indexes.
2. **Structure rights.** Anyone who can write in the space adds fields and renames fields, options and tables.
   Deleting a field or table, and changing a field's type, need a space manager (workspace admin, or space admin).
3. **Limits are records per base**, set per plan, plus a hard cap of 100,000 live records per table on every plan
   (the size the spike proved). Placeholder plan numbers in section 10 stay placeholders until pricing is set;
   the code reads them from one table in `limits.go`.
4. **Attachments (1b)** accept images, PDF, `.xlsx`, `.docx`, `.csv`, download-only (no preview for office files).
   The allow-list change is made in the files module and applies to every upload; files are served with
   `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`.

### 17.2 Querying (spike: docs/design/tables-spike.md)

- Under tenant RLS, `cnote_app` cannot use any index on `data` for a filter, because `->>`, casts, `@>` and
  `ILIKE` are not `LEAKPROOF`. **Phase 1a creates no index on `data`** and no `LEAKPROOF` objects, so the
  migration needs no superuser. Indexes: PK `(workspace_id, table_id, id)`, the partial default-order index
  `(workspace_id, table_id, created_at, id) WHERE deleted_at IS NULL`, both link indexes; `fillfactor = 80`.
- Measured without a data index on 100,000 records beside a 500,000-record neighbour: every filter, filtered
  count, group-by and first-page sort is at most 112 ms p95. The **target is 300 ms p95 warm** for any grid
  query on a table at the cap; the spike meets it.
- **Keyset pagination only** (token = last sort value + record id, id always the tie-break), page size 100–200.
  No OFFSET in the API. Counts are separate and cached per view for 30 s.
- Upgrade path, not built in 1a: per-field partial btrees with `LEAKPROOF` wrappers (needs a superuser
  migration role, check the host), or the typed side table. Prepared statements with generic plans were not
  tested; the query builder inlines no user text and uses ordinary parameters, and e2e times a 100k table.

### 17.3 Permissions (review 1, 2)

- One resolver `baseRole(ctx, baseID) -> none|read|write|manage` in `store.go`, built from workspace role,
  space visibility and space membership (the same inputs `access.AssertSpaceAccess` uses, plus space admin).
  Every command that takes an id loads the base through it; mixed-id bulk calls reject the whole call if any id
  is outside the caller's bases. List commands filter by `access.ReadableSpaceIDs`. Tests: private space,
  open-space viewer, guest, API key, AI employee principal, mixed-id bulk.
- **Events carry no values.** `record.created|updated|deleted|restored` payloads hold `base_id`, `table_id`,
  `record_id`, `field_ids` changed and the actor, never field values or record titles. A consumer that needs
  values calls `record.get`, which applies the consumer's own access. Webhooks set up by a workspace admin may
  then read private-space records only if that admin can; this is the stated policy.

### 17.4 Data safety (review 5, 6, 9)

- **Type changes in 1a are only the lossless ones:** anything → text (display text, documented as display
  text, not round-trippable), number ↔ currency ↔ percent, single select → multi select, text → single/multi
  select (new options from distinct values, 200 max), phone/email/URL ↔ text. Every other change is refused
  with "Create a new field and copy the values" until a later phase. Conversion runs in the command's
  transaction for tables up to the cap (measured bulk update: 8 ms per 1,000 rows, so ~1 s at 100k), so there
  is no async job, no preview/version skew and no partial state.
- **No undo of import or paste in 1a.** Paste and import are ordinary writes recorded in history; the UI says
  "Imported 1,240 records" with a link to filter by import id, and the user can select and delete them.
  In-grid single-cell undo (Ctrl+Z on your own last edit, while the cell still holds your value, compared by
  `updated_at`) stays.
- **Links:** `tables.links (workspace_id, base_id, field_id, from_record, to_record)`, composite FKs with
  `workspace_id`; a trigger-free check in the write path that the field is a link field of the source table,
  the target is in the field's target table and the same base, and the single-link cardinality holds (taken
  under `SELECT … FOR UPDATE` on the source record). Soft-deleted targets are hidden from the cell and counts and
  reappear on restore. Deleting a link field soft-deletes it with its links; purge (after 30 days) hard-deletes
  records, fields and their links. Deleting a table is blocked while another table has a link field to it.

### 17.5 Events and AI (review 10, 13)

- One event per record up to 100 records; above that a single `record.batch` event `{op, table_id, count,
  batch_id}` and `record.batch_list(batch_id)` returns the ids (access-checked). `docs/hooks.md` documents it.
  Restore, import and bulk delete all use the same rule.
- AI employees are not triggered by `record.*` in 1a (agent triggers ignore the `record.` prefix). Read commands
  (`base.list`, `table.get`, `record.list`, `record.get`) and `record.create|update` are added to the general
  AI-employee template allowlist; structure and delete commands are not. Command names are checked against the
  full registry in a test.

### 17.6 Uploads and import (review 8, 12)

- CSV import uploads through a streaming HTTP endpoint `POST /api/tables/import` (same pattern and limits as
  `server/imports.go`, 10 MB, authenticated, base write access) that returns an upload handle; `table_import.*`
  commands take the handle. MCP clients import by sending rows in `record.create_many` (max 500 per call,
  2 MB body). The Tables import UI is its own small wizard reusing `ReadCSV` and the column-match component only
  if it can be exported without changing the existing wizard's behaviour; otherwise a copy. Cells keep their
  whitespace (a Tables-specific reader option), and the import is idempotent per upload handle.

### 17.7 Per-person view state (review 11)

- `ui_config` holds only: hidden field ids, column widths for shown fields, row height, collapsed groups. Field
  order, filters, sorts and groups live on the shared view. Saves are debounced 800 ms; a quota or rate error
  keeps the state in memory and shows "Your layout couldn't be saved". A test saves the worst case (100 fields)
  through the real command and stays under 16 KB.

### 17.8 Phase 1b, recorded now so 1a leaves room (review 3, 4)

- Attachments: streaming upload endpoint, staged object with a pending row, attach in the record transaction,
  delete by a sweep that removes objects whose row is gone (no reliance on FK cascade). Internal
  `file.record_*` commands check that the file belongs to that record and field.
- Public forms: 128-bit random token stored hashed; rate limits checked before the body is read (per token,
  per address via trusted-proxy IP, global), counters outside the submission transaction; per-form daily
  submission cap and a storage budget reserved atomically; honeypot only as an extra. Uploads on forms are
  optional per form and limited to 3 files of 5 MB.

### 17.9 Work packages for 1a (replace section 14)

| # | Owns | Content |
|---|---|---|
| 0 | `migrations/1320_tables.sql`; `modules/tables/{tables,types,store}.go`; one line each in `modules/modules.go`, `server/server.go`; `web/package.json` (TanStack table + virtual), `lib/modules.ts`, `lib/tables.ts`, route placeholders | Schema, command list with stubs and schemas, `baseRole`, compiles and migrates |
| 1 | `tables/{bases,structure,fields,coerce,convert,limits,templates}.go` + tests | Bases, tables, fields, validation, lossless conversion, limits, templates |
| 2 | `tables/{records,query,links,history,paste,events,purge,imports}.go`, `server/tables_import.go` + tests | Records, keyset query compiler, links, history, events, import, purge sweep |
| 3 | `components/tables/grid/*`, `cells.tsx` | Grid, editors, clipboard, single-cell undo |
| 4 | Base home, new base, base page, view bar, menus, field dialog, link picker, record panel, kanban, import UI, route pages | Shell and views |
| 5 | `tables/templates/*.json` (10), `testdata/*` | Templates and test data |
| 6 | `server/tables_test.go`, `e2e/tables-*.spec.ts`, `docs/hooks.md` | Integration, e2e (including a 100k-record timing check), review |

Open questions 5–8 in section 16 are answered by 17.3, 17.5 and "bases live in spaces" (kept). Question 6
(OTP on forms) moves to 1b.
