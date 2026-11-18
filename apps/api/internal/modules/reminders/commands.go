package reminders

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
)

func Commands() []command.Command {
	mk := func(name string, g command.Group, min command.Role, fin bool, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole, c.Finance = min, fin
		return c
	}
	return []command.Command{
		mk("reminder.settings_get", command.GroupRead, command.Admin, false, settingsGet),
		mk("reminder.settings_set", command.GroupAdmin, command.Admin, false, settingsSet),
		mk("reminder.pause", command.GroupManageInvoices, command.Member, true, pause),
		mk("reminder.log_list", command.GroupRead, command.Member, true, logList),
		mk("contact.reminders_opt_out", command.GroupManageCRM, command.Member, false, optOut),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Every sweep, hook and eligibility check uses this one definition: the receivables ageing filter, plus a customer
// email and no pause or opt-out. Credit notes are never targeted. Due falls back to the invoice date.
const eligibleFrom = `billing.invoices i join core.notes n on n.id = i.note_id join crm.contacts ct on ct.note_id = i.contact_id
	where i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid')
	  and i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor > 0 and n.deleted_at is null
	  and coalesce(ct.email,'') <> '' and coalesce(i.due_date, i.invoice_date) is not null
	  and not exists (select 1 from billing.reminder_prefs p where p.paused and p.note_id in (i.note_id, i.contact_id))`

func eligible(c *command.Ctx, invoiceID string) (bool, error) {
	var ok bool
	err := c.Tx.QueryRow(c.Context, `select exists (select 1 from `+eligibleFrom+` and i.note_id = $1::uuid)`, invoiceID).Scan(&ok)
	return ok, command.DB(err)
}

// cancelQueued cancels the queued reminders of an invoice (note is the invoice) or of all invoices of a customer
// (note is the contact). ifIneligible: only the ones that no longer qualify.
func cancelQueued(c *command.Ctx, note, reason string, ifIneligible bool) error {
	q := `update billing.reminder_log l set status = 'cancelled', error = $2 where l.status = 'queued'
		and exists (select 1 from billing.invoices i where i.note_id = l.invoice_note_id and (i.note_id = $1::uuid or i.contact_id = $1::uuid))`
	if ifIneligible {
		q += ` and not exists (select 1 from ` + eligibleFrom + ` and i.note_id = l.invoice_note_id)`
	}
	_, err := c.Tx.Exec(c.Context, q, note, reason)
	return command.DB(err)
}

// OnEvent cancels queued reminders when a payment, credit note, write-off or cancellation makes an invoice ineligible.
func OnEvent(c *command.Ctx, e jobs.Event) error {
	if e.NoteID == nil || e.Kind == "invoice.reminder_sent" {
		return nil
	}
	return cancelQueued(c, *e.NoteID, "no longer owed", true)
}

func idOf(b json.RawMessage, keys ...string) (id string, flag bool, err error) {
	m := map[string]json.RawMessage{}
	if json.Unmarshal(b, &m) != nil {
		return "", false, command.ErrInvalid("input must be a JSON object")
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			_ = json.Unmarshal(v, &id)
			break
		}
	}
	if !uuidRe.MatchString(id) {
		return "", false, command.ErrInvalid("id must be a UUID")
	}
	for _, k := range []string{"paused", "opt_out"} {
		if v, ok := m[k]; ok && json.Unmarshal(v, &flag) != nil {
			return "", false, command.ErrInvalid(k + " must be true or false")
		}
	}
	return strings.ToLower(id), flag, nil
}

func setPref(c *command.Ctx, note string, on bool) error {
	var err error
	if on {
		_, err = c.Tx.Exec(c.Context, `insert into billing.reminder_prefs (workspace_id, note_id, paused) values ($1::uuid,$2::uuid,true) on conflict (workspace_id, note_id) do update set paused = true`, c.WorkspaceID(), note)
	} else {
		_, err = c.Tx.Exec(c.Context, `delete from billing.reminder_prefs where note_id = $1::uuid`, note)
	}
	if err != nil {
		return command.DB(err)
	}
	if on {
		return cancelQueued(c, note, "paused", false)
	}
	return nil
}

func isKind(c *command.Ctx, q, id string) (bool, error) {
	var ok bool
	err := c.Tx.QueryRow(c.Context, q, id).Scan(&ok)
	return ok, command.DB(err)
}

// pause {id: invoice note, paused}: stops (or resumes) the automatic reminders of one invoice.
func pause(c *command.Ctx, b json.RawMessage) (any, error) {
	id, on, err := idOf(b, "id", "note_id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if ok, err := isKind(c, `select exists (select 1 from billing.invoices where note_id = $1::uuid and kind = 'invoice')`, id); err != nil {
		return nil, err
	} else if !ok {
		return nil, command.ErrInvalid("only invoices can have reminders paused")
	}
	if err := setPref(c, id, on); err != nil {
		return nil, err
	}
	if _, err := c.Emit("reminder.paused", &id, map[string]any{"paused": on}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "paused": on}, nil
}

// optOut {id: contact note, opt_out}: no automatic reminders for any invoice of this customer.
func optOut(c *command.Ctx, b json.RawMessage) (any, error) {
	id, on, err := idOf(b, "id", "note_id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if ok, err := isKind(c, `select exists (select 1 from crm.contacts where note_id = $1::uuid)`, id); err != nil {
		return nil, err
	} else if !ok {
		return nil, command.ErrInvalid("that is not a contact")
	}
	if err := setPref(c, id, on); err != nil {
		return nil, err
	}
	if _, err := c.Emit("reminder.opted_out", &id, map[string]any{"opt_out": on}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "no_reminders": on}, nil
}

func istDay(t time.Time) string { return t.In(ist).Format(time.DateOnly) }

// logList {invoice_id}: {paused, contact_opted_out, next:{label,date}|null, items:[{step,label,status,at}]}.
// Without invoice_id: the workspace's recent reminders, newest first, for the pages the caller can read.
func logList(c *command.Ctx, b json.RawMessage) (any, error) {
	var in struct {
		InvoiceID string `json:"invoice_id"`
		Limit     int    `json:"limit"`
	}
	if json.Unmarshal(b, &in) != nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	s, err := loadSettings(c)
	if err != nil {
		return nil, err
	}
	if in.InvoiceID == "" {
		return logAll(c, s, in.Limit)
	}
	id := strings.ToLower(in.InvoiceID)
	if !uuidRe.MatchString(id) {
		return nil, command.ErrInvalid("invoice_id must be a UUID")
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	var due *time.Time
	err = c.Tx.QueryRow(c.Context, `select coalesce(due_date, invoice_date) from billing.invoices where note_id = $1::uuid and kind = 'invoice'`, id).Scan(&due)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrInvalid("that is not an invoice")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	var paused, optedOut bool
	if err := c.Tx.QueryRow(c.Context, `select
		exists (select 1 from billing.reminder_prefs where note_id = $1::uuid and paused),
		exists (select 1 from billing.reminder_prefs p join billing.invoices i on i.contact_id = p.note_id where i.note_id = $1::uuid and p.paused)`, id).Scan(&paused, &optedOut); err != nil {
		return nil, command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context, `select step, status, coalesce(sent_at, created_at) from billing.reminder_log where invoice_note_id = $1::uuid order by step`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	items := []map[string]any{}
	done := map[int]bool{}
	for rows.Next() {
		var step int
		var status string
		var at time.Time
		if err := rows.Scan(&step, &status, &at); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		done[step] = true
		items = append(items, map[string]any{"step": step, "label": s.Steps[step].label(), "status": status, "at": command.Ts(at)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var next any
	ok, err := eligible(c, id)
	if err != nil {
		return nil, err
	}
	if ok && s.Enabled && due != nil {
		now := time.Now()
		today := istDay(now)
		var ran bool
		if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from billing.reminder_runs where day = $1::date)`, today).Scan(&ran); err != nil {
			return nil, command.DB(err)
		}
		best := ""
		var bl string
		for i, st := range s.Steps {
			d := due.AddDate(0, 0, st.offset()).Format(time.DateOnly)
			if !st.Enabled || done[i] || d < today || d == today && ran {
				continue
			}
			if best == "" || d < best {
				best, bl = d, st.label()
			}
		}
		if best != "" {
			next = map[string]any{"label": bl, "date": best}
		}
	}
	return map[string]any{"paused": paused, "contact_opted_out": optedOut, "next": next, "items": items}, nil
}

func logAll(c *command.Ctx, s Settings, limit int) (any, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context, `select l.id::text, l.invoice_note_id::text, i.number, cn.title, l.step, l.status, coalesce(l.sent_at, l.created_at), l.to_email
		from billing.reminder_log l join billing.invoices i on i.note_id = l.invoice_note_id join core.notes n on n.id = i.note_id
		left join core.notes cn on cn.id = i.contact_id
		where n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id, $2::uuid, $3, $1::text[]::uuid[]) <> 'none'
		order by l.created_at desc, l.id limit $4`, spaces, uid, admin, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, inv, status, to string
		var number, contact *string
		var step int
		var at time.Time
		if err := rows.Scan(&id, &inv, &number, &contact, &step, &status, &at, &to); err != nil {
			return nil, command.DB(err)
		}
		items = append(items, map[string]any{"id": id, "invoice_id": inv, "number": number, "contact": contact, "step": step,
			"label": s.Steps[step].label(), "status": status, "at": command.Ts(at), "to_email": to})
	}
	return map[string]any{"items": items}, command.DB(rows.Err())
}
