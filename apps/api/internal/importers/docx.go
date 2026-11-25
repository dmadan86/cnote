package importers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Word (.docx) import (docs-parity-b 5.7, 13.2.8). The file is read once with the standard library only and turned
// straight into Markdown; nothing of it is stored or served. Every limit lives here, so the command and the raw
// route (which calls the command) cannot differ.
// shortcut: styles come from the style id (Heading1..6, Title), not from styles.xml; upgrade if localised ids appear.
const (
	DocxMaxInput    = 10 << 20
	DocxMaxEntries  = 1000
	DocxMaxTotal    = 50 << 20
	DocxMaxDepth    = 64
	DocxMaxTokens   = 2_000_000
	DocxMaxMarkdown = 2 << 20
	DocxMaxImages   = 20
	DocxMaxPerWS    = 2
	docxDeadline    = 10 * time.Second
	docxMainType    = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

// DocxError is a refusal caused by the file itself (the command reports it as invalid input).
type DocxError struct{ Msg string }

func (e *DocxError) Error() string     { return e.Msg }
func docxErr(f string, a ...any) error { return &DocxError{fmt.Sprintf(f, a...)} }

type DocxImage struct {
	Ref  string // the placeholder in Markdown, e.g. "\x00IMG0\x00"
	Name string
	Data []byte
}

type DocxResult struct {
	Title    string
	Markdown string
	Images   []DocxImage
	Warnings []string
}

var (
	docxMu    sync.Mutex
	docxBusy  = map[string]int{}
	headingRe = regexp.MustCompile(`(?i)^heading\s*([1-6])$`)
)

// AcquireDocx takes one of the workspace's import slots; release must be called when done.
// shortcut: counted per API process, so several replicas allow that many more; move to a DB counter if they scale out.
func AcquireDocx(ws string) (release func(), ok bool) {
	docxMu.Lock()
	defer docxMu.Unlock()
	if docxBusy[ws] >= DocxMaxPerWS {
		return nil, false
	}
	docxBusy[ws]++
	var once sync.Once
	return func() {
		once.Do(func() {
			docxMu.Lock()
			defer docxMu.Unlock()
			if docxBusy[ws]--; docxBusy[ws] <= 0 {
				delete(docxBusy, ws)
			}
		})
	}, true
}

// budget is the cumulative decompression allowance shared by every part read.
type dxBudget struct {
	ctx   context.Context
	total int64
}

// capReader never trusts a declared size: it stops with an error once the part or the package allowance is spent,
// and checks cancellation on every read.
type capReader struct {
	r    io.Reader
	b    *dxBudget
	part int64
}

var errTooBig = docxErr("the document is too large once unpacked")

func (c *capReader) Read(p []byte) (int, error) {
	if err := c.b.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > c.part+1 {
		p = p[:c.part+1]
	}
	n, err := c.r.Read(p)
	c.part -= int64(n)
	c.b.total -= int64(n)
	if c.part < 0 || c.b.total < 0 {
		return 0, errTooBig
	}
	return n, err
}

func (b *dxBudget) open(f *zip.File, limit int64) (io.Reader, io.Closer, error) {
	if int64(f.UncompressedSize64) > limit {
		return nil, nil, errTooBig
	}
	rc, err := f.Open()
	if err != nil {
		return nil, nil, docxErr("the document could not be read")
	}
	return &capReader{r: rc, b: b, part: limit}, rc, nil
}

func (b *dxBudget) readAll(f *zip.File, limit int64) ([]byte, error) {
	r, cl, err := b.open(f, limit)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	data, err := io.ReadAll(r)
	return data, b.fail(err)
}

// fail turns a read error into the right refusal; cancellation stays a context error.
func (b *dxBudget) fail(err error) error {
	var de *DocxError
	switch {
	case err == nil, errors.As(err, &de):
		return err
	case b.ctx.Err() != nil:
		return b.ctx.Err()
	}
	return docxErr("the document could not be read")
}

// walk drives a token loop with the depth, token, directive and cancellation rules.
func (b *dxBudget) walk(r io.Reader, fn func(xml.Token, int) error) error {
	d := xml.NewDecoder(r)
	depth, tokens := 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			var se *xml.SyntaxError
			if errors.As(err, &se) {
				return docxErr("the document XML is not valid")
			}
			return b.fail(err)
		}
		if tokens++; tokens > DocxMaxTokens {
			return docxErr("the document is too complex to import")
		}
		if tokens%1024 == 0 {
			if err := b.ctx.Err(); err != nil {
				return err
			}
		}
		switch t := tok.(type) {
		case xml.Directive:
			return docxErr("the document XML is not valid")
		case xml.StartElement:
			if depth++; depth > DocxMaxDepth {
				return docxErr("the document is nested too deeply")
			}
			if err := fn(t, depth); err != nil {
				return err
			}
			continue
		case xml.EndElement:
			depth--
		}
		if err := fn(tok, depth); err != nil {
			return err
		}
	}
}

