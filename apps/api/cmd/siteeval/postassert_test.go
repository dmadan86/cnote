package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sites "github.com/cnote/api/internal/modules/sites"
)

const postDoc = `{"type":"doc","content":[
 {"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Tips"}]},
 {"type":"paragraph","content":[{"type":"text","text":"see "},{"type":"text","text":"us","marks":[{"type":"link","attrs":{"href":"/"}}]}]},
 {"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]}]}]}`

func snap() PostSnap {
	return PostSnap{Title: "T", Description: "D", Body: json.RawMessage(postDoc), Changed: true, Plan: json.RawMessage(`{"title":"P","points":["a","b"]}`), Reply: "Done"}
}

func TestPostAssertions(t *testing.T) {
	s := snap()
	ok := []Assertion{
		{"post/title", "equals", "T"}, {"post/changed", "equals", "true"}, {"post/blocks", "count", 3}, {"post/blocks.0.type", "equals", "h2"},
		{"post/blocks.1.type", "equals", "p"}, {"post/blocks.2.type", "equals", "ul"}, {"post/blocks.1.text", "equals", "see us"}, {"post/links", "equals", "1"},
		{"post/images", "equals", "0"}, {"post/text", "contains", "Tips"}, {"post/plan.points", "count", 2}, {"post/plan.title", "equals", "P"},
		{"post/reply", "contains", "Done"}, {"post/blocks.9.type", "absent", nil}, {"post/blocks.0.json", "contains", `"level":2`},
	}
	if errs := EvalPostAssertions("c", s, ok); len(errs) != 0 {
		t.Fatal(errs[0])
	}
	bad := []Assertion{{"post/title", "equals", "X"}, {"post/links", "equals", "0"}, {"post/plan.title", "absent", nil}}
	if errs := EvalPostAssertions("c", s, bad); len(errs) != 3 {
		t.Fatalf("%d errors", len(errs))
	}
	s.Plan = nil
	if errs := EvalPostAssertions("c", s, []Assertion{{"post/plan.points", "absent", nil}}); len(errs) != 0 {
		t.Fatal(errs[0])
	}
}

func TestPostCollateral(t *testing.T) {
	before := snap()
	after := snap()
	if d := CheckPostCollateral("c", before, after, nil); len(d) != 0 {
		t.Fatal(d)
	}
	after.Body = json.RawMessage(strings.Replace(postDoc, `"text":"a"`, `"text":"b"`, 1))
	if d := CheckPostCollateral("c", before, after, nil); len(d) != 1 || !strings.Contains(d[0], "block 2") {
		t.Fatal(d)
	}
	if d := CheckPostCollateral("c", before, after, []string{"post/block.2"}); len(d) != 0 {
		t.Fatal(d)
	}
	if d := CheckPostCollateral("c", before, after, []string{"post/body"}); len(d) != 0 {
		t.Fatal(d)
	}
	after.Title = "Z"
	if d := CheckPostCollateral("c", before, after, []string{"post/block.2"}); len(d) != 1 {
		t.Fatal(d)
	}
}

func TestSplitAndFixture(t *testing.T) {
	page, post := splitAssertions([]Assertion{{Path: "Hero.title"}, {Path: "post/title"}})
	if len(page) != 1 || len(post) != 1 {
		t.Fatal(page, post)
	}
	for _, raw := range []string{postDoc, `{"title":"A","body":` + postDoc + `}`, `{"post":{"title":"A","body":` + postDoc + `}}`} {
		p, err := parsePostFixture([]byte(raw))
		if err != nil || len(postNodes(p.Body)) != 3 || p.Title == "" {
			t.Fatalf("%v %+v", err, p)
		}
	}
	var c Case
	_ = json.Unmarshal([]byte(`{"id":"x","post":"new"}`), &c)
	if !c.isPost() || (Case{}).isPost() {
		t.Fatal("isPost")
	}
}

func TestRunPostCaseKeepsOtherBlocks(t *testing.T) {
	blogFixture, blogLoaded, blogPage = snap(), true, json.RawMessage(`{"root":{"props":{}},"content":[]}`)
	n := 0
	m := func(_ context.Context, _ sites.StepReq) (sites.StepRes, error) {
		n++
		if n == 1 {
			in := `{"blocks":[{"keep":0},{"kind":"p","text":"short"},{"keep":2}]}`
			return sites.StepRes{Stop: "tools", Calls: []sites.ToolCall{{ID: "1", Name: "post_write", Input: json.RawMessage(in)}}}, nil
		}
		return sites.StepRes{Stop: "end", Text: "ok"}, nil
	}
	c := Case{ID: "shorten", Post: json.RawMessage(`true`), MayChange: []string{"post/block.1"}, Expect: []Assertion{{Path: "post/blocks.1.text", Op: "equals", Value: "short"}}}
	if rr := runPostCase(context.Background(), c, m, time.Now()); !rr.Success {
		t.Fatalf("%+v", rr.Failure)
	}
	n = 0
	c.MayChange = nil
	if rr := runPostCase(context.Background(), c, m, time.Now()); rr.Success || !strings.Contains(rr.Failure.Reason, "block 1") {
		t.Fatalf("%+v", rr.Failure)
	}
}
