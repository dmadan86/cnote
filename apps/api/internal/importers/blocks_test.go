package importers

import (
	"encoding/json"
	"os"
	"strings"
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

// A page file that starts with a tag is read as HTML, not Markdown. Exported A1 pages that begin with an expand or a
// status wrapper (no heading first) are the risk; this records today's behaviour so a change is noticed.
func TestMarkdownStartingWithATagIsReadAsHTML(t *testing.T) {
	p, err := parsePages("notion", "<details><summary>Returns</summary>\n\nSeven days.\n\n</details>")
	if err != nil || len(p.Recs) != 1 {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(p.Recs[0].BodyMD, "Seven days") {
		t.Fatalf("text lost: %q", p.Recs[0].BodyMD)
	}
	t.Logf("body: %q title: %q", p.Recs[0].BodyMD, p.Recs[0].Title)
}