type dxRel struct {
	target   string
	external bool
}

func xattr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// inPackage normalises a relationship target against word/ and refuses anything that leaves the package.
func inPackage(target string) (string, bool) {
	if strings.ContainsAny(target, "\\\x00") {
		return "", false
	}
	var p string
	if strings.HasPrefix(target, "/") {
		p = path.Clean(target[1:])
	} else {
		p = path.Clean("word/" + target)
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return "", false
	}
	return p, true
}

// ParseDocx converts one .docx into Markdown. Cancelling ctx stops it; it also gives up after ten seconds.
func ParseDocx(ctx context.Context, data []byte) (*DocxResult, error) {
	if len(data) > DocxMaxInput {
		return nil, docxErr("the file is larger than 10 MB")
	}
	ctx, cancel := context.WithTimeout(ctx, docxDeadline)
	defer cancel()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, docxErr("this is not a Word (.docx) file")
	}
	if len(zr.File) > DocxMaxEntries {
		return nil, docxErr("the document has too many parts")
	}
	b := &dxBudget{ctx: ctx, total: DocxMaxTotal}
	parts := map[string]*zip.File{}
	var warn dxWarnings
	for i, f := range zr.File {
		if i%64 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := strings.ToLower(f.Name)
		if _, dup := parts[f.Name]; dup {
			return nil, docxErr("the document is not valid")
		}
		parts[f.Name] = f
		base := path.Base(name)
		switch {
		case base == "vbaproject.bin" || strings.HasSuffix(name, "/vbadata.xml"):
			return nil, docxErr("documents with macros cannot be imported; save it as .docx first")
		case strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer"):
			warn.add("headers and footers were dropped")
		case name == "word/footnotes.xml" || name == "word/endnotes.xml":
			warn.add("footnotes were dropped")
		case name == "word/comments.xml":
			warn.add("comments were dropped")
		case strings.HasPrefix(name, "word/embeddings/"):
			warn.add("embedded objects were dropped")
		}
	}

	// It must be a Word document, whatever the file is called.
	ct := parts["[Content_Types].xml"]
	doc := parts["word/document.xml"]
	if ct == nil || doc == nil {
		return nil, docxErr("this is not a Word (.docx) file")
	}
	okType, err := mainTypeOK(b, ct)
	if err != nil {
		return nil, err
	}
	if !okType {
		return nil, docxErr("this is not a plain Word document (templates and macro-enabled files are refused)")
	}

	rels := map[string]dxRel{}
	if f := parts["word/_rels/document.xml.rels"]; f != nil {
		r, cl, err := b.open(f, 5<<20)
		if err != nil {
			return nil, err
		}
		err = b.walk(r, func(t xml.Token, _ int) error {
			if se, ok := t.(xml.StartElement); ok && se.Name.Local == "Relationship" {
				rels[xattr(se, "Id")] = dxRel{xattr(se, "Target"), strings.EqualFold(xattr(se, "TargetMode"), "External")}
			}
			return nil
		})
		cl.Close()
		if err != nil {
			return nil, err
		}
	}
	num := dxNumbering{}
	if f := parts["word/numbering.xml"]; f != nil {
		r, cl, err := b.open(f, 5<<20)
		if err != nil {
			return nil, err
		}
		err = num.parse(b, r)
		cl.Close()
		if err != nil {
			return nil, err
		}
	}

	cv := &docxConv{b: b, parts: parts, rels: rels, num: num, warn: &warn, imgIdx: map[string]string{}, counters: map[string]int{}}
	r, cl, err := b.open(doc, 30<<20)
	if err != nil {
		return nil, err
	}
	err = b.walk(r, cv.token)
	cl.Close()
	if err != nil {
		return nil, err
	}
	cv.flushBlock()
	md := strings.TrimSpace(cv.out.String())
	if len(md) > DocxMaxMarkdown {
		return nil, docxErr("the document is too long to import (limit 2 MB of text)")
	}
	return &DocxResult{Title: cv.title, Markdown: md, Images: cv.images, Warnings: warn.list}, nil
}

