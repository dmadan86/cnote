package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"strings"
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

func TestSetContent(t *testing.T) {
	data := mustApply(t, fixture(t), "add_page", `{"title":"About"}`)
	data = mustApply(t, data, "add_block", `{"type":"Text","page":"about","props":{"title":"A","body":"b"}}`)
	d := docOf(t, data)
	keep := asMap(contentOf(d, "about")[0])
	keepID := asMap(keep["props"])["id"].(string)
	in, _ := json.Marshal(map[string]any{"page": "about", "content": []any{
		keep, // same id and type: kept
		map[string]any{"type": "Text", "props": map[string]any{"id": keepID, "title": "dup"}},        // repeated id: replaced
		map[string]any{"type": "Text", "props": map[string]any{"id": "Text-ffffffff", "title": "x"}}, // valid client-chosen id: kept
		map[string]any{"type": "Hero", "props": map[string]any{"id": "Hero-00000002", "title": "t"}}, // id of a Home block: replaced
	}})
	out := mustApply(t, data, "set_content", string(in))
	list := contentOf(docOf(t, out), "about")
	if len(list) != 4 {
		t.Fatalf("blocks %d", len(list))
	}
	seen := map[string]bool{}
	for i, b := range list {
		id := asMap(asMap(b)["props"])["id"].(string)
		if seen[id] || (i > 0 && (id == keepID || id == "Hero-00000002")) {
			t.Fatalf("id %s reused", id)
		}
		seen[id] = true
	}
	if asMap(asMap(list[2])["props"])["id"] != "Text-ffffffff" {
		t.Fatal("proposed id not kept")
	}
	second := mustApply(t, out, "set_content", string(in))
	if asMap(asMap(contentOf(docOf(t, second), "about")[2])["props"])["id"] != "Text-ffffffff" {
		t.Fatal("id changed on second save")
	}
	if asMap(asMap(list[0])["props"])["id"] != keepID {
		t.Fatal("kept block lost its id")
	}
	if len(contentOf(docOf(t, out), "")) != 5 {
		t.Fatal("Home changed")
	}
	// the same checks as every other write
	for name, in := range map[string]string{
		"shared off Home": `{"page":"about","content":[{"type":"Footer","props":{}}]}`,
		"unknown prop":    `{"page":"about","content":[{"type":"Text","props":{"nope":"x"}}]}`,
		"too long":        `{"page":"about","content":[{"type":"Text","props":{"title":"` + strings.Repeat("x", 81) + `"}}]}`,
		"mask":            `{"page":"about","content":[{"type":"Text","props":{"title":"[PHONE]"}}]}`,
		"unknown page":    `{"page":"nope","content":[]}`,
		"not a list":      `{"page":"about","content":{}}`,
		"extra key":       `{"page":"about","content":[],"x":1}`,
		"no type":         `{"page":"about","content":[{"props":{}}]}`,
		"bad embed":       `{"page":"about","content":[{"type":"Embed","props":{"provider":"youtube","source":"https://evil.test/x"}}]}`,
	} {
		if _, err := Apply(data, "set_content", json.RawMessage(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Home can be set too, shared blocks included
	home := docOf(t, data)["content"]
	hb, _ := json.Marshal(map[string]any{"content": home})
	mustApply(t, data, "set_content", string(hb))
}

func TestEmbedNormalised(t *testing.T) {
	data := mustApply(t, fixture(t), "add_block", `{"type":"Embed","props":{"provider":"youtube","source":"https://youtu.be/dQw4w9WgXcQ"}}`)
	if !strings.Contains(string(data), `"source":"dQw4w9WgXcQ"`) {
		t.Fatalf("not normalised: %s", data)
	}
	for _, bad := range []string{`https://vimeo.com/1`, `javascript:alert(1)`, `<iframe src="https://evil.test/x"></iframe>`} {
		b, _ := json.Marshal(map[string]any{"type": "Embed", "props": map[string]any{"provider": "youtube", "source": bad}})
		if _, err := Apply(data, "add_block", b); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestVariants(t *testing.T) {
	base := fixture(t)
	for typ, def := range catalog.Blocks {
		v, ok := def.Props["variant"]
		if !ok {
			continue
		}
		_ = typ
		for _, val := range v.Values {
			if err := validateField("variant", val, v); err != nil {
				t.Errorf("%s %s: %v", typ, val, err)
			}
		}
	}
	for _, val := range []string{"split", "cover", "centered"} {
		out := mustApply(t, base, "update_block_props", `{"id":"Hero-00000002","props":{"variant":"`+val+`"}}`)
		if !strings.Contains(string(out), `"variant":"`+val+`"`) || !strings.Contains(string(out), `"align":"center"`) {
			t.Fatal(val)
		}
	}
	if _, err := Apply(base, "update_block_props", json.RawMessage(`{"id":"Hero-00000002","props":{"variant":"full-bleed"}}`)); err == nil {
		t.Fatal("unknown variant accepted")
	}
}

func TestOldDocumentsStayValid(t *testing.T) {
	if err := Validate(fixture(t)); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal(fixture(t), &d)
	d["pages"] = []any{map[string]any{"slug": "a", "title": "A", "content": []any{}, "extra": 1}}
	b, _ := json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("unknown page field accepted")
	}
	d["pages"] = []any{map[string]any{"slug": "a", "title": "", "content": []any{}}}
	b, _ = json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("empty title accepted")
	}
	d["pages"] = []any{map[string]any{"slug": "a", "title": "A", "content": []any{}}, map[string]any{"slug": "a", "title": "B", "content": []any{}}}
	b, _ = json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("duplicate slug accepted")
	}
	// a block id is unique across pages
	d["pages"] = []any{map[string]any{"slug": "a", "title": "A", "content": []any{map[string]any{"type": "Hero", "props": map[string]any{"id": "Hero-00000002", "title": "x"}}}}}
	b, _ = json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("duplicate block id across pages accepted")
	}
}

func TestImageIDsWalkPages(t *testing.T) {
	data := json.RawMessage(`{"content":[],"pages":[{"slug":"a","title":"A","content":[{"type":"Hero","props":{"image":"12345678-1234-1234-1234-123456789abc"}}]}]}`)
	if ids := ImageIDs(data); len(ids) != 1 || !HasBlock(data, "Hero") {
		t.Fatal("pages not walked")
	}
}

func TestCatalogueBlockThroughTools(t *testing.T) {
	data := fixture(t)
	next, err := Apply(data, "add_block", json.RawMessage(`{"type":"Catalogue","props":{"title":"Our products","variant":"list"}}`))
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, b := range allBlocks(next) {
		if b.Type == "Catalogue" {
			id = b.Props["id"].(string)
		}
	}
	if id == "" {
		t.Fatal("no Catalogue block")
	}
	if _, err = Apply(next, "update_block_props", json.RawMessage(`{"id":"`+id+`","props":{"enquire":"whatsapp","intro":"Pick one"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(next, "update_block_props", json.RawMessage(`{"id":"`+id+`","props":{"items":[{"name":"X","price":500}]}}`)); err == nil {
		t.Fatal("the model must not be able to write item names or prices")
	}
	if _, err = Apply(next, "add_block", json.RawMessage(`{"type":"Catalogue","props":{}}`)); err == nil {
		t.Fatal("one Catalogue block per page")
	}
	if out, err := Apply(next, "remove_block", json.RawMessage(`{"id":"`+id+`"}`)); err != nil || len(allBlocks(out)) != len(allBlocks(data)) {
		t.Fatal(err)
	}
}
