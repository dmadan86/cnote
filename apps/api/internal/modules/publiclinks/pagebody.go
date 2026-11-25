package publiclinks

import (
	"html"
	"regexp"
	"strings"
)

// The core body pass (docs-parity-b 4.2): what a public reader may keep of a stored page body.
//
//	file of this page   /api/w/<ws>/files/<id>              -> /api/public/page/<token>/files/<id>
//	any other image     (a file of another page, /w/, ...)  -> its alt text
//	any other link      (mention, page link, /w/, /api/, ...) -> its text
//	[[children]]                                            -> removed
//	http, https, mailto links and external images           -> kept
//
// It rewrites Markdown destinations and the href and src of the HTML subset (merged tables). It fails closed: anything that is
// not on the keep list is replaced by its text, and a last scrub removes every /w/<id> and /api/w/<id> path that is left.

func init() { RegisterCoreBodyPass(corePass{}) }

type corePass struct{}

func (corePass) Name() string { return "core" }

const uuidPat = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

var (
	mdLink = regexp.MustCompile(`(!?)\[((?:\\.|[^\[\]\\])*)\]\(\s*(<[^>]*>|[^\s)]*)(\s+"(?:\\.|[^"\\])*")?\s*\)`)
	mdDef  = regexp.MustCompile(`(?m)^[ ]{0,3}\[[^\]\n]+\]:[ \t]*\S.*$`)
	htmlA  = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	htmlIm = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	attrRe = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?is)\b` + name + `\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	}
	hrefRe     = attrRe("href")
	srcRe      = attrRe("src")
	altRe      = attrRe("alt")
	children   = regexp.MustCompile(`(?i)\[\[\s*children\s*\]\]`)
	internal   = regexp.MustCompile(`(?i)(^|/)(api/)?w/` + uuidPat)
	leftPath   = regexp.MustCompile(`(?i)/(?:api/)?w/` + uuidPat + `[^\s)"'<>\]]*`)
	ownFileFmt = `(?i)^/api/w/%s/files/(` + uuidPat + `)$`
)

func attr(re *regexp.Regexp, tag string) (string, bool) {
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return html.UnescapeString(m[1]), true
	}
	return html.UnescapeString(m[2]), true
}

// dest classifies one link destination: a public file path, a destination to keep, or neither (replace by text).
func (corePass) dest(d string, s BodyScope) (out string, keep bool) {
	d = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(d), "<"), ">"))
	if m := regexp.MustCompile(strings.Replace(ownFileFmt, "%s", regexp.QuoteMeta(s.WorkspaceID), 1)).FindStringSubmatch(d); m != nil {
		if id := strings.ToLower(m[1]); s.OwnFiles[id] {
			return "/api/public/page/" + s.Token + "/files/" + id, true
		}
		return "", false
	}
	if id, ok := strings.CutPrefix(d, "/api/public/page/"+s.Token+"/files/"); ok && s.OwnFiles[strings.ToLower(id)] {
		return d, true // already rewritten: the pass is idempotent
	}
	l := strings.ToLower(d)
	if (strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")) && !internal.MatchString(d) {
		return d, true
	}
	return "", false
}

func (p corePass) Rewrite(md string, s BodyScope) (string, error) {
	for i := 0; i < 4; i++ { // a link inside an image's link text only shows up once the inner one is gone
		before := md
		md = mdLink.ReplaceAllStringFunc(md, func(m string) string {
			g := mdLink.FindStringSubmatch(m)
			bang, text, dest, title := g[1], g[2], g[3], g[4]
			nd, keep := p.dest(dest, s)
			switch {
			case keep && nd != dest:
				return bang + "[" + text + "](" + nd + title + ")"
			case keep:
				return m
			}
			return text // an image keeps its alt text, a link its text
		})
		if md == before {
			break
		}
	}
	md = mdDef.ReplaceAllString(md, "") // reference definitions point somewhere we cannot vouch for
	md = htmlA.ReplaceAllStringFunc(md, func(m string) string {
		g := htmlA.FindStringSubmatch(m)
		href, ok := attr(hrefRe, g[1])
		if nd, keep := p.dest(href, s); ok && keep {
			if nd == href {
				return m
			}
			return hrefRe.ReplaceAllLiteralString(m, `href="`+html.EscapeString(nd)+`"`)
		}
		return g[2]
	})
	md = htmlIm.ReplaceAllStringFunc(md, func(m string) string {
		if src, ok := attr(srcRe, m); ok {
			if nd, keep := p.dest(src, s); keep {
				return srcRe.ReplaceAllLiteralString(m, `src="`+html.EscapeString(nd)+`"`)
			}
		}
		alt, _ := attr(altRe, m)
		return html.EscapeString(alt)
	})
	md = children.ReplaceAllString(md, "")
	return leftPath.ReplaceAllString(md, ""), nil
}
