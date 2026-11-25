package notes

import (
	"regexp"
	"strings"

	"github.com/cnote/api/internal/command"
)

// PlainText turns stored page Markdown into the text that search snippets and AI context should show
// (docs-parity 7.4). It removes only the markers of the editor's HTML subset and block forms, so older pages are
// unchanged apart from tags: panel marker lines ([!INFO] ...) and quote prefixes, <details> and <summary> tags
// (the title and the body are kept), a line holding only [[toc]], <span data-status> and <time> wrappers, HTML table
// tags, and ![alt](url) becomes alt. Code fences keep their lines verbatim (fence lines go) and so do code spans. Other
// Markdown (headings, **bold**, links, pipe tables) is left alone. Lines are trimmed, blanks dropped, joined by "\n".
//
// The browser and the collab gateway need the same function once a feature there reads text; the fixtures in
// apps/collab/test/fixtures/blocks.json hold the expected output for both.
func PlainText(md string) string {
	var out []string
	fence := 0 // length of the open code fence's backtick run, 0 when outside one
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if n := backtickRun(t); n >= 3 {
			if fence == 0 && (n == len(t) || !strings.Contains(t[n:], "`")) {
				fence = n // opening fence, with or without a language
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
		for strings.HasPrefix(t, ">") { // quote and panel prefixes, nested
			t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
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
	tagRe       = regexp.MustCompile(`</?(details|summary|span|time|table|thead|tbody|tr|th|td|p|br)(\s[^>]*)?/?>`)
	cellEnd     = regexp.MustCompile(`</(td|th|tr|p|summary)>|<br\s*/?>`)
	imageRe     = regexp.MustCompile(`!\[([^\]]*)\]\([^)\s]*\)`)
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
		p = imageRe.ReplaceAllString(p, "$1")
		parts[i] = unescapeHTML(p)
	}
	return strings.Join(parts, "`")
}

var htmlEntities = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&amp;", "&")

func unescapeHTML(s string) string { return htmlEntities.Replace(s) }

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
