package audit

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

// SubjectCatalog lists every foreign key that points at a contact or its note. data.export_subject either exports it
// ("export") or says why not. TestSubjectExportCoversEveryReference fails when a migration adds a table that is not
// listed, so a new table cannot be forgotten.
var SubjectCatalog = map[string]string{
	"crm.deals.contact_id": "export", "billing.invoices.contact_id": "export", "billing.quotations.contact_id": "export",
	"billing.recurring_invoices.contact_id": "export", "billing.purchase_bills.vendor_id": "export",
	"core.comments.note_id": "export", "core.attachments.note_id": "export", "core.events.note_id": "export",
	"crm.activities.note_id": "export", "crm.contacts.note_id": "export", "billing.invoices.note_id": "export (as the invoice)",
	"core.relations.from_id": "export", "core.relations.to_id": "export",
	"billing.receipts.contact_id": "export (when billing.receipts exists)", "billing.receipts.note_id": "export (as the receipt)",
	"crm.deals.note_id": "export (as the deal)", "core.data_requests.subject_note_id": "request log: ids and counts only",
	"core.notes.parent_id":                         "structure only: child pages are other records",
	"core.confirmations.note_id":                   "approval requests name the document, not the person",
	"core.note_permissions.note_id":                "access control, no personal data of the subject",
	"core.notifications.note_id":                   "notifications of members, not of the subject",
	"core.note_updates.note_id":                    "collaboration log of the page; scrubbed on erase",
	"core.note_revisions.note_id":                  "immutable page versions",
	"core.public_links.note_id":                    "link hashes of documents",
	"ai.chunks.note_id":                            "derived search index of the page text; deleted on erase",
	"projects.time_entries.item_id":                "work time on items, not a contact record",
	"projects.items.note_id":                       "work items, not a contact record",
	"projects.evidence.note_id":                    "work items, not a contact record",
	"docs.freshness_flags.note_id":                 "page upkeep",
	"docs.freshness_flags.source_note_id":          "page upkeep",
	"docs.edit_proposals.note_id":                  "page upkeep",
	"docs.capture_requests.answer_note_id":         "page upkeep",
	"docs.external_bindings.note_id":               "page upkeep",
	"docs.template_bindings.note_id":               "page upkeep",
	"docs.template_bindings.source_id":             "page upkeep",
	"billing.invoice_lines.item_note_id":           "catalogue items, not people",
	"billing.quotation_lines.item_note_id":         "catalogue items, not people",
	"billing.quotations.converted_item_id":         "work items",
	"billing.quotations.note_id":                   "export (as the quotation)",
	"billing.recurring_invoices.note_id":           "export (as the schedule)",
	"billing.recurring_invoice_lines.item_note_id": "catalogue items, not people",
	"billing.purchase_bills.note_id":               "export (as the bill)",
	"billing.purchase_bill_lines.item_note_id":     "catalogue items, not people",
}

// hasReceipts: billing.receipts arrives with a separate migration; handle it when it is there.
func hasReceipts(c *command.Ctx) bool {
	var ok bool
	_ = c.Tx.QueryRow(c.Context, `select to_regclass('billing.receipts') is not null`).Scan(&ok)
	return ok
}

func subject(c *command.Ctx, m in) (contact, user string, err error) {
	if contact, err = m.uuid("contact_id"); err != nil {
		return
	}
	if user, err = m.uuid("user_id"); err != nil {
		return
	}
	if (contact == "") == (user == "") {
		err = command.ErrInvalid("give exactly one of contact_id or user_id")
	}
	return
}

// agg runs a query and returns its rows as a JSON array (to_jsonb of each row).
func agg(c *command.Ctx, sql string, args ...any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.Tx.QueryRow(c.Context, `select coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) from (`+sql+`) t`, args...).Scan(&out)
	return out, command.DB(err)
}

func record(c *command.Ctx, kind, status string, note, user *string, reason string, result any) (string, error) {
	b, _ := command.Marshal(result)
	var id string
	err := c.Tx.QueryRow(c.Context, `insert into core.data_requests (workspace_id, subject_note_id, subject_user_id, kind, status, reason, result, requested_by, decided_by, done_at)
		values ($1::uuid,$2::uuid,$3::uuid,$4,$5,nullif($6,''),$7,$8::uuid,$8::uuid,now()) returning id::text`,
		c.WorkspaceID(), note, user, kind, status, reason, json.RawMessage(b), c.Principal.UserID).Scan(&id)
	return id, command.DB(err)
}

