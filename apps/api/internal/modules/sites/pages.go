package sites

// Pages (design 2, 3 and 17.10): add, remove and rename pages, links between them and the plan's page limit.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
)

func PagesCommands() []command.Command { return []command.Command{} }

const (
	pagesIncluded = 4  // Home plus 4
	pagesPro      = 10 // Home plus 10
	menuLabelMax  = 30
)

func pageLimit(pro bool) int {
	if pro {
		return pagesPro
	}
	return pagesIncluded
}

// pageCount is the number of extra pages (Home is free).
func pageCount(data json.RawMessage) int {
	var d struct {
		Pages []json.RawMessage `json:"pages"`
	}
	_ = json.Unmarshal(data, &d)
	return len(d.Pages)
}

func pageLimitErr(pro bool) error {
	if pro {
		return command.NewError(command.PlanLimit, "Website Pro allows Home plus 10 more pages.")
	}
	return command.NewError(command.PlanLimit, "Your plan allows Home plus 4 more pages. Website Pro allows 10.")
}

// checkPageGrowth refuses a change that takes the page count past the plan. A site that lapsed from Pro keeps its
// extra pages editable: a change that does not add pages is fine (publish still refuses, see checkPublishPages).
func checkPageGrowth(pro bool, before, after int) error {
	if after > pageLimit(pro) && after > before {
		return pageLimitErr(pro)
	}
	return nil
}

func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// uniqueSlug returns base, or base-2, base-3 ... when it is reserved or taken.
func uniqueSlug(d map[string]any, base string) string {
	if base == "" {
		base = "page"
	}
	for n := 1; ; n++ {
		s := base
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			if len(s)+len(suffix) > 40 {
				s = strings.TrimRight(s[:40-len(suffix)], "-")
			}
			s += suffix
		}
		if !reservedSlugs[s] && findPage(d, s) == nil {
			return s
		}
	}
}

// checkSlug validates an address the caller chose.
func checkSlug(d map[string]any, slug string) error {
	switch {
	case !slugPattern.MatchString(slug):
		return fmt.Errorf("the page address may use only a-z, 0-9 and hyphens, up to 40 characters")
	case reservedSlugs[slug]:
		return fmt.Errorf("%q is reserved; choose another address", slug)
	case findPage(d, slug) != nil:
		return fmt.Errorf("a page with the address %q already exists", slug)
	}
	return nil
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n])
	}
	return s
}

// eachBlock visits every block of every page.
func eachBlock(d map[string]any, fn func(b map[string]any)) {
	for _, s := range slugsOf(d) {
		for _, b := range contentOf(d, s) {
			if m := asMap(b); m != nil {
				fn(m)
			}
		}
	}
}

// rewriteLinks points links to /from at /to, keeping a #block anchor; an empty to sends them Home.
func rewriteLinks(d map[string]any, from, to string) {
	prefix := "/" + from
	fix := func(s string) string {
		if s != prefix && !strings.HasPrefix(s, prefix+"#") {
			return s
		}
		if to == "" {
			return "/"
		}
		return "/" + to + s[len(prefix):]
	}
	var walk func(values map[string]any, defs map[string]field)
	walk = func(values map[string]any, defs map[string]field) {
		for k, f := range defs {
			switch f.Kind {
			case "link":
				if s, ok := values[k].(string); ok {
					values[k] = fix(s)
				}
			case "list":
				list, _ := values[k].([]any)
				for _, item := range list {
					if m := asMap(item); m != nil {
						walk(m, f.Props)
					}
				}
			}
		}
	}
	eachBlock(d, func(b map[string]any) {
		if p := asMap(b["props"]); p != nil {
			typ, _ := b["type"].(string)
			walk(p, catalog.Blocks[typ].Props)
		}
	})
}

// addNavLink appends a link to the list prop of Home's Header or Footer when there is room.
func addNavLink(d map[string]any, blockType, label, href string) {
	max := catalog.Blocks[blockType].Props["links"].Max
	for _, b := range contentOf(d, "") {
		m := asMap(b)
		if m["type"] != blockType {
			continue
		}
		p := asMap(m["props"])
		links, _ := p["links"].([]any)
		if len(links) < max {
			p["links"] = append(links, map[string]any{"label": trunc(label, menuLabelMax), "href": href})
		}
		return
	}
}

