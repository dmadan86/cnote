package sites

// Abuse scan (design 18.9 to 18.14): the rules over everything a published site shows, the daily sweep, cases,
// signals, visitor reports, platform review and the owner's appeal. There is no automatic takedown: a person decides.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

//go:embed abuse_rules.json
var abuseRulesJSON []byte

// AbuseOn is the switch for the whole abuse scan (sweep, reports, review, hidden links).
func AbuseOn() bool { return os.Getenv("SITES_ABUSE_SCAN") == "1" }

const (
	maxSignalsPerRule = 20
	maxSignals        = 120
	snippetRunes      = 200
	evidenceDays      = 180
	resweepAfter      = 7 * 24 * time.Hour
)

// ---------- rules ----------

type abRule struct {
	ID             string `json:"id"`
	BasePoints     int    `json:"base_points"`
	ElevatedPoints int    `json:"elevated_points"`
	Points         int    `json:"points"`
	PointsEach     int    `json:"points_each"`
	MaxPoints      int    `json:"max_points"`
	Patterns       []struct {
		Lang  string   `json:"lang"`
		Terms []string `json:"terms"`
	} `json:"patterns"`
	Brands []struct {
		Token      string   `json:"token"`
		OwnDomains []string `json:"own_domains"`
	} `json:"brands"`
	ShortenerHosts            []string `json:"shortener_hosts"`
	ExcessDomainsThreshold    int      `json:"excess_domains_threshold"`
	ExcessDomainsPoints       int      `json:"excess_domains_points"`
	PerHighRiskTLDPoints      int      `json:"per_high_risk_tld_points"`
	MaxTLDPoints              int      `json:"max_tld_points"`
	HighRiskTLDs              []string `json:"high_risk_tlds"`
	NameMismatchPoints        int      `json:"name_mismatch_points"`
	NewWorkspaceBonus         int      `json:"new_workspace_bonus"`
	NewWorkspaceThresholdDays int      `json:"new_workspace_threshold_days"`
	WindowDays                int      `json:"window_days"`
	NewWorkspacePoints        int      `json:"new_workspace_points"`
}

type abTerms struct {
	byFirst map[string][][]string // first token -> terms (token lists)
	text    map[string]string     // joined tokens -> the term as written
}

type abEngine struct {
	Threshold int      `json:"threshold"`
	Rules     []abRule `json:"rules"`
	r         map[string]*abRule
	terms     map[string]*abTerms // by rule id
}

var (
	abEngineOnce sync.Once
	abEng        *abEngine
)

func abRules() *abEngine {
	abEngineOnce.Do(func() {
		e := &abEngine{r: map[string]*abRule{}, terms: map[string]*abTerms{}}
		if err := json.Unmarshal(abuseRulesJSON, e); err != nil {
			panic("abuse_rules.json: " + err.Error())
		}
		for i := range e.Rules {
			r := &e.Rules[i]
			e.r[r.ID] = r
			ts := &abTerms{byFirst: map[string][][]string{}, text: map[string]string{}}
			for _, p := range r.Patterns {
				for _, t := range p.Terms {
					toks := abTokenTexts(abNorm(t))
					if len(toks) == 0 {
						continue
					}
					ts.byFirst[toks[0]] = append(ts.byFirst[toks[0]], toks)
					ts.text[strings.Join(toks, " ")] = t
				}
			}
			e.terms[r.ID] = ts
		}
		abEng = e
	})
	return abEng
}

// ---------- normalisation ----------

var abPunyRe = regexp.MustCompile(`xn--[a-z0-9-]+`)

// normalise is NFKC, lower case, invisible format characters (joiners, direction marks) removed, and punycode labels decoded.
func abNorm(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || r == 0 {
			return -1
		}
		return r
	}, s)
	if strings.Contains(s, "xn--") {
		s = abPunyRe.ReplaceAllStringFunc(s, func(m string) string {
			if u, err := idna.Lookup.ToUnicode(m); err == nil {
				return u
			}
			return m
		})
	}
	return s
}

type abTok struct {
	text       string
	start, end int
}

// tokens splits on anything that is not a letter, a combining mark or a digit, so a term only matches whole words.
func abTokens(s string) []abTok {
	var out []abTok
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, abTok{s[start:i], start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, abTok{s[start:], start, len(s)})
	}
	return out
}

func abTokenTexts(s string) []string {
	var out []string
	for _, t := range abTokens(s) {
		out = append(out, t.text)
	}
	return out
}

type abHit struct {
	term       string
	start, end int
}

// match finds the terms of a rule in already-normalised text.
func (ts *abTerms) match(s string) []abHit {
	var out []abHit
	toks := abTokens(s)
	for i := range toks {
		for _, t := range ts.byFirst[toks[i].text] {
			if i+len(t) > len(toks) {
				continue
			}
			ok := true
			for j := 1; j < len(t); j++ {
				if toks[i+j].text != t[j] {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, abHit{ts.text[strings.Join(t, " ")], toks[i].start, toks[i+len(t)-1].end})
			}
		}
	}
	return out
}

// snippet is a window of at most 200 characters of the normalised text around [start, end).
func abSnippet(s string, start, end int) string {
	rs := []rune(s)
	a := utf8.RuneCountInString(s[:start])
	b := utf8.RuneCountInString(s[:end])
	from := a - (snippetRunes-(b-a))/2
	if from < 0 {
		from = 0
	}
	to := from + snippetRunes
	if to > len(rs) {
		to = len(rs)
		if from = to - snippetRunes; from < 0 {
			from = 0
		}
	}
	return strings.TrimSpace(string(rs[from:to]))
}

