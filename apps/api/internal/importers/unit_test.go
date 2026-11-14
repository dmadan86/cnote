package importers

import (
	"reflect"
	"strings"
	"testing"
)

// Ports of the #[cfg(test)] unit tests of crates/cnote-importers (jira.rs, others.rs, html.rs).
// jira::fetch_issues / validate_base_url belong to import.start (live fetch) and stay in Rust.

func TestJQLCheckIsConservative(t *testing.T) {
	for _, c := range []struct {
		jql string
		ok  bool
	}{
		{`project = ENG AND status in (Open, "In Progress") ORDER BY created`, true},
		{`assignee = currentUser()`, false},
		{`sprint in openSprints()`, false},
		{`status was "Done" during (a, b)`, false},
		{`text ~ "login"`, false},
		// Unicode-aware \b and \w like the regex crate
		{`ÉWas = 1`, true},
		{`xwas = 1`, true},
		{`status WAS x`, false},
		{`a changedé b`, true},
		{`a ſ was b`, false}, // long s is its own letter; "was" is a whole word here
		{`waſ`, false},       // (?i) folds U+017F to s
		{`Ünï(1)`, false},
		{`ÄÖ in (1,2)`, true},
		{`cf[10001] = 1`, false},
		{`CF[1]`, false},
		{`x IN (1) and (a) OR (b) NOT (c)`, true},
		{"x And\t(y)", true},
		{"x in (1)", true},
		{"foo (1)", false},
	} {
		if got := jqlSupported(c.jql); got != c.ok {
			t.Errorf("jqlSupported(%q) = %v, want %v", c.jql, got, c.ok)
		}
	}
}

