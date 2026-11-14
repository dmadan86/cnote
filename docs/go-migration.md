# Rust to Go migration (strangler)

Status: phase 1. `apps/api` is a Go server in front of the Rust server. It runs `space.list`, `note.get`
and `note.history` natively and proxies everything else, byte for byte. Clients (web, mobile, MCP
clients, the collab WebSocket) cannot tell the difference.

## Design

```
client --> Go :4100 --+-- POST /api/w/{ws}/commands/{name}, name in Go registry --> native handler (pgx)
                      +-- GET /healthz                                           --> "ok"
                      +-- everything else (all paths, /collab and /mcp upgrades) --> reverse proxy --> Rust :4000
```

- `cmd/api/main.go`: env `PORT` (4100), `DATABASE_URL` (role `cnote_app`, never the admin URL),
  `RUST_API_URL` (http://localhost:4000), `DB_POOL_MAX`, `LOG_LEVEL=debug` (one line per native command or
  proxied request, to check routing).
- `internal/server`: the front door. It dispatches by hand rather than with `http.ServeMux`, because
  ServeMux redirects unclean paths (`//`, `..`) and the proxy must behave exactly like Rust.
- `internal/proxy`: `httputil.ReverseProxy`. Keeps the original `Host` (Rust derives OAuth/MCP origins from
  it), cookies, escaped path and query; adds no `X-Forwarded-*`; streams (`FlushInterval: -1`); WebSocket
  upgrades pass through.
- `internal/command`: port of `cnote-core/src/command.rs`. `Registry.Execute` runs one tenant transaction
  and applies, in the Rust order: min role, API-key scopes, rate limit, agent tool groups, idempotency
  replay, confirmation, handler, idempotency store, usage record. `Ctx.Emit` writes `core.events` and the
  `event.dispatch` job in the same transaction. Typed `*command.Error` has the Rust codes, statuses and JSON.
- `internal/auth`: `principal_for` (bearer API key, `cs_` native session, `cn_session` cookie), same SQL.
- `internal/access`: port of `access.rs` (space access, readable spaces, `core.note_access`, note access).
- `internal/db`: pool plus `BeginTenant` (`set_config('app.workspace_id', $1, true)`); RLS does the rest.
- `internal/modules`: the registry. Each ported module is a package returning `[]command.Command`.

### Collab WebSocket

`apps/collab` is the TypeScript port of the live-editing socket of `crates/cnote-collab` (y-websocket protocol, same
close code 4409, same tables). Set `COLLAB_URL` (e.g. `http://localhost:4300`; `ws://` also accepted) on the Go server
and `/collab/...` goes there; unset keeps it on Rust. Revision commands (`note.revisions|revision|restore`) stay in Rust.
Service env: `DATABASE_URL`, `COLLAB_PORT` (4300), `COLLAB_ALLOWED_ORIGINS`, `COLLAB_DEBOUNCE_MS`.

### Fallback: how byte-compatibility stays cheap

A handler may return `command.ErrFallback`; the router then replays the buffered request to Rust. Used for:
inputs serde would reject (missing or mistyped `id`, odd UUID spellings, wrong body shape, invalid JSON),
unparsable `X-Confirmation-Id`, bodies over 2 MiB. Rust produces its own error
text, so those paths are identical by construction and we never re-implement serde error messages.

### Response format rules (learned from the differential test)

- Rust serializes handler output through `serde_json::Value`, so every JSON object has **sorted keys**
  (no `preserve_order`). `command.serialize` does the same; struct field order in Go is irrelevant.
- Timestamps: chrono `AutoSi`: 0, 3 or 6 digits of fraction then `Z` (`command.Ts`). Dates: select `col::text`.
- jsonb columns go through `command.Norm`. Numbers keep their written form.
- Go escapes U+2028/2029 in strings, serde does not: `command.Marshal` fixes this.
- Never `omitempty` slices: empty lists serialize as `[]`, not `null`. Nullable columns are pointers.
- UUIDs travel as text: select `id::text`, bind `$1::uuid`, arrays as `$1::text[]::uuid[]`.
- Errors: `{"error":{"code","message"[,"details"]}}`, `Content-Type: application/json`, no trailing newline.

## How to port a module

Do one module at a time; Rust stays the fallback until the Go side is proven.

1. **Read the Rust module** (`Command::new` list at the bottom of the file): name, group, `min_role`,
   `.confirm()`, `.returns_secret()`, input struct, output struct, SQL.
2. **Handlers**: new package `internal/modules/<name>`, `Commands() []command.Command`, built with
   `command.NewCommand(name, group, handler)`; set `MinRole`/`Confirm`/`ReturnsSecret` like Rust (note
   `note.update` style `.min_role(Guest)` overrides). Decode input leniently; anything serde would reject
   returns `ErrFallback`. Copy the SQL verbatim.
3. **Access checks**: call `access.AssertNoteAccess` / `AssertSpaceAccess` / `ReadableSpaceIDs` /
   `NoteLevel` exactly where Rust does, in the same order (error precedence is part of the contract).
   Filter lists with `core.note_access(...) <> 'none'` in SQL.
4. **Events**: every Rust `ctx.emit(kind, note_id, payload)` becomes `c.Emit(kind, &noteID, payload)` with
   the same kind and payload keys. Rust background workers consume these (`core.events`, `core.jobs`), so a
   missing or renamed event silently breaks automations, notifications and hooks.
5. **Idempotency / confirmation / scopes**: nothing to do per command; the registry applies them. Check that
   commands with secrets set `ReturnsSecret`, and money-moving or outbound ones set `Confirm`.
6. **Cross-command calls**: Rust `ctx.call(...)` has no Go equivalent yet. If a command calls another one that
   is not ported, port both together or keep the caller in Rust.
