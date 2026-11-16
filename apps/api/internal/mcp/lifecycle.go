package mcp

// The 2026-07-28 lifecycle, as rmcp 3.9 serves it in stateless mode: no initialize; every request carries its
// version and client capabilities in _meta; SEP-2243 Mcp-Method/Mcp-Name headers must match the body;
// server/discover answers what initialize used to; results say resultType, cacheable ones carry ttlMs/cacheScope;
// a confirmation becomes an input_required result (servers may not send requests to the client).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
)

const (
	modernVersion   = "2026-07-28"
	metaVersionKey  = "io.modelcontextprotocol/protocolVersion"
	metaCapsKey     = "io.modelcontextprotocol/clientCapabilities"
	codeHeader      = -32020
	codeUnsupported = -32022
)

var knownVersions = append(slices.Clone(legacyVersions), modernVersion)

// parseMeta fills the lifecycle fields from params._meta (an object, checked by the caller).
func (m *message) parseMeta(raw json.RawMessage) {
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil {
		return
	}
	if v, ok := stringOf(meta[metaVersionKey]); ok {
		m.metaVersion = v
	}
	m.metaCaps = isObjectRaw(meta[metaCapsKey])
	if m.metaCaps {
		var caps map[string]json.RawMessage
		_ = json.Unmarshal(meta[metaCapsKey], &caps)
		_, m.canElicit = caps["elicitation"]
	}
}

// modern: the request negotiated 2026-07-28 or later in its own _meta.
func (m message) modern() bool { return m.metaVersion >= modernVersion }

func missingMeta(m message) string {
	var miss []string
	if m.metaVersion == "" {
		miss = append(miss, metaVersionKey)
	}
	if !m.metaCaps {
		miss = append(miss, metaCapsKey)
	}
	return strings.Join(miss, ", ")
}

// lifecycleRefusal applies the transport checks rmcp runs before dispatch, in its order: standard headers, then the
// header against _meta. It writes the refusal and returns true when the request must not go on.
func lifecycleRefusal(w http.ResponseWriter, r *http.Request, m message) bool {
	header := ""
	if vs := r.Header.Values("MCP-Protocol-Version"); len(vs) > 0 {
		header = vs[0]
	}
	if header >= modernVersion {
		if reason := standardHeaders(r.Header, m); reason != "" {
			jsonRPCStatus400(w, m.id, codeHeader, reason)
			return true
		}
	}
	if m.notification || m.method == "initialize" {
		return false
	}
	if m.metaVersion == "" {
		if m.method == "server/discover" || header >= modernVersion {
			jsonRPCStatus400(w, m.id, codeInvalidParams, "Invalid params: request _meta is missing or has malformed required fields: "+missingMeta(m))
			return true
		}
		return false
	}
	if header == "" {
		jsonRPCStatus400(w, m.id, codeHeader, "request _meta protocolVersion requires MCP-Protocol-Version header")
		return true
	}
	if header != m.metaVersion {
		jsonRPCStatus400(w, m.id, codeHeader, "MCP-Protocol-Version header ("+header+") does not match request _meta protocolVersion ("+m.metaVersion+")")
		return true
	}
	return false
}

// standardHeaders is SEP-2243: Mcp-Method must name the body's method (optional only on initialize), and Mcp-Name
// its tool name or resource uri, Base64-wrapped as =?base64?...?= when needed.
func standardHeaders(h http.Header, m message) string {
	methods := h.Values("Mcp-Method")
	if m.method == "initialize" {
		switch {
		case len(methods) == 0:
			return ""
		case len(methods) > 1:
			return "multiple Mcp-Method header values supplied; initialize requires exactly one"
		case methods[0] != "initialize":
			return "Mcp-Method header `" + methods[0] + "` does not match body method `initialize`"
		}
		return ""
	}
	switch {
	case len(methods) > 1:
		return "duplicate Mcp-Method header"
	case len(methods) == 0:
		return "missing required Mcp-Method header"
	case methods[0] != m.method:
		return "Mcp-Method header `" + methods[0] + "` does not match body method `" + m.method + "`"
	}
	key := map[string]string{"tools/call": "name", "prompts/get": "name", "resources/read": "uri"}[m.method]
	expected, ok := stringOf(m.params[key])
	if key == "" || !ok {
		return ""
	}
	names := h.Values("Mcp-Name")
	switch {
	case len(names) > 1:
		return "duplicate Mcp-Name header"
	case len(names) == 0:
		return "missing required Mcp-Name header for `" + m.method + "`"
	}
	got, valid := decodeHeader(names[0])
	if !valid {
		return "Mcp-Name header is not valid Base64"
	}
	if got != expected {
		return "Mcp-Name header `" + got + "` does not match body value `" + expected + "`"
	}
	return ""
}

