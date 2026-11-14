// Package recurring implements Go-only recurring invoice templates and generation.
package recurring

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	out := []command.Command{}
	for _, x := range []struct {
		name          string
		h             command.Handler
		read, confirm bool
	}{
		{"create", create, false, true}, {"update", update, false, true}, {"get", get, true, false}, {"list", list, true, false},
		{"pause", transition("paused"), false, false}, {"resume", transition("active"), false, true}, {"end", transition("ended"), false, false}, {"run_now", runNow, false, true},
	} {
		g := command.GroupManageInvoices
		if x.read {
			g = command.GroupRead
		}
		c := command.NewCommand("recurring."+x.name, g, x.h)
		c.Confirm = x.confirm
		out = append(out, c)
	}
	return out
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type input struct {
	ID        string  `json:"id"`
	SpaceID   string  `json:"space_id"`
	ContactID *string `json:"contact_id"`
	Frequency *string `json:"frequency"`
	Interval  *int    `json:"interval"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	AutoIssue *bool   `json:"auto_issue"`
}

func decode(raw json.RawMessage) (input, error) {
	var v input
	if json.Unmarshal(raw, &v) != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, s := range []string{v.ID, v.SpaceID} {
		if s != "" && !uuid.MatchString(s) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	if v.ContactID != nil && !uuid.MatchString(*v.ContactID) {
		return v, command.ErrInvalid("invalid contact_id")
	}
	for _, s := range []*string{v.StartDate, v.EndDate} {
		if s != nil {
			if _, e := time.Parse(time.DateOnly, *s); e != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
		}
	}
	if v.Frequency != nil && *v.Frequency != "monthly" && *v.Frequency != "quarterly" && *v.Frequency != "yearly" {
		return v, command.ErrInvalid("invalid frequency")
	}
	if v.Interval != nil && (*v.Interval < 1 || *v.Interval > 120) {
		return v, command.ErrInvalid("interval must be 1 through 120")
	}
	return v, nil
}

type schedule struct {
	ID        string           `json:"id"`
	SpaceID   string           `json:"space_id"`
	ContactID string           `json:"contact_id"`
	Frequency string           `json:"frequency"`
	Interval  int              `json:"interval"`
	Start     string           `json:"start_date"`
	Next      string           `json:"next_run"`
	End       *string          `json:"end_date"`
	Auto      bool             `json:"auto_issue"`
	Status    string           `json:"status"`
	Last      *string          `json:"last_invoice_id"`
	Creator   string           `json:"created_by"`
	Lines     []map[string]any `json:"lines"`
}

const cols = `r.note_id::text,n.space_id::text,r.contact_id::text,r.frequency,r.interval,r.start_date::text,r.next_run::text,r.end_date::text,r.auto_issue,r.status,r.last_invoice_id::text,r.created_by::text`

func scan(row pgx.Row) (schedule, error) {
	var s schedule
	err := row.Scan(&s.ID, &s.SpaceID, &s.ContactID, &s.Frequency, &s.Interval, &s.Start, &s.Next, &s.End, &s.Auto, &s.Status, &s.Last, &s.Creator)
	s.Lines = []map[string]any{}
	return s, err
}
func load(c *command.Ctx, id string, lock bool) (schedule, error) {
	q := `select ` + cols + ` from billing.recurring_invoices r join core.notes n on n.id=r.note_id where r.note_id=$1::uuid and n.deleted_at is null`
	if lock {
		q += ` for update of r`
	}
	s, e := scan(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return s, command.ErrNotFound("recurring invoice")
	}
	return s, command.DB(e)
}
func checked(c *command.Ctx, raw json.RawMessage, write bool) (schedule, error) {
	v, e := decode(raw)
	if e != nil {
		return schedule{}, e
	}
	if v.ID == "" {
		return schedule{}, command.ErrInvalid("id is required")
	}
	if _, e = access.AssertNoteAccess(c, v.ID, write); e != nil {
		return schedule{}, e
	}
	return load(c, v.ID, write)
}
func detail(c *command.Ctx, s schedule) (any, error) {
	rows, e := c.Tx.Query(c.Context, `select description,hsn_sac,quantity::text,unit_price_minor,gst_rate_bp,item_note_id::text,stock_item_id::text from billing.recurring_invoice_lines where recurring_invoice_id=$1::uuid order by position`, s.ID)
	if e != nil {
		return nil, command.DB(e)
	}
	defer rows.Close()
	for rows.Next() {
		var d, q string
		var h, i, st *string
		var p int64
		var g int32
		if e = rows.Scan(&d, &h, &q, &p, &g, &i, &st); e != nil {
			return nil, command.DB(e)
		}
		l := map[string]any{"description": d, "quantity": json.Number(q), "unit_price_minor": p, "gst_rate_bp": g}
		if h != nil {
			l["hsn_sac"] = *h
		}
		if i != nil {
			l["item_note_id"] = *i
		}
		if st != nil {
			l["stock_item_id"] = *st
		}
		s.Lines = append(s.Lines, l)
	}
	return s, command.DB(rows.Err())
}
func get(c *command.Ctx, raw json.RawMessage) (any, error) {
	s, e := checked(c, raw, false)
	if e != nil {
		return nil, e
	}
	return detail(c, s)
}
func list(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, e := decode(raw); e != nil {
		return nil, e
	}
	sp, e := access.ReadableSpaceIDs(c)
	if e != nil {
		return nil, e
	}
	u, a := access.Viewer(c)
	rows, e := c.Tx.Query(c.Context, `select `+cols+` from billing.recurring_invoices r join core.notes n on n.id=r.note_id where n.deleted_at is null and core.note_access(n.id,$1::uuid,$2,$3::text[]::uuid[]) <> 'none' order by r.next_run,r.note_id limit 500`, u, a, sp)
	if e != nil {
		return nil, command.DB(e)
	}
	defer rows.Close()
	out := []schedule{}
	for rows.Next() {
		s, e := scan(rows)
		if e != nil {
			return nil, command.DB(e)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}
func validLines(raw json.RawMessage, required bool) (billing.Lines, bool, error) {
	l, h, e := billing.DecodeLines(raw, "lines", required)
	if errors.Is(e, command.ErrFallback) {
		e = command.ErrInvalid("invalid lines")
	}
	return l, h, e
}
func create(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, e := decode(raw)
	if e != nil {
		return nil, e
	}
	if v.SpaceID == "" || v.ContactID == nil || v.StartDate == nil || v.Frequency == nil || c.Principal.UserID == nil {
		return nil, command.ErrInvalid("space_id, contact_id, start_date, frequency and creator are required")
	}
	if v.EndDate != nil && *v.EndDate < *v.StartDate {
		return nil, command.ErrInvalid("end_date precedes start_date")
	}
	l, _, e := validLines(raw, true)
	if e != nil {
		return nil, e
	}
	name, _, _, e := billing.LoadContact(c, *v.ContactID)
	if e != nil {
		return nil, e
	}
	n, e := notes.InsertNote(c, v.SpaceID, "Recurring invoice for "+name, nil, nil, []string{"recurring_invoice"})
	if e != nil {
		return nil, e
	}
	_, e = c.Tx.Exec(c.Context, `insert into billing.recurring_invoices(note_id,workspace_id,contact_id,frequency,interval,start_date,next_run,end_date,auto_issue,created_by) values($1::uuid,$2::uuid,$3::uuid,$4,coalesce($5,1),$6::date,$6::date,$7::date,coalesce($8,false),$9::uuid)`, n.ID, c.WorkspaceID(), v.ContactID, v.Frequency, v.Interval, v.StartDate, v.EndDate, v.AutoIssue, c.Principal.UserID)
	if e != nil {
		return nil, command.DB(e)
	}
	if e = billing.WriteRecurringLines(c, n.ID, l); e != nil {
		return nil, e
	}
	if _, e = c.Emit("recurring.created", &n.ID, map[string]any{"contact_id": v.ContactID}); e != nil {
		return nil, e
	}
	return get(c, mustJSON(map[string]any{"id": n.ID}))
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func update(c *command.Ctx, raw json.RawMessage) (any, error) {
	s, e := checked(c, raw, true)
	if e != nil {
		return nil, e
	}
	if s.Status == "ended" {
		return nil, command.ErrConflict("schedule has ended")
	}
	v, _ := decode(raw)
	// Changing the anchor after generation could replay already billed periods.
	if (v.StartDate != nil || v.Frequency != nil || v.Interval != nil) && s.Last != nil {
		return nil, command.ErrConflict("create a new schedule to change cadence after generation")
	}
	start := s.Start
	if v.StartDate != nil {
		start = *v.StartDate
	}
	end := s.End
	if v.EndDate != nil {
		end = v.EndDate
	}
	if end != nil && *end < start {
		return nil, command.ErrInvalid("end_date precedes start_date")
	}
	if v.ContactID != nil {
		if _, _, _, e = billing.LoadContact(c, *v.ContactID); e != nil {
			return nil, e
		}
	}
	l, have, e := validLines(raw, false)
	if e != nil {
		return nil, e
	}
	_, e = c.Tx.Exec(c.Context, `update billing.recurring_invoices set contact_id=coalesce($2::uuid,contact_id),frequency=coalesce($3,frequency),interval=coalesce($4,interval),start_date=coalesce($5::date,start_date),next_run=coalesce($5::date,next_run),end_date=coalesce($6::date,end_date),auto_issue=coalesce($7,auto_issue) where note_id=$1::uuid`, s.ID, v.ContactID, v.Frequency, v.Interval, v.StartDate, v.EndDate, v.AutoIssue)
	if e != nil {
		return nil, command.DB(e)
	}
	if have {
		if e = billing.WriteRecurringLines(c, s.ID, l); e != nil {
			return nil, e
		}
	}
	return get(c, raw)
}
func transition(to string) command.Handler {
	return func(c *command.Ctx, raw json.RawMessage) (any, error) {
		s, e := checked(c, raw, true)
		if e != nil {
			return nil, e
		}
		if s.Status == "ended" || s.Status == to {
			return nil, command.ErrConflict("invalid schedule transition")
		}
		_, e = c.Tx.Exec(c.Context, `update billing.recurring_invoices set status=$2 where note_id=$1::uuid`, s.ID, to)
		if e != nil {
			return nil, command.DB(e)
		}
		kind := map[string]string{"paused": "recurring.paused", "active": "recurring.resumed", "ended": "recurring.ended"}[to]
		if _, e = c.Emit(kind, &s.ID, map[string]any{}); e != nil {
			return nil, e
		}
		return get(c, raw)
	}
}

// NextDate advances from the current period while retaining the original anchor day.
func NextDate(start, current time.Time, frequency string, interval int) time.Time {
	months := interval
	switch frequency {
	case "quarterly":
		months *= 3
	case "yearly":
		months *= 12
	}
	first := time.Date(current.Year(), current.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	day := min(start.Day(), last)
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}
func generate(c *command.Ctx, s schedule, date string) (string, error) {
	d, e := detail(c, s)
	if e != nil {
		return "", e
	}
	s = d.(schedule)
	out, e := c.Call("invoice.create_draft", map[string]any{"space_id": s.SpaceID, "contact_id": s.ContactID, "invoice_date": date, "lines": s.Lines})
	if e != nil {
		return "", e
	}
	var inv struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &inv) != nil || inv.ID == "" {
		return "", command.NewError(command.Internal, "invoice draft returned no id")
	}
	if s.Auto {
		out, e = c.Call("invoice.issue", map[string]any{"id": inv.ID})
		if e != nil {
			return "", e
		}
		var conf struct {
			Required bool   `json:"confirmation_required"`
			ID       string `json:"confirmation_id"`
		}
		_ = json.Unmarshal(out, &conf)
		if conf.Required {
			// Preserve the draft and approval request for machine-triggered runs.
			// Scheduled system runs issue directly on behalf of the creator.
			if _, e = c.Emit("recurring.issue_confirmation_required", &s.ID, map[string]any{"invoice_id": inv.ID, "confirmation_id": conf.ID}); e != nil {
				return "", e
			}
		}
	}
	if _, e = c.Emit("recurring.generated", &s.ID, map[string]any{"invoice_id": inv.ID, "run_date": date, "auto_issue": s.Auto}); e != nil {
		return "", e
	}
	return inv.ID, nil
}

// runNow consumes one scheduled period; future periods cannot be billed early.
func runNow(c *command.Ctx, raw json.RawMessage) (any, error) {
	s, e := checked(c, raw, true)
	if e != nil {
		return nil, e
	}
	return runPeriod(c, s, billing.TodayIST().Format(time.DateOnly))
}
func runPeriod(c *command.Ctx, s schedule, today string) (any, error) {
	if s.Status != "active" {
		return nil, command.ErrConflict("schedule is not active")
	}
	if s.Next > today {
		return nil, command.ErrConflict("next period is not due")
	}
	if s.End != nil && s.Next > *s.End {
		return finish(c, s)
	}
	id, e := generate(c, s, s.Next)
	if e != nil {
		return nil, e
	}
	start, _ := time.Parse(time.DateOnly, s.Start)
	cur, _ := time.Parse(time.DateOnly, s.Next)
	next := NextDate(start, cur, s.Frequency, s.Interval).Format(time.DateOnly)
	_, e = c.Tx.Exec(c.Context, `update billing.recurring_invoices set next_run=$2::date,last_invoice_id=$3::uuid where note_id=$1::uuid`, s.ID, next, id)
	if e != nil {
		return nil, command.DB(e)
	}
	s.Next = next
	s.Last = &id
	if s.End != nil && next > *s.End {
		return finish(c, s)
	}
	return detail(c, s)
}
func finish(c *command.Ctx, s schedule) (any, error) {
	_, e := c.Tx.Exec(c.Context, `update billing.recurring_invoices set status='ended' where note_id=$1::uuid`, s.ID)
	if e != nil {
		return nil, command.DB(e)
	}
	if _, e = c.Emit("recurring.ended", &s.ID, map[string]any{}); e != nil {
		return nil, e
	}
	s.Status = "ended"
	return detail(c, s)
}
