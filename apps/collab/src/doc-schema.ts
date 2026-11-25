// The editor schema number (docs-parity 7.3). Raise it whenever the editor gains a node or mark, together with
// COLLAB_MIN_SCHEMA on the gateway. apps/web imports this file by relative path; keep it free of imports.
export const DOC_SCHEMA = 2
/** WebSocket close code: this tab's editor is older than the gateway's minimum schema; reload to keep editing. */
export const CLOSE_RELOAD = 4410
