package leads

// Email-to-ticket: one signed JSON post per inbound email. The sender's address is matched to a CRM contact (or a
// contact is created, like the public form does); a reply threads onto its ticket by In-Reply-To/References or a
// TKT-nnnnnn tag in the subject, and only when it comes from that ticket's contact. Text only: HTML is never read.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/cnote/api/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrBadSignature covers every authentication failure (unknown workspace, email-in off, bad, missing or stale signature)
// so the endpoint does not reveal which one it was.
var ErrBadSignature = errors.New("bad signature")

// MaxMailBody is the largest accepted request body (attachments are not part of the shape, so this is generous).
const MaxMailBody = 256 << 10

const sigWindow = 5 * time.Minute

// Sign is the signature a sender (or a provider adapter) computes: hex HMAC-SHA256 over "<unix seconds>.<body>".
func Sign(secret, ts string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts + "."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

type MailResult struct {
	TicketID  string `json:"ticket_id"`
	Ref       string `json:"ref"`
	Created   bool   `json:"created"`
	Duplicate bool   `json:"duplicate"`
}

type inboundMail struct {
	From       string          `json:"from"`
	To         string          `json:"to"`
	Subject    string          `json:"subject"`
	Text       string          `json:"text"`
	MessageID  string          `json:"message_id"`
	InReplyTo  string          `json:"in_reply_to"`
	References json.RawMessage `json:"references"` // a string or a list of strings
}

func ids(s string) []string {
	var out []string
	for _, f := range strings.Fields(strings.NewReplacer("<", " ", ">", " ", ",", " ").Replace(s)) {
		out = append(out, f)
	}
	return out
}

func clean(s string, max int) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
	r := []rune(strings.TrimSpace(s))
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}

var quoteHead = regexp.MustCompile(`(?i)^(-{2,}\s*(original message|forwarded message)\s*-{2,}|_{8,}|on .{5,200} wrote:|from:\s.*<.+@.+>.*)$`)

// stripQuoted drops the quoted history under a reply: from the first "On ... wrote:" / "Original Message" / "> " line
// down. shortcut: English markers only; when nothing is left it keeps the whole text.
func stripQuoted(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		wrapped := i+1 < len(lines) && strings.HasPrefix(strings.ToLower(t), "on ") && strings.HasSuffix(strings.TrimSpace(lines[i+1]), "wrote:")
		if strings.HasPrefix(t, ">") || quoteHead.MatchString(t) || wrapped {
			if kept := strings.TrimSpace(strings.Join(lines[:i], "\n")); kept != "" {
				return kept
			}
			break
		}
	}
	return strings.TrimSpace(text)
}

