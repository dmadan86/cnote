// Package mcp serves the Model Context Protocol endpoint (POST /mcp) that crates/cnote-mcp serves with rmcp: the
// stateless streamable-HTTP transport in JSON mode, bearer authentication (cn_ API keys, cno_ OAuth tokens), the tool
// catalogue (core commands plus discover and invoke) and note/view resources, all on top of the command registry.
//
// Go answers every request whose JSON-RPC shape it fully understands (legacy lifecycle: no per-request _meta, no
// 2026-07-28 headers). The rest, and the execution of commands that only Rust has, is proxied to Rust, which stays the
// reference for protocol corner cases. See docs/go-migration.md.
package mcp
