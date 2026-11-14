// Package mcp serves the Model Context Protocol endpoint (POST /mcp): the
// stateless streamable-HTTP transport in JSON mode, bearer authentication (cn_ API keys, cno_ OAuth tokens), the tool
// catalogue (core commands plus discover and invoke) and note/view resources, all on top of the command registry.
//
// It serves the 2024-11-05 to 2025-11-25 lifecycles and offers only those versions in initialize. The 2026-07-28
// request-metadata lifecycle (per-request _meta, elicitation round trips) is refused with a clear 400, and anything
// else the JSON-RPC shape does not fit gets the protocol's own error. See docs/go-migration.md.
package mcp
