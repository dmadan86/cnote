# Building an app for the C Note marketplace

An installed app is an external agent in the customer's workspace. It calls the normal command API with a `cn_` key
that only works for the tool groups (scopes) the app declared, and only while the app stays installed.

## 1. Publish

In the web app: Marketplace, Build apps, New app (or call `dev.app_save`). You provide:

| Field | Rule |
| --- | --- |
| `slug` | `a-z0-9-`, 3-40 chars, unique |
| `name`, `tagline`, `description` | max 60 / 120 / 4000 characters |
| `category` | productivity, sales, finance, developer, communication, other |
| `icon_url`, `homepage_url` | optional, https only |
| `redirect_url` | https (http only for `localhost` / `127.0.0.1`, for testing) |
| `scopes` | must include `read`; others from the list below |

Creating the app returns `client_secret` (`cnas_...`) once. Store it; `dev.app_rotate_secret` issues a new one.
Then `dev.app_submit` sends it to review. A platform admin (email listed in the server's `CNOTE_PLATFORM_ADMINS`,
comma-separated; `@example.com` matches a domain) approves or rejects it with `market.review`. Editing a published
app puts it back in review. While in draft you can install your own app in your own workspace to test.

## 2. Install flow

1. A workspace admin clicks Install (`market.install`). C Note creates the agent and redirects the browser to
   `<redirect_url>?code=cnic_...&workspace_id=<workspace id>` (`&` if your URL already has a query).
2. Your server exchanges the code within 10 minutes. A code works once.

## 3. Token exchange

```sh
curl -X POST https://<c-note-host>/api/marketplace/token \
  -H 'Content-Type: application/json' \
  -d '{"client_id": "<app id>", "client_secret": "<client_secret>", "code": "<code>"}'
```

```json
{"access_token": "cn_...", "token_type": "bearer", "workspace_id": "<workspace id>", "scopes": ["read", "write_items"]}
```

Any failure (wrong secret, used or expired code, unknown app) returns `400 {"error": "invalid_grant"}`.
`client_id` is the app id returned by `dev.app_save`.

## 4. Calling commands

```sh
curl -X POST https://<c-note-host>/api/w/<workspace id>/commands/note.list \
  -H 'Authorization: Bearer <access_token>' -H 'Content-Type: application/json' -d '{}'
```

`GET /api/commands` lists every command and its tool group. A command outside your scopes returns 403. The workspace
must also have the tool group enabled for agents (Settings, Tool groups); `market.get` reports this as
`blocked_scopes`, and the install dialog warns the admin. Money-moving and outbound commands (`manage_invoices`,
`send_messages`) still need a human confirmation. Your role is capped at the installing admin's role, never owner.

## 5. Uninstall

Uninstalling (`market.uninstall`, admin only) deletes the app's agent, and every key it holds stops working at once.
Installing again issues a fresh code. You cannot delete an app that still has installs.

## Scopes

| Scope | Allows |
| --- | --- |
| `read` | read pages, items, contacts, invoices (required) |
| `write_notes` | create and edit pages |
| `write_items` | create and edit work items |
| `manage_crm` | contacts, companies, deals |
| `manage_invoices` | invoices and payments (human-confirmed) |
| `send_messages` | outbound email and WhatsApp (human-confirmed) |

`admin` and `destructive` can never be granted to an app. Apps also cannot install apps, publish apps or manage members.
