package docs

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

var kinds = []string{"none", "contact", "deal", "invoice", "item"}

type field struct{ Path, Label string }

var (
	common  = []field{{"workspace.name", "Workspace name"}, {"today", "Today's date (IST)"}, {"user.name", "Your name"}}
	contact = []field{
		{"contact.name", "Contact name"}, {"contact.email", "Contact email"}, {"contact.phone", "Contact phone"},
		{"contact.company", "Contact company"}, {"contact.gstin", "Contact GSTIN"}, {"contact.address", "Billing address"},
	}
	deal = []field{
		{"deal.title", "Deal title"}, {"deal.value", "Deal value"}, {"deal.currency", "Deal currency"},
		{"deal.stage", "Deal stage"}, {"deal.expected_close", "Expected close date"}, {"deal.probability", "Win probability"},
	}
	invoice = []field{
		{"invoice.number", "Invoice number"}, {"invoice.status", "Invoice status"}, {"invoice.date", "Invoice date"},
		{"invoice.due_date", "Due date"}, {"invoice.subtotal", "Subtotal"}, {"invoice.tax", "GST total"},
		{"invoice.total", "Invoice total"}, {"invoice.paid", "Amount paid"}, {"invoice.balance", "Balance due"},
	}
	item = []field{
		{"item.key", "Item key"}, {"item.title", "Item title"}, {"item.status", "Item status"}, {"item.priority", "Item priority"},
		{"item.type", "Item type"}, {"item.due_date", "Due date"}, {"item.assignee", "Assignee"},
	}
)

func catalogue(kind string) []field {
	out := append([]field{}, common...)
	switch kind {
	case "contact":
		out = append(out, contact...)
	case "deal":
		out = append(append(out, deal...), contact...)
	case "invoice":
		out = append(append(out, invoice...), contact...)
	case "item":
		out = append(out, item...)
	}
	return out
}

// ---------- pure: parsing, substitution, formatting ----------

// Rust's \s is Unicode White_Space; RE2's is ASCII only.
const ws = `[\t-\r \x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}]*`

// The editor stores an underscore as `\_`, so `{{ask.meeting_date}}` typed in a page arrives as `{{ask.meeting\_date}}`.
const identSeg = `(?:\\?_|[A-Za-z])(?:\\?_|[A-Za-z0-9])*`

var placeholderRe = regexp.MustCompile(`\{\{` + ws + `(` + identSeg + `(?:\.` + identSeg + `)*)` + ws + `\}\}`)