7. **Tests** (all against the real DB, no mocks):
   - Integration tests in `internal/server` or the module package: auth variants, tenant isolation, role
     and scope gates, access edge cases, emitted events, idempotent replay.
   - **Differential test**: add the commands and representative inputs and callers (owner, member, guest,
     agent key, API key, other workspace, no auth, bad key, not-found ids) to
     `internal/server/differential_test.go`. Create fixtures through Rust endpoints, call both servers,
     require equal status and equal body bytes. Mutating commands: run on two identical fixtures and
     compare responses after normalizing ids and timestamps that the test itself cannot control, then compare
     the resulting rows (events included).
   - Port or reference the matching Rust tests (table below); the behavior they assert must hold in Go.
8. **Flip routing**: registering the commands in `internal/modules/modules.go` is the flip. Verify with
   `LOG_LEVEL=debug` that the traffic is native, run `pnpm e2e` through the Go server (below), then keep the
   Rust handlers for one release as the rollback path (remove the module from `modules.go` to roll back).
9. **Delete Rust code** only after a release without regressions; Rust tests for that module move to Go.

## Running the full e2e through Go

The web app bakes `API_URL` into its rewrites at **build** time, so it must be rebuilt, not just restarted:

```
lsof -ti tcp:4100 -sTCP:LISTEN | xargs kill; lsof -ti tcp:3000 -sTCP:LISTEN | xargs kill
set -a; . ./.env; set +a
(cd apps/api && go build -o /tmp/cnote-api ./cmd/api) && LOG_LEVEL=debug PORT=4100 /tmp/cnote-api &
(cd apps/web && API_URL=http://localhost:4100 pnpm build && API_URL=http://localhost:4100 pnpm start --port 3000) &
API_URL=http://localhost:4100 pnpm e2e
```

Browser live-editing keeps talking to Rust directly (`NEXT_PUBLIC_COLLAB_URL`). Rebuild with
`API_URL=http://localhost:4000` to switch back.

## Proposed module order

Small, read-heavy, few events first; money, outbound and background-coupled last.

1. **core notes (rest)**: `note.list`, `search.notes`, `view.list`, `comment.list`, `relation.*`, then writes
   `note.create/update/delete`, `comment.*`, `space.create`. Highest traffic; the editor depends on it.
   (Rust `notes.rs`, 16 commands.)
2. **sharing**: `note.restrict/share/unshare`, `shared_with_me`, page access info. Small and already
   covered by `page_permissions.rs`.
3. **admin**: members, agents, API keys, settings. Needs argon2/secret handling only for keys (sha256).
4. **notifications**.
5. **auth endpoints** (`/api/auth/*`, session cookies, argon2id): not commands; port after the registry
   covers most commands. Keep the exact `Set-Cookie` and the `x-cnote-client: native` token behavior.
6. **docs** (`cnote-docs`: living pages, templates, bindings). Depends on freshness events and the worker.
7. **projects** (`cnote-projects`: items, workflows, evidence, GitHub webhook).
8. **marketplace**, **importers**, **ai** (providers, hooks, BYO keys; needs `CNOTE_MASTER_KEY` crypto parity).
9. **finance/billing**: money-moving, `confirm` flows, plan limits. Port with the strictest differential tests.
10. **Last**: collab WebSocket and revisions (Yjs); MCP and OAuth moved in Wave 3 (see below). These stay proxied as long
    as needed; the worker (`event.dispatch`, jobs, sweeps) moves only after the modules it calls are in Go.

## Rust tests that map to each module

| Module | Rust tests |
| --- | --- |
| command layer, notes, access, idempotency, rate limit | `crates/cnote-core/tests/command_layer.rs` |
| sharing, page restrictions | `crates/cnote-core/tests/page_permissions.rs` |
| marketplace | `crates/cnote-core/tests/marketplace.rs`, `crates/cnote-server` marketplace routes |
| docs (living pages, templates) | `crates/cnote-docs/tests/living_pages.rs`, `templates.rs` |
| projects | `crates/cnote-projects/tests/projects.rs` |
| finance/billing | `crates/cnote-finance/tests/finance.rs` |
| ai, hooks | `crates/cnote-ai/tests/ai.rs`, `crates/cnote-server/tests/hooks.rs` |
| importers | `crates/cnote-importers/tests/import.rs` |
| MCP | `crates/cnote-mcp/tests/http.rs` |
| OAuth | `crates/cnote-server/tests/oauth.rs`, `e2e/oauth.spec.ts` |
| collab | `crates/cnote-collab/tests/collab.rs`, `e2e/collab.spec.ts`, `e2e/versions.spec.ts` |
| UI flows | `e2e/*.spec.ts` (run through the Go server before every flip) |

## Known limits of phase 1

- Rate limiting is an in-process bucket per server (as in Rust), so a caller can briefly use both budgets
  while commands are split across the two servers.
- Invalid inputs are served by Rust (fallback), not natively. OAuth access tokens (`cno_`) are native since Wave 3.
- `ctx.call` (nested commands) and event/job handlers are not ported; the Rust worker still consumes
  `core.events` and `core.jobs` written by Go.
- The Go server needs `DATABASE_URL`; `RUST_API_URL` must be reachable or non-native traffic returns 502.
- pgx v5.8.0 is the newest release that builds with Go 1.24; later pgx releases require Go 1.25.

## Module note: sharing (ported)

