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
	"company.get": {"Read the company profile that prints on documents: legal name, PAN, GSTIN, address, UPI and bank details, financial-year start.", schema(nil, props{})},
	"company.update": {"Update the company profile. Only the fields you send change; an empty string clears one. GSTIN, PAN, IFSC, PIN, phone and UPI ID are format-checked and the GSTIN must agree with the PAN and state. Admins only.", schema(nil, props{
		"legal_name": str, "trade_name": str, "pan": str, "gstin": str, "state_code": str, "address": str, "pincode": str, "phone": str, "email": str,
		"upi_vpa": str, "upi_payee_name": str, "bank_name": str, "bank_account": str, "bank_ifsc": str, "bank_details": str, "fy_start_month": props{"type": "integer", "description": "1-12, default 4 (April)"}})},
	"department.list":   {"List departments with their head and active headcount.", schema(nil, props{})},
	"department.create": {"Create a department (flat list). Admins only.", schema([]string{"name"}, props{"name": str, "head_user_id": id})},
	"department.update": {"Rename a department or set its head (null clears). Admins only.", schema([]string{"id"}, props{"id": id, "name": str, "head_user_id": id})},
	"department.delete": {"Delete a department; its members become unassigned. Needs human confirmation for agents.", idOnly},
	"people.list":       {"The people directory: role, department, manager, designation, phone, joined/left dates, finance access, and who is deactivated.", schema(nil, props{})},
	"member.update": {"Update a person's role, department, manager, designation, phone, joined/left dates or finance access (null clears a field). People only: agents and API keys are refused. Admins only.", schema([]string{"user_id"}, props{
		"user_id": id, "role": props{"type": "string", "enum": []string{"admin", "member", "guest"}}, "department_id": id, "manager_user_id": id, "designation": str, "phone": str,
		"joined_on": date, "left_on": date, "finance_access": flag})},
	"member.deactivate": {"Deactivate a person who left: they can no longer sign in to this workspace and their API keys stop. Not the last owner. People only.", schema([]string{"user_id"}, props{"user_id": id})},
	"member.reactivate": {"Let a deactivated person back in. Their old API keys stay revoked. People only.", schema([]string{"user_id"}, props{"user_id": id})},
	"approval.rule_set": {"Set the approval rule for a command (quote.send, invoice.issue, bill.record, bill.pay, invoice.write_off, receipt.refund, expense.approve, expense.reimburse): actions at or above the amount by non-owners wait for approval. Admins only.", schema([]string{"command"}, props{
		"command": str, "min_amount_minor": props{"type": "integer", "description": "Paise"}, "approver_user_id": id, "approver_role": props{"type": "string", "enum": []string{"admin", "owner"}}, "fallback_user_id": id, "escalate_after_hours": num, "enabled": flag})},
	"approval.rule_list": {"List the workspace's approval rules.", schema(nil, props{})},
	"approval.list":      {"List approval requests (pending by default), optionally only mine or for one page.", schema(nil, props{"status": str, "mine": flag, "note_id": id})},
	"approval.decide":    {"Approve or reject a pending approval request; rejecting needs a reason. People only: agents and API keys are refused.", schema([]string{"id", "decision"}, props{"id": id, "decision": props{"type": "string", "enum": []string{"approve", "reject"}}, "reason": str})},
	"approval.cancel":    {"Cancel your own pending approval request.", idOnly},
	"share.create": {"Create a public link to a quotation or invoice for the customer (accept or pay). Returns the link once, plus a WhatsApp click-to-chat URL. Needs human confirmation for agents.", schema([]string{"note_id"}, props{
		"note_id": id, "expires_in_days": props{"type": "integer", "description": "1-365, default 30"}, "origin": str, "phone": str, "message": str, "reminder": flag})},
	"share.email": {"Email a fresh public link to a quotation or invoice. Needs human confirmation for agents.", schema([]string{"note_id", "to"}, props{
		"note_id": id, "to": props{"type": "string", "description": "Recipient email"}, "expires_in_days": num, "origin": str, "message": str, "reminder": flag})},
	"share.list":     {"List public links for a quotation or invoice with status and view counts.", schema([]string{"note_id"}, props{"note_id": id})},
	"share.revoke":   {"Revoke a public link.", idOnly},
	"invoice.upi_qr": {"UPI payment URI for an invoice's outstanding amount, for a QR code.", idOnly},
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
	"quote.convert_to_project": {"Turn an accepted quotation into a project item; optional milestones [{name, percent_bp}] create billing milestones (percents of the quotation).", schema([]string{"id"}, props{
		"id": id, "milestones": props{"type": "array", "items": map[string]any{"type": "object", "required": []string{"name", "percent_bp"}, "properties": props{"name": str, "percent_bp": props{"type": "integer", "description": "Basis points of the quotation, 3000 = 30%"}}}}})},
	"time.log":    {"Log time on a work item (people only). At most 24 hours a day per person.", schema([]string{"item_id", "minutes"}, props{"item_id": id, "minutes": num, "worked_on": date, "note": str, "billable": flag})},
	"time.update": {"Change your own unbilled time entry (admins: anyone's).", schema([]string{"id"}, props{"id": id, "minutes": num, "worked_on": date, "note": str, "billable": flag})},
	"time.delete": {"Delete your own unbilled time entry (admins: anyone's).", idOnly},
	"time.list":   {"List time entries with totals, filtered by project, item, person, dates or unbilled only.", schema(nil, props{"space_id": id, "item_id": id, "user_id": id, "from": date, "to": date, "unbilled": flag, "limit": num})},
	"time.invoice": {"Raise a draft invoice from a project's unbilled billable time up to a date, at the project's hourly rate and SAC. The entries become billed and locked.", schema([]string{"space_id", "to_date"}, props{
		"space_id": id, "to_date": date, "group_by": props{"type": "string", "enum": []string{"item", "person", "none"}}, "contact_id": id, "invoice_date": date})},
	"project.billing_set": {"Set a project's billing profile: customer, hourly rate (paise), SAC and GST rate used by time and milestone invoices.", schema([]string{"space_id"}, props{
		"space_id": id, "contact_id": id, "hourly_rate_minor": props{"type": "integer", "description": "Paise per hour"}, "hsn_sac": str, "gst_rate_bp": num})},
	"project.billing_get": {"Read a project's billing profile.", schema([]string{"space_id"}, props{"space_id": id})},
	"milestone.set_billing": {"Make a project version a billing milestone: an amount before GST, or a percent of an accepted quotation (the milestone that completes 100% takes the remainder; the sum cannot exceed the quotation).", schema([]string{"version_id"}, props{
		"version_id": id, "quotation_id": id, "bill_amount_minor": props{"type": "integer", "description": "Paise before GST"}, "bill_percent_bp": props{"type": "integer", "description": "Basis points of the quotation"}})},
	"milestone.invoice":    {"Raise the draft invoice for a billing milestone (one line per GST rate of the quotation). invoice_date defaults to the version's release date, else today.", schema([]string{"version_id"}, props{"version_id": id, "invoice_date": date})},
	"milestone.list":       {"List a project's versions with billing: amount, percent, quotation, invoice and a status of planned, due, invoiced or paid.", schema([]string{"space_id"}, props{"space_id": id})},
	"report.project_money": {"One project's money: quoted, invoiced, received, advances (received, unapplied, applied), milestones and hours with the unbilled value.", schema([]string{"space_id"}, props{"space_id": id})},

	"bill.create_draft": {"Create a draft purchase bill from a vendor.", schema([]string{"space_id", "vendor_id", "vendor_bill_number"}, props{
		"space_id": id, "vendor_id": id, "vendor_bill_number": str, "bill_date": date, "due_date": date, "place_of_supply": str, "notes": str, "lines": lines})},
	"bill.update_draft": {"Update a draft purchase bill; sending `lines` replaces all lines.", schema([]string{"id"}, props{
		"id": id, "vendor_bill_number": str, "bill_date": date, "due_date": date, "place_of_supply": str, "notes": str, "lines": lines})},
	"bill.get":    {"Get one purchase bill with its lines and payments.", idOnly},
	"bill.list":   {"List purchase bills, optionally by status or vendor.", schema(nil, props{"status": str, "vendor_id": id, "limit": num})},
	"bill.record": {"Record (post) a draft purchase bill to the ledger with input GST.", idOnly},
	"bill.pay":    {"Pay a recorded purchase bill, fully or partly. Moves money. TDS is proposed from the vendor's default code; pass tds_code and tds_minor to choose or override.", schema([]string{"id", "amount_minor"}, props{"id": id, "amount_minor": props{"type": "integer", "description": "Gross paise settled against the bill"}, "method": str, "reference": str, "tds_code": str, "tds_minor": props{"type": "integer", "description": "Paise withheld; overrides the proposal"}})},
	"bill.cancel": {"Cancel a draft or recorded purchase bill (reverses its posting).", idOnly},
	"vendor.list": {"List vendors (contacts marked as vendor).", schema(nil, props{"limit": num})},

	"receipt.create": {"Record an advance received from a customer and issue its receipt voucher (GST on services is due now; goods carry no GST). Moves money.", schema([]string{"contact_id", "amount_minor", "supply_type"}, props{
		"contact_id": id, "quotation_id": id, "space_id": id, "amount_minor": props{"type": "integer", "description": "Gross amount received in paise, tax included"},
		"supply_type": props{"type": "string", "enum": []string{"services", "goods"}}, "gst_rate_bp": props{"type": "integer", "description": "Basis points, default 1800"},
		"place_of_supply": str, "method": str, "reference": str, "received_at": date, "description": str})},
	"receipt.apply":  {"Apply part or all of an unapplied advance to an open invoice of the same customer. Moves money.", schema([]string{"id", "invoice_id", "amount_minor"}, props{"id": id, "invoice_id": id, "amount_minor": props{"type": "integer", "description": "Paise"}})},
	"receipt.refund": {"Refund the unapplied part of an advance and issue a refund voucher. Moves money; admins only.", schema([]string{"id"}, props{"id": id, "amount_minor": props{"type": "integer", "description": "Paise; default all unapplied"}, "method": str, "reference": str})},
	"receipt.get":    {"Get one receipt or refund voucher with its applications and refunds.", idOnly},
	"receipt.list":   {"List receipt vouchers; status 'open' = advances still unapplied.", schema(nil, props{"contact_id": id, "status": str, "kind": props{"type": "string", "enum": []string{"receipt", "refund"}}, "limit": num})},

	"expense.create":        {"Start an expense claim for the calling person: what was spent, when, how much and the category. Attach the receipt photo to the page. Draft until submitted.", schema([]string{"spent_on", "category", "amount_minor"}, props{"space_id": id, "spent_on": date, "category": props{"type": "string", "description": "travel, meals, office, software, communication, repairs, other (see expense.categories)"}, "amount_minor": props{"type": "integer", "description": "Paise, tax included"}, "gst_minor": props{"type": "integer", "description": "GST inside the amount, informational"}, "vendor_name": str, "description": str, "method": str, "reference": str, "account_code": str})},
	"expense.update":        {"Edit your own draft or rejected claim (a rejected claim returns to draft).", schema([]string{"id"}, props{"id": id, "spent_on": date, "category": str, "amount_minor": num, "gst_minor": num, "vendor_name": str, "description": str, "account_code": str})},
	"expense.submit":        {"Submit your draft claim for approval.", idOnly},
	"expense.approve":       {"Approve a submitted claim: books the expense and what the company owes the claimant. Admins only; not your own claim unless you are an owner.", idOnly},
	"expense.reject":        {"Reject a submitted claim with a reason. Admins only.", schema([]string{"id", "reason"}, props{"id": id, "reason": str})},
	"expense.reimburse":     {"Pay back an approved claim to the claimant. Moves money; admins only.", schema([]string{"id"}, props{"id": id, "method": str, "reference": str})},
	"expense.record_direct": {"Record a company-paid expense with no vendor bill; it posts to the ledger at once. Moves money.", schema([]string{"spent_on", "category", "amount_minor"}, props{"space_id": id, "spent_on": date, "category": str, "amount_minor": props{"type": "integer", "description": "Paise"}, "gst_minor": num, "vendor_name": str, "description": str, "method": str, "reference": str, "account_code": str})},
	"expense.list":          {"List expenses: your own claims, or all for admins. status 'open' = submitted or approved (the approval and reimbursement queues).", schema(nil, props{"status": str, "kind": props{"type": "string", "enum": []string{"claim", "direct"}}, "from": date, "to": date, "limit": num})},
	"expense.get":           {"Get one expense.", idOnly},
	"expense.categories":    {"The expense categories and the ledger account each one posts to.", schema(nil, props{})},
	"report.profit_loss":    {"Income against expenses by account from the ledger between two dates (by posting date).", schema([]string{"from", "to"}, props{"from": date, "to": date})},
	"report.tds_payable":    {"Tax deducted at source on vendor payments between two dates, by section code and vendor with PAN, and what is owed to the government.", schema([]string{"from", "to"}, props{"from": date, "to": date})},
	"bill.tds_preview":      {"Preview the TDS that paying a bill would deduct (vendor default code or tds_code), with the reason and warnings such as a missing PAN.", schema([]string{"id", "amount_minor"}, props{"id": id, "amount_minor": props{"type": "integer", "description": "Gross payment in paise"}, "tds_code": str})},
	"vendor.set_tax":        {"Set a vendor's PAN and default TDS code (empty string clears).", schema([]string{"id"}, props{"id": id, "pan": str, "default_tds_code": str})},
	"tds.rate_list":         {"List the TDS codes with rates and thresholds (editable data, review each April).", schema(nil, props{})},
	"tds.rate_set":          {"Create or change a TDS code: label, rate and no-PAN rate in basis points, single-payment and annual limits in paise (null clears), active. Admins only.", schema([]string{"code"}, props{"code": str, "label": str, "rate_bp": num, "no_pan_rate_bp": num, "single_limit_minor": num, "annual_limit_minor": num, "active": flag})},

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

	"report.gstr1":           {"GSTR-1 summary for a month (with advances_11a and advances_adjusted_11b when the month has advances).", schema([]string{"period"}, props{"period": props{"type": "string", "description": "YYYY-MM"}})},
	"report.itc_summary":     {"Input tax credit from purchase bills between two dates.", schema([]string{"from", "to"}, props{"from": date, "to": date})},
	"report.payables_ageing": {"Ageing buckets of unpaid purchase bills.", schema(nil, props{"as_of": date})},
	"home.summary":           {"Owner home: what needs attention today (tasks, invoices, bills, quotes, deals, confirmations).", schema(nil, props{})},
	"audit.list":             {"Search the workspace audit log (events) by actor, kind prefix, page and dates, newest first, 200 per page. Admins only.", schema(nil, props{"actor_id": str, "kind_prefix": str, "note_id": id, "from": date, "to": date, "cursor": str, "limit": num})},
	"audit.export":           {"Export the filtered audit log as CSV (at most 50000 rows). Needs human confirmation for agents.", schema(nil, props{"actor_id": str, "kind_prefix": str, "note_id": id, "from": date, "to": date})},
	"audit.retention":        {"Report how many audit events are older than the retention period and optionally set the period in years. Nothing is deleted.", schema(nil, props{"years": num})},
	"data.export_subject":    {"Export one data subject's own records (a contact, or a workspace member) as JSON. Needs human confirmation for agents.", schema(nil, props{"contact_id": id, "user_id": id})},
	"data.request_list":      {"List data export and erase requests with their outcome.", schema(nil, props{})},
	"data.erase_subject":     {"Anonymise a contact (DPDP erasure) while keeping statutory finance records. People only; refused while unpaid documents exist unless confirm_unpaid is set.", schema([]string{"contact_id", "reason"}, props{"contact_id": id, "reason": str, "confirm_unpaid": flag})},
	"attendance.check_in":    {"Mark yourself present for today (IST) and record the check-in time. People only: an agent or API key cannot mark presence. Repeating it keeps the first time.", schema(nil, props{})},
	"attendance.check_out":   {"Record your check-out time for today. People only.", schema(nil, props{})},
	"attendance.mark": {"Admin: mark someone's attendance for a day in this or the previous month (present, half_day, absent, week_off, holiday). Approved leave days cannot be overwritten.", schema([]string{"employee_id", "day", "status"}, props{
		"employee_id": id, "day": date, "status": props{"type": "string", "enum": []string{"present", "half_day", "absent", "week_off", "holiday"}}, "note": str})},
	"attendance.today":    {"Today's board (IST): present, on leave, absent, not marked and expected counts, with people. Admins see everyone; others see themselves and their direct reports.", schema(nil, props{})},
	"attendance.register": {"Admin: the monthly attendance register as a grid and a CSV for payroll or the CA. Personal data: needs human confirmation for agents.", schema([]string{"month"}, props{"month": props{"type": "string", "description": "YYYY-MM"}})},
	"leave.request": {"Request leave. Working days exclude the employee's weekly offs and holidays; the balance of the leave type must cover it and dates cannot overlap another pending or approved request. Admins may pass employee_id for someone else.", schema([]string{"leave_type_id", "from_day"}, props{
		"leave_type_id": id, "from_day": date, "to_day": date, "half_day": flag, "reason": str, "employee_id": id})},
	"leave.decide": {"Approve or reject a pending leave request as the employee's manager or an admin (not your own unless owner). Approval writes leave days into attendance. Rejecting needs a reason. People only.", schema([]string{"id", "decision"}, props{
		"id": id, "decision": props{"type": "string", "enum": []string{"approve", "reject"}}, "reason": str})},
	"leave.cancel":      {"Cancel a pending or approved leave request (yours, or any as admin); approved leave days leave the attendance sheet.", idOnly},
	"leave.balance":     {"Leave balance per type for a year: quota, approved, pending, remaining (null remaining = no limit). Yourself by default; a manager or admin can pass employee_id.", schema(nil, props{"employee_id": id, "year": num})},
	"leave.list":        {"List leave requests you may see (yours, your reports', everyone's for admins), newest first, with can_decide and can_cancel.", schema(nil, props{"status": props{"type": "string", "enum": []string{"pending", "approved", "rejected", "cancelled"}}, "employee_id": id})},
	"leave_type.list":   {"List leave types with yearly quota (null = no limit). Defaults are seeded once and are not legal advice: check the state's Shops and Establishments rules.", schema(nil, props{})},
	"leave_type.set":    {"Admin: create (no id) or update a leave type: name, yearly_quota (null = no limit), paid, active.", schema(nil, props{"id": id, "name": str, "yearly_quota": props{"type": []string{"number", "null"}}, "paid": flag, "active": flag})},
	"holiday.list":      {"Holidays of a year. The three national holidays (26 Jan, 15 Aug, 2 Oct) are seeded; the owner adds the rest.", schema(nil, props{"year": num})},
	"holiday.set":       {"Admin: add a holiday or rename the one on that day (kind festival or company).", schema([]string{"day", "name"}, props{"day": date, "name": str, "kind": props{"type": "string", "enum": []string{"festival", "company"}}})},
	"holiday.delete":    {"Admin: remove a holiday.", idOnly},
	"employee.list":     {"Admin: employees (members get one automatically; offline employees have no login) with code, weekly off days and active flag.", schema(nil, props{})},
	"employee.create":   {"Admin: add an offline employee without a login so their attendance and leave can be tracked.", schema([]string{"name"}, props{"name": str, "code": str, "weekly_off": props{"type": "array", "items": num, "description": "0 = Sunday .. 6 = Saturday; default [0]"}})},
	"employee.update":   {"Admin: change an employee's code, weekly off days or active flag (name only for offline employees).", schema([]string{"id"}, props{"id": id, "name": str, "code": str, "weekly_off": props{"type": "array", "items": num}, "active": flag})},
	"file.upload":       {"Attach a file (image or PDF, at most 10 MB, judged from its bytes) to a page, e.g. a receipt photo on an expense claim. A claim takes files only while draft or rejected; finance pages need finance access.", schema([]string{"note_id", "filename", "data_base64"}, props{"note_id": id, "filename": str, "data_base64": props{"type": "string", "description": "File bytes, base64"}})},
	"file.list":         {"List the files attached to a page.", schema([]string{"note_id"}, props{"note_id": id})},
	"file.download":     {"Read one attached file as base64.", idOnly},
	"file.delete":       {"Remove an attached file (same rules as uploading).", idOnly},
	"contact.lifecycle": {"Everything about one contact in one read: deals, quotes, invoices and more.", idOnly},
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