func decodeHeader(v string) (string, bool) {
	inner, wrapped := strings.CutPrefix(v, "=?base64?")
	if !wrapped || !strings.HasSuffix(inner, "?=") {
		return v, true
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(inner, "?="))
	if err != nil || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

// handlerRefusal is what rmcp's handler refuses before running a method on the per-request lifecycle.
func handlerRefusal(w http.ResponseWriter, m message) bool {
	if m.metaVersion == "" || m.method == "initialize" {
		return false
	}
	if !slices.Contains(knownVersions, m.metaVersion) {
		b, _ := command.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id, "error": map[string]any{"code": codeUnsupported,
			"message": "Unsupported protocol version", "data": map[string]any{"requested": m.metaVersion, "supported": knownVersions}}})
		write(w, http.StatusOK, "application/json", b)
		return true
	}
	if m.modern() && !m.metaCaps {
		respondErr(w, m.id, codeInvalidParams, "request _meta is missing or has malformed required fields: "+metaCapsKey)
		return true
	}
	if m.modern() && m.method == "ping" {
		respondErr(w, m.id, -32601, "ping")
		return true
	}
	return false
}

func discoverResult() map[string]any {
	return map[string]any{
		"resultType": "complete", "supportedVersions": knownVersions,
		"capabilities": map[string]any{"resources": map[string]any{}, "tools": map[string]any{}},
		"instructions": instructions, "ttlMs": 0, "cacheScope": "private",
		"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": serverName, "version": serverVer}},
	}
}

// inputRequired asks the person, through the client, to approve in C Note. The answer is never approval: the retry
// carrying requestState only gets the pending confirmation back.
func inputRequired(id string) map[string]any {
	return map[string]any{
		"resultType": "input_required",
		"inputRequests": map[string]any{"approval": map[string]any{"method": "elicitation/create", "params": map[string]any{
			"mode": "form", "message": approvalMessage(id),
			"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{
				"noted": map[string]any{"type": "boolean", "title": "I will approve it in C Note"}}},
		}}},
		"requestState": "confirmation:" + id,
	}
}

// recorder buffers a reply so a 2026-07-28 peer gets resultType, cache hints and its error codes.
type recorder struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (c *recorder) Header() http.Header         { return c.h }
func (c *recorder) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *recorder) WriteHeader(s int)           { c.status = s }

var cacheable = map[string]bool{"tools/list": true, "prompts/list": true, "resources/list": true, "resources/templates/list": true, "resources/read": true}

func modernize(method string, body []byte) []byte {
	var resp map[string]json.RawMessage
	if json.Unmarshal(body, &resp) != nil {
		return body
	}
	if raw, has := resp["result"]; has {
		var res map[string]json.RawMessage
		if json.Unmarshal(raw, &res) != nil || res == nil {
			return body
		}
		if _, has := res["resultType"]; !has {
			res["resultType"] = json.RawMessage(`"complete"`)
			if cacheable[method] {
				res["ttlMs"], res["cacheScope"] = json.RawMessage(`0`), json.RawMessage(`"private"`)
			}
		}
		resp["result"], _ = command.Marshal(res)
	} else if raw, has := resp["error"]; has {
		var e map[string]json.RawMessage
		if json.Unmarshal(raw, &e) == nil && string(e["code"]) == "-32002" {
			e["code"] = json.RawMessage(`-32602`) // resource-not-found is plain invalid params from 2026-07-28
			resp["error"], _ = command.Marshal(e)
		}
	}
	out, err := command.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   json.RawMessage `json:"error,omitempty"`
	}{"2.0", resp["id"], resp["result"], resp["error"]})
	if err != nil {
		return body
	}
	return out
}