`note.permissions`, `note.restrict`, `note.share`, `note.unshare`, `note.shared_with_me` run natively
(`apps/api/internal/modules/sharing`), same SQL and events (`note.restricted`, `note.shared`,
`note.unshared`) as `sharing.rs`. Inputs are decoded with exact-case keys (serde is case sensitive,
`encoding/json` is not); anything serde would reject falls back to Rust. Tests: `internal/server/sharing_test.go`
(port of `page_permissions.rs`; unported commands it uses are proxied to Rust) and
`TestDifferentialSharingWrites` (twin fixtures, responses plus permission rows and events compared).
The differential test waits for the Rust worker to drain jobs (`settle`) before comparing `note.history`,
because the worker adds asynchronous `doc.bindings_synced` events.
## Admin port

`internal/modules/admin` now registers all 12 commands from `cnote-core/src/admin.rs`:
`member.list`, `member.add`, `agent.create`, `agent.list`, `apikey.create`, `apikey.list`,
`apikey.revoke`, `workspace.get`, `workspace.update`, `confirmation.list`, `confirmation.decide`,
and `usage.summary`.

- Both key-creation commands set `ReturnsSecret`: repeated idempotency keys create fresh credentials,
  and no response is persisted in `core.idempotency`. Keys use 24 random bytes encoded as unpadded
  base64url, an eight-character random hex prefix, and lowercase SHA-256 hex of the full key.
  Integration tests authenticate Rust-issued keys in Go and Go-issued keys in Rust, for both personal
  and agent keys, and check revocation in both servers.
- Role defaults and overrides match Rust (including member-level personal key creation and human
  confirmation decisions). Admins may add guests but cannot change the owner or an admin peer.
  Agent ownership, default scopes, human-only creation, and workspace tool-group gates are preserved.
- Confirmation decisions only change pending requests, record the deciding person and event, and do
  not execute the requested command. Tests cover rejection, immutable decisions, authoritative approved
  input and single-use consumption through the existing command gate.
- The package has small private key-generation and slug helpers because those Rust auth helpers have
  no exported Go counterpart. Rust's expanding lowercase of U+0130 is reproduced. Email inputs with
  uppercase Greek sigma use Rust fallback for contextual lowercase parity. Malformed/duplicate inputs
  use the existing fallback contract; no whole admin command remains proxied.
- `internal/server/admin_test.go` adds real-Postgres integration and differential coverage based on
  `command_layer.rs` security/agent tests and the confirmation flow in finance/MCP tests. It compares
  deterministic responses byte for byte across owner/admin/member/guest, foreign-workspace, personal
  key, agent key and unauthenticated callers. Twin fixtures compare writes and database effects,
  including events and dispatch jobs. UUIDs, fixture slugs, timestamps, and freshly generated secret
  material cannot be identical across independent writes: generated identifiers are mapped or removed,
  secret response shapes are compared, and each stored hash is verified against its returned key.
  Settings include Unicode, empty and large values and integers beyond JavaScript's exact range.
  Usage tests check aggregation, the 30-day window, and tenant isolation. Differential usage reads
  restore the fixture's meter between servers because each machine read records another call.

Validation: from `apps/api`, with the worktree `.env` loaded, run `go vet ./...`,
`test -z "$(gofmt -l .)"`, and `go test -race -count=1 ./...`. The shared Rust reference stays running;
Go HTTP servers are in-process `httptest` listeners. Browser e2e was not run: this worktree's brief
forbids starting servers on the web/API ports used by that workflow.
## Module 1 (core notes, rest): ported

`space.create`, `note.create`, `note.update`, `note.list`, `note.delete`, `relation.add`, `relation.remove`,
`comment.add`, `comment.list`, `comment.resolve`, `search.notes`, `view.create`, `view.list` now run in Go
(`internal/modules/notes`, with `space.list`, `note.get`, `note.history`).

- Input decoding (`decode.go`) mimics serde: wrong types, `null` for non-Option fields, non-canonical UUIDs,
  lone surrogates, non-`YYYY-MM-DD` dates, non-integer numbers and float/huge numbers inside `view.create`
  `options` fall back to Rust, so error text stays identical.
- `notify_mentions` and `checkpoint` (page revisions) are ported privately inside the package; the
  notification read commands stay with their own module. Mention order follows first appearance (Rust iterates a
  HashSet, so its order is arbitrary).
- `search.notes` rank is a Postgres `real`; Rust widens it to f64 through `serde_json::Value`, so the Go side
  prints the f64 widening (`Rank`). View `options` are re-printed the way serde prints f64 (`normValue`);
  `command.Norm` keeps numbers as written, which differs for fractions with trailing zeros and integers beyond u64.
- Rows with equal `search.notes` rank or equal view/space names have no defined order (same in Rust), so the
  twin-fixture test compares them order-insensitively and the same-fixture raw-byte comparison checks real order.
- Tests: `internal/server/notes_write_test.go` ports the Rust `command_layer.rs` and `page_permissions.rs`
  assertions; `internal/server/notes_diff_test.go` runs one script (~690 steps) against twin fixtures through Rust
  and Go and compares normalized responses, resulting rows and events, then raw-compares all read commands.
  The differential test takes ~70s because it paces itself under the fair-use rate limit of the shared Rust server.
## Module notes: notifications (port/notifications)

Commands ported: `notification.list`, `notification.read`, `notification.unread_count`.
Package: `apps/api/internal/modules/notifications`.

**`notify_mentions` not ported**: the helper that creates notifications is called by `note.update` and
`comment.add` (Rust side); it is being ported by a separate agent. Go reads from `core.notifications`
rows that the Rust worker writes, which is correct for phase 1.

**`me()` check**: all three commands require `Principal.UserID != nil`. API keys and agent keys that
resolve through `core.resolve_api_key` always carry a `user_id` column value, so they can call
notification commands as the owning user. Pure unauthenticated requests get 401 (before the command
gate) and the `me()` check is a defence-in-depth 403 guard matching the Rust `me()` helper message
`"notifications belong to people"`.

