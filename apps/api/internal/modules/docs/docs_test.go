package docs

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Ports of the unit tests in crates/cnote-docs/src/templates.rs.

func testVals() vals {
	return vals{"contact.name": `Asha "A" Rao`, "invoice.total": "₹1,000.00"}
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSubstitutesKnownAndKeepsUnknown(t *testing.T) {
	m := map[string]bool{}
	out := substitute("Hi {{ contact.name }}, {{invoice.total}} {{deal.value}} {{deal.value}} {{1+1}}", testVals(), m)
	if want := `Hi Asha "A" Rao, ₹1,000.00 {{deal.value}} {{deal.value}} {{1+1}}`; out != want {
		t.Fatalf("%q", out)
	}
	if !reflect.DeepEqual(keys(m), []string{"deal.value"}) {
		t.Fatal(keys(m))
	}
	// Rust's \s is Unicode White_Space (vertical tab, no-break space, ideographic space)
	if got := substitute("{{\v contact.name　}}", testVals(), map[string]bool{}); got != `Asha "A" Rao` {
		t.Fatalf("%q", got)
	}
}

func TestValuesAreNotRescanned(t *testing.T) {
	m := map[string]bool{}
	if got := substitute("{{a.b}}", vals{"a.b": "{{contact.name}}"}, m); got != "{{contact.name}}" || len(m) != 0 {
		t.Fatalf("%q %v", got, m)
	}
}

func TestBoardValuesStayValidJSON(t *testing.T) {
	md := "```tldraw\n{\"store\":{\"s1\":{\"props\":{\"text\":\"For {{contact.name}} {{x.y}}\"}}}}\n```"
	m := map[string]bool{}
	out, err := renderBoard(md, testVals(), m)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(out, "```tldraw\n"), "\n```")
	var j map[string]map[string]map[string]map[string]string
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		t.Fatal(err)
	}
	if j["store"]["s1"]["props"]["text"] != `For Asha "A" Rao {{x.y}}` || !m["x.y"] {
		t.Fatalf("%v %v", j, m)
	}
	if _, err := renderBoard("```tldraw\nnot json\n```", testVals(), m); err == nil {
		t.Fatal("invalid snapshot must fail")
	}
	// numbers keep serde's spelling
	out, _ = renderBoard("```tldraw\n{\"b\":10.50,\"a\":[1,2.0,-3]}\n```", testVals(), m)
	if out != "```tldraw\n{\"a\":[1,2.0,-3],\"b\":10.5}\n```" {
		t.Fatalf("%q", out)
	}
}

func TestMoneyUsesIndianGroupingForINR(t *testing.T) {
	for _, c := range []struct {
		minor int64
		cur   string
		want  string
	}{
		{0, "INR", "₹0.00"}, {99_999, "INR", "₹999.99"}, {100_000, "INR", "₹1,000.00"},
		{1_234_567_890, "INR", "₹1,23,45,678.90"}, {-500, "INR", "-₹5.00"}, {123_456_789, "USD", "USD 1,234,567.89"},
		{-9223372036854775808, "INR", "-₹92,23,37,20,36,85,47,758.08"},
	} {
		if got := money(c.minor, c.cur); got != c.want {
			t.Errorf("money(%d,%s) = %s, want %s", c.minor, c.cur, got, c.want)
		}
	}
}

func TestDateFormat(t *testing.T) {
	if got := date(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)); got != "1 Apr 2026" {
		t.Fatal(got)
	}
}

func TestCatalogueHasCommonAndKindFields(t *testing.T) {
	has := func(k, p string) bool {
		for _, f := range catalogue(k) {
			if f.Path == p {
				return true
			}
		}
		return false
	}
	if !has("deal", "today") || !has("deal", "contact.email") || len(catalogue("none")) != len(common) {
		t.Fatal("catalogue")
	}
}
