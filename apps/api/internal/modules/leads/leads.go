// Package leads is the follow-up layer on top of crm deals: activity log, next step with a date, lost reason,
// the unanswered-inquiry inbox and the public inquiry form. All Go-only; the Rust-parity crm commands are untouched
// (a lead is a deal in stage 'lead'; their output never gains fields).
package leads

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	role := func(c command.Command, r command.Role) command.Command { c.MinRole = r; return c }
	return []command.Command{
		command.NewCommand("activity.log", command.GroupManageCRM, activityLog),
		command.NewCommand("activity.list", command.GroupRead, activityList), // guests read what page access lets them
		role(command.NewCommand("followup.list", command.GroupRead, followupList), command.Member),
		command.NewCommand("followup.set", command.GroupManageCRM, followupSet),
		command.NewCommand("deal.lose", command.GroupManageCRM, dealLose),
		role(command.NewCommand("inquiry_form.set", command.GroupAdmin, formSet), command.Admin),
		role(command.NewCommand("inquiry_form.get", command.GroupAdmin, formGet), command.Admin),
	}
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	kinds  = map[string]bool{"call": true, "visit": true, "whatsapp": true, "email": true, "note": true}
)

// in decodes the input keeping null apart from absent (null clears a follow-up date).
type in map[string]json.RawMessage

func decode(raw json.RawMessage) (in, error) {
	var m in
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

// str returns (value, present, error); null counts as absent.
func (m in) str(k string, max int) (string, bool, error) {
	v, ok := m[k]
	if !ok || string(v) == "null" {
		return "", false, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false, command.ErrInvalid(k + " must be a string")
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return "", false, command.ErrInvalid(k + " is too long")
	}
	return s, true, nil
}

func (m in) req(k string, max int) (string, error) {
	s, ok, err := m.str(k, max)
	if err == nil && (!ok || s == "") {
		err = command.ErrInvalid(k + " is required")
	}
	return s, err
}

func (m in) id(k string) (string, error) {
	s, err := m.req(k, 36)
	if err == nil && !uuidRe.MatchString(s) {
		err = command.ErrInvalid(k + " must be a UUID")
	}
	return s, err
}

func (m in) date(k string) (*string, bool, error) {
	s, ok, err := m.str(k, 10)
	if err != nil || !ok {
		return nil, ok || m.has(k), err
	}
	if _, e := time.Parse(time.DateOnly, s); e != nil || !dateRe.MatchString(s) {
		return nil, true, command.ErrInvalid(k + " must be a date like 2026-12-31")
	}
	return &s, true, nil
}

func (m in) has(k string) bool { _, ok := m[k]; return ok }

// kindOf says whether a live note is a deal or a contact ("" = neither).
func kindOf(c *command.Ctx, id string) (string, error) {
	var k string
	err := c.Tx.QueryRow(c.Context, `select case when exists(select 1 from crm.deals where note_id=n.id) then 'deal'
		when exists(select 1 from crm.contacts where note_id=n.id) then 'contact' else '' end
		from core.notes n where n.id=$1::uuid and n.deleted_at is null`, id).Scan(&k)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("note")
	}
	return k, command.DB(err)
}

// plan merges a new follow-up date and/or step into the deal (null date or empty step clears that half).
func plan(c *command.Ctx, id string, date *string, hasDate bool, step string, hasStep bool) error {
	var curDate, curStep *string
	if err := c.Tx.QueryRow(c.Context, `select next_follow_up::text,next_step from crm.deals where note_id=$1::uuid`, id).Scan(&curDate, &curStep); err != nil {
		return command.DB(err)
	}
	if hasDate {
		curDate = date
	}
	if hasStep {
		curStep = &step
		if step == "" {
			curStep = nil
		}
	}
	if _, err := c.Tx.Exec(c.Context, `update crm.deals set next_follow_up=$2::date, next_step=$3 where note_id=$1::uuid`, id, curDate, curStep); err != nil {
		return command.DB(err)
	}
	_, err := c.Emit("deal.follow_up_set", &id, map[string]any{"next_follow_up": curDate, "next_step": curStep})
	return err
}

// openDeal loads a deal the caller may edit and refuses closed ones for follow-up work.
func openDeal(c *command.Ctx, id string, forPlan bool) error {
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return err
	}
	var stage string
	err := c.Tx.QueryRow(c.Context, `select d.stage from crm.deals d join core.notes n on n.id=d.note_id where d.note_id=$1::uuid and n.deleted_at is null`, id).Scan(&stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("deal")
	}
	if err == nil && forPlan && (stage == "won" || stage == "lost") {
		return command.ErrInvalid("deal is closed; a closed deal has no follow-up")
	}
	return command.DB(err)
}

