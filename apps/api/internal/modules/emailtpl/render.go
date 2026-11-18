package emailtpl

import (
	"encoding/json"
	"errors"
	"html"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/docs"
)

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*([a-z_]+(?:\.[a-z_]+)*)\s*\}\}`)
	boldRe        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	paraRe        = regexp.MustCompile(`\n{2,}`)
	crlfRe        = regexp.MustCompile(`[\r\n]+`)
	accentRe      = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

const defaultAccent = "#1d4ed8"

// brand is the workspace's look and sender, read from workspaces.settings (validated again here: it may have
// been written through workspace.update).
type brand struct {
	WorkspaceName, FromName, ReplyTo, Accent, Signature, Footer string
	Common                                                      map[string]string // company.*, workspace.name, user.name, today
}

func oneLine(s string) string { return strings.TrimSpace(crlfRe.ReplaceAllString(s, " ")) }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func loadBrand(c *command.Ctx, event string) (brand, error) {
	var name string
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select name, settings from core.workspaces where id=$1::uuid", c.WorkspaceID()).Scan(&name, &raw); err != nil {
		return brand{}, command.DB(err)
	}
	s := map[string]any{}
	_ = json.Unmarshal(raw, &s)
	get := func(k string) string { v, _ := s[k].(string); return strings.TrimSpace(v) }
	b := brand{WorkspaceName: name, Common: map[string]string{}}
	b.FromName = oneLine(clip(get("email_from_name"), 80))
	if b.FromName == "" {
		b.FromName = oneLine(name)
	}
	if a, err := mail.ParseAddress(get("email_reply_to")); err == nil && a.Name == "" {
		b.ReplyTo = a.Address
	} else if a, err := mail.ParseAddress(get("email")); err == nil && a.Name == "" {
		b.ReplyTo = a.Address
	}
	if event == "ticket.reply" {
		b.ReplyTo = "" // replies must reach ticket intake
	}
	b.Accent = defaultAccent
	if a := get("email_accent"); accentRe.MatchString(a) {
		b.Accent = a
	}
	b.Signature, b.Footer = clip(get("email_signature"), 500), clip(get("email_footer"), 500)
	var uname *string
	if c.Principal.UserID != nil {
		if err := c.Tx.QueryRow(c.Context, "select name from core.users where id=$1::uuid", *c.Principal.UserID).Scan(&uname); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return brand{}, command.DB(err)
		}
	}
	b.Common = map[string]string{"workspace.name": name, "today": docs.Date(docs.TodayIST()),
		"company.legal_name": get("legal_name"), "company.gstin": get("gstin"), "company.phone": get("phone"),
		"company.email": get("email"), "company.upi_vpa": get("upi_vpa")}
	if uname != nil {
		b.Common["user.name"] = *uname
	}
	b.Common["_address"] = get("address")
	return b, nil
}

// Render turns the event's template (override, else the workspace's row, else the default) and values into mail parts.
func Render(c *command.Ctx, event string, values map[string]string, o *Override) (Rendered, error) {
	ev, ok := eventOf(event)
	if !ok {
		return Rendered{}, command.ErrInvalid("unknown message " + event)
	}
	subject, body := ev.DefaultSubject, ev.DefaultBody
	var rs, rb string
	err := c.Tx.QueryRow(c.Context, "select subject, body from core.email_templates where workspace_id=$1::uuid and event=$2 and customised", c.WorkspaceID(), event).Scan(&rs, &rb)
	if err == nil {
		subject, body = rs, rb
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Rendered{}, command.DB(err)
	}
	if o != nil {
		if o.Subject != "" {
			subject = o.Subject
		}
		if o.Body != "" {
			body = o.Body
		}
	}
	b, err := loadBrand(c, event)
	if err != nil {
		return Rendered{}, err
	}
	return renderWith(ev, subject, body, values, b), nil
}

// safeLink is true for a server-built absolute https URL: the only value that ever becomes an href.
func safeLink(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.ContainsAny(v, " \"'<>\r\n")
}

func renderWith(ev EventInfo, subject, body string, values map[string]string, b brand) Rendered {
	val := func(k string) string {
		v, ok := values[k]
		if !ok {
			v = b.Common[k]
		}
		if v == "" && k == "contact.name" {
			return "there"
		}
		return v
	}
	known := func(k string) bool { return ev.has(k) }
	// Body is what the send dialog edits: the template with values filled in, without link append, sign-off or footer.
	plain := placeholderRe.ReplaceAllStringFunc(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"), func(m string) string {
		if k := placeholderRe.FindStringSubmatch(m)[1]; known(k) {
			return strings.ReplaceAll(val(k), "\r", "")
		}
		return m
	})
	has := func(key string) bool { return hasKey(body, key) }
	// a support reply always carries the agent's text, whatever a stored body says
	if ev.Event == "ticket.reply" && !has("ticket.message") {
		body = strings.TrimRight(body, "\n ") + "\n\n{{ticket.message}}"
	}
	// the link is appended when the body lacks it, as before
	if ev.LinkKey != "" && !has(ev.LinkKey) {
		body = strings.TrimRight(body, "\n ") + "\n\n{{" + ev.LinkKey + "}}"
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")

	subj := placeholderRe.ReplaceAllStringFunc(subject, func(m string) string {
		if k := placeholderRe.FindStringSubmatch(m)[1]; known(k) {
			return val(k)
		}
		return m
	})
	subj = clip(oneLine(subj), 250)

	textBody := placeholderRe.ReplaceAllStringFunc(boldRe.ReplaceAllString(body, "$1"), func(m string) string {
		if k := placeholderRe.FindStringSubmatch(m)[1]; known(k) {
			return strings.ReplaceAll(val(k), "\r", "")
		}
		return m
	})

	// HTML: escape the template, apply bold and paragraphs, then substitute escaped values in one pass (never re-scanned).
	h := boldRe.ReplaceAllString(html.EscapeString(body), "<strong>$1</strong>")
	var paras []string
	for _, p := range paraRe.Split(strings.TrimSpace(h), -1) {
		paras = append(paras, `<p style="margin:0 0 16px">`+strings.ReplaceAll(p, "\n", "<br>")+`</p>`)
	}
	h = placeholderRe.ReplaceAllStringFunc(strings.Join(paras, "\n"), func(m string) string {
		k := placeholderRe.FindStringSubmatch(m)[1]
		if !known(k) {
			return m
		}
		v := val(k)
		if k == ev.LinkKey && safeLink(v) {
			e := html.EscapeString(v)
			return `<a href="` + e + `" style="color:` + b.Accent + `">` + e + `</a>`
		}
		return strings.ReplaceAll(html.EscapeString(strings.ReplaceAll(v, "\r", "")), "\n", "<br>")
	})

	sign := b.Signature
	if sign == "" {
		sign = "Regards,\n" + b.FromName
	}
	var foot []string
	for _, p := range []string{b.Common["company.legal_name"], gstinLabel(b.Common["company.gstin"]), oneLine(strings.ReplaceAll(b.Common["_address"], "\n", ", "))} {
		if p != "" {
			foot = append(foot, p)
		}
	}
	footLine := strings.Join(foot, " · ")
	button := ""
	if lk := val(ev.LinkKey); ev.LinkKey != "" && safeLink(lk) {
		label := "View"
		if strings.HasPrefix(ev.Event, "invoice.") {
			label = "View and pay"
		}
		button = `<p style="margin:8px 0 24px"><a href="` + html.EscapeString(lk) + `" style="display:inline-block;background:` + b.Accent +
			`;color:#ffffff;text-decoration:none;padding:12px 24px;border-radius:6px;font-weight:600">` + label + `</a></p>`
	}
	esc := func(s string) string { return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>") }
	page := `<!doctype html><html><head><meta charset="utf-8">` +
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"></head>` +
		`<body style="margin:0;background:#f4f4f5;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#18181b">` +
		`<div style="max-width:600px;margin:0 auto;background:#ffffff"><div style="height:6px;background:` + b.Accent + `"></div>` +
		`<div style="padding:28px 32px;font-size:16px;line-height:1.55">` + h + button +
		`<p style="margin:24px 0 0">` + esc(sign) + `</p></div>` +
		`<div style="padding:16px 32px;border-top:1px solid #e4e4e7;font-size:12px;line-height:1.5;color:#71717a">`
	if footLine != "" {
		page += esc(footLine) + `<br>`
	}
	if b.Footer != "" {
		page += esc(b.Footer) + `<br>`
	}
	page += `Sent via C Note</div></div></body></html>`

	text := strings.TrimSpace(textBody) + "\n\n" + sign + "\n\n--\n"
	if footLine != "" {
		text += footLine + "\n"
	}
	if b.Footer != "" {
		text += b.Footer + "\n"
	}
	text += "Sent via C Note\n"
	return Rendered{Subject: subj, Body: strings.TrimSpace(plain), Text: text, HTML: page, FromName: b.FromName, ReplyTo: b.ReplyTo}
}

