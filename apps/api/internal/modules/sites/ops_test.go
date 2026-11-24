package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"testing"
)

func TestApply(t *testing.T) {
	data := fixture(t)
	apply := func(tool, input string) {
		t.Helper()
		var err error
		data, err = Apply(data, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	apply("update_block_props", `{"id":"Hero-00000002","props":{"title":"Changed"}}`)
	apply("add_block", `{"type":"Services","props":{"title":"More"},"index":0}`)
	var doc struct {
		Content []struct {
			Type  string         `json:"type"`
			Props map[string]any `json:"props"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	id, ok := doc.Content[0].Props["id"].(string)
	if !ok || !idPattern.MatchString(id) || id == "Services-00000003" {
		t.Fatalf("assigned id: %q", id)
	}
	apply("move_block", `{"id":"Hero-00000002","index":0}`)
	apply("remove_block", `{"id":"`+id+`"}`)
	apply("set_theme", `{"primary":"#123456"}`)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Content[0].Type != "Hero" || doc.Content[0].Props["title"] != "Changed" || len(doc.Content) != 5 {
		t.Fatalf("unexpected content: %+v", doc.Content)
	}
	for _, in := range []string{`{"type":"Services","props":{"id":"Services-abcdef"}}`, `{"id":"Hero-00000002","props":{"id":"Hero-abcdef"}}`} {
		tool := "add_block"
		if in[2] == 'i' {
			tool = "update_block_props"
		}
		if _, err := Apply(data, tool, json.RawMessage(in)); err == nil {
			t.Fatal("caller supplied id accepted")
		}
	}
}

func TestMaskOnlyChangedProps(t *testing.T) {
	var d map[string]any
	if err := json.Unmarshal(fixture(t), &d); err != nil {
		t.Fatal(err)
	}
	blocks := d["content"].([]any)
	blocks[1].(map[string]any)["props"].(map[string]any)["title"] = "[PHONE]"
	data, _ := json.Marshal(d)
	if err := Validate(data); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(data, "set_theme", json.RawMessage(`{"primary":"#123456"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(data, "update_block_props", json.RawMessage(`{"id":"Hero-00000002","props":{"title":"[EMAIL]"}}`)); err == nil || err.Error() != maskReply {
		t.Fatalf("mask error: %v", err)
	}
}

func TestAddRegeneratesCollidingID(t *testing.T) {
	old := rand.Reader
	rand.Reader = bytes.NewReader([]byte{0, 0, 0, 3, 0, 0, 0, 9})
	defer func() { rand.Reader = old }()
	out, err := Apply(fixture(t), "add_block", json.RawMessage(`{"type":"Services","props":{"title":"More"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("Services-00000009")) {
		t.Fatal("collision was not regenerated")
	}
}
