package docs

import (
	"regexp"
	"strings"
	"testing"
)

func TestBuiltinIDParsing(t *testing.T) {
	for id, want := range map[string]string{"builtin:sop": "sop", "builtin:": "", "builtin:x:y": "x:y"} {
		if k, ok := builtinKey(id); !ok || k != want {
			t.Fatalf("%q: %q %v", id, k, ok)
		}
	}
	// a UUID, an empty id and a different prefix are not built-ins, so they go on to UUID parsing
	for _, id := range []string{"", "0f0e0d0c-0b0a-4908-8706-050403020100", "Builtin:sop", " builtin:sop"} {
		if _, ok := builtinKey(id); ok {
			t.Fatalf("%q parsed as built-in", id)
		}
	}
	if _, ok := findBuiltin("nope"); ok {
		t.Fatal("unknown key found")
	}
}

func TestBuiltinsAreWellFormed(t *testing.T) {
	if len(builtins) != 12 {
		t.Fatalf("want the 12 templates of 5.5, got %d", len(builtins))
	}
	keys, names := map[string]bool{}, map[string]bool{}
	for _, b := range builtins {
		if keys[b.Key] || names[b.Name] || !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(b.Key) {
			t.Fatalf("bad or repeated key/name %q %q", b.Key, b.Name)
		}
		keys[b.Key], names[b.Name] = true, true
		known := false
		for _, c := range builtinCategories {
			known = known || c == b.Category
		}
		if !known || b.Description == "" || b.Title == "" || strings.TrimSpace(b.Body) == "" {
			t.Fatalf("%s: incomplete", b.Key)
		}
		// every placeholder must be one a template without a source can resolve
		missing := map[string]bool{}
		v := vals{"workspace.name": "W", "today": "1 Jan 2026", "user.name": "U"}
		for _, s := range []string{b.Title, b.Body} {
			out := substitute(s, v, missing)
			if strings.Contains(out, "{{") {
				t.Fatalf("%s: placeholder left over", b.Key)
			}
		}
		if len(missing) > 0 {
			t.Fatalf("%s: unresolved %v", b.Key, missing)
		}
	}
}