func mainTypeOK(b *dxBudget, f *zip.File) (bool, error) {
	r, cl, err := b.open(f, 1<<20)
	if err != nil {
		return false, err
	}
	defer cl.Close()
	ok := false
	err = b.walk(r, func(t xml.Token, _ int) error {
		if se, is := t.(xml.StartElement); is && se.Name.Local == "Override" && xattr(se, "PartName") == "/word/document.xml" {
			ok = xattr(se, "ContentType") == docxMainType
		}
		return nil
	})
	return ok, err
}

type dxWarnings struct{ list []string }

func (w *dxWarnings) add(s string) {
	for _, x := range w.list {
		if x == s {
			return
		}
	}
	w.list = append(w.list, s)
}

// numbering maps a list id and level to ordered (true) or bullet.
type dxNumbering struct {
	abs  map[string]map[int]bool
	nums map[string]string
}

func (n *dxNumbering) parse(b *dxBudget, r io.Reader) error {
	n.abs, n.nums = map[string]map[int]bool{}, map[string]string{}
	var curAbs, curNum string
	lvl := -1
	return b.walk(r, func(t xml.Token, _ int) error {
		se, ok := t.(xml.StartElement)
		if !ok {
			return nil
		}
		switch se.Name.Local {
		case "abstractNum":
			curAbs, curNum = xattr(se, "abstractNumId"), ""
			n.abs[curAbs] = map[int]bool{}
		case "num":
			curNum, curAbs = xattr(se, "numId"), ""
		case "lvl":
			lvl, _ = strconv.Atoi(xattr(se, "ilvl"))
		case "numFmt":
			if curAbs != "" && lvl >= 0 {
				v := xattr(se, "val")
				n.abs[curAbs][lvl] = v != "bullet" && v != "none"
			}
		case "abstractNumId":
			if curNum != "" {
				n.nums[curNum] = xattr(se, "val")
			}
		}
		return nil
	})
}

func (n dxNumbering) ordered(id string, lvl int) bool { return n.abs[n.nums[id]][lvl] }

// ---- the document body ----

type dxMarks struct{ b, i, u, s bool }

// seg is one piece of a paragraph: text with marks and an optional link, a hard break, or a picture placeholder.
type dxSeg struct {
	text  string // raw, unescaped
	m     dxMarks
	link  string
	brk   bool
	image string // a DocxImage.Ref
}

