package importers

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
	"time"
)

const wNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`

func relsOf(items ...string) string {
	return `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + strings.Join(items, "") + `</Relationships>`
}

func rel(id, typ, target string, external bool) string {
	m := ""
	if external {
		m = ` TargetMode="External"`
	}
	return `<Relationship Id="` + id + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/` + typ + `" Target="` + target + `"` + m + `/>`
}

func bodyDoc(body string) string {
	return `<?xml version="1.0"?><w:document ` + wNS + `><w:body>` + body + `</w:body></w:document>`
}

// build makes a package from a body, relationships and extra parts.
func build(body, rels string, extra map[string][]byte) []byte {
	buf, zw := newDocx()
	addFile(zw, "word/document.xml", bodyDoc(body))
	for n, d := range extra {
		addBytes(zw, n, d)
	}
	if rels == "" {
		rels = docRelsXML
	}
	return sealDocx(buf, zw, contentTypesXML, rels)
}

func mustParse(t *testing.T, data []byte) *DocxResult {
	t.Helper()
	r, err := ParseDocx(context.Background(), data)
	if err != nil {
		t.Fatalf("ParseDocx: %v", err)
	}
	return r
}

func wantRefused(t *testing.T, data []byte, contains string) {
	t.Helper()
	_, err := ParseDocx(context.Background(), data)
	var de *DocxError
	if !errors.As(err, &de) || !strings.Contains(de.Msg, contains) {
		t.Fatalf("want refusal containing %q, got %v", contains, err)
	}
}

func hasWarning(r *DocxResult, sub string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestDocxSimpleAndHindi(t *testing.T) {
	r := mustParse(t, docxSimple())
	if r.Title != "Simple Document Title" || strings.Contains(r.Markdown, "Simple Document") {
		t.Fatalf("title %q md %q", r.Title, r.Markdown)
	}
	if !strings.Contains(r.Markdown, "First paragraph of body text.") || !strings.Contains(r.Markdown, "**Bold run.** *Italic run.*") {
		t.Fatalf("md: %q", r.Markdown)
	}
	h := mustParse(t, docxHindi())
	if !strings.Contains(h.Markdown, "नमस्ते दुनिया") || h.Title != "" {
		t.Fatalf("hindi: %q %q", h.Title, h.Markdown)
	}
}

func TestDocxRichKeepsStructure(t *testing.T) {
	r := mustParse(t, docxRich())
	for _, want := range []string{"## Heading Two", "### Heading Three", "- Bullet item one", "- Bullet item two", "1. Numbered item one", "2. Numbered item two",
		"| R1C1 | R1C2 | R1C3 |", "| --- | --- | --- |", "| R3C1 | R3C2 | R3C3 |", "[Visit example](https://example.com)"} {
		if !strings.Contains(r.Markdown, want) {
			t.Errorf("missing %q in:\n%s", want, r.Markdown)
		}
	}
	if r.Title != "Heading One" {
		t.Errorf("title %q", r.Title)
	}
	if len(r.Images) != 1 || r.Images[0].Name != "image1.png" || !strings.Contains(r.Markdown, r.Images[0].Ref) {
		t.Fatalf("images: %+v", r.Images)
	}
}

func TestDocxHostileFixtures(t *testing.T) {
	wantRefused(t, docxDeepNesting(), "nested too deeply")
	wantRefused(t, docxZipBomb(), "too large")
	wantRefused(t, docxManyEntries(), "too many parts")
	wantRefused(t, docxMacro(), "macros")
	wantRefused(t, docxNotAZip(), "not a Word")
	wantRefused(t, make([]byte, DocxMaxInput+1), "larger than 10 MB")

	// an external picture is never fetched or kept; the page text survives
	r := mustParse(t, docxExternalImage())
	if len(r.Images) != 0 || !strings.Contains(r.Markdown, "External image reference.") {
		t.Fatalf("external image: %+v", r)
	}
	// a javascript: link stays text
	j := mustParse(t, docxJavascriptLink())
	if strings.Contains(strings.ToLower(j.Markdown), "javascript") || !strings.Contains(j.Markdown, "Click me") || !hasWarning(j, "unsafe") {
		t.Fatalf("js link: %q %v", j.Markdown, j.Warnings)
	}
}

func TestDocxLyingSizesAndCumulativeLimit(t *testing.T) {
	// A part whose header claims 100 bytes but inflates to 60 MB: the read limit, not the header, stops it.
	var comp bytes.Buffer
	fw, _ := flate.NewWriter(&comp, flate.BestCompression)
	zeros := make([]byte, 1<<20)
	for i := 0; i < 60; i++ {
		fw.Write(zeros)
	}
	fw.Close()
	buf, zw := newDocx()
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "word/document.xml", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(bytes.Repeat(zeros, 60)),
		CompressedSize64: uint64(comp.Len()), UncompressedSize64: 100})
	if err != nil {
		t.Fatal(err)
	}
	w.Write(comp.Bytes())
	wantRefused(t, sealDocx(buf, zw, contentTypesXML, docRelsXML), "") // the zip reader or the read cap, whichever sees it first

	// 25 MB of document plus three 10 MB pictures is over the 50 MB package allowance
	pad := strings.Repeat(" ", 25<<20)
	pics := ""
	extra := map[string][]byte{}
	rs := []string{}
	for i := 0; i < 3; i++ {
		id := "rId" + itoa(i)
		pics += `<w:p><w:r><w:drawing><a:blip r:embed="` + id + `"/></w:drawing></w:r></w:p>`
		extra["word/media/p"+itoa(i)+".png"] = bytes.Repeat([]byte{1}, 10<<20-1)
		rs = append(rs, rel(id, "image", "media/p"+itoa(i)+".png", false))
	}
	r := mustParse(t, build(pad+pics, relsOf(rs...), extra))
	if len(r.Images) == 3 && !hasWarning(r, "could not be read") {
		t.Fatalf("cumulative limit not applied: %d images", len(r.Images))
	}
}

