package sites

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustApply(t *testing.T, data json.RawMessage, tool, input string) json.RawMessage {
	t.Helper()
	out, err := Apply(data, tool, json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s %s: %v", tool, input, err)
	}
	return out
}

func docOf(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func slugList(d map[string]any) []string { return slugsOf(d)[1:] }

func TestPagesAddRemoveRename(t *testing.T) {
	data := mustApply(t, fixture(t), "add_page", `{"title":"About us"}`)
	d := docOf(t, data)
	if got := slugList(d); len(got) != 1 || got[0] != "about-us" {
		t.Fatalf("slugs %v", got)
	}
	// the Header gets a link, the Footer does not
	if !strings.Contains(string(data), `"href":"/about-us"`) {
		t.Fatal("no header link")
	}
	if links, _ := asMap(asMap(contentOf(d, "")[0])["props"])["links"].([]any); len(links) != 1 {
		t.Fatalf("header links %v", links)
	}
	// same title again gets -2, a reserved name is suffixed too, an explicit duplicate or reserved slug is refused
	data = mustApply(t, data, "add_page", `{"title":"About us"}`)
	data = mustApply(t, data, "add_page", `{"title":"API"}`)
	if got := slugList(docOf(t, data)); strings.Join(got, ",") != "about-us,about-us-2,api-2" {
		t.Fatalf("slugs %v", got)
	}
	for _, in := range []string{`{"title":"x","slug":"about-us"}`, `{"title":"x","slug":"assets"}`, `{"title":"x","slug":"Bad Slug"}`, `{"slug":"x"}`, `{"title":"x","template":"nope"}`, `{"title":"x","extra":1}`} {
		if _, err := Apply(data, "add_page", json.RawMessage(in)); err == nil {
			t.Fatalf("accepted %s", in)
		}
	}
	// a link to a page follows a rename and goes Home when the page is removed
	data = mustApply(t, data, "update_block_props", `{"id":"Hero-00000002","props":{"cta_href":"/about-us#Hero-00000002"}}`)
	data = mustApply(t, data, "set_page", `{"page":"about-us","slug":"about","title":"About","description":"Who we are"}`)
	if !strings.Contains(string(data), `"cta_href":"/about#Hero-00000002"`) || !strings.Contains(string(data), `"href":"/about"`) || strings.Contains(string(data), `"/about-us"`) {
		t.Fatalf("links did not follow: %s", data)
	}
	data = mustApply(t, data, "remove_page", `{"page":"about"}`)
	if strings.Contains(string(data), `/about"`) || !strings.Contains(string(data), `"cta_href":"/"`) {
		t.Fatalf("links not sent home: %s", data)
	}
	// Home keeps its title and description path
	data = mustApply(t, data, "set_page", `{"title":"New home"}`)
	if docOf(t, data)["root"].(map[string]any)["props"].(map[string]any)["title"] != "New home" {
		t.Fatal("home title")
	}
	for _, in := range []string{`{"page":"nope","title":"x"}`, `{"slug":"x"}`, `{"page":"about-us-2","slug":"api"}`, `{"bogus":1}`} {
		if _, err := Apply(data, "set_page", json.RawMessage(in)); err == nil {
			t.Fatalf("accepted %s", in)
		}
	}
	if _, err := Apply(data, "remove_page", json.RawMessage(`{"page":""}`)); err == nil {
		t.Fatal("removed Home")
	}
}

func TestPageLimitsAndSharedBlocks(t *testing.T) {
	data := fixture(t)
	for i := 0; i < 10; i++ {
		data = mustApply(t, data, "add_page", `{"title":"Page `+string(rune('a'+i))+`"}`)
	}
	if _, err := Apply(data, "add_page", json.RawMessage(`{"title":"One more"}`)); err == nil {
		t.Fatal("11th page accepted")
	}
	// blocks go to the named page, ids are unique across the document, shared blocks stay on Home
	data = mustApply(t, data, "add_block", `{"type":"Text","page":"page-a","props":{"title":"Hi","body":"x"}}`)
	d := docOf(t, data)
	if len(contentOf(d, "page-a")) != 1 || len(contentOf(d, "")) != 5 {
		t.Fatal("block on wrong page")
	}
	for _, in := range []string{`{"type":"Header","page":"page-a","props":{"brand":"x"}}`, `{"type":"Footer","page":"page-b","props":{}}`, `{"type":"Text","page":"nope","props":{}}`} {
		if _, err := Apply(data, "add_block", json.RawMessage(in)); err == nil {
			t.Fatalf("accepted %s", in)
		}
	}
	// max counts per page: Hero twice is fine on two pages
	data = mustApply(t, data, "add_block", `{"type":"Hero","page":"page-a","props":{"title":"A"}}`)
	if _, err := Apply(data, "add_block", json.RawMessage(`{"type":"Hero","page":"page-a","props":{"title":"B"}}`)); err == nil {
		t.Fatal("two Heroes on one page")
	}
	// move, remove and update find a block on any page
	id := docOf(t, data)["pages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["props"].(map[string]any)["id"].(string)
	data = mustApply(t, data, "move_block", `{"id":"`+id+`","index":1}`)
	data = mustApply(t, data, "update_block_props", `{"id":"`+id+`","props":{"title":"Changed"}}`)
	data = mustApply(t, data, "remove_block", `{"id":"`+id+`"}`)
	if len(contentOf(docOf(t, data), "page-a")) != 1 {
		t.Fatal("remove")
	}
}

func TestLinksToPages(t *testing.T) {
	data := mustApply(t, fixture(t), "add_page", `{"title":"About"}`)
	for link, ok := range map[string]bool{"/": true, "/about": true, "/about#Hero-00000002": true, "/#Hero-00000002": true, "/missing": false, "//about": false, "/about/x": false, "/ABOUT": false} {
		b, _ := json.Marshal(map[string]any{"id": "Hero-00000002", "props": map[string]any{"cta_href": link}})
		if _, err := Apply(data, "update_block_props", b); (err == nil) != ok {
			t.Errorf("%s: %v", link, err)
		}
	}
	// without the page, the link is invalid
	if _, err := Apply(fixture(t), "update_block_props", json.RawMessage(`{"id":"Hero-00000002","props":{"cta_href":"/about"}}`)); err == nil {
		t.Fatal("link to a missing page accepted")
	}
}

func TestLegalTemplates(t *testing.T) {
	slots := templateSlots(map[string]string{"name": "Sharma Dental", "address": "", "email": "a@b.in"}, "https://x.test")
	for _, key := range []string{"privacy", "terms"} {
		data := mustAddTemplate(t, fixture(t), key, slots)
		d := docOf(t, data)
		p := findPage(d, key)
		if p == nil || len(contentOf(d, key)) == 0 {
			t.Fatalf("%s page missing", key)
		}
		s := string(data)
		if !strings.Contains(s, "Sharma Dental") || !strings.Contains(s, "[add your address]") || strings.Contains(s, "{{") {
			t.Fatalf("slots: %s", s)
		}
		// the link goes to the Footer, not the Header
		if h := asMap(asMap(contentOf(d, "")[0])["props"])["links"].([]any); len(h) != 0 {
			t.Fatalf("header link %v", h)
		}
		if f := asMap(asMap(contentOf(d, "")[4])["props"])["links"].([]any); len(f) != 1 {
			t.Fatalf("footer link %v", f)
		}
		if _, err := applyWith(data, "add_page", json.RawMessage(`{"template":"`+key+`"}`), slots); err == nil {
			t.Fatal("second privacy page accepted")
		}
	}
}

func mustAddTemplate(t *testing.T, data json.RawMessage, key string, slots map[string]string) json.RawMessage {
	t.Helper()
	out, err := applyWith(data, "add_page", json.RawMessage(`{"template":"`+key+`"}`), slots)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlanPageRule(t *testing.T) {
	if checkPageGrowth(false, 4, 5) == nil || checkPageGrowth(true, 4, 5) != nil || checkPageGrowth(true, 10, 11) == nil {
		t.Fatal("growth past the plan")
	}
	// after a Pro lapse the pages stay editable, but cannot grow
	if checkPageGrowth(false, 6, 6) != nil || checkPageGrowth(false, 6, 5) != nil || checkPageGrowth(false, 6, 7) == nil {
		t.Fatal("lapsed site")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"About us": "about-us", "  Hello, World!  ": "hello-world", "हमारी सेवाएं": "", strings.Repeat("a", 60): strings.Repeat("a", 40)} {
		if got := slugify(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	d := docOf(t, mustApply(t, fixture(t), "add_page", `{"title":"हमारी सेवाएं"}`))
	if got := slugList(d); len(got) != 1 || got[0] != "page" {
		t.Fatalf("hindi title slug %v", got)
	}
}
