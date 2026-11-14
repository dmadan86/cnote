package projects

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// Port of the unit tests in crates/cnote-projects/src/workflow.rs and items.rs.

func gin(role command.Role, filled map[string]bool, evidence ...string) GuardInput {
	return GuardInput{Role: role, Filled: filled, Evidence: evidence}
}

func TestTemplatesAreValid(t *testing.T) {
	for _, n := range []string{"software", "kanban", "anything-else"} {
		if err := Template(n).Validate(); err != nil {
			t.Fatal(n, err)
		}
	}
	if len(Template("software").Statuses) != 6 || Template("kanban").Initial().Name != "To Do" {
		t.Fatal("template shape")
	}
}

func TestParsesTheDocumentedJSONShape(t *testing.T) {
	d, ok := DecodeDefinition(json.RawMessage(`{
		"statuses": [{"name": "A", "category": "todo"}, {"name": "B", "category": "done"}],
		"transitions": [{
			"name": "Go", "from": "*", "to": "B",
			"guards": [{"type": "required_fields", "fields": ["assignee"]},
			           {"type": "min_role", "role": "admin"},
			           {"type": "evidence", "event": "pr_merged"}],
			"post_actions": [{"type": "set_field", "field": "priority", "value": "low"},
			                 {"type": "assign_reporter"}, {"type": "emit", "event": "x.y"}]
		}]}`), true)
	if !ok {
		t.Fatal("decode failed")
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Transitions[0].Guards) != 3 || len(d.Transitions[0].PostActions) != 3 {
		t.Fatal("guards or post actions lost")
	}
	// serde rejects these; the decoder must too (the caller then falls back to Rust)
	for _, bad := range []string{
		`{"statuses":[{"name":"A","category":"nope"}],"transitions":[]}`,
		`{"statuses":[],"transitions":null}`,
		`{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":5,"to":"A"}]}`,
		`{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":"*","to":"A","guards":[{"type":"bogus"}]}]}`,
		`{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":"*","to":"A","post_actions":[{"type":"set_field","field":"f"}]}]}`,
		`{"Statuses":[],"transitions":[]}`,
	} {
		if _, ok := DecodeDefinition(json.RawMessage(bad), true); ok {
			t.Errorf("decoded %s", bad)
		}
	}
}

func TestValidateRejectsBadDefinitions(t *testing.T) {
	d := Template("kanban")
	d.Transitions[0].To = "Nowhere"
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatal(err)
	}
	d = Template("kanban")
	d.Statuses = append(d.Statuses, Status{"done", "done"})
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatal(err)
	}
	d = Template("kanban")
	all := "all"
	d.Transitions[0].From = FromSpec{Any: &all}
	if d.Validate() == nil {
		t.Fatal("from \"all\" accepted")
	}
	d = Template("kanban")
	d.Transitions[0].Guards = []Guard{{Type: "min_role", Role: "boss"}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatal(err)
	}
}

func TestPickByStatusOrTransitionName(t *testing.T) {
	d := Template("software")
	i := gin(command.Member, map[string]bool{})
	pick := func(from, to string) (*Transition, error) { return d.Pick(from, to, i) }
	if tr, _ := pick("Backlog", "Todo"); tr == nil || tr.Name != "Plan" {
		t.Fatal(tr)
	}
	if tr, _ := pick("todo", "in progress"); tr == nil || tr.Name != "Start" {
		t.Fatal(tr)
	}
	if tr, _ := pick("Todo", "Start"); tr == nil || tr.To != "In Progress" {
		t.Fatal(tr)
	}
	if tr, _ := pick("In Progress", "Cancelled"); tr == nil || tr.Name != "Cancel" {
		t.Fatal(tr)
	}
	if _, err := pick("Backlog", "Done"); err == nil || !strings.Contains(err.Error(), "PR merged") {
		t.Fatal(err) // only the evidence route is open from Backlog
	}
	if _, err := pick("Done", "In Review"); err == nil || !strings.Contains(err.Error(), "allowed targets: Todo, Cancelled") {
		t.Fatal(err)
	}
	if _, err := pick("Todo", "Todo"); err == nil {
		t.Fatal("no self transition")
	}
}