// pathOf is the placeholder's name without the editor's backslashes.
func pathOf(s string) string { return strings.ReplaceAll(s, `\`, "") }

type vals map[string]string

// ---------- prompts (docs-parity-b 5.8) ----------

const (
	askPrefix    = "ask."
	maxPrompts   = 20
	maxAnswerLen = 500
	promptKeyRe  = `^[A-Za-z_][A-Za-z0-9_]*$`
)

type prompt struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var promptKeyRx = regexp.MustCompile(promptKeyRe)

// promptsOf lists the {{ask.name}} placeholders of a template, in order of first appearance in title and body.
func promptsOf(title, body string) []prompt {
	out := []prompt{}
	seen := map[string]bool{}
	for _, text := range []string{title, body} {
		for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
			k, ok := strings.CutPrefix(pathOf(m[1]), askPrefix)
			if !ok || strings.Contains(k, ".") || seen[k] || len(out) >= maxPrompts {
				continue
			}
			seen[k] = true
			label := strings.ReplaceAll(k, "_", " ")
			out = append(out, prompt{k, strings.ToUpper(label[:1]) + label[1:]})
		}
	}
	return out
}

// mdLiteral makes an answer plain text inside Markdown: it cannot open a link, tag, emphasis, table cell or block.
func mdLiteral(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch r {
		case '\\', '*', '_', '`', '~', '[', ']', '<', '>', '|', '&', '#', '!', '(', ')':
			b.WriteByte('\\')
		case '-', '+', '=':
			if i == 0 {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
	}
	out := strings.ReplaceAll(b.String(), "://", "\\://") // no bare address for the editor to turn into a link
	if n := len(out) - len(strings.TrimLeft(out, "0123456789")); n > 0 && n < len(out) && (out[n] == '.' || out[n] == ')') {
		out = out[:n] + "\\" + out[n:]
	}
	return out
}

// checkAnswers validates the answers of template.render against the template's own prompts and returns the values to
// substitute, keyed by prompt name.
func checkAnswers(raw map[string]any, t tpl) (map[string]string, error) {
	known := map[string]bool{}
	for _, p := range promptsOf(t.title, t.content) {
		known[p.Key] = true
	}
	out := map[string]string{}
	for k, v := range raw {
		s, ok := v.(string)
		key := strings.TrimPrefix(k, askPrefix)
		switch {
		case !ok:
			return nil, command.ErrInvalid("answers must be text")
		case !promptKeyRx.MatchString(key) || !known[key]:
			return nil, command.ErrInvalid("this template has no prompt named " + k)
		case utf8.RuneCountInString(s) > maxAnswerLen:
			return nil, command.ErrInvalid("an answer is at most 500 characters")
		case strings.ContainsAny(s, "\r\n"):
			return nil, command.ErrInvalid("an answer must be one line")
		}
		if s = strings.TrimSpace(s); s != "" {
			out[key] = s
		}
	}
	return out, nil
}

// substitute replaces every known placeholder in one pass (values are never re-scanned).
func substitute(text string, v vals, missing map[string]bool) string {
	var b strings.Builder
	last := 0
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[last:m[0]])
		path := pathOf(text[m[2]:m[3]])
		if x, ok := v[path]; ok {
			b.WriteString(x)
		} else {
			missing[path] = true
			b.WriteString(text[m[0]:m[1]])
		}
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// renderMarkdownAnswers removes links whose substituted destination is not an allowed URL.
// Work on the template before escaping answers, so validation sees the actual destination.
func renderMarkdownAnswers(text string, raw, escaped vals, missing map[string]bool) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		start := i
		if text[i] == '!' && i+1 < len(text) && text[i+1] == '[' {
			i++
		}
		if text[i] != '[' {
			out.WriteByte(text[start])
			i = start + 1
			continue
		}
		labelStart := i + 1
		depth := 1
		i++
		for i < len(text) && depth > 0 {
			if text[i] == '\\' && i+1 < len(text) {
				i += 2
				continue
			}
			if text[i] == '[' {
				depth++
			}
			if text[i] == ']' {
				depth--
			}
			i++
		}
		if depth != 0 || i >= len(text) || text[i] != '(' {
			out.WriteByte(text[start])
			i = start + 1
			continue
		}
		labelEnd := i - 1
		destStart := i + 1
		i++
		depth = 1
		for i < len(text) && depth > 0 {
			if text[i] == '\\' && i+1 < len(text) {
				i += 2
				continue
			}
			if text[i] == '(' {
				depth++
			}
			if text[i] == ')' {
				depth--
			}
			i++
		}
		if depth != 0 {
			out.WriteByte(text[start])
			i = start + 1
			continue
		}
		dest := text[destStart : i-1]
		hasAnswer := false
		for _, p := range placeholderRe.FindAllStringSubmatch(dest, -1) {
			if strings.HasPrefix(pathOf(p[1]), askPrefix) {
				hasAnswer = true
			}
		}
		if !hasAnswer {
			out.WriteString(text[start:i])
			continue
		}
		resolved := html.UnescapeString(substitute(dest, raw, map[string]bool{}))
		resolved = strings.TrimSpace(resolved)
		u, err := url.Parse(resolved)
		safe := err == nil && !strings.ContainsAny(resolved, "\\\r\n\t ") &&
			((u.Scheme == "http" || u.Scheme == "https") && u.Host != "" || u.Scheme == "mailto" && u.Opaque != "" || u.Scheme == "" && u.Host == "" && strings.HasPrefix(resolved, "/w/"))
		if !safe {
			out.WriteString(text[labelStart:labelEnd])
		} else {
			out.WriteString(text[start:i])
		}
	}
	return substitute(out.String(), escaped, missing)
}