func activityLog(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("note_id")
	if err != nil {
		return nil, err
	}
	kind, err := m.req("kind", 20)
	if err == nil && !kinds[kind] {
		err = command.ErrInvalid("kind must be call, visit, whatsapp, email or note")
	}
	if err != nil {
		return nil, err
	}
	summary, err := m.req("summary", 2000)
	if err != nil {
		return nil, err
	}
	at, _, err := m.str("happened_at", 40)
	if err != nil {
		return nil, err
	}
	var when *time.Time
	if at != "" {
		t, e := time.Parse(time.RFC3339, at)
		if e != nil || t.After(time.Now().Add(5*time.Minute)) {
			return nil, command.ErrInvalid("happened_at must be a past time like 2026-12-31T10:00:00+05:30")
		}
		when = &t
	}
	date, hasDate, err := m.date("next_follow_up")
	if err != nil {
		return nil, err
	}
	step, hasStep, err := m.str("next_step", 300)
	if err != nil {
		return nil, err
	}
	k, err := kindOf(c, id)
	if err != nil {
		return nil, err
	}
	if k == "" {
		return nil, command.ErrInvalid("activities attach to a deal or a contact")
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if k == "contact" && (hasDate || hasStep) {
		return nil, command.ErrInvalid("a next step belongs on a deal, not a contact")
	}
	if k == "deal" && (hasDate || hasStep) {
		if err := openDeal(c, id, true); err != nil {
			return nil, err
		}
	}
	var out struct {
		ID string `json:"id"`
		At string `json:"happened_at"`
	}
	var t time.Time
	if err := c.Tx.QueryRow(c.Context, `insert into crm.activities(workspace_id,note_id,kind,summary,happened_at,created_by)
		values($1::uuid,$2::uuid,$3,$4,coalesce($5,now()),$6::uuid) returning id::text, happened_at`,
		c.WorkspaceID(), id, kind, summary, when, c.Principal.UserID).Scan(&out.ID, &t); err != nil {
		return nil, command.DB(err)
	}
	out.At = t.UTC().Format(time.RFC3339)
	if k == "deal" {
		if kind != "note" { // a call, visit or message is a response; a private note is not
			if _, err := c.Tx.Exec(c.Context, `update crm.deals set first_response_at=coalesce(first_response_at,$2) where note_id=$1::uuid`, id, t); err != nil {
				return nil, command.DB(err)
			}
		}
		if hasDate || hasStep {
			if err := plan(c, id, date, hasDate, step, hasStep); err != nil {
				return nil, err
			}
		}
	}
	if _, err := c.Emit("activity.logged", &id, map[string]any{"activity_id": out.ID, "kind": kind}); err != nil {
		return nil, err
	}
	return map[string]any{"id": out.ID, "note_id": id, "kind": kind, "summary": summary, "happened_at": out.At}, nil
}

func activityList(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	var note, contact *string
	if s, ok, e := m.str("note_id", 36); e != nil {
		return nil, e
	} else if ok {
		note = &s
	}
	if s, ok, e := m.str("contact_id", 36); e != nil {
		return nil, e
	} else if ok {
		contact = &s
	}
	if (note == nil) == (contact == nil) || (note != nil && !uuidRe.MatchString(*note)) || (contact != nil && !uuidRe.MatchString(*contact)) {
		return nil, command.ErrInvalid("give exactly one of note_id or contact_id")
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	// contact_id: the contact's own entries plus those on its deals (the customer 360 timeline)
	rows, err := c.Tx.Query(c.Context, `select a.id::text, a.note_id::text, n.title, a.kind, a.summary, a.happened_at, a.created_by::text, coalesce(u.name,'')
		from crm.activities a join core.notes n on n.id=a.note_id left join core.users u on u.id=a.created_by
		where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id,$2::uuid,$3,$1::text[]::uuid[]) <> 'none'
		and (a.note_id=$4::uuid or a.note_id=$5::uuid or a.note_id in (select note_id from crm.deals where contact_id=$5::uuid))
		order by a.happened_at desc, a.created_at desc limit 200`, spaces, uid, admin, note, contact)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, nid, title, kind, sum, who string
		var by *string
		var at time.Time
		if err := rows.Scan(&id, &nid, &title, &kind, &sum, &at, &by, &who); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "note_id": nid, "note_title": title, "kind": kind, "summary": sum,
			"happened_at": command.Ts(at), "created_by": by, "created_by_name": who})
	}
	return out, command.DB(rows.Err())
}

func followupSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	date, hasDate, err := m.date("next_follow_up")
	if err != nil {
		return nil, err
	}
	step, hasStep, err := m.str("next_step", 300)
	if err != nil {
		return nil, err
	}
	src, hasSrc, err := m.str("source", 60)
	if err != nil {
		return nil, err
	}
	if !hasDate && !hasStep && !hasSrc {
		return nil, command.ErrInvalid("give next_follow_up, next_step or source")
	}
	if err := openDeal(c, id, hasDate || hasStep); err != nil {
		return nil, err
	}
	if hasSrc {
		if _, err := c.Tx.Exec(c.Context, `update crm.deals set source=nullif($2,'') where note_id=$1::uuid`, id, src); err != nil {
			return nil, command.DB(err)
		}
	}
	if hasDate || hasStep {
		if err := plan(c, id, date, hasDate, step, hasStep); err != nil {
			return nil, err
		}
	}
	return planRow(c, id)
}

func planRow(c *command.Ctx, id string) (any, error) {
	var d, s, src, lr *string
	var fr *time.Time
	err := c.Tx.QueryRow(c.Context, `select next_follow_up::text,next_step,source,lost_reason,first_response_at from crm.deals where note_id=$1::uuid`, id).Scan(&d, &s, &src, &lr, &fr)
	return map[string]any{"id": id, "next_follow_up": d, "next_step": s, "source": src, "lost_reason": lr, "first_response_at": command.TsPtr(fr)}, command.DB(err)
}

func dealLose(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	reason, err := m.req("lost_reason", 200)
	if err != nil {
		return nil, err
	}
	out, err := c.Call("deal.move_stage", map[string]any{"id": id, "stage": "lost"}) // same gate and events as a drag to Lost
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `update crm.deals set lost_reason=$2, next_follow_up=null, next_step=null where note_id=$1::uuid`, id, reason); err != nil {
		return nil, command.DB(err)
	}
	return out, nil
}

type row struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Stage        string      `json:"stage"`
	AmountMinor  int64       `json:"amount_minor"`
	ContactID    *string     `json:"contact_id"`
	ContactName  string      `json:"contact_name"`
	ContactPhone *string     `json:"contact_phone"`
	OwnerID      *string     `json:"owner_id"`
	Source       *string     `json:"source"`
	NextFollowUp *string     `json:"next_follow_up"`
	NextStep     *string     `json:"next_step"`
	Responded    *command.Ts `json:"first_response_at"`
	CreatedAt    command.Ts  `json:"created_at"`
	LastNote     *string     `json:"last_activity"`
	DaysOverdue  *int64      `json:"days_overdue"`
}

