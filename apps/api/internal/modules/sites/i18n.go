package sites

// Languages (design 18, Phase 3b). Translations live inside each page object: page.i18n[lang][blockId][prop], Home in
// root.i18n, the page's own title and description under the key "@meta". An entry is {"text": text, "fp": fingerprint of
// the source text it translates, "r": 0 machine written | 1 a person typed or checked it}. A list prop holds an array of
// {itemKey: entry}, by index. The renderer shows an entry only while its fingerprint matches the source; everything
// else falls back to the main language ("needs update"). Nothing here edits the source text.

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
)

type overlays map[string]map[string]map[string]any

type langInfo struct{ Code, Name, Native string }

// langTable is every code the catalog knows (design 6). pa and or are refused by SupportedLang unless SITES_EXTRA_LANGS lists them.
var langTable = []langInfo{{"en", "English", "English"}, {"hi", "Hindi", "हिन्दी"}, {"ta", "Tamil", "தமிழ்"}, {"te", "Telugu", "తెలుగు"},
	{"kn", "Kannada", "ಕನ್ನಡ"}, {"mr", "Marathi", "मराठी"}, {"bn", "Bengali", "বাংলা"}, {"gu", "Gujarati", "ગુજરાતી"}, {"ml", "Malayalam", "മലയാളം"},
	{"pa", "Punjabi", "ਪੰਜਾਬੀ"}, {"or", "Odia", "ଓଡ଼ିଆ"}}

const (
	maxLangs      = 4 // the main language and up to three more (Pro)
	extraIncluded = 1
	extraPro      = 3
	metaKey       = "@meta"
)

func langName(code string) string {
	for _, l := range langTable {
		if l.Code == code {
			return l.Name
		}
	}
	return code
}

func knownLang(code string) bool {
	for _, l := range langTable {
		if l.Code == code {
			return true
		}
	}
	return false
}

func extraLangLimit(pro bool) int {
	if pro {
		return extraPro
	}
	return extraIncluded
}

// languagesOn is the SITES_LANGUAGES switch: everything in this file that changes or serves a translation is off without it.
func languagesOn() bool { return os.Getenv("SITES_LANGUAGES") == "1" }

func Fingerprint(s string) string { sum := sha256.Sum256([]byte(s)); return fmt.Sprintf("%x", sum[:8]) }

func SupportedLang(code string) bool {
	switch code {
	case "en", "hi", "ta", "te", "kn", "mr", "bn", "gu", "ml":
		return true
	}
	if code != "pa" && code != "or" {
		return false
	}
	for _, v := range strings.Split(os.Getenv("SITES_EXTRA_LANGS"), ",") {
		if strings.TrimSpace(v) == code {
			return true
		}
	}
	return false
}

// ---------- the language list of a document ----------

func langStrList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// readLangs checks and returns root.props langs (main first; absent is ["en"]) and langs_hidden.
func readLangs(rp map[string]any) (langs, hidden []string, err error) {
	langs = []string{"en"}
	if v, ok := rp["langs"]; ok {
		l, ok := langStrList(v)
		if !ok || len(l) < 1 || len(l) > maxLangs {
			return nil, nil, fmt.Errorf("root.langs: between 1 and %d language codes", maxLangs)
		}
		langs = l
	}
	for i, l := range langs {
		if !knownLang(l) || slices.Contains(langs[:i], l) {
			return nil, nil, fmt.Errorf("root.langs: %q is unknown or repeated", l)
		}
	}
	if v, ok := rp["langs_hidden"]; ok {
		h, ok := langStrList(v)
		if !ok {
			return nil, nil, fmt.Errorf("root.langs_hidden: expected a list")
		}
		for i, l := range h {
			if l == langs[0] || !slices.Contains(langs, l) || slices.Contains(h[:i], l) {
				return nil, nil, fmt.Errorf("root.langs_hidden: %q is not an extra language of the site", l)
			}
		}
		hidden = h
	}
	return langs, hidden, nil
}

func validateLangs(rp map[string]any) ([]string, error) {
	langs, _, err := readLangs(rp)
	return langs, err
}

func docLangsOf(data json.RawMessage) (langs, hidden []string) {
	var d struct {
		Root struct{ Props map[string]any }
	}
	if json.Unmarshal(data, &d) != nil {
		return []string{"en"}, nil
	}
	l, h, err := readLangs(d.Root.Props)
	if err != nil {
		return []string{"en"}, nil
	}
	return l, h
}

// ShownLangs are the languages visitors get: the main language first, then the extra languages that are not hidden.
// With SITES_LANGUAGES off a site shows its main language only.
func ShownLangs(data json.RawMessage) []string {
	langs, hidden := docLangsOf(data)
	if !languagesOn() {
		return langs[:1]
	}
	out := []string{}
	for _, l := range langs {
		if !slices.Contains(hidden, l) {
			out = append(out, l)
		}
	}
	return out
}

// PublicLanguages is what the web needs to route /<lang>/...: the main language and the shown ones, with their names.
func PublicLanguages(data json.RawMessage) props {
	langs, _ := docLangsOf(data)
	shown := ShownLangs(data)
	names := []props{}
	for _, l := range shown {
		for _, t := range langTable {
			if t.Code == l {
				names = append(names, props{"code": t.Code, "name": t.Name, "native": t.Native})
			}
		}
	}
	return props{"main": langs[0], "shown": shown, "names": names}
}