// ---------- what a site shows ----------

type abText struct{ loc, text string }
type abLink struct{ loc, canon string }

// abUnit is one thing a visitor reads: a page, a post or the catalogue.
type abUnit struct {
	name        string
	texts       []abText
	links       []abLink
	form, embed bool
	shared      []abLink // links of the shared blocks drawn on this page
}

type abPost struct {
	Slug, Title, Description string
	Body                     json.RawMessage
}

var abSkipKeys = map[string]bool{"id": true, "type": true, "variant": true, "align": true, "provider": true, "enquire": true, "image": true, "logo": true,
	"slug": true, "cover_image_id": true, "og_image_id": true, "fp": true, "f": true, "lang": true, "image_id": true, "theme": true, "price_mode": true,
	"pages": true, "content": false}

var abURLRe = regexp.MustCompile(`(?i)https?://[^\s<>"'\]\)]+`)

// SitesDomain subdomains are the site itself, not an outbound link.
func abOwnHost(h string) bool {
	d, _, _ := strings.Cut(SitesDomain(), ":")
	return d != "" && (h == d || strings.HasSuffix(h, "."+d))
}

func (u *abUnit) addString(path, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if t := strings.TrimSpace(s); !(strings.HasPrefix(strings.ToLower(t), "http") && !strings.ContainsAny(t, " \t\n") && abURLRe.MatchString(t)) {
		u.texts = append(u.texts, abText{path, abURLRe.ReplaceAllString(s, " ")}) // a URL is judged as a link, not as words
	}
	seen := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimRight(raw, ".,;:!?")
		if c, err := CanonicalURL(raw); err == nil && !seen[c] {
			seen[c] = true
			u.links = append(u.links, abLink{path, c})
		}
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "//") && !strings.ContainsAny(t, " \t\n") {
		add("https:" + t)
	}
	for _, m := range abURLRe.FindAllString(s, -1) {
		add(m)
	}
}

func (u *abUnit) walk(v any, path string) {
	switch x := v.(type) {
	case string:
		u.addString(path, x)
	case []any:
		for i, e := range x {
			u.walk(e, fmt.Sprintf("%s[%d]", path, i))
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			if !abSkipKeys[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			u.walk(x[k], path+"."+k)
		}
	}
}

// generated adds the outbound links the renderer builds from a block (design 18.9).
func (u *abUnit) generated(loc, typ string, p map[string]any) {
	add := func(raw string) {
		if c, err := CanonicalURL(raw); err == nil {
			u.links = append(u.links, abLink{loc, c})
		}
	}
	s := func(k string) string { v, _ := p[k].(string); return v }
	switch typ {
	case "WhatsAppButton":
		if d := WaDigits(s("phone")); d != "" {
			add("https://wa.me/" + d)
		}
	case "MapHours":
		if strings.TrimSpace(s("address")) != "" {
			add("https://www.google.com/maps/search/?api=1&query=x")
		}
	case "Embed":
		u.embed = true
		if src := EmbedSrc(s("provider"), s("source")); src != "" {
			add(src)
		}
	case "ContactForm":
		u.form = true
	}
}

func (u *abUnit) addBlocks(d map[string]any, blocks []any, sharedOut *[]abLink) {
	for _, b := range blocks {
		m := asMap(b)
		if m == nil {
			continue
		}
		typ, _ := m["type"].(string)
		p := asMap(m["props"])
		id, _ := p["id"].(string)
		loc := u.name + "/" + firstNonEmpty(id, typ)
		before := len(u.links)
		u.walk(p, loc)
		u.generated(loc, typ, p)
		if catalog.Blocks[typ].Shared && sharedOut != nil {
			*sharedOut = append(*sharedOut, u.links[before:]...)
		}
	}
}

// abBuildUnits lays a published site out as units: Home, every page, the catalogue snapshot and each published post.
func abBuildUnits(d map[string]any, posts []abPost, cat json.RawMessage) []*abUnit {
	var out []*abUnit
	var shared []abLink
	home := &abUnit{name: "home"}
	if root := asMap(d["root"]); root != nil {
		home.walk(asMap(root["props"]), "home/root")
		home.walk(root["i18n"], "home/i18n")
	}
	home.addBlocks(d, contentOf(d, ""), &shared)
	out = append(out, home)
	for _, pm := range pageMaps(d) {
		slug, _ := pm["slug"].(string)
		u := &abUnit{name: "page:" + slug}
		u.addString(u.name+"/title", abStr(pm["title"]))
		u.addString(u.name+"/description", abStr(pm["description"]))
		u.walk(pm["i18n"], u.name+"/i18n")
		u.addBlocks(d, contentOf(d, slug), nil)
		out = append(out, u)
	}
	if len(cat) > 0 && string(cat) != "null" {
		var v any
		if json.Unmarshal(cat, &v) == nil {
			u := &abUnit{name: "catalogue"}
			u.walk(v, "catalogue/item")
			out = append(out, u)
		}
	}
	for _, p := range posts {
		u := &abUnit{name: "post:" + p.Slug}
		u.addString(u.name+"/title", p.Title)
		u.addString(u.name+"/description", p.Description)
		var v any
		if json.Unmarshal(p.Body, &v) == nil {
			u.walk(v, u.name+"/body")
		}
		out = append(out, u)
	}
	for _, u := range out { // shared blocks are drawn on every page, not on posts or the catalogue
		if strings.HasPrefix(u.name, "page:") || u.name == "home" {
			u.shared = shared
		}
	}
	return out
}

func abStr(v any) string { s, _ := v.(string); return s }

// ---------- scoring ----------

type AbuseSignal struct{ Rule, Location, Snippet string }

type AbuseSubject struct {
	SiteName, RootTitle, BusinessName string
	AgeDays                           int
}

// AbuseResult of the rules (R1 to R7); Web Risk and visitor reports are added by the scan.
type AbuseResult struct {
	Score   int
	Signals []AbuseSignal
	Links   []string // every distinct canonical outbound link, sorted
	Rules   map[string]int
}

type abBuilder struct {
	r    AbuseResult
	per  map[string]int
	seen map[string]bool
}

func (b *abBuilder) signal(rule, loc, snip string) {
	k := rule + "\x00" + loc + "\x00" + snip
	if b.seen[k] || b.per[rule] >= maxSignalsPerRule || len(b.r.Signals) >= maxSignals {
		return
	}
	b.seen[k] = true
	b.per[rule]++
	b.r.Signals = append(b.r.Signals, AbuseSignal{rule, loc, strings.ReplaceAll(snip, "\x00", "")})
}

func abHostOf(canon string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(canon, "https://"), "http://")
	h, _, _ = strings.Cut(h, "/")
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			return h[:i+1]
		}
	}
	h, _, _ = strings.Cut(h, ":")
	return h
}

