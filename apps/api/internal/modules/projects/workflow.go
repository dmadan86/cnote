package projects

// Workflow engine (ADR-007): a pure state machine over a JSON definition. Port of crates/cnote-projects/src/workflow.rs.
// No I/O here; commands load the definition, build a GuardInput from the item, and apply the chosen transition.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

type Status struct {
	Name     string `json:"name"`
	Category string `json:"category"` // todo | in_progress | done
}

func validCategory(s string) bool { return s == "todo" || s == "in_progress" || s == "done" }

// FromSpec is `"*"` (any status) or a list of status names (serde untagged: Any(String) | Statuses(Vec<String>)).
type FromSpec struct {
	Any      *string
	Statuses []string
}

func (f FromSpec) MarshalJSON() ([]byte, error) {
	if f.Any != nil {
		return json.Marshal(*f.Any)
	}
	if f.Statuses == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(f.Statuses)
}

func (f FromSpec) matches(status string) bool {
	if f.Any != nil {
		return true
	}
	for _, s := range f.Statuses {
		if eqFold(s, status) {
			return true
		}
	}
	return false
}

// Guard: required_fields {fields} | min_role {role} | evidence {event}.
type Guard struct {
	Type   string
	Fields []string
	Role   string
	Event  string
}

func (g Guard) MarshalJSON() ([]byte, error) {
	switch g.Type {
	case "required_fields":
		return json.Marshal(map[string]any{"type": g.Type, "fields": g.Fields})
	case "min_role":
		return json.Marshal(map[string]any{"type": g.Type, "role": g.Role})
	}
	return json.Marshal(map[string]any{"type": g.Type, "event": g.Event})
}

// PostAction: set_field {field, value} | assign_reporter | emit {event}.
type PostAction struct {
	Type  string
	Field string
	Value json.RawMessage
	Event string
}

func (a PostAction) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case "set_field":
		v, err := notes.NormValue(a.Value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"type": a.Type, "field": a.Field, "value": v})
	case "emit":
		return json.Marshal(map[string]any{"type": a.Type, "event": a.Event})
	}
	return json.Marshal(map[string]any{"type": a.Type})
}

type Transition struct {
	Name        string       `json:"name"`
	From        FromSpec     `json:"from"`
	To          string       `json:"to"`
	Guards      []Guard      `json:"guards,omitempty"`
	PostActions []PostAction `json:"post_actions,omitempty"`
}

type Definition struct {
	Statuses    []Status     `json:"statuses"`
	Transitions []Transition `json:"transitions"`
	Types       []string     `json:"types,omitempty"` // allowed item types; empty = any
}

// GuardInput is what the guards can see about the item and the caller.
type GuardInput struct {
	Role     command.Role
	Filled   map[string]bool
	Evidence []string
}

// ---------- decoding (serde semantics: exact keys, no nulls for non-Option fields, strict types) ----------

type jobj map[string]json.RawMessage

func asObj(raw json.RawMessage) (jobj, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

func asArr(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil || a == nil {
		return nil, false
	}
	return a, true
}

func asStr(raw json.RawMessage, req bool) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || bytes.TrimSpace(raw)[0] != '"' {
		return "", false
	}
	return s, !(req && strings.ContainsRune(s, '�'))
}

func (o jobj) str(k string, req bool) (string, bool) {
	v, ok := o[k]
	if !ok {
		return "", false
	}
	return asStr(v, req)
}

func (o jobj) strs(k string, req bool) ([]string, bool) {
	v, ok := o[k]
	if !ok {
		return nil, false
	}
	return asStrs(v, req)
}

func asStrs(v json.RawMessage, req bool) ([]string, bool) {
	arr, ok := asArr(v)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := asStr(e, req)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// plainJSON accepts JSON whose numbers survive a serde_json round trip unchanged (plain integers).
func plainJSON(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return false
	}
	return plainNumbers(v)
}

func plainNumbers(v any) bool {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if s == "-0" {
			return false
		}
		_, err := strconv.ParseInt(s, 10, 64)
		return err == nil
	case string:
		return !strings.ContainsRune(x, '�')
	case []any:
		for _, e := range x {
			if !plainNumbers(e) {
				return false
			}
		}
	case map[string]any:
		for k, e := range x {
			if strings.ContainsRune(k, '�') || !plainNumbers(e) {
				return false
			}
		}
	}
	return true
}

