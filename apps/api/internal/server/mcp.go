package server

// /mcp is still Rust's (transport, resources, core tools, OAuth endpoints). Go answers only the two meta tools'
// Go-only half, because Go-only commands are not in Rust's registry:
//   - tools/call discover: Rust answers, Go merges in its Go-only commands (same scoring, same shape).
//   - tools/call invoke of a Go-only command: runs natively through the same gate as REST.
//
// Everything else, and every request Go cannot fully authenticate or parse, is proxied byte for byte.
// shortcut: no MCP elicitation nudge for Go-only confirmations (the result still carries the approval message);
// upgrade when Rust's MCP moves to Go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/cnote/api/internal/command"
)

type rpcReq struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		State     any            `json:"requestState"`
		StateSnk  any            `json:"request_state"`
	} `json:"params"`
}

// mcpResource mirrors cnote_mcp::mcp_resource: PUBLIC_BASE_URL, else loopback hosts only, else "".
func mcpResource(h http.Header, host string) string {
	if v := os.Getenv("PUBLIC_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/") + "/mcp"
	}
	name := host
	if rest, ok := strings.CutPrefix(host, "["); ok {
		name, _, _ = strings.Cut(rest, "]")
	} else {
		name, _, _ = strings.Cut(host, ":")
	}
	if host != "" && (name == "localhost" || name == "127.0.0.1" || name == "::1") {
		return "http://" + host + "/mcp"
	}
	return ""
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
	proxy := func() { r.Body = io.NopCloser(bytes.NewReader(buf)); s.Upstream.ServeHTTP(w, r) }
	var q rpcReq
	if err != nil || len(buf) > maxBody || !isJSON(r) || json.Unmarshal(buf, &q) != nil ||
		q.Method != "tools/call" || q.Params.State != nil || q.Params.StateSnk != nil ||
		(q.Params.Name != "discover" && q.Params.Name != "invoke") ||
		!strings.Contains(r.Header.Get("Accept"), "application/json") || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		proxy()
		return
	}
	p, ok, err := s.Auth.MCPPrincipal(r.Context(), r.Header, mcpResource(r.Header, r.Host))
	if err != nil || !ok {
		proxy() // Rust owns the 401 challenge and 500s
		return
	}
	if q.Params.Name == "discover" {
		s.mcpDiscover(w, r, q, proxy)
		return
	}
	cmd, ok := s.goOnly(q.Params.Arguments["command"])
	if !ok {
		proxy()
		return
	}
	s.mcpInvoke(w, r, q, p, cmd, proxy)
}

// goOnly resolves "quote.send" or "quote_send" to a command that carries MCP metadata (= Rust lacks it).
func (s *Server) goOnly(v any) (command.Command, bool) {
	name, _ := v.(string)
	for _, c := range s.Registry.List() {
		if c.Description != "" && (c.Name == name || strings.ReplaceAll(c.Name, ".", "_") == name) {
			return c, true
		}
	}
	return command.Command{}, false
}

func rpcWrite(w http.ResponseWriter, id json.RawMessage, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	writeJSON(w, http.StatusOK, b)
}

