package sites

import (
	"encoding/json"
	"testing"
)

func TestDescribeDoc(t *testing.T) {
	base := fixture(t)
	for want, change := range map[string]func() json.RawMessage{
		"Updated Hero": func() json.RawMessage {
			return mustApply(t, base, "update_block_props", `{"id":"Hero-00000002","props":{"title":"x"}}`)
		},
		"Reordered sections":                     func() json.RawMessage { return mustApply(t, base, "move_block", `{"id":"Hero-00000002","index":3}`) },
		"Deleted Hero":                           func() json.RawMessage { return mustApply(t, base, "remove_block", `{"id":"Hero-00000002"}`) },
		"Added Services":                         func() json.RawMessage { return mustApply(t, base, "add_block", `{"type":"Services","props":{}}`) },
		"Changed the theme":                      func() json.RawMessage { return mustApply(t, base, "set_theme", `{"primary":"#000000"}`) },
		"Changed the page title and description": func() json.RawMessage { return mustApply(t, base, "set_page", `{"title":"Z"}`) },
		"Added page About, Updated Header":       func() json.RawMessage { return mustApply(t, base, "add_page", `{"title":"About"}`) },
		"Saved":                                  func() json.RawMessage { return base },
	} {
		if got := describeDoc(base, change()); got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}

func TestDuplicateOpPage(t *testing.T) {
	data := mustApply(t, fixture(t), "add_page", `{"title":"About"}`)
	data = mustApply(t, data, "add_block", `{"type":"Text","page":"about","props":{"title":"A"}}`)
	id := asMap(asMap(contentOf(docOf(t, data), "about")[0])["props"])["id"].(string)
	tool, in, err := duplicateOp(data, json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil || tool != "add_block" {
		t.Fatal(err)
	}
	out := mustApply(t, data, tool, string(in))
	if len(contentOf(docOf(t, out), "about")) != 2 || len(contentOf(docOf(t, out), "")) != 5 {
		t.Fatal("duplicate landed on the wrong page")
	}
}

func TestRestoreRuleAndSummary(t *testing.T) {
	if got := opSummary("add_page", json.RawMessage(`{"title":"About"}`)); got != "Added page About" {
		t.Fatal(got)
	}
	if got := opSummary("update_block_props", json.RawMessage(`{"id":"Hero-00000002"}`)); got != "Updated Hero" {
		t.Fatal(got)
	}
}
