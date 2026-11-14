package modules

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

// What agents see over MCP (`discover`) for commands that exist only in Go. Commands Rust also has are described by
// Rust and omitted here on purpose. A Go-only command without an entry fails TestGoOnlyCommandsHaveMCPMetadata.

type props map[string]any

func schema(required []string, p props) json.RawMessage {
	o := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		o["required"] = required
	}
	b, _ := json.Marshal(o)
	return b
}

var (
	str   = props{"type": "string"}
	num   = props{"type": "integer"}
	flag  = props{"type": "boolean"}
	id    = props{"type": "string", "description": "UUID"}
	date  = props{"type": "string", "description": "YYYY-MM-DD"}
	lines = props{"type": "array", "description": "Line items; at least one on create", "items": map[string]any{
		"type":     "object",
		"required": []string{"description", "unit_price_minor"},
		"properties": props{"description": str, "quantity": props{"type": []string{"string", "number"}, "description": "Decimal, default 1"},
			"unit_price_minor": props{"type": "integer", "description": "Paise per unit"}, "gst_rate_bp": props{"type": "integer", "description": "GST rate in basis points, default 1800"},
			"hsn_sac": str, "item_note_id": id},
	}}
)

func with(base props, more props) props {
	o := props{}
	for k, v := range base {
		o[k] = v
	}
	for k, v := range more {
		o[k] = v
	}
	return o
}

type meta struct {
	desc   string
	schema json.RawMessage
}

var idOnly = schema([]string{"id"}, props{"id": id})

