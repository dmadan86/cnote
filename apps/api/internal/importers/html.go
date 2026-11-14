package importers

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Small lenient HTML -> Markdown converter (port of html.rs) for Confluence storage format and HTML exports.

type attr struct{ k, v string }

type node struct {
	isText bool
	text   string
	tag    string
	attrs  []attr
	kids   []*node
}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "meta": true, "link": true, "input": true}

func isWS(r rune) bool { return unicode.IsSpace(r) }

func trimStartWS(s string) string { return strings.TrimLeftFunc(s, isWS) }

func decodeEntities(s string) string {
	var out strings.Builder
	rest := s
	for {
		i := strings.IndexByte(rest, '&')
		if i < 0 {
			break
		}
		out.WriteString(rest[:i])
		rest = rest[i:]
		end := strings.IndexByte(rest, ';')
		if end < 0 || end > 10 {
			out.WriteByte('&')
			rest = rest[1:]
			continue
		}
		ent := rest[1:end]
		var ch rune
		ok := true
		switch ent {
		case "amp":
			ch = '&'
		case "lt":
			ch = '<'
		case "gt":
			ch = '>'
		case "quot":
			ch = '"'
		case "apos":
			ch = '\''
		case "nbsp":
			ch = ' '
		default:
			ch, ok = numericEntity(ent)
		}
		if ok {
			out.WriteRune(ch)
			rest = rest[end+1:]
		} else {
			out.WriteByte('&')
			rest = rest[1:]
		}
	}
	out.WriteString(rest)
	return out.String()
}

// numericEntity: "#123", "#x1F" (u32::from_str_radix / parse accept one leading '+'), then char::from_u32.
func numericEntity(ent string) (rune, bool) {
	n, ok := strings.CutPrefix(ent, "#")
	if !ok {
		return 0, false
	}
	base := 10
	if h, ok := strings.CutPrefix(n, "x"); ok {
		n, base = h, 16
	} else if h, ok := strings.CutPrefix(n, "X"); ok {
		n, base = h, 16
	}
	n = strings.TrimPrefix(n, "+")
	if n == "" || n[0] == '+' || n[0] == '-' {
		return 0, false
	}
	v, err := strconv.ParseUint(n, base, 32)
	if err != nil || v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
		return 0, false
	}
	return rune(v), true
}

type frame struct {
	tag   string
	attrs []attr
	kids  []*node
}

func parseHTML(html string) []*node {
	stack := []*frame{{}}
	top := func() *frame { return stack[len(stack)-1] }
	closeTop := func() {
		f := top()
		stack = stack[:len(stack)-1]
		top().kids = append(top().kids, &node{tag: f.tag, attrs: f.attrs, kids: f.kids})
	}
	i := 0
	for i < len(html) {
		rest := html[i:]
		switch {
		case !strings.HasPrefix(rest, "<"):
			end := strings.IndexByte(rest, '<')
			if end < 0 {
				end = len(rest)
			}
			top().kids = append(top().kids, &node{isText: true, text: decodeEntities(rest[:end])})
			i += end
		case strings.HasPrefix(rest, "<!--"):
			if e := strings.Index(rest, "-->"); e >= 0 {
				i += e + 3
			} else {
				i += len(rest)
			}
		case strings.HasPrefix(rest, "<![CDATA["):
			body := rest[len("<![CDATA["):]
			end := strings.Index(body, "]]>")
			if end < 0 {
				end = len(body)
			}
			top().kids = append(top().kids, &node{isText: true, text: body[:end]})
			i += 9 + end + 3
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?"):
			if e := strings.IndexByte(rest, '>'); e >= 0 {
				i += e + 1
			} else {
				i += len(rest)
			}
		case strings.HasPrefix(rest, "</"):
			r := rest[2:]
			end := strings.IndexByte(r, '>')
			if end < 0 {
				end = len(r)
			}
			name := rustLower(strings.TrimFunc(r[:end], isWS))
			found := false
			for _, f := range stack[1:] {
				if f.tag == name {
					found = true
				}
			}
			if found {
				for len(stack) > 1 {
					done := top().tag == name
					closeTop()
					if done {
						break
					}
				}
			}
			i += 2 + end + 1
		default:
			end := tagEnd(rest)
			if end < 0 {
				top().kids = append(top().kids, &node{isText: true, text: rest})
				i = len(html)
				continue
			}
			inner := rest[1:end]
			selfClosing := strings.HasSuffix(inner, "/")
			tag, attrs := parseTag(strings.TrimRight(inner, "/"))
			i += end + 1
			if tag == "" {
				top().kids = append(top().kids, &node{isText: true, text: "<"})
				continue
			}
			if tag == "script" || tag == "style" {
				closeTag := "</" + tag
				skip := len(html) - i
				// Rust searches html[i..].to_lowercase() and uses that offset on the original: same for ASCII.
				if p := strings.Index(rustLower(html[i:]), closeTag); p >= 0 && i+p <= len(html) {
					if g := strings.IndexByte(html[i+p:], '>'); g >= 0 {
						skip = p + g + 1
					}
				}
				i += skip
				continue
			}
			if selfClosing || voidTags[tag] {
				top().kids = append(top().kids, &node{tag: tag, attrs: attrs})
			} else {
				if (tag == "li" || tag == "p") && top().tag == tag && len(stack) > 0 {
					closeTop()
				}
				stack = append(stack, &frame{tag: tag, attrs: attrs})
			}
		}
	}
	for len(stack) > 1 {
		closeTop()
	}
	return stack[0].kids
}

