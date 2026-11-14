package leads

// Support tickets (WP12): a ticket is a typed note ('ticket') linked to a CRM contact or company, optionally an
// invoice and a project (space). Replies are the ordinary note comments (comment.add / comment.list).
// Deferred: email-to-ticket, customer portal, business-hours SLA, first-response SLA, canned replies.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ticketCommands() []command.Command {
	adm := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	return []command.Command{
		command.NewCommand("ticket.create", command.GroupManageCRM, ticketCreate),
		command.NewCommand("ticket.update", command.GroupManageCRM, ticketUpdate),
		command.NewCommand("ticket.list", command.GroupRead, ticketList),
		command.NewCommand("ticket.get", command.GroupRead, ticketGet),
		command.NewCommand("ticket.sla_get", command.GroupRead, slaGet),
		adm(command.NewCommand("ticket.sla_set", command.GroupAdmin, slaSet)),
	}
}

var (
	priorities  = []string{"low", "normal", "high", "urgent"}
	statuses    = []string{"open", "pending", "resolved", "closed"}
	defaultSLAh = map[string]int{"urgent": 4, "high": 8, "normal": 24, "low": 72} // calendar hours; admins change them per workspace
)

// slaHours is the workspace's hours for a priority (its own row, else the default).
func slaHours(c *command.Ctx, priority string) (int, error) {
	var h int
	err := c.Tx.QueryRow(c.Context, `select hours from crm.ticket_sla where workspace_id=$1::uuid and priority=$2`, c.WorkspaceID(), priority).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSLAh[priority], nil
	}
	return h, command.DB(err)
}

// optID reads a nullable UUID: (nil, true) means "clear it", (nil, false) means not sent.
func (m in) optID(k string) (*string, bool, error) {
	if !m.has(k) {
		return nil, false, nil
	}
	if string(m[k]) == "null" {
		return nil, true, nil
	}
	s, err := m.id(k)
	if err != nil {
		return nil, true, err
	}
	return &s, true, nil
}

func (m in) oneOf(k string, set []string) (string, bool, error) {
	s, ok, err := m.str(k, 20)
	if err == nil && ok && !slices.Contains(set, s) {
		err = command.ErrInvalid(k + " must be one of " + strings.Join(set, ", "))
	}
	return s, ok, err
}

// links checks the optional references of a ticket are visible and exist in this workspace.
func links(c *command.Ctx, assignee, invoice, project *string) error {
	if assignee != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from core.memberships where user_id=$1::uuid and workspace_id=$2::uuid and role in ('owner','admin','member'))`, *assignee, c.WorkspaceID()).Scan(&ok); err != nil {
			return command.DB(err)
		}
		if !ok {
			return command.ErrInvalid("assignee_id must be a member of this workspace")
		}
	}
	if invoice != nil {
		if _, err := access.AssertNoteAccess(c, *invoice, false); err != nil {
			return err
		}
		var ok bool
		if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from billing.invoices where note_id=$1::uuid)`, *invoice).Scan(&ok); err != nil {
			return command.DB(err)
		}
		if !ok {
			return command.ErrInvalid("invoice_id is not an invoice")
		}
	}
	if project != nil {
		return access.AssertSpaceAccess(c, *project, false)
	}
	return nil
}

// newTicket inserts the note and its ticket row. owner becomes the note owner and the first assignee.
func newTicket(c *command.Ctx, space, contact, title, desc, priority, source string, assignee, invoice, project *string) (string, error) {
	hours, err := slaHours(c, priority)
	if err != nil {
		return "", err
	}
	n, err := notes.InsertTypedNote(c, space, title, "ticket", assignee)
	if err != nil {
		return "", err
	}
	if _, err := c.Tx.Exec(c.Context, `insert into crm.tickets(note_id,workspace_id,contact_id,invoice_id,project_id,priority,assignee_id,description,source,sla_due_at)
		values($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8,$9, now() + make_interval(hours => $10))`,
		n.ID, c.WorkspaceID(), contact, invoice, project, priority, assignee, desc, source, hours); err != nil {
		return "", command.DB(err)
	}
	_, err = c.Emit("ticket.created", &n.ID, map[string]any{"contact_id": contact, "priority": priority, "source": source})
	return n.ID, err
}

func ticketCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	title, err := m.req("title", 200)
	if err != nil {
		return nil, err
	}
	contact, err := m.id("contact_id")
	if err != nil {
		return nil, err
	}
	desc, _, err := m.str("description", 4000)
	if err != nil {
		return nil, err
	}
	prio, ok, err := m.oneOf("priority", priorities)
	if err != nil {
		return nil, err
	}
	if !ok {
		prio = "normal"
	}
	assignee, _, err := m.optID("assignee_id")
	if err != nil {
		return nil, err
	}
	invoice, _, err := m.optID("invoice_id")
	if err != nil {
		return nil, err
	}
	project, _, err := m.optID("project_id")
	if err != nil {
		return nil, err
	}
	if k, err := kindOf(c, contact); err != nil {
		return nil, err
	} else if k != "contact" {
		return nil, command.ErrInvalid("contact_id must be a contact or company")
	}
	space, err := access.AssertNoteAccess(c, contact, false)
	if err != nil {
		return nil, err
	}
	if err := links(c, assignee, invoice, project); err != nil {
		return nil, err
	}
	id, err := newTicket(c, space, contact, title, desc, prio, "internal", assignee, invoice, project)
	if err != nil {
		return nil, err
	}
	return ticketOne(c, id)
}

func ticketUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	status, hasStatus, err := m.oneOf("status", statuses)
	if err != nil {
		return nil, err
	}
	prio, hasPrio, err := m.oneOf("priority", priorities)
	if err != nil {
		return nil, err
	}
	assignee, hasAssignee, err := m.optID("assignee_id")
	if err != nil {
		return nil, err
	}
	invoice, hasInvoice, err := m.optID("invoice_id")
	if err != nil {
		return nil, err
	}
	project, hasProject, err := m.optID("project_id")
	if err != nil {
		return nil, err
	}
	if !hasStatus && !hasPrio && !hasAssignee && !hasInvoice && !hasProject {
		return nil, command.ErrInvalid("give status, priority, assignee_id, invoice_id or project_id")
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	var curStatus, curPrio string
	var curAssignee, curInvoice, curProject *string
	err = c.Tx.QueryRow(c.Context, `select status, priority, assignee_id::text, invoice_id::text, project_id::text from crm.tickets where note_id=$1::uuid for update`, id).
		Scan(&curStatus, &curPrio, &curAssignee, &curInvoice, &curProject)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("ticket")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if !hasStatus {
		status = curStatus
	}
	if !hasPrio {
		prio = curPrio
	}
	if !hasAssignee {
		assignee = curAssignee
	}
	if !hasInvoice {
		invoice = curInvoice
	}
	if !hasProject {
		project = curProject
	}
	if err := links(c, tern(hasAssignee, assignee), tern(hasInvoice, invoice), tern(hasProject, project)); err != nil {
		return nil, err
	}
	hours, err := slaHours(c, prio)
	if err != nil {
		return nil, err
	}
	// A priority change restarts the target from the ticket's creation; resolved_at marks when it left the live states.
	if _, err := c.Tx.Exec(c.Context, `update crm.tickets set status=$2, priority=$3, assignee_id=$4::uuid, invoice_id=$5::uuid, project_id=$6::uuid,
		sla_due_at=case when priority=$3 then sla_due_at else created_at + make_interval(hours => $7) end,
		resolved_at=case when $2 in ('resolved','closed') then coalesce(resolved_at, now()) else null end where note_id=$1::uuid`,
		id, status, prio, assignee, invoice, project, hours); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("ticket.updated", &id, map[string]any{"status": status, "priority": prio, "assignee_id": assignee}); err != nil {
		return nil, err
	}
	if status != curStatus && status == "resolved" {
		if _, err := c.Emit("ticket.resolved", &id, map[string]any{}); err != nil {
			return nil, err
		}
	}
	return ticketOne(c, id)
}

// tern returns v only when the field was sent, so unchanged links are not re-checked.
func tern(sent bool, v *string) *string {
	if sent {
		return v
	}
	return nil
}

type ticketFilter struct {
	status, priority, assignee, contact, id *string
	breached                                bool
}

const ticketCols = `n.id::text, n.title, t.status, t.priority, t.assignee_id::text, coalesce(u.name,''), t.contact_id::text, coalesce(cn.title,''), ct.phone,
	t.invoice_id::text, t.project_id::text, t.description, t.source, t.sla_due_at, t.resolved_at, t.created_at,
	(t.status in ('open','pending') and t.sla_due_at < now())`