// exportSubject returns only records tied to one person: a contact by id, or a member of this workspace. The row
// filters are the security boundary together with RLS; nothing is selected without the subject's id in its WHERE.
func exportSubject(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	contact, user, err := subject(c, m)
	if err != nil {
		return nil, err
	}
	data := map[string]json.RawMessage{}
	put := func(k, sql string, args ...any) {
		if err == nil {
			data[k], err = agg(c, sql, args...)
		}
	}
	var noteID, userID *string // userID only names the subject in the request row
	if contact != "" {
		var ok bool
		if e := c.Tx.QueryRow(c.Context, `select true from crm.contacts where note_id = $1::uuid`, contact).Scan(&ok); errors.Is(e, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("contact")
		} else if e != nil {
			return nil, command.DB(e)
		}
		noteID = &contact
		put("contact", `select c.*, n.title, n.content_md, n.created_at, n.deleted_at from crm.contacts c join core.notes n on n.id = c.note_id where c.note_id = $1::uuid`, contact)
		put("deals", `select d.*, n.title, n.created_at from crm.deals d join core.notes n on n.id = d.note_id where d.contact_id = $1::uuid`, contact)
		put("activities", `select a.* from crm.activities a where a.note_id = $1::uuid or a.note_id in (select note_id from crm.deals where contact_id = $1::uuid) order by happened_at`, contact)
		put("comments", `select id, author_name, body, created_at from core.comments where note_id = $1::uuid order by created_at`, contact)
		put("attachments", `select id, filename, content_type, size_bytes, created_at from core.attachments where note_id = $1::uuid`, contact)
		put("invoices", `select i.* from billing.invoices i where i.contact_id = $1::uuid order by i.invoice_date`, contact)
		put("payments", `select p.* from billing.payments p join billing.invoices i on i.note_id = p.invoice_id where i.contact_id = $1::uuid order by p.received_at`, contact)
		put("quotations", `select q.* from billing.quotations q where q.contact_id = $1::uuid`, contact)
		put("recurring_invoices", `select r.* from billing.recurring_invoices r where r.contact_id = $1::uuid`, contact)
		put("purchase_bills", `select b.* from billing.purchase_bills b where b.vendor_id = $1::uuid`, contact)
		put("purchase_payments", `select p.* from billing.purchase_payments p join billing.purchase_bills b on b.note_id = p.bill_id where b.vendor_id = $1::uuid`, contact)
		if hasReceipts(c) {
			put("receipts", `select r.* from billing.receipts r where r.contact_id = $1::uuid order by r.received_at`, contact)
		}
		put("relations", `select id, from_id, to_id, kind, created_at from core.relations where from_id = $1::uuid or to_id = $1::uuid`, contact)
		put("events", `select id, kind, actor_type, actor_id, payload, created_at from core.events where note_id = $1::uuid order by id`, contact)
	} else {
		// A member of this workspace only: another tenant's user is "not found", never an empty export.
		var ok bool
		if e := c.Tx.QueryRow(c.Context, `select true from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid`, c.WorkspaceID(), user).Scan(&ok); errors.Is(e, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("member")
		} else if e != nil {
			return nil, command.DB(e)
		}
		userID = &user
		put("account", `select u.id, u.email, u.name, u.created_at, m.role, to_jsonb(m) - 'workspace_id' - 'user_id' as membership
			from core.users u join core.memberships m on m.user_id = u.id and m.workspace_id = $1::uuid where u.id = $2::uuid`, c.WorkspaceID(), user)
		put("pages_created", `select id, title, types, created_at from core.notes where created_by = $1::uuid or owner_id = $1::uuid order by created_at`, user)
		put("comments", `select id, note_id, body, created_at from core.comments where author_id = $1::uuid order by created_at`, user)
		put("activities", `select id, note_id, kind, summary, happened_at from crm.activities where created_by = $1::uuid order by happened_at`, user)
		put("events", `select id, kind, note_id, created_at from core.events where actor_type = 'user' and actor_id = $1 order by id`, user)
	}
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for k, v := range data {
		var a []json.RawMessage
		_ = json.Unmarshal(v, &a)
		counts[k] = len(a)
	}
	rid, err := record(c, "export", "done", noteID, userID, "", map[string]any{"counts": counts})
	if err != nil {
		return nil, err
	}
	who := "contact"
	if user != "" {
		who = "member"
	}
	if _, err = c.Emit("data.exported", noteID, map[string]any{"request_id": rid, "subject": who, "user_id": userID, "counts": counts}); err != nil {
		return nil, err
	}
	return map[string]any{"request_id": rid, "filename": "subject-export.json", "data": data}, nil
}

func requestList(c *command.Ctx, _ json.RawMessage) (any, error) {
	return agg(c, `select r.id, r.kind, r.status, r.reason, r.result, r.created_at, r.done_at, r.subject_note_id, r.subject_user_id,
		coalesce(n.title, u.name) as subject_name, rb.name as requested_by_name
		from core.data_requests r left join core.notes n on n.id = r.subject_note_id left join core.users u on u.id = r.subject_user_id
		left join core.users rb on rb.id = r.requested_by order by r.created_at desc limit 200`)
}

