package notes

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const (
	tWS   = "00000000-0000-4000-8000-0000000000a1"
	tRavi = "00000000-0000-4000-8000-0000000000b2"
)

func fixtureMD(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile("../../../../collab/test/fixtures/blocks.json")
	if err != nil {
		t.Fatal(err)
	}
	var all []struct{ ID, MD string }
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	for _, f := range all {
		if f.ID == id {
			return f.MD
		}
	}
	t.Fatalf("fixture %s missing", id)
	return ""
}

func TestTasksFromTheSharedFixture(t *testing.T) {
	got := Tasks(fixtureMD(t, "task-assignee-due"), tWS)
	want := []Task{
		{0, "Send the GST summary", tRavi, "2026-12-01", false},
		{1, "Book the venue", "", "2026-12-05", false},
		{2, "Review the draft", tRavi, "", false},
		{3, "File the return", tRavi, "2026-11-30", true},
		{4, "Plan the audit", tRavi, "", false},
		{5, "Collect the invoices", "", "2026-12-02", false}, // nested: its own task, the parent's mention is not inherited
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tasks: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestAMentionInANormalBulletIsNotATask(t *testing.T) {
	got := Tasks(fixtureMD(t, "task-lookalike"), tWS)
	if len(got) != 1 || got[0].Text != "Pay the vendor" || got[0].Assignee != "" || got[0].Due != "" {
		t.Fatalf("only the checkbox item is a task, with no assignee or date: %+v", got)
	}
}

func TestTaskParserEdges(t *testing.T) {
	other := "00000000-0000-4000-8000-0000000000ff"
	md := "```\n- [ ] in a fence\n```\n" +
		"- [ ] first [@A](/w/" + tWS + "/people/" + tRavi + ") then [@B](/w/" + tWS + "/people/" + tWS + ") <time datetime=\"2026-01-02\">x</time> <time datetime=\"2026-01-03\">y</time>\n" +
		"- [ ] foreign [@C](/w/" + other + "/people/" + tRavi + ") and a [link](https://x.test)\n" +
		"- [ ] bad date <time datetime=\"2026-13-40\">z</time>\n" +
		"- [X] upper\n" +
		"> - [ ] quoted\n" +
		"- [ ] " + strings.Repeat("é", 400)
	got := Tasks(md, strings.ToUpper(tWS))
	if len(got) != 5 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Assignee != tRavi || got[0].Due != "2026-01-02" || got[0].Text != "first then" {
		t.Errorf("first mention and first date win: %+v", got[0])
	}
	if got[1].Assignee != "" || got[1].Text != "foreign and a link" {
		t.Errorf("a mention of another workspace is no assignee: %+v", got[1])
	}
	if got[2].Due != "" || !got[3].Done || len([]rune(got[4].Text)) != 300 {
		t.Errorf("bad date, done, truncation: %+v %+v %d", got[2], got[3], len([]rune(got[4].Text)))
	}
}
