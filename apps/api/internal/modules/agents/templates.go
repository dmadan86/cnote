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

// Template is the reviewed definition behind one template key (section 8). Scopes and Tools are what an install
// grants; Optional lists extra scopes and tools an install flag adds, and update may never exceed Scopes+Optional.
type Template struct {
	Key, Name, Description, Tier string
	Scopes                       []string
	OptionalScopes               []string // added by install flag `quotes`
	OptionalTools                []string // allowlist entries only offered with that flag
	Events                       []string
	EveryMinutes                 int
	ChatAgents                   bool // needs chat_agents on and a delivery channel
	Instructions                 string
}

// Defs are in display order. Instructions are the editable paragraphs; the fixed preamble lives in the loop.
var Defs = []Template{
	{Key: TemplateAsk, Name: "Ask your business", Tier: "reasoning", Scopes: []string{"read"},
		Description:  "Answers questions about money, customers, tickets and pages from your own records. Read-only.",
		Instructions: "Answer what was asked. Use home.summary for what needs attention and report.run with grouping and a limit for totals. Say so plainly when the person cannot see the data."},
	{Key: TemplateBriefing, Name: "Daily briefing", Tier: "routine", Scopes: []string{"read"}, EveryMinutes: 1440, ChatAgents: true,
		Description:  "Posts one short summary a day to a chat channel: money to collect and pay, follow-ups, tickets and approvals. Read-only.",
		Instructions: "Write under 200 words on money to collect and pay, inquiries and follow-ups due, tickets at risk, approvals waiting, and the three things to do first."},
	{Key: TemplateLeadResponder, Name: "Lead responder", Tier: "reasoning", Scopes: []string{"read", "manage_crm", "send_messages"},
		OptionalScopes: []string{"manage_invoices"}, OptionalTools: []string{"quote.create_draft", "quote.list", "stock_item.list"},
		Events:      []string{"inquiry.received"},
		Description: "Reads each new inquiry, logs a note, sets the follow-up and drafts a reply for you to approve. Nothing is emailed until a person approves it.",
		Instructions: "Read the inquiry. Log one private note with activity.log (kind note, which keeps the lead in the unanswered inbox) holding a two-line summary and the suggested reply. " +
			"Set the follow-up with followup.set. If the contact has an email, draft the reply with contact.email, passing the lead as deal_id. " +
			"Write in the customer's language, under 120 words, with no prices or promises that are not in the records. Your final message is only the reply text."},
	{Key: TemplateSupport, Name: "Support agent", Tier: "reasoning", Scopes: []string{"read", "write_notes", "send_messages"},
		Events:      []string{"ticket.created", "ticket.customer_replied"},
		Description: "Reads each ticket and drafts a reply from your pages for you to approve, with an internal note on what it is based on. Nothing is sent until a person approves it.",
		Instructions: "Read the ticket and its conversation, look for the answer in pages with search.hybrid and note.get, and draft the reply with ticket.reply. " +
			"Then add an internal comment with comment.add saying what the draft is based on."},
}

// Def returns the definition for a template key.
func Def(key string) (Template, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Template{}, false
}

func init() {
	RegisterInstructions(func(key string) string {
		d, _ := Def(key)
		return d.Instructions
	})
}