**`notification.read` input**: the `id` field is `Option<Uuid>` in Rust with `#[serde(default)]`.
Go decodes it as `*string` with the same semantics: absent or JSON `null` → mark-all; present and
valid UUID → mark one. Invalid UUID spellings (no dashes, wrong length) fall back to Rust so serde
error messages stay identical.

**Serialisation**: `notification.read` and `notification.unread_count` return plain `map[string]any`
(`{"updated":n}` / `{"count":n}`). The `n` values are `int64`; `command.serialize` → `Norm`
keeps them as integers matching Rust's `serde_json::json!({...})` output.

**Differential test design**: read-only calls (`list`, `unread_count`) and zero-effect mutations
(bad UUID, missing UUID from `notification.read`) run byte-for-byte against both servers with the
same fixture. Mutating calls (`notification.read` mark-all and mark-by-id) use twin fixtures: one
dedicated workspace per server so each starts from `updated=1` and responses are still byte-identical.

## Module notes: finance billing (port/finance-billing)

Commands ported (`apps/api/internal/modules/billing`): `invoice.create_draft`, `invoice.update_draft`, `invoice.preview`,
`invoice.get`, `invoice.list`, `invoice.issue`, `invoice.cancel`, `invoice.write_off`, `payment.record`, `credit_note.create`,
`invoice.generate_irn`, `invoice.cancel_irn`, `report.gst_summary`, `report.receivables_ageing`, `report.gstr1`,
`ledger.trial_balance`, `export.tally`. CRM commands (`contact.*`, `deal.*`) and the `finance.irn_window` sweep and
the chart-of-accounts workspace hook stay in Rust.

- Money is int64 paise; GST math (`gst.go`) uses `math/big` for qty x price and tax so half-up rounding and the
  overflow errors match `rust_decimal`/i128. CGST gets the floor half, SGST the odd paisa. Messages of `GstError` are verbatim.
- IRN goes through the `GspClient` interface (`irn.go`, `SetGSP` = Rust `set_gsp`). Rust never installs a real GSP, so
  the deterministic offline sandbox (same IRN hash, ack number, signed-QR payload) is the only implementation, and
  the IRN commands are ported, not left in Rust.
- `quantity` is a `serde_json::Number` fed to `rust_decimal`: plain integers keep their text, fractions go through
  f64/ryu (`2.50` -> `2.5`, `1.0` -> `1.0`); exponents, >15 significant digits and values below 1e-3 fall back to Rust.
  Years outside 0001-9999 and year 0000 dates fall back too.
- Reports: `rupees()` prints the f64 the way ryu does (`18.0`, `1180.5`). `report.gstr1` was ported with the rest of
  reports.rs (b2cl limit by invoice date: Rs 2.5 lakh before 2024-08-01, Rs 1 lakh from).
- `notes.InsertNote` / `notes.AddRelation` (new `notes/export.go`) are the Go `insert_note` / `add_relation` used for the
  invoice and credit-note pages and the `bills` relations. `billing/decode.go` is a private copy of the notes serde-style
  decoder (its methods are unexported).
- Fix in the shared registry: the agent confirmation-request response is now built from a map so its keys are sorted like
  Rust's `serde_json::Value` (`confirmation_id` before `confirmation_required`); the byte comparison caught it.
- Order that Rust itself leaves undefined (so the twin-fixture comparison sorts it, the same-database raw comparison does
  not): lines inside one Tally voucher (`order by journal_lines.id`, a random uuid) and invoices of the same date in
  `report.receivables_ageing`.
- Tests: `internal/modules/billing/unit_test.go` (ports of the `#[cfg(test)]` modules in gst/irn/ledger/billing/reports.rs),
  `internal/server/billing_test.go` (port of every command-related test in `crates/cnote-finance/tests/finance.rs`, plus
  idempotent replay, role gates, preview-equals-issue and worker-visible events/jobs), `internal/server/billing_diff_test.go`
  (`TestDifferentialBillingWrites`: ~1300 steps on twin fixtures, responses and billing/ledger/event/confirmation/idempotency
  rows compared, then 280 raw-byte read comparisons per fixture). The `irn_window_sweep` assertions of
  `irn_window_is_enforced_for_large_taxpayers` are not ported: the sweep is a Rust worker job, not a command.

## Module notes: docs (port/docs)

Ported (`apps/api/internal/modules/docs`): `doc.verify`, `doc.set_owner`, `doc.flags`, `doc.proposals`,
`doc.proposal_decide`, `doc.archived`, `doc.restore`, `doc.freshness_score`, `capture.list`, `capture.answer`,
`template.save`, `template.list`, `template.fields`, `template.render`, `template.refresh`, `template.delete`.
Left in Rust: `doc.ask` and `doc.capture` (AI provider), the `seed_templates` workspace hook, the binding/freshness
event handlers, the `docs.propose_edit` job and the freshness sweep.

- `doc.restore` restores an archived living page (stale again, verify-by +14 days, `date_lapsed` flag); it is unrelated
  to `note.restore` (collab revisions). `doc.archived` was not in the task list but is the same module and trivial, so
  it is ported too.
- `doc.proposal_decide` approves through a nested `note.update` (Rust `ctx.call`). `command.Ctx.Call` runs another
  command through the whole gate (role, scope, rate limit, agent groups, usage), exactly like Rust, so a machine caller
  records usage for both commands and a rate-limit token for both. Epoch/version are not passed (as in Rust), so the
  edit never conflicts with collab epoch. Proposed text containing U+FFFD makes the nested decoder fall back, which
  replays the whole request to Rust.