func gstinLabel(g string) string {
	if g == "" {
		return ""
	}
	return "GSTIN " + g
}

// checkTemplate refuses a subject/body with a placeholder the event does not offer, a half-typed one, or a bad size.
func checkTemplate(ev EventInfo, subject, body string) error {
	if n := len([]rune(subject)); n < 1 || n > 200 {
		return command.ErrInvalid("subject must be 1 to 200 characters")
	}
	if n := len([]rune(body)); n < 1 || n > 10000 {
		return command.ErrInvalid("message must be 1 to 10000 characters")
	}
	if ev.Event == "ticket.reply" && !hasKey(body, "ticket.message") {
		return command.ErrInvalid("keep [Reply text] in the message")
	}
	for _, s := range []string{subject, body} {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			if !ev.has(m[1]) {
				return command.ErrInvalid("\"" + m[0] + "\" is not available in this message")
			}
		}
		if strings.Contains(placeholderRe.ReplaceAllString(s, ""), "{{") || strings.Contains(placeholderRe.ReplaceAllString(s, ""), "}}") {
			return command.ErrInvalid("a placeholder is not written correctly: use the Insert buttons")
		}
	}
	return nil
}

func hasKey(s, key string) bool {
	return slices.ContainsFunc(placeholderRe.FindAllStringSubmatch(s, -1), func(m []string) bool { return m[1] == key })
}
