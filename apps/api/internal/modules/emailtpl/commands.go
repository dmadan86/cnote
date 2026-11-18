package emailtpl

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
)

func Commands() []command.Command {
	mk := func(name string, g command.Group, min command.Role, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = min
		return c
	}
	return []command.Command{
		mk("email_template.list", command.GroupRead, command.Admin, list),
		mk("email_template.save", command.GroupAdmin, command.Admin, save),
		mk("email_template.reset", command.GroupAdmin, command.Admin, reset),
		mk("email_template.preview", command.GroupRead, command.Member, preview), // Admin without note_id: checked inside
		mk("email_template.test", command.GroupSendMessages, command.Admin, test),
	}
}

type input struct {
	Event   string  `json:"event"`
	Subject *string `json:"subject"`
	Body    *string `json:"body"`
	Version int     `json:"version"`
	NoteID  string  `json:"note_id"`
}

func decode(b json.RawMessage, needEvent bool) (input, EventInfo, error) {
	var in input
	if json.Unmarshal(b, &in) != nil {
		return in, EventInfo{}, command.ErrInvalid("input must be a JSON object")
	}
	ev, ok := eventOf(in.Event)
	if !ok && (needEvent || in.Event != "") {
		return in, ev, command.ErrInvalid("unknown message " + in.Event)
	}
	return in, ev, nil
}

func list(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, _, err := decode(b, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select event, coalesce(subject,''), coalesce(body,''), customised, version from core.email_templates where workspace_id=$1::uuid", c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	type row struct {
		subject, body string
		customised    bool
		version       int
	}
	have := map[string]row{}
	for rows.Next() {
		var e string
		var r row
		if err := rows.Scan(&e, &r.subject, &r.body, &r.customised, &r.version); err != nil {
			return nil, command.DB(err)
		}
		have[e] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	out := []map[string]any{}
	for _, ev := range events {
		r := have[ev.Event]
		custom := r.customised
		if !custom {
			r.subject, r.body = ev.DefaultSubject, ev.DefaultBody
		}
		out = append(out, map[string]any{"event": ev.Event, "label": ev.Label, "group": ev.Group, "sent_when": ev.SentWhen, "link_key": ev.LinkKey,
			"subject": r.subject, "body": r.body, "default_subject": ev.DefaultSubject, "default_body": ev.DefaultBody,
			"customised": custom, "version": r.version, "fields": ev.Fields})
	}
	br, err := loadBrand(c, "")
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id=$1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return nil, command.DB(err)
	}
	s := map[string]any{}
	_ = json.Unmarshal(raw, &s)
	get := func(k string) string { v, _ := s[k].(string); return v }
	return map[string]any{"events": out, "sender": map[string]any{"from_name": get("email_from_name"), "reply_to": get("email_reply_to"),
		"accent": get("email_accent"), "signature": get("email_signature"), "footer": get("email_footer"),
		"effective_from_name": br.FromName, "effective_reply_to": br.ReplyTo, "effective_accent": br.Accent}}, nil
}

