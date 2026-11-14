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

### Fallback: how byte-compatibility stays cheap

A handler may return `command.ErrFallback`; the router then replays the buffered request to Rust. Used for:
inputs serde would reject (missing or mistyped `id`, odd UUID spellings, wrong body shape, invalid JSON),
OAuth access tokens (`cno_`), unparsable `X-Confirmation-Id`, bodies over 2 MiB. Rust produces its own error
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
10. **Last**: MCP (`/mcp`), OAuth endpoints, collab WebSocket and revisions (Yjs). These stay proxied as long
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
- OAuth access tokens and invalid inputs are served by Rust (fallback), not natively.
- `ctx.call` (nested commands) and event/job handlers are not ported; the Rust worker still consumes
  `core.events` and `core.jobs` written by Go.
- The Go server needs `DATABASE_URL`; `RUST_API_URL` must be reachable or non-native traffic returns 502.
- pgx v5.8.0 is the newest release that builds with Go 1.24; later pgx releases require Go 1.25.

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
