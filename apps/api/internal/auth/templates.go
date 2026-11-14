package auth

// Seed text of the two docs templates every new workspace gets (cnote-docs templates.rs, copied verbatim).
const (
	PROPOSAL = "# Proposal for {{contact.name}}\n\nPrepared on {{today}} by {{workspace.name}}.\n\n## Overview\n\n{{deal.title}}: a short description of what we will deliver.\n\n## Investment\n\nProposed value: **{{deal.value}}**. Expected decision by {{deal.expected_close}}.\n\n## Next steps\n\n1. Review this proposal.\n2. Confirm scope and timeline.\n3. Sign off and we begin.\n\nContact: {{contact.name}}, {{contact.email}}\n"
	COVER    = "Dear {{contact.name}},\n\nPlease find attached invoice {{invoice.number}} dated {{invoice.date}} for **{{invoice.total}}** (including GST of {{invoice.tax}}), due on {{invoice.due_date}}.\n\nThank you for your business. Reply to this note if anything needs correcting.\n\nRegards,\n{{user.name}}\n{{workspace.name}}\n"
)