// PublicDoc is the published document without anything hidden: translations of hidden languages are dropped, and with
// SITES_LANGUAGES off every translation is. The web overlays whatever is left.
func PublicDoc(data json.RawMessage) json.RawMessage {
	if !strings.Contains(string(data), `"langs`) && !strings.Contains(string(data), `"i18n"`) {
		return data
	}
	var d map[string]any
	if json.Unmarshal(data, &d) != nil {
		return data
	}
	shown := ShownLangs(data)
	keep := func(h map[string]any) {
		ov := asMap(h["i18n"])
		for l := range ov {
			if !slices.Contains(shown, l) {
				delete(ov, l)
			}
		}
		if len(ov) == 0 {
			delete(h, "i18n")
		}
	}
	root := asMap(d["root"])
	keep(root)
	for _, p := range pageMaps(d) {
		keep(p)
	}
	rp := asMap(root["props"])
	delete(rp, "langs_hidden")
	if len(shown) > 1 || shown[0] != "en" {
		rp["langs"] = shown
	} else {
		delete(rp, "langs")
	}
	out, err := json.Marshal(d)
	if err != nil {
		return data
	}
	return out
}

// ---------- validation ----------

var fpRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

func validateEntry(path string, v any, f field) error {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 3 {
		return fmt.Errorf("%s: expected {text, fp, r}", path)
	}
	s, ok := m["text"].(string)
	if !ok || strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s: expected text", path)
	}
	if err := validateField(path, s, field{Kind: f.Kind, Max: f.Max}); err != nil {
		return err
	}
	if fp, _ := m["fp"].(string); !fpRe.MatchString(fp) {
		return fmt.Errorf("%s: invalid fingerprint", path)
	}
	if r, _ := m["r"].(float64); r != 0 && r != 1 {
		return fmt.Errorf("%s: r must be 0 or 1", path)
	}
	return nil
}

// metaDefs are the page title and description a translation may hold.
func metaDefs(title field) map[string]field {
	return map[string]field{"title": {Kind: "text", Max: title.Max, Translatable: true}, "description": {Kind: "longtext", Max: 160, Translatable: true}}
}