- Exports added to `internal/modules/notes/export.go` (smallest change instead of copying): `ParseObj`/`Obj` getters,
  `InsertNote` (Rust `insert_note`), `F64` (serde f64 printing; scores print `1.0`, `0.5`).
- `template.render` of boards substitutes inside the JSON of the ```tldraw fence and re-prints it like
  `serde_json::Value` (sorted keys). Numbers serde prints in exponent form (or rejects) fall back to Rust.
  Placeholder regex uses an explicit Unicode White_Space class because RE2 `\s` is ASCII only.
- Tests: `internal/server/docs_test.go` (ports of `living_pages.rs` and `templates.rs`; rows the worker would write
  are seeded with SQL, `doc.ask`/`doc.capture` are proxied to Rust), `internal/modules/docs/docs_test.go` (ports of the
  `templates.rs` unit tests), `internal/server/docs_diff_test.go` (`TestDifferentialDocs`: twin fixtures, ~630 steps
  compared on normalized responses, docs/notes/events/jobs/usage rows, then 480 raw read comparisons).

## Module notes: project items and workflow (port/projects-items)

Commands ported (`apps/api/internal/modules/projects`): `item.create/get/update/list/transition/assign/rank`,
`triage.list/accept`, `workflow.get/update`, `space.drift/realign`, `evidence.record`. Sprints, versions, components
and automations (`planning.rs`, `automation.rs`) are a separate port and stay proxied.

- **CNQL** (`cnql.go`, `rustcompat.go`): lexer, parser and SQL compiler ported rule for rule; messages and 1-based
  column positions are identical. Rust/chrono behaviour the language depends on is re-implemented: `str::to_lowercase`
  (final sigma, U+0130), `char::is_alphanumeric`, `Uuid::parse_str` (simple/hyphenated/braced/urn), chrono's lenient
  `%Y-%m-%d` style parsers (1-digit fields, leading spaces, signed years), `parse_from_rfc3339`, month arithmetic that
  clamps the day, and sqlx's truncation of timestamps to microseconds. All Rust unit tests are Go table tests
  (`cnql_test.go`, `workflow_test.go`); `internal/server/cnql_diff_test.go` compares ~2000 queries byte for byte.
- **Workflow engine** (`workflow.go`): definitions are decoded with exact keys and serde's strictness; anything serde
  would reject (and non-integer numbers in `set_field` values) falls back to Rust for the error text.
- **Shared helpers** are exported for the planning/automation port: `LoadItem`, `TransitionItem`, `SpaceWF`,
  `DefinitionFor`, `RankBetween`, `FilledFields`, `ParseCNQL`/`CompileCNQL`, `ItemMatches`, `MatchingItemIDs`, `Definition`.
- **`ctx.call`**: new `command.Ctx.Call(name, input)` runs another command in the same transaction through the full gate
  (role, scopes, rate limit, agent groups, usage), like Rust. `item.update` uses it for `note.update`, so a key scoped to
  `write_items` only gets 403 when it changes title/description, as in Rust. `notes.InsertNote`, `notes.F64` and
  `notes.NormValue` are the exported notes helpers.
- Events and jobs are emitted exactly as in Rust (same kinds, payloads, actors), so the Rust worker still reacts
  (`item.assigned_to_agent` etc.); `TestDifferentialProjectItems` compares events and the `event.dispatch` job kinds.
- Not byte-identical by nature: item order is undefined in Rust when rank/number tie across spaces (unfiltered
  `item.list`), and `item.get` with an `id` and a `key` of two different items returns an arbitrary one. Tests avoid both.
- Tests: `projects_test.go` (port of the Rust item/workflow/evidence tests), `projects_diff_test.go` (twin fixtures,
  ~1500 compared steps and rows, then raw reads), `cnql_diff_test.go` (corpus).

## Module notes: finance CRM (port/finance-crm)

`contact.create`, `contact.get`, `contact.list`, `contact.update`, `deal.create`, `deal.list`,
`deal.update`, and `deal.move_stage` are registered by `internal/modules/crm`.
`notes.InsertTypedNote` is a small exported wrapper around the existing insertion helper; typed notes
retain its access checks, ownership, trimming, and `note.created` event plus dispatch job.
CRM validation, coalesce/null update semantics, list filtering, stage probabilities and closed timestamps
match `cnote-finance/src/crm.rs`. A repeated stage move emits nothing; stage moves do not bump the
note version. Creating a won deal emits only `deal.created` after `note.created`; moving to won emits
`deal.stage_changed` and `deal.won`, each with its own `event.dispatch` job and original actor.
No worker handlers, sweeps, or provider calls were moved. Other finance commands remain in Rust.

Tests port `crm.rs` stage/tax assertions and `finance.rs` CRM pipeline and tenant assertions.
`TestDifferentialCRMWrites` compares twin-fixture normalized responses and full CRM/note rows,
command events and worker-visible dispatch jobs, then compares read responses as raw bytes.
Generated UUIDs and timestamps are normalized only across independent twin writes. Coverage includes
owner/member/guest, foreign workspace, API/agent keys, no authentication, malformed inputs, Unicode,
large strings and integers, empty values, invalid tax IDs and every pipeline stage. Additional integration
checks cover viewer/private/restricted access, scope and workspace tool-group gates, and idempotent
replay with exact event/job counts. Decoder failures and non-ASCII GSTIN uppercase cases delegate to
Rust to preserve serde errors and full Unicode uppercase behavior; no entire CRM command is deferred.

Validation uses the worktree `.env`, real Postgres, the shared Rust reference on :4000, and in-process
Go HTTP servers. No shared server is restarted. Browser e2e is not run because this brief prohibits
starting/restarting the ports required by the documented browser workflow.

Final checks (with `.env` loaded): `cd apps/api && go vet ./...`,
`test -z "$(gofmt -l .)"`, and `go test -race -count=1 ./...` all passed.
The final suite contains 57 tests across five tested packages (zero failures), including five CRM tests.
Focused differential validation compared 443 twin responses/database rows and 90 raw read pairs
with zero differences. Earlier exploratory runs without `.env` failed on the required database check;
an added gate test initially assumed default agents could not manage CRM, and was corrected to
explicitly disable that workspace tool group, matching both reference and Go behavior.

## Marketplace port (port/marketplace)

`internal/modules/marketplace` registers all twelve marketplace commands: `market.list`,
`market.get`, `market.install`, `market.uninstall`, `market.installed`, `dev.apps`,
`dev.app_save`, `dev.app_submit`, `dev.app_rotate_secret`, `dev.app_delete`,
`market.review_queue`, and `market.review`. Admin mutations require a human principal.
Platform review authorization reads `CNOTE_PLATFORM_ADMINS` on every call, accepting trimmed,
case-insensitive email and `@domain` entries, including changes made in-process by tests.

Installation creates the Rust-compatible external agent, scoped install row and ten-minute
one-time code. Codes and client secrets use lowercase SHA-256 hex; random bytes use unpadded
base64url with the Rust prefixes and lengths. `/api/marketplace/token` remains proxied to Rust,
which exchanges these codes for scoped API keys. Tests exercise that exchange, failed credentials,
code replay, authenticating the resulting key in Go, scope restrictions and uninstall revocation.
Both `dev.app_save` and `dev.app_rotate_secret` set `ReturnsSecret`, so secret responses never enter
idempotency storage. The module has private random-token and hashing helpers matching Rust auth.
Marketplace transactions set the local timezone to UTC: Postgres `jsonb_build_object` and
`to_jsonb` render timestamps using the session timezone, while the Rust sqlx sessions use UTC.

`internal/server/marketplace_test.go` ports the assertions in Rust's marketplace integration tests
and adds env changes, secret rotation/hashing/idempotency, positive same-fixture read comparisons,
malformed input and caller matrices for all twelve commands, plus twin-fixture writes. The latter
compare editable/status rows, install/agent/code properties, events, actors, payload relationships
and dispatch jobs consumed by the Rust worker. Deterministic responses are compared as raw bytes;
independently generated UUIDs, codes, secrets and timestamps are checked or normalized for twins.
No requested command is left to Rust; invalid serde inputs use the existing fallback contract.
The token HTTP endpoint and background workers remain in Rust by design. No AI providers are used.

Validation (load the worktree `.env` first): `cd apps/api && go vet ./... &&
test -z "$(gofmt -l .)" && go test -race -count=1 ./...`. All pass against real Postgres and the
running Rust reference. No shared servers were restarted. Browser e2e was not run because the
brief forbids restarting the shared ports; HTTP tests run Go in-process with `httptest`.
Final race-suite result: 55 top-level tests / 357 including subtests passed, zero failures;
4 packages passed and 9 had no tests. Marketplace's differential caller/input matrix compared
240 response pairs, in addition to positive lifecycle reads and twin-fixture mutations.

## Go-only commands: quotations

`quote.create_draft`, `quote.update_draft`, `quote.get`, `quote.list`, `quote.send`, `quote.accept`,
`quote.reject`, `quote.convert_to_invoice` and `quote.convert_to_project` (`internal/modules/quotes`, migration
`0950_quotations.sql`) exist only in Go. Rust has no handler for them, so there is nothing to fall back to: bad
input returns an `invalid` error from Go rather than `ErrFallback`. Agents reach them through
`/mcp` `discover` and `invoke` (see "OAuth 2.1 authorization server and the MCP endpoint" below). Quotations never
post to the ledger; their numbers (`QT/<FY>/<n>`) come from their own `quote:<FY>` series in `billing.sequences`.

## Go-only command: contact.lifecycle

`contact.lifecycle {id}` (`internal/modules/crm/lifecycle.go`, group `read`) returns one contact's `deals`, `quotes`,
`invoices`, `projects` (items created from its visible quotes), `payments` and `outstanding_minor`. It composes
`deal.list`, `quote.list`, `invoice.list` and `item.get` through `c.Call`, so each section obeys the same gate and
note access as that command; payments are limited to the listed invoices. A section the caller may not read is null.

## Go-only commands: vendors and purchase bills

`bill.create_draft`, `bill.update_draft`, `bill.get`, `bill.list`, `bill.record`, `bill.pay`, `bill.cancel`,
`vendor.list`, `report.payables_ageing` and `report.itc_summary` (`internal/modules/purchases`, migration
`0970_purchase_bills.sql`) exist only in Go; like `quote.*` they are not in Rust's MCP tool list. A vendor is a CRM
contact with `is_vendor` (set through `contact.create` / `contact.update`, filter with `contact.list {vendor}`; the
contact JSON does not expose the flag, use `vendor.list`). Recording posts Dr expense (line `account_code`, default
5000) + Dr input CGST/SGST/IGST (1301/1302/1303) / Cr accounts payable (2100); paying posts Dr 2100 / Cr 1200;
cancelling a recorded unpaid bill posts the exact reverse. The five purchase accounts are seeded per workspace on the
first recorded bill (Rust seeds the original seven), so trial balances of workspaces without bills are unchanged.
`bill.pay` and `bill.cancel` need human confirmation from machine callers.

## Go-only commands: recurring invoices

`recurring.create|update|get|list|pause|resume|end|run_now` (`internal/modules/recurring`, migration
`0960_recurring_invoices.sql`) are Go-only and absent from the Rust `/mcp` list. A schedule is a `recurring_invoice`
note with `billing.recurring_invoices` (frequency monthly|quarterly|yearly, interval, start/next/end dates,
`auto_issue`, status, `last_invoice_id`) and `billing.recurring_invoice_lines`; both have tenant RLS. Each period calls
`invoice.create_draft` with `invoice_date` = the period date, then `invoice.issue` when `auto_issue`. The anchor day is
kept, so a 31st schedule runs on the last day of shorter months and Feb 29 returns in leap years.

The sweep (`RECURRING_SWEEP_SECS`, default 60, `0` disables) lists workspaces via `core.all_workspace_ids()` and handles
each due schedule in its own `db.BeginTenant` transaction using `for update skip locked`, so several API instances
never double-generate. It catches up at most 12 periods per run and rolls everything back if one period fails (the
schedule is retried next sweep). Runs act as `KindSystem` on behalf of the creator, who must still be a member; system
principals may issue invoices, so scheduled auto-issue posts directly. A machine caller (agent or API key) of
`run_now` needs confirmation, and if `auto_issue` is set the nested `invoice.issue` needs its own: the draft is kept,
the period is consumed and `recurring.issue_confirmation_required` carries the invoice and confirmation ids.
`run_now` only consumes a due period. Cadence cannot change once an invoice was generated.

## QA coverage (CN-QA-001)

Go integration tests against real Postgres as the app role (`apps/api/internal/server`, `qa_*_test.go`; Rust on
:4000 is the reference where a command is ported):

- `qa_rls_test.go`: DB-001, DB-002, DB-012. Discovers every table with a `workspace_id` column (all schemas) and
  checks RLS, empty view without a tenant GUC, no cross-tenant read/insert/update/delete, and that pooled
  connections never keep a tenant (pool of 1 and a concurrent pool of 2). Only `core.oauth_codes`,
  `core.oauth_grants` and `core.app_install_codes` are exempt (read before a tenant is known; allowlist in the test).
- `qa_integrity_test.go`: DB-003, DB-004, DB-005, section 5.2 invariants. Commands refuse another tenant's ids;
  direct SQL as the app role is refused by the composite FKs of `migrations/0980_qa_constraints.sql`; a catalog
  check fails any new tenant-to-tenant FK that lacks `workspace_id`.
- `qa_revisions_test.go`: RV-001, RV-006, RV-008, DB-007, DB-008, DB-011 (immutability, unique numbers, concurrent
  edits/restores, restore authorisation).
- `qa_authz_test.go`: section 8 matrix generated from the Go registry (127 commands x guest, member, admin, owner,
  two API keys, agent key) and compared with Rust; SEC-007 guest share revocation; SEC-014 agent escalation.
- `qa_api_test.go`: API-002, API-007, API-009, API-012, API-013.

Findings fixed in this pass:

- **Cross-workspace references.** Every FK was single-column, and Postgres checks FKs as the table owner, so RLS did
  not stop a tenant pointing `parent_id`, a relation, a comment, a deal contact or an invoice line at another
  tenant's id. `view.create` with a foreign `space_id` returned 200 in Go and Rust. Migration 0980 turns the 58
  tenant-to-tenant FKs into `(workspace_id, col)` FKs (same names and delete actions; SET NULL uses the column
  list form). A foreign id now fails as `invalid` (23503), the same answer as a random id. Rust gets the same
  protection from the schema (its `view.create` now answers with its database error instead of storing the row).
  Not covered yet: `billing.recurring_*` and `billing.purchase_*` (0960/0970), skipped explicitly in the catalog test.
  The migration fails loudly if existing data already crosses workspaces.
- **Oversized body (API-009).** Rust answers 413 `Failed to buffer the request body: length limit exceeded` before
  auth and drops the connection; the Go proxy replay turned that into 502. Go now sends the same 413 itself for
  JSON bodies over 2 MiB on any `/api/w/{ws}/commands/{name}` path.
- **Request id (API-013).** Go adds `X-Request-Id` (echoes a sane client value, else random) to every response and
  logs it on internal errors. Rust sends none; the header is additive and bodies stay byte-identical. The
  unreachable non-command error path now answers `internal error` instead of the raw cause (Rust echoes the cause).

Open, not fixed (see test skips and the QA report): API-007 reusing an idempotency key with a different payload
returns the first result on both servers (needs a payload hash in `core.idempotency` and a Rust change);
`core.events` is append-only, so hard-deleting a note that has events fails (notes are soft-deleted in practice);
SEC-014 is closed by WP7 (`command.denied` events for agent/API-key gate refusals, Go-registry commands only; Rust-handled commands still do not log denials); the three OAuth/install tables are readable by any tenant session.

## OAuth 2.1 authorization server and the MCP endpoint (Wave 3)

Both now run in Go (`internal/oauth`, `internal/mcp`, `internal/modules/oauthapps`). Same tables, same SHA-256 hashing of
codes and tokens, same token shapes (`cnx_` code, `cno_` access, `cnr_` refresh, `cnc_` registered client), so a token
issued by either server is valid on both (`TestOAuthTokensAreValidOnBothServers`). No new env vars: it reads
`PUBLIC_BASE_URL`, `PUBLIC_WEB_URL`, `TRUST_PROXY`, `OAUTH_CIMD_ALLOW_PRIVATE` like Rust.

**OAuth** (`internal/oauth`): `GET /.well-known/oauth-protected-resource[/mcp]`, `GET /.well-known/oauth-authorization-server`,
`POST /oauth/register`, `/oauth/token` (code + PKCE S256, refresh rotation with reuse detection, resource binding),
`/oauth/revoke`, and the cookie-session consent endpoints `POST /api/oauth/authorize[/context]`; client ID metadata
documents are fetched with the SSRF guard of `netguard.rs` (public addresses only, connection pinned to the checked
addresses, no redirects, 5 s, 16 KiB). Rate limits are the same buckets as Rust (`command.CheckRateWith`), in-process.
`oauth_app.list|revoke` (connected apps) are Go commands. Cases Go will not decide itself are proxied to Rust before any
side effect: bodies serde would reject (its error text is the contract), `OPTIONS`/`HEAD`/wrong verbs, and redirect URIs
the `url` crate might parse differently from Go (anything but plain lowercase `scheme://host[:port]/path?query` URIs
with no dot segments and an already-normalized form; see `oauth/uri.go`).

