// Package audit is the admin view over core.events (the append-only audit trail), its CSV export and the
// retention report. Data-subject export and erasure live in data.go. All Go-only.
package audit

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
)

const (
	maxExport        = 50_000
	defaultRetention = 8 // years; Companies Act 8 yrs, GST 72 months from the annual return due date. Verify with counsel.
)

func Commands() []command.Command {
	adm := func(name string, g command.Group, h command.Handler, role command.Role, confirm bool) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole, c.Confirm = role, confirm
		return c
	}
	return []command.Command{
		adm("audit.list", command.GroupAdmin, list, command.Admin, false),
		adm("audit.export", command.GroupAdmin, export, command.Admin, true),
		adm("audit.retention", command.GroupAdmin, retention, command.Admin, false),
		adm("data.export_subject", command.GroupAdmin, exportSubject, command.Admin, true),
		adm("data.request_list", command.GroupAdmin, requestList, command.Admin, false),
		adm("data.erase_subject", command.GroupDestructive, eraseSubject, command.Owner, false),
	}
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type in map[string]json.RawMessage

func decode(raw json.RawMessage) (in, error) {
	var m in
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

// str returns "" when absent or null.
func (m in) str(k string, max int) (string, error) {
	v, ok := m[k]
	if !ok || string(v) == "null" {
		return "", nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", command.ErrInvalid(k + " must be a string")
	}
	if s = strings.TrimSpace(s); len([]rune(s)) > max {
		return "", command.ErrInvalid(k + " is too long")
	}
	return s, nil
}

func (m in) uuid(k string) (string, error) {
	s, err := m.str(k, 36)
	if err == nil && s != "" && !uuidRe.MatchString(s) {
		err = command.ErrInvalid(k + " must be a UUID")
	}
	return s, err
}

func (m in) date(k string) (string, error) {
	s, err := m.str(k, 10)
	if err == nil && s != "" {
		if _, e := time.Parse(time.DateOnly, s); e != nil || !dateRe.MatchString(s) {
			err = command.ErrInvalid(k + " must be a date like 2026-12-31")
		}
	}
	return s, err
}

type filter struct{ actor, prefix, note, from, to string }

func parseFilter(m in) (f filter, err error) {
	if f.actor, err = m.str("actor_id", 64); err != nil {
		return
	}
	if f.prefix, err = m.str("kind_prefix", 80); err != nil {
		return
	}
	if f.note, err = m.uuid("note_id"); err != nil {
		return
	}
	if f.from, err = m.date("from"); err != nil {
		return
	}
	f.to, err = m.date("to")
	return
}

type row struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	NoteID     *string         `json:"note_id"`
	NoteTitle  *string         `json:"note_title"`
	ActorType  string          `json:"actor_type"`
	ActorID    *string         `json:"actor_id"`
	ActorName  *string         `json:"actor_name"`
	OnBehalfOf *string         `json:"on_behalf_of"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  command.Ts      `json:"created_at"`
}

// query returns events newest first. Every argument is bound; the kind prefix is compared with left(), not LIKE,
// so "%" in a filter is literal.
func query(c *command.Ctx, f filter, cursor int64, limit int) ([]row, error) {
	rows, err := c.Tx.Query(c.Context, `
select e.id, e.kind, e.note_id::text, n.title, e.actor_type, e.actor_id,
       coalesce(u.name, a.name, case when e.actor_type = 'system' then 'System' end), e.on_behalf_of::text, e.payload, e.created_at
from core.events e
left join core.notes n on n.id = e.note_id
left join core.users u on e.actor_type = 'user' and u.id::text = e.actor_id
left join core.agents a on e.actor_type = 'agent' and a.id::text = e.actor_id
where ($1 = '' or e.actor_id = $1)
  and ($2 = '' or left(e.kind, length($2)) = $2)
  and ($3 = '' or e.note_id = $3::uuid)
  and ($4 = '' or e.created_at >= $4::date)
  and ($5 = '' or e.created_at < $5::date + 1)
  and ($6::bigint = 0 or e.id < $6::bigint)
order by e.id desc limit $7`, f.actor, f.prefix, f.note, f.from, f.to, cursor, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []row{}
	for rows.Next() {
		var r row
		var t time.Time
		if err := rows.Scan(&r.ID, &r.Kind, &r.NoteID, &r.NoteTitle, &r.ActorType, &r.ActorID, &r.ActorName, &r.OnBehalfOf, &r.Payload, &t); err != nil {
			return nil, command.DB(err)
		}
		r.CreatedAt = command.Ts(t)
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

func list(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	f, err := parseFilter(m)
	if err != nil {
		return nil, err
	}
	var cursor int64
	if s, err := m.str("cursor", 20); err != nil {
		return nil, err
	} else if s != "" {
		if cursor, err = strconv.ParseInt(s, 10, 64); err != nil || cursor < 1 {
			return nil, command.ErrInvalid("cursor is not valid")
		}
	}
	limit := 50
	if v, ok := m["limit"]; ok && string(v) != "null" {
		if json.Unmarshal(v, &limit) != nil || limit < 1 || limit > 200 {
			return nil, command.ErrInvalid("limit must be 1-200")
		}
	}
	rows, err := query(c, f, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		s := strconv.FormatInt(rows[limit-1].ID, 10)
		next = &s
	}
	return map[string]any{"events": rows, "next_cursor": next}, nil
}

// cell neutralises spreadsheet formulas: event titles and payloads are written by users.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func sp(s *string) string {
	if s == nil {
		return ""
	}
	return cell(*s)
}

func export(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	f, err := parseFilter(m)
	if err != nil {
		return nil, err
	}
	rows, err := query(c, f, 0, maxExport+1)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxExport {
		return nil, command.ErrInvalid("more than 50000 events match; narrow the date range or the filters")
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"id", "time", "kind", "actor_type", "actor_id", "actor_name", "note_id", "note_title", "payload"})
	for _, r := range rows {
		ts, _ := r.CreatedAt.MarshalJSON()
		_ = w.Write([]string{strconv.FormatInt(r.ID, 10), strings.Trim(string(ts), `"`), r.Kind, r.ActorType, sp(r.ActorID), sp(r.ActorName), sp(r.NoteID), sp(r.NoteTitle), cell(string(r.Payload))})
	}
	w.Flush()
	if _, err := c.Emit("audit.exported", nil, map[string]any{"rows": len(rows), "actor_id": f.actor, "kind_prefix": f.prefix, "note_id": f.note, "from": f.from, "to": f.to}); err != nil {
		return nil, err
	}
	return map[string]any{"filename": "audit-log.csv", "rows": len(rows), "csv": b.String()}, nil
}