// serdeValue rewrites decoded JSON the way serde_json::Value would print it (floats as notes.F64 prints them); a number
// serde would reject returns ok=false.
func serdeValue(x any, depth int, f func(string) string) (any, bool) {
	if depth > 100 { // serde_json's recursion limit is 128
		return nil, false
	}
	switch v := x.(type) {
	case string:
		return f(v), true
	case json.Number:
		t := v.String()
		if !strings.ContainsAny(t, ".eE") && t != "-0" {
			if _, err := strconv.ParseInt(t, 10, 64); err == nil {
				return v, true
			}
			if _, err := strconv.ParseUint(t, 10, 64); err == nil {
				return v, true
			}
		}
		fl, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsInf(fl, 0) {
			return nil, false // serde refuses a number that is not a finite f64
		}
		return notes.F64(fl), true
	case []any:
		for i := range v {
			var ok bool
			if v[i], ok = serdeValue(v[i], depth+1, f); !ok {
				return nil, false
			}
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			var ok bool
			if out[k], ok = serdeValue(e, depth+1, f); !ok {
				return nil, false
			}
		}
		return out, true
	}
	return x, true
}

var fenceRe = regexp.MustCompile("(?s)```tldraw\n(.*?)\n```")

// renderBoard substitutes inside the JSON string values of a ```tldraw fence so that quotes or newlines in a
// value cannot break the snapshot.
func renderBoard(md string, v vals, missing map[string]bool) (string, error) {
	m := fenceRe.FindStringSubmatchIndex(md)
	if m == nil {
		return substitute(md, v, missing), nil
	}
	body := md[m[2]:m[3]]
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var snap any
	var rest json.RawMessage
	if dec.Decode(&snap) != nil || dec.Decode(&rest) != io.EOF {
		return "", command.ErrInvalid("the board snapshot in this template is not valid JSON")
	}
	out, ok := serdeValue(snap, 0, func(s string) string { return substitute(s, v, missing) })
	if !ok {
		return "", command.ErrFallback
	}
	b, err := command.Marshal(out)
	if err != nil {
		return "", err
	}
	return md[:m[2]] + string(b) + md[m[3]:], nil
}

func groupDigits(d string, indian bool) string {
	out := make([]byte, 0, len(d)+len(d)/2)
	for i := 0; i < len(d); i++ {
		c := d[len(d)-1-i]
		if indian && (i == 3 || (i > 3 && (i-3)%2 == 0)) || !indian && i > 0 && i%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return string(out)
}

// money: i64 minor units to text; Indian grouping with the rupee sign for INR, otherwise "USD 1,234.50".
func money(minor int64, currency string) string {
	abs := uint64(minor)
	if minor < 0 {
		abs = -abs
	}
	inr := currency == "INR"
	whole := groupDigits(strconv.FormatUint(abs/100, 10), inr)
	sign, sym := "", currency+" "
	if minor < 0 {
		sign = "-"
	}
	if inr {
		sym = "₹"
	}
	return fmt.Sprintf("%s%s%s.%02d", sign, sym, whole, abs%100)
}

func date(t time.Time) string { return t.Format("2 Jan 2006") }

func optDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return date(*t)
}