var mcpMeta = map[string]meta{
	"activity.log": {"Log a call, visit, WhatsApp, email or note on a deal or contact; optionally set the next follow-up.", schema([]string{"note_id", "kind", "summary"}, props{
		"note_id": id, "kind": props{"type": "string", "enum": []string{"call", "visit", "whatsapp", "email", "note"}}, "summary": str, "happened_at": props{"type": "string", "description": "RFC 3339; default now"},
		"next_follow_up": date, "next_step": str})},
	"activity.list": {"List logged activities for a deal or contact, newest first.", schema(nil, props{"note_id": id, "contact_id": id})},
	"followup.list": {"Deals needing follow-up: overdue, today, week, unplanned (no date) or inbox (new leads not yet answered).", schema(nil, props{
		"due": props{"type": "string", "enum": []string{"overdue", "today", "week", "unplanned", "inbox"}}, "owner_id": id, "note_id": id, "contact_id": id})},
	"followup.set":     {"Set or clear a deal's next follow-up date, next step and lead source.", schema([]string{"id"}, props{"id": id, "next_follow_up": date, "next_step": str, "source": str})},
	"deal.lose":        {"Mark a deal lost with a reason.", schema([]string{"id", "lost_reason"}, props{"id": id, "lost_reason": str})},
	"inquiry_form.get": {"Read the workspace's public inquiry form settings and link (admins).", schema(nil, props{})},
	"inquiry_form.set": {"Configure the public inquiry form: target space, owner, on/off, or rotate its link (admins).", schema(nil, props{"space_id": id, "owner_id": id, "enabled": flag, "rotate": flag})},
	"quote.create_draft": {"Create a draft quotation (estimate) for a contact. Never posts to the ledger.", schema([]string{"space_id", "contact_id", "lines"}, props{
		"space_id": id, "contact_id": id, "lines": lines, "quote_date": date, "valid_until": date, "place_of_supply": str, "reverse_charge": flag, "notes": str})},
	"quote.update_draft": {"Update a draft quotation; sending `lines` replaces all lines.", schema([]string{"id"}, props{
		"id": id, "contact_id": id, "lines": lines, "quote_date": date, "valid_until": date, "place_of_supply": str, "reverse_charge": flag, "notes": str})},
	"quote.get":                {"Get one quotation with its lines.", idOnly},
	"quote.list":               {"List quotations, optionally by status or contact.", schema(nil, props{"status": str, "contact_id": id, "limit": num})},
	"quote.send":               {"Send a draft quotation to the customer (assigns its QT number).", idOnly},
	"quote.accept":             {"Mark a sent quotation as accepted.", idOnly},
	"quote.reject":             {"Mark a sent quotation as rejected.", idOnly},
	"quote.convert_to_invoice": {"Turn an accepted quotation into a draft invoice.", idOnly},
	"quote.convert_to_project": {"Turn an accepted quotation into a project item.", idOnly},

	"bill.create_draft": {"Create a draft purchase bill from a vendor.", schema([]string{"space_id", "vendor_id", "vendor_bill_number"}, props{
		"space_id": id, "vendor_id": id, "vendor_bill_number": str, "bill_date": date, "due_date": date, "place_of_supply": str, "notes": str, "lines": lines})},
	"bill.update_draft": {"Update a draft purchase bill; sending `lines` replaces all lines.", schema([]string{"id"}, props{
		"id": id, "vendor_bill_number": str, "bill_date": date, "due_date": date, "place_of_supply": str, "notes": str, "lines": lines})},
	"bill.get":    {"Get one purchase bill with its lines and payments.", idOnly},
	"bill.list":   {"List purchase bills, optionally by status or vendor.", schema(nil, props{"status": str, "vendor_id": id, "limit": num})},
	"bill.record": {"Record (post) a draft purchase bill to the ledger with input GST.", idOnly},
	"bill.pay":    {"Pay a recorded purchase bill, fully or partly. Moves money.", schema([]string{"id", "amount_minor"}, props{"id": id, "amount_minor": props{"type": "integer", "description": "Paise"}, "method": str, "reference": str})},
	"bill.cancel": {"Cancel a draft or recorded purchase bill (reverses its posting).", idOnly},
	"vendor.list": {"List vendors (contacts marked as vendor).", schema(nil, props{"limit": num})},

	"recurring.create": {"Create a recurring invoice schedule for a contact.", schema([]string{"space_id", "contact_id", "frequency", "start_date", "lines"}, props{
		"space_id": id, "contact_id": id, "frequency": props{"type": "string", "enum": []string{"monthly", "quarterly", "yearly"}}, "interval": num,
		"start_date": date, "end_date": date, "auto_issue": flag, "lines": lines})},
	"recurring.update":  {"Update a recurring invoice schedule.", schema([]string{"id"}, props{"id": id, "frequency": str, "interval": num, "end_date": date, "auto_issue": flag, "lines": lines})},
	"recurring.get":     {"Get one recurring invoice schedule.", idOnly},
	"recurring.list":    {"List recurring invoice schedules.", schema(nil, props{})},
	"recurring.pause":   {"Pause a recurring invoice schedule.", idOnly},
	"recurring.resume":  {"Resume a paused recurring invoice schedule.", idOnly},
	"recurring.end":     {"End a recurring invoice schedule for good.", idOnly},
	"recurring.run_now": {"Generate the next invoice of a schedule now.", idOnly},

	"report.gstr1":           {"GSTR-1 summary for a month.", schema([]string{"period"}, props{"period": props{"type": "string", "description": "YYYY-MM"}})},
	"report.itc_summary":     {"Input tax credit from purchase bills between two dates.", schema([]string{"from", "to"}, props{"from": date, "to": date})},
	"report.payables_ageing": {"Ageing buckets of unpaid purchase bills.", schema(nil, props{"as_of": date})},
	"home.summary":           {"Owner home: what needs attention today (tasks, invoices, bills, quotes, deals, confirmations).", schema(nil, props{})},
	"contact.lifecycle":      {"Everything about one contact in one read: deals, quotes, invoices and more.", idOnly},
}

// withMeta attaches MCP metadata; a command Rust also has keeps none.
func withMeta(cmds []command.Command) []command.Command {
	for i, c := range cmds {
		if m, ok := mcpMeta[c.Name]; ok {
			cmds[i].Description, cmds[i].Schema = m.desc, m.schema
		}
	}
	return cmds
}