func abHostIs(h, dom string) bool { return h == dom || strings.HasSuffix(h, "."+dom) }

var abIPv4Re = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

func abIsIP(h string) bool {
	if strings.HasPrefix(h, "[") {
		return true
	}
	_, err := netip.ParseAddr(h)
	return err == nil || abIPv4Re.MatchString(h)
}

// abScanData applies the rules to a published site. d is the published page data; posts are the published posts.
func abScanData(d map[string]any, posts []abPost, cat json.RawMessage, sub AbuseSubject) AbuseResult {
	e := abRules()
	units := abBuildUnits(d, posts, cat)
	b := &abBuilder{r: AbuseResult{Rules: map[string]int{}, Links: []string{}}, per: map[string]int{}, seen: map[string]bool{}}

	type ext struct{ host, canon, loc string }
	var exts []ext
	linkSet := map[string]bool{}
	for _, u := range units {
		for _, l := range append(append([]abLink{}, u.links...), u.shared...) {
			if h := abHostOf(l.canon); !abOwnHost(h) {
				if !linkSet[l.canon] {
					linkSet[l.canon] = true
					exts = append(exts, ext{h, l.canon, l.loc})
				}
			}
		}
	}
	for l := range linkSet {
		b.r.Links = append(b.r.Links, l)
	}
	sort.Strings(b.r.Links)
	norm2 := func(h string) string { // match on the decoded host
		if u, err := idna.Lookup.ToUnicode(h); err == nil {
			h = u
		}
		return abNorm(h)
	}

	// R1 credential words: elevated when the same page has a form, an embed or an outbound link.
	if r := e.r["R1"]; r != nil {
		pts := 0
		for _, u := range units {
			elevated := u.form || u.embed || abHasExternal(u)
			for _, t := range u.texts {
				n := abNorm(t.text)
				for _, h := range e.terms["R1"].match(n) {
					b.signal("R1", t.loc, abSnippet(n, h.start, h.end))
					p := r.BasePoints
					if elevated {
						p = r.ElevatedPoints
					}
					if p > pts {
						pts = p
					}
				}
			}
		}
		b.r.Rules["R1"] = pts
	}

	// R2 brand impersonation. Elevated: a brand name in the host of a link that is not that brand's own domain.
	// Base: a brand named in the site's title or business name next to a link that is suspicious in itself.
	if r := e.r["R2"]; r != nil {
		pts := 0
		headline := abNorm(sub.SiteName + " " + sub.RootTitle + " " + sub.BusinessName)
		headToks := map[string]bool{}
		for _, t := range abTokenTexts(headline) {
			headToks[t] = true
		}
		for _, br := range r.Brands {
			tk := abNorm(br.Token)
			for _, x := range exts {
				h := norm2(x.host)
				own := false
				for _, od := range br.OwnDomains {
					own = own || abHostIs(h, od)
				}
				if own {
					continue
				}
				inHost := false
				for _, part := range strings.FieldsFunc(h, func(r rune) bool { return r == '.' || r == '-' }) {
					if part == tk || (len(tk) >= 5 && strings.Contains(part, tk)) {
						inHost = true
					}
				}
				switch {
				case inHost:
					b.signal("R2", x.loc, x.canon)
					pts = max(pts, r.ElevatedPoints)
				case headToks[tk] && abSuspicious(e, h):
					b.signal("R2", x.loc, tk+" named in the title, link "+x.canon)
					pts = max(pts, r.BasePoints)
				}
			}
		}
		b.r.Rules["R2"] = pts
	}

	// R3 shorteners.
	if r := e.r["R3"]; r != nil {
		n := 0
		for _, x := range exts {
			if abIsShortener(e, norm2(x.host)) {
				n++
				b.signal("R3", x.loc, x.canon)
			}
		}
		b.r.Rules["R3"] = min(n*r.PointsEach, r.MaxPoints)
	}

	// R4 many outbound domains and high-risk TLDs.
	if r := e.r["R4"]; r != nil {
		hosts, tld := map[string]bool{}, 0
		for _, x := range exts {
			h := norm2(x.host)
			if hosts[h] {
				continue
			}
			hosts[h] = true
			for _, t := range r.HighRiskTLDs {
				if strings.HasSuffix(h, t) {
					tld++
					b.signal("R4", x.loc, x.canon)
					break
				}
			}
		}
		pts := min(tld*r.PerHighRiskTLDPoints, r.MaxTLDPoints)
		if len(hosts) > r.ExcessDomainsThreshold {
			pts += r.ExcessDomainsPoints
			b.signal("R4", "site", fmt.Sprintf("%d outbound domains", len(hosts)))
		}
		b.r.Rules["R4"] = pts
	}

	// R5 investment and crypto wording: points for each block that has it.
	if r := e.r["R5"]; r != nil {
		blocks := map[string]bool{}
		for _, u := range units {
			for _, t := range u.texts {
				n := abNorm(t.text)
				if hs := e.terms["R5"].match(n); len(hs) > 0 {
					blocks[abBlockOf(t.loc)] = true
					b.signal("R5", t.loc, abSnippet(n, hs[0].start, hs[0].end))
				}
			}
		}
		b.r.Rules["R5"] = min(len(blocks)*r.Points, r.MaxPoints)
	}

	// R6 the site shares no word with the business name, on a new workspace.
	if r := e.r["R6"]; r != nil && strings.TrimSpace(sub.BusinessName) != "" && sub.AgeDays < r.NewWorkspaceThresholdDays {
		biz := map[string]bool{}
		for _, t := range abTokenTexts(abNorm(sub.BusinessName)) {
			biz[t] = true
		}
		shares := false
		for _, t := range abTokenTexts(abNorm(sub.SiteName + " " + sub.RootTitle)) {
			shares = shares || (utf8.RuneCountInString(t) >= 3 && biz[t])
		}
		if !shares {
			b.r.Rules["R6"] = r.NameMismatchPoints + r.NewWorkspaceBonus
			b.signal("R6", "site", fmt.Sprintf("site %q, business %q, workspace %d days old", trunc(sub.RootTitle, 60), trunc(sub.BusinessName, 60), sub.AgeDays))
		}
	}

	// R7 punycode or IP hosts.
	if r := e.r["R7"]; r != nil {
		for _, x := range exts {
			if strings.Contains(x.host, "xn--") || abIsIP(x.host) {
				b.r.Rules["R7"] = r.Points
				if sub.AgeDays < e.r["R6"].NewWorkspaceThresholdDays {
					b.r.Rules["R7"] += r.NewWorkspacePoints
				}
				b.signal("R7", x.loc, x.canon)
			}
		}
	}
	for _, p := range b.r.Rules {
		b.r.Score += p
	}
	return b.r
}

