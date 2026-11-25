package reports

import (
	"fmt"
	"slices"
)

// Every dataset has one row grain: FROM holds the base table plus many-to-one joins only, so a sum never multiplies.
// scope is copied from the module's own list command so a report shows exactly what the module list shows.

type field struct {
	key, label, typ string
	sql             string   // column or expression; for person/contact the uuid id
	labelSQL        string   // person/contact: display name expression
	group           bool     // may be grouped by
	finance         bool     // hidden from members without finance access
	values          []Option // enum values; nil = free text values, not validated
}

type dataset struct {
	key, label, noun, desc string
	module                 string // settings.hidden_modules switch
	finance                bool   // whole dataset needs finance access
	from                   string // after "from"
	pk                     string // stable tiebreaker
	scope                  func(g *gen, env Env) string
	defDate                string
	defCols                []string
	fields                 []field
	byKey                  map[string]*field
}

type gen struct{ args []any }

// push binds a value as a typed placeholder, the same style as projects/cnql.go.
func (g *gen) push(v any, cast string) string {
	g.args = append(g.args, v)
	return fmt.Sprintf("$%d::%s", len(g.args), cast)
}

// skipOpening keeps opening balance documents (carried over from another system) out of tax-flavoured datasets.
func skipOpening(inner func(g *gen, env Env) string, alias string) func(g *gen, env Env) string {
	return func(g *gen, env Env) string { return "(" + inner(g, env) + ") and not " + alias + ".opening" }
}

// noteScope is the page-level rule every note-backed list command uses: readable spaces + core.note_access.
func noteScope(spaceCol string) func(g *gen, env Env) string {
	return func(g *gen, env Env) string {
		spaces := env.Spaces
		if spaces == nil {
			spaces = []string{}
		}
		sp := g.push(spaces, "text[]")
		uid, admin := g.push(env.UserID, "uuid"), g.push(env.Admin, "boolean")
		return fmt.Sprintf("n.deleted_at is null and %s = any(%s::uuid[]) and core.note_access(n.id, %s, %s, %s::uuid[]) <> 'none'", spaceCol, sp, uid, admin, sp)
	}
}

// expenses/expenses.go list: admins see all, others their own claims, plus direct expenses with finance access.
func expenseScope(g *gen, env Env) string {
	return fmt.Sprintf("n.deleted_at is null and (%s or e.paid_by_user_id = %s or (e.kind = 'direct' and %s))",
		g.push(env.Admin, "boolean"), g.push(env.UserID, "uuid"), g.push(env.Finance, "boolean"))
}

func ist(col string) string { return "(" + col + " at time zone 'Asia/Kolkata')::date" }

func opts(kv ...string) []Option {
	out := make([]Option, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, Option{Value: kv[i], Label: kv[i+1]})
	}
	return out
}

var (
	invStatus  = opts("draft", "Draft", "issued", "Issued", "irn_generated", "E-invoice generated", "sent", "Sent", "partly_paid", "Partly paid", "paid", "Paid", "cancelled", "Cancelled")
	billStatus = opts("draft", "Draft", "recorded", "Recorded", "partly_paid", "Partly paid", "paid", "Paid", "cancelled", "Cancelled")
)

const invOpen = "i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid')"

