# Service boundaries: a monolith that can be split

C Note runs as one Go binary today. Any module (email, notifications, billing, CRM, agents…) must be movable into
its own service later without rewriting callers. Rules:

1. **A module owns its tables and commands.** Other modules do not read or write its tables.
2. **Modules talk through the command registry or events.** Call another module with `c.Call("module.command", input)`
   or react to its events (`c.Emit` / `jobs.Event`). Both work the same whether the module is in-process or remote:
   the registry can route a command prefix to an HTTP service, as the old Go/Rust strangler proxy did.
3. **No new module-to-module Go imports.** `internal/modules/boundaries_test.go` lists today's imports and fails on new
   ones. Remove entries as couplings are paid down; adding one needs a note here explaining why.
4. **Side-effect services sit behind one seam each:**
   | Service | Seam | Local implementation | Later |
   |---|---|---|---|
   | Email | `internal/mailer` (`mailer.Send`) | SMTP from the API process | HTTP or queue client to a mail service |
   | Notifications | `internal/notify` (`notify.Send`, `notify.Default`) | insert into `core.notifications` in the caller's tx | notification service (in-app, push, WhatsApp) |
   | AI models | `internal/modules/ai` (`ai.Open`, `Session.Step`, `ai.Complete`) | raw HTTP to Anthropic / OpenAI-compatible | model gateway |
   | Background work | `core.jobs` + `internal/worker` | worker inside the API (`WITH_WORKER=1`) or `cmd/worker` | separate worker deployment (already possible) |
   Only the seam package may talk to the underlying system (SMTP, `core.notifications`, provider HTTP).
5. **Shared kernel stays small:** `command`, `access`, `db`, `jobs`, `auth`, the seams above. Shared tables in `core`
   (workspaces, users, memberships, notes, events, confirmations) are the platform's, not a module's.

To extract a service: (1) make sure every caller goes through its seam or commands, (2) move the package into its own
binary that serves the same commands/seam over HTTP, (3) swap the seam's default implementation or add a registry route.
