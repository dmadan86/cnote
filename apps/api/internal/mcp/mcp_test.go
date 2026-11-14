package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
)

// Ports of the unit tests in crates/cnote-mcp/src/lib.rs, run against the embedded Rust catalogue.

func srv() *Server { return &Server{Registry: command.NewRegistry()} }

func TestToolsAreCoreCommandsPlusDiscoverAndInvoke(t *testing.T) {
	tools := srv().tools()
	by := map[string]tool{}
	for _, tl := range tools {
		by[tl.Name] = tl
		if strings.Contains(tl.Name, ".") {
			t.Errorf("dotted tool name %s", tl.Name)
		}
	}
	for _, n := range []string{"note_create", "discover", "invoke"} {
		if _, ok := by[n]; !ok {
			t.Errorf("%s missing", n)
		}
	}
	if _, ok := by["note_delete"]; ok {
		t.Error("non-core commands are reached through invoke")
	}
	if !*by["note_get"].Annotations.ReadOnly || *by["note_create"].Annotations.ReadOnly {
		t.Error("read-only hints are wrong")
	}
	if tools[len(tools)-2].Name != "discover" || tools[len(tools)-1].Name != "invoke" {
		t.Error("discover and invoke come last")
	}
	if !strings.HasSuffix(by["invoice_issue"].Description, "that confirmation_id.") || !strings.Contains(by["invoice_issue"].Description, "Requires human approval") {
		t.Errorf("confirmation commands say so: %q", by["invoice_issue"].Description)
	}
	var schema map[string]any
	_ = json.Unmarshal(by["note_create"].InputSchema, &schema)
	if schema["type"] != "object" || schema["properties"].(map[string]any)["title"] == nil {
		t.Errorf("schema: %v", schema)
	}
}

func TestDestructiveCommandsAreAnnotated(t *testing.T) {
	for _, e := range srv().catalog() {
		if e.Name == "note.delete" && e.Group != "destructive" {
			t.Fatalf("note.delete group %q", e.Group)
		}
	}
}

func TestCatalogIsSortedAndResolvesUnderscores(t *testing.T) {
	s := srv()
	cat := s.catalog()
	for i := 1; i < len(cat); i++ {
		if cat[i-1].Name >= cat[i].Name {
			t.Fatalf("not sorted at %s", cat[i].Name)
		}
	}
	if e, ok := s.resolve("note_create"); !ok || e.Name != "note.create" {
		t.Fatal(e, ok)
	}
	if _, ok := s.resolve("nope"); ok {
		t.Fatal("resolved nothing")
	}
}

func TestGoOnlyCommandsJoinTheCatalogue(t *testing.T) {
	c := command.NewCommand("zzz.only", command.GroupRead, nil)
	c.Description, c.Schema = "Only in Go.", json.RawMessage(`{"type":"object", "properties": {}}`)
	s := &Server{Registry: command.NewRegistry(c)}
	e, ok := s.resolve("zzz_only")
	if !ok || e.Description != "Only in Go." || string(e.Schema) != `{"properties":{},"type":"object"}` {
		t.Fatalf("%+v %v", e, ok)
	}
	if cat := s.catalog(); cat[len(cat)-1].Name != "zzz.only" || cat[len(cat)-1].Core {
		t.Fatal("Go-only commands are discoverable but not core tools")
	}
}

func TestURIParsing(t *testing.T) {
	ws, id := "6f1c8f1e-0000-4000-8000-000000000001", "6f1c8f1e-0000-4000-8000-000000000002"
	for uri, want := range map[string][2]string{"cnote://ws/" + ws + "/note/" + id: {"note", id}, "cnote://ws/" + ws + "/view/" + id: {"view", id},
		"cnote://ws/" + strings.ToUpper(ws) + "/note/" + strings.ReplaceAll(id, "-", ""): {"note", id}} {
		w, kind, got, ok, _ := parseURI(uri)
		if !ok || w != ws || kind != want[0] || got != want[1] {
			t.Errorf("%s: %v %v %v", uri, w, kind, got)
		}
	}
	for _, bad := range []string{"cnote://ws/" + ws + "/space/" + id, "cnote://ws/" + ws + "/note/" + id + "/x", "https://example.com", "cnote://ws/x/note/y", ""} {
		if _, _, _, ok, exotic := parseURI(bad); ok || exotic {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, _, _, _, exotic := parseURI("cnote://ws/{" + ws + "}/note/" + id); !exotic {
		t.Error("braced UUIDs are Rust's call")
	}
}

func TestFrontMatterOmitsNullsAndEscapes(t *testing.T) {
	enc := func(v any) json.RawMessage { b, _ := command.Marshal(v); return b }
	got := frontMatter([]string{"title", "owner", "verified_at", "types"}, map[string]json.RawMessage{
		"title": enc("Runbook: \"prod\" <b>  "), "owner": enc("Tess"), "verified_at": enc(nil), "types": enc([]string{})})
	want := "---\ntitle: \"Runbook: \\\"prod\\\" <b>  \"\nowner: \"Tess\"\ntypes: []\n---\n"
	if got != want {
		t.Fatalf("%q != %q", got, want)
	}
}

func TestConfirmationResultCarriesTheApprovalMessage(t *testing.T) {
	id := "6f1c8f1e-0000-4000-8000-000000000003"
	r := shape([]byte(`{"confirmation_id":"` + id + `","confirmation_required":true,"message":"x"}`))
	var out map[string]any
	_ = json.Unmarshal(r.StructuredContent, &out)
	if m := out["message"].(string); !strings.Contains(m, "approve request "+id) || !strings.Contains(m, "confirmation_id "+id+".") {
		t.Fatal(m)
	}
	if r := shape([]byte(`{"id":1}`)); strings.Contains(string(r.StructuredContent), "message") {
		t.Fatal("plain results are untouched")
	}
}

func TestParseMessageAcceptsOnlyTheLegacyLifecycle(t *testing.T) {
	ok := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`, `{"jsonrpc":"2.0","id":"a","method":"tools/list","params":{"cursor":"c"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":null}}`, `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
	}
	for _, b := range ok {
		if _, good := parseMessage([]byte(b)); !good {
			t.Errorf("%s declined", b)
		}
	}
	for _, b := range []string{`[]`, `{}`, `not json`, `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, `{"jsonrpc":"2.0","id":null,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{}}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","requestState":"s"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":5}}`, `{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":null}}`, `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":null}}`,
		`{"jsonrpc":"2.0","id":1,"method":null}`, `{"jsonrpc":null,"id":1,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"method":"ping","extra":1}`} {
		if _, good := parseMessage([]byte(b)); good {
			t.Errorf("%s accepted", b)
		}
	}
}

func TestAcceptNeedsBothMediaTypesWithPositiveWeight(t *testing.T) {
	for accept, want := range map[string]bool{"application/json, text/event-stream": true, "APPLICATION/JSON;q=0.5, text/event-stream;q=0.001": true, "application/json": false,
		"*/*": false, "application/json, text/event-stream;q=0": false, "application/json, text/event-stream;q=bogus": true, `application/json, text/event-stream;x="a,b"`: true} {
		h := http.Header{"Accept": {accept}}
		j, s := accepts(h)
		if (j && s) != want {
			t.Errorf("%q: %v", accept, j && s)
		}
	}
}