// followup.list: open deals by follow-up date (overdue | today | week), deals with no date (unplanned), the unanswered inbox (inbox), or every
// open deal (omit due). Visibility is exactly deal.list's: readable spaces and page access.
func followupList(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	due, _, err := m.str("due", 10)
	if err != nil {
		return nil, err
	}
	var where string
	switch due {
	case "":
	case "overdue":
		where = ` and d.next_follow_up < $4::date`
	case "today":
		where = ` and d.next_follow_up = $4::date`
	case "week":
		where = ` and d.next_follow_up > $4::date and d.next_follow_up <= $4::date + 7`
	case "unplanned":
		where = ` and d.next_follow_up is null`
	case "inbox":
		where = ` and d.stage = 'lead' and d.first_response_at is null`
	default:
		return nil, command.ErrInvalid("due must be overdue, today, week, unplanned or inbox")
	}
	var ids [3]*string // owner_id, note_id, contact_id
	for i, k := range []string{"owner_id", "note_id", "contact_id"} {
		if s, ok, e := m.str(k, 36); e != nil {
			return nil, e
		} else if ok {
			if !uuidRe.MatchString(s) {
				return nil, command.ErrInvalid(k + " must be a UUID")
			}
			ids[i] = &s
		}
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	today := billing.TodayIST().Format(time.DateOnly)
	rows, err := c.Tx.Query(c.Context, `select n.id::text, n.title, d.stage, d.amount_minor, d.contact_id::text, coalesce(cn.title,''), ct.phone,
		d.owner_id::text, d.source, d.next_follow_up::text, d.next_step, d.first_response_at, n.created_at, ($4::date - d.next_follow_up),
		(select summary from crm.activities a where a.note_id=n.id order by a.happened_at desc, a.created_at desc limit 1)
		from crm.deals d join core.notes n on n.id=d.note_id
		left join core.notes cn on cn.id=d.contact_id left join crm.contacts ct on ct.note_id=d.contact_id
		where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id,$2::uuid,$3,$1::text[]::uuid[]) <> 'none'
		and d.stage not in ('won','lost') and ($5::uuid is null or d.owner_id=$5::uuid) and ($6::uuid is null or n.id=$6::uuid)
		and ($7::uuid is null or d.contact_id=$7::uuid)`+where+`
		order by d.next_follow_up nulls last, n.created_at desc limit 200`, spaces, uid, admin, today, ids[0], ids[1], ids[2])
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []row{}
	for rows.Next() {
		var r row
		var fr *time.Time
		var ca time.Time
		if err := rows.Scan(&r.ID, &r.Title, &r.Stage, &r.AmountMinor, &r.ContactID, &r.ContactName, &r.ContactPhone, &r.OwnerID, &r.Source,
			&r.NextFollowUp, &r.NextStep, &fr, &ca, &r.DaysOverdue, &r.LastNote); err != nil {
			return nil, command.DB(err)
		}
		r.Responded, r.CreatedAt = command.TsPtr(fr), command.Ts(ca)
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

// ---- inquiry form config (admin) ----

func formView(c *command.Ctx) (map[string]any, error) {
	var space, token string
	var owner *string
	var enabled bool
	err := c.Tx.QueryRow(c.Context, `select space_id::text, owner_id::text, token, enabled from crm.inquiry_forms where workspace_id=$1::uuid`, c.WorkspaceID()).Scan(&space, &owner, &token, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"configured": false, "enabled": false}, nil
	}
	return map[string]any{"configured": true, "enabled": enabled, "space_id": space, "owner_id": owner, "token": token, "path": "/f/" + token}, command.DB(err)
}

func formGet(c *command.Ctx, _ json.RawMessage) (any, error) { return formView(c) }

func formSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	var enabled *bool
	if v, ok := m["enabled"]; ok && string(v) != "null" {
		var b bool
		if json.Unmarshal(v, &b) != nil {
			return nil, command.ErrInvalid("enabled must be true or false")
		}
		enabled = &b
	}
	var rotate bool
	if v, ok := m["rotate"]; ok && string(v) != "null" && json.Unmarshal(v, &rotate) != nil {
		return nil, command.ErrInvalid("rotate must be true or false")
	}
	var space, owner *string
	for k, dst := range map[string]**string{"space_id": &space, "owner_id": &owner} {
		if s, ok, e := m.str(k, 36); e != nil {
			return nil, e
		} else if ok {
			if !uuidRe.MatchString(s) {
				return nil, command.ErrInvalid(k + " must be a UUID")
			}
			*dst = &s
		}
	}
	if space != nil {
		if err := access.AssertSpaceAccess(c, *space, true); err != nil {
			return nil, err
		}
	}
	if owner != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from core.memberships where user_id=$1::uuid and workspace_id=$2::uuid and role in ('owner','admin','member'))`, *owner, c.WorkspaceID()).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrInvalid("owner_id must be a member of this workspace")
		}
	}
	var have bool
	if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from crm.inquiry_forms where workspace_id=$1::uuid)`, c.WorkspaceID()).Scan(&have); err != nil {
		return nil, command.DB(err)
	}
	if !have && space == nil {
		return nil, command.ErrInvalid("space_id is required to create the form")
	}
	if !have {
		var b [24]byte
		_, _ = rand.Read(b[:])
		if _, err = c.Tx.Exec(c.Context, `insert into crm.inquiry_forms(workspace_id,space_id,owner_id,token,enabled) values($1::uuid,$2::uuid,$3::uuid,$4,coalesce($5,true))`,
			c.WorkspaceID(), space, owner, hex.EncodeToString(b[:]), enabled); err != nil {
			return nil, command.DB(err)
		}
		return formView(c)
	}
	var b [24]byte
	_, _ = rand.Read(b[:])
	_, err = c.Tx.Exec(c.Context, `update crm.inquiry_forms set space_id=coalesce($1::uuid,space_id), owner_id=case when $2 then $3::uuid else owner_id end,
		enabled=coalesce($4,enabled), token=case when $5 then $6 else token end where workspace_id=$7::uuid`,
		space, m.has("owner_id"), owner, enabled, rotate, hex.EncodeToString(b[:]), c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	return formView(c)
}
