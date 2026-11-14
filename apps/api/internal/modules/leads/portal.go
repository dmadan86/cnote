package leads

// Customer portal: no customer passwords. A contact asks for a sign-in link by email; the link is single use and
// lives 15 minutes, and trading it in starts a 12-hour session. Only the sha256 of either token is stored. The portal
// reads crm.ticket_messages and the ticket's public fields, never comments or SLA, assignee or priority.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPortalAuth: unknown, used, expired or other-workspace token, or the portal is off.
var ErrPortalAuth = errors.New("portal sign-in failed")

const (
	loginTTL    = 15 * time.Minute
	sessionTTL  = 12 * time.Hour
	loginPer15m = 3 // links per contact per 15 minutes
)

func newToken() (token, hash string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, hashToken(token), nil
}

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func portalTx(ctx context.Context, pool *pgxpool.Pool, ws string) (pgx.Tx, *command.Ctx, error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return nil, nil, err
	}
	return tx, &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Admin}}, nil
}

// PortalInfo is the workspace name for the sign-in page, when the portal is on.
func PortalInfo(ctx context.Context, pool *pgxpool.Pool, ws string) (string, error) {
	tx, _, err := portalTx(ctx, pool, ws)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var name string
	err = tx.QueryRow(ctx, `select w.name from core.workspaces w join crm.ticket_channels c on c.workspace_id=w.id where w.id=$1::uuid and c.portal_enabled`, ws).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPortalAuth
	}
	return name, command.DB(err)
}

// PortalRequestLink emails a sign-in link when the address belongs to a contact of this workspace. It answers the
// same way (nil) for an unknown address, a disabled portal and a rate-limited contact, so it cannot be used to
// find out who is a customer. The mail is sent in the background for the same reason (equal response time).
func PortalRequestLink(ctx context.Context, pool *pgxpool.Pool, ws, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > 200 {
		return nil
	}
	tx, _, err := portalTx(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var name, contact string
	err = tx.QueryRow(ctx, `select w.name, ct.note_id::text from core.workspaces w join crm.ticket_channels ch on ch.workspace_id=w.id and ch.portal_enabled
		join crm.contacts ct on ct.workspace_id=w.id join core.notes n on n.id=ct.note_id
		where w.id=$1::uuid and n.deleted_at is null and lower(ct.email)=$2 order by n.created_at limit 1`, ws, email).Scan(&name, &contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return command.DB(err)
	}
	var recent int
	if err := tx.QueryRow(ctx, `select count(*) from crm.portal_tokens where contact_id=$1::uuid and kind='login' and created_at > now() - interval '15 minutes'`, contact).Scan(&recent); err != nil {
		return command.DB(err)
	}
	if recent >= loginPer15m {
		return nil
	}
	token, hash, err := newToken()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into crm.portal_tokens(workspace_id,contact_id,kind,token_hash,expires_at) values($1::uuid,$2::uuid,'login',$3, now() + make_interval(secs => $4))`,
		ws, contact, hash, loginTTL.Seconds()); err != nil {
		return command.DB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return command.DB(err)
	}
	go func() {
		body := "Sign in to your tickets with " + name + ":\n\n" + webBase() + "/portal/" + ws + "/verify#" + token +
			"\n\nThis link works once and expires in 15 minutes. If you did not ask for it, ignore this email."
		if _, err := mailTo(email, "Your sign-in link for "+name, body); err != nil {
			slog.Warn("portal sign-in email failed", "error", err)
		}
	}()
	return nil
}

// PortalVerify trades a login link for a session token (once).
func PortalVerify(ctx context.Context, pool *pgxpool.Pool, ws, token string) (string, error) {
	tx, _, err := portalTx(ctx, pool, ws)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var contact string
	err = tx.QueryRow(ctx, `update crm.portal_tokens set used_at=now() where token_hash=$1 and workspace_id=$2::uuid and kind='login' and used_at is null and expires_at > now()
		and exists(select 1 from crm.ticket_channels where workspace_id=$2::uuid and portal_enabled) returning contact_id::text`, hashToken(token), ws).Scan(&contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPortalAuth
	}
	if err != nil {
		return "", command.DB(err)
	}
	session, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into crm.portal_tokens(workspace_id,contact_id,kind,token_hash,expires_at) values($1::uuid,$2::uuid,'session',$3, now() + make_interval(secs => $4))`,
		ws, contact, hash, sessionTTL.Seconds()); err != nil {
		return "", command.DB(err)
	}
	return session, command.DB(tx.Commit(ctx))
}

