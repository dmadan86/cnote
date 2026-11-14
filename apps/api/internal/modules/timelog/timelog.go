// Package timelog: time entries on work items and billing from them (WP9). Go-only.
// Hours are billed per project space (core.spaces.config.billing: contact, hourly rate, SAC). Billing milestones live
// in milestones.go. Money amounts are paise; a milestone or time line is a taxable value, GST is added by the invoice.
package timelog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
)

func Commands() []command.Command {
	mi, wi, rd := command.GroupManageInvoices, command.GroupWriteItems, command.GroupRead
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c }
	nc := command.NewCommand
	return []command.Command{
		nc("time.log", wi, log),
		nc("time.update", wi, update),
		nc("time.delete", wi, remove),
		member(nc("time.list", rd, list)),
		nc("time.invoice", mi, invoice),
		nc("project.billing_set", mi, billingSet),
		member(nc("project.billing_get", rd, billingGet)),
		nc("milestone.set_billing", mi, setBilling),
		nc("milestone.invoice", mi, milestoneInvoice),
		member(nc("milestone.list", rd, milestoneList)),
		member(nc("report.project_money", rd, projectMoney)),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type in struct {
	ID            string  `json:"id"`
	ItemID        string  `json:"item_id"`
	SpaceID       string  `json:"space_id"`
	UserID        string  `json:"user_id"`
	VersionID     string  `json:"version_id"`
	QuotationID   *string `json:"quotation_id"`
	ContactID     *string `json:"contact_id"`
	WorkedOn      *string `json:"worked_on"`
	Minutes       *int    `json:"minutes"`
	Note          *string `json:"note"`
	Billable      *bool   `json:"billable"`
	From          *string `json:"from"`
	To            *string `json:"to"`
	ToDate        *string `json:"to_date"`
	InvoiceDate   *string `json:"invoice_date"`
	GroupBy       *string `json:"group_by"`
	Unbilled      *bool   `json:"unbilled"`
	Limit         *int    `json:"limit"`
	HourlyRate    *int64  `json:"hourly_rate_minor"`
	HSNSAC        *string `json:"hsn_sac"`
	GSTRateBP     *int    `json:"gst_rate_bp"`
	BillAmount    *int64  `json:"bill_amount_minor"`
	BillPercentBP *int    `json:"bill_percent_bp"`
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, s := range []string{v.ID, v.ItemID, v.SpaceID, v.UserID, v.VersionID} {
		if s != "" && !uuidRe.MatchString(s) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	for _, s := range []*string{v.QuotationID, v.ContactID} {
		if s != nil && !uuidRe.MatchString(*s) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	for _, d := range []*string{v.WorkedOn, v.From, v.To, v.ToDate, v.InvoiceDate} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
		}
	}
	v.ID, v.ItemID, v.SpaceID, v.UserID, v.VersionID = strings.ToLower(v.ID), strings.ToLower(v.ItemID), strings.ToLower(v.SpaceID), strings.ToLower(v.UserID), strings.ToLower(v.VersionID)
	return v, nil
}

func need(name, v string) error {
	if v == "" {
		return command.ErrInvalid(name + " is required")
	}
	return nil
}

func person(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil {
		return "", command.ErrForbidden("time is logged by people, not agents")
	}
	return *c.Principal.UserID, nil
}

// ---------- entries ----------

const entryCols = `e.id::text, e.item_id::text, pi.key, n.title, n.space_id::text, e.user_id::text, u.name, e.worked_on::text,
	e.minutes, e.note, e.billable, e.invoice_id::text`

func scanEntry(r pgx.Row) (map[string]any, error) {
	var id, item, key, title, space, user, uname, day string
	var minutes int
	var note, inv *string
	var billable bool
	if err := r.Scan(&id, &item, &key, &title, &space, &user, &uname, &day, &minutes, &note, &billable, &inv); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "item_id": item, "item_key": key, "item_title": title, "space_id": space, "user_id": user,
		"user_name": uname, "worked_on": day, "minutes": minutes, "note": note, "billable": billable, "invoice_id": inv}, nil
}

const entryFrom = `from projects.time_entries e join core.notes n on n.id = e.item_id join projects.items pi on pi.note_id = e.item_id
	join core.users u on u.id = e.user_id`

func entry(c *command.Ctx, id string) (map[string]any, error) {
	m, err := scanEntry(c.Tx.QueryRow(c.Context, "select "+entryCols+" "+entryFrom+" where e.id = $1::uuid", id))
	if err == pgx.ErrNoRows {
		return nil, command.ErrNotFound("time entry")
	}
	return m, command.DB(err)
}

// dayCap keeps one person's day at 24 h; the advisory lock serialises two concurrent logs for the same day.
func dayCap(c *command.Ctx, user, day string, add int, except string) error {
	if _, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtext($1))", c.WorkspaceID()+user+day); err != nil {
		return command.DB(err)
	}
	var total int
	if err := c.Tx.QueryRow(c.Context,
		"select coalesce(sum(minutes),0) from projects.time_entries where user_id = $1::uuid and worked_on = $2::date and ($3 = '' or id <> nullif($3,'')::uuid)",
		user, day, except).Scan(&total); err != nil {
		return command.DB(err)
	}
	if total+add > 1440 {
		return command.ErrInvalid(fmt.Sprintf("that is more than 24 hours on %s (%d min already logged)", day, total))
	}
	return nil
}

