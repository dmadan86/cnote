package sites

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAcceptTranslations(t *testing.T) {
	d := docOf(t, fixture(t))
	todo := translateTodo(d, "", "hi", "missing")
	if len(todo) == 0 {
		t.Fatal("nothing to translate")
	}
	calls := []ToolCall{{Name: "set_translations", Input: json.RawMessage(`{"entries":[
		{"key":"Hero-00000002.title","text":"  नमस्ते\n दुनिया "},
		{"key":"Hero-00000002.cta_href","text":"/evil"},
		{"key":"Nope-00000009.title","text":"x"},
		{"key":"Hero-00000002.image_alt","text":""},
		{"key":"Services-00000003.title","text":"` + strings.Repeat("अ", 81) + `"}]}`)}, {Name: "other", Input: json.RawMessage(`{}`)}}
	got := acceptTranslations(calls, todo)
	if len(got) != 1 || got["Hero-00000002.title"] != "नमस्ते दुनिया" {
		t.Fatalf("accepted %v", got)
	}
}

func TestTranslateTodoScopes(t *testing.T) {
	d := docOf(t, fixture(t))
	rootProps(d)["langs"] = []any{"en", "hi"}
	u, _ := unitByKey(d, "", "Hero-00000002.title")
	setEntry(d, "", "hi", u, "नमस्ते", 0)
	has := func(list []txUnit) bool {
		for _, x := range list {
			if x.key() == u.key() {
				return true
			}
		}
		return false
	}
	if has(translateTodo(d, "", "hi", "missing")) || !has(translateTodo(d, "", "hi", "all")) {
		t.Fatal("a machine entry is redone only with scope all")
	}
	setEntry(d, "", "hi", u, "नमस्ते", 1)
	if has(translateTodo(d, "", "hi", "all")) {
		t.Fatal("a checked entry must never be redone")
	}
}
