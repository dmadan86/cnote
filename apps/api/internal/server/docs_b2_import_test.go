package server_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cnote/api/internal/importers"
)

// Word import and template prompts (docs-parity-b 5.7, 5.8, 13.2.8): the command and the raw route share one parser,
// so every hostile file gets the same verdict on both.

const (
	q3CT = `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	q3NS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
)

// q3PNG is a real 1x1 PNG, so the files path's type check passes.
var q3PNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func q3Zip(parts map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, d := range parts {
		w, _ := zw.Create(n)
		_, _ = w.Write(d)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func q3Docx(body, rels string, extra map[string][]byte) []byte {
	p := map[string][]byte{
		"[Content_Types].xml":          []byte(q3CT),
		"word/document.xml":            []byte(`<?xml version="1.0"?><w:document ` + q3NS + `><w:body>` + body + `</w:body></w:document>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels + `</Relationships>`),
	}
	for n, d := range extra {
		p[n] = d
	}
	return q3Zip(p)
}

func q3Para(style, text string) string {
	s := ""
	if style != "" {
		s = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + s + `<w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func q3Pic(id string) string {
	return `<w:p><w:r><w:drawing><a:blip r:embed="` + id + `"/></w:drawing></w:r></w:p>`
}

func q3R(id, typ, target string, external bool) string {
	ext := ""
	if external {
		ext = ` TargetMode="External"`
	}
	return `<Relationship Id="` + id + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/` + typ + `" Target="` + target + `"` + ext + `/>`
}

func (d dg) q3Import(w who, in map[string]any, data []byte) resp {
	d.t.Helper()
	in["data_base64"] = base64.StdEncoding.EncodeToString(data)
	return d.run(w, "import.docx", in)
}

// q3HTTP posts the raw file the way the browser does.
func (d dg) q3HTTP(w who, space, parent, name string, data []byte) resp {
	d.t.Helper()
	q := url.Values{"space_id": {space}, "filename": {name}}
	if parent != "" {
		q.Set("parent_id", parent)
	}
	req, _ := http.NewRequest("POST", d.e.gosv.URL+"/api/w/"+w.ws+"/import/docx?"+q.Encode(), bytes.NewReader(data))
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b}
}

func q3Code(r resp) string {
	e, _ := r.json()["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestDocxImportEveryFixtureOnBothPaths(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "WORD", "open")
	parent := d.page(owner, space, "Imports", nil, "")

	zipBomb := func() []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: zip.Deflate})
		zeros := make([]byte, 1<<20)
		for i := 0; i < 60; i++ {
			_, _ = w.Write(zeros)
		}
		for n, c := range map[string]string{"[Content_Types].xml": q3CT} {
			f, _ := zw.Create(n)
			_, _ = f.Write([]byte(c))
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	many := map[string][]byte{}
	for i := 0; i < 1100; i++ {
		many["word/extra/"+strconv.Itoa(i)+".txt"] = []byte("x")
	}
	deep := strings.Repeat("<w:p>", 100) + `<w:r><w:t>deep</w:t></w:r>` + strings.Repeat("</w:p>", 100)
	macro := q3Zip(map[string][]byte{"[Content_Types].xml": []byte(strings.Replace(q3CT, "document.main+xml", "document.macroEnabled.main+xml", 1)),
		"word/document.xml": []byte(`<w:document ` + q3NS + `><w:body/></w:document>`), "word/vbaProject.bin": []byte("x")})
	hindi := q3Docx(q3Para("", "नमस्ते दुनिया"), "", nil)

	cases := []struct {
		name  string
		data  []byte
		ok    bool
		title string   // expected title, "" = do not check
		has   []string // in the page body
		not   []string // never in the page body
		warn  string
	}{
		{name: "simple.docx", ok: true, title: "Simple", data: q3Docx(q3Para("Title", "Simple")+q3Para("", "Body text.")+`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Bold</w:t></w:r></w:p>`, "", nil),
			has: []string{"Body text.", "**Bold**"}, not: []string{"Simple"}},
		{name: "hindi.docx", ok: true, title: "hindi", data: hindi, has: []string{"नमस्ते दुनिया"}},
		{name: "link.docx", ok: true, data: q3Docx(`<w:p><w:hyperlink r:id="a"><w:r><w:t>Site</w:t></w:r></w:hyperlink><w:hyperlink r:id="b"><w:r><w:t>Click me</w:t></w:r></w:hyperlink></w:p>`,
			q3R("a", "hyperlink", "https://example.com", true)+q3R("b", "hyperlink", "javascript:alert(1)", true), nil),
			has: []string{"[Site](https://example.com)", "Click me"}, not: []string{"javascript"}, warn: "unsafe"},
		{name: "extimg.docx", ok: true, data: q3Docx(q3Para("", "No fetch.")+q3Pic("x"), q3R("x", "image", "http://127.0.0.1:1/x.png", true), nil),
			has: []string{"No fetch."}, not: []string{"127.0.0.1", "!["}, warn: "web were dropped"},
		{name: "escape.docx", ok: true, data: q3Docx(q3Para("", "&lt;script&gt;x&lt;/script&gt; [a](javascript:alert(1))"), "", nil),
			not: []string{" <script", "\n<script"}, has: []string{`\<script\>`, `\[a\]`}},
		{name: "deep.docx", data: q3Docx(strings.Repeat("<w:p>", 100)+"</w:p>", "", nil)},
		{name: "deep64.docx", data: q3Docx(deep, "", nil)},
		{name: "bomb.docx", data: zipBomb()},
		{name: "many.docx", data: q3Docx(q3Para("", "x"), "", many)},
		{name: "macro.docx", data: macro},
		{name: "notzip.docx", data: []byte{0xDE, 0xAD, 0xBE, 0xEF, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
		{name: "legacy.doc", data: append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 600)...)},
		{name: "empty.docx", data: []byte{}},
	}
	for _, c := range cases {
		for _, path := range []string{"command", "http"} {
			var r resp
			if path == "command" {
				r = d.q3Import(owner, map[string]any{"space_id": space, "parent_id": parent, "filename": c.name}, c.data)
			} else {
				r = d.q3HTTP(owner, space, parent, c.name, c.data)
			}
			if !c.ok {
				if r.status != 400 || q3Code(r) != "invalid" {
					t.Errorf("%s via %s: want 400 invalid, got %d %s", c.name, path, r.status, r.body)
				}
				continue
			}
			if r.status != 200 {
				t.Errorf("%s via %s: %d %s", c.name, path, r.status, r.body)
				continue
			}
			out := r.json()
			note := d.ok(owner, "note.get", map[string]any{"id": out["id"]})
			body := note["content_md"].(string)
			if note["parent_id"] != parent || (c.title != "" && out["title"] != c.title) || out["title"] != note["title"] {
				t.Errorf("%s via %s: title/parent: %v / %v", c.name, path, out, note["title"])
			}
			for _, s := range c.has {
				if !strings.Contains(body, s) {
					t.Errorf("%s via %s: body lacks %q: %s", c.name, path, s, body)
				}
			}
			for _, s := range c.not {
				if strings.Contains(body, s) {
					t.Errorf("%s via %s: body has %q: %s", c.name, path, s, body)
				}
			}
			if c.warn != "" && !strings.Contains(string(r.body), c.warn) {
				t.Errorf("%s via %s: no warning %q: %s", c.name, path, c.warn, r.body)
			}
			if _, ok := out["warnings"].([]any); !ok {
				t.Errorf("%s via %s: warnings must be a list: %s", c.name, path, r.body)
			}
		}
	}
	// nothing of any original file was stored
	if n := d.scalar("select count(*)::text from core.attachments where lower(filename) like '%.doc%'"); n != "0" {
		t.Errorf("a Word file was stored: %s", n)
	}
}

func TestDocxImportPicturesBecomeUploads(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "PIC", "open")
	rels := q3R("a", "image", "media/a.png", false) + q3R("b", "image", "media/b.svg", false) + q3R("c", "image", "media/c.png", false)
	data := q3Docx(q3Para("", "Pictures")+q3Pic("a")+q3Pic("b")+q3Pic("c"), rels, map[string][]byte{
		"word/media/a.png": q3PNG, "word/media/b.svg": []byte(`<svg onload="alert(1)"/>`), "word/media/c.png": []byte("this is not a png"),
	})
	for _, path := range []string{"command", "http"} {
		var r resp
		if path == "command" {
			r = d.q3Import(owner, map[string]any{"space_id": space, "filename": "p.docx"}, data)
		} else {
			r = d.q3HTTP(owner, space, "", "p.docx", data)
		}
		if r.status != 200 {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		out := r.json()
		files := d.list(owner, "file.list", map[string]any{"note_id": out["id"]})
		body := d.ok(owner, "note.get", map[string]any{"id": out["id"]})["content_md"].(string)
		if len(files) != 1 || files[0]["content_type"] != "image/png" || !strings.Contains(body, "](/api/w/"+owner.ws+"/files/"+files[0]["id"].(string)+")") {
			t.Fatalf("%s: files %v body %s", path, files, body)
		}
		// the svg was dropped by the parser, the fake png was refused by the files path, and the page says so
		if !strings.Contains(string(r.body), "other than JPEG") || !strings.Contains(string(r.body), "could not be added") || strings.Contains(body, "\x00") || strings.Contains(body, "IMG") {
			t.Fatalf("%s: warnings %s body %s", path, r.body, body)
		}
	}
}

func TestDocxImportAccess(t *testing.T) {
	d, owner := newDG(t)
	member, viewer, outsider := d.member(owner), d.member(owner), d.member(owner)
	open := d.space(owner, "OPENW", "open")
	private := d.space(owner, "HUSHW", "private")
	d.ok(owner, "space.member_set", map[string]any{"space_id": open, "user_id": viewer.userID, "role": "viewer"})
	good := q3Docx(q3Para("", "hello"), "", nil)

	if r := d.q3Import(member, map[string]any{"space_id": open, "filename": "a.docx"}, good); r.status != 200 {
		t.Fatalf("member: %d %s", r.status, r.body)
	}
	for _, path := range []string{"command", "http"} {
		do := func(w who, space string) resp {
			if path == "command" {
				return d.q3Import(w, map[string]any{"space_id": space, "filename": "a.docx"}, good)
			}
			return d.q3HTTP(w, space, "", "a.docx", good)
		}
		if r := do(viewer, open); r.status != 403 {
			t.Errorf("%s: a viewer imported: %d %s", path, r.status, r.body)
		}
		if r := do(outsider, private); r.status == 200 {
			t.Errorf("%s: outsider imported into a private space", path)
		}
		d.archive(open, true)
		if r := do(owner, open); r.status != 403 || !strings.Contains(string(r.body), archivedMsg) {
			t.Errorf("%s: archived space: %d %s", path, r.status, r.body)
		}
		d.archive(open, false)
	}
	// a restricted parent needs edit access to the parent
	parent := d.page(owner, open, "Locked", nil, "")
	d.ok(owner, "note.restrict", map[string]any{"note_id": parent, "restriction": "edit"})
	if r := d.q3Import(member, map[string]any{"space_id": open, "parent_id": parent, "filename": "a.docx"}, good); r.status == 200 {
		t.Error("imported under a page the member cannot edit")
	}
	// no session, no import
	req, _ := http.NewRequest("POST", d.e.gosv.URL+"/api/w/"+owner.ws+"/import/docx?space_id="+open+"&filename=a.docx", bytes.NewReader(good))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 401 {
		t.Errorf("anonymous: %v %v", err, res)
	}
}

func TestDocxImportSizeAndConcurrencyCaps(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "CAPS", "open")
	big := make([]byte, importers.DocxMaxInput+1)
	if r := d.q3HTTP(owner, space, "", "big.docx", big); r.status != 400 || q3Code(r) != "invalid" {
		t.Errorf("http oversize: %d %s", r.status, r.body)
	}
	if r := d.q3Import(owner, map[string]any{"space_id": space, "filename": "big.docx"}, big); r.status != 400 && r.status != 413 {
		t.Errorf("command oversize: %d %s", r.status, r.body)
	}

	good := q3Docx(q3Para("", "hello"), "", nil)
	r1, ok1 := importers.AcquireDocx(owner.ws)
	r2, ok2 := importers.AcquireDocx(owner.ws)
	if !ok1 || !ok2 {
		t.Fatal("could not take the two slots")
	}
	for _, path := range []string{"command", "http"} {
		var r resp
		if path == "command" {
			r = d.q3Import(owner, map[string]any{"space_id": space, "filename": "a.docx"}, good)
		} else {
			r = d.q3HTTP(owner, space, "", "a.docx", good)
		}
		if r.status != 429 || q3Code(r) != "rate_limited" {
			t.Errorf("%s with both slots taken: %d %s", path, r.status, r.body)
		}
	}
	// another workspace is not affected, and a freed slot works again
	other := d.e.signup("Other", true)
	if r := d.run(other, "space.create", map[string]any{"key": "OTH", "name": "OTH", "visibility": "open"}); r.status != 200 {
		t.Fatal(r.body)
	}
	r1()
	if r := d.q3Import(owner, map[string]any{"space_id": space, "filename": "a.docx"}, good); r.status != 200 {
		t.Errorf("after release: %d %s", r.status, r.body)
	}
	r2()
	// a failed import gives its slot back
	for i := 0; i < 5; i++ {
		d.q3Import(owner, map[string]any{"space_id": space, "filename": "x.docx"}, []byte("not a zip at all, sorry"))
	}
	if r := d.q3Import(owner, map[string]any{"space_id": space, "filename": "a.docx"}, good); r.status != 200 {
		t.Errorf("slots leaked: %d %s", r.status, r.body)
	}
}

// ---------- template prompts ----------

func (d dg) q3Template(w who, space, title, body string) string {
	d.t.Helper()
	n := d.ok(w, "note.create", map[string]any{"space_id": space, "title": title, "content_md": body})
	return d.ok(w, "template.save", map[string]any{"note_id": n["id"], "name": title, "source_kind": "none"})["id"].(string)
}

func TestTemplatePromptsListAndRender(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "TPL", "open")
	id := d.q3Template(owner, space, "Brief for {{ask.client_name}}", "Client {{ask.client_name}} meets on {{ask.meeting_date}}.\n\n| Who | Note |\n| --- | --- |\n| {{ask.client_name}} | {{ask.remark}} |\n\nBy {{user.name}} {{ask.client_name}}")

	var got map[string]any
	for _, x := range d.list(owner, "template.list", map[string]any{}) {
		if x["id"] == id {
			got = x
		}
	}
	ps, _ := got["prompts"].([]any)
	if len(ps) != 3 || ps[0].(map[string]any)["key"] != "client_name" || ps[0].(map[string]any)["label"] != "Client name" || ps[1].(map[string]any)["key"] != "meeting_date" || ps[2].(map[string]any)["key"] != "remark" {
		t.Fatalf("prompts: %v", got["prompts"])
	}
	// built-ins and templates without prompts list an empty array, never null
	for _, x := range d.list(owner, "template.list", map[string]any{}) {
		if x["builtin"] == true && x["name"] == "Meeting notes (MoM)" {
			if p, _ := json.Marshal(x["prompts"]); string(p) != `[{"key":"date","label":"Date"},{"key":"attendees","label":"Attendees"}]` {
				t.Fatalf("meeting notes prompts: %s", p)
			}
		} else if x["prompts"] == nil {
			t.Fatalf("%v has no prompts array", x["name"])
		}
	}

	hostile := "**bold** [x](http://evil.test/p) <b>t</b> `c` # h ![i](http://e/x.png) http://bare.test | pipe"
	r := d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"client_name": hostile, "meeting_date": "1. 12 Dec", "remark": "- x | y"}})
	note := r["note"].(map[string]any)
	body := note["content_md"].(string)
	for _, bad := range []string{"**bold**", "[x](", "<b>", "![i]", "\n# h", "\n- x", "\n1. 12", "| pipe |", "](http"} {
		if strings.Contains(body, strings.Replace(bad, "\n", "", 1)) && !strings.Contains(body, `\`+strings.TrimLeft(bad, "\n")) {
			t.Errorf("%q survived unescaped in:\n%s", bad, body)
		}
	}
	for _, want := range []string{`\*\*bold\*\*`, `\[x\]\(http`, `\<b\>t`, `\!\[i\]`, `\|`, `\://bare.test`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "\n") != strings.Count(strings.ReplaceAll(body, "\r", ""), "\n") || len(r["unresolved"].([]any)) != 0 {
		t.Errorf("unresolved or odd body: %v", r["unresolved"])
	}
	// the table is still a three-line table: a pipe in an answer did not add a column
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "| \\*\\*bold") && strings.Count(strings.ReplaceAll(l, `\|`, ""), "|") != 3 {
			t.Errorf("answer changed the table: %s", l)
		}
	}
	// the title is text as typed
	if note["title"] != "Brief for "+hostile {
		t.Errorf("title: %v", note["title"])
	}

	// an unanswered prompt stays as its placeholder and is listed as unresolved
	r = d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"client_name": "Asha"}})
	if u := r["unresolved"].([]any); len(u) != 2 || u[0] != "ask.meeting_date" || u[1] != "ask.remark" {
		t.Errorf("unresolved: %v", u)
	}
	if b := r["note"].(map[string]any)["content_md"].(string); !strings.Contains(b, "{{ask.meeting_date}}") || !strings.Contains(b, "Client Asha meets") {
		t.Errorf("body: %s", b)
	}
	// blank means unanswered; the prefix is accepted
	r = d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"ask.client_name": "Ravi", "meeting_date": "  "}})
	if len(r["unresolved"].([]any)) != 2 || !strings.Contains(r["note"].(map[string]any)["content_md"].(string), "Client Ravi") {
		t.Errorf("prefixed key: %v", r)
	}

	// refusals
	for name, answers := range map[string]any{
		"too long":  map[string]any{"client_name": strings.Repeat("a", 501)},
		"two lines": map[string]any{"client_name": "a\nb"},
		"unknown":   map[string]any{"nope": "x"},
		"not text":  map[string]any{"client_name": 5},
		"a list":    []any{"x"},
		"bad key":   map[string]any{"client name": "x"},
	} {
		if r := d.run(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": answers}); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := d.run(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"client_name": strings.Repeat("é", 500)}}); r.status != 200 {
		t.Errorf("500 characters must pass: %d %s", r.status, r.body)
	}
}

