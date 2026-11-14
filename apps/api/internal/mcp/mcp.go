package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
)

const (
	maxRequestBody = 4 * 1024 * 1024 // rmcp's default POST body cap
	recentNotes    = 50
	instructions   = "C Note: notes, projects, docs, CRM and invoicing on one object model. Core tools are listed directly. Use `discover` to find any other command by keyword (it returns schemas), then run it with `invoke`. Money-moving and outbound commands return confirmation_required; a human must approve them in C Note, then call `invoke` again with the confirmation_id."
	invokeDesc     = "Run any C Note command by name (dots or underscores), with its arguments. Optional `idempotency_key` makes a retried write safe. Commands that move money or send messages (invoices, email, WhatsApp) do not run immediately: the result has confirmation_required=true and a confirmation_id. A human must approve that request in C Note (an MCP elicitation acceptance is NOT approval); after they have, call invoke again with the same command and `confirmation_id`. The approved payload is the one that runs."
	discoverDesc   = "Search ALL C Note commands by keyword (name and description). Returns names, descriptions, tool groups and input schemas; run one with `invoke`."
	confirmSuffix  = " Requires human approval in C Note: returns confirmation_required and a confirmation_id; after approval, call `invoke` with this command and that confirmation_id."
)

// Server is the MCP endpoint. Upstream is the Rust server: it answers what Go does not.
type Server struct {
	Pool     *pgxpool.Pool
	Registry *command.Registry
	Auth     *auth.Auth
	Upstream http.Handler

	once sync.Once
	cat  []entry
}