// DecodeDefinition parses a workflow definition. req marks request input: values serde would reject, and
// numbers it would re-print differently, return ok=false so the caller falls back to Rust.
func DecodeDefinition(raw json.RawMessage, req bool) (*Definition, bool) {
	o, ok := asObj(raw)
	if !ok {
		return nil, false
	}
	d := &Definition{}
	sts, ok := o["statuses"]
	if !ok {
		return nil, false
	}
	arr, ok := asArr(sts)
	if !ok {
		return nil, false
	}
	d.Statuses = make([]Status, 0, len(arr))
	for _, e := range arr {
		so, ok := asObj(e)
		if !ok {
			return nil, false
		}
		name, ok1 := so.str("name", req)
		cat, ok2 := so.str("category", req)
		if !ok1 || !ok2 || !validCategory(cat) {
			return nil, false
		}
		d.Statuses = append(d.Statuses, Status{name, cat})
	}
	trs, ok := o["transitions"]
	if !ok {
		return nil, false
	}
	if arr, ok = asArr(trs); !ok {
		return nil, false
	}
	d.Transitions = make([]Transition, 0, len(arr))
	for _, e := range arr {
		t, ok := decodeTransition(e, req)
		if !ok {
			return nil, false
		}
		d.Transitions = append(d.Transitions, t)
	}
	if tv, present := o["types"]; present {
		if d.Types, ok = asStrs(tv, req); !ok {
			return nil, false
		}
		if len(d.Types) == 0 {
			d.Types = nil
		}
	}
	return d, true
}

func decodeTransition(raw json.RawMessage, req bool) (Transition, bool) {
	var t Transition
	o, ok := asObj(raw)
	if !ok {
		return t, false
	}
	var ok1, ok2 bool
	t.Name, ok1 = o.str("name", req)
	t.To, ok2 = o.str("to", req)
	if !ok1 || !ok2 {
		return t, false
	}
	fv, ok := o["from"]
	if !ok {
		return t, false
	}
	if s, ok := asStr(fv, req); ok {
		t.From.Any = &s
	} else if t.From.Statuses, ok = asStrs(fv, req); !ok {
		return t, false
	}
	if gv, present := o["guards"]; present {
		arr, ok := asArr(gv)
		if !ok {
			return t, false
		}
		for _, e := range arr {
			g, ok := decodeGuard(e, req)
			if !ok {
				return t, false
			}
			t.Guards = append(t.Guards, g)
		}
	}
	if pv, present := o["post_actions"]; present {
		arr, ok := asArr(pv)
		if !ok {
			return t, false
		}
		for _, e := range arr {
			a, ok := decodePostAction(e, req)
			if !ok {
				return t, false
			}
			t.PostActions = append(t.PostActions, a)
		}
	}
	return t, true
}

func decodeGuard(raw json.RawMessage, req bool) (Guard, bool) {
	var g Guard
	o, ok := asObj(raw)
	if !ok {
		return g, false
	}
	g.Type, ok = o.str("type", false)
	if !ok {
		return g, false
	}
	switch g.Type {
	case "required_fields":
		g.Fields, ok = o.strs("fields", req)
	case "min_role":
		g.Role, ok = o.str("role", req)
	case "evidence":
		g.Event, ok = o.str("event", req)
	default:
		return g, false
	}
	return g, ok
}

func decodePostAction(raw json.RawMessage, req bool) (PostAction, bool) {
	var a PostAction
	o, ok := asObj(raw)
	if !ok {
		return a, false
	}
	a.Type, ok = o.str("type", false)
	if !ok {
		return a, false
	}
	switch a.Type {
	case "set_field":
		var ok2 bool
		a.Field, ok = o.str("field", req)
		v, present := o["value"]
		if !present || !ok {
			return a, false
		}
		if req && !plainJSON(v) {
			return a, false
		}
		a.Value, ok2 = v, true
		return a, ok2
	case "assign_reporter":
		return a, true
	case "emit":
		a.Event, ok = o.str("event", req)
		return a, ok
	}
	return a, false
}

// jsonKey is a canonical comparable form (serde_json::Value equality) of any marshalable value.
func jsonKey(v any) string {
	b, err := command.Marshal(v)
	if err != nil {
		return ""
	}
	n, err := notes.NormValue(b)
	if err != nil {
		return string(b)
	}
	return string(n)
}

func (d *Definition) Equal(o *Definition) bool { return jsonKey(d) == jsonKey(o) }

// ---------- templates ----------