func sessionContact(c *command.Ctx, ws, session string) (string, error) {
	var contact string
	err := c.Tx.QueryRow(c.Context, `select t.contact_id::text from crm.portal_tokens t join crm.ticket_channels ch on ch.workspace_id=t.workspace_id and ch.portal_enabled
		where t.token_hash=$1 and t.workspace_id=$2::uuid and t.kind='session' and t.expires_at > now()`, hashToken(session), ws).Scan(&contact)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPortalAuth
	}
	return contact, command.DB(err)
}

type PortalMessage struct {
	From      string     `json:"from"` // "you" or "support": agents' names stay internal
	Body      string     `json:"body"`
	CreatedAt command.Ts `json:"created_at"`
}

type PortalTicket struct {
	Ref       string          `json:"ref"`
	Number    int             `json:"number"`
	Title     string          `json:"title"`
	Status    string          `json:"status"`
	CreatedAt command.Ts      `json:"created_at"`
	Messages  []PortalMessage `json:"messages"`
}

// PortalTickets lists the signed-in contact's tickets with their public messages, newest ticket first.
func PortalTickets(ctx context.Context, pool *pgxpool.Pool, ws, session string) (string, []PortalTicket, error) {
	tx, c, err := portalTx(ctx, pool, ws)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	contact, err := sessionContact(c, ws, session)
	if err != nil {
		return "", nil, err
	}
	var name string
	if err := tx.QueryRow(ctx, `select name from core.workspaces where id=$1::uuid`, ws).Scan(&name); err != nil {
		return "", nil, command.DB(err)
	}
	rows, err := tx.Query(ctx, `select t.note_id::text, t.number, n.title, t.status, t.created_at, t.description from crm.tickets t join core.notes n on n.id=t.note_id
		where t.contact_id=$1::uuid and n.deleted_at is null order by t.created_at desc limit 50`, contact)
	if err != nil {
		return "", nil, command.DB(err)
	}
	out, byID := []PortalTicket{}, map[string]int{}
	var order []string
	for rows.Next() {
		var id, desc string
		var p PortalTicket
		var at time.Time
		if err := rows.Scan(&id, &p.Number, &p.Title, &p.Status, &at, &desc); err != nil {
			rows.Close()
			return "", nil, command.DB(err)
		}
		p.Ref, p.CreatedAt, p.Messages = Ref(p.Number), command.Ts(at), []PortalMessage{}
		if desc != "" { // what the customer typed into the public form
			p.Messages = append(p.Messages, PortalMessage{From: "you", Body: desc, CreatedAt: p.CreatedAt})
		}
		byID[id] = len(out)
		order = append(order, id)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", nil, command.DB(err)
	}
	mr, err := tx.Query(ctx, `select ticket_id::text, origin, body, created_at from crm.ticket_messages where ticket_id = any($1::text[]::uuid[]) order by created_at, id`, order)
	if err != nil {
		return "", nil, command.DB(err)
	}
	defer mr.Close()
	for mr.Next() {
		var id, origin, body string
		var at time.Time
		if err := mr.Scan(&id, &origin, &body, &at); err != nil {
			return "", nil, command.DB(err)
		}
		from := "you"
		if origin == "agent" {
			from = "support"
		}
		i := byID[id]
		out[i].Messages = append(out[i].Messages, PortalMessage{From: from, Body: body, CreatedAt: command.Ts(at)})
	}
	return name, out, command.DB(mr.Err())
}

// PortalReply adds the customer's reply to one of their own tickets (by number) and reopens a resolved one.
func PortalReply(ctx context.Context, pool *pgxpool.Pool, ws, session string, number int, body string) error {
	body = clean(body, 5000)
	if body == "" {
		return command.ErrInvalid("write a reply first")
	}
	tx, c, err := portalTx(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	contact, err := sessionContact(c, ws, session)
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, `select t.note_id::text from crm.tickets t join core.notes n on n.id=t.note_id where t.contact_id=$1::uuid and t.number=$2 and n.deleted_at is null`, contact, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("ticket")
	}
	if err != nil {
		return command.DB(err)
	}
	if _, err := tx.Exec(ctx, `insert into crm.ticket_messages(workspace_id,ticket_id,origin,body) values($1::uuid,$2::uuid,'customer_portal',$3)`, ws, id, body); err != nil {
		return command.DB(err)
	}
	if _, err := tx.Exec(ctx, `update crm.tickets set status='open', resolved_at=null where note_id=$1::uuid and status in ('resolved','closed')`, id); err != nil {
		return command.DB(err)
	}
	if _, err := c.Emit("ticket.customer_replied", &id, map[string]any{"via": "portal"}); err != nil {
		return err
	}
	return command.DB(tx.Commit(ctx))
}