func opt(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func todayIST() time.Time { return time.Now().UTC().Add(19800 * time.Second) }

// ---------- resolving a source record ----------

// contactValues fills contact.* when the caller can read the contact's space.
func contactValues(c *command.Ctx, id string, v vals) error {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return err
	}
	uid, admin := access.Viewer(c)
	var name string
	var email, phone, company, gstin, addr *string
	err = c.Tx.QueryRow(c.Context,
		`select n.title, c.email, c.phone, c.company, c.gstin, c.billing_address
		 from crm.contacts c join core.notes n on n.id = c.note_id
		 where c.note_id = $1::uuid and n.deleted_at is null and n.space_id = any($2::text[]::uuid[])
		   and core.note_access(n.id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'`, id, spaces, uid, admin).
		Scan(&name, &email, &phone, &company, &gstin, &addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	v["contact.name"], v["contact.email"], v["contact.phone"] = name, opt(email), opt(phone)
	v["contact.company"], v["contact.gstin"], v["contact.address"] = opt(company), opt(gstin), opt(addr)
	return nil
}

// resolve returns every placeholder value for kind/source. The caller must be able to read the source note.
func resolve(c *command.Ctx, kind string, source *string) (vals, error) {
	v := vals{}
	var wsName string
	if err := c.Tx.QueryRow(c.Context, "select name from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&wsName); err != nil {
		return nil, err
	}
	v["workspace.name"], v["today"] = wsName, date(todayIST())
	var uname *string
	err := c.Tx.QueryRow(c.Context, "select name from core.users where id = $1::uuid", c.Principal.UserID).Scan(&uname)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	v["user.name"] = opt(uname)
	if source == nil {
		return v, nil
	}
	id := *source
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	wrong := command.ErrInvalid("the source record is not a " + kind)
	switch kind {
	case "contact":
		if err := contactValues(c, id, v); err != nil {
			return nil, err
		}
		if _, ok := v["contact.name"]; !ok {
			return nil, wrong
		}
	case "deal":
		var title, cur, stage string
		var amount int64
		var close *time.Time
		var prob int32
		var contactID *string
		err := c.Tx.QueryRow(c.Context,
			`select n.title, d.amount_minor, d.currency::text, d.stage, d.expected_close, d.probability, d.contact_id::text
			 from crm.deals d join core.notes n on n.id = d.note_id where d.note_id = $1::uuid and n.deleted_at is null`, id).
			Scan(&title, &amount, &cur, &stage, &close, &prob, &contactID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, wrong
		}
		if err != nil {
			return nil, err
		}
		v["deal.title"], v["deal.value"], v["deal.currency"] = title, money(amount, cur), cur
		v["deal.stage"], v["deal.expected_close"], v["deal.probability"] = stage, optDate(close), fmt.Sprintf("%d%%", prob)
		if contactID != nil {
			if err := contactValues(c, *contactID, v); err != nil {
				return nil, err
			}
		}
	case "invoice":
		var number *string
		var status, cur string
		var d, due *time.Time
		var sub, tax, total, paid, bal int64
		var contactID *string
		err := c.Tx.QueryRow(c.Context,
			`select i.number, i.status, i.currency::text, i.invoice_date, i.due_date, i.subtotal_minor,
			        i.cgst_minor + i.sgst_minor + i.igst_minor, i.total_minor, i.paid_minor, i.total_minor - i.paid_minor, i.contact_id::text
			 from billing.invoices i join core.notes n on n.id = i.note_id where i.note_id = $1::uuid and n.deleted_at is null`, id).
			Scan(&number, &status, &cur, &d, &due, &sub, &tax, &total, &paid, &bal, &contactID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, wrong
		}
		if err != nil {
			return nil, err
		}
		v["invoice.number"] = "DRAFT"
		if number != nil {
			v["invoice.number"] = *number
		}
		v["invoice.status"], v["invoice.date"], v["invoice.due_date"] = status, optDate(d), optDate(due)
		v["invoice.subtotal"], v["invoice.tax"], v["invoice.total"] = money(sub, cur), money(tax, cur), money(total, cur)
		v["invoice.paid"], v["invoice.balance"] = money(paid, cur), money(bal, cur)
		if contactID != nil {
			if err := contactValues(c, *contactID, v); err != nil {
				return nil, err
			}
		}
	case "item":
		var key, title, status, priority, typ string
		var due *time.Time
		var assignee *string
		err := c.Tx.QueryRow(c.Context,
			`select i.key, n.title, i.status, i.priority, i.item_type, i.due_date,
			        (select u.name from core.users u where u.id = i.assignee_id)
			 from projects.items i join core.notes n on n.id = i.note_id where i.note_id = $1::uuid and n.deleted_at is null`, id).
			Scan(&key, &title, &status, &priority, &typ, &due, &assignee)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, wrong
		}
		if err != nil {
			return nil, err
		}
		v["item.key"], v["item.title"], v["item.status"], v["item.priority"] = key, title, status, priority
		v["item.type"], v["item.due_date"], v["item.assignee"] = typ, optDate(due), opt(assignee)
	default:
		return nil, command.ErrInvalid("this template takes no source record")
	}
	return v, nil
}

// ---------- commands ----------

func checkKind(k string) error {
	for _, x := range kinds {
		if x == k {
			return nil
		}
	}
	return command.ErrInvalid("source_kind must be none, contact, deal, invoice or item")
}

