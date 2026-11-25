package publiclinks

import "testing"

// docs C1: the public body pass keeps highlight, cell colour and numbering, and a highlight inside a link survives
// whether the link is kept or reduced to its text.
func TestCorePassKeepsC1Forms(t *testing.T) {
	const tbl = `<table data-numbered="true"><tr><th colspan="2" data-bg="blue"><p>H</p></th></tr><tr><td data-bg="orange"><p>a <mark data-color="yellow">b</mark></p></td><td><p>c</p></td></tr></table>`
	cases := []struct{ name, in, want string }{
		{"highlight", `A <mark data-color="green"><span style="color: #c0392b">ok</span></mark> b`, `A <mark data-color="green"><span style="color: #c0392b">ok</span></mark> b`},
		{"table untouched", tbl, tbl},
		{"external link keeps the mark", `[<mark data-color="blue">a link</mark>](https://example.com/g)`, `[<mark data-color="blue">a link</mark>](https://example.com/g)`},
		{"page link keeps the mark and loses the link", `[<mark data-color="blue">Roadmap</mark>](/w/` + tWS + `/n/` + tOther + `)`, `<mark data-color="blue">Roadmap</mark>`},
		{"a mark does not hide a link target", `<mark data-color="red">[x](/w/` + tWS + `/n/` + tOther + `)</mark>`, `<mark data-color="red">x</mark>`},
		{"literal marker text is not a label block", "`<mark data-color=\"red\">code</mark>` and [[children]]", "`<mark data-color=\"red\">code</mark>` and "},
	}
	for _, c := range cases {
		got, err := corePass{}.Rewrite(c.in, scope())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s:\n in  %q\n got %q\nwant %q", c.name, c.in, got, c.want)
		}
	}
}
