// main_test.go contains integration tests that require the sites package (Package A).
// They are compiled only with the "siteeval_full" build tag, which is set once
// internal/modules/sites is in place. Until then, run only the assert tests:
//
//	cd apps/api && go test ./cmd/siteeval/ -run Assert
package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	ai "github.com/cnote/api/internal/modules/ai"
	sites "github.com/cnote/api/internal/modules/sites"
)

// localModel wraps ai.Local as a sites.Model.
func localModel(_ context.Context, r sites.StepReq) (sites.StepRes, error) {
	req := ai.StepRequest{
		Purpose: r.Purpose,
		Model:   r.Model,
		System:  r.System,
		Tools:   make([]ai.Tool, len(r.Tools)),
		Turns:   make([]ai.Turn, len(r.Turns)),
	}
	for i, t := range r.Tools {
		req.Tools[i] = ai.Tool{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	for i, t := range r.Turns {
		req.Turns[i] = ai.Turn{
			Role:    t.Role,
			Text:    t.Text,
			Calls:   make([]ai.ToolCall, len(t.Calls)),
			Results: make([]ai.ToolResult, len(t.Results)),
			Raw:     t.Raw,
		}
		for j, c := range t.Calls {
			req.Turns[i].Calls[j] = ai.ToolCall{ID: c.ID, Name: c.Name, Input: c.Input}
		}
		for j, x := range t.Results {
			req.Turns[i].Results[j] = ai.ToolResult{CallID: x.CallID, Content: x.Content, IsError: x.IsError}
		}
	}

	sr, err := ai.Local{}.Step(context.Background(), req)
	if err != nil {
		return sites.StepRes{}, err
	}
	res := sites.StepRes{
		Text:     sr.Text,
		Stop:     sr.Stop,
		Provider: sr.Provider,
		Model:    sr.Model,
		Raw:      sr.Raw,
		TokensIn: sr.TokensIn, TokensOut: sr.TokensOut, CacheRead: sr.CacheRead,
		Calls: make([]sites.ToolCall, len(sr.Calls)),
	}
	for i, c := range sr.Calls {
		res.Calls[i] = sites.ToolCall{ID: c.ID, Name: c.Name, Input: c.Input}
	}
	return res, nil
}

// loadFixture reads the fixture page from fixture.json (sibling of this test file).
func loadFixture(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("fixture.json")
	if err != nil {
		t.Fatalf("loadFixture: %v", err)
	}
	return json.RawMessage(raw)
}

// TestLocalUpdateBlockProps sends a /tool update_block_props command via the Local provider
// and verifies that RunTurn calls Apply correctly.
func TestLocalUpdateBlockProps(t *testing.T) {
	data := loadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	in := sites.TurnInput{
		Message: `/tool update_block_props {"id":"Hero-d4e5f6a7","props":{"title":"Hello Pune"}}`,
		Tier:    "routine",
	}
	res := sites.RunTurn(ctx, localModel, data, in)
	if res.Err != "" {
		t.Fatalf("RunTurn error: %s", res.Err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true, got false")
	}

	errs := EvalAssertions("local-update", res.Data, []Assertion{
		{Path: "Hero.title", Op: "equals", Value: "Hello Pune"},
	})
	for _, e := range errs {
		t.Errorf("assertion failed: %s", e.Error())
	}
}

// TestLocalAddBlock tests that add_block works through RunTurn.
func TestLocalAddBlock(t *testing.T) {
	data := loadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	in := sites.TurnInput{
		Message: `/tool add_block {"type":"Services","props":{"title":"Extra Services"}}`,
		Tier:    "routine",
	}
	res := sites.RunTurn(ctx, localModel, data, in)
	if res.Err != "" {
		t.Fatalf("RunTurn error: %s", res.Err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	errs := EvalAssertions("local-add", res.Data, []Assertion{
		{Path: "blocks", Op: "count", Value: float64(6)},
	})
	for _, e := range errs {
		t.Errorf("assertion failed: %s", e.Error())
	}
}

// TestLocalRemoveBlock tests that remove_block works through RunTurn.
func TestLocalRemoveBlock(t *testing.T) {
	data := loadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	in := sites.TurnInput{
		Message: `/tool remove_block {"id":"Hero-d4e5f6a7"}`,
		Tier:    "routine",
	}
	res := sites.RunTurn(ctx, localModel, data, in)
	if res.Err != "" {
		t.Fatalf("RunTurn error: %s", res.Err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	errs := EvalAssertions("local-remove", res.Data, []Assertion{
		{Path: "Hero.title", Op: "absent"},
	})
	for _, e := range errs {
		t.Errorf("assertion failed: %s", e.Error())
	}
}

// TestLocalNoChange tests that a turn that makes no tool call has Changed=false.
func TestLocalNoChange(t *testing.T) {
	data := loadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	in := sites.TurnInput{
		Message: "What is the current Hero title?",
		Tier:    "routine",
	}
	res := sites.RunTurn(ctx, localModel, data, in)
	if res.Err != "" {
		t.Fatalf("RunTurn error: %s", res.Err)
	}
	if res.Changed {
		t.Fatal("expected Changed=false for a no-tool turn")
	}
}