var registry = []*dataset{
	{
		key: "invoices", label: "Invoices", noun: "invoices", desc: "One row per invoice or credit note", module: "billing", finance: true,
		from: "billing.invoices i join core.notes n on n.id = i.note_id left join core.notes cn on cn.id = i.contact_id",
		pk:   "i.note_id", scope: skipOpening(noteScope("n.space_id"), "i"), defDate: "invoice_date",
		defCols: []string{"number", "contact", "invoice_date", "status", "total", "balance"},
		fields: []field{
			{key: "number", label: "Invoice number", typ: TText, sql: "i.number"},
			{key: "kind", label: "Type", typ: TEnum, sql: "i.kind", group: true, values: opts("invoice", "Invoice", "credit_note", "Credit note")},
			{key: "status", label: "Status", typ: TEnum, sql: "i.status", group: true, values: invStatus},
			{key: "contact", label: "Customer", typ: TContact, sql: "i.contact_id", labelSQL: "cn.title", group: true},
			{key: "invoice_date", label: "Invoice date", typ: TDate, sql: "i.invoice_date", group: true},
			{key: "due_date", label: "Due date", typ: TDate, sql: "i.due_date", group: true},
			{key: "subtotal", label: "Amount before GST", typ: TMoney, sql: "i.subtotal_minor"},
			{key: "gst", label: "GST", typ: TMoney, sql: "(i.cgst_minor + i.sgst_minor + i.igst_minor)"},
			{key: "total", label: "Total amount", typ: TMoney, sql: "i.total_minor"},
			{key: "paid", label: "Amount received", typ: TMoney, sql: "i.paid_minor"},
			{key: "credited", label: "Credited", typ: TMoney, sql: "i.credited_minor"},
			{key: "written_off", label: "Written off", typ: TMoney, sql: "i.written_off_minor"},
			// same definition as report.receivables_ageing: open invoices only, never negative
			{key: "balance", label: "Balance due", typ: TMoney, sql: "(case when " + invOpen + " then greatest(i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor, 0) else 0 end)"},
			{key: "overdue", label: "Overdue", typ: TBool, sql: "(" + invOpen + " and i.due_date < (now() at time zone 'Asia/Kolkata')::date and i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor > 0)", group: true},
		},
	},
	{
		key: "payments", label: "Payments received", noun: "payments", desc: "One row per payment received against an invoice", module: "billing", finance: true,
		from: "billing.payments p join billing.invoices i on i.note_id = p.invoice_id join core.notes n on n.id = i.note_id left join core.notes cn on cn.id = i.contact_id",
		pk:   "p.id", scope: skipOpening(noteScope("n.space_id"), "i"), defDate: "received_on",
		defCols: []string{"received_on", "contact", "invoice_number", "amount", "method"},
		fields: []field{
			{key: "received_on", label: "Date received", typ: TDate, sql: ist("p.received_at"), group: true},
			{key: "amount", label: "Amount", typ: TMoney, sql: "p.amount_minor"},
			{key: "method", label: "Method", typ: TText, sql: "p.method", group: true},
			{key: "reference", label: "Reference", typ: TText, sql: "p.reference"},
			{key: "invoice_number", label: "Invoice number", typ: TText, sql: "i.number"},
			{key: "contact", label: "Customer", typ: TContact, sql: "i.contact_id", labelSQL: "cn.title", group: true},
		},
	},
	{
		key: "purchase_bills", label: "Purchase bills", noun: "bills", desc: "One row per bill from a supplier", module: "billing", finance: true,
		from: "billing.purchase_bills b join core.notes n on n.id = b.note_id left join core.notes cn on cn.id = b.vendor_id",
		pk:   "b.note_id", scope: skipOpening(noteScope("n.space_id"), "b"), defDate: "bill_date",
		defCols: []string{"vendor_bill_number", "vendor", "bill_date", "status", "total", "balance"},
		fields: []field{
			{key: "vendor_bill_number", label: "Supplier bill number", typ: TText, sql: "b.vendor_bill_number"},
			{key: "vendor", label: "Supplier", typ: TContact, sql: "b.vendor_id", labelSQL: "cn.title", group: true},
			{key: "status", label: "Status", typ: TEnum, sql: "b.status", group: true, values: billStatus},
			{key: "bill_date", label: "Bill date", typ: TDate, sql: "b.bill_date", group: true},
			{key: "due_date", label: "Due date", typ: TDate, sql: "b.due_date", group: true},
			{key: "subtotal", label: "Amount before GST", typ: TMoney, sql: "b.subtotal_minor"},
			{key: "gst", label: "GST (input credit)", typ: TMoney, sql: "(b.cgst_minor + b.sgst_minor + b.igst_minor)"},
			{key: "total", label: "Total amount", typ: TMoney, sql: "b.total_minor"},
			{key: "paid", label: "Amount paid", typ: TMoney, sql: "b.paid_minor"},
			{key: "balance", label: "Balance to pay", typ: TMoney, sql: "(case when b.status in ('recorded','partly_paid') then b.total_minor - b.paid_minor else 0 end)"},
		},
	},
	{
		key: "expenses", label: "Expenses", noun: "expenses", desc: "One row per expense claim or company-paid expense", module: "billing",
		from: "billing.expenses e join core.notes n on n.id = e.note_id left join core.users u on u.id = e.paid_by_user_id left join billing.accounts a on a.code = e.account_code and a.workspace_id = e.workspace_id",
		pk:   "e.note_id", scope: expenseScope, defDate: "spent_on",
		defCols: []string{"spent_on", "title", "category", "paid_by", "amount", "status"},
		fields: []field{
			{key: "spent_on", label: "Date", typ: TDate, sql: "e.spent_on", group: true},
			{key: "title", label: "Title", typ: TText, sql: "n.title"},
			{key: "kind", label: "Type", typ: TEnum, sql: "e.kind", group: true, values: opts("claim", "Claim", "direct", "Paid by company")},
			{key: "status", label: "Status", typ: TEnum, sql: "e.status", group: true, values: opts("draft", "Draft", "submitted", "Submitted", "approved", "Approved", "rejected", "Rejected", "reimbursed", "Reimbursed", "posted", "Posted")},
			{key: "category", label: "Category", typ: TText, sql: "coalesce(a.name, e.category)", group: true},
			{key: "vendor_name", label: "Vendor", typ: TText, sql: "e.vendor_name", group: true},
			{key: "amount", label: "Amount", typ: TMoney, sql: "e.amount_minor"},
			{key: "gst", label: "GST", typ: TMoney, sql: "e.gst_minor"},
			{key: "paid_by", label: "Paid by", typ: TPerson, sql: "e.paid_by_user_id", labelSQL: "u.name", group: true},
			{key: "method", label: "Payment method", typ: TText, sql: "e.method", group: true},
		},
	},
	{
		key: "deals", label: "Deals", noun: "deals", desc: "One row per deal in the pipeline", module: "crm",
		from: "crm.deals d join core.notes n on n.id = d.note_id left join core.notes cn on cn.id = d.contact_id left join core.users uo on uo.id = d.owner_id",
		pk:   "d.note_id", scope: noteScope("n.space_id"), defDate: "created_on",
		defCols: []string{"title", "contact", "stage", "amount", "owner", "expected_close"},
		fields: []field{
			{key: "title", label: "Deal", typ: TText, sql: "n.title"},
			{key: "stage", label: "Stage", typ: TEnum, sql: "d.stage", group: true, values: opts("lead", "Lead", "qualified", "Qualified", "proposal", "Proposal", "negotiation", "Negotiation", "won", "Won", "lost", "Lost")},
			{key: "contact", label: "Customer", typ: TContact, sql: "d.contact_id", labelSQL: "cn.title", group: true},
			{key: "owner", label: "Owner", typ: TPerson, sql: "d.owner_id", labelSQL: "uo.name", group: true},
			{key: "amount", label: "Deal value", typ: TMoney, sql: "d.amount_minor"},
			{key: "weighted", label: "Weighted value", typ: TMoney, sql: "(d.amount_minor * d.probability / 100)"},
			{key: "probability", label: "Probability (%)", typ: TNumber, sql: "d.probability"},
			{key: "expected_close", label: "Expected close", typ: TDate, sql: "d.expected_close", group: true},
			{key: "created_on", label: "Created on", typ: TDate, sql: ist("n.created_at"), group: true},
			{key: "closed_on", label: "Closed on", typ: TDate, sql: ist("d.closed_at"), group: true},
		},
	},
	{
		key: "tickets", label: "Support tickets", noun: "tickets", desc: "One row per support ticket", module: "crm",
		from: "crm.tickets t join core.notes n on n.id = t.note_id join core.notes cn on cn.id = t.contact_id join crm.contacts ct on ct.note_id = t.contact_id left join core.users u on u.id = t.assignee_id",
		pk:   "t.note_id", scope: noteScope("n.space_id"), defDate: "created_on",
		defCols: []string{"number", "subject", "contact", "status", "priority", "assignee"},
		fields: []field{
			{key: "number", label: "Ticket number", typ: TText, sql: "('TKT-' || lpad(t.number::text, 6, '0'))"},
			{key: "subject", label: "Subject", typ: TText, sql: "n.title"},
			{key: "status", label: "Status", typ: TEnum, sql: "t.status", group: true, values: opts("open", "Open", "pending", "Pending", "resolved", "Resolved", "closed", "Closed")},
			{key: "priority", label: "Priority", typ: TEnum, sql: "t.priority", group: true, values: opts("low", "Low", "normal", "Normal", "high", "High", "urgent", "Urgent")},
			{key: "source", label: "Source", typ: TEnum, sql: "t.source", group: true, values: opts("internal", "Internal", "form", "Web form", "email", "Email")},
			{key: "contact", label: "Customer", typ: TContact, sql: "t.contact_id", labelSQL: "cn.title", group: true},
			{key: "assignee", label: "Assigned to", typ: TPerson, sql: "t.assignee_id", labelSQL: "u.name", group: true},
			{key: "created_on", label: "Created on", typ: TDate, sql: ist("t.created_at"), group: true},
			{key: "due_on", label: "Response due", typ: TDate, sql: ist("t.sla_due_at"), group: true},
			{key: "resolved_on", label: "Resolved on", typ: TDate, sql: ist("t.resolved_at"), group: true},
			{key: "sla_missed", label: "Past response time", typ: TBool, sql: "(t.status in ('open','pending') and t.sla_due_at < now())", group: true},
		},
	},
	{
		key: "tasks", label: "Tasks", noun: "tasks", desc: "One row per task or work item in a project", module: "projects",
		from: "projects.items i join core.notes n on n.id = i.note_id join core.spaces sp on sp.id = i.space_id left join core.users ua on ua.id = i.assignee_id left join core.users ur on ur.id = i.reporter_id",
		pk:   "i.note_id", scope: noteScope("i.space_id"), defDate: "due_date",
		defCols: []string{"key", "title", "project", "status", "assignee", "due_date"},
		fields: []field{
			{key: "key", label: "Task key", typ: TText, sql: "i.key"},
			{key: "title", label: "Title", typ: TText, sql: "n.title"},
			{key: "project", label: "Project", typ: TText, sql: "sp.name", group: true},
			{key: "item_type", label: "Type", typ: TText, sql: "i.item_type", group: true},
			{key: "status", label: "Status", typ: TText, sql: "i.status", group: true},
			{key: "status_category", label: "Progress", typ: TEnum, sql: "i.status_category", group: true, values: opts("todo", "To do", "in_progress", "In progress", "done", "Done")},
			{key: "priority", label: "Priority", typ: TEnum, sql: "i.priority", group: true, values: opts("urgent", "Urgent", "high", "High", "medium", "Medium", "low", "Low", "none", "None")},
			{key: "assignee", label: "Assigned to", typ: TPerson, sql: "i.assignee_id", labelSQL: "ua.name", group: true},
			{key: "reporter", label: "Created by", typ: TPerson, sql: "i.reporter_id", labelSQL: "ur.name", group: true},
			{key: "due_date", label: "Due date", typ: TDate, sql: "i.due_date", group: true},
			{key: "created_on", label: "Created on", typ: TDate, sql: ist("n.created_at"), group: true},
			{key: "resolved_on", label: "Completed on", typ: TDate, sql: ist("i.resolved_at"), group: true},
			{key: "estimate", label: "Estimate (points)", typ: TNumber, sql: "i.estimate"},
		},
	},
	{
		key: "time_entries", label: "Time logged", noun: "time entries", desc: "One row per time entry logged on a task", module: "projects",
		from: "projects.time_entries e join core.notes n on n.id = e.item_id join projects.items pi on pi.note_id = e.item_id join core.users u on u.id = e.user_id join core.spaces sp on sp.id = n.space_id",
		pk:   "e.id", scope: noteScope("n.space_id"), // stricter than time.list: private tasks stay hidden here too
		defDate: "worked_on",
		defCols: []string{"worked_on", "person", "project", "task", "hours", "billable"},
		fields: []field{
			{key: "worked_on", label: "Date", typ: TDate, sql: "e.worked_on", group: true},
			{key: "person", label: "Person", typ: TPerson, sql: "e.user_id", labelSQL: "u.name", group: true},
			{key: "project", label: "Project", typ: TText, sql: "sp.name", group: true},
			{key: "task", label: "Task", typ: TText, sql: "(pi.key || ' ' || n.title)", group: true},
			{key: "hours", label: "Hours", typ: TNumber, sql: "(e.minutes / 60.0)"},
			{key: "minutes", label: "Minutes", typ: TNumber, sql: "e.minutes"},
			{key: "billable", label: "Billable", typ: TBool, sql: "e.billable", group: true},
			{key: "invoiced", label: "Invoiced", typ: TBool, sql: "(e.invoice_id is not null)", group: true},
			{key: "note", label: "Note", typ: TText, sql: "e.note"},
		},
	},
}

