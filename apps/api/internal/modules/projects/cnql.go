package projects

// CNQL: a JQL-compatible query language for items (port of crates/cnote-projects/src/cnql.rs). Parse builds an
// AST, Compile turns it into a SQL WHERE fragment plus an ORDER BY with numbered placeholders. Values are never
// interpolated: every literal becomes a bind. Error texts and column positions are identical to Rust.

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
)

// FromSQL is the FROM clause the compiled fragments refer to (aliases i, n, sp).
const FromSQL = "projects.items i join core.notes n on n.id = i.note_id join core.spaces sp on sp.id = i.space_id"
const DefaultOrder = `i.rank collate "C" asc, i.number asc`

type CnqlError struct {
	Pos int // 1-based column in the query text
	Msg string
}

func (e *CnqlError) Error() string { return fmt.Sprintf("%s (at column %d)", e.Msg, e.Pos) }

func cerr(pos int, f string, a ...any) *CnqlError {
	return &CnqlError{Pos: pos, Msg: fmt.Sprintf(f, a...)}
}

// ---------- lexer ----------

type tokKind int

const (
	tWord tokKind = iota
	tStr
	tEq
	tNe
	tGt
	tGe
	tLt
	tLe
	tTilde
	tNotTilde
	tLParen
	tRParen
	tComma
	tEOF
)

type tok struct {
	kind tokKind
	s    string
	pos  int
}

func (t tok) describe() string {
	switch t.kind {
	case tWord:
		return "'" + t.s + "'"
	case tStr:
		return `"` + t.s + `"`
	case tEq:
		return "'='"
	case tNe:
		return "'!='"
	case tGt:
		return "'>'"
	case tGe:
		return "'>='"
	case tLt:
		return "'<'"
	case tLe:
		return "'<='"
	case tTilde:
		return "'~'"
	case tNotTilde:
		return "'!~'"
	case tLParen:
		return "'('"
	case tRParen:
		return "')'"
	case tComma:
		return "','"
	}
	return "end of query"
}

func isWordChar(c rune) bool {
	return isRustAlnum(c) || strings.ContainsRune("_-.@+:/", c)
}

func lex(input string) ([]tok, *CnqlError) {
	chars := []rune(input)
	var out []tok
	i := 0
	for i < len(chars) {
		c := chars[i]
		pos := i + 1
		var next rune = -1
		if i+1 < len(chars) {
			next = chars[i+1]
		}
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			out = append(out, tok{kind: tLParen, pos: pos})
			i++
		case c == ')':
			out = append(out, tok{kind: tRParen, pos: pos})
			i++
		case c == ',':
			out = append(out, tok{kind: tComma, pos: pos})
			i++
		case c == '=':
			out = append(out, tok{kind: tEq, pos: pos})
			i++
		case c == '~':
			out = append(out, tok{kind: tTilde, pos: pos})
			i++
		case c == '!' && next == '=':
			out = append(out, tok{kind: tNe, pos: pos})
			i += 2
		case c == '!' && next == '~':
			out = append(out, tok{kind: tNotTilde, pos: pos})
			i += 2
		case c == '>' || c == '<':
			eq := next == '='
			k := map[[2]bool]tokKind{{true, true}: tGe, {true, false}: tGt, {false, true}: tLe, {false, false}: tLt}[[2]bool{c == '>', eq}]
			out = append(out, tok{kind: k, pos: pos})
			i++
			if eq {
				i++
			}
		case c == '"' || c == '\'':
			var sb strings.Builder
			j := i + 1
			for {
				if j >= len(chars) {
					return nil, cerr(pos, "unterminated string")
				}
				ch := chars[j]
				if ch == '\\' && j+1 < len(chars) {
					sb.WriteRune(chars[j+1])
					j += 2
				} else if ch == c {
					break
				} else {
					sb.WriteRune(ch)
					j++
				}
			}
			out = append(out, tok{kind: tStr, s: sb.String(), pos: pos})
			i = j + 1
		case isWordChar(c):
			j := i
			for j < len(chars) && isWordChar(chars[j]) {
				j++
			}
			out = append(out, tok{kind: tWord, s: string(chars[i:j]), pos: pos})
			i = j
		case c == '!':
			return nil, cerr(pos, "'!' must be followed by '=' or '~'")
		default:
			return nil, cerr(pos, "unexpected character '%c'", c)
		}
	}
	out = append(out, tok{kind: tEOF, pos: len(chars) + 1})
	return out, nil
}