func TestADFToText(t *testing.T) {
	d, err := decodeJSON(`{"type":"doc","content":[
            {"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Steps"}]},
            {"type":"paragraph","content":[{"type":"text","text":"Do it"}]},
            {"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := textOf(d); got != "## Steps\n\nDo it\n\n- a" {
		t.Fatalf("%q", got)
	}
}

func TestCSVRequiresMappedColumns(t *testing.T) {
	m := &csvMapping{Title: sp("Name")}
	if _, err := parseCSV("a,b\n1,2\n", m); err == nil {
		t.Fatal("missing column must fail")
	}
	p, err := parseCSV("Name,Extra\nFix login,x\n,y\n", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Recs) != 1 || p.Recs[0].Item.Custom["Extra"] != "x" {
		t.Fatalf("%+v", p.Recs)
	}
}

func TestSingleMarkdownPageTakesTitleFromH1(t *testing.T) {
	p, err := parsePages("notion", "# Onboarding\n\nWelcome")
	if err != nil {
		t.Fatal(err)
	}
	if p.Recs[0].Title != "Onboarding" || p.Recs[0].BodyMD != "Welcome" {
		t.Fatalf("%+v", p.Recs[0])
	}
}

func TestConvertsCommonStructures(t *testing.T) {
	html := `<h1>Runbook</h1><p>Restart the <strong>pods</strong> &amp; <em>wait</em>, see <a href="https://example.com/x">docs</a>.</p>
<ul><li>one<ul><li>nested</li></ul></li><li>two</li></ul><ol><li>a</li><li>b</li></ol>
<pre><code class="language-sh">kubectl get pods
</code></pre><blockquote><p>careful</p></blockquote>
<table><tr><th>Env</th><th>Host</th></tr><tr><td>prod</td><td>a|b</td></tr></table>`
	md, dropped := htmlToMD(html)
	if dropped != 0 {
		t.Fatal(dropped)
	}
	if !strings.HasPrefix(md, "# Runbook\n\nRestart the **pods** & *wait*, see [docs](https://example.com/x).") {
		t.Fatal(md)
	}
	for _, want := range []string{"- one\n  - nested\n- two", "1. a\n2. b", "```sh\nkubectl get pods\n```", "> careful", "| Env | Host |\n| --- | --- |\n| prod | a\\|b |"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in %q", want, md)
		}
	}
}

func TestConfluenceCodeMacroAndUnknownMacro(t *testing.T) {
	html := `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">sql</ac:parameter><ac:plain-text-body><![CDATA[select 1 < 2;]]></ac:plain-text-body></ac:structured-macro>
<ac:structured-macro ac:name="toc"/><p>after</p>`
	md, dropped := htmlToMD(html)
	if !strings.Contains(md, "```sql\nselect 1 < 2;\n```") || !strings.HasSuffix(md, "after") || dropped != 1 {
		t.Fatalf("%q %d", md, dropped)
	}
}

func TestTitleAndScriptSkipping(t *testing.T) {
	html := "<html><head><title>My Page</title><script>var a = '<b>';</script></head><body><p>hi</p></body></html>"
	if got := firstText(html, "title"); got == nil || *got != "My Page" {
		t.Fatal(got)
	}
	if md, _ := htmlToMD(html); md != "hi" {
		t.Fatal(md)
	}
}

// Chrono parity for the timestamp and date helpers (expected values worked out from chrono 0.4.45 parse.rs).
func TestParseTS(t *testing.T) {
	for in, want := range map[string]string{
		"2024-01-11T10:00:00.000+0530":          "2024-01-11T04:30:00Z",
		"2024-01-11T10:00:00+05:30":             "2024-01-11T04:30:00Z",
		"2024-01-11T10:00:00Z":                  "2024-01-11T10:00:00Z",
		"2024-01-11t10:00:00z":                  "2024-01-11T10:00:00Z",
		"2024-01-11 10:00:00+00:00":             "2024-01-11T10:00:00Z",
		"2024-1-5T9:5:7+0000":                   "2024-01-05T09:05:07Z",
		"2024-01-11T10:00:00.123456789012+0000": "2024-01-11T10:00:00.123456789Z",
		"2024-01-11T10:00:00-0800":              "2024-01-11T18:00:00Z",
		"2024-01-11T10:00:00 +0100":             "2024-01-11T09:00:00Z",
		"2024-01-11T10:00:00+01:00:":            "",
		"2024-01-11T10:00:00":                   "",
		"2024-02-30T10:00:00Z":                  "",
		"2024-01-11T10:00:00+2400":              "",
		"2024-01-11T10:00:00+05":                "",
		"garbage":                               "",
		"":                                      "",
	} {
		got := parseTS(in)
		switch {
		case want == "" && got != nil:
			t.Errorf("%q parsed to %v", in, got)
		case want != "" && got == nil:
			t.Errorf("%q not parsed", in)
		case want != "" && got.Format("2006-01-02T15:04:05.999999999Z07:00") != want:
			t.Errorf("%q = %s want %s", in, got.Format("2006-01-02T15:04:05.999999999Z07:00"), want)
		}
	}
}

func TestParseDate(t *testing.T) {
	for in, want := range map[string]string{
		"2024-03-05": "2024-03-05", "2024-03-05T10:00:00": "2024-03-05", "2024-3-5xxxxx": "", "2024-02-30": "", "20240305": "",
		"2024-03-0é": "", "short": "",
	} {
		got := parseDate(in)
		if (got == nil) != (want == "") || (got != nil && *got != want) {
			t.Errorf("%q = %v want %q", in, got, want)
		}
	}
}

func TestSlugAndLower(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!": "hello-world", "  --Épic Ü--  ": "épic-ü", "日本語 テスト": "日本語-テスト", "": "", "!!!": "",
		"İstanbul": "i-stanbul", // U+0130 lowercases to i + combining dot (not alphanumeric)
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q want %q", in, got, want)
		}
	}
	if got := rustLower("ΣΑΣ Σ"); got != "σας σ" {
		t.Errorf("final sigma: %q", got)
	}
}

func TestCSVReaderMatchesCsvCore(t *testing.T) {
	got := readCSV("a,b\r\n\r\n\"x\"\"y\",\"l1\nl2\"\n\"q\"r,s\"t\n,\nlast")
	want := [][]string{{"a", "b"}, {`x"y`, "l1\nl2"}, {"qr", `s"t`}, {"", ""}, {"last"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}
