// Package mcp serves the Model Context Protocol endpoint (POST /mcp): the
// stateless streamable-HTTP transport in JSON mode, bearer authentication (cn_ API keys, cno_ OAuth tokens), the tool
// catalogue (core commands plus discover and invoke) and note/view resources, all on top of the command registry.
//
// It serves the 2024-11-05 to 2025-11-25 lifecycles (initialize offers only those) and the 2026-07-28
// per-request lifecycle (lifecycle.go). Anything else the JSON-RPC shape does not fit gets the protocol's own error.
// See docs/go-migration.md.
package mcp