// ---------- AST ----------

type Field int

const (
	FProject Field = iota
	FKey
	FStatus
	FStatusCategory
	FAssignee
	FReporter
	FPriority
	FType
	FLabels
	FSprint
	FFixVersion
	FComponent
	FCreated
	FUpdated
	FDue
	FResolved
	FText
	FRank // ORDER BY only
)

const fieldNames = "project, key, status, statusCategory, assignee, reporter, priority, type, labels, sprint, fixVersion, component, created, updated, due, resolved, text"

var fieldByName = map[string]Field{
	"project": FProject, "key": FKey, "issuekey": FKey, "status": FStatus, "statuscategory": FStatusCategory,
	"assignee": FAssignee, "reporter": FReporter, "priority": FPriority, "type": FType, "issuetype": FType,
	"labels": FLabels, "label": FLabels, "sprint": FSprint, "fixversion": FFixVersion, "component": FComponent,
	"created": FCreated, "updated": FUpdated, "due": FDue, "duedate": FDue, "resolved": FResolved,
	"resolutiondate": FResolved, "text": FText, "rank": FRank,
}

func parseField(s string) (Field, bool) { f, ok := fieldByName[asciiLower(s)]; return f, ok }

func (f Field) Name() string {
	return [...]string{"project", "key", "status", "statusCategory", "assignee", "reporter", "priority", "type", "labels",
		"sprint", "fixVersion", "component", "created", "updated", "due", "resolved", "text", "rank"}[f]
}

type Val struct {
	Func bool
	Lit  string // literal, or the function name
	Args []string
}

type valPos struct {
	V   Val
	Pos int
}

type Op int

const (
	OpEq Op = iota
	OpNe
	OpGt
	OpGe
	OpLt
	OpLe
	OpLike
	OpNotLike
)

type exprKind int

const (
	eAnd exprKind = iota
	eOr
	eNot
	eCmp
	eIn
	eEmpty
)

type Expr struct {
	kind    exprKind
	parts   []*Expr // and/or; not uses parts[0]
	field   Field
	op      Op
	val     valPos
	vals    []valPos
	negated bool
	pos     int
}

type OrderTerm struct {
	Field Field
	Desc  bool
	Pos   int
}

type Query struct {
	Expr  *Expr
	Order []OrderTerm
}

// ---------- parser ----------

type parser struct {
	toks []tok
	i    int
}

func (p *parser) peek() tok { return p.toks[p.i] }
func (p *parser) pos() int  { return p.toks[p.i].pos }
func (p *parser) bump() tok {
	t := p.toks[p.i]
	if p.i+1 < len(p.toks) {
		p.i++
	}
	return t
}
func (p *parser) isKw(kw string) bool { t := p.peek(); return t.kind == tWord && eqFold(t.s, kw) }
func (p *parser) peekKwAt(off int, kw string) bool {
	if p.i+off >= len(p.toks) {
		return false
	}
	t := p.toks[p.i+off]
	return t.kind == tWord && eqFold(t.s, kw)
}

