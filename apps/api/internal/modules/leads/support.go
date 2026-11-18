package leads

// Support v2 (WP20): the customer-visible reference, agent replies that are emailed to the customer, and the
// per-workspace email-in / portal switches. Internal notes stay ordinary note comments (core.comments); only rows in
// crm.ticket_messages ever reach a customer, by email or on the portal.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/modules/emailtpl"
	"github.com/cnote/api/internal/secretbox"
	"github.com/jackc/pgx/v5"
)

// Ref is the number a customer quotes, e.g. TKT-000123 (per workspace; see the tickets_number trigger).
func Ref(n int) string { return fmt.Sprintf("TKT-%06d", n) }

func supportCommands() []command.Command {
	adm := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	reply := command.NewCommand("ticket.reply", command.GroupSendMessages, ticketReply)
	reply.Confirm = true // it emails a customer
	rotate := adm(command.NewCommand("ticket_inbound.rotate", command.GroupAdmin, inboundRotate))
	rotate.ReturnsSecret, rotate.Confirm = true, true
	return []command.Command{
		reply,
		command.NewCommand("ticket.messages", command.GroupRead, ticketMessages),
		adm(command.NewCommand("ticket_channels.get", command.GroupAdmin, channelsGet)),
		adm(command.NewCommand("ticket_channels.set", command.GroupAdmin, channelsSet)),
		rotate,
	}
}

func env(k string) string { return strings.TrimSpace(os.Getenv(k)) }