const softwareTemplate = `{
  "types": ["epic", "story", "task", "bug", "subtask"],
  "statuses": [
    {"name": "Backlog", "category": "todo"},
    {"name": "Todo", "category": "todo"},
    {"name": "In Progress", "category": "in_progress"},
    {"name": "In Review", "category": "in_progress"},
    {"name": "Done", "category": "done"},
    {"name": "Cancelled", "category": "done"}
  ],
  "transitions": [
    {"name": "Plan", "from": ["Backlog"], "to": "Todo"},
    {"name": "Back to Backlog", "from": ["Todo"], "to": "Backlog"},
    {"name": "Start", "from": ["Backlog", "Todo"], "to": "In Progress"},
    {"name": "Back to Todo", "from": ["In Progress", "In Review"], "to": "Todo"},
    {"name": "Request review", "from": ["In Progress"], "to": "In Review"},
    {"name": "Back to progress", "from": ["In Review"], "to": "In Progress"},
    {"name": "PR opened", "from": ["Backlog", "Todo", "In Progress"], "to": "In Review",
     "guards": [{"type": "evidence", "event": "pr_opened"}]},
    {"name": "Complete", "from": ["Todo", "In Progress", "In Review"], "to": "Done"},
    {"name": "PR merged", "from": ["Backlog", "Todo", "In Progress", "In Review"], "to": "Done",
     "guards": [{"type": "evidence", "event": "pr_merged"}]},
    {"name": "Reopen", "from": ["Done", "Cancelled"], "to": "Todo"},
    {"name": "Cancel", "from": "*", "to": "Cancelled"}
  ]
}`

const kanbanTemplate = `{
  "types": ["task", "bug"],
  "statuses": [
    {"name": "To Do", "category": "todo"},
    {"name": "Doing", "category": "in_progress"},
    {"name": "Done", "category": "done"}
  ],
  "transitions": [
    {"name": "Start", "from": ["To Do"], "to": "Doing"},
    {"name": "Back", "from": ["Doing"], "to": "To Do"},
    {"name": "Finish", "from": ["To Do", "Doing"], "to": "Done"},
    {"name": "PR merged", "from": ["To Do", "Doing"], "to": "Done",
     "guards": [{"type": "evidence", "event": "pr_merged"}]},
    {"name": "Reopen", "from": ["Done"], "to": "To Do"}
  ]
}`

// Template is Definition::template: the built-in workflow for a Space template name.
func Template(name string) *Definition {
	src := softwareTemplate
	if name == "kanban" {
		src = kanbanTemplate
	}
	d, ok := DecodeDefinition(json.RawMessage(src), false)
	if !ok {
		panic("built-in workflow template is invalid")
	}
	return d
}

// ---------- engine ----------

func (d *Definition) Validate() error {
	if len(d.Statuses) == 0 {
		return fmt.Errorf("a workflow needs at least one status")
	}
	seen := map[string]bool{}
	for _, s := range d.Statuses {
		l := rustLower(s.Name)
		if strings.TrimSpace(s.Name) == "" || seen[l] {
			return fmt.Errorf("status '%s' is empty or duplicated", s.Name)
		}
		seen[l] = true
	}
	names := map[string]bool{}
	for _, t := range d.Transitions {
		l := rustLower(t.Name)
		if names[l] {
			return fmt.Errorf("transition name '%s' is duplicated", t.Name)
		}
		names[l] = true
		if d.Status(t.To) == nil {
			return fmt.Errorf("transition '%s' targets unknown status '%s'", t.Name, t.To)
		}
		if t.From.Any != nil && *t.From.Any != "*" {
			return fmt.Errorf("transition '%s': from must be \"*\" or a list of statuses", t.Name)
		}
		for _, s := range t.From.Statuses {
			if d.Status(s) == nil {
				return fmt.Errorf("transition '%s' starts from unknown status '%s'", t.Name, s)
			}
		}
		for _, g := range t.Guards {
			switch {
			case g.Type == "required_fields" && len(g.Fields) == 0:
				return fmt.Errorf("transition '%s': required_fields is empty", t.Name)
			case g.Type == "min_role" && !(g.Role == "owner" || g.Role == "admin" || g.Role == "member" || g.Role == "guest"):
				return fmt.Errorf("transition '%s': unknown role '%s'", t.Name, g.Role)
			case g.Type == "evidence" && strings.TrimSpace(g.Event) == "":
				return fmt.Errorf("transition '%s': evidence event is empty", t.Name)
			}
		}
		for _, a := range t.PostActions {
			if a.Type == "emit" && strings.TrimSpace(a.Event) == "" {
				return fmt.Errorf("transition '%s': emit event is empty", t.Name)
			}
		}
	}
	return nil
}

func (d *Definition) Status(name string) *Status {
	for i := range d.Statuses {
		if eqFold(d.Statuses[i].Name, name) {
			return &d.Statuses[i]
		}
	}
	return nil
}