// ReceiveEmail verifies the signature and files the email, all in one tenant transaction.
func ReceiveEmail(ctx context.Context, pool *pgxpool.Pool, ws, ts, sig string, body []byte) (MailResult, error) {
	var res MailResult
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return res, ErrBadSignature
	}
	if d := time.Since(time.Unix(sec, 0)); d > sigWindow || d < -sigWindow {
		return res, ErrBadSignature
	}
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var blob []byte
	var space, owner *string
	err = tx.QueryRow(ctx, `select inbound_secret, inbound_space_id::text, inbound_owner_id::text from crm.ticket_channels where workspace_id=$1::uuid and inbound_secret is not null`, ws).Scan(&blob, &space, &owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && space == nil) {
		return res, ErrBadSignature
	}
	if err != nil {
		return res, command.DB(err)
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return res, err
	}
	secret, err := secretbox.Decrypt(key, ws, blob)
	if err != nil {
		return res, err
	}
	want, _ := hex.DecodeString(Sign(secret, ts, body))
	got, e := hex.DecodeString(strings.TrimSpace(sig))
	if e != nil || !hmac.Equal(want, got) {
		return res, ErrBadSignature
	}

	var in inboundMail
	if json.Unmarshal(body, &in) != nil {
		return res, command.ErrInvalid("invalid JSON")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(in.From))
	if err != nil || len(addr.Address) > 200 {
		return res, command.ErrInvalid("from must be an email address")
	}
	sender := strings.ToLower(addr.Address)
	mid := strings.Trim(strings.TrimSpace(in.MessageID), "<>")
	if mid == "" || len(mid) > 998 {
		return res, command.ErrInvalid("message_id is required")
	}
	refs := ids(in.InReplyTo)
	var asList []string
	if json.Unmarshal(in.References, &asList) == nil {
		for _, r := range asList {
			refs = append(refs, ids(r)...)
		}
	} else {
		var asStr string
		_ = json.Unmarshal(in.References, &asStr)
		refs = append(refs, ids(asStr)...)
	}
	if len(refs) > 50 {
		refs = refs[:50]
	}
	text := clean(stripQuoted(clean(in.Text, 100000)), 20000)
	if text == "" {
		text = "(no text)"
	}
	subject := clean(strings.NewReplacer("\r", " ", "\n", " ").Replace(in.Subject), 200)

	c := &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, UserID: owner, Role: command.Admin}}
	// A redelivery (or a racing one) waits here, then sees the first one's message row.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, ws+"|"+mid); err != nil {
		return res, command.DB(err)
	}
	var dupe *string
	var dupeNo int
	err = tx.QueryRow(ctx, `select m.ticket_id::text, t.number from crm.ticket_messages m join crm.tickets t on t.note_id=m.ticket_id where m.message_id=$1`, mid).Scan(&dupe, &dupeNo)
	if err == nil {
		return MailResult{TicketID: *dupe, Ref: Ref(dupeNo), Duplicate: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return res, command.DB(err)
	}

	ticket, number, status, err := threadOf(c, sender, refs)
	if err != nil {
		return res, err
	}
	if ticket == "" {
		contact, err := contactByEmail(c, *space, sender, addr.Name)
		if err != nil {
			return res, err
		}
		title := subject
		if title == "" {
			title = "(no subject)"
		}
		if ticket, err = newTicket(c, *space, contact, title, "", "normal", "email", owner, nil, nil); err != nil {
			return res, err
		}
		if err := tx.QueryRow(ctx, `select number from crm.tickets where note_id=$1::uuid`, ticket).Scan(&number); err != nil {
			return res, command.DB(err)
		}
		res.Created = true
	} else if status == "resolved" || status == "closed" { // the customer wrote again
		if _, err := tx.Exec(ctx, `update crm.tickets set status='open', resolved_at=null where note_id=$1::uuid`, ticket); err != nil {
			return res, command.DB(err)
		}
	}
	if _, err := tx.Exec(ctx, `insert into crm.ticket_messages(workspace_id,ticket_id,origin,body,message_id) values($1::uuid,$2::uuid,'customer_email',$3,$4)`, ws, ticket, text, mid); err != nil {
		return res, command.DB(err)
	}
	if !res.Created {
		if _, err := c.Emit("ticket.customer_replied", &ticket, map[string]any{"via": "email"}); err != nil {
			return res, err
		}
	}
	res.TicketID, res.Ref = ticket, Ref(number)
	return res, command.DB(tx.Commit(ctx))
}

// threadOf finds the live ticket this email continues by a Message-ID it quotes. The sender must be that ticket's
// contact. The subject's TKT tag is deliberately not used: From is forgeable and the tag is guessable.
func threadOf(c *command.Ctx, sender string, refs []string) (id string, number int, status string, err error) {
	const q = `select t.note_id::text, t.number, t.status from crm.tickets t join core.notes n on n.id=t.note_id join crm.contacts ct on ct.note_id=t.contact_id
		where n.deleted_at is null and lower(ct.email)=$1 and `
	scan := func(row pgx.Row) error {
		e := row.Scan(&id, &number, &status)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return command.DB(e)
	}
	if len(refs) > 0 {
		if err = scan(c.Tx.QueryRow(c.Context, q+`t.note_id = (select ticket_id from crm.ticket_messages where message_id = any($2) order by created_at desc limit 1)`, sender, refs)); err != nil || id != "" {
			return
		}
	}
	return
}

// contactByEmail is the contact with this address (oldest first), or a new person in the inbound space.
func contactByEmail(c *command.Ctx, space, email, name string) (string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context, `select ct.note_id::text from crm.contacts ct join core.notes n on n.id=ct.note_id
		where n.deleted_at is null and lower(ct.email)=$1 order by n.created_at limit 1`, email).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", command.DB(err)
	}
	name = clean(name, 100)
	if name == "" {
		name = clean(strings.SplitN(email, "@", 2)[0], 100)
	}
	n, err := notes.InsertTypedNote(c, space, name, "contact", nil)
	if err != nil {
		return "", err
	}
	if _, err := c.Tx.Exec(c.Context, `insert into crm.contacts(note_id,workspace_id,kind,email) values($1::uuid,$2::uuid,'person',$3)`, n.ID, c.WorkspaceID(), email); err != nil {
		return "", command.DB(err)
	}
	_, err = c.Emit("contact.created", &n.ID, map[string]any{"name": name, "via": "email"})
	return n.ID, err
}
