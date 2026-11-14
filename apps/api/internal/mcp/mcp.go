package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cnote/api/internal/serdecheck"
	"github.com/cnote/api/internal/serdejson"
	"github.com/cnote/api/internal/uuidparse"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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

// Server is the MCP endpoint.
type Server struct {
	Pool     *pgxpool.Pool
	Registry *command.Registry
	Auth     *auth.Auth

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
	if r.Method != http.MethodPost { // rmcp's stateless service only has POST; auth came first, like Rust
		w.Header().Set("Allow", "POST")
		w.Header()["Content-Type"] = nil // no sniffed type: Rust sends none
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "Method Not Allowed")
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
	if err != nil || len(buf) > maxRequestBody {
		text(w, http.StatusRequestEntityTooLarge, "Failed to buffer the request body: length limit exceeded")
		return
	}
	m, ok := parseMessage(buf)
	if !utf8.Valid(buf) {
		ok = false // serde refuses invalid UTF-8; Go's decoder would repair it
	}
	if !ok {
		s.decline(w, r, buf) // protocol corner cases: bad JSON, unrecognized messages, responses, unknown methods
		return
	}
	if vs := r.Header.Values("MCP-Protocol-Version"); len(vs) > 1 || (len(vs) == 1 && !slices.Contains(legacyVersions, vs[0])) {
		versionHeader(w, r, m.id, vs)
		return
	} else if len(vs) == 1 && m.method == "initialize" && vs[0] != m.initVersion {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		jsonRPCStatus400(w, m.id, -32600, "Invalid Request: MCP-Protocol-Version header ("+vs[0]+") does not match initialize params.protocolVersion ("+m.initVersion+")")
		return
	}
	if m.notification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.dispatch(w, r, p, m)
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
	if !isString(o["method"]) || json.Unmarshal(o["method"], &m.method) != nil {
		return m, false
	}
	if raw, has := o["params"]; has {
		var ps map[string]json.RawMessage
		if json.Unmarshal(raw, &ps) != nil || ps == nil {
			return m, false
		}
		if meta, has := ps["_meta"]; has && (!isObjectRaw(meta) || bytes.Contains(meta, []byte("io.modelcontextprotocol/"))) {
			return m, false // the 2026 request-metadata lifecycle is not served (see decline)
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
			if k == "_meta" {
				continue
			}
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
			case "_meta":
			case "requestState":
				var rs string
				if !isString(v) || json.Unmarshal(v, &rs) != nil {
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

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, p command.Principal, m message) {
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
		s.readResource(w, r, p, m)
	case "tools/call":
		s.callTool(w, r, p, m)
	}
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request, p command.Principal, m message) {
	var name string
	_ = json.Unmarshal(m.params["name"], &name)
	// the retry of an elicitation round trip: the command already ran once and created the confirmation
	var state string
	if json.Unmarshal(m.params["requestState"], &state) == nil {
		if id := parseUUID(strings.TrimPrefix(state, "confirmation:")); id != nil && strings.HasPrefix(state, "confirmation:") {
			out, _ := command.Marshal(map[string]any{"confirmation_required": true, "confirmation_id": *id})
			respond(w, m.id, shape(out))
			return
		}
	}
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
		s.exec(w, r, p, m, e.Name, inner, idem, confirm)
	default:
		for _, e := range s.catalog() {
			if e.Core && toolName(e.Name) == name {
				var raw json.RawMessage = []byte("{}")
				if v, has := m.params["arguments"]; has && string(v) != "null" {
					raw = v
				}
				s.exec(w, r, p, m, e.Name, raw, nil, nil)
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

// parseUUID is Uuid::from_str: every spelling the uuid crate accepts, canonical lowercase; nil when it refuses.
func parseUUID(s string) *string {
	if c, bad := uuidparse.Parse(s); bad == "" {
		return &c
	}
	return nil
}

// exec runs a command for a tool call and shapes the reply, including the human-approval flow. An input the Go
// handler declines is answered with serde's wording (serdecheck).
func (s *Server) exec(w http.ResponseWriter, r *http.Request, p command.Principal, m message, name string, rawArgs json.RawMessage,
	idem, confirm *string) {
	if !s.Registry.Has(name) {
		respondErr(w, m.id, codeInvalidParams, "unknown tool "+name)
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
	args = serdecheck.NormalizeUUIDs(InputSchema(s.Registry, name), serdecheck.EmptySequence(name, args))
	out, err := s.Registry.Execute(r.Context(), s.Pool, name, args, p, command.ExecOptions{IdempotencyKey: idem, ConfirmationID: confirm})
	var ce *command.Error
	switch {
	case err == nil:
		respond(w, m.id, shape(out))
	case errors.Is(err, command.ErrFallback): // the handler declined an input serde would reject
		respond(w, m.id, toolErr(command.ErrInvalid("invalid input: "+serdecheck.Describe(name, InputSchema(s.Registry, name), args))))
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

func isObjectRaw(b json.RawMessage) bool { return len(b) > 0 && b[0] == '{' }

// versionHeader answers a request whose MCP-Protocol-Version is not one of the legacy lifecycles: an unknown value
// is refused like rmcp does; 2026-07-28 (request-metadata lifecycle, elicitation round trips) is not served here.
func versionHeader(w http.ResponseWriter, r *http.Request, id json.RawMessage, vs []string) {
	v := strings.Join(vs, ", ")
	if len(vs) == 1 && vs[0] == "2026-07-28" {
		code, msg := -32020, "missing required Mcp-Method header"
		if r.Header.Get("Mcp-Method") != "" {
			code, msg = -32600, "the 2026-07-28 request-metadata lifecycle is not supported; use MCP-Protocol-Version 2025-11-25"
		}
		jsonRPCStatus400(w, id, code, msg)
		return
	}
	text(w, http.StatusBadRequest, "Bad Request: Unsupported MCP-Protocol-Version: "+v)
}

// decline answers what the fast path does not recognize, the way rmcp's streamable-HTTP server (stateless mode)
// does: unparsable bodies are a 415 "fail to deserialize" text, JSON-RPC responses a 400 error, notifications are
// accepted and ignored, and every request whose method or params rmcp cannot type is "method not found" naming
// the method. The 2026-07-28 request-metadata lifecycle (`_meta` protocolVersion) is not served.
func (s *Server) decline(w http.ResponseWriter, r *http.Request, buf []byte) {
	deser := func(msg string) { text(w, http.StatusUnsupportedMediaType, "fail to deserialize request body "+msg) }
	if e := serdejson.Check(buf, false); e != nil {
		deser(e.Error())
		return
	}
	o, valid := jsonObject(buf)
	if !valid {
		deser("unrecognized JSON-RPC message") // arrays (batches), scalars, repeated keys
		return
	}
	var ver string
	if !isString(o["jsonrpc"]) || json.Unmarshal(o["jsonrpc"], &ver) != nil || ver != "2.0" {
		deser("unrecognized JSON-RPC message")
		return
	}
	method, hasMethod := o["method"]
	if !hasMethod {
		if _, res := o["result"]; res {
			rpcStateless(w)
			return
		}
		if _, er := o["error"]; er {
			rpcStateless(w)
			return
		}
		deser("unrecognized JSON-RPC message")
		return
	}
	var name string
	if !isString(method) || json.Unmarshal(method, &name) != nil {
		deser("unrecognized JSON-RPC message")
		return
	}
	var params map[string]json.RawMessage
	if raw, has := o["params"]; has && string(raw) != "null" {
		if json.Unmarshal(raw, &params) != nil || params == nil {
			deser("unrecognized JSON-RPC message")
			return
		}
	}
	id, hasID := o["id"]
	if !hasID {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !(isString(id) || idRe.Match(id)) {
		deser("unrecognized JSON-RPC message")
		return
	}
	if meta, has := params["_meta"]; has && bytes.Contains(meta, []byte("io.modelcontextprotocol/protocolVersion")) {
		if len(r.Header.Values("MCP-Protocol-Version")) == 0 {
			jsonRPCStatus400(w, id, -32020, "request _meta protocolVersion requires MCP-Protocol-Version header")
			return
		}
		versionHeader(w, r, id, r.Header.Values("MCP-Protocol-Version"))
		return
	}
	respondErr(w, id, -32601, name)
}

func rpcStateless(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","error":{"code":-32600,"message":"stateless server does not accept JSON-RPC responses"}}`)
}

// jsonRPCStatus400 is a 400 JSON-RPC error with the member order rmcp writes (jsonrpc, id, error).
func jsonRPCStatus400(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	e, _ := json.Marshal(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{code, msg})
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":` + string(e) + `}`))
}
