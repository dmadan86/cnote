// Package admin ports workspace administration from cnote-core/admin.rs.
package admin

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	specs := []struct {
		name   string
		group  command.Group
		role   command.Role
		secret bool
		h      command.Handler
	}{
		{"member.list", command.GroupRead, command.Guest, false, memberList},
		{"member.add", command.GroupAdmin, command.Admin, false, memberAdd},
		{"agent.create", command.GroupAdmin, command.Admin, true, agentCreate},
		{"agent.list", command.GroupRead, command.Guest, false, agentList},
		{"apikey.create", command.GroupAdmin, command.Member, true, keyCreate},
		{"apikey.list", command.GroupAdmin, command.Admin, false, keyList},
		{"apikey.revoke", command.GroupAdmin, command.Admin, false, keyRevoke},
		{"workspace.get", command.GroupRead, command.Guest, false, workspaceGet},
		{"workspace.update", command.GroupAdmin, command.Admin, false, workspaceUpdate},
		{"confirmation.list", command.GroupRead, command.Guest, false, confirmationList},
		{"confirmation.decide", command.GroupAdmin, command.Member, false, confirmationDecide},
		{"usage.summary", command.GroupRead, command.Admin, false, usageSummary},
	}
	out := make([]command.Command, 0, len(specs))
	for _, s := range specs {
		c := command.NewCommand(s.name, s.group, s.h)
		c.MinRole = s.role
		c.ReturnsSecret = s.secret
		out = append(out, c)
	}
	return out
}

