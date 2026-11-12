# Webhooks, schedules and MCP hooks

Admins create hooks in **Settings → Webhooks**, or from any MCP client/API key with the `admin` tool group via
`hook.create` / `hook.list` / `hook.set_enabled` / `hook.delete` / `hook.test` (C Note's MCP server: `discover` then `invoke`).

A hook fires **on events** (kind prefixes such as `item.created`, `note.`, or `*`), **on a schedule**
(`every_minutes` ≥ 5, first run at `start_at`), or both. Each firing delivers one JSON body:

```json
{"type": "event", "hook_id": "…", "workspace_id": "…", "event": {"id": 1, "kind": "note.created", "note_id": "…", "payload": {}, "created_at": "…"}}
{"type": "schedule", "hook_id": "…", "workspace_id": "…", "name": "Daily digest", "scheduled_for": "…"}
{"type": "test", "hook_id": "…", "workspace_id": "…", "sent_at": "…"}
```

## Targets

- **Webhook (`http`)**: `POST` with `content-type: application/json`, `x-cnote-timestamp` (unix seconds) and
  `x-cnote-signature: sha256=<hex HMAC-SHA256(signing_secret, "<timestamp>.<raw body>")>`. The `whsec_` signing secret
  is shown once at creation. Verify the signature and reject timestamps older than a few minutes. Any 2xx is success.
- **MCP server (`mcp`)**: C Note connects to the streamable-HTTP MCP endpoint at `url` (optional bearer token) and
  calls `tool` with your static `arguments` plus `cnote` = the body above. A tool result with `isError` counts as failure.

## Delivery

At least once. Failures retry 4 more times after 2, 4, 8 and 16 minutes; the hook shows the last status and error,
and the server logs `hook delivery failed` with the hook, workspace and error. A schedule that was missed (worker down)
fires once and then continues on its cadence. URLs must be public HTTPS (`AI_ALLOW_PRIVATE_ENDPOINTS=1` lifts this for
self-hosted installs). Secrets and tokens are encrypted with `CNOTE_MASTER_KEY`, which every server and worker must share.