// tagEnd is the byte index of the `>` that ends the tag at the start of s, skipping quoted attribute values; -1 if none.
func tagEnd(s string) int {
	var quote rune
	for i, c := range s {
		if i == 0 {
			continue
		}
		switch {
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && c == '>':
			return i
		}
	}
	return -1
}

func parseTag(inner string) (string, []attr) {
	inner = strings.TrimFunc(inner, isWS)
	nameEnd := strings.IndexFunc(inner, isWS)
	if nameEnd < 0 {
		nameEnd = len(inner)
	}
	tag := rustLower(inner[:nameEnd])
	var attrs []attr
	rest := trimStartWS(inner[nameEnd:])
	for rest != "" {
		keyEnd := strings.IndexFunc(rest, func(c rune) bool { return c == '=' || isWS(c) })
		if keyEnd < 0 {
			keyEnd = len(rest)
		}
		key := rustLower(rest[:keyEnd])
		rest = trimStartWS(rest[keyEnd:])
		val := ""
		if r, ok := strings.CutPrefix(rest, "="); ok {
			r = trimStartWS(r)
			if r != "" && (r[0] == '"' || r[0] == '\'') {
				q := r[0]
				body := r[1:]
				e := strings.IndexByte(body, q)
				if e < 0 {
					e = len(body)
				}
				val = decodeEntities(body[:e])
				if e+1 <= len(body) {
					rest = trimStartWS(body[e+1:])
				} else {
					rest = ""
				}
			} else {
				e := strings.IndexFunc(r, isWS)
				if e < 0 {
					e = len(r)
				}
				val = decodeEntities(r[:e])
				rest = trimStartWS(r[e:])
			}
		}
		if key != "" {
			attrs = append(attrs, attr{key, val})
		}
	}
	return tag, attrs
}

func attrOf(attrs []attr, k string) (string, bool) {
	for _, a := range attrs {
		if a.k == k {
			return a.v, true
		}
	}
	return "", false
}

func attrIs(attrs []attr, k, v string) bool { x, ok := attrOf(attrs, k); return ok && x == v }

func rawText(nodes []*node, out *strings.Builder) {
	for _, n := range nodes {
		if n.isText {
			out.WriteString(n.text)
			continue
		}
		if n.tag == "br" {
			out.WriteByte('\n')
		}
		rawText(n.kids, out)
	}
}

type conv struct{ macrosDropped uint32 }