var datasetByKey = map[string]*dataset{}

func init() {
	for _, d := range registry {
		d.byKey = map[string]*field{}
		for i := range d.fields {
			d.byKey[d.fields[i].key] = &d.fields[i]
		}
		datasetByKey[d.key] = d
	}
}

func (f *field) aggs() []string {
	switch f.typ {
	case TMoney, TNumber:
		return []string{"sum", "avg", "min", "max"}
	case TDate:
		return []string{"min", "max"}
	}
	return []string{}
}

// allowed: the module is on, the caller is a member (not a guest) and has finance access when the dataset needs it.
func (d *dataset) allowed(env Env) bool {
	return env.Role >= 1 && (env.ModuleOn == nil || env.ModuleOn(d.module)) && (!d.finance || env.Finance)
}

func (f *field) visible(env Env) bool { return !f.finance || env.Finance }

// Catalog lists only the datasets and fields the caller may use.
func Catalog(env Env) []DatasetInfo {
	out := []DatasetInfo{}
	for _, d := range registry {
		if !d.allowed(env) {
			continue
		}
		info := DatasetInfo{Key: d.key, Label: d.label, Description: d.desc, DefaultDate: d.defDate, DefaultCols: slices.Clone(d.defCols), Fields: []FieldInfo{}}
		for i := range d.fields {
			f := &d.fields[i]
			if f.visible(env) {
				info.Fields = append(info.Fields, FieldInfo{Key: f.key, Label: f.label, Type: f.typ, Aggs: f.aggs(), Values: f.values, Group: f.group})
			}
		}
		out = append(out, info)
	}
	return out
}