func abBlockOf(loc string) string {
	if i := strings.Index(loc, "."); i > 0 {
		return loc[:i]
	}
	return loc
}

func abHasExternal(u *abUnit) bool {
	for _, l := range append(append([]abLink{}, u.links...), u.shared...) {
		if !abOwnHost(abHostOf(l.canon)) {
			return true
		}
	}
	return false
}

func abIsShortener(e *abEngine, h string) bool {
	for _, s := range e.r["R3"].ShortenerHosts {
		if abHostIs(h, s) {
			return true
		}
	}
	return false
}

func abSuspicious(e *abEngine, h string) bool {
	if abIsShortener(e, h) || abIsIP(h) || strings.Contains(h, "xn--") {
		return true
	}
	for _, t := range e.r["R4"].HighRiskTLDs {
		if strings.HasSuffix(h, t) {
			return true
		}
	}
	return false
}

// abReportPts is R8: distinct reporters in the window, capped (design 18.13).
func abReportPts(n int) int {
	r := abRules().r["R8"]
	return min(n*r.PointsEach, r.MaxPoints)
}

// ---------- the scan of one site ----------

type abSnap struct {
	version, name       string
	data                map[string]any
	catalog             json.RawMessage
	posts               []abPost
	postRev             string
	reports             int
	age                 int
	business            string
	expires, checkedAt  map[string]time.Time
	dismissedForVersion bool
}