func (c *conv) inline(nodes []*node) string {
	var out bytes.Buffer
	for _, n := range nodes {
		if n.isText {
			cb := out.Bytes()
			prevSpace := len(cb) > 0 && cb[len(cb)-1] == ' '
			for _, ch := range n.text {
				if isWS(ch) {
					if !prevSpace {
						out.WriteByte(' ')
					}
					prevSpace = true
				} else {
					out.WriteRune(ch)
					prevSpace = false
				}
			}
			continue
		}
		inner := c.inline(n.kids)
		wrap := func(m string) string {
			if strings.TrimFunc(inner, isWS) == "" {
				return inner
			}
			return m + strings.TrimFunc(inner, isWS) + m
		}
		switch n.tag {
		case "strong", "b":
			out.WriteString(wrap("**"))
		case "em", "i":
			out.WriteString(wrap("*"))
		case "del", "s":
			out.WriteString(wrap("~~"))
		case "code":
			out.WriteString(wrap("`"))
		case "br":
			out.WriteString("  \n")
		case "a":
			if h, ok := attrOf(n.attrs, "href"); ok && strings.TrimFunc(inner, isWS) != "" {
				fmt.Fprintf(&out, "[%s](%s)", strings.TrimFunc(inner, isWS), h)
			} else {
				out.WriteString(inner)
			}
		case "img":
			alt, _ := attrOf(n.attrs, "alt")
			src, _ := attrOf(n.attrs, "src")
			fmt.Fprintf(&out, "![%s](%s)", alt, src)
		case "ac:structured-macro":
			if !attrIs(n.attrs, "ac:name", "code") {
				c.macrosDropped++
			}
			out.WriteString(c.inline(n.kids))
		case "ac:parameter":
		default:
			out.WriteString(inner)
		}
	}
	return out.String()
}

func (c *conv) block(nodes []*node, out *strings.Builder, indent string) {
	var buf string
	flush := func() {
		t := strings.TrimFunc(buf, isWS)
		if t != "" {
			out.WriteString(indent)
			out.WriteString(t)
			out.WriteString("\n\n")
		}
		buf = ""
	}
	for _, n := range nodes {
		if n.isText {
			buf += c.inline([]*node{n})
			continue
		}
		switch n.tag {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			flush()
			level := int(n.tag[1] - '0')
			fmt.Fprintf(out, "%s %s\n\n", strings.Repeat("#", level), strings.TrimFunc(c.inline(n.kids), isWS))
		case "p":
			flush()
			buf = c.inline(n.kids)
			flush()
		case "ul", "ol":
			flush()
			c.list(n.tag == "ol", n.kids, out, indent)
			out.WriteByte('\n')
		case "pre":
			flush()
			lang := ""
			for _, k := range n.kids {
				if k.isText {
					continue
				}
				if cl, ok := attrOf(k.attrs, "class"); ok {
					found := false
					for _, w := range strings.Fields(cl) {
						if l, ok := strings.CutPrefix(w, "language-"); ok {
							lang, found = l, true
							break
						}
					}
					if found {
						break
					}
				}
			}
			var code strings.Builder
			rawText(n.kids, &code)
			fmt.Fprintf(out, "```%s\n%s\n```\n\n", lang, strings.Trim(code.String(), "\n"))
		case "ac:structured-macro":
			if attrIs(n.attrs, "ac:name", "code") {
				flush()
				lang := ""
				for _, k := range n.kids {
					if !k.isText && k.tag == "ac:parameter" && attrIs(k.attrs, "ac:name", "language") {
						var s strings.Builder
						rawText(k.kids, &s)
						lang = s.String()
						break
					}
				}
				var code strings.Builder
				for _, k := range n.kids {
					if !k.isText && k.tag == "ac:plain-text-body" {
						rawText(k.kids, &code)
					}
				}
				fmt.Fprintf(out, "```%s\n%s\n```\n\n", lang, strings.Trim(code.String(), "\n"))
			} else {
				flush()
				c.macrosDropped++
				c.block(n.kids, out, indent)
			}
		case "blockquote":
			flush()
			var inner strings.Builder
			c.block(n.kids, &inner, "")
			for _, l := range rustLines(strings.TrimRightFunc(inner.String(), isWS)) {
				out.WriteString(strings.ReplaceAll("> "+l+"\n", "> \n", ">\n"))
			}
			out.WriteByte('\n')
		case "table":
			flush()
			c.table(n.kids, out)
		case "hr":
			flush()
			out.WriteString("---\n\n")
		case "head", "title", "meta", "link", "ac:parameter":
		case "div", "section", "article", "main", "body", "html", "header", "footer", "ac:rich-text-body", "ac:layout",
			"ac:layout-section", "ac:layout-cell", "details", "figure":
			flush()
			c.block(n.kids, out, indent)
		default:
			buf += c.inline([]*node{n})
		}
	}
	flush()
}

