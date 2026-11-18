package leads

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/modules/emailtpl"
	"github.com/jackc/pgx/v5"
)

func contactEmailCommand() command.Command {
	c := command.NewCommand("contact.email", command.GroupSendMessages, contactEmail)
	c.Confirm = true // it emails a customer
	return c
}

// contactEmail sends a free-form reply to a contact's address in the workspace's branded layout and logs it as an
// email activity (on the deal when given, which also counts as the first response).
func contactEmail(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("contact_id")
	if err != nil {
		return nil, err
	}
	subject, err := m.req("subject", 200)
	if err != nil {
		return nil, err
	}
	body, err := m.req("body", 10000)
	if err != nil {
		return nil, err
	}
	logOn := id
	if s, ok, e := m.str("deal_id", 36); e != nil {
		return nil, e
	} else if ok {
		if !uuidRe.MatchString(s) {
			return nil, command.ErrInvalid("deal_id must be a UUID")
		}
		logOn = s
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	var email *string
	var name string
	err = c.Tx.QueryRow(c.Context, `select ct.email, n.title from crm.contacts ct join core.notes n on n.id=ct.note_id
		where ct.note_id=$1::uuid and n.deleted_at is null`, id).Scan(&email, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("contact")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if email == nil || *email == "" {
		return nil, command.ErrInvalid("the contact has no email address; add one first")
	}
	if logOn != id { // the deal must belong to this contact
		var ok bool
		if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from crm.deals where note_id=$1::uuid and contact_id=$2::uuid)`, logOn, id).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrInvalid("deal_id is not a deal of this contact")
		}
	}
	if !mailer.Configured() {
		return nil, command.ErrInvalid("email is not set up for this workspace, so nothing was sent")
	}
	// Fixed wording in the branded layout (the ticket.reply event's sign-off and footer); the body is exactly what was approved.
	r, err := emailtpl.Render(c, "ticket.reply", map[string]string{"ticket.subject": subject, "ticket.message": body, "contact.name": name},
		&emailtpl.Override{Subject: "{{ticket.subject}}", Body: "{{ticket.message}}"})
	if err != nil {
		return nil, err
	}
	if _, err := mailer.Send(c.Context, mailer.Mail{To: *email, ReplyTo: r.ReplyTo, FromName: r.FromName, Subject: r.Subject, Text: r.Text, HTML: r.HTML}); err != nil {
		slog.Warn("contact email failed", "error", err)
		return nil, command.ErrInvalid("the email could not be sent; try again")
	}
	log, _ := json.Marshal(map[string]string{"note_id": logOn, "kind": "email", "summary": "Emailed: " + subject})
	if _, err := activityLog(c, log); err != nil {
		return nil, err
	}
	if _, err := c.Emit("contact.emailed", &id, map[string]any{"deal_id": logOn}); err != nil {
		return nil, err
	}
	return map[string]any{"contact_id": id, "to": *email, "sent": true}, nil
}