func TestTemplatePromptsSurviveRefreshAndBuiltins(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "TPR", "open")
	id := d.q3Template(owner, space, "Plan {{ask.topic}}", "Topic: {{ask.topic}} on {{today}}")
	r := d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"topic": "Q4 *launch*"}})
	page := r["note"].(map[string]any)["id"].(string)
	if s := d.scalar("select answers::text from docs.template_bindings where note_id = $1::uuid", page); !strings.Contains(s, "Q4 *launch*") {
		t.Fatalf("binding answers: %s", s)
	}
	d.sql("update core.notes set content_md = 'scribble' where id = $1::uuid", page)
	rr := d.ok(owner, "template.refresh", map[string]any{"note_id": page})
	body := d.ok(owner, "note.get", map[string]any{"id": page})["content_md"].(string)
	if !strings.HasPrefix(body, `Topic: Q4 \*launch\*`) || len(rr["unresolved"].([]any)) != 0 {
		t.Fatalf("refresh lost the answers: %q %v", body, rr)
	}
	// no answers, no stored answers
	r = d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space})
	if s := d.scalar("select coalesce(answers::text,'null') from docs.template_bindings where note_id = $1::uuid", r["note"].(map[string]any)["id"]); s != "null" {
		t.Errorf("answers stored without any: %s", s)
	}

	// a built-in with prompts renders with answers; unanswered ones are listed
	r = d.ok(owner, "template.render", map[string]any{"template_id": "builtin:meeting-notes", "space_id": space, "answers": map[string]any{"date": "3 Dec 2026", "attendees": "Asha, Ravi"}})
	b := r["note"].(map[string]any)["content_md"].(string)
	if !strings.Contains(b, "Date: 3 Dec 2026.") || !strings.Contains(b, "- Asha, Ravi") || len(r["unresolved"].([]any)) != 0 {
		t.Errorf("built-in: %v\n%s", r["unresolved"], b)
	}
	r = d.ok(owner, "template.render", map[string]any{"template_id": "builtin:meeting-notes", "space_id": space})
	if u := r["unresolved"].([]any); len(u) != 2 || u[0] != "ask.attendees" || u[1] != "ask.date" {
		t.Errorf("built-in unanswered: %v", u)
	}
	// a template without that prompt refuses the key; a built-in id is not a UUID but still checked
	if r := d.run(owner, "template.render", map[string]any{"template_id": "builtin:sop", "space_id": space, "answers": map[string]any{"attendees": "x"}}); r.status != 400 {
		t.Errorf("sop: %d %s", r.status, r.body)
	}
}

// The editor writes an underscore as `\_`, so a placeholder typed in a page must still be found and filled.
func TestTemplatePromptsInEditorEscapedForm(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "TPE", "open")
	id := d.q3Template(owner, space, "Visit", `Hello {{ask.client\_name}} on {{ deal.expected\_close }}`)
	for _, x := range d.list(owner, "template.list", map[string]any{}) {
		if x["id"] == id {
			if p, _ := json.Marshal(x["prompts"]); string(p) != `[{"key":"client_name","label":"Client name"}]` {
				t.Fatalf("prompts: %s", p)
			}
		}
	}
	r := d.ok(owner, "template.render", map[string]any{"template_id": id, "space_id": space, "answers": map[string]any{"client_name": "Asha"}})
	if b := r["note"].(map[string]any)["content_md"].(string); !strings.HasPrefix(b, "Hello Asha on ") {
		t.Errorf("body: %s", b)
	}
	if u := r["unresolved"].([]any); len(u) != 1 || u[0] != "deal.expected_close" {
		t.Errorf("unresolved: %v", u)
	}
}