func webBase() string {
	for _, k := range []string{"PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"} {
		if v := env(k); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://localhost:3000"
}

// mailRendered sends a branded message; (false, nil) means SMTP is not configured. It returns the Message-ID it set.
func mailRendered(c *command.Ctx, to, event string, values map[string]string, o *emailtpl.Override) (sent bool, messageID string, err error) {
	if !mailer.Configured() {
		return false, "", nil
	}
	r, err := emailtpl.Render(c, event, values, o)
	if err != nil {
		return false, "", err
	}
	messageID, err = mailer.Send(c.Context, mailer.Mail{To: to, ReplyTo: r.ReplyTo, FromName: r.FromName, Subject: r.Subject, Text: r.Text, HTML: r.HTML})
	return err == nil, messageID, err
}

func ticketReply(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	body, err := m.req("body", 10000)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	var number int
	var title string
	var email *string
	var contactName string
	err = c.Tx.QueryRow(c.Context, `select t.number, n.title, ct.email, coalesce(cn.title,'') from crm.tickets t join core.notes n on n.id=t.note_id
		join crm.contacts ct on ct.note_id=t.contact_id join core.notes cn on cn.id=ct.note_id where t.note_id=$1::uuid and n.deleted_at is null`, id).Scan(&number, &title, &email, &contactName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("ticket")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if email == nil || *email == "" {
		return nil, command.ErrInvalid("the customer has no email address; add one to the contact first")
	}
	ref := Ref(number)
	text := body + "\n\n--\n" + ref + ": reply to this email to add to the ticket."
	if on, _ := portalOn(c); on {
		text += "\nView all your tickets: " + webBase() + "/portal/" + c.WorkspaceID()
	}
	// The email goes first so a failed send rolls the message back instead of leaving a reply nobody received.
	sent, msgID, err := mailRendered(c, *email, "ticket.reply", map[string]string{"ticket.ref": ref, "ticket.subject": title, "ticket.message": text, "contact.name": contactName}, nil)
	if err != nil {
		slog.Warn("ticket reply email failed", "error", err)
		return nil, command.ErrInvalid("the email could not be sent, so the reply was not saved; try again")
	}
	var mid string
	var at time.Time
	var sentID *string // the customer's reply quotes it, which is how mailin threads the reply into this ticket
	if msgID != "" {
		t := strings.Trim(msgID, "<>")
		sentID = &t
	}
	if err := c.Tx.QueryRow(c.Context, `insert into crm.ticket_messages(workspace_id,ticket_id,origin,body,author_id,emailed,message_id)
		values($1::uuid,$2::uuid,'agent',$3,$4::uuid,$5,$6) returning id::text, created_at`, c.WorkspaceID(), id, body, c.Principal.UserID, sent, sentID).Scan(&mid, &at); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("ticket.replied", &id, map[string]any{"message_id": mid, "emailed": sent}); err != nil {
		return nil, err
	}
	return map[string]any{"id": mid, "ref": ref, "emailed": sent, "created_at": command.Ts(at)}, nil
}

func portalOn(c *command.Ctx) (bool, error) {
	var on bool
	err := c.Tx.QueryRow(c.Context, `select portal_enabled from crm.ticket_channels where workspace_id=$1::uuid`, c.WorkspaceID()).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return on, err
}

func ticketMessages(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select m.id::text, m.origin, m.body, coalesce(u.name,''), m.emailed, m.created_at
		from crm.ticket_messages m left join core.users u on u.id=m.author_id where m.ticket_id=$1::uuid order by m.created_at, m.id`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mid, origin, body, author string
		var emailed bool
		var at time.Time
		if err := rows.Scan(&mid, &origin, &body, &author, &emailed, &at); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": mid, "origin": origin, "body": body, "author_name": author, "emailed": emailed, "created_at": command.Ts(at)})
	}
	return out, command.DB(rows.Err())
}

func channelsGet(c *command.Ctx, _ json.RawMessage) (any, error) {
	var portal bool
	var space *string
	var rotated *time.Time
	var inbound bool
	err := c.Tx.QueryRow(c.Context, `select portal_enabled, inbound_secret is not null, inbound_space_id::text, inbound_rotated_at from crm.ticket_channels where workspace_id=$1::uuid`,
		c.WorkspaceID()).Scan(&portal, &inbound, &space, &rotated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	return map[string]any{"portal_enabled": portal, "inbound_enabled": inbound, "inbound_space_id": space, "inbound_rotated_at": command.TsPtr(rotated),
		"inbound_path": "/api/public/ticket-mail/" + c.WorkspaceID(), "portal_path": "/portal/" + c.WorkspaceID()}, nil
}

func channelsSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		Portal  *bool `json:"portal_enabled"`
		Inbound *bool `json:"inbound_enabled"`
	}
	if json.Unmarshal(raw, &in) != nil || (in.Portal == nil && in.Inbound == nil) {
		return nil, command.ErrInvalid("give portal_enabled and/or inbound_enabled")
	}
	if in.Inbound != nil && *in.Inbound {
		return nil, command.ErrInvalid("use ticket_inbound.rotate to turn email-in on; it creates the signing secret")
	}
	if _, err := c.Tx.Exec(c.Context, `insert into crm.ticket_channels(workspace_id,portal_enabled) values($1::uuid, coalesce($2,false))
		on conflict (workspace_id) do update set portal_enabled=coalesce($2, crm.ticket_channels.portal_enabled),
		inbound_secret=case when $3 then null else crm.ticket_channels.inbound_secret end`, c.WorkspaceID(), in.Portal, in.Inbound != nil); err != nil {
		return nil, command.DB(err)
	}
	return channelsGet(c, nil)
}

// inboundRotate creates or replaces the signing secret and returns it once; the old one stops working at once.
func inboundRotate(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	space, hasSpace, err := m.optID("space_id")
	if err != nil {
		return nil, err
	}
	if hasSpace && space != nil {
		if err := access.AssertSpaceAccess(c, *space, true); err != nil {
			return nil, err
		}
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return nil, err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, command.NewError(command.Internal, "random failed")
	}
	secret := hex.EncodeToString(b[:])
	blob, err := secretbox.Encrypt(key, c.WorkspaceID(), secret)
	if err != nil {
		return nil, err
	}
	var cur *string
	_ = c.Tx.QueryRow(c.Context, `select inbound_space_id::text from crm.ticket_channels where workspace_id=$1::uuid`, c.WorkspaceID()).Scan(&cur)
	if space == nil {
		space = cur
	}
	if space == nil {
		return nil, command.ErrInvalid("space_id is required the first time: the space new email tickets are filed in")
	}
	if _, err := c.Tx.Exec(c.Context, `insert into crm.ticket_channels(workspace_id,inbound_secret,inbound_space_id,inbound_owner_id,inbound_rotated_at)
		values($1::uuid,$2,$3::uuid,$4::uuid,now()) on conflict (workspace_id) do update set inbound_secret=$2, inbound_space_id=$3::uuid,
		inbound_owner_id=$4::uuid, inbound_rotated_at=now()`, c.WorkspaceID(), blob, *space, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("ticket_inbound.rotated", nil, map[string]any{}); err != nil {
		return nil, err
	}
	return map[string]any{"secret": secret, "inbound_path": "/api/public/ticket-mail/" + c.WorkspaceID(), "note": "Shown once. Sign each request: hex HMAC-SHA256 of \"<timestamp>.<body>\" in X-Cnote-Signature, unix seconds in X-Cnote-Timestamp."}, nil
}