// validateOverlays checks one page's (or Home's) overlays: the languages are extra languages of the site, the keys name
// blocks of this page and translatable props, and every value passes the same field check as the source.
func validateOverlays(path string, ov overlays, langs []string, blocks []docBlock, title field) error {
	byID := map[string]docBlock{}
	for _, b := range blocks {
		id, _ := b.Props["id"].(string)
		byID[id] = b
	}
	for lang, keys := range ov {
		if lang == langs[0] || !slices.Contains(langs, lang) {
			return fmt.Errorf("%s.i18n.%s: not an extra language of the site", path, lang)
		}
		for key, pv := range keys {
			p := path + ".i18n." + lang + "." + key
			if key == metaKey {
				defs := metaDefs(title)
				for prop, v := range pv {
					f, ok := defs[prop]
					if !ok {
						return fmt.Errorf("%s.%s: not translatable", p, prop)
					}
					if err := validateEntry(p+"."+prop, v, f); err != nil {
						return err
					}
				}
				continue
			}
			b, ok := byID[key]
			if !ok {
				return fmt.Errorf("%s: no such block on this page", p)
			}
			defs := catalog.Blocks[b.Type].Props
			for prop, v := range pv {
				f, ok := defs[prop]
				if !ok {
					return fmt.Errorf("%s.%s: not translatable", p, prop)
				}
				if f.Kind != "list" {
					if !f.Translatable {
						return fmt.Errorf("%s.%s: not translatable", p, prop)
					}
					if err := validateEntry(p+"."+prop, v, f); err != nil {
						return err
					}
					continue
				}
				arr, ok := v.([]any)
				src, _ := b.Props[prop].([]any)
				if !ok || len(arr) > len(src) {
					return fmt.Errorf("%s.%s: expected a list no longer than the source", p, prop)
				}
				for i, it := range arr {
					im, ok := it.(map[string]any)
					if !ok {
						return fmt.Errorf("%s.%s[%d]: expected object", p, prop, i)
					}
					for k, ev := range im {
						sf, ok := f.Props[k]
						if !ok || !sf.Translatable {
							return fmt.Errorf("%s.%s[%d].%s: not translatable", p, prop, i, k)
						}
						if err := validateEntry(fmt.Sprintf("%s.%s[%d].%s", p, prop, i, k), ev, sf); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

// pruneOverlays drops what can no longer be valid after an edit: languages the site no longer has, blocks and props
// that are gone, list entries past the end of their source. Entries whose source text changed stay (they are "needs update").
func pruneOverlays(d map[string]any) {
	root := asMap(d["root"])
	langs, _, err := readLangs(asMap(root["props"]))
	if err != nil {
		return
	}
	pruneHolder(root, langs, contentOf(d, ""))
	for _, p := range pageMaps(d) {
		c, _ := p["content"].([]any)
		pruneHolder(p, langs, c)
	}
}

func pruneHolder(h map[string]any, langs []string, blocks []any) {
	ov := asMap(h["i18n"])
	if ov == nil {
		delete(h, "i18n")
		return
	}
	byID := map[string]map[string]any{}
	for _, b := range blocks {
		if m := asMap(b); m != nil {
			if id, _ := asMap(m["props"])["id"].(string); id != "" {
				byID[id] = m
			}
		}
	}
	for lang, lv := range ov {
		keys := asMap(lv)
		if keys == nil || lang == langs[0] || !slices.Contains(langs, lang) {
			delete(ov, lang)
			continue
		}
		for key, kv := range keys {
			pm := asMap(kv)
			if key == metaKey {
				for prop := range pm {
					if prop != "title" && prop != "description" {
						delete(pm, prop)
					}
				}
			} else if blk := byID[key]; blk == nil || pm == nil {
				delete(keys, key)
				continue
			} else {
				defs := catalog.Blocks[fmt.Sprint(blk["type"])].Props
				for prop, ev := range pm {
					f, ok := defs[prop]
					switch {
					case !ok:
						delete(pm, prop)
					case f.Kind == "list":
						arr, _ := ev.([]any)
						src, _ := asMap(blk["props"])[prop].([]any)
						if len(arr) > len(src) {
							arr = arr[:len(src)]
						}
						for _, it := range arr {
							im := asMap(it)
							for k := range im {
								if sf, ok := f.Props[k]; !ok || !sf.Translatable {
									delete(im, k)
								}
							}
						}
						for len(arr) > 0 && len(asMap(arr[len(arr)-1])) == 0 {
							arr = arr[:len(arr)-1]
						}
						if len(arr) == 0 {
							delete(pm, prop)
						} else {
							pm[prop] = arr
						}
					case !f.Translatable:
						delete(pm, prop)
					}
				}
			}
			if len(pm) == 0 {
				delete(keys, key)
			}
		}
		if len(keys) == 0 {
			delete(ov, lang)
		}
	}
	if len(ov) == 0 {
		delete(h, "i18n")
	}
}

// ---------- translatable units ----------

// txUnit is one translatable string of a page: its place, its source text and the field that limits it.
type txUnit struct {
	Block, Prop, Sub string // Block is metaKey for the page title and description; Sub is a list item's key
	Idx              int    // list item index, -1 for a plain prop
	Source, Label    string
	BlockType        string
	Def              field
}

func (u txUnit) key() string {
	if u.Idx >= 0 {
		return u.Block + "." + u.Prop + "." + strconv.Itoa(u.Idx) + "." + u.Sub
	}
	return u.Block + "." + u.Prop
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pageUnits lists what can be translated on one page ("" is Home, with the shared blocks), in reading order. Empty source text is skipped.
func pageUnits(d map[string]any, slug string) []txUnit {
	var out []txUnit
	var titleDef field
	var title, desc any
	if slug == "" {
		rp := asMap(asMap(d["root"])["props"])
		title, desc, titleDef = rp["title"], rp["description"], field{Kind: "text", Max: 120}
	} else {
		p := findPage(d, slug)
		title, desc, titleDef = p["title"], p["description"], field{Kind: "text", Max: 80}
	}
	if s, _ := title.(string); s != "" {
		out = append(out, txUnit{Block: metaKey, Prop: "title", Idx: -1, Source: s, Label: "Page title", Def: titleDef})
	}
	if s, _ := desc.(string); s != "" {
		out = append(out, txUnit{Block: metaKey, Prop: "description", Idx: -1, Source: s, Label: "Page description", Def: field{Kind: "longtext", Max: 160}})
	}
	for _, b := range contentOf(d, slug) {
		m := asMap(b)
		typ, _ := m["type"].(string)
		bp := asMap(m["props"])
		id, _ := bp["id"].(string)
		def := catalog.Blocks[typ]
		for _, name := range sortedMapKeys(def.Props) {
			f := def.Props[name]
			if f.Kind == "list" {
				list, _ := bp[name].([]any)
				for i, it := range list {
					im := asMap(it)
					for _, sn := range sortedMapKeys(f.Props) {
						sf := f.Props[sn]
						if s, _ := im[sn].(string); sf.Translatable && s != "" {
							out = append(out, txUnit{Block: id, BlockType: typ, Prop: name, Idx: i, Sub: sn, Source: s, Def: sf, Label: firstNonEmpty(f.Label, name) + " " + strconv.Itoa(i+1) + ": " + firstNonEmpty(sf.Label, sn)})
						}
					}
				}
			} else if s, _ := bp[name].(string); f.Translatable && s != "" {
				out = append(out, txUnit{Block: id, BlockType: typ, Prop: name, Idx: -1, Source: s, Def: f, Label: firstNonEmpty(f.Label, name)})
			}
		}
	}
	return out
}

// pageSlugs are Home ("") and the extra pages, in order.
func pageSlugs(d map[string]any) []string { return slugsOf(d) }

func pageHolder(d map[string]any, slug string) map[string]any {
	if slug == "" {
		return asMap(d["root"])
	}
	return findPage(d, slug)
}

// entryOf is the overlay entry for a txUnit, or nil.
func entryOf(d map[string]any, slug, lang string, u txUnit) map[string]any {
	bm := asMap(asMap(asMap(pageHolder(d, slug)["i18n"])[lang])[u.Block])
	if u.Idx < 0 {
		return asMap(bm[u.Prop])
	}
	arr, _ := bm[u.Prop].([]any)
	if u.Idx >= len(arr) {
		return nil
	}
	return asMap(asMap(arr[u.Idx])[u.Sub])
}

func ensureSubMap(m map[string]any, k string) map[string]any {
	if c := asMap(m[k]); c != nil {
		return c
	}
	c := map[string]any{}
	m[k] = c
	return c
}

// setEntry writes a translation of u.Source.
func setEntry(d map[string]any, slug, lang string, u txUnit, text string, r int) {
	setEntryFP(d, slug, lang, u, text, r, Fingerprint(u.Source))
}

// setEntryFP writes a translation of the source text whose fingerprint is fp (the translate turn saves what the model saw).
func setEntryFP(d map[string]any, slug, lang string, u txUnit, text string, r int, fp string) {
	b := ensureSubMap(ensureSubMap(ensureSubMap(pageHolder(d, slug), "i18n"), lang), u.Block)
	e := map[string]any{"text": text, "fp": fp, "r": r}
	if u.Idx < 0 {
		b[u.Prop] = e
		return
	}
	arr, _ := b[u.Prop].([]any)
	for len(arr) <= u.Idx {
		arr = append(arr, map[string]any{})
	}
	asMap(arr[u.Idx])[u.Sub] = e
	b[u.Prop] = arr
}

func delEntry(d map[string]any, slug, lang string, u txUnit) {
	if entryOf(d, slug, lang, u) == nil {
		return
	}
	bm := asMap(asMap(asMap(pageHolder(d, slug)["i18n"])[lang])[u.Block])
	if u.Idx < 0 {
		delete(bm, u.Prop)
		return
	}
	arr, _ := bm[u.Prop].([]any)
	delete(asMap(arr[u.Idx]), u.Sub)
}

// entryState is "ok" (a translation of the current text), "needs-update" (the source changed since) or "missing".
func entryState(d map[string]any, slug, lang string, u txUnit) (state, text string, checked bool) {
	e := entryOf(d, slug, lang, u)
	if e == nil {
		return "missing", "", false
	}
	text, _ = e["text"].(string)
	var r float64
	switch v := e["r"].(type) { // float64 from JSON, int when set in this process
	case float64:
		r = v
	case int:
		r = float64(v)
	}
	if fp, _ := e["fp"].(string); fp != Fingerprint(u.Source) {
		return "needs-update", text, r == 1
	}
	return "ok", text, r == 1
}

// unitByKey finds a txUnit of a page by its key.
func unitByKey(d map[string]any, slug, key string) (txUnit, bool) {
	for _, u := range pageUnits(d, slug) {
		if u.key() == key {
			return u, true
		}
	}
	return txUnit{}, false
}

// ---------- consent texts ----------

//go:embed consent_texts.json
var consentJSON []byte

// consentSet is one approved sentence in every language.
type consentSet struct {
	ID, Version string
	Texts       map[string]string
}

var consentSets = loadConsents()

func loadConsents() map[string]consentSet {
	out := map[string]consentSet{DefaultConsentID: {ID: DefaultConsentID, Version: consentVersion, Texts: map[string]string{"en": "I agree that {site_name} may use these details to reply to my enquiry."}}}
	var sets []consentSet
	var one consentSet
	if json.Unmarshal(consentJSON, &one) == nil && one.ID != "" {
		sets = append(sets, one)
	} else {
		_ = json.Unmarshal(consentJSON, &sets)
	}
	for _, s := range sets {
		if s.ID != "" && s.Version != "" && s.Texts["en"] != "" {
			out[s.ID] = s
		}
	}
	return out
}

// ConsentForLang is the server's own consent sentence for a consent id and language, with the site name filled in, and its
// version "<version>-<lang>". A language without a text falls back to English.
func ConsentForLang(id, lang, siteName string) (text, version string, ok bool) {
	if id == "" {
		id = DefaultConsentID
	}
	s, found := consentSets[id]
	if !found {
		return "", "", false
	}
	t := s.Texts[lang]
	if t == "" {
		lang, t = "en", s.Texts["en"]
	}
	return strings.ReplaceAll(t, "{site_name}", siteName), s.Version + "-" + lang, true
}

// ---------- commands ----------

func I18nCommands() []command.Command {
	R, W, M := command.GroupRead, command.GroupWriteItems, command.Member
	id, ver := str("site id"), str("the draft version the person is looking at; a stale one is a conflict")
	lang := str("language code of an extra language of the site, e.g. hi")
	langList := props{"type": "array", "items": props{"type": "string"}}
	return append([]command.Command{
		mk("site.languages", R, M, false, "The site's languages: main and extra, hidden or shown, how many entries of each are checked, need an update or are missing, the posts written in each, and the languages the plan and server allow adding.",
			obj([]string{"id"}, props{"id": id}), languagesGet),
		mk("site.set_langs", W, M, false, "Set the site's languages as a new version. langs[0] is the main language; hidden lists extra languages visitors cannot see yet (a newly added language starts hidden when hidden is omitted). Removing a language drops its translations; refused while posts are written in it.",
			obj([]string{"id", "expected_version_id", "langs"}, props{"id": id, "expected_version_id": ver, "langs": langList, "hidden": langList}), setLangs),
		mk("site.translations", R, M, false, "The review sheet for one language: per page, each translatable text with its source, its translation and a status (ok, needs-update, missing) and whether a person checked it. page omitted lists every page; @home lists Home.",
			obj([]string{"id", "lang"}, props{"id": id, "lang": lang, "page": str("a page address, @home, or omit for every page")}), translationsGet),
		mk("site.set_translation", W, M, false, "Write or clear the translations of up to 50 entries of one page as a new version. entries are {key, text}; empty text clears one. A typed entry counts as checked. page omitted is Home.",
			obj([]string{"id", "expected_version_id", "lang", "entries"}, props{"id": id, "expected_version_id": ver, "lang": lang, "page": str("a page address; omit for Home"),
				"entries": props{"type": "array", "items": props{"type": "object", "required": []string{"key", "text"}, "properties": props{"key": props{"type": "string"}, "text": props{"type": "string"}}}}}), setTranslation),
		mk("site.review_translation", W, M, false, "Mark translations as checked (Looks right) as a new version. keys, or a page, or neither for the whole language. Entries whose source changed are not marked.",
			obj([]string{"id", "expected_version_id", "lang"}, props{"id": id, "expected_version_id": ver, "lang": lang, "page": str("a page address, @home, or omit for every page"), "keys": props{"type": "array", "items": props{"type": "string"}}}), reviewTranslation),
		translateCommand(id, ver, lang),
	}, I18nWebCommands()...)
}

func languagesGate() error {
	if !languagesOn() {
		return command.ErrInvalid("Languages are not turned on.")
	}
	return nil
}

func docOfData(data json.RawMessage) (map[string]any, error) {
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, command.ErrInvalid("the page data is unreadable")
	}
	return d, nil
}

func rootProps(d map[string]any) map[string]any { return asMap(asMap(d["root"])["props"]) }

// mutate runs fn on the draft's document under the site lock and saves the result as one "edit" version. fn returns the
// version summary; empty means nothing changed. extra is merged into the answer.
func mutate(c *command.Ctx, id, expected string, extra props, fn func(site siteOut, d map[string]any) (string, error)) (any, error) {
	if err := languagesGate(); err != nil {
		return nil, err
	}
	site, data, err := loadSite(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := checkDraft(c, site, expected); err != nil {
		return nil, err
	}
	d, err := docOfData(data)
	if err != nil {
		return nil, err
	}
	summary, err := fn(site, d)
	if err != nil {
		return nil, err
	}
	out := props{"site": site, "data": data}
	for k, v := range extra {
		out[k] = v
	}
	if summary == "" {
		return out, nil
	}
	pruneOverlays(d)
	next, err := json.Marshal(d)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	if sameJSON(data, next) {
		return out, nil
	}
	if _, err := addVersion(c, site.ID, &site.DraftVersionID, nil, "edit", summary, next); err != nil {
		return nil, err
	}
	site, data, err = loadSite(c, site.ID, false)
	out["site"], out["data"] = site, data
	return out, err
}

// pagesArg is the pages a request covers: every page, Home (@home) or one extra page.
func pagesArg(d map[string]any, page string) ([]string, error) {
	switch {
	case page == "":
		return pageSlugs(d), nil
	case page == "@home":
		return []string{""}, nil
	case findPage(d, page) == nil:
		return nil, command.ErrInvalid("that page is not on the site")
	}
	return []string{page}, nil
}

func extraLang(d map[string]any, lang string) error {
	langs, _, err := readLangs(rootProps(d))
	if err != nil {
		return command.ErrInvalid(err.Error())
	}
	if lang == langs[0] || !slices.Contains(langs, lang) {
		return command.ErrInvalid("that is not an extra language of this site")
	}
	return nil
}

func languagesGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, data, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	d, err := docOfData(data)
	if err != nil {
		return nil, err
	}
	langs, hidden, err := readLangs(rootProps(d))
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	rows, err := c.Tx.Query(c.Context, `select lang, count(*) filter (where status = 'published'), count(*) from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid group by lang`, c.WorkspaceID(), site.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	posts := map[string][2]int{}
	for rows.Next() {
		var l string
		var p [2]int
		if err := rows.Scan(&l, &p[0], &p[1]); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		posts[l] = p
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	list := []props{}
	for i, l := range langs {
		e := props{"code": l, "name": langName(l), "main": i == 0, "hidden": slices.Contains(hidden, l), "published_posts": posts[l][0], "posts": posts[l][1]}
		if i > 0 {
			var ok, stale, missing, unchecked int
			for _, slug := range pageSlugs(d) {
				for _, u := range pageUnits(d, slug) {
					switch st, _, chk := entryState(d, slug, l, u); st {
					case "ok":
						ok++
						if !chk {
							unchecked++
						}
					case "needs-update":
						stale++
					default:
						missing++
					}
				}
			}
			e["entries"] = props{"ok": ok, "needs_update": stale, "missing": missing, "unchecked": unchecked}
		}
		for _, t := range langTable {
			if t.Code == l {
				e["native"] = t.Native
			}
		}
		list = append(list, e)
	}
	avail := []props{}
	for _, t := range langTable {
		if SupportedLang(t.Code) && !slices.Contains(langs, t.Code) {
			avail = append(avail, props{"code": t.Code, "name": t.Name, "native": t.Native})
		}
	}
	return props{"enabled": languagesOn(), "main": langs[0], "langs": list, "available": avail,
		"plan": props{"extra_used": len(langs) - 1, "extra_limit": extraLangLimit(site.Pro)}}, nil
}

func setLangs(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string    `json:"expected_version_id"`
		Langs             []string  `json:"langs"`
		Hidden            *[]string `json:"hidden"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	return mutate(c, in.ID, in.ExpectedVersionID, nil, func(site siteOut, d map[string]any) (string, error) {
		rp := rootProps(d)
		old, oldHidden, err := readLangs(rp)
		if err != nil {
			return "", command.ErrInvalid(err.Error())
		}
		if len(in.Langs) < 1 || len(in.Langs) > maxLangs {
			return "", command.ErrInvalid(fmt.Sprintf("choose between 1 and %d languages", maxLangs))
		}
		for i, l := range in.Langs {
			if !knownLang(l) || slices.Contains(in.Langs[:i], l) {
				return "", command.ErrInvalid("unknown or repeated language")
			}
			if !slices.Contains(old, l) && !SupportedLang(l) {
				return "", command.ErrInvalid(langName(l) + " is not available yet.")
			}
		}
		if len(in.Langs) > len(old) && len(in.Langs)-1 > extraLangLimit(site.Pro) {
			return "", command.NewError(command.PlanLimit, fmt.Sprintf("Your plan includes %d extra language%s.", extraLangLimit(site.Pro), map[bool]string{true: "", false: "s"}[extraLangLimit(site.Pro) == 1]))
		}
		if in.Langs[0] != old[0] && len(old) > 1 {
			return "", command.ErrInvalid("The main language cannot change while the site has other languages. Remove them first.")
		}
		var removed, added []string
		for _, l := range old {
			if !slices.Contains(in.Langs, l) {
				removed = append(removed, l)
			}
		}
		for _, l := range in.Langs {
			if !slices.Contains(old, l) {
				added = append(added, l)
			}
		}
		if len(removed) > 0 {
			rows, err := c.Tx.Query(c.Context, "select lang, status, count(*) from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and lang = any($3) group by lang, status order by lang",
				c.WorkspaceID(), site.ID, removed)
			if err != nil {
				return "", command.DB(err)
			}
			defer rows.Close()
			for rows.Next() {
				var l, st string
				var n int
				if err := rows.Scan(&l, &st, &n); err != nil {
					return "", command.DB(err)
				}
				if st == "published" {
					return "", command.ErrConflict(fmt.Sprintf("%d published post%s written in %s. Move %s back to drafts or delete %s first.", n, plural(n, " is", "s are"), langName(l), plural(n, "it", "them"), plural(n, "it", "them")))
				}
				return "", command.ErrConflict(fmt.Sprintf("%d post%s written in %s. Delete %s or write %s in another language first.", n, plural(n, " is", "s are"), langName(l), plural(n, "it", "them"), plural(n, "it", "them")))
			}
			if err := rows.Err(); err != nil {
				return "", command.DB(err)
			}
		}
		hidden := []string{}
		if in.Hidden != nil {
			hidden = *in.Hidden
			for i, h := range hidden {
				if h == in.Langs[0] || !slices.Contains(in.Langs, h) || slices.Contains(hidden[:i], h) {
					return "", command.ErrInvalid("hidden must list extra languages of the site")
				}
			}
		} else {
			for _, l := range in.Langs[1:] {
				if slices.Contains(oldHidden, l) || slices.Contains(added, l) {
					hidden = append(hidden, l)
				}
			}
		}
		if len(in.Langs) == 1 && in.Langs[0] == "en" {
			delete(rp, "langs")
			delete(rp, "langs_hidden")
		} else {
			rp["langs"] = in.Langs
			if len(hidden) > 0 {
				rp["langs_hidden"] = hidden
			} else {
				delete(rp, "langs_hidden")
			}
		}
		names := func(l []string) string {
			n := make([]string, len(l))
			for i, x := range l {
				n[i] = langName(x)
			}
			return strings.Join(n, ", ")
		}
		switch {
		case len(added) > 0 && len(removed) > 0:
			return "Changed languages", nil
		case len(added) > 0:
			return "Added " + names(added), nil
		case len(removed) > 0:
			return "Removed " + names(removed), nil
		}
		return "Changed languages", nil
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func statusOut(d map[string]any, slug, lang string, u txUnit) props {
	st, text, checked := entryState(d, slug, lang, u)
	e := props{"key": u.key(), "label": u.Label, "source": u.Source, "translation": nil, "status": st, "checked": checked && st == "ok", "max": u.Def.Max, "multiline": u.Def.Kind == "longtext"}
	if text != "" {
		e["translation"] = text
	}
	if u.BlockType != "" {
		e["block"], e["block_id"] = u.BlockType, u.Block
	}
	return e
}

func translationsGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Lang, Page string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	_, data, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	d, err := docOfData(data)
	if err != nil {
		return nil, err
	}
	if err := extraLang(d, in.Lang); err != nil {
		return nil, err
	}
	slugs, err := pagesArg(d, in.Page)
	if err != nil {
		return nil, err
	}
	_, hidden, _ := readLangs(rootProps(d))
	pages := []props{}
	for _, slug := range slugs {
		title := fmt.Sprint(rootProps(d)["title"])
		if slug != "" {
			title = fmt.Sprint(findPage(d, slug)["title"])
		}
		entries := []props{}
		for _, u := range pageUnits(d, slug) {
			entries = append(entries, statusOut(d, slug, in.Lang, u))
		}
		pages = append(pages, props{"page": slug, "title": title, "entries": entries})
	}
	return props{"lang": in.Lang, "name": langName(in.Lang), "hidden": slices.Contains(hidden, in.Lang), "pages": pages}, nil
}

func setTranslation(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		Lang, Page        string
		Entries           []struct{ Key, Text string }
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Entries) < 1 || len(in.Entries) > 50 {
		return nil, command.ErrInvalid("send between 1 and 50 entries")
	}
	return mutate(c, in.ID, in.ExpectedVersionID, nil, func(site siteOut, d map[string]any) (string, error) {
		if err := extraLang(d, in.Lang); err != nil {
			return "", err
		}
		if in.Page != "" && findPage(d, in.Page) == nil {
			return "", command.ErrInvalid("that page is not on the site")
		}
		for _, e := range in.Entries {
			u, ok := unitByKey(d, in.Page, e.Key)
			if !ok {
				return "", command.ErrInvalid("unknown entry " + e.Key)
			}
			text := strings.TrimSpace(e.Text)
			if text == "" {
				delEntry(d, in.Page, in.Lang, u)
				continue
			}
			if u.Def.Kind == "text" && strings.ContainsAny(text, "\r\n") {
				return "", command.ErrInvalid(u.Label + " must be one line")
			}
			if err := validateField(u.key(), text, field{Kind: u.Def.Kind, Max: u.Def.Max}); err != nil {
				return "", command.ErrInvalid(err.Error())
			}
			setEntry(d, in.Page, in.Lang, u, text, 1)
		}
		return "Edited the " + langName(in.Lang) + " translation", nil
	})
}

func reviewTranslation(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		Lang, Page        string
		Keys              []string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Keys) > 500 {
		return nil, command.ErrInvalid("too many keys")
	}
	n := 0
	extra := props{}
	out, err := mutate(c, in.ID, in.ExpectedVersionID, extra, func(site siteOut, d map[string]any) (string, error) {
		if err := extraLang(d, in.Lang); err != nil {
			return "", err
		}
		slugs, err := pagesArg(d, in.Page)
		if err != nil {
			return "", err
		}
		want := map[string]bool{}
		for _, k := range in.Keys {
			want[k] = true
		}
		for _, slug := range slugs {
			for _, u := range pageUnits(d, slug) {
				if len(want) > 0 && !want[u.key()] {
					continue
				}
				if st, _, chk := entryState(d, slug, in.Lang, u); st == "ok" && !chk {
					entryOf(d, slug, in.Lang, u)["r"] = 1
					n++
				}
			}
		}
		extra["checked"] = n
		if n == 0 {
			return "", nil
		}
		return "Checked the " + langName(in.Lang) + " translation", nil
	})
	return out, err
}

// checkPostLang settles a post's language: one of the site's languages, the main one for a new post, unchanged for an
// existing post unless it is a draft. cur is the stored post (zero for a new one).
func checkPostLang(c *command.Ctx, site siteOut, cur postOut, want string) (string, error) {
	data, err := versionData(c, site.ID, site.DraftVersionID)
	if err != nil {
		return "", err
	}
	langs, _ := docLangsOf(data)
	switch {
	case want == "" && cur.Lang != "":
		want = cur.Lang
	case want == "":
		want = langs[0]
	}
	if !slices.Contains(langs, want) {
		return "", command.ErrInvalid("that is not one of the site's languages")
	}
	if cur.Status == "published" && want != cur.Lang {
		return "", command.ErrInvalid("A published post keeps its language. Move it back to drafts first.")
	}
	return want, nil
}

// ---------- the web's command names (apps/web/src/lib/sites.ts, Phase 3b) ----------
// Thin wrappers over the commands above that take the current draft, so the builder need not send expected_version_id.

func I18nWebCommands() []command.Command {
	R, W, M := command.GroupRead, command.GroupWriteItems, command.Member
	id, lang := str("site id"), str("language code")
	return []command.Command{
		mk("site.lang_list", R, M, false, "Per extra language: hidden, checked (nothing missing, stale or unchecked) and counts {ok, needs_update, missing, unchecked}.", obj([]string{"id"}, props{"id": id}), langList),
		mk("site.lang_add", W, M, false, "Add an extra language (hidden until checked).", obj([]string{"id", "lang"}, props{"id": id, "lang": lang}), langChange(func(old []string, l string, _ bool) []string { return append(append([]string{}, old...), l) })),
		mk("site.lang_remove", W, M, false, "Remove an extra language and its translations.", obj([]string{"id", "lang"}, props{"id": id, "lang": lang}), langChange(func(old []string, l string, _ bool) []string {
			out := []string{}
			for _, x := range old {
				if x != l {
					out = append(out, x)
				}
			}
			return out
		})),
		mk("site.lang_set_hidden", W, M, false, "Hide a language from visitors until it is checked, or show it (it goes live at the next publish).",
			obj([]string{"id", "lang", "hidden"}, props{"id": id, "lang": lang, "hidden": props{"type": "boolean"}}), langSetHidden),
		mk("site.lang_review", R, M, false, "Every translatable entry of a language, missing ones included: {lang, pages:[{slug, entries:[{block_id, prop, index?, source, text, status, stale, checked, label}]}]}. A list text is prop \"items.name\" with its item index; Home is slug \"\".",
			obj([]string{"id", "lang"}, props{"id": id, "lang": lang}), langReview),
		mk("site.lang_entry_set", W, M, false, "Write one entry (empty text clears it); it counts as checked.",
			obj([]string{"id", "expected_version_id", "lang", "block_id", "prop", "text"}, props{"id": id, "expected_version_id": str("draft version"), "lang": lang, "page": str("page address, empty for Home"),
				"block_id": props{"type": "string"}, "prop": props{"type": "string"}, "index": props{"type": "integer"}, "text": props{"type": "string"}}), langEntrySet),
		mk("site.lang_mark_checked", W, M, false, "Mark the fresh entries of a language (or one page; @home for Home) as checked.",
			obj([]string{"id", "lang"}, props{"id": id, "lang": lang, "page": props{"type": "string"}}), langMarkChecked),
	}
}

func draftOf(c *command.Ctx, id string) (string, error) {
	site, _, err := loadSite(c, id, false)
	return site.DraftVersionID, err
}

func langList(c *command.Ctx, raw json.RawMessage) (any, error) {
	out, err := languagesGet(c, raw)
	if err != nil {
		return nil, err
	}
	list := []props{}
	for _, l := range out.(props)["langs"].([]props) {
		e, ok := l["entries"].(props)
		if !ok {
			continue
		}
		ok0, stale, missing, unchecked := e["ok"].(int), e["needs_update"].(int), e["missing"].(int), e["unchecked"].(int)
		list = append(list, props{"code": l["code"], "hidden": l["hidden"], "checked": stale+missing+unchecked == 0, "counts": e,
			"translated": ok0, "stale": stale, "missing": missing, "unchecked": unchecked})
	}
	return props{"langs": list}, nil
}

// langChange adds or removes one language through set_langs, on the current draft.
func langChange(f func(old []string, l string, _ bool) []string) command.Handler {
	return func(c *command.Ctx, raw json.RawMessage) (any, error) {
		var in struct{ ID, Lang string }
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		_, data, err := loadSite(c, in.ID, false)
		if err != nil {
			return nil, err
		}
		old, _ := docLangsOf(data)
		draft, err := draftOf(c, in.ID)
		if err != nil {
			return nil, err
		}
		next := f(old, in.Lang, false)
		if len(next) > len(old) && slices.Contains(old, in.Lang) {
			return nil, command.ErrInvalid("that language is already on the site")
		}
		b, _ := json.Marshal(props{"id": in.ID, "expected_version_id": draft, "langs": next})
		if _, err := setLangs(c, b); err != nil {
			return nil, err
		}
		return langList(c, raw)
	}
}

func langSetHidden(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID, Lang string
		Hidden   bool
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	_, data, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	langs, hidden := docLangsOf(data)
	if in.Lang == langs[0] || !slices.Contains(langs, in.Lang) {
		return nil, command.ErrInvalid("that is not an extra language of this site")
	}
	next := []string{}
	for _, h := range hidden {
		if h != in.Lang {
			next = append(next, h)
		}
	}
	if in.Hidden {
		next = append(next, in.Lang)
	}
	draft, err := draftOf(c, in.ID)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(props{"id": in.ID, "expected_version_id": draft, "langs": langs, "hidden": next})
	if _, err := setLangs(c, b); err != nil {
		return nil, err
	}
	return langList(c, raw)
}

func webEntry(d map[string]any, slug, lang string, u txUnit) props {
	st, text, chk := entryState(d, slug, lang, u)
	prop := u.Prop
	e := props{"block_id": u.Block, "prop": prop, "text": text, "source": u.Source, "status": st, "stale": st == "needs-update", "checked": chk && st == "ok", "label": u.Label, "key": u.key()}
	if u.Idx >= 0 {
		e["prop"], e["index"] = u.Prop+"."+u.Sub, u.Idx
	}
	return e
}

func langReview(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Lang string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	_, data, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	d, err := docOfData(data)
	if err != nil {
		return nil, err
	}
	if err := extraLang(d, in.Lang); err != nil {
		return nil, err
	}
	pages := []props{}
	for _, slug := range pageSlugs(d) {
		es := []props{}
		for _, u := range pageUnits(d, slug) {
			es = append(es, webEntry(d, slug, in.Lang, u))
		}
		pages = append(pages, props{"slug": slug, "entries": es})
	}
	return props{"lang": in.Lang, "pages": pages}, nil
}

func langEntrySet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		Lang, Page        string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	var r struct {
		BlockID string `json:"block_id"`
		Prop    string `json:"prop"`
		Index   *int   `json:"index"`
		Text    string `json:"text"`
	}
	_ = json.Unmarshal(raw, &r)
	key := r.BlockID + "." + r.Prop
	if list, item, ok := strings.Cut(r.Prop, "."); ok {
		idx := 0
		if r.Index != nil {
			idx = *r.Index
		}
		key = r.BlockID + "." + list + "." + strconv.Itoa(idx) + "." + item
	}
	b, _ := json.Marshal(props{"id": in.ID, "expected_version_id": in.ExpectedVersionID, "lang": in.Lang, "page": in.Page, "entries": []props{{"key": key, "text": r.Text}}})
	out, err := setTranslation(c, b)
	if err != nil {
		return nil, err
	}
	m := out.(props)
	d, derr := docOfData(m["data"].(json.RawMessage))
	if derr != nil {
		return m, nil
	}
	if u, ok := unitByKey(d, in.Page, key); ok {
		m["entry"] = webEntry(d, in.Page, in.Lang, u)
	}
	return m, nil
}

func langMarkChecked(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Lang, Page string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	draft, err := draftOf(c, in.ID)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(props{"id": in.ID, "expected_version_id": draft, "lang": in.Lang, "page": in.Page})
	if _, err := reviewTranslation(c, b); err != nil {
		return nil, err
	}
	return props{"ok": true}, nil
}

// publicExtraLangs is public_get's `langs`: the shown languages other than the main one, in order.
func publicExtraLangs(data json.RawMessage) []string { return ShownLangs(data)[1:] }
