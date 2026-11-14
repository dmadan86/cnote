// Package crm ports the command handlers in cnote-finance/src/crm.rs.
package crm

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/jackc/pgx/v5"
)

var stages = []string{"lead", "qualified", "proposal", "negotiation", "won", "lost"}

func checkStage(s string) error {
	for _, v := range stages {
		if s == v {
			return nil
		}
	}
	return command.ErrInvalid("stage must be one of " + strings.Join(stages, ", "))
}
func stageDefaults(s string, p *int64) (int64, bool) {
	if s == "won" {
		return 100, true
	}
	if s == "lost" {
		return 0, true
	}
	return value(p, int64(10)), false
}
func value[T any](p *T, d T) T {
	if p != nil {
		return *p
	}
	return d
}
func checkFields(a, p *int64) error {
	if a != nil && *a < 0 {
		return command.ErrInvalid("amount_minor cannot be negative")
	}
	if p != nil && (*p < 0 || *p > 100) {
		return command.ErrInvalid("probability must be 0-100")
	}
	return nil
}

type Contact struct {
	ID      string  `json:"id"`
	SpaceID string  `json:"space_id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Email   *string `json:"email"`
	Phone   *string `json:"phone"`
	Company *string `json:"company"`
	GSTIN   *string `json:"gstin"`
	State   *string `json:"state_code"`
	Address *string `json:"billing_address"`
}

const contactSQL = `select n.id::text,n.space_id::text,n.title,c.kind,c.email,c.phone,c.company,c.gstin,c.state_code,c.billing_address from crm.contacts c join core.notes n on n.id=c.note_id`

func scanContact(r pgx.Row) (Contact, error) {
	var v Contact
	err := r.Scan(&v.ID, &v.SpaceID, &v.Name, &v.Kind, &v.Email, &v.Phone, &v.Company, &v.GSTIN, &v.State, &v.Address)
	return v, err
}
func loadContact(c *command.Ctx, id string) (Contact, error) {
	v, err := scanContact(c.Tx.QueryRow(c.Context, contactSQL+` where c.note_id=$1::uuid and n.deleted_at is null`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = command.ErrNotFound("contact")
	}
	return v, err
}

type Deal struct {
	ID            string      `json:"id"`
	SpaceID       string      `json:"space_id"`
	Title         string      `json:"title"`
	Stage         string      `json:"stage"`
	Amount        int64       `json:"amount_minor"`
	Currency      string      `json:"currency"`
	ContactID     *string     `json:"contact_id"`
	OwnerID       *string     `json:"owner_id"`
	ExpectedClose *string     `json:"expected_close"`
	Probability   int32       `json:"probability"`
	ClosedAt      *command.Ts `json:"closed_at"`
}

const dealSQL = `select n.id::text,n.space_id::text,n.title,d.stage,d.amount_minor,d.currency::text,d.contact_id::text,d.owner_id::text,d.expected_close::text,d.probability,d.closed_at from crm.deals d join core.notes n on n.id=d.note_id`

func scanDeal(r pgx.Row) (Deal, error) {
	var v Deal
	var t *time.Time
	err := r.Scan(&v.ID, &v.SpaceID, &v.Title, &v.Stage, &v.Amount, &v.Currency, &v.ContactID, &v.OwnerID, &v.ExpectedClose, &v.Probability, &t)
	v.ClosedAt = command.TsPtr(t)
	return v, err
}
func loadDeal(c *command.Ctx, id string) (Deal, error) {
	v, err := scanDeal(c.Tx.QueryRow(c.Context, dealSQL+` where d.note_id=$1::uuid and n.deleted_at is null`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = command.ErrNotFound("deal")
	}
	return v, err
}

// Decode every known field before validation, as serde does (including unused update fields).
type input struct {
	id, space, title    string
	strs                map[string]*string
	amount, prob, limit *int64
	vendor              *bool // Go-only: is_vendor on create/update, vendor filter on list (Rust ignores both)
}

func decode(raw json.RawMessage, cmd string) (input, error) {
	i := input{strs: map[string]*string{}}
	o, e := parseObj(raw)
	if e != nil {
		return i, e
	}
	req := func(k string, uuid bool) error {
		var s string
		var err error
		if uuid {
			s, err = o.reqUUID(k)
		} else {
			s, err = o.reqStr(k)
		}
		if err == nil {
			switch k {
			case "id":
				i.id = s
			case "space_id":
				i.space = s
			default:
				i.title = s
			}
		}
		return err
	}
	if strings.HasSuffix(cmd, "create") {
		if e = req("space_id", true); e != nil {
			return i, e
		}
		k := "title"
		if strings.HasPrefix(cmd, "contact") {
			k = "name"
		}
		if e = req(k, false); e != nil {
			return i, e
		}
	} else if !strings.HasSuffix(cmd, "list") {
		if e = req("id", true); e != nil {
			return i, e
		}
	}
	var ss, us, ds []string
	switch cmd {
	case "contact.create":
		ss = []string{"kind", "email", "phone", "company", "gstin", "state_code", "billing_address"}
	case "contact.update":
		ss = []string{"name", "email", "phone", "company", "gstin", "state_code", "billing_address"}
	case "contact.list":
		ss = []string{"q"}
		us = []string{"space_id"}
	case "deal.create":
		ss = []string{"currency", "stage"}
		us = []string{"contact_id", "owner_id"}
		ds = []string{"expected_close"}
	case "deal.update":
		ss = []string{"title"}
		us = []string{"contact_id", "owner_id"}
		ds = []string{"expected_close"}
	case "deal.list":
		ss = []string{"stage"}
		us = []string{"space_id", "contact_id"}
	case "deal.move_stage":
		s, err := o.reqStr("stage")
		if err != nil {
			return i, err
		}
		i.strs["stage"] = &s
	}
	for _, k := range ss {
		i.strs[k], e = o.optStr(k)
		if e != nil {
			return i, e
		}
	}
	for _, k := range us {
		i.strs[k], e = o.optUUID(k)
		if e != nil {
			return i, e
		}
	}
	for _, k := range ds {
		i.strs[k], e = o.optDate(k)
		if e != nil {
			return i, e
		}
	}
	if cmd == "deal.create" || cmd == "deal.update" {
		i.amount, e = o.optInt("amount_minor", 64)
		if e != nil {
			return i, e
		}
		i.prob, e = o.optInt("probability", 32)
		if e != nil {
			return i, e
		}
	}
	if strings.HasPrefix(cmd, "contact.") {
		k := "is_vendor"
		if cmd == "contact.list" {
			k = "vendor"
		}
		if v, ok := o.get(k); ok {
			var b bool
			if json.Unmarshal(v, &b) != nil {
				return i, command.ErrFallback
			}
			i.vendor = &b
		}
	}
	if strings.HasSuffix(cmd, "list") {
		i.limit, e = o.optInt("limit", 64)
	}
	return i, e
}
func Commands() []command.Command {
	var out []command.Command
	for _, name := range []string{"contact.create", "contact.get", "contact.list", "contact.update", "deal.create", "deal.list", "deal.update", "deal.move_stage"} {
		n := name
		g := command.GroupManageCRM
		if strings.HasSuffix(n, "get") || strings.HasSuffix(n, "list") {
			g = command.GroupRead
		}
		out = append(out, command.NewCommand(n, g, func(c *command.Ctx, raw json.RawMessage) (any, error) {
			i, err := decode(raw, n)
			if err != nil {
				return nil, err
			}
			return execute(c, n, i)
		}))
	}
	return append(out, command.NewCommand("contact.lifecycle", command.GroupRead, lifecycle))
}
func emit(c *command.Ctx, kind, id string, p any) error { _, err := c.Emit(kind, &id, p); return err }
func rename(c *command.Ctx, id string, title *string) error {
	if title != nil {
		s := strings.TrimSpace(*title)
		title = nil
		if s != "" {
			title = &s
		}
	}
	_, err := c.Tx.Exec(c.Context, `update core.notes set title=coalesce($2,title),updated_at=now(),version=version+1 where id=$1::uuid`, id, title)
	return err
}
func execute(c *command.Ctx, cmd string, i input) (any, error) {
	s := i.strs
	switch cmd {
	case "contact.create":
		kind := value(s["kind"], "person")
		if kind != "person" && kind != "company" {
			return nil, command.ErrInvalid("kind must be person or company")
		}
		if strings.TrimSpace(i.title) == "" {
			return nil, command.ErrInvalid("name is required")
		}
		g, st, err := checkTaxIDs(s["gstin"], s["state_code"])
		if err != nil {
			return nil, err
		}
		n, err := notes.InsertTypedNote(c, i.space, i.title, "contact", nil)
		if err != nil {
			return nil, err
		}
		_, err = c.Tx.Exec(c.Context, `insert into crm.contacts(note_id,workspace_id,kind,email,phone,company,gstin,state_code,billing_address,is_vendor) values($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,coalesce($10,false))`, n.ID, c.WorkspaceID(), kind, s["email"], s["phone"], s["company"], g, st, s["billing_address"], i.vendor)
		if err != nil {
			return nil, err
		}
		if err = emit(c, "contact.created", n.ID, map[string]any{"name": n.Title}); err != nil {
			return nil, err
		}
		return loadContact(c, n.ID)
	case "contact.get":
		if _, err := access.AssertNoteAccess(c, i.id, false); err != nil {
			return nil, err
		}
		return loadContact(c, i.id)
	case "contact.update":
		if _, err := access.AssertNoteAccess(c, i.id, true); err != nil {
			return nil, err
		}
		cur, err := loadContact(c, i.id)
		if err != nil {
			return nil, err
		}
		g, st := cur.GSTIN, cur.State
		if s["gstin"] != nil || s["state_code"] != nil {
			st = s["state_code"]
			if st == nil && s["gstin"] == nil {
				st = cur.State
			}
			g = s["gstin"]
			if g == nil {
				g = cur.GSTIN
			}
			g, st, err = checkTaxIDs(g, st)
			if err != nil {
				return nil, err
			}
		}
		_, err = c.Tx.Exec(c.Context, `update crm.contacts set email=coalesce($2,email),phone=coalesce($3,phone),company=coalesce($4,company),gstin=$5,state_code=$6,billing_address=coalesce($7,billing_address),is_vendor=coalesce($8,is_vendor) where note_id=$1::uuid`, i.id, s["email"], s["phone"], s["company"], g, st, s["billing_address"], i.vendor)
		if err != nil {
			return nil, err
		}
		if err = rename(c, i.id, s["name"]); err != nil {
			return nil, err
		}
		if err = emit(c, "contact.updated", i.id, map[string]any{}); err != nil {
			return nil, err
		}
		return loadContact(c, i.id)
	case "deal.create":
		stage := value(s["stage"], "lead")
		if err := checkStage(stage); err != nil {
			return nil, err
		}
		if err := checkFields(i.amount, i.prob); err != nil {
			return nil, err
		}
		if err := checkContact(c, s["contact_id"]); err != nil {
			return nil, err
		}
		n, err := notes.InsertTypedNote(c, i.space, i.title, "deal", s["owner_id"])
		if err != nil {
			return nil, err
		}
		prob, closed := stageDefaults(stage, i.prob)
		owner := s["owner_id"]
		if owner == nil {
			owner = c.Principal.UserID
		}
		_, err = c.Tx.Exec(c.Context, `insert into crm.deals(note_id,workspace_id,stage,amount_minor,currency,contact_id,owner_id,expected_close,probability,closed_at) values($1::uuid,$2::uuid,$3,$4,$5,$6::uuid,$7::uuid,$8::date,$9,case when $10 then now() end)`, n.ID, c.WorkspaceID(), stage, value(i.amount, 0), value(s["currency"], "INR"), s["contact_id"], owner, s["expected_close"], prob, closed)
		if err != nil {
			return nil, err
		}
		if err = emit(c, "deal.created", n.ID, map[string]any{"stage": stage}); err != nil {
			return nil, err
		}
		return loadDeal(c, n.ID)
	case "deal.update":
		if _, err := access.AssertNoteAccess(c, i.id, true); err != nil {
			return nil, err
		}
		if _, err := loadDeal(c, i.id); err != nil {
			return nil, err
		}
		if err := checkFields(i.amount, i.prob); err != nil {
			return nil, err
		}
		if err := checkContact(c, s["contact_id"]); err != nil {
			return nil, err
		}
		_, err := c.Tx.Exec(c.Context, `update crm.deals set contact_id=coalesce($2::uuid,contact_id),amount_minor=coalesce($3,amount_minor),expected_close=coalesce($4::date,expected_close),probability=coalesce($5,probability),owner_id=coalesce($6::uuid,owner_id) where note_id=$1::uuid`, i.id, s["contact_id"], i.amount, s["expected_close"], i.prob, s["owner_id"])
		if err != nil {
			return nil, err
		}
		if err = rename(c, i.id, s["title"]); err != nil {
			return nil, err
		}
		if err = emit(c, "deal.updated", i.id, map[string]any{}); err != nil {
			return nil, err
		}
		return loadDeal(c, i.id)
	case "deal.move_stage":
		stage := *s["stage"]
		if err := checkStage(stage); err != nil {
			return nil, err
		}
		if _, err := access.AssertNoteAccess(c, i.id, true); err != nil {
			return nil, err
		}
		before, err := loadDeal(c, i.id)
		if err != nil {
			return nil, err
		}
		if before.Stage == stage {
			return before, nil
		}
		p := int64(before.Probability)
		prob, closed := stageDefaults(stage, &p)
		_, err = c.Tx.Exec(c.Context, `update crm.deals set stage=$2,probability=$3,closed_at=case when $4 then now() end where note_id=$1::uuid`, i.id, stage, prob, closed)
		if err != nil {
			return nil, err
		}
		d, err := loadDeal(c, i.id)
		if err != nil {
			return nil, err
		}
		if err = emit(c, "deal.stage_changed", i.id, map[string]any{"from": before.Stage, "to": d.Stage}); err != nil {
			return nil, err
		}
		if stage == "won" {
			err = emit(c, "deal.won", i.id, map[string]any{"amount_minor": d.Amount, "currency": d.Currency, "contact_id": d.ContactID})
		}
		return d, err
	default:
		return list(c, cmd, i)
	}
}
func checkContact(c *command.Ctx, id *string) error {
	if id == nil {
		return nil
	}
	if _, err := access.AssertNoteAccess(c, *id, false); err != nil {
		return err
	}
	_, err := loadContact(c, *id)
	return err
}
func list(c *command.Ctx, cmd string, i input) (any, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	lim := value(i.limit, 100)
	if lim < 1 {
		lim = 1
	}
	if lim > 500 {
		lim = 500
	}
	s := i.strs
	if cmd == "contact.list" {
		q := s["q"]
		if q != nil && strings.TrimSpace(*q) == "" {
			q = nil
		}
		rows, err := c.Tx.Query(c.Context, contactSQL+` where n.deleted_at is null and n.space_id=any($1::text[]::uuid[]) and ($2::uuid is null or n.space_id=$2::uuid) and core.note_access(n.id,$5::uuid,$6,$1::text[]::uuid[]) <> 'none' and ($3::text is null or n.title ilike '%'||$3||'%' or c.email ilike '%'||$3||'%' or c.company ilike '%'||$3||'%') and ($7::boolean is null or c.is_vendor=$7) order by n.title limit $4`, spaces, s["space_id"], q, lim, uid, admin, i.vendor)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []Contact{}
		for rows.Next() {
			v, err := scanContact(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	rows, err := c.Tx.Query(c.Context, dealSQL+` where n.deleted_at is null and n.space_id=any($1::text[]::uuid[]) and ($2::uuid is null or n.space_id=$2::uuid) and core.note_access(n.id,$6::uuid,$7,$1::text[]::uuid[]) <> 'none' and ($3::text is null or d.stage=$3) and ($4::uuid is null or d.contact_id=$4::uuid) order by n.updated_at desc limit $5`, spaces, s["space_id"], s["stage"], s["contact_id"], lim, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deal{}
	for rows.Next() {
		v, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
