package notes

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fixture struct {
	ID    string `json:"id"`
	MD    string `json:"md"`
	Plain string `json:"plain"`
}

// loadFixtures reads the one fixture set shared with the collab gateway and the web tests (docs-parity 7.4).
func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	b, err := os.ReadFile("../../../../collab/test/fixtures/blocks.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx []fixture
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx) < 10 {
		t.Fatalf("only %d fixtures", len(fx))
	}
	return fx
}

func TestPlainTextFixtures(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.ID, func(t *testing.T) {
			if got := PlainText(f.MD); got != f.Plain {
				t.Fatalf("got:\n%s\nwant:\n%s", got, f.Plain)
			}
			// stripping is idempotent for text that holds no markers any more, except literal lookalikes in code
			if strings.Contains(f.MD, "```") || strings.Contains(f.Plain, "</") { // plain text that shows a closing tag is not markup-free
				return
			}
			if again := PlainText(f.Plain); again != f.Plain {
				t.Fatalf("not stable:\n%s", again)
			}
		})
	}
}

func TestPlainTextLeavesOldPagesAlone(t *testing.T) {
	const old = "# Runbook\n\nRestart the **pods**.\n\n- a\n- b\n\n> careful\n\n| a | b |\n| --- | --- |\n| 1 | 2 |"
	if got := PlainText(old); got != strings.ReplaceAll(strings.ReplaceAll(old, "\n\n", "\n"), "> ", "") {
		t.Fatalf("existing Markdown changed:\n%s", got)
	}
}
