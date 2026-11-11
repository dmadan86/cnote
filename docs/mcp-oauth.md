# MCP OAuth 2.1

Implements the MCP authorization spec (revision 2025-11-25) for the Streamable HTTP endpoint `/mcp` (ADR-004).
API keys (`cn_...`) keep working; OAuth is the path for interactive MCP clients (Claude, Cursor, Inspector).

## Flow

1. Client calls `/mcp` without a token, gets `401` with
   `WWW-Authenticate: Bearer resource_metadata="<base>/.well-known/oauth-protected-resource/mcp"`.
2. It reads the protected-resource metadata (RFC 9728, also served at `/.well-known/oauth-protected-resource`), then the
   authorization-server metadata at `/.well-known/oauth-authorization-server` (RFC 8414).
3. Registration, either way:
   - **Client ID Metadata Document**: `client_id` is an `https://` URL with a path serving the client's JSON
     (`client_id` equal to the URL, `redirect_uris`, optional `client_name`). Fetched through an SSRF guard
     (public addresses only, connection pinned to the checked IPs, no redirects, 5 s, 16 KB), cached for one hour.
   - **Dynamic Client Registration** (RFC 7591) at `POST /oauth/register`. Public clients only
     (`token_endpoint_auth_method: none`); returns a `cnc_...` client id.
4. Browser goes to `authorization_endpoint` (the web app page `/oauth/authorize`) with `response_type=code`,
   `client_id`, `redirect_uri`, `code_challenge` (S256), `resource`, optional `state`. The signed-in user sees the client
   name and the redirect host, picks a workspace and approves or denies. The redirect carries `code`, `state` and
   `iss` (RFC 9207).
5. `POST /oauth/token` (form-encoded): `authorization_code` + `code_verifier`, or `refresh_token`.
6. `POST /oauth/revoke` (RFC 7009).

## Security rules enforced

| Rule | Where |
| --- | --- |
| PKCE S256 only; `plain`/missing is refused at authorize, verifier length and charset checked at token | `validate_authz`, `exchange_code` |
| `resource` required; only `<base>/mcp` is ever issued; re-checked at token and refresh (`invalid_target`) and on every use (the grant's stored resource must equal the endpoint's). The same token is also accepted on `/api/w/{ws}/commands` as part of the same protected resource | `canonical_resource`, `principal_from_oauth_token` |
| `redirect_uri` exact string match; never redirected to before it is verified; http only for loopback; no fragment/credentials; `javascript:`, `data:` etc. refused | `validate_redirect_uri` |
| Codes: 256-bit, 5 minutes, single use; replay revokes the grant the code produced | `exchange_code` |
| Refresh tokens rotate; presenting a used one revokes the whole grant (reuse detection) | `refresh` |
| Only SHA-256 hashes stored (codes, tokens) | migration `0600_oauth.sql` |
| Approval needs the cookie session, JSON body, workspace membership re-checked; cross-site fetches refused; consent page sent with `frame-ancestors 'none'` | `authorize`, `next.config.ts` |
| Rate limits keyed by client address (registration, token, revoke) and per user (consent context); the per-client token bucket is charged only after a real code/refresh token is found. Buckets are length-capped and pruned | `ratelimit::check_with` |
| Removing a member revokes their grants (DB trigger, so re-adding cannot resurrect them); refresh also requires current membership | migration `0601` |
| Metadata fetch refuses private, NAT64, 6to4, Teredo, multicast, reserved ranges, ignores proxy env vars, pins checked IPs | `netguard.rs` |
| Token endpoints answer `Cache-Control: no-store` | `no_store` |

## What a token is

`cno_...` access token (1 hour) and `cnr_...` refresh token (30 days, rotating). An access token resolves, on every
request, to an **agent principal** (`auth::principal_from_oauth_token`) on the workspace the user approved, with the
user's *current* role capped at admin, never owner (same rule as API keys). Revoking the grant, removing the user
from the workspace, or token expiry make it stop working immediately. It is accepted on `/mcp` and on
`/api/w/{ws}/commands/{name}` (only for its own workspace). Agents cannot create API keys or manage connected apps.
Money-moving and outbound commands still go through the human confirmation flow.

## Connected apps

Settings, "Connected apps" lists the caller's grants in the workspace and offers Disconnect. Commands:
`oauth_app.list`, `oauth_app.revoke` (people only; an OAuth/API-key principal gets 403).

## Configuration

| Env | Meaning |
| --- | --- |
| `PUBLIC_BASE_URL` | Public origin of the API/MCP (issuer, resource, challenge URLs). **Required in production.** Without it, OAuth and its metadata answer 503 unless the request Host is loopback (`localhost`, `127.0.0.1`, `::1`), so a spoofed Host cannot mint URLs. |
| `TRUST_PROXY` | `1` makes rate limits use the left-most `X-Forwarded-For` entry; otherwise the socket peer address. Set only behind a proxy that sets the header. |
| `PUBLIC_WEB_URL` | Origin serving `/oauth/authorize` when it differs from the API origin. |
| `OAUTH_CIMD_ALLOW_PRIVATE` | Tests/dev only: lets the metadata-document fetch use http and private addresses. |

The Next.js app proxies `/.well-known/*`, `/oauth/{token,register,revoke}` and `/mcp` to the API, so a single public
origin can serve everything (`PUBLIC_BASE_URL` = the web origin, no `PUBLIC_WEB_URL` needed).

## Known limits

- `scope` is accepted and stored but not enforced; the tool-group policy of the workspace (Settings, Tool groups)
  is the capability control for OAuth agents as for API keys.
- Redirect URIs match exactly, including the port, so loopback clients register the URI they will listen on.
- Rate-limit buckets are in-process (same limitation as the fair-use limiter).
- Expired codes and tokens are purged opportunistically on code exchange; abandoned dynamic registrations after 30 days.
