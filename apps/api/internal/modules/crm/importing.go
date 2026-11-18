package crm

import (
	"encoding/json"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Import-only commands for the data importers.

func importCmd(name string, h command.Handler) command.Command {
	c := command.NewCommand(name, command.GroupAdmin, h)
	c.MinRole = command.Admin
	c.Internal = true
	return c
}

// contact.import_keys {} -> {contacts:[{id,name,gstin,phone,email,is_vendor}]} for duplicate detection.
func importKeys(c *command.Ctx, _ json.RawMessage) (any, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.title, c.gstin, c.phone, c.email, c.is_vendor from crm.contacts c join core.notes n on n.id = c.note_id
		  where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id, $2::uuid, $3, $1::text[]::uuid[]) <> 'none'
		  order by n.created_at, n.id`, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var gstin, phone, email *string
		var vendor bool
		if err := rows.Scan(&id, &name, &gstin, &phone, &email, &vendor); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "name": name, "gstin": gstin, "phone": phone, "email": email, "is_vendor": vendor})
	}
	return map[string]any{"contacts": out}, command.DB(rows.Err())
}

// contact.import_usage {ids:[...]} -> {used:[id]}: contacts that any document, deal, ticket or portal link points at.
func importUsage(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		IDs []string `json:"ids"`
	}
	if json.Unmarshal(input, &v) != nil {
		return nil, command.ErrInvalid("ids is required")
	}
	rows, err := c.Tx.Query(c.Context,
		`select u from unnest($1::text[]::uuid[]) u where exists (select 1 from billing.invoices where contact_id = u)
		   or exists (select 1 from billing.purchase_bills where vendor_id = u) or exists (select 1 from billing.quotations where contact_id = u)
		   or exists (select 1 from billing.receipts where contact_id = u) or exists (select 1 from billing.recurring_invoices where contact_id = u)
		   or exists (select 1 from crm.deals where contact_id = u) or exists (select 1 from crm.tickets where contact_id = u)
		   or exists (select 1 from crm.portal_tokens where contact_id = u)`, v.IDs)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	used := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, command.DB(err)
		}
		used = append(used, id)
	}
	return map[string]any{"used": used}, command.DB(rows.Err())
}