**MCP** (`internal/mcp`, `POST /mcp`): authentication and the 401 challenge, `initialize`, `ping`, `tools/list`,
`tools/call` (`discover`, `invoke`, core tools), `resources/list|read`, `resources/templates/list`, `prompts/list`,
`notifications/initialized` are answered natively, byte-identical to rmcp 3.9 for the legacy lifecycle. The command
catalogue Go needs for that (names, descriptions, groups, schemas of the commands Rust owns) is the embedded snapshot
`internal/mcp/catalog.json` of `GET /api/commands`; Go-only commands add theirs from `modules/mcpmeta.go`.
`TestMCPCatalogMatchesRust` fails when Rust's registry moved on: regenerate with
`curl -s localhost:4000/api/commands > apps/api/internal/mcp/catalog.json`. Executing a command that Go has ported runs
through `Registry.Execute` (the REST gate: role, scopes, tool groups, rate limit, idempotency, confirmation, usage). What
Go proxies to Rust unchanged: execution of commands only Rust has (AI, importers, ...), anything declined with
`ErrFallback`, the 2026-07-28 request-metadata lifecycle (`_meta`, `Mcp-*` headers, elicitation round trips,
`server/discover`), unknown `MCP-Protocol-Version`, batches, JSON-RPC responses, other methods, invalid JSON, `GET`/`DELETE`.
Discover is the one deliberate difference from Rust: it lists Go-only commands too.