func setPage(d, in map[string]any) error {
	slug, err := pageArg(d, in["page"])
	if err != nil {
		return err
	}
	target := asMap(asMap(d["root"])["props"])
	if slug != "" {
		target = findPage(d, slug)
	}
	newSlug := ""
	for k, v := range in {
		switch k {
		case "page":
		case "title", "description":
			if err := maskCheck(target, map[string]any{k: v}); err != nil {
				return err
			}
		case "slug":
			if slug == "" {
				return fmt.Errorf("the Home page has no address to change")
			}
			s, _ := v.(string)
			if s != slug {
				if err := checkSlug(d, s); err != nil {
					return err
				}
				newSlug = s
			}
		default:
			return fmt.Errorf("unknown page property")
		}
	}
	for _, k := range []string{"title", "description"} {
		if v, ok := in[k]; ok {
			target[k] = v
		}
	}
	if newSlug != "" {
		rewriteLinks(d, slug, newSlug)
		target["slug"] = newSlug
	}
	return nil
}

func removePage(d, in map[string]any) error {
	for k := range in {
		if k != "page" {
			return fmt.Errorf("unknown property %s", k)
		}
	}
	slug, _ := in["page"].(string)
	if slug == "" || findPage(d, slug) == nil {
		return fmt.Errorf("unknown page")
	}
	var kept []any
	for _, p := range pageMaps(d) {
		if p["slug"] != slug {
			kept = append(kept, p)
		}
	}
	if kept == nil {
		kept = []any{}
	}
	d["pages"] = kept
	rewriteLinks(d, slug, "")
	return nil
}

func addPage(d, in map[string]any, slots map[string]string) error {
	for k := range in {
		if k != "title" && k != "slug" && k != "template" {
			return fmt.Errorf("unknown property %s", k)
		}
	}
	title, _ := in["title"].(string)
	slug, _ := in["slug"].(string)
	tpl, _ := in["template"].(string)
	title = strings.TrimSpace(title)
	if hasMask(title) {
		return fmt.Errorf("%s", maskReply)
	}
	var blocks []any
	if tpl != "" {
		tslug, ttitle, tblocks, err := legalTemplate(tpl, slots)
		if err != nil {
			return err
		}
		if title == "" {
			title = ttitle
		}
		if slug == "" {
			slug = tslug
		}
		blocks = tblocks
	}
	if title == "" {
		return fmt.Errorf("title required")
	}
	if slug != "" || tpl != "" {
		if err := checkSlug(d, slug); err != nil {
			return err
		}
	} else {
		slug = uniqueSlug(d, slugify(title))
	}
	taken := allIDs(d)
	for _, b := range blocks {
		p := asMap(asMap(b)["props"])
		id, err := freshID(asMap(b)["type"].(string), taken)
		if err != nil {
			return err
		}
		p["id"] = id
	}
	if blocks == nil {
		blocks = []any{}
	}
	pages, _ := d["pages"].([]any)
	d["pages"] = append(pages, map[string]any{"slug": slug, "title": title, "description": "", "content": blocks})
	if tpl != "" || slug == "privacy" || slug == "terms" {
		addNavLink(d, "Footer", title, "/"+slug)
	} else {
		addNavLink(d, "Header", title, "/"+slug)
	}
	return nil
}

// ---------- legal page templates ----------

var slotLabels = map[string]string{"name": "business name", "address": "address", "email": "email", "phone": "phone", "url": "website address"}

// templateSlots are the values of a legal page's {{slots}}: the company profile, the site's address and today's date.
func templateSlots(business map[string]string, siteURL string) map[string]string {
	return map[string]string{"name": business["name"], "address": business["address"], "email": business["email"], "phone": business["phone"],
		"url": siteURL, "date": time.Now().In(ist).Format("2 January 2006")}
}

// legalTemplate reads templates/legal_<key>.json and fills it. An empty slot stays as visible text, never truncated.
func legalTemplate(key string, slots map[string]string) (slug, title string, blocks []any, err error) {
	if key != "privacy" && key != "terms" {
		return "", "", nil, fmt.Errorf("unknown page template %q", key)
	}
	raw, err := assets.ReadFile("templates/legal_" + key + ".json")
	if err != nil {
		return "", "", nil, err
	}
	if slots == nil {
		slots = templateSlots(nil, "")
	}
	s := string(raw)
	for _, k := range []string{"name", "address", "email", "phone", "url", "date"} {
		v := strings.TrimSpace(slots[k])
		if v == "" {
			v = "[add your " + slotLabels[k] + "]"
		}
		b, _ := json.Marshal(v)
		s = strings.ReplaceAll(s, "{{"+k+"}}", string(b[1:len(b)-1]))
	}
	var t struct {
		Slug, Title string
		Blocks      []any
	}
	if err = json.Unmarshal([]byte(s), &t); err != nil {
		return "", "", nil, err
	}
	return t.Slug, t.Title, t.Blocks, nil
}