// Decode fields separately: Go's case-insensitive struct decoding and null defaults differ from serde.
func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if !utf8.Valid(raw) || json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrFallback
	}
	// serde rejects duplicate known fields; defer duplicate objects to the reference.
	dec := json.NewDecoder(bytes.NewReader(raw))
	_, _ = dec.Token()
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, command.ErrFallback
		}
		k := tok.(string)
		if seen[k] {
			return nil, command.ErrFallback
		}
		seen[k] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil, command.ErrFallback
		}
	}
	return m, nil
}
func required[T any](m map[string]json.RawMessage, k string) (T, error) {
	var v T
	b, ok := m[k]
	if !ok || string(b) == "null" || json.Unmarshal(b, &v) != nil {
		return v, command.ErrFallback
	}
	return v, nil
}
func optional[T any](m map[string]json.RawMessage, k string) (*T, error) {
	b, ok := m[k]
	if !ok || string(b) == "null" {
		return nil, nil
	}
	v, e := required[T](m, k)
	return &v, e
}
func scopes(m map[string]json.RawMessage) ([]string, error) {
	if _, ok := m["scopes"]; !ok {
		return []string{}, nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(m["scopes"], &raw) != nil || raw == nil {
		return nil, command.ErrFallback
	}
	out := []string{}
	for _, b := range raw {
		var v string
		if string(b) == "null" || json.Unmarshal(b, &v) != nil {
			return nil, command.ErrFallback
		}
		out = append(out, v)
	}
	return out, nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func id(m map[string]json.RawMessage) (string, error) {
	s, e := required[string](m, "id")
	if e != nil || !uuidRE.MatchString(s) {
		return "", command.ErrFallback
	}
	return strings.ToLower(s), nil
}

// All UUID columns are selected as text. Timestamp values use chrono AutoSi formatting.
func query(c *command.Ctx, sql string, args ...any) ([]map[string]any, error) {
	rows, e := c.Tx.Query(c.Context, sql, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		vs, e := rows.Values()
		if e != nil {
			return nil, e
		}
		m := map[string]any{}
		for i, f := range rows.FieldDescriptions() {
			v := vs[i]
			if f.DataTypeOID == 114 || f.DataTypeOID == 3802 {
				if v != nil {
					n, err := command.Norm(rows.RawValues()[i])
					if err != nil {
						return nil, err
					}
					v = n
				}
			}
			if t, ok := v.(time.Time); ok {
				v = command.Ts(t)
			}
			m[f.Name] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func one(c *command.Ctx, sql string, args ...any) (any, error) {
	rs, e := query(c, sql, args...)
	if e != nil {
		return nil, e
	}
	if len(rs) == 0 {
		return nil, pgx.ErrNoRows
	}
	return rs[0], nil
}
func listing(sql string) command.Handler {
	return func(c *command.Ctx, b json.RawMessage) (any, error) {
		if _, e := object(b); e != nil {
			return nil, e
		}
		return query(c, sql)
	}
}

var memberList = listing("select m.user_id::text, u.email, u.name, m.role from core.memberships m join core.users u on u.id=m.user_id where m.deactivated_at is null order by u.name")
var agentList = listing("select id::text,name,kind,owner_user_id::text,scopes,created_at from core.agents order by name")
var keyList = listing("select id::text,name,prefix,agent_id::text,scopes,last_used_at,revoked_at from core.api_keys order by created_at desc")
var confirmationList = listing("select id::text,command,input,requested_by,status,created_at from core.confirmations where status='pending' and kind='agent' order by created_at desc")
var usageSummary = listing("select kind,sum(quantity)::bigint as quantity from core.usage where created_at > now()-interval '30 days' group by kind order by kind")

const workspaceCols = "id::text,name,slug,plan,region,settings"

func workspaceGet(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, e := object(b); e != nil {
		return nil, e
	}
	return one(c, "select "+workspaceCols+" from core.workspaces where id=$1::uuid", c.WorkspaceID())
}

func memberAdd(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	email, e := required[string](m, "email")
	if e != nil {
		return nil, e
	}
	role, e := required[string](m, "role")
	if e != nil {
		return nil, e
	}
	name, e := optional[string](m, "name")
	if e != nil {
		return nil, e
	}
	if !slices.Contains([]string{"admin", "member", "guest"}, role) {
		return nil, command.ErrInvalid("role must be admin, member or guest")
	}
	// Rust applies contextual Greek final-sigma lowercasing; Go does not. Preserve
	// exact behavior through the reference for that uncommon email spelling.
	if strings.ContainsRune(email, 'Σ') {
		return nil, command.ErrFallback
	}
	email = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(email), "İ", "i\u0307"))
	n := strings.Split(email, "@")[0]
	if name != nil {
		n = *name
	}
	var uid string
	e = c.Tx.QueryRow(c.Context, "insert into core.users (email,name) values ($1,$2) on conflict (email) do update set email=excluded.email returning id::text", email, n).Scan(&uid)
	if e != nil {
		return nil, e
	}
	var cur string
	e = c.Tx.QueryRow(c.Context, "select role from core.memberships where user_id=$1::uuid", uid).Scan(&cur)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	me := c.Principal.Role
	if e == nil && (command.ParseRole(cur) == command.Owner || (command.ParseRole(cur) >= me && me != command.Owner)) {
		return nil, command.ErrForbidden("cannot change this member's role")
	}
	if command.ParseRole(role) > me {
		return nil, command.ErrForbidden("cannot grant a role above your own")
	}
	_, e = c.Tx.Exec(c.Context, "insert into core.memberships (workspace_id,user_id,role) values ($1::uuid,$2::uuid,$3) on conflict (workspace_id,user_id) do update set role=excluded.role", c.WorkspaceID(), uid, role)
	if e != nil {
		return nil, e
	}
	_, e = c.Emit("member.added", nil, map[string]any{"user_id": uid, "role": role})
	return map[string]any{"user_id": uid, "email": email, "name": n, "role": role}, e
}
func newKey() (string, string, string, error) {
	buf := make([]byte, 28)
	if _, e := rand.Read(buf); e != nil {
		return "", "", "", e
	}
	prefix := hex.EncodeToString(buf[:4])
	key := "cn_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(buf[4:])
	h := sha256.Sum256([]byte(key))
	return prefix, key, hex.EncodeToString(h[:]), nil
}
func keyCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	name, e := required[string](m, "name")
	if e != nil {
		return nil, e
	}
	s, e := scopes(m)
	if e != nil {
		return nil, e
	}
	if c.Principal.Kind != command.KindUser {
		return nil, command.ErrForbidden("only people can create API keys")
	}
	p, k, h, e := newKey()
	if e != nil {
		return nil, e
	}
	var uid string
	e = c.Tx.QueryRow(c.Context, "insert into core.api_keys (workspace_id,user_id,name,prefix,key_hash,scopes) values ($1::uuid,$2::uuid,$3,$4,$5,$6) returning id::text", c.WorkspaceID(), c.Principal.UserID, name, p, h, s).Scan(&uid)
	return map[string]any{"id": uid, "api_key": k}, e
}
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.ReplaceAll(s, "İ", "i\u0307")) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	s = strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "workspace"
	}
	return s
}
func agentCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	name, e := required[string](m, "name")
	if e != nil {
		return nil, e
	}
	kind, e := optional[string](m, "kind")
	if e != nil {
		return nil, e
	}
	s, e := scopes(m)
	if e != nil {
		return nil, e
	}
	if c.Principal.UserID == nil {
		return nil, command.ErrForbidden("agents must be created by a person")
	}
	if c.Principal.Kind != command.KindUser {
		return nil, command.ErrForbidden("only people can create agents")
	}
	if len(s) == 0 {
		s = []string{"read", "write_notes", "write_items", "manage_crm"}
	}
	knd := "external"
	if kind != nil {
		knd = *kind
	}
	var aid, kid string
	e = c.Tx.QueryRow(c.Context, "insert into core.agents (workspace_id,name,kind,owner_user_id,scopes) values ($1::uuid,$2,$3,$4::uuid,$5) returning id::text", c.WorkspaceID(), strings.TrimSpace(name), knd, c.Principal.UserID, s).Scan(&aid)
	if e != nil {
		return nil, e
	}
	p, k, h, e := newKey()
	if e != nil {
		return nil, e
	}
	e = c.Tx.QueryRow(c.Context, "insert into core.api_keys (workspace_id,user_id,agent_id,name,prefix,key_hash,scopes) values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7) returning id::text", c.WorkspaceID(), c.Principal.UserID, aid, "agent:"+slugify(name), p, h, s).Scan(&kid)
	if e != nil {
		return nil, e
	}
	_, e = c.Emit("agent.created", nil, map[string]any{"agent_id": aid, "scopes": s})
	return map[string]any{"agent_id": aid, "api_key_id": kid, "api_key": k}, e
}
func keyRevoke(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	uid, e := id(m)
	if e != nil {
		return nil, e
	}
	tag, e := c.Tx.Exec(c.Context, "update core.api_keys set revoked_at=now() where id=$1::uuid and revoked_at is null", uid)
	return map[string]bool{"revoked": tag.RowsAffected() == 1}, e
}
func workspaceUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	name, e := optional[string](m, "name")
	if e != nil {
		return nil, e
	}
	settings := m["settings"]
	if settings == nil {
		return nil, command.ErrFallback
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(settings, &obj)
	var groups []json.RawMessage
	if json.Unmarshal(obj["agent_tool_groups"], &groups) == nil {
		for _, g := range groups {
			var s string
			if json.Unmarshal(g, &s) != nil || !slices.Contains([]string{"read", "write_notes", "write_items", "manage_crm", "manage_invoices", "send_messages", "admin", "destructive"}, s) {
				normalized, err := command.Norm(g)
				if err != nil {
					return nil, err
				}
				return nil, command.ErrInvalid("unknown tool group " + string(normalized))
			}
		}
	}
	out, e := one(c, "update core.workspaces set name=coalesce($2,name),settings=settings || $3::jsonb where id=$1::uuid returning "+workspaceCols, c.WorkspaceID(), name, settings)
	if e != nil {
		return nil, e
	}
	var keys []string
	if obj != nil {
		keys = []string{}
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	_, e = c.Emit("workspace.settings_updated", nil, map[string]any{"keys": keys})
	return out, e
}
func confirmationDecide(c *command.Ctx, b json.RawMessage) (any, error) {
	m, e := object(b)
	if e != nil {
		return nil, e
	}
	uid, e := id(m)
	if e != nil {
		return nil, e
	}
	approve, e := required[bool](m, "approve")
	if e != nil {
		return nil, e
	}
	if c.Principal.Kind != command.KindUser {
		return nil, command.ErrForbidden("only a person can confirm")
	}
	status := "rejected"
	if approve {
		status = "confirmed"
	}
	var cmd string
	e = c.Tx.QueryRow(c.Context, "update core.confirmations set status=$2,confirmed_by=$3::uuid,decided_at=now() where id=$1::uuid and status='pending' and kind='agent' returning command", uid, status, c.Principal.UserID).Scan(&cmd)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("pending confirmation")
	}
	if e != nil {
		return nil, e
	}
	_, e = c.Emit("confirmation.decided", nil, map[string]any{"confirmation_id": uid, "command": cmd, "status": status})
	return map[string]string{"status": status}, e
}

// NewKey and Slugify are exported for the HTTP surface (marketplace token exchange, workspace creation).
func NewKey() (string, string, string, error) { return newKey() }
func Slugify(s string) string                 { return slugify(s) }