// pretty is serde_json::to_string_pretty over a Value: sorted keys, two spaces, no HTML escaping.
func pretty(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func toolResult(v any, isErr bool) map[string]any {
	text := pretty(v)
	if isErr {
		b, _ := json.Marshal(v)
		text = string(b)
	}
	r := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
	if _, obj := v.(map[string]any); obj {
		r["structuredContent"] = v
	}
	return r
}

func (s *Server) mcpInvoke(w http.ResponseWriter, r *http.Request, q rpcReq, p command.Principal, cmd command.Command, proxy func()) {
	a := q.Params.Arguments
	inner, _ := a["arguments"].(map[string]any)
	if inner == nil {
		inner = map[string]any{}
	}
	idem, _ := a["idempotency_key"].(string)
	conf, _ := a["confirmation_id"].(string)
	// meta-arguments may also ride inside `arguments`, but only when not given at the top level (as in Rust)
	if _, set := a["idempotency_key"].(string); !set {
		if v, ok := inner["idempotency_key"].(string); ok {
			idem = v
		}
		delete(inner, "idempotency_key")
	}
	if conf == "" {
		conf, _ = inner["confirmation_id"].(string)
		delete(inner, "confirmation_id")
	}
	opts := command.ExecOptions{}
	if idem != "" {
		opts.IdempotencyKey = &idem
	}
	if conf = strings.ToLower(conf); wsRe.MatchString(conf) {
		opts.ConfirmationID = &conf
	}
	in, _ := json.Marshal(inner)
	out, err := s.Registry.Execute(r.Context(), s.Pool, cmd.Name, in, p, opts)
	var ce *command.Error
	switch {
	case err == nil:
		var v any
		_ = json.Unmarshal(out, &v)
		if m, ok := v.(map[string]any); ok && m["confirmation_required"] == true {
			if id, _ := m["confirmation_id"].(string); id != "" {
				m["message"] = fmt.Sprintf("This action needs human approval. Ask the person to approve request %s in C Note "+
					"(Settings > Confirmations). Your acceptance here is not approval. After they approve, call `invoke` "+
					"again with the same command and confirmation_id %s.", id, id)
			}
		}
		rpcWrite(w, q.ID, toolResult(v, false))
	case errors.Is(err, command.ErrFallback):
		proxy()
	case errors.As(err, &ce):
		rpcWrite(w, q.ID, toolResult(map[string]any{"error": ce}, true))
	default:
		rpcWrite(w, q.ID, toolResult(map[string]any{"error": command.NewError(command.Internal, "internal error")}, true))
	}
}

// score mirrors cnote_mcp::discover: every word must hit; a name hit is worth 3, a description hit 1.
func score(words []string, name, desc string) (int, bool) {
	name, desc = strings.ToLower(name), strings.ToLower(desc)
	n := 0
	for _, w := range words {
		switch {
		case strings.Contains(name, w):
			n += 3
		case strings.Contains(desc, w):
			n++
		default:
			return 0, false
		}
	}
	return n, true
}

func (s *Server) mcpDiscover(w http.ResponseWriter, r *http.Request, q rpcReq, proxy func()) {
	rec := httptest.NewRecorder()
	s.Upstream.ServeHTTP(rec, r)
	pass := func() {
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
	var resp struct {
		Result struct {
			Structured struct {
				Commands []map[string]any `json:"commands"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		pass()
		return
	}
	query, _ := q.Params.Arguments["query"].(string)
	limit := 15
	if l, ok := q.Params.Arguments["limit"].(float64); ok && l >= 0 {
		limit = min(max(int(l), 1), 50)
	}
	words := strings.FieldsFunc(strings.ToLower(query), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) })
	type hit struct {
		score int
		name  string
		v     map[string]any
	}
	var hits []hit
	for _, c := range resp.Result.Structured.Commands {
		n, _ := c["name"].(string)
		d, _ := c["description"].(string)
		sc, _ := score(words, n, d)
		hits = append(hits, hit{sc, n, c})
	}
	extra := 0
	for _, c := range s.Registry.List() {
		if sc, ok := score(words, c.Name, c.Description); ok && c.Description != "" {
			var schema any
			_ = json.Unmarshal(c.Schema, &schema)
			hits = append(hits, hit{sc, c.Name, map[string]any{"name": c.Name, "description": c.Description, "group": string(c.Group),
				"confirmation_required": c.Confirm, "input_schema": schema}})
			extra++
		}
	}
	if extra == 0 {
		pass()
		return
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.name, b.name)
	})
	cmds := []any{}
	for _, h := range hits[:min(limit, len(hits))] {
		cmds = append(cmds, h.v)
	}
	rpcWrite(w, q.ID, toolResult(map[string]any{"commands": cmds}, false))
}
