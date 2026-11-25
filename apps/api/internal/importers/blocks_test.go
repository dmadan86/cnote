package importers

import (
	"encoding/json"
	"os"
	"testing"
)

// docs-parity 7.4: the shared block fixtures through the Markdown import path. A Markdown page body is stored
// as written (it is re-read by the editor converters on first open), so every A1 form must pass through byte for byte.
func TestBlockFixturesSurviveMarkdownImport(t *testing.T) {
	b, err := os.ReadFile("../../../collab/test/fixtures/blocks.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx []struct{ ID, MD string }
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	for _, f := range fx {
		t.Run(f.ID, func(t *testing.T) {
			p, err := parsePages("notion", "# Fixture\n\n"+f.MD)
			if err != nil || len(p.Recs) != 1 {
				t.Fatalf("parse: %v %v", err, p)
			}
			if p.Recs[0].BodyMD != f.MD {
				t.Fatalf("body changed:\n%s\nwant:\n%s", p.Recs[0].BodyMD, f.MD)
			}
		})
	}
}

// A page file that starts with one of the editor's own HTML-subset forms is Markdown: an exported page that begins
// with an expand must keep its tags (a plain HTML page still goes through htmlToMD).
func TestMarkdownStartingWithAnExpandKeepsItsTags(t *testing.T) {
	md := "<details><summary>Returns</summary>\n\nSeven days.\n\n</details>"
	p, err := parsePages("notion", md)
	if err != nil || len(p.Recs) != 1 {
		t.Fatalf("parse: %v", err)
	}
	if p.Recs[0].BodyMD != md {
		t.Fatalf("body changed: %q", p.Recs[0].BodyMD)
	}
}

// docs C1: a page whose first block is a highlight, a numbered table or a coloured cell is the editor's own Markdown,
// not an HTML document, so the tint and the numbering survive the import.
func TestMarkdownStartingWithC1FormsKeepsThem(t *testing.T) {
	for name, md := range map[string]string{
		"highlight":      `<mark data-color="green">Approved</mark> by finance.`,
		"bare highlight": `<mark>Approved</mark> by finance.`,
		"numbered table": `<table data-numbered="true"><tr><th><p>Item</p></th></tr><tr><td><p>Rice</p></td></tr></table>`,
		"coloured cell":  `<table><tr><th data-bg="grey"><p>Item</p></th></tr><tr><td data-bg="red"><p>Rice</p></td></tr></table>`,
	} {
		t.Run(name, func(t *testing.T) {
			p, err := parsePages("notion", md)
			if err != nil || len(p.Recs) != 1 {
				t.Fatalf("parse: %v", err)
			}
			if p.Recs[0].BodyMD != md {
				t.Fatalf("body changed: %q", p.Recs[0].BodyMD)
			}
		})
	}
}