func (d *Definition) Initial() *Status { return &d.Statuses[0] }

// Candidates are the transitions out of from whose name or target status equals target (case-insensitive).
func (d *Definition) Candidates(from, target string) []*Transition {
	var out []*Transition
	for i := range d.Transitions {
		t := &d.Transitions[i]
		if t.From.matches(from) && !eqFold(t.To, from) && (eqFold(t.Name, target) || eqFold(t.To, target)) {
			out = append(out, t)
		}
	}
	return out
}

func (t *Transition) evidenceEvents() []string {
	var out []string
	for _, g := range t.Guards {
		if g.Type == "evidence" {
			out = append(out, g.Event)
		}
	}
	return out
}

// EvidenceDriven are the transitions out of from that are fired by evidence of kind.
func (d *Definition) EvidenceDriven(from, kind string) []*Transition {
	var out []*Transition
	for i := range d.Transitions {
		t := &d.Transitions[i]
		if !t.From.matches(from) || eqFold(t.To, from) {
			continue
		}
		for _, e := range t.evidenceEvents() {
			if e == kind {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func (d *Definition) AllowedFrom(from string) []*Transition {
	var out []*Transition
	for i := range d.Transitions {
		t := &d.Transitions[i]
		if t.From.matches(from) && !eqFold(t.To, from) {
			out = append(out, t)
		}
	}
	return out
}

// Check runs the guards in order and returns the first failure.
func (t *Transition) Check(in GuardInput) error {
	for _, g := range t.Guards {
		switch g.Type {
		case "required_fields":
			var missing []string
			for _, f := range g.Fields {
				if !in.Filled[f] {
					missing = append(missing, f)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("transition '%s' requires fields: %s", t.Name, strings.Join(missing, ", "))
			}
		case "min_role":
			if in.Role < command.ParseRole(g.Role) {
				return fmt.Errorf("transition '%s' requires role %s or higher", t.Name, g.Role)
			}
		case "evidence":
			found := false
			for _, e := range in.Evidence {
				if e == g.Event {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("transition '%s' requires evidence '%s' (none recorded)", t.Name, g.Event)
			}
		}
	}
	return nil
}

// Pick returns the first candidate whose guards pass; otherwise the first candidate's failure.
func (d *Definition) Pick(from, target string, in GuardInput) (*Transition, error) {
	cands := d.Candidates(from, target)
	if len(cands) == 0 {
		var allowed []string
		for _, t := range d.AllowedFrom(from) {
			allowed = append(allowed, t.To)
		}
		list := "none"
		if len(allowed) > 0 {
			list = strings.Join(allowed, ", ")
		}
		return nil, fmt.Errorf("no transition from '%s' to '%s'; allowed targets: %s", from, target, list)
	}
	var first error
	for _, t := range cands {
		err := t.Check(in)
		if err == nil {
			return t, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

// Diff is the human-readable differences from tpl (what space.drift reports).
func (d *Definition) Diff(tpl *Definition) []string {
	out := []string{}
	for _, s := range d.Statuses {
		t := tpl.Status(s.Name)
		switch {
		case t == nil:
			out = append(out, fmt.Sprintf("status '%s' is not in the template", s.Name))
		case t.Category != s.Category:
			out = append(out, fmt.Sprintf("status '%s' has category %s (template: %s)", s.Name, s.Category, t.Category))
		}
	}
	for _, t := range tpl.Statuses {
		if d.Status(t.Name) == nil {
			out = append(out, fmt.Sprintf("status '%s' from the template is missing", t.Name))
		}
	}
	for i := range d.Transitions {
		t := &d.Transitions[i]
		var x *Transition
		for j := range tpl.Transitions {
			if eqFold(tpl.Transitions[j].Name, t.Name) {
				x = &tpl.Transitions[j]
				break
			}
		}
		switch {
		case x == nil:
			out = append(out, fmt.Sprintf("transition '%s' is not in the template", t.Name))
		case jsonKey(x) != jsonKey(t):
			out = append(out, fmt.Sprintf("transition '%s' differs from the template", t.Name))
		}
	}
	for i := range tpl.Transitions {
		x := &tpl.Transitions[i]
		found := false
		for j := range d.Transitions {
			if eqFold(d.Transitions[j].Name, x.Name) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, fmt.Sprintf("transition '%s' from the template is missing", x.Name))
		}
	}
	if !slices.Equal(d.Types, tpl.Types) {
		out = append(out, fmt.Sprintf("item types are [%s] (template: [%s])", strings.Join(d.Types, ", "), strings.Join(tpl.Types, ", ")))
	}
	return out
}