func save(c *command.Ctx, b json.RawMessage) (any, error) {
	in, ev, err := decode(b, true)
	if err != nil {
		return nil, err
	}
	if in.Subject == nil || in.Body == nil {
		return nil, command.ErrInvalid("subject and body are required")
	}
	subject, body := strings.TrimSpace(*in.Subject), strings.TrimSpace(*in.Body)
	if err := checkTemplate(ev, subject, body); err != nil {
		return nil, err
	}
	conflict := command.ErrConflict("someone else changed this message; reload and try again")
	var version int
	if in.Version == 0 { // first customisation
		err = c.Tx.QueryRow(c.Context, `insert into core.email_templates (workspace_id, event, subject, body, version, updated_by)
			values ($1::uuid,$2,$3,$4,1,$5::uuid) on conflict do nothing returning version`, c.WorkspaceID(), ev.Event, subject, body, c.Principal.UserID).Scan(&version)
	} else {
		err = c.Tx.QueryRow(c.Context, `update core.email_templates set subject=$3, body=$4, customised=true, version=version+1, updated_by=$5::uuid, updated_at=now()
			where workspace_id=$1::uuid and event=$2 and version=$6 returning version`, c.WorkspaceID(), ev.Event, subject, body, c.Principal.UserID, in.Version).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, conflict
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("email_template.saved", nil, map[string]any{"event": ev.Event}); err != nil {
		return nil, err
	}
	return map[string]any{"template": map[string]any{"event": ev.Event, "subject": subject, "body": body, "customised": true, "version": version}}, nil
}

func reset(c *command.Ctx, b json.RawMessage) (any, error) {
	in, ev, err := decode(b, true)
	if err != nil {
		return nil, err
	}
	var cur int
	var custom bool
	err = c.Tx.QueryRow(c.Context, "select version, customised from core.email_templates where workspace_id=$1::uuid and event=$2 for update", c.WorkspaceID(), ev.Event).Scan(&cur, &custom)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"ok": true}, nil // already the default
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if cur != in.Version {
		return nil, command.ErrConflict("someone else changed this message; reload and try again")
	}
	if !custom {
		return map[string]any{"ok": true}, nil
	}
	// keep the row: the version only ever goes up
	if _, err := c.Tx.Exec(c.Context, "update core.email_templates set subject=null, body=null, customised=false, version=version+1, updated_at=now() where workspace_id=$1::uuid and event=$2", c.WorkspaceID(), ev.Event); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("email_template.reset", nil, map[string]any{"event": ev.Event}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func kindOf(ev EventInfo) string {
	switch {
	case strings.HasPrefix(ev.Event, "invoice."):
		return "invoice"
	case strings.HasPrefix(ev.Event, "quote."):
		return "quote"
	case strings.HasPrefix(ev.Event, "receipt."):
		return "receipt"
	}
	return ""
}

func load(c *command.Ctx, kind, id string) (map[string]string, string, error) {
	switch kind {
	case "invoice":
		return invoiceValues(c, id)
	case "quote":
		return quoteValues(c, id)
	}
	return receiptValues(c, id)
}

// pick is the latest record of the kind the caller can read: readable spaces, core.note_access, and finance access
// for invoices and receipts. "" when there is none.
func pick(c *command.Ctx, kind string) (string, error) {
	if kind == "invoice" || kind == "invoice_overdue" || kind == "receipt" {
		if ok, err := c.FinanceAllowed(); err != nil || !ok {
			return "", err
		}
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return "", err
	}
	uid, admin := access.Viewer(c)
	from := map[string]string{
		"invoice": "billing.invoices r join core.notes n on n.id = r.note_id where r.kind = 'invoice' and r.number is not null order by r.invoice_date desc nulls last, r.note_id desc",
		// reminders preview best on an invoice that is actually overdue and unpaid
		"invoice_overdue": "billing.invoices r join core.notes n on n.id = r.note_id where r.kind = 'invoice' and r.number is not null and r.due_date < (now() at time zone 'Asia/Kolkata')::date and r.total_minor - r.paid_minor - r.credited_minor - r.written_off_minor > 0 order by r.due_date desc, r.note_id desc",
		"quote":           "billing.quotations r join core.notes n on n.id = r.note_id where r.number is not null order by r.quote_date desc nulls last, r.note_id desc",
		"receipt":         "billing.receipts r join core.notes n on n.id = r.note_id where r.kind = 'receipt' order by r.received_at desc, r.note_id desc",
	}[kind]
	// the scope predicate goes before "order by"
	i := strings.Index(from, " order by ")
	q := `select r.note_id::text from ` + from[:i] + ` and n.deleted_at is null and n.space_id = any($1::text[]::uuid[])
		and core.note_access(n.id, $2::uuid, $3, $1::text[]::uuid[]) <> 'none'` + from[i:] + " limit 1"
	var id string
	err = c.Tx.QueryRow(c.Context, q, spaces, uid, admin).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, command.DB(err)
}

// previewData is the record's values (explicit note, else the latest readable one, else the examples) with a sample link.
func previewData(c *command.Ctx, ev EventInfo, noteID string) (vals map[string]string, label string, sample bool, err error) {
	kind := kindOf(ev)
	if noteID != "" {
		if kind == "" {
			return nil, "", false, command.ErrInvalid("this message has no record to preview")
		}
		if _, err := access.AssertNoteAccess(c, noteID, false); err != nil {
			return nil, "", false, err
		}
		if kind != "quote" {
			if ok, err := c.FinanceAllowed(); err != nil {
				return nil, "", false, err
			} else if !ok {
				return nil, "", false, command.ErrForbidden("finance access is off for you in this workspace")
			}
		}
	} else if kind != "" {
		if strings.HasPrefix(ev.Event, "invoice.reminder") {
			if noteID, err = pick(c, "invoice_overdue"); err != nil {
				return nil, "", false, err
			}
		}
		if noteID == "" {
			if noteID, err = pick(c, kind); err != nil {
				return nil, "", false, err
			}
		}
	}
	if noteID == "" {
		return sampleValues(ev), "Sample " + strings.ToLower(ev.Label), true, nil
	}
	if vals, label, err = load(c, kind, noteID); err != nil {
		return nil, "", false, err
	}
	if ev.LinkKey != "" {
		vals[ev.LinkKey] = sampleLink()
	}
	return vals, label, false, nil
}

func preview(c *command.Ctx, b json.RawMessage) (any, error) {
	in, ev, err := decode(b, true)
	if err != nil {
		return nil, err
	}
	if in.NoteID == "" && c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("email_template.preview requires role admin")
	}
	vals, label, sample, err := previewData(c, ev, in.NoteID)
	if err != nil {
		return nil, err
	}
	o, err := override(ev, in)
	if err != nil {
		return nil, err
	}
	r, err := Render(c, ev.Event, vals, o)
	if err != nil {
		return nil, err
	}
	return map[string]any{"subject": r.Subject, "text": r.Text, "html": r.HTML, "sample": sample, "record_label": label,
		"from_name": r.FromName, "reply_to": r.ReplyTo, "link_note": "(link created when you send)"}, nil
}

func override(ev EventInfo, in input) (*Override, error) {
	if in.Subject == nil && in.Body == nil {
		return nil, nil
	}
	o := &Override{}
	if in.Subject != nil {
		o.Subject = strings.TrimSpace(*in.Subject)
	}
	if in.Body != nil {
		o.Body = strings.TrimSpace(*in.Body)
	}
	subj, body := o.Subject, o.Body
	if subj == "" {
		subj = ev.DefaultSubject
	}
	if body == "" {
		body = ev.DefaultBody
	}
	return o, checkTemplate(ev, subj, body)
}

func test(c *command.Ctx, b json.RawMessage) (any, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return nil, command.ErrForbidden("only people can do this, not API keys or agents")
	}
	in, ev, err := decode(b, true)
	if err != nil {
		return nil, err
	}
	// 5 test sends per 5 minutes per person
	if err := command.CheckRateWith("email_template.test:"+*c.Principal.UserID, 5, 5.0/300); err != nil {
		return nil, err
	}
	o, err := override(ev, in)
	if err != nil {
		return nil, err
	}
	var to string
	if err := c.Tx.QueryRow(c.Context, "select email from core.users where id=$1::uuid", *c.Principal.UserID).Scan(&to); err != nil {
		return nil, command.DB(err)
	}
	vals, _, _, err := previewData(c, ev, "")
	if err != nil {
		return nil, err
	}
	r, err := Render(c, ev.Event, vals, o)
	if err != nil {
		return nil, err
	}
	if _, err := mailer.Send(c.Context, mailer.Mail{To: to, ReplyTo: r.ReplyTo, FromName: r.FromName, Subject: "[Test] " + r.Subject, Text: r.Text, HTML: r.HTML}); err != nil {
		return nil, command.ErrInvalid("could not send the test email: " + err.Error())
	}
	return map[string]any{"sent_to": to}, nil
}