func TestDocxTokenAndOutputLimits(t *testing.T) {
	wantRefused(t, build(strings.Repeat("<w:p/>", 1_100_000), "", nil), "too complex")
	wantRefused(t, build("<w:p><w:r><w:t>"+strings.Repeat("a", 3<<20)+"</w:t></w:r></w:p>", "", nil), "too long")
}

func TestDocxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseDocx(ctx, docxSimple()); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
	// cancelled while reading a large part: the read loop notices
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	if _, err := ParseDocx(ctx, build(strings.Repeat("<w:p/>", 500_000), "", nil)); err == nil {
		t.Fatal("expired context was ignored")
	}
}

func TestDocxRefusesNonWordPackages(t *testing.T) {
	for name, ct := range map[string]string{
		"template": strings.Replace(contentTypesXML, "document.main+xml", "template.main+xml", 1),
		"macro":    contentTypesMacroXML,
		"none":     `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
	} {
		buf, zw := newDocx()
		addFile(zw, "word/document.xml", bodyDoc("<w:p/>"))
		if err := func() error { addFile(zw, "[Content_Types].xml", ct); return zw.Close() }(); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseDocx(context.Background(), buf.Bytes()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// the file name decides nothing: a zip without word/document.xml is not a Word file
	buf, zw := newDocx()
	addFile(zw, "[Content_Types].xml", contentTypesXML)
	zw.Close()
	wantRefused(t, buf.Bytes(), "not a Word")
}

func TestDocxTextStaysText(t *testing.T) {
	r := mustParse(t, build(`<w:p><w:r><w:t>&lt;script&gt;alert(1)&lt;/script&gt; [x](javascript:alert(1)) **no** | a | `+"`c`"+` ![i](http://e/x.png)</w:t></w:r></w:p>`+
		`<w:p><w:r><w:t># not a heading</w:t></w:r></w:p><w:p><w:r><w:t>- not a list</w:t></w:r></w:p><w:p><w:r><w:t>1. nor this</w:t></w:r></w:p>`, "", nil))
	m := r.Markdown
	for _, bad := range []string{"<script", "](javascript", "\n# ", "\n- not", "\n1. nor", "![i]"} {
		if strings.Contains("\n"+m, bad) && !strings.Contains("\n"+m, `\`+bad) { // present only when not backslash-escaped
			t.Errorf("%q leaked into:\n%s", bad, m)
		}
	}
	if !strings.Contains(m, `\<script\>`) || !strings.Contains(m, `\[x\](javascript`) {
		t.Errorf("text not escaped:\n%s", m)
	}
}

func TestDocxLinksAndRelationshipTargets(t *testing.T) {
	rels := relsOf(
		rel("ok", "hyperlink", "https://example.com/a b(c)", true),
		rel("mail", "hyperlink", "mailto:a@b.co", true),
		rel("file", "hyperlink", "file:///etc/passwd", true),
		rel("data", "hyperlink", "data:text/html,&lt;script&gt;", true),
		rel("rel", "hyperlink", "doc.html", false),
		rel("up", "image", "../../evil.png", false),
		rel("abs", "image", "/../../evil.png", false),
		rel("ext", "image", "https://e.test/x.png", true),
		rel("svg", "image", "media/a.svg", false),
		rel("emf", "image", "media/a.emf", false))
	link := func(id, text string) string {
		return `<w:p><w:hyperlink r:id="` + id + `"><w:r><w:t>` + text + `</w:t></w:r></w:hyperlink></w:p>`
	}
	pic := func(id string) string {
		return `<w:p><w:r><w:drawing><a:blip r:embed="` + id + `"/></w:drawing></w:r></w:p>`
	}
	body := link("ok", "A") + link("mail", "M") + link("file", "F") + link("data", "D") + link("rel", "R") + pic("up") + pic("abs") + pic("ext") + pic("svg") + pic("emf") +
		`<w:p><w:r><w:drawing><a:blip r:link="ext"/></w:drawing></w:r></w:p><w:p><w:altChunk r:id="x"/></w:p>`
	r := mustParse(t, build(body, rels, map[string][]byte{"word/media/a.svg": []byte("<svg onload=alert(1)>"), "word/media/a.emf": {1}}))
	if !strings.Contains(r.Markdown, "[A](https://example.com/a%20b%28c%29)") || !strings.Contains(r.Markdown, "[M](mailto:a@b.co)") {
		t.Errorf("good links lost: %s", r.Markdown)
	}
	for _, bad := range []string{"file:", "data:", "doc.html", "evil"} {
		if strings.Contains(r.Markdown, bad) {
			t.Errorf("%s kept: %s", bad, r.Markdown)
		}
	}
	if len(r.Images) != 0 || !hasWarning(r, "web were dropped") || !hasWarning(r, "other than JPEG") {
		t.Errorf("pictures: %d %v", len(r.Images), r.Warnings)
	}
	for _, c := range []struct {
		in string
		ok bool
	}{{"media/a.png", true}, {"../media/a.png", true}, {"/word/media/a.png", true}, {"../../a.png", false}, {"/../a.png", false}, {"a\\b.png", false}, {"..", false}} {
		if p, ok := inPackage(c.in); ok != c.ok {
			t.Errorf("inPackage(%q) = %q, %v", c.in, p, ok)
		}
	}
}

func TestDocxPictureCapAndDroppedParts(t *testing.T) {
	var pics, rs strings.Builder
	extra := map[string][]byte{"word/footnotes.xml": []byte("<x/>"), "word/comments.xml": []byte("<x/>"), "word/header1.xml": []byte("<x/>"), "word/embeddings/o.bin": []byte("x")}
	for i := 0; i < 21; i++ {
		id, name := "i"+itoa(i), "m"+itoa(i)+".png"
		pics.WriteString(`<w:p><w:r><w:drawing><a:blip r:embed="` + id + `"/></w:drawing></w:r></w:p>`)
		rs.WriteString(rel(id, "image", "media/"+name, false))
		extra["word/media/"+name] = []byte{byte(i)}
	}
	body := pics.String() + `<w:p><w:r><w:t>kept</w:t></w:r><w:del><w:r><w:delText>gone</w:delText></w:r></w:del></w:p>` +
		`<w:p><w:r><mc:AlternateContent xmlns:mc="x"><mc:Choice><w:txbxContent><w:p><w:r><w:t>boxed</w:t></w:r></w:p></w:txbxContent></mc:Choice><mc:Fallback><w:t>fallback</w:t></mc:Fallback></mc:AlternateContent></w:r></w:p>`
	r := mustParse(t, build(body, relsOf(rs.String()), extra))
	if len(r.Images) != DocxMaxImages || !hasWarning(r, "first 20") {
		t.Errorf("images %d %v", len(r.Images), r.Warnings)
	}
	for _, w := range []string{"footnotes", "comments", "headers and footers", "embedded objects", "tracked deletions", "text boxes"} {
		if !hasWarning(r, w) {
			t.Errorf("no warning for %s: %v", w, r.Warnings)
		}
	}
	for _, s := range []string{"gone", "boxed", "fallback"} {
		if strings.Contains(r.Markdown, s) {
			t.Errorf("%s leaked", s)
		}
	}
}

func TestDocxMergedCellsAndNestedLists(t *testing.T) {
	cell := func(extra, txt string) string {
		return `<w:tc><w:tcPr>` + extra + `</w:tcPr><w:p><w:r><w:t>` + txt + `</w:t></w:r></w:p></w:tc>`
	}
	li := func(lvl, id, txt string) string {
		return `<w:p><w:pPr><w:numPr><w:ilvl w:val="` + lvl + `"/><w:numId w:val="` + id + `"/></w:numPr></w:pPr><w:r><w:t>` + txt + `</w:t></w:r></w:p>`
	}
	body := `<w:tbl><w:tr>` + cell(`<w:gridSpan w:val="2"/>`, "wide") + cell("", "c") + `</w:tr><w:tr>` + cell("", "a") + cell("", "b|b") + cell("", "c") + `</w:tr></w:tbl>` +
		li("0", "1", "top") + li("1", "1", "child") + li("0", "1", "next")
	r := mustParse(t, build(body, "", nil))
	if !strings.Contains(r.Markdown, "| wide |  | c |") || !strings.Contains(r.Markdown, `| a | b\|b | c |`) || !hasWarning(r, "merged") {
		t.Errorf("table: %s %v", r.Markdown, r.Warnings)
	}
	if !strings.Contains(r.Markdown, "- top\n  - child\n- next") {
		t.Errorf("list: %q", r.Markdown)
	}
}

func TestAcquireDocxCapsPerWorkspace(t *testing.T) {
	r1, ok1 := AcquireDocx("ws-a")
	r2, ok2 := AcquireDocx("ws-a")
	_, ok3 := AcquireDocx("ws-a")
	other, ok4 := AcquireDocx("ws-b")
	if !ok1 || !ok2 || ok3 || !ok4 {
		t.Fatalf("slots: %v %v %v %v", ok1, ok2, ok3, ok4)
	}
	r1()
	r1() // a second release must not free someone else's slot
	r3, ok := AcquireDocx("ws-a")
	if !ok {
		t.Fatal("a released slot is not reusable")
	}
	if _, ok := AcquireDocx("ws-a"); ok {
		t.Fatal("double release freed two slots")
	}
	r2()
	r3()
	other()
}

func TestFileTitle(t *testing.T) {
	for in, want := range map[string]string{"Plan 2026.docx": "Plan 2026", `C:\x\Notes.docx`: "Notes", "": "Imported document", ".docx": "Imported document"} {
		if got := FileTitle(in); got != want {
			t.Errorf("FileTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParseDocx(f *testing.F) {
	for _, d := range [][]byte{docxSimple(), docxRich(), docxExternalImage(), docxJavascriptLink(), docxMacro(), docxNotAZip(), docxHindi()} {
		f.Add(d)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if r, err := ParseDocx(ctx, data); err == nil && len(r.Markdown) > DocxMaxMarkdown {
			t.Fatal("output over the cap")
		}
	})
}
