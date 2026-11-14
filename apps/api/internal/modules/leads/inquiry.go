package leads

import (
	"context"
	"errors"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoForm: unknown token or form switched off (the public endpoint answers 404 for both).
var ErrNoForm = errors.New("inquiry form not found")

type Form struct {
	WorkspaceID, WorkspaceName, SpaceID string
	OwnerID                             *string
}

// Lookup resolves a public token across tenants through a SECURITY DEFINER function.
func Lookup(ctx context.Context, pool *pgxpool.Pool, token string) (Form, error) {
	var f Form
	var on bool
	err := pool.QueryRow(ctx, `select workspace_id::text, workspace_name, space_id::text, owner_id::text, enabled from crm.inquiry_form_lookup($1)`, token).
		Scan(&f.WorkspaceID, &f.WorkspaceName, &f.SpaceID, &f.OwnerID, &on)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !on) {
		return f, ErrNoForm
	}
	return f, err
}

type Inquiry struct{ Name, Phone, Email, Message string }

// Clean trims and validates one public submission and returns it with the normalised phone number.
func Clean(in Inquiry) (Inquiry, string, error) {
	in.Name, in.Email, in.Message = strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.Message)
	phone, ok := NormalizePhone(in.Phone)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 100:
		return in, "", command.ErrInvalid("name is required (100 characters at most)")
	case !ok:
		return in, "", command.ErrInvalid("enter a valid phone number")
	case in.Email != "" && (len(in.Email) > 200 || !strings.Contains(in.Email, "@")):
		return in, "", command.ErrInvalid("enter a valid email address")
	case len([]rune(in.Message)) > 2000:
		return in, "", command.ErrInvalid("message is too long (2000 characters at most)")
	}
	return in, phone, nil
}

// Principal is the System principal a public form acts as: the form owner, admin role.
func (f Form) Principal() command.Principal {
	return command.Principal{Kind: command.KindSystem, WorkspaceID: f.WorkspaceID, UserID: f.OwnerID, Role: command.Admin}
}

// ResolveContact finds the contact with this phone or email, or creates one, and records consent.
func ResolveContact(c *command.Ctx, f Form, in Inquiry, phone string) (string, error) {
	tx, ctx := c.Tx, c.Context
	// shortcut: matches on the last 10 digits, right for India and good enough elsewhere; upgrade to E.164 equality once all contacts are normalised.
	var contact string
	err := tx.QueryRow(ctx, `select c.note_id::text from crm.contacts c join core.notes n on n.id=c.note_id
		where n.deleted_at is null and ((c.phone is not null and right(regexp_replace(c.phone,'\D','','g'),10)=right($1,10)) or ($2<>'' and lower(c.email)=$2))
		order by n.created_at limit 1`, strings.TrimPrefix(phone, "+"), in.Email).Scan(&contact)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		n, e := notes.InsertTypedNote(c, f.SpaceID, in.Name, "contact", nil)
		if e != nil {
			return "", e
		}
		contact = n.ID
		if _, err = tx.Exec(ctx, `insert into crm.contacts(note_id,workspace_id,kind,email,phone,consent_at) values($1::uuid,$2::uuid,'person',nullif($3,''),$4,now())`, contact, f.WorkspaceID, in.Email, phone); err != nil {
			return "", command.DB(err)
		}
		if _, err = c.Emit("contact.created", &contact, map[string]any{"name": in.Name}); err != nil {
			return "", err
		}
	case err != nil:
		return "", command.DB(err)
	default: // a returning customer consents again with this submission
		if _, err = tx.Exec(ctx, `update crm.contacts set consent_at=coalesce(consent_at,now()) where note_id=$1::uuid`, contact); err != nil {
			return "", command.DB(err)
		}
	}
	return contact, nil
}

// Receive turns one form submission into a contact (matched by phone or email when one exists) and a deal in
// stage 'lead' with source 'form'. The message becomes the first activity. Every row is written as a System
// principal acting for the form owner, in one tenant transaction.
func Receive(ctx context.Context, pool *pgxpool.Pool, f Form, in Inquiry) error {
	in, phone, err := Clean(in)
	if err != nil {
		return err
	}
	tx, err := db.BeginTenant(ctx, pool, f.WorkspaceID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &command.Ctx{Context: ctx, Tx: tx, Principal: f.Principal()}
	contact, err := ResolveContact(c, f, in, phone)
	if err != nil {
		return err
	}
	d, err := notes.InsertTypedNote(c, f.SpaceID, "Inquiry from "+in.Name, "deal", f.OwnerID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `insert into crm.deals(note_id,workspace_id,stage,amount_minor,currency,contact_id,owner_id,probability,source) values($1::uuid,$2::uuid,'lead',0,'INR',$3::uuid,$4::uuid,10,'form')`, d.ID, f.WorkspaceID, contact, f.OwnerID); err != nil {
		return command.DB(err)
	}
	if in.Message != "" {
		if _, err = tx.Exec(ctx, `insert into crm.activities(workspace_id,note_id,kind,summary) values($1::uuid,$2::uuid,'note',$3)`, f.WorkspaceID, d.ID, in.Message); err != nil {
			return command.DB(err)
		}
	}
	if _, err = c.Emit("deal.created", &d.ID, map[string]any{"stage": "lead"}); err != nil {
		return err
	}
	if _, err = c.Emit("inquiry.received", &d.ID, map[string]any{"contact_id": contact, "source": "form"}); err != nil {
		return err
	}
	return command.DB(tx.Commit(ctx))
}