Reference for differential tests: Rust throttles registration/token/consent per client address and :4000 is shared, so
for a complete run start a private reference and point the tests at it:
`TRUST_PROXY=1 OAUTH_CIMD_ALLOW_PRIVATE=1 cnote serve --port 4520` then `RUST_OAUTH_URL=http://localhost:4520 go test ./...`.
Without it the throttled cases are skipped and `TestOAuthDifferentialClientMetadataDocuments` is skipped.

## Go-only commands and public routes: share links (WP2)
`share.create|list|revoke|email` and `invoice.upi_qr` (migration `1010_public_links.sql`) are Go-only and absent from
the Rust `/mcp` list. `share.create` and `share.email` are `send_messages` + confirmation for agents and API keys;
`share.create` returns a secret (the token), so it is never stored in idempotency, events or confirmations.
The server also routes `/api/public/{token}` (GET the document) and `/api/public/{token}/quote` (POST accept or
decline) natively, with no session: tokens are 32 random bytes, only their sha256 is stored, the unauthenticated
lookup is the narrow `core.resolve_public_link(hash)` SECURITY DEFINER function, and everything after it runs in
the link's tenant under RLS. Customer decisions run `quote.accept` / `quote.reject` as a System principal on
behalf of the link creator (their current role and page access; a removed or demoted creator kills their links).
Rate limits (in-process): `PUBLIC_IP_BURST/PER_SEC`, `PUBLIC_FAIL_BURST/PER_SEC` (misses per IP),
`PUBLIC_TOKEN_BURST/PER_SEC`; `PUBLIC_TRUST_PROXY=1` takes the client IP from the right-most `X-Forwarded-For`.
Email uses `SMTP_URL` + `EMAIL_FROM`; the link origin comes from the request's `origin` or `PUBLIC_WEB_URL`.

