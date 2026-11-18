# Customer messages (templated email) — v1 spec

Sources: Fable domain research and code design, Fable Mobbin study (2026-11-18). Name in the product: **Customer messages** (not "Email templates": "Use template" already means doc templates).

## Scope v1
- Editable subject + body per event, placeholders shown as words ("[Customer name]"), inserted from chips; stored as `{{invoice.number}}`.
- Live server-rendered preview (latest real record, else sample), "Send test to me", "Reset to default" (only when customised), "Customised" pill.
- Look and sender: sender name, reply-to, sign-off, accent colour, footer from Company profile (legal name, GSTIN, address). No logo in v1 (files are login-gated).
- Send dialog on invoices/quotes: Email | WhatsApp | Copy link, wording chosen by state (reminder level when overdue), editable for this one send, preview.
- Mail fixes: proper From/envelope parsing, "Business via C Note" display name, Reply-To, Date, Message-ID, multipart/alternative (text first, unencoded-compatible + HTML), UTF-8 (₹, Hindi).
- Out of v1: automatic reminder schedule (next wave, feeds the Collections agent), PDF attachment, logo, per-language templates, WhatsApp provider templates, tracking, own domain, version history.

## Events (v1)
| key | label | sent by |
|---|---|---|
| invoice.sent | Invoice sent | share.email on an invoice (not overdue) |
| invoice.reminder_1 / _2 / _3 | Payment reminder: friendly / second / final | share.email with reminder level 1-3 |
| quote.sent | Quote sent | share.email on a quote |
| receipt.sent | Payment receipt | share.email on a receipt |
| ticket.reply | Support reply (sign-off/footer only; body is the agent's text) | ticket.reply |
Portal sign-in keeps fixed wording inside the branded layout.

## Placeholders (whitelist per event)
Common: contact.name (fallback "there"), workspace.name, company.legal_name, company.gstin, company.phone, company.email, company.upi_vpa, user.name, today.
Invoice events: invoice.number, invoice.date, invoice.due_date, invoice.total, invoice.balance (= total − paid − credited − written_off, same as billing), invoice.days_overdue, invoice.link.
quote.sent: quote.number, quote.date, quote.valid_until, quote.total, quote.link.
receipt.sent: receipt.number, receipt.date, receipt.amount, receipt.link.
ticket.reply: ticket.ref, ticket.subject.
Money ₹1,18,000.00 (en-IN), dates "30 Nov 2026" — reuse docs templates' money/date formatters.

## Rendering rules
Body is plain text with `**bold**` and blank-line paragraphs. Render: HTML-escape body → apply bold/paragraphs → single-pass substitute HTML-escaped values (values never re-scanned). Text part: same substitution without escaping. Unknown placeholder → save refused (Invalid). Missing value → empty string (contact.name → "there"). Only server-built `*.link` values become anchors/buttons; anything an admin types stays text. If a template lacks the event's link placeholder, the link is appended (as today). Strip CR/LF from subject and names. One fixed HTML layout: accent bar, body, optional button "View and pay" for *.link, sign-off, footer (legal name · GSTIN · address), "Sent via C Note".

## Storage — migrations/1240_email_templates.sql
core.email_templates(workspace_id, event, subject 1..200, body 1..10000, version int default 1, updated_by, updated_at, PK(workspace_id,event)), tenant RLS, grants. Defaults in code; reset = delete row.
Branding/sender in workspaces.settings flat keys (validated in company.go profileKeys/checks AND at render time): email_from_name (≤80), email_reply_to (one address), email_accent (#rrggbb), email_signature (≤500), email_footer (≤500).

## Commands (JSON contract)
- email_template.list {} → {events:[{event,label,group,subject,body,default_subject,default_body,customised:bool,version,fields:[{key,label,example}]}], sender:{from_name,reply_to,accent,signature,footer}} — MinRole Admin, GroupRead.
- email_template.save {event, subject, body, version} → {template} — Admin, GroupAdmin; version mismatch → Conflict; unknown placeholder/size → Invalid.
- email_template.reset {event} → {ok:true} — Admin, GroupAdmin.
- email_template.preview {event, subject?, body?, note_id?} → {subject, text, html, sample:bool, record_label} — Admin (Member when note_id given and caller can read it), GroupRead. No link is created (sample link).
- email_template.test {event, subject?, body?} → {sent_to} — Admin, GroupSendMessages, people only, to caller's own email, subject prefixed "[Test] ", rate limit 5 per 5 min per user (command.CheckRateWith).
- share.preview {note_id, reminder_level?} → {event, subject, text, html, to} — Member, GroupRead, same page-access + finance checks as share issue(); creates no link.
- share.email (existing) gains optional subject and reminder_level (1..3); existing message/reminder inputs keep working.
Set MinRole explicitly (NewCommand defaults read to Guest). Add every command to modules/mcpmeta.go. None in financePrefixes.

## Go contracts (lead-owned stubs, filled by agents)
- package mailer (new leaf, apps/api/internal/mailer): `type Mail struct{ To, ReplyTo, FromName, Subject, Text, HTML string; Headers map[string]string }`; `func Send(ctx context.Context, m Mail) (messageID string, err error)` — parses EMAIL_FROM ("Name <addr>" or addr), From = `"<FromName> via <EMAIL_FROM name or C Note>" <addr>`, envelope MAIL FROM = bare addr, Reply-To, Date, Message-ID (<uuid@domain>), MIME multipart/alternative with text/plain first then text/html, UTF-8, quoted-printable or 8bit consistent with existing tests that grep raw SMTP data. publiclinks.sendMail and worker.SendMail become thin wrappers (or callers switch).
- package emailtpl (apps/api/internal/modules/emailtpl): `func Render(c *command.Ctx, event string, values map[string]string, override *Override) (Rendered, error)` with `type Override struct{ Subject, Body string }`, `type Rendered struct{ Subject, Text, HTML string; FromName, ReplyTo string }`; `func Events() []EventInfo`; values keys are placeholder keys. Uses workspace template row or default.

## Web (Settings → Workspace → Customer messages, ?tab=Messages)
Look and sender card; messages grouped (Invoices, Payment reminders, Quotes & receipts, Support) with "Sent when…" line, Customised pill, chevron → editor (form left: subject with counter, body textarea, Insert chips, Save, Send test to me, Reset to default; preview right: "Preview with: <record>", From line, subject, rendered HTML in a sandboxed iframe srcDoc). WhatsApp wording (settings.share_message) editable in the same page. Billing → Settings gets a link. Share dialog on invoice/quote: Email | WhatsApp | Copy link tabs, wording from share.preview, editable subject/message for this send, reminder level select when overdue, "Edit wording for all invoices" link. Follow existing settings design (icons, cards, EmptyState, tokens only).

## Tests
Unit: escaping/no re-scan/unknown placeholder/admin URL not an anchor/₹+Hindi round trip/CRLF stripped. Server: roles, version conflict, cross-tenant, preview creates no link, test-send rate limit + only to caller, existing TestShareEmail, TestTicketReplyEmailsCustomerNotes, TestWhatsAppReminderWording still pass. e2e: edit invoice.sent, preview shows real values, reset, send from invoice dialog (SMTP sink if available).

## Binding revisions after Codex design review
1. **Link origin:** customer emails must build `*.link` only from the server's PUBLIC_WEB_URL. In share.email, ignore/refuse any caller-supplied origin for the emailed link (links.go ~151): if the caller passes an origin that is not PUBLIC_WEB_URL, use PUBLIC_WEB_URL anyway. Test it.
2. **Every HTML context escaped:** body, values, signature, footer, from-name, record labels — html.EscapeString everywhere; the only anchor/button href is a server-built https URL (scheme validated). Accent colour must match ^#[0-9a-fA-F]{6}$ else default. Headers: strip CR/LF, encode non-ASCII with mime.QEncoding, Reply-To must parse as one address via net/mail. No arbitrary headers from input.
3. **Preview iframe:** `<iframe sandbox="" srcDoc=...>` (no scripts, no same-origin) and the rendered HTML includes `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`. Layout uses no external resources. Server never fetches template URLs.
4. **Preview record:** without note_id, pick the latest record of that kind the caller can read (readable spaces + core.note_access + finance check for invoices/receipts); otherwise sample data, flagged sample:true.
5. **Back-compat for share.email:** precedence = explicit per-send message (existing `message` input; link appended if missing, exactly as today) > workspace template for the event (core.email_templates row) > settings.share_message (legacy custom wording, used as the body default for invoice.sent/quote.sent if set and no template row) > code default. Existing `reminder: true` maps to reminder_level 1. Keep TestShareEmail, TestWhatsAppReminderWording passing unchanged; add tests for each precedence step. Overdue email now uses reminder wording when reminder/level is given.
6. **Support replies:** ticket.reply keeps its existing subject "[TKT-…] title", ticket ref, portal link and reply routing; workspace email_reply_to is NOT applied to ticket replies (replies must reach ticket intake). Store the sent Message-ID if the ticket schema allows (check mailin.go threading); at minimum keep current behaviour and add a test that a reply to the agent email threads into the same ticket if feasible, else document.
7. **Send-time links:** preview shows the sample link labelled "(link created when you send)"; the real link is created only at send. Share dialog shows actionable states: no customer email (add it in CRM), mail not configured (SMTP_URL missing → clear message), test-send failure text.
8. **Reset** takes {event, version} and checks it (Conflict on mismatch).
9. **Delivery:** keep current synchronous send semantics in v1 (no queue), but send only after all validation and after link creation succeeds; on SMTP failure return an error and do not record the send event. (Queued delivery with retries is v2 together with automatic reminders.)
10. **Formatters:** extract docs money/date formatters into exported helpers (docs/export.go or a small shared package) used by both. Non-INR: format with the invoice currency code if not INR. Missing date → "". Paid invoice: reminder levels refused (Invalid "invoice is paid") for share.email reminder sends; invoice.days_overdue = max(0, today_IST − due_date).