// ---------- wire types (field order is the order rmcp serializes) ----------

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type annotations struct {
	ReadOnly    *bool `json:"readOnlyHint,omitempty"`
	Destructive *bool `json:"destructiveHint,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations annotations     `json:"annotations"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content           []textContent   `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

type content struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

const (
	codeInvalidParams     = -32602
	codeInternal          = -32603
	codeResourceNotFound  = -32002
	protocolDefault       = "2025-11-25"
	serverName, serverVer = "rmcp", "3.9.0" // what rmcp 3.9 reports in serverInfo
)

var bTrue, bFalse = true, false

func ptr(b bool) *bool {
	if b {
		return &bTrue
	}
	return &bFalse
}

var discoverSchema = json.RawMessage(`{"properties":{"limit":{"description":"Max results (default 15)","type":"integer"},"query":{"description":"Keywords, e.g. \"invoice send\" or \"deal stage\". Empty lists everything.","type":"string"}},"required":["query"],"type":"object"}`)
var invokeSchema = json.RawMessage(`{"properties":{"arguments":{"description":"The command's input, per its schema","type":"object"},"command":{"description":"Command name, e.g. \"invoice.issue\" (from ` + "`discover`" + `)","type":"string"},"confirmation_id":{"description":"Id of a request a human approved in C Note","type":"string"},"idempotency_key":{"description":"Retrying with the same key returns the first result instead of repeating the write","type":"string"}},"required":["command"],"type":"object"}`)

func (s *Server) tools() []tool {
	var out []tool
	for _, e := range s.catalog() {
		if !e.Core {
			continue
		}
		d := e.Description
		if e.Confirm {
			d += confirmSuffix
		}
		out = append(out, tool{Name: toolName(e.Name), Description: d, InputSchema: e.Schema,
			Annotations: annotations{ReadOnly: ptr(e.Group == "read"), Destructive: ptr(e.Group == "destructive")}})
	}
	return append(out,
		tool{Name: "discover", Description: discoverDesc, InputSchema: discoverSchema, Annotations: annotations{ReadOnly: ptr(true)}},
		tool{Name: "invoke", Description: invokeDesc, InputSchema: invokeSchema, Annotations: annotations{Destructive: ptr(true)}})
}

// ---------- helpers ----------

func write(w http.ResponseWriter, status int, ct string, body []byte) {
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header()["Content-Type"] = nil // Rust sends none; a nil entry stops net/http sniffing text/plain
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func respond(w http.ResponseWriter, id json.RawMessage, result any) {
	b, _ := command.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
	write(w, http.StatusOK, "application/json", b)
}

func respondErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	b, _ := command.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	write(w, http.StatusOK, "application/json", b)
}

func text(w http.ResponseWriter, status int, msg string) { write(w, status, "", []byte(msg)) }

// pretty is serde_json::to_string_pretty over a Value: sorted keys, two spaces, no HTML escaping.
func pretty(raw []byte) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// norm rewrites any JSON value the way serde_json::Value would print it (sorted keys, numbers as written).
func norm(v any) []byte {
	b, _ := command.Marshal(v)
	n, err := command.Norm(b)
	if err != nil {
		return b
	}
	return n
}

func toolOK(raw []byte) callResult {
	r := callResult{Content: []textContent{{"text", pretty(raw)}}}
	if len(raw) > 0 && raw[0] == '{' { // structuredContent must be an object
		r.StructuredContent = raw
	}
	return r
}

// toolErr is a tool error carrying the CmdError code, so agents can branch on it (forbidden, not_found, invalid, ...).
func toolErr(ce *command.Error) callResult {
	b := norm(map[string]any{"error": ce})
	return callResult{Content: []textContent{{"text", string(b)}}, StructuredContent: b, IsError: true}
}

var debugName = map[command.Code]string{command.Unauthenticated: "Unauthenticated", command.Forbidden: "Forbidden", command.NotFound: "NotFound",
	command.Invalid: "Invalid", command.Conflict: "Conflict", command.RateLimited: "RateLimited", command.PlanLimit: "PlanLimit", command.Internal: "Internal"}

// rpcErr is rpc_err(): errors from reading resources become protocol errors.
func rpcErr(w http.ResponseWriter, id json.RawMessage, err error) {
	var ce *command.Error
	if !errors.As(err, &ce) {
		slog.Error("mcp resource error", "error", err)
		ce = command.NewError(command.Internal, "internal error")
	}
	switch ce.Code {
	case command.NotFound:
		respondErr(w, id, codeResourceNotFound, ce.Message)
	case command.Invalid:
		respondErr(w, id, codeInvalidParams, ce.Message)
	default:
		respondErr(w, id, codeInternal, debugName[ce.Code]+": "+ce.Message)
	}
}

// ---------- authentication and transport checks ----------

// unauthorized is the 401 with the RFC 9728 pointer so MCP clients can discover the authorization server.
func unauthorized(w http.ResponseWriter, host, errCode string) {
	challenge := `Bearer realm="cnote"`
	if base, ok := auth.PublicBase(host); ok {
		challenge += `, resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp"`
	}
	if errCode != "" {
		challenge += `, error="` + errCode + `"`
	}
	for i := 0; i < len(challenge); i++ {
		if c := challenge[i]; (c < 32 && c != '\t') || c > 126 {
			challenge = "Bearer" // HeaderValue::from_str failed
			break
		}
	}
	w.Header().Set("WWW-Authenticate", challenge)
	b, _ := command.Marshal(map[string]any{"error": map[string]string{"code": "unauthenticated", "message": "Authorization: Bearer <cn_ API key or cno_ OAuth token> required"}})
	write(w, http.StatusUnauthorized, "application/json", b)
}