// isViewRestricted: the page or one of its ancestors is view-restricted.
func isViewRestricted(c *command.Ctx, id string) (bool, error) {
	var r bool
	err := c.Tx.QueryRow(c.Context,
		`with recursive c as (select id, parent_id, restriction, 1 d from core.notes where id = $1::uuid
		   union all select p.id, p.parent_id, p.restriction, c.d + 1 from core.notes p join c on p.id = c.parent_id where c.d < 50)
		 select exists(select 1 from c where restriction = 'view')`, id).Scan(&r)
	return r, err
}

type template struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	SourceKind  string     `json:"source_kind"`
	UpdatedAt   command.Ts `json:"updated_at"`
	Builtin     bool       `json:"builtin,omitempty"`
	Category    string     `json:"category,omitempty"`
	Description string     `json:"description,omitempty"`
	Prompts     []prompt   `json:"prompts"`
	ContentMD   *string    `json:"content_md,omitempty"` // only when the caller asks for include_content (gallery preview)
}

func templateSave(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	rawName, err := o.ReqStr("name")
	if err != nil {
		return nil, err
	}
	sk, err := o.ReqStr("source_kind")
	if err != nil {
		return nil, err
	}
	if err := checkKind(sk); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(rawName)
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	space, err := access.AssertNoteAccess(c, noteID, false)
	if err != nil {
		return nil, err
	}
	readable, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	// A template is visible to every reader of the space, so it must not copy a page only some people may open.
	restricted, err := isViewRestricted(c, noteID)
	if err != nil {
		return nil, err
	}
	if restricted {
		return nil, command.ErrForbidden("a page with view restrictions cannot be saved as a template")
	}
	var title, md string
	var types []string
	err = c.Tx.QueryRow(c.Context, "select title, types, content_md from core.notes where id = $1::uuid and deleted_at is null", noteID).Scan(&title, &types, &md)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("note")
	}
	if err != nil {
		return nil, err
	}
	if types == nil {
		types = []string{}
	}
	kind := "board"
	if len(types) == 0 {
		kind = "page"
	} else if !containsAny(types, "whiteboard", "design") {
		return nil, command.ErrInvalid("only pages and boards can be templates")
	}
	var t template
	var at time.Time
	err = c.Tx.QueryRow(c.Context,
		`insert into docs.templates (workspace_id, name, kind, source_kind, types, title, content_md, created_by, space_id)
		 values ($1::uuid,$2,$3,$4,$5::text[],$6,$7,$8::uuid,$9::uuid)
		 on conflict (workspace_id, name) do update set kind = excluded.kind, source_kind = excluded.source_kind,
		   types = excluded.types, title = excluded.title, content_md = excluded.content_md,
		   space_id = excluded.space_id, updated_at = now()
		 where (docs.templates.space_id is null or docs.templates.space_id = any($10::text[]::uuid[]))
		   and (docs.templates.created_by = $8::uuid or $11)
		 returning id::text, name, kind, source_kind, updated_at`,
		c.WorkspaceID(), name, kind, sk, types, title, md, c.Principal.UserID, space, readable, c.Principal.Role >= command.Admin).
		Scan(&t.ID, &t.Name, &t.Kind, &t.SourceKind, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("a template with this name already exists")
	}
	if err != nil {
		return nil, err
	}
	t.UpdatedAt = command.Ts(at)
	if _, err := c.Emit("template.saved", &noteID, map[string]any{"template_id": t.ID, "name": t.Name}); err != nil {
		return nil, err
	}
	return t, nil
}

func containsAny(l []string, want ...string) bool {
	for _, s := range l {
		for _, w := range want {
			if s == w {
				return true
			}
		}
	}
	return false
}

