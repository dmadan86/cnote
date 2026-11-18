package agents

// Template keys. Each template has a reviewed tool allowlist; an agent's own `tools` column may only narrow it
// (section 16.3). Instructions, scopes and triggers are filled in by the templates work (A3).
const (
	TemplateAsk           = "ask"
	TemplateBriefing      = "briefing"
	TemplateLeadResponder = "lead_responder"
	TemplateSupport       = "support"
)

var Templates = []string{TemplateAsk, TemplateBriefing, TemplateLeadResponder, TemplateSupport}

// ToolAllowlist is the most a template's agent can ever be offered (section 16.10). Held outbound sends
// (contact.email, ticket.reply) are Confirm commands: a person approves each one.
var ToolAllowlist = map[string][]string{
	TemplateAsk: {"home.summary", "report.run", "report.datasets", "search.hybrid", "note.get", "item.list", "followup.list",
		"invoice.list", "deal.list", "ticket.list", "contact.get", "contact.list"},
	TemplateBriefing: {"home.summary", "report.receivables_ageing", "report.payables_ageing", "followup.list", "ticket.list",
		"approval.list", "report.run"},
	TemplateLeadResponder: {"contact.lifecycle", "activity.list", "activity.log", "followup.set", "contact.email",
		"quote.create_draft", "quote.list", "stock_item.list"},
	TemplateSupport: {"ticket.get", "ticket.messages", "contact.lifecycle", "search.hybrid", "note.get", "comment.add", "ticket.reply"},
}
