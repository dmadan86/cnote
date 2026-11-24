package uiconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCheckConfig(t *testing.T) {
	ok := []string{`{"v":1}`, `{"v":3,"w":[{"id":"a","size":"m"}]}`, `{"v":1,"a":{"b":{"c":{"d":{"e":1}}}}}`, `{"v":1,"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}`, `{"v":1,"a":1,"a":2}`}
	bad := []string{``, `null`, `[]`, `"x"`, `{}`, `{"v":0}`, `{"v":-1}`, `{"v":1.5}`, `{"v":"1"}`, `{"v":1e0}`, `{"v":1}{"v":2}x`,
		`{"v":1,"a":{"b":{"c":{"d":{"e":{"f":{"g":1}}}}}}}`, // 7 levels
		`{"v":1,"a":"` + strings.Repeat("a", 201) + `"}`, `{"v":1,"a":"\u0000"}`, `{"v":1,"a":[` + strings.Repeat("1,", 100) + `1]}`}
	for _, s := range ok {
		if _, err := checkConfig(json.RawMessage(s)); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	for _, s := range bad {
		if _, err := checkConfig(json.RawMessage(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	out, _ := checkConfig(json.RawMessage(`{"v":1,"a":1,"a":2,"b":null}`))
	if string(out) != `{"a":2,"b":null,"v":1}` {
		t.Errorf("stored form %s", out)
	}
}

func TestScreenKeys(t *testing.T) {
	for _, s := range []string{"home", "list:invoices", "a.b-c_d", strings.Repeat("a", 64)} {
		if checkScreen(s) != nil {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", "Home", "a b", "a/b", strings.Repeat("a", 65), "é"} {
		if checkScreen(s) == nil {
			t.Errorf("%q accepted", s)
		}
	}
}