type docxConv struct {
	b      *dxBudget
	parts  map[string]*zip.File
	rels   map[string]dxRel
	num    dxNumbering
	warn   *dxWarnings
	out    strings.Builder
	title  string
	images []DocxImage
	imgIdx map[string]string // part name -> Ref

	stack  []string
	skip   int // depth of a subtree being ignored (0 = none)
	segs   []dxSeg
	style  string
	numID  string
	ilvl   int
	run    dxMarks
	link   string
	inRun  bool
	inText bool

	tbl      int // table nesting; only the outermost builds a grid
	rows     [][]string
	row      []string
	cell     []string // paragraphs of the current cell
	span     int
	merged   bool
	counters map[string]int
	inList   bool
	titled   bool
}

func (c *docxConv) parent() string {
	if len(c.stack) < 2 {
		return ""
	}
	return c.stack[len(c.stack)-2]
}

func dxOn(se xml.StartElement) bool {
	v := strings.ToLower(xattr(se, "val"))
	return v != "0" && v != "false" && v != "off" && v != "none"
}

func (c *docxConv) token(t xml.Token, _ int) error {
	switch t := t.(type) {
	case xml.StartElement:
		name := t.Name.Local
		c.stack = append(c.stack, name)
		if c.skip > 0 {
			c.skip++
			return nil
		}
		switch name {
		case "txbxContent":
			c.warn.add("text boxes were dropped")
			c.skip = 1
		case "object", "del", "Fallback", "moveFrom":
			if name == "object" {
				c.warn.add("embedded objects were dropped")
			} else if name != "Fallback" && c.parent() != "rPr" {
				c.warn.add("tracked deletions were dropped")
			}
			c.skip = 1
		case "footnoteReference", "endnoteReference":
			c.warn.add("footnotes were dropped")
		case "commentReference":
			c.warn.add("comments were dropped")
		case "altChunk":
			c.warn.add("embedded content was dropped")
		case "fldChar":
			c.warn.add("fields were replaced by the text Word last showed")
		case "tbl":
			if c.tbl++; c.tbl > 1 {
				c.warn.add("nested tables were flattened")
			}
		case "tr":
			if c.tbl == 1 {
				c.row = nil
			}
		case "tc":
			if c.tbl == 1 {
				c.cell, c.span = nil, 1
			}
		case "gridSpan":
			if n, _ := strconv.Atoi(xattr(t, "val")); c.tbl == 1 && n > 1 && n <= 64 {
				c.span = n
				c.merged = true
			}
		case "vMerge":
			c.merged = true
		case "p":
			c.segs, c.style, c.numID, c.ilvl = nil, "", "", 0
		case "pStyle":
			if c.parent() == "pPr" {
				c.style = xattr(t, "val")
			}
		case "numId":
			if c.parent() == "numPr" {
				c.numID = xattr(t, "val")
			}
		case "ilvl":
			if c.parent() == "numPr" {
				c.ilvl, _ = strconv.Atoi(xattr(t, "val"))
				c.ilvl = max(0, min(c.ilvl, 8))
			}
		case "hyperlink":
			c.link = c.linkTarget(xattr(t, "id"))
		case "r":
			c.run, c.inRun = dxMarks{}, true
		case "b", "i", "u", "strike", "dstrike":
			if c.inRun && c.parent() == "rPr" && dxOn(t) {
				switch name {
				case "b":
					c.run.b = true
				case "i":
					c.run.i = true
				case "u":
					c.run.u = true
				default:
					c.run.s = true
				}
			}
		case "t":
			c.inText = true
		case "tab":
			if c.inRun && c.parent() == "r" {
				c.addText(" ")
			}
		case "br", "cr":
			if xattr(t, "type") != "page" && xattr(t, "type") != "column" {
				c.segs = append(c.segs, dxSeg{brk: true})
			}
		case "blip":
			c.picture(t)
		}
	case xml.CharData:
		if c.skip == 0 && c.inText {
			c.addText(string(t))
		}
	case xml.EndElement:
		name := t.Name.Local
		if len(c.stack) > 0 {
			c.stack = c.stack[:len(c.stack)-1]
		}
		if c.skip > 0 {
			c.skip--
			return nil
		}
		switch name {
		case "t":
			c.inText = false
		case "r":
			c.inRun = false
		case "hyperlink":
			c.link = ""
		case "p":
			c.endParagraph()
		case "tc":
			if c.tbl == 1 {
				cell := strings.Join(c.cell, " ")
				c.row = append(c.row, cell)
				for i := 1; i < c.span; i++ {
					c.row = append(c.row, "")
				}
			}
		case "tr":
			if c.tbl == 1 && len(c.row) > 0 {
				c.rows = append(c.rows, c.row)
			}
		case "tbl":
			if c.tbl--; c.tbl == 0 {
				c.endTable()
			}
		}
	}
	if c.out.Len() > DocxMaxMarkdown*2 {
		return docxErr("the document is too long to import (limit 2 MB of text)")
	}
	return nil
}