func log(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	user, err := person(c)
	if err != nil {
		return nil, err
	}
	if err := need("item_id", v.ItemID); err != nil {
		return nil, err
	}
	if v.Minutes == nil || *v.Minutes < 1 || *v.Minutes > 1440 {
		return nil, command.ErrInvalid("minutes must be between 1 and 1440")
	}
	var space string
	if err := c.Tx.QueryRow(c.Context,
		"select i.space_id::text from projects.items i join core.notes n on n.id = i.note_id where i.note_id = $1::uuid and n.deleted_at is null", v.ItemID).Scan(&space); err != nil {
		if err == pgx.ErrNoRows {
			return nil, command.ErrNotFound("item")
		}
		return nil, command.DB(err)
	}
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		return nil, err
	}
	day := billing.TodayIST().Format("2006-01-02")
	if v.WorkedOn != nil {
		day = *v.WorkedOn
	}
	if err := dayCap(c, user, day, *v.Minutes, ""); err != nil {
		return nil, err
	}
	billable := v.Billable == nil || *v.Billable
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into projects.time_entries (workspace_id, item_id, user_id, worked_on, minutes, note, billable)
		 values ($1::uuid,$2::uuid,$3::uuid,$4::date,$5,nullif(btrim($6::text),''),$7) returning id::text`,
		c.WorkspaceID(), v.ItemID, user, day, *v.Minutes, deref(v.Note), billable).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("time.logged", &v.ItemID, map[string]any{"entry_id": id, "minutes": *v.Minutes, "worked_on": day, "billable": billable}); err != nil {
		return nil, err
	}
	return entry(c, id)
}

// own loads an entry for change: only its author (or an admin) may touch it, and a billed entry is frozen.
func own(c *command.Ctx, v in) (map[string]any, error) {
	if err := need("id", v.ID); err != nil {
		return nil, err
	}
	if _, err := person(c); err != nil {
		return nil, err
	}
	var user string
	var billed bool
	if err := c.Tx.QueryRow(c.Context, "select user_id::text, invoice_id is not null from projects.time_entries where id = $1::uuid for update", v.ID).Scan(&user, &billed); err != nil {
		if err == pgx.ErrNoRows {
			return nil, command.ErrNotFound("time entry")
		}
		return nil, command.DB(err)
	}
	if user != *c.Principal.UserID && c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("only the person who logged this time (or an admin) can change it")
	}
	if billed {
		return nil, command.ErrConflict("this time is already on an invoice")
	}
	e, err := entry(c, v.ID)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, e["space_id"].(string), true); err != nil {
		return nil, err
	}
	return e, nil
}

func update(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	e, err := own(c, v)
	if err != nil {
		return nil, err
	}
	minutes, day := e["minutes"].(int), e["worked_on"].(string)
	if v.Minutes != nil {
		if *v.Minutes < 1 || *v.Minutes > 1440 {
			return nil, command.ErrInvalid("minutes must be between 1 and 1440")
		}
		minutes = *v.Minutes
	}
	if v.WorkedOn != nil {
		day = *v.WorkedOn
	}
	if err := dayCap(c, e["user_id"].(string), day, minutes, v.ID); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update projects.time_entries set minutes = $2, worked_on = $3::date, note = case when $4::text is null then note else nullif(btrim($4::text),'') end,
		   billable = coalesce($5, billable) where id = $1::uuid`, v.ID, minutes, day, v.Note, v.Billable); err != nil {
		return nil, command.DB(err)
	}
	item := e["item_id"].(string)
	if _, err := c.Emit("time.updated", &item, map[string]any{"entry_id": v.ID, "minutes": minutes, "worked_on": day}); err != nil {
		return nil, err
	}
	return entry(c, v.ID)
}

func remove(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	e, err := own(c, v)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "delete from projects.time_entries where id = $1::uuid", v.ID); err != nil {
		return nil, command.DB(err)
	}
	item := e["item_id"].(string)
	if _, err := c.Emit("time.deleted", &item, map[string]any{"entry_id": v.ID, "minutes": e["minutes"]}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func list(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	limit := 500
	if v.Limit != nil && *v.Limit > 0 && *v.Limit < 2000 {
		limit = *v.Limit
	}
	rows, err := c.Tx.Query(c.Context,
		"select "+entryCols+" "+entryFrom+` where n.deleted_at is null and n.space_id = any($1::text[]::uuid[])
		   and ($2 = '' or n.space_id = nullif($2,'')::uuid) and ($3 = '' or e.item_id = nullif($3,'')::uuid)
		   and ($4 = '' or e.user_id = nullif($4,'')::uuid) and ($5::date is null or e.worked_on >= $5::date)
		   and ($6::date is null or e.worked_on <= $6::date) and (not coalesce($7, false) or (e.invoice_id is null and e.billable))
		 order by e.worked_on desc, e.created_at desc limit $8`,
		spaces, v.SpaceID, v.ItemID, v.UserID, v.From, v.To, v.Unbilled, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	var total, billable, unbilled int
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		m := e["minutes"].(int)
		total += m
		if e["billable"].(bool) {
			billable += m
			if e["invoice_id"] == (*string)(nil) {
				unbilled += m
			}
		}
		out = append(out, e)
	}
	return map[string]any{"entries": out, "totals": map[string]any{"minutes": total, "billable_minutes": billable, "unbilled_minutes": unbilled}}, command.DB(rows.Err())
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
