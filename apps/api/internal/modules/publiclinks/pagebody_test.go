package publiclinks

import (
	"strings"
	"testing"
)

const (
	tWS    = "11111111-1111-4111-8111-111111111111"
	tOwn   = "22222222-2222-4222-8222-222222222222"
	tOther = "33333333-3333-4333-8333-333333333333"
	tUser  = "44444444-4444-4444-8444-444444444444"
	tTok   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
)

func scope() BodyScope {
	return BodyScope{WorkspaceID: tWS, NoteID: "n", Token: tTok, OwnFiles: map[string]bool{tOwn: true}}
}

func TestCorePassRewrites(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"own image", "![pic](/api/w/" + tWS + "/files/" + tOwn + ")", "![pic](/api/public/page/" + tTok + "/files/" + tOwn + ")"},
		{"own file link", "[doc](/api/w/" + tWS + "/files/" + tOwn + ")", "[doc](/api/public/page/" + tTok + "/files/" + tOwn + ")"},
		{"other page image", "![cat](/api/w/" + tWS + "/files/" + tOther + ")", "cat"},
		{"other page file link", "[their file](/api/w/" + tWS + "/files/" + tOther + ")", "their file"},
		{"other workspace file", "![x](/api/w/" + tOther + "/files/" + tOwn + ")", "x"},
		{"mention", "hi [@Ravi](/w/" + tWS + "/people/" + tUser + ") there", "hi @Ravi there"},
		{"page link", "see [Roadmap](/w/" + tWS + "/n/" + tOther + ")", "see Roadmap"},
		{"api link", "[x](/api/w/" + tWS + "/commands/note.get)", "x"},
		{"absolute app link", "[x](https://app.example.com/w/" + tWS + "/n/" + tOther + ")", "x"},
		{"external kept", "[a](https://example.com/p?q=1) [m](mailto:a@b.co) ![i](http://e.com/i.png)", "[a](https://example.com/p?q=1) [m](mailto:a@b.co) ![i](http://e.com/i.png)"},
		{"javascript", "[x](javascript:alert(1))", "x)"},
		{"relative", "[x](/somewhere) [y](#frag)", "x y"},
		{"children", "a\n\n[[children]]\n\nb", "a\n\n\n\nb"},
		{"nested image in link", "[![a](/api/w/" + tWS + "/files/" + tOther + ")](/w/" + tWS + "/n/" + tOther + ")", "a"},
		{"reference definition", "[x][r]\n\n[r]: /w/" + tWS + "/n/" + tOther, "[x][r]\n\n"},
		{"html table link", `<table><tr><td><a href="/w/` + tWS + `/n/` + tOther + `">Roadmap</a> <a href="https://e.com/a?b=1&amp;c=2">ext</a></td></tr></table>`,
			`<table><tr><td>Roadmap <a href="https://e.com/a?b=1&amp;c=2">ext</a></td></tr></table>`},
		{"html image own", `<td><img src="/api/w/` + tWS + `/files/` + tOwn + `" alt="p"></td>`, `<td><img src="/api/public/page/` + tTok + `/files/` + tOwn + `" alt="p"></td>`},
		{"html image other", `<td><img src="/api/w/` + tWS + `/files/` + tOther + `" alt="p"></td>`, `<td>p</td>`},
		{"bare path", "see /w/" + tWS + "/n/" + tOther + " now", "see  now"},
	}
	for _, c := range cases {
		got, err := corePass{}.Rewrite(c.in, scope())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s:\n in  %q\n got %q\nwant %q", c.name, c.in, got, c.want)
		}
		for _, id := range []string{tWS, tOther, tUser} {
			if strings.Contains(got, id) {
				t.Errorf("%s: output still holds %s: %q", c.name, id, got)
			}
		}
	}
}

func TestCorePassIsRegistered(t *testing.T) {
	out, err := PublicBody("[@A](/w/"+tWS+"/people/"+tUser+")", scope())
	if err != nil || out != "@A" {
		t.Fatalf("PublicBody = %q, %v", out, err)
	}
}