// abLoadSnap reads everything a scan needs in one transaction that holds the site row, so the version and the post revision
// it records are the ones it read. ok is false when the site is not live or its case is dismissed for this version.
func abLoadSnap(ctx context.Context, tx pgx.Tx, ws, siteID string) (s abSnap, ok bool, err error) {
	var data, cat []byte
	var down bool
	err = tx.QueryRow(ctx, `select s.published_version_id::text, s.name, v.data, s.catalog_published, coalesce(p.taken_down_at is not null, false)
		from sites.sites s join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.published_version_id
		left join sites.site_platform p on p.site_id = s.id where s.workspace_id = $1::uuid and s.id = $2::uuid for share of s`, ws, siteID).
		Scan(&s.version, &s.name, &data, &cat, &down)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && down) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err = json.Unmarshal(data, &s.data); err != nil {
		return s, false, err
	}
	s.catalog = cat
	rows, err := tx.Query(ctx, `select slug, title, description, body from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and status = 'published' order by slug`, ws, siteID)
	if err != nil {
		return s, false, err
	}
	for rows.Next() {
		var p abPost
		if err = rows.Scan(&p.Slug, &p.Title, &p.Description, &p.Body); err != nil {
			rows.Close()
			return s, false, err
		}
		s.posts = append(s.posts, p)
	}
	if err = rows.Err(); err != nil {
		return s, false, err
	}
	if err = tx.QueryRow(ctx, `select encode(sha256(convert_to(coalesce(string_agg(id::text || ':' || rev::text, ',' order by id), ''), 'UTF8')), 'hex') from sites.posts where site_id = $1::uuid and status = 'published'`, siteID).Scan(&s.postRev); err != nil {
		return s, false, err
	}
	var created time.Time
	var legal, trade, wsName *string
	if err = tx.QueryRow(ctx, `select created_at, name, settings->>'legal_name', settings->>'trade_name' from core.workspaces where id = $1::uuid`, ws).Scan(&created, &wsName, &legal, &trade); err != nil {
		return s, false, err
	}
	s.age = int(time.Since(created).Hours() / 24)
	s.business = strings.TrimSpace(deref(legal) + " " + deref(trade))
	if s.business == "" {
		s.business = deref(wsName)
	}
	if err = tx.QueryRow(ctx, `select count(distinct ip_hash) from sites.abuse_reports where site_id = $1::uuid and created_at > now() - interval '30 days'`, siteID).Scan(&s.reports); err != nil {
		return s, false, err
	}
	s.expires, s.checkedAt = map[string]time.Time{}, map[string]time.Time{}
	lr, err := tx.Query(ctx, `select canonical_url, checked_at, expires_at from sites.abuse_links where site_id = $1::uuid`, siteID)
	if err != nil {
		return s, false, err
	}
	for lr.Next() {
		var u string
		var c, e time.Time
		if err = lr.Scan(&u, &c, &e); err != nil {
			lr.Close()
			return s, false, err
		}
		s.checkedAt[u], s.expires[u] = c, e
	}
	if err = lr.Err(); err != nil {
		return s, false, err
	}
	err = tx.QueryRow(ctx, `select exists (select 1 from sites.abuse_cases where site_id = $1::uuid and status in ('dismissed','restored') and dismissed_for_version = $2::uuid)`, siteID, s.version).Scan(&s.dismissedForVersion)
	return s, !s.dismissedForVersion, err
}

// ScanSite scans one live site: the rules, then Web Risk (outside any transaction), then one transaction that checks the
// site still has the version it read and stores the result. Returns false when nothing was stored.
func ScanSite(ctx context.Context, pool *pgxpool.Pool, ws, siteID string) (bool, error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return false, err
	}
	snap, ok, err := abLoadSnap(ctx, tx, ws, siteID)
	_ = tx.Rollback(ctx)
	if err != nil || !ok {
		return false, err
	}
	res := abScanData(snap.data, snap.posts, snap.catalog, AbuseSubject{SiteName: snap.name, RootTitle: abRootTitle(snap.data), BusinessName: snap.business, AgeDays: snap.age})

	var checked []wrResult
	var notChecked []string
	if WebRiskKey() != "" {
		var due []string
		due, notChecked = wrPickLinks(res.Links, snap.expires, snap.checkedAt, time.Now())
		var skipped []string
		checked, skipped = wrCheckLinks(ctx, pool, due)
		notChecked = append(notChecked, skipped...)
	}

	tx, err = db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return false, err
	}
	fin := func(stored bool, err error) (bool, error) {
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return false, err
		}
		return stored, nil
	}
	again, okAgain, err := abLoadSnap(ctx, tx, ws, siteID)
	if err != nil || !okAgain || again.version != snap.version || again.postRev != snap.postRev {
		_ = tx.Rollback(ctx) // newer content: the next sweep scans it; an older result never marks it scanned
		return false, err
	}
	if _, err := tx.Exec(ctx, `select 1 from sites.sites where workspace_id = $1::uuid and id = $2::uuid for update`, ws, siteID); err != nil {
		return fin(false, err)
	}
	return fin(true, abStore(ctx, tx, ws, siteID, snap, res, checked, notChecked))
}

func abRootTitle(d map[string]any) string {
	return abStr(asMap(asMap(d["root"])["props"])["title"])
}