func (p *parser) orExpr() (*Expr, *CnqlError) {
	first, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	parts := []*Expr{first}
	for p.isKw("OR") {
		p.bump()
		e, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		parts = append(parts, e)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return &Expr{kind: eOr, parts: parts}, nil
}

func (p *parser) andExpr() (*Expr, *CnqlError) {
	first, err := p.notExpr()
	if err != nil {
		return nil, err
	}
	parts := []*Expr{first}
	for p.isKw("AND") {
		p.bump()
		e, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		parts = append(parts, e)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return &Expr{kind: eAnd, parts: parts}, nil
}

func (p *parser) notExpr() (*Expr, *CnqlError) {
	if p.isKw("NOT") {
		p.bump()
		e, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		return &Expr{kind: eNot, parts: []*Expr{e}}, nil
	}
	return p.primary()
}

func (p *parser) primary() (*Expr, *CnqlError) {
	if p.peek().kind == tLParen {
		open := p.pos()
		p.bump()
		e, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, cerr(p.pos(), "expected ')' to close '(' at column %d", open)
		}
		p.bump()
		return e, nil
	}
	t := p.bump()
	if t.kind != tWord {
		return nil, cerr(t.pos, "expected a field name, found %s", t.describe())
	}
	name := t.s
	field, ok := parseField(name)
	if !ok {
		return nil, cerr(t.pos, "unknown field '%s'; valid fields: %s", name, fieldNames)
	}
	opPos := p.pos()
	ops := map[tokKind]Op{tEq: OpEq, tNe: OpNe, tGt: OpGt, tGe: OpGe, tLt: OpLt, tLe: OpLe, tTilde: OpLike, tNotTilde: OpNotLike}
	if op, ok := ops[p.peek().kind]; ok {
		p.bump()
		val, err := p.value()
		if err != nil {
			return nil, err
		}
		return &Expr{kind: eCmp, field: field, op: op, val: val, pos: opPos}, nil
	}
	if p.isKw("IN") || (p.isKw("NOT") && p.peekKwAt(1, "IN")) {
		negated := p.isKw("NOT")
		if negated {
			p.bump()
		}
		p.bump()
		// JQL allows a bare function: `sprint in openSprints()`.
		if p.peek().kind == tWord && p.i+1 < len(p.toks) && p.toks[p.i+1].kind == tLParen {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			return &Expr{kind: eIn, field: field, vals: []valPos{v}, negated: negated, pos: opPos}, nil
		}
		if p.peek().kind != tLParen {
			return nil, cerr(p.pos(), "expected '(' after IN, found %s", p.peek().describe())
		}
		p.bump()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		vals := []valPos{v}
		for p.peek().kind == tComma {
			p.bump()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		if p.peek().kind != tRParen {
			return nil, cerr(p.pos(), "expected ',' or ')', found %s", p.peek().describe())
		}
		p.bump()
		return &Expr{kind: eIn, field: field, vals: vals, negated: negated, pos: opPos}, nil
	}
	if p.isKw("IS") {
		p.bump()
		negated := p.isKw("NOT")
		if negated {
			p.bump()
		}
		if p.isKw("EMPTY") || p.isKw("NULL") {
			p.bump()
			return &Expr{kind: eEmpty, field: field, negated: negated, pos: opPos}, nil
		}
		return nil, cerr(p.pos(), "expected EMPTY after IS, found %s", p.peek().describe())
	}
	return nil, cerr(opPos, "expected an operator (=, !=, >, >=, <, <=, ~, !~, IN, NOT IN, IS EMPTY, IS NOT EMPTY) after '%s', found %s", name, p.peek().describe())
}

func (p *parser) value() (valPos, *CnqlError) {
	t := p.bump()
	switch t.kind {
	case tStr:
		return valPos{Val{Lit: t.s}, t.pos}, nil
	case tWord:
		if p.peek().kind != tLParen {
			return valPos{Val{Lit: t.s}, t.pos}, nil
		}
		p.bump()
		args := []string{}
		if p.peek().kind != tRParen {
			for {
				a := p.bump()
				if a.kind != tStr && a.kind != tWord {
					return valPos{}, cerr(a.pos, "expected a function argument, found %s", a.describe())
				}
				args = append(args, a.s)
				if p.peek().kind == tComma {
					p.bump()
				} else {
					break
				}
			}
		}
		if p.peek().kind != tRParen {
			return valPos{}, cerr(p.pos(), "expected ')' after arguments of %s()", t.s)
		}
		p.bump()
		return valPos{Val{Func: true, Lit: t.s, Args: args}, t.pos}, nil
	}
	return valPos{}, cerr(t.pos, "expected a value, found %s", t.describe())
}

func ParseCNQL(input string) (*Query, *CnqlError) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	q := &Query{}
	if p.peek().kind != tEOF && !p.isKw("ORDER") {
		if q.Expr, err = p.orExpr(); err != nil {
			return nil, err
		}
	}
	if p.isKw("ORDER") {
		p.bump()
		if !p.isKw("BY") {
			return nil, cerr(p.pos(), "expected BY after ORDER")
		}
		p.bump()
		for {
			t := p.bump()
			if t.kind != tWord {
				return nil, cerr(t.pos, "expected a field name, found %s", t.describe())
			}
			f, ok := parseField(t.s)
			if !ok {
				return nil, cerr(t.pos, "unknown field '%s'; valid fields: %s, rank", t.s, fieldNames)
			}
			desc := false
			if p.isKw("DESC") {
				p.bump()
				desc = true
			} else if p.isKw("ASC") {
				p.bump()
			}
			q.Order = append(q.Order, OrderTerm{f, desc, t.pos})
			if p.peek().kind == tComma {
				p.bump()
			} else {
				break
			}
		}
	}
	if p.peek().kind != tEOF {
		return nil, cerr(p.pos(), "unexpected %s; expected AND, OR or ORDER BY", p.peek().describe())
	}
	return q, nil
}

// ---------- compiler ----------

type BindKind int

const (
	BText BindKind = iota
	BTextArr
	BUUIDArr
	BTs
	BDate
	BInt
)

// Bind is one compiled parameter. UUIDs are canonical strings.
type Bind struct {
	Kind  BindKind
	Text  string
	Texts []string
	T     time.Time
	Int   int32
}

// Value converts the bind to a pgx argument matching the cast written in the SQL.
func (b Bind) Value() any {
	switch b.Kind {
	case BText:
		return b.Text
	case BTextArr:
		return b.Texts
	case BUUIDArr:
		out := make([]pgtype.UUID, len(b.Texts))
		for i, s := range b.Texts {
			_ = out[i].Scan(s)
		}
		return out
	case BTs:
		return chronoMicros(b.T)
	case BDate:
		return pgtype.Date{Time: time.Date(b.T.Year(), b.T.Month(), b.T.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	}
	return b.Int
}

// Cx is the evaluation context for currentUser() and now().
type Cx struct {
	Now  time.Time
	User *string
}

type Compiled struct {
	Where string // boolean SQL (`true` for an empty query)
	Order string // comma-separated ORDER BY terms
	Binds []Bind
}

// Args are the binds as pgx arguments, in placeholder order.
func (c *Compiled) Args() []any {
	out := make([]any, len(c.Binds))
	for i, b := range c.Binds {
		out[i] = b.Value()
	}
	return out
}

type gen struct {
	cx    *Cx
	first int
	binds []Bind
	leap  bool // the last parsed date was a leap second: chrono's date_naive stays on the same day
}

const priorityRank = "(case i.priority when 'urgent' then 4 when 'high' then 3 when 'medium' then 2 when 'low' then 1 else 0 end)"

func CompileCNQL(q *Query, cx *Cx, firstParam int) (*Compiled, *CnqlError) {
	g := &gen{cx: cx, first: firstParam}
	where := "true"
	if q.Expr != nil {
		w, err := g.expr(q.Expr)
		if err != nil {
			return nil, err
		}
		where = w
	}
	var terms []string
	for _, o := range q.Order {
		dir := "asc"
		if o.Desc {
			dir = "desc"
		}
		var col string
		switch o.Field {
		case FKey:
			col = "sp.key " + dir + ", i.number"
		case FCreated:
			col = "n.created_at"
		case FUpdated:
			col = "n.updated_at"
		case FDue:
			col = "i.due_date"
		case FResolved:
			col = "i.resolved_at"
		case FPriority:
			col = priorityRank
		case FStatus:
			col = "i.status"
		case FRank:
			col = `i.rank collate "C"`
		default:
			return nil, cerr(o.Pos, "cannot ORDER BY '%s'", o.Field.Name())
		}
		terms = append(terms, col+" "+dir+" nulls last")
	}
	terms = append(terms, "i.number asc")
	order := DefaultOrder
	if len(q.Order) > 0 {
		order = strings.Join(terms, ", ")
	}
	return &Compiled{Where: where, Order: order, Binds: g.binds}, nil
}

type predKind int

const (
	pEq predKind = iota
	pCmp
	pLike
	pEmpty
)

type pred struct {
	kind predKind
	vs   []valPos // pEq
	op   Op       // pCmp
	v    valPos   // pCmp, pLike
}

func (g *gen) push(b Bind, cast string) string {
	g.binds = append(g.binds, b)
	return fmt.Sprintf("$%d::%s", g.first+len(g.binds)-1, cast)
}

func neg(p string) string { return "(not coalesce(" + p + ", false))" }

func sqlOp(op Op) string {
	switch op {
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpNe:
		return "<>"
	}
	return "="
}

func (g *gen) exprs(es []*Expr, sep string) (string, *CnqlError) {
	parts := make([]string, len(es))
	for i, x := range es {
		s, err := g.expr(x)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, sep) + ")", nil
}

func (g *gen) expr(e *Expr) (string, *CnqlError) {
	switch e.kind {
	case eAnd:
		return g.exprs(e.parts, " and ")
	case eOr:
		return g.exprs(e.parts, " or ")
	case eNot:
		s, err := g.expr(e.parts[0])
		if err != nil {
			return "", err
		}
		return "(not coalesce(" + s + ", false))", nil
	case eCmp:
		switch e.op {
		case OpEq:
			return g.pred(e.field, pred{kind: pEq, vs: []valPos{e.val}}, e.pos)
		case OpNe:
			s, err := g.pred(e.field, pred{kind: pEq, vs: []valPos{e.val}}, e.pos)
			if err != nil {
				return "", err
			}
			return neg(s), nil
		case OpLike:
			return g.pred(e.field, pred{kind: pLike, v: e.val}, e.pos)
		case OpNotLike:
			s, err := g.pred(e.field, pred{kind: pLike, v: e.val}, e.pos)
			if err != nil {
				return "", err
			}
			return neg(s), nil
		}
		return g.pred(e.field, pred{kind: pCmp, op: e.op, v: e.val}, e.pos)
	case eIn:
		s, err := g.pred(e.field, pred{kind: pEq, vs: e.vals}, e.pos)
		if err != nil {
			return "", err
		}
		if e.negated {
			s = neg(s)
		}
		return s, nil
	}
	s, err := g.pred(e.field, pred{kind: pEmpty}, e.pos)
	if err != nil {
		return "", err
	}
	if e.negated {
		s = "(not (" + s + "))"
	}
	return s, nil
}

func (g *gen) plain(f Field, v valPos, norm func(string) (string, string)) (string, *CnqlError) {
	if v.V.Func {
		return "", cerr(v.Pos, "function %s() cannot be used with '%s'", v.V.Lit, f.Name())
	}
	out, m := norm(v.V.Lit)
	if m != "" {
		return "", cerr(v.Pos, "invalid value '%s' for '%s': %s", v.V.Lit, f.Name(), m)
	}
	return out, nil
}

func (g *gen) plains(f Field, vs []valPos, norm func(string) (string, string)) ([]string, *CnqlError) {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		s, err := g.plain(f, v, norm)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func lowerNorm(s string) (string, string) { return rustLower(s), "" }

func unsupported(pos int, f Field, what string) (string, *CnqlError) {
	return "", cerr(pos, "operator %s is not supported for field '%s'", what, f.Name())
}

const opCmpList = "<, <=, >, >="

func (g *gen) pred(f Field, p pred, pos int) (string, *CnqlError) {
	switch f {
	case FProject, FKey, FStatus, FType:
		col := map[Field]string{FProject: "sp.key", FKey: "i.key", FStatus: "i.status", FType: "i.item_type"}[f]
		switch p.kind {
		case pEq:
			vals, err := g.plains(f, p.vs, lowerNorm)
			if err != nil {
				return "", err
			}
			b := g.push(Bind{Kind: BTextArr, Texts: vals}, "text[]")
			return "(lower(" + col + ") = any(" + b + "))", nil
		case pEmpty:
			return "(" + col + " is null)", nil
		case pCmp:
			return unsupported(pos, f, opCmpList)
		}
		return unsupported(pos, f, "~")
	case FStatusCategory:
		switch p.kind {
		case pEq:
			vals, err := g.plains(f, p.vs, func(s string) (string, string) {
				switch strings.NewReplacer(" ", "_", "-", "_").Replace(rustLower(s)) {
				case "todo", "to_do", "open":
					return "todo", ""
				case "in_progress", "inprogress":
					return "in_progress", ""
				case "done", "closed", "complete":
					return "done", ""
				}
				return "", "expected To Do, In Progress or Done"
			})
			if err != nil {
				return "", err
			}
			b := g.push(Bind{Kind: BTextArr, Texts: vals}, "text[]")
			return "(i.status_category = any(" + b + "))", nil
		case pEmpty:
			return "(i.status_category is null)", nil
		case pCmp:
			return unsupported(pos, f, opCmpList)
		}
		return unsupported(pos, f, "~")
	case FPriority:
		norm := func(s string) (string, string) {
			switch rustLower(s) {
			case "urgent", "highest":
				return "urgent", ""
			case "high":
				return "high", ""
			case "medium":
				return "medium", ""
			case "low":
				return "low", ""
			case "none", "lowest":
				return "none", ""
			}
			return "", "expected Urgent, High, Medium, Low or None"
		}
		switch p.kind {
		case pEq:
			vals, err := g.plains(f, p.vs, norm)
			if err != nil {
				return "", err
			}
			b := g.push(Bind{Kind: BTextArr, Texts: vals}, "text[]")
			return "(i.priority = any(" + b + "))", nil
		case pCmp:
			s, err := g.plain(f, p.v, norm)
			if err != nil {
				return "", err
			}
			rank := map[string]int32{"urgent": 4, "high": 3, "medium": 2, "low": 1}[s]
			b := g.push(Bind{Kind: BInt, Int: rank}, "int")
			return "(" + priorityRank + " " + sqlOp(p.op) + " " + b + ")", nil
		case pEmpty:
			return "(i.priority is null)", nil
		}
		return unsupported(pos, f, "~")
	case FAssignee, FReporter:
		assignee := f == FAssignee
		switch p.kind {
		case pEq:
			var ids, names []string
			for _, v := range p.vs {
				switch {
				case v.V.Func && eqFold(v.V.Lit, "currentUser"):
					if g.cx.User == nil {
						return "", cerr(v.Pos, "currentUser() needs a signed-in user")
					}
					ids = append(ids, *g.cx.User)
				case v.V.Func:
					return "", cerr(v.Pos, "function %s() cannot be used with '%s'", v.V.Lit, f.Name())
				default:
					if u, ok := parseUUID(v.V.Lit); ok {
						ids = append(ids, u)
					} else {
						names = append(names, rustLower(v.V.Lit))
					}
				}
			}
			var parts []string
			userCol := "i.reporter_id"
			if assignee {
				userCol = "i.assignee_id"
			}
			if len(ids) > 0 {
				b := g.push(Bind{Kind: BUUIDArr, Texts: ids}, "uuid[]")
				parts = append(parts, userCol+" = any("+b+")")
				if assignee {
					parts = append(parts, "i.assignee_agent_id = any("+b+")")
				}
			}
			if len(names) > 0 {
				b := g.push(Bind{Kind: BTextArr, Texts: names}, "text[]")
				parts = append(parts, userCol+" in (select m.user_id from core.memberships m join core.users u on u.id = m.user_id where lower(u.email) = any("+b+") or lower(u.name) = any("+b+"))")
				if assignee {
					parts = append(parts, "i.assignee_agent_id in (select a.id from core.agents a where lower(a.name) = any("+b+"))")
				}
			}
			return "(" + strings.Join(parts, " or ") + ")", nil
		case pEmpty:
			if assignee {
				return "(i.assignee_id is null and i.assignee_agent_id is null)", nil
			}
			return "(i.reporter_id is null)", nil
		case pCmp:
			return unsupported(pos, f, opCmpList)
		}
		return unsupported(pos, f, "~")
	case FLabels:
		switch p.kind {
		case pEq:
			vals, err := g.plains(f, p.vs, lowerNorm)
			if err != nil {
				return "", err
			}
			b := g.push(Bind{Kind: BTextArr, Texts: vals}, "text[]")
			return "(exists (select 1 from unnest(i.labels) l where lower(l) = any(" + b + ")))", nil
		case pEmpty:
			return "(cardinality(i.labels) = 0)", nil
		case pCmp:
			return unsupported(pos, f, opCmpList)
		}
		return unsupported(pos, f, "~")
	case FSprint:
		return g.reference(f, p, pos, "i.sprint_id", "projects.sprints", [][2]string{
			{"openSprints", "state = 'active'"}, {"closedSprints", "state = 'closed'"}, {"futureSprints", "state = 'future'"}})
	case FFixVersion:
		return g.reference(f, p, pos, "i.fix_version_id", "projects.versions", [][2]string{
			{"releasedVersions", "released"}, {"unreleasedVersions", "not released"}})
	case FComponent:
		return g.reference(f, p, pos, "i.component_id", "projects.components", nil)
	case FCreated, FUpdated, FResolved, FDue:
		col, isDate := map[Field]string{FCreated: "n.created_at", FUpdated: "n.updated_at", FResolved: "i.resolved_at", FDue: "i.due_date"}[f], f == FDue
		switch p.kind {
		case pEmpty:
			return "(" + col + " is null)", nil
		case pLike:
			return unsupported(pos, f, "~")
		case pCmp:
			ts, _, err := g.date(f, p.v)
			if err != nil {
				return "", err
			}
			b := g.bindDate(ts, isDate)
			return "(" + col + " " + sqlOp(p.op) + " " + b + ")", nil
		}
		var parts []string
		for _, v := range p.vs {
			ts, dayOnly, err := g.date(f, v)
			if err != nil {
				return "", err
			}
			if dayOnly && !isDate {
				a := g.bindDate(ts, false)
				b := g.bindDate(ts.Add(24*time.Hour), false)
				parts = append(parts, "("+col+" >= "+a+" and "+col+" < "+b+")")
			} else {
				parts = append(parts, col+" = "+g.bindDate(ts, isDate))
			}
		}
		return "(" + strings.Join(parts, " or ") + ")", nil
	case FText:
		switch p.kind {
		case pLike:
			s, err := g.plain(f, p.v, func(s string) (string, string) { return s, "" })
			if err != nil {
				return "", err
			}
			esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
			b := g.push(Bind{Kind: BText, Text: "%" + esc + "%"}, "text")
			return "(n.title ilike " + b + " or n.content_md ilike " + b + " or i.key ilike " + b + ")", nil
		case pEmpty:
			return "(n.title = '' and n.content_md = '')", nil
		}
		return unsupported(pos, f, "=, !=, <, >, IN (use ~ for text search)")
	}
	return "", cerr(pos, "'rank' can only be used in ORDER BY")
}

// reference: sprint / fixVersion / component match by id, by name, or by a named function.
func (g *gen) reference(f Field, p pred, pos int, col, table string, funcs [][2]string) (string, *CnqlError) {
	switch p.kind {
	case pEq:
		var ids, names, parts []string
		for _, v := range p.vs {
			if v.V.Func {
				cond := ""
				for _, fn := range funcs {
					if eqFold(fn[0], v.V.Lit) {
						cond = fn[1]
						break
					}
				}
				if cond == "" {
					return "", cerr(v.Pos, "function %s() cannot be used with '%s'", v.V.Lit, f.Name())
				}
				parts = append(parts, col+" in (select id from "+table+" where "+cond+")")
			} else if u, ok := parseUUID(v.V.Lit); ok {
				ids = append(ids, u)
			} else {
				names = append(names, rustLower(v.V.Lit))
			}
		}
		if len(ids) > 0 {
			b := g.push(Bind{Kind: BUUIDArr, Texts: ids}, "uuid[]")
			parts = append(parts, col+" = any("+b+")")
		}
		if len(names) > 0 {
			b := g.push(Bind{Kind: BTextArr, Texts: names}, "text[]")
			parts = append(parts, col+" in (select id from "+table+" where lower(name) = any("+b+"))")
		}
		return "(" + strings.Join(parts, " or ") + ")", nil
	case pEmpty:
		return "(" + col + " is null)", nil
	case pCmp:
		return unsupported(pos, f, opCmpList)
	}
	return unsupported(pos, f, "~")
}

func (g *gen) date(f Field, v valPos) (time.Time, bool, *CnqlError) {
	g.leap = false
	now := g.cx.Now
	bad := func(m string) (time.Time, bool, *CnqlError) {
		return time.Time{}, false, cerr(v.Pos, "invalid date for '%s': %s", f.Name(), m)
	}
	if v.V.Func {
		var base time.Time
		switch asciiLower(v.V.Lit) {
		case "now":
			base = now
		case "startofday":
			base = startOfDay(now)
		case "startofweek":
			base = startOfDay(now).AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
		case "startofmonth":
			base = startOfDay(now).AddDate(0, 0, -(now.Day() - 1))
		default:
			return bad(fmt.Sprintf("unknown function %s(); use now(), startOfDay(), startOfWeek() or startOfMonth()", v.V.Lit))
		}
		if len(v.V.Args) == 0 {
			return base, false, nil
		}
		off := v.V.Args[0]
		t, ok := relative(base, off)
		if !ok {
			return bad(fmt.Sprintf("bad offset '%s', expected e.g. -7d, 2w, 3h", off))
		}
		return t, false, nil
	}
	s := v.V.Lit
	if t, ok := relative(now, s); ok {
		return t, false, nil
	}
	if t, ok := chronoDate(s); ok {
		return t, true, nil
	}
	if t, ok, leap := chronoDateTime(s); ok {
		g.leap = leap
		return t, false, nil
	}
	if t, ok, leap := chronoRFC3339(s); ok {
		g.leap = leap
		return t, false, nil
	}
	return bad(fmt.Sprintf("'%s' is not a date; use YYYY-MM-DD, \"YYYY-MM-DD HH:MM\", a relative value like -7d, or now()", s))
}

func (g *gen) bindDate(ts time.Time, asDate bool) string {
	if asDate {
		if g.leap {
			ts = ts.Add(-time.Second)
		}
		return g.push(Bind{Kind: BDate, T: ts}, "date")
	}
	return g.push(Bind{Kind: BTs, T: ts}, "timestamptz")
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// relative parses `-7d`, `+2w`, `3h`: m minutes, h hours, d days, w weeks, M months, y years.
func relative(base time.Time, s string) (time.Time, bool) {
	if s == "" {
		return base, false
	}
	runes := []rune(s)
	unit := runes[len(runes)-1]
	num, ok := parseI64(s[:len(s)-len(string(unit))])
	if !ok {
		return base, false
	}
	switch unit {
	case 'm':
		return addScaled(base, num, 60)
	case 'h':
		return addScaled(base, num, 3600)
	case 'd':
		return addScaled(base, num, 86400)
	case 'w':
		return addScaled(base, num, 7*86400)
	case 'M', 'y':
		abs := uint64(num)
		if num < 0 {
			abs = -uint64(num)
		}
		if unit == 'y' {
			abs *= 12
		}
		if abs > 1<<32-1 {
			return base, false
		}
		months := int64(abs)
		if num < 0 {
			months = -months
		}
		return addMonths(base, months)
	}
	return base, false
}

func addScaled(base time.Time, n, mul int64) (time.Time, bool) {
	if n > 1<<40 || n < -(1<<40) {
		return base, false
	}
	return addSecs(base, n*mul)
}

// parseI64 is Rust's i64::from_str: optional sign, ASCII digits only.
func parseI64(s string) (int64, bool) {
	i := 0
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		i = 1
	}
	if i == len(s) {
		return 0, false
	}
	var n uint64
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		d := uint64(s[i] - '0')
		if n > (1<<64-1-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	if neg {
		if n > 1<<63 {
			return 0, false
		}
		return -int64(n), true
	}
	if n > 1<<63-1 {
		return 0, false
	}
	return int64(n), true
}