func ticketRows(c *command.Ctx, f ticketFilter) ([]map[string]any, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context, `select `+ticketCols+`
		from crm.tickets t join core.notes n on n.id=t.note_id join core.notes cn on cn.id=t.contact_id join crm.contacts ct on ct.note_id=t.contact_id
		left join core.users u on u.id=t.assignee_id
		where n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id,$2::uuid,$3,$1::text[]::uuid[]) <> 'none'
		and ($4::text is null or ($4='active' and t.status in ('open','pending')) or t.status=$4)
		and ($5::text is null or t.priority=$5) and ($6::text is null or t.assignee_id=$6::uuid) and ($7::uuid is null or t.contact_id=$7::uuid)
		and ($8::uuid is null or n.id=$8::uuid) and (not $9 or (t.status in ('open','pending') and t.sla_due_at < now()))
		order by (t.status in ('open','pending')) desc, t.sla_due_at, n.created_at desc limit 200`,
		spaces, uid, admin, f.status, f.priority, f.assignee, f.contact, f.id, f.breached)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, st, pr, contact, cname, aname, desc, src string
		var assignee, phone, inv, proj *string
		var due, created time.Time
		var resolved *time.Time
		var breached bool
		if err := rows.Scan(&id, &title, &st, &pr, &assignee, &aname, &contact, &cname, &phone, &inv, &proj, &desc, &src, &due, &resolved, &created, &breached); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "title": title, "status": st, "priority": pr, "assignee_id": assignee, "assignee_name": aname,
			"contact_id": contact, "contact_name": cname, "contact_phone": phone, "invoice_id": inv, "project_id": proj, "description": desc,
			"source": src, "sla_due_at": command.Ts(due), "resolved_at": command.TsPtr(resolved), "created_at": command.Ts(created), "breached": breached})
	}
	return out, command.DB(rows.Err())
}

func ticketOne(c *command.Ctx, id string) (any, error) {
	r, err := ticketRows(c, ticketFilter{id: &id})
	if err != nil {
		return nil, err
	}
	if len(r) == 0 {
		return nil, command.ErrNotFound("ticket")
	}
	return r[0], nil
}

func ticketGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	id, err := m.id("id")
	if err != nil {
		return nil, err
	}
	return ticketOne(c, id)
}

func ticketList(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	active := "active"
	f := ticketFilter{status: &active}
	if s, ok, e := m.str("status", 20); e != nil {
		return nil, e
	} else if ok {
		if s != "active" && s != "all" && !slices.Contains(statuses, s) {
			return nil, command.ErrInvalid("status must be active, all, " + strings.Join(statuses, ", "))
		}
		f.status = &s
		if s == "all" {
			f.status = nil
		}
	}
	if s, ok, e := m.oneOf("priority", priorities); e != nil {
		return nil, e
	} else if ok {
		f.priority = &s
	}
	if s, ok, e := m.str("assignee_id", 36); e != nil {
		return nil, e
	} else if ok {
		if s == "me" {
			f.assignee = c.Principal.UserID
		} else if !uuidRe.MatchString(s) {
			return nil, command.ErrInvalid("assignee_id must be a UUID or me")
		} else {
			f.assignee = &s
		}
		if f.assignee == nil {
			return []map[string]any{}, nil
		}
	}
	if s, ok, e := m.str("contact_id", 36); e != nil {
		return nil, e
	} else if ok {
		if !uuidRe.MatchString(s) {
			return nil, command.ErrInvalid("contact_id must be a UUID")
		}
		f.contact = &s
	}
	if v, ok := m["breached"]; ok && string(v) != "null" && json.Unmarshal(v, &f.breached) != nil {
		return nil, command.ErrInvalid("breached must be true or false")
	}
	return ticketRows(c, f)
}

func slaGet(c *command.Ctx, _ json.RawMessage) (any, error) {
	out := map[string]any{}
	for _, p := range priorities {
		h, err := slaHours(c, p)
		if err != nil {
			return nil, err
		}
		out[p] = h
	}
	return map[string]any{"hours": out}, nil
}

func slaSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var body struct {
		Hours map[string]int `json:"hours"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Hours) == 0 {
		return nil, command.ErrInvalid("give hours like {\"urgent\": 4}")
	}
	for p, h := range body.Hours {
		if !slices.Contains(priorities, p) || h < 1 || h > 8760 {
			return nil, command.ErrInvalid("hours must map low, normal, high or urgent to 1-8760")
		}
	}
	for p, h := range body.Hours {
		if _, err := c.Tx.Exec(c.Context, `insert into crm.ticket_sla(workspace_id,priority,hours) values($1::uuid,$2,$3) on conflict (workspace_id,priority) do update set hours=$3`, c.WorkspaceID(), p, h); err != nil {
			return nil, command.DB(err)
		}
	}
	return slaGet(c, nil)
}

// ReceiveTicket turns one public submission into a contact (matched by phone or email) and an open ticket in the
// form's space, assigned to the form owner. Only the title (Subject) and the message are new inputs.
func ReceiveTicket(ctx context.Context, pool *pgxpool.Pool, f Form, subject string, in Inquiry) error {
	subject = strings.TrimSpace(subject)
	in, phone, err := Clean(in)
	switch {
	case err != nil:
		return err
	case subject == "" || len([]rune(subject)) > 200:
		return command.ErrInvalid("subject is required (200 characters at most)")
	case in.Message == "":
		return command.ErrInvalid("describe the problem")
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
	if _, err := newTicket(c, f.SpaceID, contact, subject, in.Message, "normal", "form", f.OwnerID, nil, nil); err != nil {
		return err
	}
	return command.DB(tx.Commit(ctx))
}