// retention reports how many events are older than the configured years; nothing is deleted in v1
// (core.events is append-only). `years` sets the policy.
func retention(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if v, ok := m["years"]; ok && string(v) != "null" {
		var y int
		if json.Unmarshal(v, &y) != nil || y < 1 || y > 30 {
			return nil, command.ErrInvalid("years must be 1-30")
		}
		if _, err := c.Tx.Exec(c.Context, `update core.workspaces set settings = jsonb_set(settings, '{audit_retention_years}', to_jsonb($2::int)) where id = $1::uuid`, c.WorkspaceID(), y); err != nil {
			return nil, command.DB(err)
		}
		if _, err := c.Emit("audit.retention_set", nil, map[string]any{"years": y}); err != nil {
			return nil, err
		}
	}
	var years int
	if err := c.Tx.QueryRow(c.Context, `select coalesce((settings->>'audit_retention_years')::int, $2) from core.workspaces where id = $1::uuid`, c.WorkspaceID(), defaultRetention).Scan(&years); err != nil {
		return nil, command.DB(err)
	}
	var older int64
	var oldest *time.Time
	if err := c.Tx.QueryRow(c.Context, `select count(*) filter (where created_at < now() - make_interval(years => $1)), min(created_at) from core.events`, years).Scan(&older, &oldest); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"years": years, "default_years": defaultRetention, "events_older": older, "oldest_event": command.TsPtr(oldest), "deletes": false}, nil
}