func (c *docxConv) addText(s string) {
	s = strings.ReplaceAll(s, "\x00", "")
	if s == "" {
		return
	}
	if n := len(c.segs); n > 0 && !c.segs[n-1].brk && c.segs[n-1].image == "" && c.segs[n-1].m == c.run && c.segs[n-1].link == c.link {
		c.segs[n-1].text += s
		return
	}
	c.segs = append(c.segs, dxSeg{text: s, m: c.run, link: c.link})
}

// linkTarget keeps a link only when it is external and http, https or mailto.
func (c *docxConv) linkTarget(id string) string {
	r, ok := c.rels[id]
	if !ok || !r.external {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(r.target))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") || (u.Scheme != "mailto" && u.Host == "") {
		c.warn.add("links with unsafe addresses were kept as plain text")
		return ""
	}
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E", "\n", "", "\r", "").Replace(u.String())
}

var imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

func (c *docxConv) picture(se xml.StartElement) {
	id := xattr(se, "embed")
	if id == "" {
		if xattr(se, "link") != "" {
			c.warn.add("pictures linked from the web were dropped")
		}
		return
	}
	r, ok := c.rels[id]
	if !ok || r.external {
		c.warn.add("pictures linked from the web were dropped")
		return
	}
	name, ok := inPackage(r.target)
	f := c.parts[name]
	if !ok || f == nil {
		c.warn.add("a picture could not be found and was dropped")
		return
	}
	if ref, seen := c.imgIdx[name]; seen {
		c.segs = append(c.segs, dxSeg{image: ref})
		return
	}
	if !imageExt[strings.ToLower(path.Ext(name))] {
		c.warn.add("pictures other than JPEG, PNG, GIF and WebP were dropped")
		return
	}
	if len(c.images) >= DocxMaxImages {
		c.warn.add("only the first 20 pictures were kept")
		return
	}
	data, err := c.b.readAll(f, 10<<20)
	if err != nil {
		// A picture that is too big or unreadable is skipped; a cancelled import stops at the next read.
		c.warn.add("a picture that could not be read was dropped")
		return
	}
	ref := "\x00IMG" + strconv.Itoa(len(c.images)) + "\x00"
	c.images = append(c.images, DocxImage{Ref: ref, Name: path.Base(name), Data: data})
	c.imgIdx[name] = ref
	c.segs = append(c.segs, dxSeg{image: ref})
}

// ---- Markdown rendering ----

// escapeMD makes text literal: no emphasis, link, tag or table cell can come out of it.
func escapeMD(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '*', '_', '`', '~', '[', ']', '<', '>', '|', '&':
			b.WriteByte('\\')
		}
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

func dxWrap(s string, m dxMarks) string {
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	lead, trail := s[:strings.Index(s, core)], s[strings.Index(s, core)+len(core):]
	if m.s {
		core = "~~" + core + "~~"
	}
	if m.u {
		core = "<u>" + core + "</u>"
	}
	if m.i {
		core = "*" + core + "*"
	}
	if m.b {
		core = "**" + core + "**"
	}
	return lead + core + trail
}