func abStore(ctx context.Context, tx pgx.Tx, ws, siteID string, snap abSnap, res AbuseResult, checked []wrResult, notChecked []string) error {
	for _, r := range checked {
		if _, err := tx.Exec(ctx, `insert into sites.abuse_links (workspace_id, site_id, canonical_url, checked_at, expires_at) values ($1::uuid, $2::uuid, $3, now(), $4)
			on conflict (workspace_id, site_id, canonical_url) do update set checked_at = now(), expires_at = excluded.expires_at`, ws, siteID, r.URL, r.Expires); err != nil {
			return err
		}
		if r.Threat != "" {
			_, err := tx.Exec(ctx, `insert into sites.blocked_links (workspace_id, site_id, canonical_url, threat_type, expires_at, checked_at) values ($1::uuid, $2::uuid, $3, $4, $5, now())
				on conflict (workspace_id, site_id, canonical_url) do update set threat_type = excluded.threat_type, expires_at = excluded.expires_at, checked_at = now()`, ws, siteID, r.URL, r.Threat, r.Expires)
			if err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `delete from sites.blocked_links where site_id = $1::uuid and canonical_url = $2`, siteID, r.URL); err != nil {
			return err
		}
	}
	// links the site no longer has are forgotten
	if _, err := tx.Exec(ctx, `delete from sites.blocked_links where site_id = $1::uuid and canonical_url <> all($2::text[])`, siteID, res.Links); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from sites.abuse_links where site_id = $1::uuid and canonical_url <> all($2::text[])`, siteID, res.Links); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `select canonical_url, threat_type from sites.blocked_links where site_id = $1::uuid order by canonical_url`, siteID)
	if err != nil {
		return err
	}
	signals := append([]AbuseSignal{}, res.Signals...)
	blocked := 0
	for rows.Next() {
		var u, t string
		if err := rows.Scan(&u, &t); err != nil {
			rows.Close()
			return err
		}
		blocked++
		signals = append(signals, AbuseSignal{"web_risk", "link", trunc(t+" "+u, snippetRunes)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	scan := res.Score
	if blocked > 0 {
		scan += abRules().r["R9"].Points
	}
	score := scan + abReportPts(snap.reports)
	if notChecked == nil {
		notChecked = []string{}
	}
	nc, _ := json.Marshal(notChecked)

	var caseID, status string
	err = tx.QueryRow(ctx, `select id::text, status from sites.abuse_cases where site_id = $1::uuid order by created_at desc limit 1 for update`, siteID).Scan(&caseID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		caseID, status, err = "", "", nil
	}
	if err != nil {
		return err
	}
	flagged := score >= abRules().Threshold || blocked > 0
	switch {
	case status == "open":
	case flagged && (status == "" || status == "taken_down"):
		err = tx.QueryRow(ctx, `insert into sites.abuse_cases (workspace_id, site_id, score) values ($1::uuid, $2::uuid, 0) returning id::text`, ws, siteID).Scan(&caseID)
	case flagged: // dismissed or restored for an older version: look again
		_, err = tx.Exec(ctx, `update sites.abuse_cases set status = 'open', dismissed_for_version = null where id = $1::uuid`, caseID)
	default:
		caseID = ""
	}
	if err != nil {
		return err
	}
	if caseID != "" {
		if _, err = tx.Exec(ctx, `update sites.abuse_cases set score = $2, scan_score = $3, published_version_id = $4::uuid, post_rev = $5, unchecked = $6::jsonb, updated_at = now() where id = $1::uuid`,
			caseID, score, scan, snap.version, snap.postRev, nc); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `delete from sites.abuse_signals where case_id = $1::uuid`, caseID); err != nil {
			return err
		}
		for _, s := range signals {
			if _, err = tx.Exec(ctx, `insert into sites.abuse_signals (workspace_id, site_id, case_id, rule, location, snippet) values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6)`,
				ws, siteID, caseID, s.Rule, trunc(s.Location, 200), trunc(s.Snippet, snippetRunes)); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `update sites.abuse_reports set case_id = $1::uuid where site_id = $2::uuid and case_id is null`, caseID, siteID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `insert into sites.abuse_scans (workspace_id, site_id, published_version_id, post_rev, score, scanned_at) values ($1::uuid, $2::uuid, $3::uuid, $4, $5, now())
		on conflict (workspace_id, site_id) do update set published_version_id = excluded.published_version_id, post_rev = excluded.post_rev, score = excluded.score, scanned_at = now()`,
		ws, siteID, snap.version, snap.postRev, score)
	return err
}

// ---------- the daily sweep ----------

// AbuseSweep (worker sweep "sites.abuse", daily) scans the sites that are due: never scanned, a newer published version or
// post revision, a new visitor report, an expired Web Risk answer or the last scan over a week old. It also drops
// evidence older than 180 days. Off unless SITES_ABUSE_SCAN=1.
func AbuseSweep(ctx context.Context, pool *pgxpool.Pool) error {
	if !AbuseOn() {
		return nil
	}
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var firstErr error
	for _, ws := range wss {
		due, err := abDue(ctx, pool, ws)
		for _, id := range due {
			if err == nil {
				_, err = ScanSite(ctx, pool, ws, id)
			}
		}
		if err != nil && firstErr == nil {
			firstErr = err // one workspace failing does not stop the others
		}
	}
	return firstErr
}

func abDue(ctx context.Context, pool *pgxpool.Pool, ws string) ([]string, error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		fmt.Sprintf(`delete from sites.abuse_reports where created_at < now() - interval '%d days'`, evidenceDays),
		fmt.Sprintf(`delete from sites.abuse_signals g using sites.abuse_cases c where g.case_id = c.id and c.status <> 'open' and c.updated_at < now() - interval '%d days'`, evidenceDays),
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `select s.id::text from sites.sites s left join sites.site_platform p on p.site_id = s.id left join sites.abuse_scans a on a.site_id = s.id
		where s.published_version_id is not null and p.taken_down_at is null
		and not exists (select 1 from sites.abuse_cases c where c.site_id = s.id and c.status in ('dismissed','restored') and c.dismissed_for_version = s.published_version_id)
		and (a.site_id is null or a.published_version_id <> s.published_version_id
		  or a.post_rev <> (select encode(sha256(convert_to(coalesce(string_agg(x.id::text || ':' || x.rev::text, ',' order by x.id), ''), 'UTF8')), 'hex') from sites.posts x where x.site_id = s.id and x.status = 'published')
		  or a.scanned_at < now() - $1 * interval '1 second'
		  or exists (select 1 from sites.abuse_links l where l.site_id = s.id and l.expires_at < now())
		  or exists (select 1 from sites.abuse_reports r where r.site_id = s.id and r.created_at > a.scanned_at))
		order by coalesce(a.scanned_at, 'epoch'), s.id limit 200`, resweepAfter.Seconds())
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}

// ---------- hidden links on the public page ----------

// withBlocked adds the links hidden on a live site to what the web server reads (public_get). Off unless SITES_ABUSE_SCAN=1.
func withBlocked(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, p *Public) error {
	p.Blocked = []string{}
	if !AbuseOn() {
		return nil
	}
	if err := q.QueryRow(ctx, "select coalesce(array(select sites.public_blocked($1::uuid)), '{}')", p.SiteID).Scan(&p.Blocked); err != nil {
		return command.DB(err)
	}
	return nil
}

// ---------- commands ----------

func AbuseCommands() []command.Command {
	R, W := command.GroupRead, command.GroupWriteItems
	id := str("case id")
	return []command.Command{
		mk("site.abuse_list", R, command.Member, true, "Platform admins only: the abuse review queue, highest score first. status: open, dismissed, taken_down or restored.",
			obj(nil, props{"status": str("open, dismissed, taken_down or restored")}), abuseList),
		mk("site.abuse_get", R, command.Member, true, "Platform admins only: one case with its signals (text only: snippet is plain text, snippet_escaped is HTML-escaped), visitor reports, links not checked, appeal and history.",
			obj([]string{"id"}, props{"id": id}), abuseGet),
		mk("site.abuse_act", W, command.Member, true, "Platform admins only: dismiss (until the next published version), takedown or restore a site for a case. note is 3 to 500 characters; for a takedown the owner sees it.",
			obj([]string{"id", "action", "note"}, props{"id": id, "action": props{"type": "string", "enum": []string{"dismiss", "takedown", "restore"}}, "note": str("3 to 500 characters")}), abuseAct),
		mk("site.abuse_appeal", W, command.Admin, true, "Owner or admin: appeal the takedown of this website, once per takedown. id is the site id (or the case id).",
			obj([]string{"id", "note"}, props{"id": str("site id"), "note": str("why the site should be restored, 3 to 1000 characters")}), abuseAppeal),
	}
}

func abuseGate(c *command.Ctx) error {
	if err := c.PlatformAdmin(); err != nil {
		return err
	}
	if !AbuseOn() {
		return command.ErrNotFound("abuse review")
	}
	return nil
}

// adminConn is the owner connection the platform functions run over, as for site.platform_set.
func adminConn(c *command.Ctx) (*pgx.Conn, error) {
	u := strings.TrimSpace(os.Getenv("DATABASE_ADMIN_URL"))
	if u == "" {
		return nil, command.ErrInvalid("platform controls are not configured")
	}
	conn, err := pgx.Connect(c.Context, u)
	return conn, command.DB(err)
}

func abuseList(c *command.Ctx, raw json.RawMessage) (any, error) {
	if err := abuseGate(c); err != nil {
		return nil, err
	}
	var in struct{ Status string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	switch in.Status {
	case "", "open", "dismissed", "taken_down", "restored":
	default:
		return nil, command.ErrInvalid("status must be open, dismissed, taken_down or restored")
	}
	conn, err := adminConn(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close(c.Context)
	var b []byte
	if err := conn.QueryRow(c.Context, "select sites.abuse_review_list(nullif($1,''), 100)", in.Status).Scan(&b); err != nil {
		return nil, command.DB(err)
	}
	var list []map[string]any
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, m := range list {
		m["opened_at"] = m["created_at"]
	}
	if list == nil {
		list = []map[string]any{}
	}
	return props{"cases": list}, nil
}

// cleanEvidence removes control and direction characters from stored text before it is shown.
func cleanEvidence(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func abCaseView(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, command.ErrNotFound("case")
	}
	sig := []any{}
	for _, x := range abList(m["signals"]) {
		s := asMap(x)
		sn := cleanEvidence(abStr(s["snippet"]))
		sig = append(sig, props{"rule": s["rule"], "location": cleanEvidence(abStr(s["location"])), "snippet": sn, "snippet_escaped": html.EscapeString(sn)})
	}
	for _, x := range abList(m["reports"]) { // visitor reports are listed after the verified evidence and flagged
		s := asMap(x)
		sn := cleanEvidence(abStr(s["reason"]))
		sig = append(sig, props{"rule": "report", "location": "visitor", "snippet": sn, "snippet_escaped": html.EscapeString(sn), "reported": true})
	}
	hist := []any{}
	for _, x := range abList(m["actions"]) {
		a := asMap(x)
		hist = append(hist, props{"at": a["created_at"], "action": a["action"], "note": cleanEvidence(abStr(a["reason"]))})
	}
	var appeal any
	if l := abList(m["appeals"]); len(l) > 0 {
		a := asMap(l[len(l)-1])
		appeal = props{"note": cleanEvidence(abStr(a["message"])), "at": a["created_at"]}
	}
	nc := m["unchecked"]
	if nc == nil {
		nc = []any{}
	}
	return props{"id": m["id"], "site_id": m["site_id"], "site_name": m["site_name"], "subdomain": m["subdomain"], "workspace_slug": m["workspace_slug"],
		"status": m["status"], "score": m["score"], "scan_score": m["scan_score"], "version_id": m["published_version_id"], "current_version_id": m["current_version_id"],
		"opened_at": m["created_at"], "updated_at": m["updated_at"], "signals": sig, "not_checked": nc, "appeal": appeal, "history": hist,
		"taken_down": m["taken_down"]}, nil
}

func abList(v any) []any { l, _ := v.([]any); return l }

func abReviewCase(c *command.Ctx, id string) (map[string]any, error) {
	if !uuidRe.MatchString(id) {
		return nil, command.ErrNotFound("case")
	}
	conn, err := adminConn(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close(c.Context)
	var b []byte
	if err := conn.QueryRow(c.Context, "select sites.abuse_review_get($1::uuid)", id).Scan(&b); err != nil {
		return nil, command.DB(err)
	}
	return abCaseView(b)
}

func abuseGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	if err := abuseGate(c); err != nil {
		return nil, err
	}
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	return abReviewCase(c, in.ID)
}

func abuseAct(c *command.Ctx, raw json.RawMessage) (any, error) {
	if err := abuseGate(c); err != nil {
		return nil, err
	}
	var in struct{ ID, Action, Note string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	note := strings.TrimSpace(cleanEvidence(in.Note))
	if n := utf8.RuneCountInString(note); n < 3 || n > 500 {
		return nil, command.ErrInvalid("note must be 3 to 500 characters")
	}
	cs, err := abReviewCase(c, in.ID)
	if err != nil {
		return nil, err
	}
	name := abStr(cs["site_name"])
	title, subject, body := "", "", ""
	switch in.Action {
	case "takedown":
		title = "Your website was taken offline: " + trunc(name, 80)
		subject = "Your website was taken offline"
		body = fmt.Sprintf("Your website %q was taken offline after a review.\n\nReason: %s\n\nIf you think this is a mistake, open the site in C Note and use Appeal. We do not promise a response time.\n", name, note)
	case "restore":
		title = "Your website is back online: " + trunc(name, 80)
		subject = "Your website is back online"
		body = fmt.Sprintf("Your website %q was restored after a review.\n\nNote: %s\n", name, note)
	case "dismiss":
	default:
		return nil, command.ErrInvalid("action must be dismiss, takedown or restore")
	}
	conn, err := adminConn(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close(c.Context)
	var out []byte
	err = conn.QueryRow(c.Context, "select sites.abuse_review_act($1::uuid, $2::uuid, $3, $4, $5, $6, $7)", c.Principal.UserID, in.ID, in.Action, note, title, subject, body).Scan(&out)
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &pg) && pg.Code == "22023":
		return nil, command.ErrConflict(pg.Message)
	case errors.As(err, &pg) && pg.Code == "42501":
		return nil, command.ErrForbidden(pg.Message)
	case err != nil:
		return nil, command.DB(err)
	case out == nil || string(out) == "null":
		return nil, command.ErrNotFound("case")
	}
	if _, err := c.Emit("site.abuse_"+in.Action, nil, props{"case_id": in.ID}); err != nil {
		return nil, err
	}
	conn.Close(c.Context)
	return abReviewCase(c, in.ID)
}

// abuseAppeal is the owner's one appeal per takedown action.
func abuseAppeal(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Note string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if !AbuseOn() {
		return nil, command.ErrNotFound("site")
	}
	note := strings.TrimSpace(cleanEvidence(in.Note))
	if n := utf8.RuneCountInString(note); n < 3 || n > 1000 {
		return nil, command.ErrInvalid("write 3 to 1000 characters")
	}
	if !uuidRe.MatchString(in.ID) {
		return nil, command.ErrNotFound("site")
	}
	var siteID, caseID, actionID string
	err := c.Tx.QueryRow(c.Context, `select a.site_id::text, a.case_id::text, a.id::text from sites.abuse_actions a join sites.abuse_cases k on k.id = a.case_id
		where a.action = 'takedown' and k.status = 'taken_down' and (a.site_id = $1::uuid or a.case_id = $1::uuid) order by a.created_at desc limit 1`, in.ID).Scan(&siteID, &caseID, &actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("takedown")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	tag, err := c.Tx.Exec(c.Context, `insert into sites.abuse_appeals (workspace_id, site_id, case_id, takedown_action_id, message) values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5) on conflict (takedown_action_id) do nothing`,
		c.WorkspaceID(), siteID, caseID, actionID, note)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrConflict("You have already appealed this takedown.")
	}
	return props{"ok": true}, nil
}

// abuseExtras adds the owner banner data to site.get: present when links are hidden or the site was taken down for a case.
func abuseExtras(c *command.Ctx, site siteOut, out props) error {
	if !AbuseOn() {
		return nil
	}
	var caseID *string
	var down, appealed bool
	if err := c.Tx.QueryRow(c.Context, `select (select id::text from sites.abuse_cases where site_id = $1::uuid and status in ('open','taken_down') order by created_at desc limit 1),
		coalesce((select status = 'taken_down' from sites.abuse_cases where site_id = $1::uuid order by created_at desc limit 1), false),
		exists (select 1 from sites.abuse_appeals a join sites.abuse_actions x on x.id = a.takedown_action_id where a.site_id = $1::uuid
		  and x.id = (select id from sites.abuse_actions where site_id = $1::uuid and action = 'takedown' order by created_at desc limit 1))`, site.ID).Scan(&caseID, &down, &appealed); err != nil {
		return command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context, `select canonical_url from sites.blocked_links where site_id = $1::uuid order by canonical_url limit 100`, site.ID)
	if err != nil {
		return command.DB(err)
	}
	blocked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return command.DB(err)
	}
	if caseID == nil || (!down && len(blocked) == 0) {
		return nil
	}
	if blocked == nil {
		blocked = []string{}
	}
	out["abuse"] = props{"case_id": *caseID, "appealed": appealed, "blocked_links": blocked}
	return nil
}