## Audit log and data protection (WP7, migration 1050)

Go-only commands in `modules/audit`: `audit.list`, `audit.export` (CSV, Confirm), `audit.retention` (report only),
`data.export_subject` (Confirm), `data.request_list`, `data.erase_subject` (Owner, people only). `core.events` stays the
audit trail; `core.data_requests` logs exports and erasures (ids and counts, never the data). `Registry.Execute` writes
`command.denied` in a second transaction for machine callers refused by the gate (one per actor+command per minute,
20 per actor per minute). Erase anonymises PII; a contact with any issued invoice, recorded bill or receipt keeps name,
GSTIN, state and address (printed on those documents). Ledger, `core.note_revisions` and `core.events` are never edited.
Retention defaults (8 years) are data in `settings.audit_retention_years`; statutory periods are to be verified with counsel.

## Go-only commands: advance receipts (WP4)
`receipt.create|apply|refund|get|list` (migration `1030_receipts.sql`, module `modules/receipts`) are Go-only and absent
from the Rust `/mcp` list. A receipt or refund voucher is a typed note `['receipt_voucher']` plus a `billing.receipts` row,
immutable except its running totals. Postings (CA review): receive Dr bank / Cr 2300 customer advances, services also
Dr 1310 GST paid on advances / Cr output GST; apply Dr 2300 / Cr receivables and Dr output GST / Cr 1310 for the applied
share; refund reverses the unapplied part. Accounts are seeded lazily and can be changed in workspace settings
(`advance_account`, `advance_gst_account`). `report.gstr1` adds `advances_11a` / `advances_adjusted_11b` only in months
that have advances. `receipt.refund` can be held by an approval rule. Receipts can be shared with `share.create`.