func dxPlain(segs []dxSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
		if s.brk {
			b.WriteByte(' ')
		}
	}
	return strings.TrimSpace(b.String())
}

func dxRender(segs []dxSeg, images bool) string {
	var b strings.Builder
	for _, s := range segs {
		switch {
		case s.brk:
			b.WriteString("  \n")
		case s.image != "":
			if images {
				b.WriteString(s.image)
			}
		case s.link != "":
			core := strings.TrimSpace(s.text)
			if core != "" {
				b.WriteString(dxWrap("["+escapeMD(core)+"]("+s.link+")", s.m))
			}
		default:
			b.WriteString(dxWrap(escapeMD(s.text), s.m))
		}
	}
	return strings.TrimSpace(b.String())
}

func (c *docxConv) endParagraph() {
	segs := c.segs
	c.segs = nil
	if c.tbl > 0 { // a table cell keeps its text on one line; pictures in a cell are dropped
		if t := dxRender(segs, false); t != "" {
			c.cell = append(c.cell, t)
		}
		return
	}
	text := dxRender(segs, true)
	if text == "" {
		return
	}
	level := 0
	if m := headingRe.FindStringSubmatch(c.style); m != nil {
		level, _ = strconv.Atoi(m[1])
	} else if strings.EqualFold(c.style, "Title") {
		level = 1
	}
	if !c.titled && level == 1 && dxPlain(segs) != "" {
		c.titled = true
		c.title = dxPlain(segs)
		return
	}
	switch {
	case level > 0:
		c.flushBlock()
		c.out.WriteString(strings.Repeat("#", level) + " " + text + "\n\n")
	case c.numID != "" && c.numID != "0":
		key := func(l int) string { return c.numID + ":" + strconv.Itoa(l) }
		c.counters[key(c.ilvl)]++
		for l := c.ilvl + 1; l <= 8; l++ {
			delete(c.counters, key(l))
		}
		marker := "- "
		if c.num.ordered(c.numID, c.ilvl) {
			marker = strconv.Itoa(c.counters[key(c.ilvl)]) + ". "
		}
		c.out.WriteString(strings.Repeat("  ", c.ilvl) + marker + strings.ReplaceAll(escapeBlockStart(text), "\n", "\n"+strings.Repeat("  ", c.ilvl+1)) + "\n")
		c.inList = true
	default:
		c.flushBlock()
		c.out.WriteString(escapeBlockStart(text) + "\n\n")
	}
}

var orderedStartRe = regexp.MustCompile(`^\d+[.)]`)

// escapeBlockStart stops a paragraph that begins "- ", "+ ", "# " or "1. " from turning into another block.
func escapeBlockStart(s string) string {
	if s != "" && strings.ContainsRune("-+=#", rune(s[0])) {
		return "\\" + s
	}
	if m := orderedStartRe.FindString(s); m != "" {
		return m[:len(m)-1] + "\\" + m[len(m)-1:] + s[len(m):]
	}
	return s
}

// flushBlock ends a list or any other block with a blank line, and resets list numbering.
func (c *docxConv) flushBlock() {
	if c.inList {
		c.out.WriteString("\n")
		c.inList = false
	}
	c.counters = map[string]int{}
}

func (c *docxConv) endTable() {
	rows := c.rows
	c.rows = nil
	if c.merged {
		c.warn.add("merged table cells were flattened")
		c.merged = false
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	if width == 0 {
		return
	}
	c.flushBlock()
	for i, r := range rows {
		for len(r) < width {
			r = append(r, "")
		}
		c.out.WriteString("| " + strings.Join(r, " | ") + " |\n")
		if i == 0 {
			c.out.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	c.out.WriteString("\n")
}

// FileTitle is a page title from a file name: the base name without its extension.
func FileTitle(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(strings.TrimSuffix(name, path.Ext(name)))
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	if name == "" || name == "." || !utf8.ValidString(name) {
		return "Imported document"
	}
	return name
}