func TestGuards(t *testing.T) {
	d := Template("kanban")
	d.Transitions[0].Guards = []Guard{{Type: "required_fields", Fields: []string{"assignee", "estimate"}}, {Type: "min_role", Role: "admin"}}
	f := map[string]bool{"assignee": true}
	_, err := d.Pick("To Do", "Doing", gin(command.Admin, f))
	if err == nil || !strings.Contains(err.Error(), "estimate") || strings.Contains(err.Error(), "assignee,") {
		t.Fatal(err)
	}
	f["estimate"] = true
	if _, err := d.Pick("To Do", "Doing", gin(command.Member, f)); err == nil || !strings.Contains(err.Error(), "requires role admin") {
		t.Fatal(err)
	}
	if _, err := d.Pick("To Do", "Doing", gin(command.Owner, f)); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceGuardAndAutoTransitions(t *testing.T) {
	d := Template("software")
	f := map[string]bool{}
	tr, err := d.Pick("In Review", "Done", gin(command.Member, f))
	if err != nil || tr.Name != "Complete" {
		t.Fatal(tr, err)
	}
	auto := d.EvidenceDriven("In Review", "pr_merged")
	if len(auto) != 1 || auto[0].To != "Done" {
		t.Fatal(auto)
	}
	if auto[0].Check(gin(command.Member, f)) == nil || auto[0].Check(gin(command.Member, f, "pr_merged")) != nil {
		t.Fatal("evidence guard")
	}
	if len(d.EvidenceDriven("Done", "pr_merged")) != 0 || len(d.EvidenceDriven("Todo", "nothing")) != 0 {
		t.Fatal("unexpected evidence transitions")
	}
}

func TestDiffReportsDrift(t *testing.T) {
	tpl := Template("software")
	if len(tpl.Diff(tpl)) != 0 {
		t.Fatal("template differs from itself")
	}
	d := Template("software")
	d.Statuses = append(d.Statuses, Status{"QA", "in_progress"})
	d.Statuses = slices.DeleteFunc(d.Statuses, func(s Status) bool { return s.Name == "Cancelled" })
	d.Transitions[0].To = "Done"
	d.Transitions = slices.DeleteFunc(d.Transitions, func(x Transition) bool { return x.Name == "Cancel" })
	all := strings.Join(d.Diff(tpl), "\n")
	for _, want := range []string{"'QA' is not in the template", "'Cancelled' from the template is missing", "'Plan' differs", "'Cancel' from the template is missing"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
}

func TestRankBetweenIsStrictlyBetween(t *testing.T) {
	var ranks []string
	var last *string
	for i := 0; i < 40; i++ {
		r := RankBetween(last, nil)
		if last != nil && !(*last < r) {
			t.Fatalf("%s < %s", *last, r)
		}
		ranks = append(ranks, r)
		last = &r
	}
	lo, hi := ranks[3], ranks[4]
	for i := 0; i < 40; i++ {
		r := RankBetween(&lo, &hi)
		if !(lo < r && r < hi) {
			t.Fatalf("%s < %s < %s", lo, r, hi)
		}
		lo = r
	}
	hi = ranks[0]
	for i := 0; i < 40; i++ {
		r := RankBetween(nil, &hi)
		if !(r < hi) || r == "" {
			t.Fatalf("%s < %s", r, hi)
		}
		hi = r
	}
}

func TestRankBetweenEqualNeighboursDoesNotPanic(t *testing.T) {
	m := "m"
	if RankBetween(&m, &m) != "mn" || RankBetween(nil, nil) != "n" {
		t.Fatal("equal neighbours")
	}
	a, b := "a", "b"
	if !strings.HasPrefix(RankBetween(&a, &b), "a") {
		t.Fatal("a..b")
	}
}

func TestFilledFieldsReadsTheItem(t *testing.T) {
	agent := nilUUID
	i := &Item{Description: " ", Priority: "none", AssigneeAgentID: &agent, Labels: []string{}, Custom: json.RawMessage(`{"team":"core","empty":null,"blank":""}`)}
	e := 3.0
	i.Estimate = (*notes.F64)(&e)
	f := FilledFields(i)
	if !f["assignee"] || !f["estimate"] || !f["team"] {
		t.Fatal(f)
	}
	for _, n := range []string{"priority", "description", "empty", "blank", "labels"} {
		if f[n] {
			t.Errorf("%s should not be filled", n)
		}
	}
}
