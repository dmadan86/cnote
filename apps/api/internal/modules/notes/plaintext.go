package notes

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
)

// PlainText turns stored page Markdown into the text that search snippets and AI context should show
// (docs-parity 7.4). It removes only the markers of the editor's HTML subset and block forms, so older pages are
// unchanged apart from tags: panel marker lines ([!INFO] ...) and quote prefixes, <details> and <summary> tags
// (the title and the body are kept), a line holding only [[toc]], <span data-status>, <mark> and <time> wrappers, HTML table
// tags, and ![alt](url) becomes alt. Code fences keep their lines verbatim (fence lines go) and so do code spans. Other
// Markdown (headings, **bold**, links, pipe tables) is left alone. Lines are trimmed, blanks dropped, joined by "\n".
//
// The browser and the collab gateway need the same function once a feature there reads text; the fixtures in
// apps/collab/test/fixtures/blocks.json hold the expected output for both.
func PlainText(md string) string {
	var out []string
	fence := 0  // length of the open code fence's backtick run, 0 when outside one
	quotes := 0 // how many quote prefixes the open fence sits inside (a fenced block in a panel)
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		// Quote and panel prefixes go first, so a fence inside a panel is still a fence. Inside a fence only as many
		// prefixes as the fence opened under are removed: a code line that starts with ">" keeps it.
		t, depth := strings.TrimSpace(line), 0
		for strings.HasPrefix(t, ">") && (fence == 0 || depth < quotes) {
			t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
			depth++
		}
		if n := backtickRun(t); n >= 3 {
			if fence == 0 && (n == len(t) || !strings.Contains(t[n:], "`")) {
				fence, quotes = n, depth // opening fence, with or without a language
				continue
			}
			if fence > 0 && n >= fence && n == len(t) {
				fence = 0
				continue
			}
		}
		if fence > 0 {
			if t != "" {
				out = append(out, t)
			}
			continue
		}
		if panelMarker.MatchString(t) || strings.EqualFold(t, "[[toc]]") {
			continue
		}
		if m := summaryLine.FindStringSubmatch(t); m != nil {
			t = unescapeHTML(m[1]) // a title is text, not Markdown
		} else {
			t = stripMarkup(t)
		}
		for _, l := range strings.Split(t, "\n") { // an HTML table row spans several lines once its cells end
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
	}
	return strings.Join(out, "\n")
}

var (
	panelMarker = regexp.MustCompile(`(?i)^\[!(info|note|success|warning|error)\]$`)
	summaryLine = regexp.MustCompile(`^<details>\s*<summary>(.*)</summary>$`)
	tagRe       = regexp.MustCompile(`</?(details|summary|span|time|a|table|thead|tbody|tr|th|td|p|br|mark)(\s[^>]*)?/?>`)
	cellEnd     = regexp.MustCompile(`</(td|th|tr|p|summary)>|<br\s*/?>`)
	// ![alt](src "title"): alt and title may hold backslash escapes (\] \[ \\ \"), which the editor writes.
	imageRe  = regexp.MustCompile(`!\[((?:\\.|[^\]\\])*)\]\([^)\s]*(?:\s+"(?:\\.|[^"\\])*")?\)`)
	unescape = regexp.MustCompile(`\\([\\\[\]"])`)
)

func backtickRun(s string) int {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	return n
}

// stripMarkup works outside single-backtick code spans only, so a span that shows <details> or [!NOTE] stays as written.
func stripMarkup(line string) string {
	parts := strings.Split(line, "`")
	for i := 0; i < len(parts); i += 2 {
		p := cellEnd.ReplaceAllString(parts[i], "\n")
		p = tagRe.ReplaceAllString(p, "")
		p = imageRe.ReplaceAllStringFunc(p, func(m string) string {
			return unescape.ReplaceAllString(imageRe.FindStringSubmatch(m)[1], "$1")
		})
		parts[i] = unescapeHTML(p)
	}
	return strings.Join(parts, "`")
}

var htmlEntities = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&")
var numericEntity = regexp.MustCompile(`&amp;|&#(\d{1,7});|&(?:lt|gt|quot);`)

// unescapeHTML decodes in one pass, so "&amp;#42;" stays "&#42;". Numeric entities are how the editor writes markdown
// punctuation inside <mark>.
func unescapeHTML(s string) string {
	return numericEntity.ReplaceAllStringFunc(s, func(m string) string {
		if sub := numericEntity.FindStringSubmatch(m); sub[1] != "" {
			if n, _ := strconv.Atoi(sub[1]); n > 0 && n <= 0x10ffff && !(n >= 0xd800 && n <= 0xdfff) {
				return string(rune(n))
			}
			return m
		}
		return htmlEntities.Replace(m)
	})
}

// Snippets returns a ts_headline excerpt of PlainText(body) for each body, in order. Search queries select the
// page body in the snippet column and pass the column through here, so panels, expands and status wrappers never
// show up in a result.
func Snippets(c *command.Ctx, q string, bodies []string) ([]string, error) {
	if len(bodies) == 0 {
		return nil, nil
	}
	plain := make([]string, len(bodies))
	for i, b := range bodies {
		plain[i] = PlainText(b)
	}
	rows, err := c.Tx.Query(c.Context,
		`select ts_headline('english', t, websearch_to_tsquery('english', $1::text), 'MaxWords=30,MinWords=10')
		 from unnest($2::text[]) with ordinality as u(t, n) order by n`, q, plain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, len(bodies))
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
