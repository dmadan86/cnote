// Package publiclinks: share a quotation or invoice as an unguessable link the customer opens without logging in
// (share.* commands for staff, plus the public HTTP handler in public.go). Go-only: Rust has no share.* commands.
//
// The token is 32 random bytes (base64url) shown once in the URL; only its sha256 is stored. Unknown, revoked and
// expired tokens all answer the same 404. A link is owned by the member who created it: customer actions run as a
// System principal on that member's behalf, with their current role and page access, so removing the member kills
// their links.
package publiclinks

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func Commands() []command.Command {
	sm := command.GroupSendMessages
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	nc := command.NewCommand
	return []command.Command{
		confirm(secret(nc("share.create", sm, create))),
		nc("share.list", command.GroupRead, list),
		nc("share.revoke", command.GroupWriteNotes, revoke),
		confirm(nc("share.email", sm, email)),
	}
}

func secret(c command.Command) command.Command { c.ReturnsSecret = true; return c }

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// newToken returns a fresh token and its stored hash.
func newToken() (token, hash string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, hashToken(token), nil
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

type in struct {
	NoteID        string  `json:"note_id"`
	ID            string  `json:"id"`
	ExpiresInDays *int    `json:"expires_in_days"`
	Origin        *string `json:"origin"`
	Phone         *string `json:"phone"`
	To            string  `json:"to"`
	Message       *string `json:"message"`
	Reminder      bool    `json:"reminder"` // overdue-invoice wording instead of the share template
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, id := range []string{v.NoteID, v.ID} {
		if id != "" && !uuidRe.MatchString(id) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	v.NoteID, v.ID = strings.ToLower(v.NoteID), strings.ToLower(v.ID)
	if v.Message != nil && len(*v.Message) > 2000 {
		return v, command.ErrInvalid("message is too long")
	}
	return v, nil
}

// doc is what a link is about: the facts the share message and the permission checks need.
type doc struct {
	kind, number, status, currency string
	total, due                     int64
	contactName, phone             string
	purpose                        string
}

// loadDoc accepts issued invoices and sent-or-later quotations only; drafts, cancelled invoices and credit notes are not shareable.
func loadDoc(c *command.Ctx, noteID string) (doc, error) {
	var d doc
	err := c.Tx.QueryRow(c.Context, `
		select 'quotation', coalesce(q.number,''), q.status, q.currency::text, q.total_minor, 0::bigint,
		       coalesce(cn.title,''), coalesce(ct.phone,'')
		from billing.quotations q left join core.notes cn on cn.id = q.contact_id left join crm.contacts ct on ct.note_id = q.contact_id
		where q.note_id = $1::uuid
		union all
		select 'invoice', coalesce(i.number,''), i.status, i.currency::text, i.total_minor,
		       i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor,
		       coalesce(cn.title,''), coalesce(ct.phone,'')
		from billing.invoices i left join core.notes cn on cn.id = i.contact_id left join crm.contacts ct on ct.note_id = i.contact_id
		where i.note_id = $1::uuid and i.kind = 'invoice'
		union all
		select 'receipt', r.number, r.status, 'INR', r.amount_minor, 0::bigint, coalesce(cn.title,''), coalesce(ct.phone,'')
		from billing.receipts r left join core.notes cn on cn.id = r.contact_id left join crm.contacts ct on ct.note_id = r.contact_id
		where r.note_id = $1::uuid and r.kind = 'receipt'`, noteID).
		Scan(&d.kind, &d.number, &d.status, &d.currency, &d.total, &d.due, &d.contactName, &d.phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, command.ErrInvalid("only quotations, invoices and receipts can be shared")
	}
	if err != nil {
		return d, command.DB(err)
	}
	if d.status == "draft" || d.status == "cancelled" {
		return d, command.ErrConflict(fmt.Sprintf("a %s %s cannot be shared", d.status, d.kind))
	}
	if d.kind != "quotation" { // invoices and receipts are finance data: no public link around finance_access
		if ok, err := c.FinanceAllowed(); err != nil {
			return d, err
		} else if !ok {
			return d, command.ErrForbidden("finance access is off for you in this workspace")
		}
	}
	d.purpose = "view"
	if d.kind == "quotation" {
		d.purpose = "quote_accept"
	}
	return d, nil
}

type made struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"`
	Path      string     `json:"path"`
	URL       string     `json:"url"`
	Purpose   string     `json:"purpose"`
	ExpiresAt command.Ts `json:"expires_at"`
	WhatsApp  string     `json:"whatsapp_url"`
	Message   string     `json:"message"`
	d         doc
}

func baseURL(origin *string) (string, error) {
	if origin == nil || *origin == "" {
		for _, k := range []string{"PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"} {
			if v := getenv(k); v != "" {
				origin = &v
				break
			}
		}
	}
	if origin == nil {
		d := "http://localhost:3000"
		origin = &d
	}
	u, err := url.Parse(strings.TrimRight(*origin, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", command.ErrInvalid("origin must look like https://example.com")
	}
	return u.String(), nil
}

// issue creates the link row and the share events. The token never reaches the event log or the idempotency table.
func issue(c *command.Ctx, v in) (made, error) {
	var m made
	if c.Principal.UserID == nil {
		return m, command.ErrForbidden("a share link needs a person to own it")
	}
	if v.NoteID == "" {
		return m, command.ErrInvalid("note_id is required")
	}
	days := 30
	if v.ExpiresInDays != nil {
		days = *v.ExpiresInDays
	}
	if days < 1 || days > 365 {
		return m, command.ErrInvalid("expires_in_days must be between 1 and 365")
	}
	base, err := baseURL(v.Origin)
	if err != nil {
		return m, err
	}
	if _, err := access.AssertNoteAccess(c, v.NoteID, true); err != nil {
		return m, err
	}
	d, err := loadDoc(c, v.NoteID)
	if err != nil {
		return m, err
	}
	token, hash, err := newToken()
	if err != nil {
		return m, command.NewError(command.Internal, "could not create a token")
	}
	var exp time.Time
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.public_links (workspace_id, note_id, token_hash, purpose, expires_at, created_by)
		 values ($1::uuid,$2::uuid,$3,$4, now() + make_interval(days => $5), $6::uuid) returning id::text, expires_at`,
		c.WorkspaceID(), v.NoteID, hash, d.purpose, days, *c.Principal.UserID).Scan(&m.ID, &exp); err != nil {
		return m, command.DB(err)
	}
	if _, err := c.Emit("share.created", &v.NoteID, map[string]any{"link_id": m.ID, "purpose": d.purpose, "expires_at": command.Ts(exp)}); err != nil {
		return m, err
	}
	m.Token, m.Path, m.Purpose, m.ExpiresAt, m.d = token, "/p/"+token, d.purpose, command.Ts(exp), d
	m.URL = base + m.Path
	if m.Message, err = shareMessage(c, d, m.URL, v.Reminder); err != nil {
		return m, err
	}
	phone := d.phone
	if v.Phone != nil {
		phone = *v.Phone
	}
	m.WhatsApp = "https://wa.me/" + waNumber(phone) + "?text=" + strings.ReplaceAll(url.QueryEscape(m.Message), "+", "%20")
	return m, nil
}

func create(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	return issue(c, v)
}

var digitsRe = regexp.MustCompile(`\D`)

// waNumber is the digits wa.me wants: country code first, no plus. A bare 10-digit number is taken as Indian.
func waNumber(p string) string {
	d := digitsRe.ReplaceAllString(p, "")
	d = strings.TrimLeft(d, "0")
	if len(d) == 10 {
		d = "91" + d
	}
	if len(d) < 8 || len(d) > 15 {
		return "" // no usable number: wa.me/?text= lets the sender pick the chat
	}
	return d
}

const reminderMessage = "Hello {name}, a gentle reminder: {doc} {number} for {amount} from {seller} is pending. You can view and pay it here: {link}"

const defaultMessage = "Hello {name}, here is your {doc} {number} for {amount} from {seller}: {link}"

// shareMessage fills the workspace's settings.share_message (placeholders {name} {doc} {number} {amount} {seller} {link}).
func shareMessage(c *command.Ctx, d doc, link string, reminder bool) (string, error) {
	var settings []byte
	var wsName string
	if err := c.Tx.QueryRow(c.Context, "select name, settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&wsName, &settings); err != nil {
		return "", command.DB(err)
	}
	var s struct {
		Message   string `json:"share_message"`
		LegalName string `json:"legal_name"`
	}
	_ = json.Unmarshal(settings, &s)
	tpl := s.Message
	if strings.TrimSpace(tpl) == "" || len(tpl) > 1000 {
		tpl = defaultMessage
	}
	if reminder {
		tpl = reminderMessage
	}
	seller := wsName
	if s.LegalName != "" {
		seller = s.LegalName
	}
	name := d.contactName
	if name == "" {
		name = "there"
	}
	return strings.NewReplacer("{name}", name, "{doc}", d.kind, "{number}", d.number, "{amount}", money(d.currency, d.due, d.total, d.kind),
		"{seller}", seller, "{link}", link).Replace(tpl), nil
}

// money: the amount still due for an invoice, the total for a quotation.
func money(cur string, due, total int64, kind string) string {
	v := total
	if kind == "invoice" {
		v = due
	}
	sym := cur + " "
	if cur == "INR" {
		sym = "Rs "
	}
	return fmt.Sprintf("%s%d.%02d", sym, v/100, v%100)
}

// ---------- list / revoke ----------

type linkOut struct {
	ID           string      `json:"id"`
	NoteID       string      `json:"note_id"`
	Purpose      string      `json:"purpose"`
	Status       string      `json:"status"` // active | expired | revoked
	CreatedAt    command.Ts  `json:"created_at"`
	CreatedBy    *string     `json:"created_by_name"`
	ExpiresAt    *command.Ts `json:"expires_at"`
	RevokedAt    *command.Ts `json:"revoked_at"`
	ViewCount    int32       `json:"view_count"`
	LastViewedAt *command.Ts `json:"last_viewed_at"`
}

const linkCols = `l.id::text, l.note_id::text, l.purpose, case when l.revoked_at is not null then 'revoked'
	when l.expires_at is not null and l.expires_at <= now() then 'expired' else 'active' end, l.created_at, u.name,
	l.expires_at, l.revoked_at, l.view_count, l.last_viewed_at`

func scanLink(r pgx.Row) (linkOut, error) {
	var o linkOut
	var created time.Time
	var exp, rev, seen *time.Time
	err := r.Scan(&o.ID, &o.NoteID, &o.Purpose, &o.Status, &created, &o.CreatedBy, &exp, &rev, &o.ViewCount, &seen)
	o.CreatedAt, o.ExpiresAt, o.RevokedAt, o.LastViewedAt = command.Ts(created), command.TsPtr(exp), command.TsPtr(rev), command.TsPtr(seen)
	return o, err
}

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.NoteID == "" {
		return nil, command.ErrInvalid("note_id is required")
	}
	if _, err := access.AssertNoteAccess(c, v.NoteID, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select "+linkCols+` from core.public_links l join core.users u on u.id = l.created_by
		where l.note_id = $1::uuid order by l.created_at desc limit 100`, v.NoteID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []linkOut{}
	for rows.Next() {
		o, err := scanLink(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, o)
	}
	return out, command.DB(rows.Err())
}

func revoke(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	var noteID string
	var already bool
	if err := c.Tx.QueryRow(c.Context, "select note_id::text, revoked_at is not null from core.public_links where id = $1::uuid for update", v.ID).Scan(&noteID, &already); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("link")
		}
		return nil, command.DB(err)
	}
	if _, err := access.AssertNoteAccess(c, noteID, true); err != nil {
		return nil, err
	}
	if !already { // a second revoke is a no-op, not a second event
		if _, err := c.Tx.Exec(c.Context, "update core.public_links set revoked_at = now() where id = $1::uuid", v.ID); err != nil {
			return nil, command.DB(err)
		}
		if _, err := c.Emit("share.revoked", &noteID, map[string]any{"link_id": v.ID}); err != nil {
			return nil, err
		}
	}
	return scanLink(c.Tx.QueryRow(c.Context, "select "+linkCols+" from core.public_links l join core.users u on u.id = l.created_by where l.id = $1::uuid", v.ID))
}

// ---------- email ----------

func email(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	addr, err := mail.ParseAddress(v.To)
	if err != nil || addr.Address != strings.TrimSpace(v.To) || strings.ContainsAny(v.To, "\r\n") {
		return nil, command.ErrInvalid("to must be a plain email address")
	}
	smtpURL, from := getenv("SMTP_URL"), getenv("EMAIL_FROM")
	if smtpURL == "" || from == "" {
		return nil, command.ErrInvalid("email is not configured on this server (SMTP_URL, EMAIL_FROM)")
	}
	m, err := issue(c, v)
	if err != nil {
		return nil, err
	}
	body := m.Message
	if v.Message != nil && strings.TrimSpace(*v.Message) != "" {
		body = *v.Message + "\n\n" + m.URL
	}
	subject := fmt.Sprintf("%s %s", strings.ToUpper(m.d.kind[:1])+m.d.kind[1:], m.d.number)
	if err := sendMail(smtpURL, from, addr.Address, subject, body+"\n"); err != nil {
		return nil, command.NewError(command.Internal, "the email could not be sent; no link was created")
	}
	if _, err := c.Emit("share.emailed", &v.NoteID, map[string]any{"link_id": m.ID, "to_domain": addr.Address[strings.LastIndex(addr.Address, "@")+1:]}); err != nil {
		return nil, err
	}
	return map[string]any{"sent": true, "link_id": m.ID, "expires_at": m.ExpiresAt}, nil
}