func templateList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	sk, err := o.OptStr("source_kind")
	if err != nil {
		return nil, err
	}
	kind, err := o.OptStr("kind")
	if err != nil {
		return nil, err
	}
	withBody := false
	if o.Has("include_content") {
		if withBody, err = o.ReqBool("include_content"); err != nil {
			return nil, err
		}
	}
	readable, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, name, kind, source_kind, updated_at, content_md, title from docs.templates
		 where ($1::text is null or source_kind in ('none', $1)) and ($2::text is null or kind = $2)
		   and (space_id is null or space_id = any($3::text[]::uuid[])) order by name`, sk, kind, readable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []template{}
	// Built-ins first: pages with no source record, so a board or record filter excludes them.
	if (kind == nil || *kind == "page") && (sk == nil || *sk == "none") {
		for _, b := range builtins {
			t := template{ID: builtinPrefix + b.Key, Name: b.Name, Kind: "page", SourceKind: "none", Builtin: true, Category: b.Category, Description: b.Description, Prompts: promptsOf(b.Title, b.Body)}
			if withBody {
				t.ContentMD = &b.Body
			}
			out = append(out, t)
		}
	}
	for rows.Next() {
		var t template
		var at time.Time
		var body, title string
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.SourceKind, &at, &body, &title); err != nil {
			return nil, err
		}
		t.UpdatedAt = command.Ts(at)
		t.Prompts = promptsOf(title, body)
		if withBody {
			t.ContentMD = &body
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func templateDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("template_id")
	if err != nil {
		return nil, err
	}
	readable, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	// Only the author or an admin may delete; readers of the space may not.
	tag, err := c.Tx.Exec(c.Context,
		`delete from docs.templates where id = $1::uuid and (space_id is null or space_id = any($2::text[]::uuid[]))
		   and (created_by = $3::uuid or $4)`, id, readable, c.Principal.UserID, c.Principal.Role >= command.Admin)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("template")
	}
	if _, err := c.Emit("template.deleted", nil, map[string]any{"template_id": id}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func templateFields(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	sk, err := o.ReqStr("source_kind")
	if err != nil {
		return nil, err
	}
	if err := checkKind(sk); err != nil {
		return nil, err
	}
	out := []map[string]string{}
	for _, f := range catalogue(sk) {
		out = append(out, map[string]string{"path": f.Path, "label": f.Label})
	}
	return out, nil
}

type tpl struct {
	name, kind, sourceKind, title, content string
	types                                  []string
}

func loadTemplate(c *command.Ctx, id string) (tpl, error) {
	if key, ok := builtinKey(id); ok {
		b, found := findBuiltin(key)
		if !found {
			return tpl{}, command.ErrNotFound("template")
		}
		return tpl{name: b.Name, kind: "page", sourceKind: "none", title: b.Title, content: b.Body, types: []string{}}, nil
	}
	readable, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return tpl{}, err
	}
	var t tpl
	err = c.Tx.QueryRow(c.Context,
		`select name, kind, source_kind, types, title, content_md from docs.templates
		 where id = $1::uuid and (space_id is null or space_id = any($2::text[]::uuid[]))`, id, readable).
		Scan(&t.name, &t.kind, &t.sourceKind, &t.types, &t.title, &t.content)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, command.ErrNotFound("template")
	}
	if t.types == nil {
		t.types = []string{}
	}
	return t, err
}

// render returns (title, body, unresolved placeholders) for a template against its source record.
func render(c *command.Ctx, t tpl, source *string, answers map[string]string) (string, string, []string, error) {
	if t.sourceKind != "none" && source == nil {
		return "", "", nil, command.ErrInvalid("this template needs a " + t.sourceKind + " as its source")
	}
	v, err := resolve(c, t.sourceKind, source)
	if err != nil {
		return "", "", nil, err
	}
	missing := map[string]bool{}
	// An answer is text, never markup: a page body takes it escaped; a title and a board's JSON strings take it as is.
	bv := vals{}
	for k, x := range v {
		bv[k] = x
	}
	for k, a := range answers {
		v[askPrefix+k] = a
		bv[askPrefix+k] = a
		if t.kind != "board" {
			bv[askPrefix+k] = mdLiteral(a)
		}
	}
	title := substitute(t.title, v, missing)
	var body string
	if t.kind == "board" {
		if body, err = renderBoard(t.content, bv, missing); err != nil {
			return "", "", nil, err
		}
	} else {
		body = renderMarkdownAnswers(t.content, v, bv, missing)
	}
	un := []string{}
	for k := range missing {
		un = append(un, k)
	}
	sort.Strings(un)
	return title, body, un, nil
}

func templateRender(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	// A built-in id is checked before UUID parsing (7.6).
	tid, err := o.ReqStr("template_id")
	if err != nil {
		return nil, err
	}
	if _, isBuiltin := builtinKey(tid); !isBuiltin {
		if tid, err = o.ReqUUID("template_id"); err != nil {
			return nil, err
		}
	}
	source, err := o.OptUUID("source_id")
	if err != nil {
		return nil, err
	}
	spaceIn, err := o.OptUUID("space_id")
	if err != nil {
		return nil, err
	}
	parent, err := o.OptUUID("parent_id")
	if err != nil {
		return nil, err
	}
	titleIn, err := o.OptStr("title")
	if err != nil {
		return nil, err
	}
	var rawAnswers map[string]any
	if o.Has("answers") {
		raw, err := o.OptValue("answers")
		if err != nil {
			return nil, err
		}
		if string(raw) != "null" && (json.Unmarshal(raw, &rawAnswers) != nil || rawAnswers == nil) {
			return nil, command.ErrInvalid("answers must be an object of text values")
		}
	}
	t, err := loadTemplate(c, tid)
	if err != nil {
		return nil, err
	}
	answers, err := checkAnswers(rawAnswers, t)
	if err != nil {
		return nil, err
	}
	title, body, unresolved, err := render(c, t, source, answers)
	if err != nil {
		return nil, err
	}
	var space string
	switch {
	case spaceIn != nil:
		space = *spaceIn
	case parent != nil: // the new page goes under this page, in its space (InsertNote checks edit on the parent)
		if space, err = access.AssertNoteAccess(c, *parent, true); err != nil {
			return nil, err
		}
	case source != nil:
		if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid", *source).Scan(&space); err != nil {
			return nil, err
		}
	default:
		return nil, command.ErrInvalid("space_id is required for a template without a source")
	}
	if titleIn != nil && strings.TrimSpace(*titleIn) != "" {
		title = *titleIn
	}
	if strings.TrimSpace(title) == "" {
		title = t.name
	}
	note, err := notes.InsertNote(c, space, title, parent, &body, t.types)
	if err != nil {
		return nil, err
	}
	// A built-in has no row to bind to and no record to refresh from.
	if _, isBuiltin := builtinKey(tid); !isBuiltin {
		var stored []byte // the answers live on the binding, because refresh renders again from the template
		if len(answers) > 0 {
			stored, _ = json.Marshal(answers)
		}
		if _, err := c.Tx.Exec(c.Context,
			"insert into docs.template_bindings (note_id, workspace_id, template_id, source_kind, source_id, answers) values ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::jsonb)",
			note.ID, c.WorkspaceID(), tid, t.sourceKind, source, stored); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("template.rendered", &note.ID, map[string]any{"template_id": tid, "source_id": source, "unresolved": unresolved}); err != nil {
		return nil, err
	}
	return map[string]any{"note": note, "unresolved": unresolved}, nil
}

func templateRefresh(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, noteID, true); err != nil {
		return nil, err
	}
	var tid, source *string
	var stored []byte
	err = c.Tx.QueryRow(c.Context, "select template_id::text, source_id::text, answers from docs.template_bindings where note_id = $1::uuid", noteID).Scan(&tid, &source, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrInvalid("this note was not created from a template")
	}
	if err != nil {
		return nil, err
	}
	if tid == nil {
		return nil, command.ErrInvalid("the template this note came from was deleted")
	}
	t, err := loadTemplate(c, *tid)
	if err != nil {
		return nil, err
	}
	if t.kind != "page" {
		return nil, command.ErrInvalid("boards are one-shot: render the template again to get fresh data")
	}
	var answers map[string]string
	if stored != nil {
		_ = json.Unmarshal(stored, &answers) // a prompt the template no longer has is simply not used
	}
	_, body, unresolved, err := render(c, t, source, answers)
	if err != nil {
		return nil, err
	}
	var version int32
	if err := c.Tx.QueryRow(c.Context,
		"update core.notes set content_md = $2, updated_at = now(), version = version + 1 where id = $1::uuid returning version", noteID, body).Scan(&version); err != nil {
		return nil, err
	}
	if _, err := c.Emit("note.updated", &noteID, map[string]any{"changed": []string{"content"}, "version": version}); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": noteID, "unresolved": unresolved}, nil
}