// eraseSubject anonymises a contact. Finance records survive: an issued invoice or recorded bill keeps its buyer name,
// GSTIN, state and address (GST invoice content, kept 72 months from the annual return due date; Companies Act 8
// years; verify with counsel), so a contact with any such document is erased partially. The ledger, revisions and the
// append-only event log are never touched.
func eraseSubject(c *command.Ctx, raw json.RawMessage) (any, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return nil, command.ErrForbidden("only a person can erase data")
	}
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	contact, user, err := subject(c, m)
	if err != nil {
		return nil, err
	}
	if user != "" {
		return nil, command.ErrInvalid("erasing a member is not supported here: remove them from the workspace and export their data; their sign-in belongs to the person")
	}
	reason, err := m.str("reason", 1000)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return nil, command.ErrInvalid("reason is required")
	}
	force := false
	if v, ok := m["confirm_unpaid"]; ok && json.Unmarshal(v, &force) != nil {
		return nil, command.ErrInvalid("confirm_unpaid must be true or false")
	}
	var kind string
	if e := c.Tx.QueryRow(c.Context, `select kind from crm.contacts where note_id = $1::uuid`, contact).Scan(&kind); errors.Is(e, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("contact")
	} else if e != nil {
		return nil, command.DB(e)
	}
	var done bool
	if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from core.data_requests where subject_note_id = $1::uuid and kind = 'erase' and status = 'done')`, contact).Scan(&done); err != nil {
		return nil, command.DB(err)
	}
	if done {
		return nil, command.ErrConflict("this contact was already erased")
	}
	var statutory, unpaid int
	receipts := "0"
	if hasReceipts(c) {
		receipts = "(select count(*) from billing.receipts where contact_id = $1::uuid)"
	}
	if err := c.Tx.QueryRow(c.Context, `select
		(select count(*) from billing.invoices where contact_id = $1::uuid and status <> 'draft') +
		(select count(*) from billing.purchase_bills where vendor_id = $1::uuid and status <> 'draft') + `+receipts+`,
		(select count(*) from billing.invoices where contact_id = $1::uuid and kind = 'invoice' and status in ('issued','irn_generated','sent','partly_paid')
		   and total_minor - paid_minor - credited_minor - written_off_minor > 0) +
		(select count(*) from billing.purchase_bills where vendor_id = $1::uuid and status in ('recorded','partly_paid'))`, contact).Scan(&statutory, &unpaid); err != nil {
		return nil, command.DB(err)
	}
	cn := contact
	if unpaid > 0 && !force {
		rid, err := record(c, "erase", "refused", &cn, nil, reason, map[string]any{"unpaid_documents": unpaid})
		if err != nil {
			return nil, err
		}
		return map[string]any{"request_id": rid, "status": "refused", "unpaid_documents": unpaid,
			"message": fmt.Sprintf("%d unpaid invoice(s) or bill(s) exist; settle them or send confirm_unpaid to erase anyway", unpaid)}, nil
	}

	mode := "full"
	if statutory > 0 {
		mode = "partial"
	}
	scrubbed := map[string]int64{}
	exec := func(k, sql string) {
		if err != nil {
			return
		}
		tag, e := c.Tx.Exec(c.Context, sql, contact)
		if e != nil {
			err = command.DB(e)
			return
		}
		scrubbed[k] = tag.RowsAffected()
	}
	if mode == "full" {
		exec("contact", `update crm.contacts set email = null, phone = null, company = null, gstin = null, state_code = null, billing_address = null, consent_at = null where note_id = $1::uuid`)
		exec("page", `update core.notes set title = 'Erased contact ' || left(id::text, 8), content_md = '', content_ydoc = null, updated_at = now() where id = $1::uuid`)
	} else {
		exec("contact", `update crm.contacts set email = null, phone = null, company = null, consent_at = null where note_id = $1::uuid`)
		exec("page", `update core.notes set content_md = '', content_ydoc = null, updated_at = now() where id = $1::uuid`)
	}
	exec("activities", `update crm.activities set summary = '[erased]' where note_id = $1::uuid or note_id in (select note_id from crm.deals where contact_id = $1::uuid)`)
	exec("comments", `update core.comments set body = '[erased]', anchor_text = null, anchor_prefix = null, author_name = null where note_id = $1::uuid`)
	exec("search_chunks", `delete from ai.chunks where note_id = $1::uuid`)
	exec("collab_log", `delete from core.note_updates where note_id = $1::uuid`)
	if err != nil {
		return nil, err
	}
	var files int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from core.attachments where note_id = $1::uuid`, contact).Scan(&files); err != nil {
		return nil, command.DB(err)
	}
	kept := []string{"audit event log (append-only)", "page revisions (immutable)", "deal titles", "ledger and journal entries"}
	if mode == "partial" {
		kept = append(kept, "name, GSTIN, state and address printed on issued invoices and recorded bills (statutory)")
	}
	if files > 0 {
		kept = append(kept, fmt.Sprintf("%d attachment file(s): delete them from the contact page", files))
	}
	result := map[string]any{"mode": mode, "scrubbed": scrubbed, "kept": kept, "statutory_documents": statutory, "unpaid_confirmed": unpaid > 0}
	rid, err := record(c, "erase", "done", &cn, nil, reason, result)
	if err != nil {
		return nil, err
	}
	// ids only: the event log cannot be edited later
	if _, err := c.Emit("data.erased", &cn, map[string]any{"request_id": rid, "mode": mode}); err != nil {
		return nil, err
	}
	result["request_id"], result["status"] = rid, "done"
	return result, nil
}