func (c *conv) list(ordered bool, items []*node, out *strings.Builder, indent string) {
	n := 0
	for _, it := range items {
		if it.isText || it.tag != "li" {
			continue
		}
		n++
		var nested, rest []*node
		for _, k := range it.kids {
			if !k.isText && (k.tag == "ul" || k.tag == "ol") {
				nested = append(nested, k)
			} else {
				rest = append(rest, k)
			}
		}
		var text strings.Builder
		for _, k := range rest {
			text.WriteString(c.inline([]*node{k}))
		}
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", n)
		}
		fmt.Fprintf(out, "%s%s%s\n", indent, marker, strings.TrimFunc(text.String(), isWS))
		for _, k := range nested {
			c.list(k.tag == "ol", k.kids, out, indent+"  ")
		}
	}
}

func (c *conv) table(nodes []*node, out *strings.Builder) {
	var rows [][]*node
	var collect func(ns []*node)
	collect = func(ns []*node) {
		for _, n := range ns {
			if n.isText {
				continue
			}
			switch n.tag {
			case "tr":
				rows = append(rows, n.kids)
			case "thead", "tbody", "tfoot":
				collect(n.kids)
			}
		}
	}
	collect(nodes)
	var cells [][]string
	for _, r := range rows {
		var row []string
		for _, cell := range r {
			if !cell.isText && (cell.tag == "td" || cell.tag == "th") {
				t := strings.TrimFunc(c.inline(cell.kids), isWS)
				row = append(row, strings.ReplaceAll(strings.ReplaceAll(t, "|", "\\|"), "\n", " "))
			}
		}
		if len(row) > 0 {
			cells = append(cells, row)
		}
	}
	width := 0
	for _, r := range cells {
		width = max(width, len(r))
	}
	if len(cells) == 0 {
		return
	}
	for i, r := range cells {
		for len(r) < width {
			r = append(r, "")
		}
		fmt.Fprintf(out, "| %s |\n", strings.Join(r, " | "))
		if i == 0 {
			fmt.Fprintf(out, "|%s\n", strings.Repeat(" --- |", width))
		}
	}
	out.WriteByte('\n')
}

// htmlToMD returns (markdown, number of unsupported macros dropped).
func htmlToMD(html string) (string, uint32) {
	tree := parseHTML(html)
	c := &conv{}
	var out strings.Builder
	c.block(tree, &out, "")
	return strings.TrimFunc(out.String(), isWS), c.macrosDropped
}

// firstText is the text of the first element with this tag (used for page titles).
func firstText(html, tag string) *string {
	var find func(ns []*node) *string
	find = func(ns []*node) *string {
		for _, n := range ns {
			if n.isText {
				continue
			}
			if n.tag == tag {
				var s strings.Builder
				rawText(n.kids, &s)
				t := strings.TrimFunc(s.String(), isWS)
				return &t
			}
			if s := find(n.kids); s != nil {
				return s
			}
		}
		return nil
	}
	s := find(parseHTML(html))
	if s == nil || *s == "" {
		return nil
	}
	return s
}