func splitUnquoted(s string, delim byte) []string {
	var out []string
	quoted, escaped, start := false, false, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && c == delim:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

var qualityRe = regexp.MustCompile(`^(0(\.[0-9]{0,3})?|1(\.0{0,3})?)$`)

func qualityAcceptable(q string) bool {
	if !qualityRe.MatchString(q) {
		return true // a malformed weight is ignored, only an explicit zero excludes the range
	}
	f, _ := strconv.ParseFloat(q, 64)
	return f > 0
}

// accepts mirrors rmcp's Accept handling: both media types must be listed and not weighted zero.
func accepts(h http.Header) (json, sse bool) {
	for _, line := range h.Values("Accept") {
		for _, entry := range splitUnquoted(line, ',') {
			parts := splitUnquoted(entry, ';')
			essence := strings.TrimSpace(parts[0])
			ok := true
			for _, p := range parts[1:] {
				if name, val, has := strings.Cut(p, "="); has && strings.EqualFold(strings.TrimSpace(name), "q") && !qualityAcceptable(strings.TrimSpace(val)) {
					ok = false
				}
			}
			if ok {
				json = json || strings.EqualFold(essence, "application/json")
				sse = sse || strings.EqualFold(essence, "text/event-stream")
			}
		}
	}
	return
}

var legacyVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// ServeHTTP handles POST /mcp.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := auth.BearerToken(r.Header)
	if !ok {
		unauthorized(w, r.Host, "")
		return
	}
	_ = token
	p, authed, err := s.Auth.MCPPrincipal(r.Context(), r.Header, auth.MCPResource(r.Host))
	if err != nil {
		slog.Error("bearer lookup failed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !authed {
		unauthorized(w, r.Host, "invalid_token")
		return
	}
	if j, e := accepts(r.Header); !j || !e {
		text(w, http.StatusNotAcceptable, "Not Acceptable: Client must accept both application/json and text/event-stream")
		return
	}
	ct := r.Header.Get("Content-Type")
	if essence, _, _ := strings.Cut(ct, ";"); !strings.EqualFold(strings.TrimSpace(essence), "application/json") {
		text(w, http.StatusUnsupportedMediaType, "Unsupported Media Type: Content-Type must be application/json")
		return
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	proxy := func() {
		r.Body = io.NopCloser(bytes.NewReader(buf))
		s.Upstream.ServeHTTP(w, r)
	}
	if err != nil || len(buf) > maxRequestBody {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		s.Upstream.ServeHTTP(w, r)
		return
	}
	m, ok := parseMessage(buf)
	if !ok {
		proxy() // Rust owns every protocol corner case (bad JSON, 2026 lifecycle, batches, responses, odd params)
		return
	}
	if vs := r.Header.Values("MCP-Protocol-Version"); len(vs) > 1 || (len(vs) == 1 && !slices.Contains(legacyVersions, vs[0])) {
		proxy()
		return
	} else if len(vs) == 1 && m.method == "initialize" && vs[0] != m.initVersion {
		proxy()
		return
	}
	if m.notification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.dispatch(w, r, p, m, proxy)
}

// ---------- request parsing ----------

type message struct {
	id           json.RawMessage
	method       string
	notification bool
	params       map[string]json.RawMessage
	initVersion  string
}

var idRe = regexp.MustCompile(`^(0|-?[1-9][0-9]{0,17})$`)

// parseMessage accepts only requests whose shape serde would also accept and that need nothing beyond the legacy
// lifecycle; ok=false means "let Rust decide".
func parseMessage(b []byte) (m message, ok bool) {
	o, valid := jsonObject(b)
	if !valid {
		return m, false
	}
	var ver string
	if !isString(o["jsonrpc"]) || json.Unmarshal(o["jsonrpc"], &ver) != nil || ver != "2.0" {
		return m, false
	}
	for k := range o {
		if k != "jsonrpc" && k != "id" && k != "method" && k != "params" {
			return m, false
		}
	}
	if !isString(o["method"]) || json.Unmarshal(o["method"], &m.method) != nil {
		return m, false
	}
	if raw, has := o["params"]; has {
		var ps map[string]json.RawMessage
		if json.Unmarshal(raw, &ps) != nil || ps == nil {
			return m, false
		}
		if _, meta := ps["_meta"]; meta {
			return m, false
		}
		m.params = ps
	}
	id, hasID := o["id"]
	if !hasID {
		m.notification = m.method == "notifications/initialized" && m.params == nil
		return m, m.notification
	}
	var sid string
	if json.Unmarshal(id, &sid) == nil && len(id) > 0 && id[0] == '"' {
		m.id = id
	} else if idRe.Match(id) {
		m.id = id
	} else {
		return m, false
	}
	switch m.method {
	case "ping":
		return m, true
	case "tools/list", "resources/list", "resources/templates/list", "prompts/list":
		for k, v := range m.params {
			var c string
			if k != "cursor" || !isString(v) || json.Unmarshal(v, &c) != nil {
				return m, false
			}
		}
		return m, true
	case "initialize":
		return m, parseInitialize(&m)
	case "tools/call":
		for k, v := range m.params {
			switch k {
			case "name":
				var n string
				if !isString(v) || json.Unmarshal(v, &n) != nil {
					return m, false
				}
			case "arguments":
				var a map[string]json.RawMessage
				if json.Unmarshal(v, &a) != nil { // null decodes to a nil map and is allowed
					return m, false
				}
			default:
				return m, false
			}
		}
		_, hasName := m.params["name"]
		return m, hasName
	case "resources/read":
		var u string
		if len(m.params) != 1 || !isString(m.params["uri"]) || json.Unmarshal(m.params["uri"], &u) != nil {
			return m, false
		}
		return m, true
	}
	return m, false
}

// isString: a JSON string literal (null would unmarshal into a Go string without error, but serde rejects it).
func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

func parseInitialize(m *message) bool {
	if len(m.params) != 3 {
		return false
	}
	if !isString(m.params["protocolVersion"]) || json.Unmarshal(m.params["protocolVersion"], &m.initVersion) != nil {
		return false
	}
	var caps map[string]json.RawMessage
	if json.Unmarshal(m.params["capabilities"], &caps) != nil || caps == nil {
		return false
	}
	for _, v := range caps {
		var o map[string]json.RawMessage
		if json.Unmarshal(v, &o) != nil || o == nil {
			return false
		}
	}
	var info map[string]string
	if json.Unmarshal(m.params["clientInfo"], &info) != nil || info["name"] == "" && info["version"] == "" {
		return false
	}
	_, hasName := info["name"]
	_, hasVer := info["version"]
	return hasName && hasVer && len(info) <= 3
}

func jsonObject(b []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	o := map[string]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		ks, isStr := k.(string)
		if err != nil || !isStr {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		if _, dup := o[ks]; dup {
			return nil, false
		}
		o[ks] = v
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return o, true
}

// ---------- methods ----------

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, p command.Principal, m message, proxy func()) {
	switch m.method {
	case "ping":
		respond(w, m.id, map[string]any{})
	case "initialize":
		v := m.initVersion
		if !slices.Contains(legacyVersions, v) {
			v = protocolDefault
		}
		respond(w, m.id, struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
			Instructions    string         `json:"instructions"`
		}{v, map[string]any{"resources": map[string]any{}, "tools": map[string]any{}}, map[string]any{"name": serverName, "version": serverVer}, instructions})
	case "tools/list":
		respond(w, m.id, map[string]any{"tools": s.tools()})
	case "prompts/list":
		respond(w, m.id, map[string]any{"prompts": []any{}})
	case "resources/templates/list":
		type tmpl struct {
			URITemplate string `json:"uriTemplate"`
			Name        string `json:"name"`
			Description string `json:"description"`
			MimeType    string `json:"mimeType"`
		}
		respond(w, m.id, map[string]any{"resourceTemplates": []tmpl{
			{"cnote://ws/{ws}/note/{id}", "note", "A note as Markdown with title, owner, freshness and verify_by front matter", "text/markdown"},
			{"cnote://ws/{ws}/view/{id}", "view", "A saved view: its CNQL query and, when available, its current results", "text/markdown"},
		}})
	case "resources/list":
		s.listResources(w, r, p, m)
	case "resources/read":
		s.readResource(w, r, p, m, proxy)
	case "tools/call":
		s.callTool(w, r, p, m, proxy)
	}
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request, p command.Principal, m message, proxy func()) {
	var name string
	_ = json.Unmarshal(m.params["name"], &name)
	args := map[string]json.RawMessage{}
	if raw, has := m.params["arguments"]; has {
		var a map[string]json.RawMessage
		_ = json.Unmarshal(raw, &a)
		if a != nil {
			args = a
		}
	}
	switch name {
	case "discover":
		s.discover(w, m.id, args)
	case "invoke":
		var cmd string
		if json.Unmarshal(args["command"], &cmd) != nil || args["command"] == nil {
			respond(w, m.id, toolErr(command.ErrInvalid("command is required")))
			return
		}
		e, found := s.resolve(cmd)
		if !found {
			respond(w, m.id, toolErr(command.ErrNotFound("command "+cmd)))
			return
		}
		inner := json.RawMessage(`{}`)
		if raw, has := args["arguments"]; has {
			inner = raw
		}
		var idem *string
		if v, ok := stringOf(args["idempotency_key"]); ok {
			idem = &v
		}
		var confirm *string
		if v, ok := stringOf(args["confirmation_id"]); ok {
			confirm = parseUUID(v)
		}
		s.exec(w, r, p, m, e.Name, inner, idem, confirm, proxy)
	default:
		for _, e := range s.catalog() {
			if e.Core && toolName(e.Name) == name {
				var raw json.RawMessage = []byte("{}")
				if v, has := m.params["arguments"]; has && string(v) != "null" {
					raw = v
				}
				s.exec(w, r, p, m, e.Name, raw, nil, nil, proxy)
				return
			}
		}
		respondErr(w, m.id, codeInvalidParams, "unknown tool "+name)
	}
}

func stringOf(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var uuidSimpleRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// parseUUID is Uuid::from_str for the spellings Go can tell apart (hyphenated or 32 hex digits); canonical lowercase.
func parseUUID(s string) *string {
	switch {
	case uuidRe.MatchString(s):
		s = strings.ToLower(s)
	case uuidSimpleRe.MatchString(s):
		s = strings.ToLower(s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:])
	default:
		return nil
	}
	return &s
}

// exec runs a command for a tool call and shapes the reply, including the human-approval flow. Commands only Rust
// has are proxied; so is anything the Go handler declines (inputs whose Rust error text it does not reproduce).
func (s *Server) exec(w http.ResponseWriter, r *http.Request, p command.Principal, m message, name string, rawArgs json.RawMessage,
	idem, confirm *string, proxy func()) {
	if !s.Registry.Has(name) {
		proxy()
		return
	}
	// Meta-arguments are ours, not the command's. Like Rust, a top-level value wins and then leaves the inner
	// object alone; otherwise the inner one is taken (and removed) when it is a string.
	args := rawArgs
	var obj map[string]json.RawMessage
	if json.Unmarshal(rawArgs, &obj) == nil && obj != nil {
		if idem == nil {
			if v, ok := stringOf(obj["idempotency_key"]); ok {
				idem = &v
			}
			delete(obj, "idempotency_key")
		}
		if confirm == nil {
			if v, ok := stringOf(obj["confirmation_id"]); ok {
				confirm = parseUUID(v)
			}
			delete(obj, "confirmation_id")
		}
		args, _ = command.Marshal(obj)
	}
	out, err := s.Registry.Execute(r.Context(), s.Pool, name, args, p, command.ExecOptions{IdempotencyKey: idem, ConfirmationID: confirm})
	var ce *command.Error
	switch {
	case err == nil:
		respond(w, m.id, shape(out))
	case errors.Is(err, command.ErrFallback):
		proxy()
	case errors.As(err, &ce):
		respond(w, m.id, toolErr(ce))
	default:
		slog.Error("mcp command failed", "command", name, "error", err)
		respond(w, m.id, toolErr(command.NewError(command.Internal, "internal error")))
	}
}

// shape is tool_ok plus the approval nudge: a confirmation result tells the agent to ask a human (an agent's own
// acceptance is never approval).
func shape(out []byte) callResult {
	var v map[string]any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if dec.Decode(&v) == nil && v != nil && v["confirmation_required"] == true {
		if id, _ := v["confirmation_id"].(string); parseUUID(id) != nil {
			id = *parseUUID(id)
			v["message"] = fmt.Sprintf("This action needs human approval. Ask the person to approve request %s in C Note "+
				"(Settings > Confirmations). Your acceptance here is not approval. After they approve, call `invoke` "+
				"again with the same command and confirmation_id %s.", id, id)
			return toolOK(norm(v))
		}
	}
	return toolOK(out)
}

// ---------- discover ----------

func words(q string) []string {
	return strings.FieldsFunc(strings.ToLower(q), func(c rune) bool {
		return !(unicode.IsLetter(c) || unicode.IsNumber(c) || unicode.Is(unicode.Other_Alphabetic, c))
	})
}

func (s *Server) discover(w http.ResponseWriter, id json.RawMessage, args map[string]json.RawMessage) {
	query, _ := stringOf(args["query"])
	limit := 15
	if raw := args["limit"]; len(raw) > 0 {
		if n, err := strconv.ParseUint(string(raw), 10, 64); err == nil { // as_u64: plain non-negative integers only
			limit = int(min(max(n, 1), 50))
		}
	}
	ws := words(query)
	type hit struct {
		score int
		e     entry
	}
	var hits []hit
	for _, e := range s.catalog() {
		name, desc := strings.ToLower(e.Name), strings.ToLower(e.Description)
		score, match := 0, true
		for _, w := range ws {
			switch {
			case strings.Contains(name, w):
				score += 3
			case strings.Contains(desc, w):
				score++
			default:
				match = false
			}
			if !match {
				break
			}
		}
		if match {
			hits = append(hits, hit{score, e})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.e.Name, b.e.Name)
	})
	cmds := []map[string]any{}
	for _, h := range hits[:min(limit, len(hits))] {
		cmds = append(cmds, map[string]any{"name": h.e.Name, "description": h.e.Description, "group": h.e.Group,
			"confirmation_required": h.e.Confirm, "input_schema": h.e.Schema})
	}
	respond(w, id, toolOK(norm(map[string]any{"commands": cmds})))
}
