package sites

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed catalog.json templates/*.json
var assets embed.FS

type field struct {
	Kind     string           `json:"kind"`
	Max      int              `json:"max"`
	Required bool             `json:"required"`
	Optional bool             `json:"optional"`
	Values   []string         `json:"values"`
	Props    map[string]field `json:"props"`
	Verbatim bool             `json:"verbatim"` // the model may only copy it from the user's message
	// Translatable marks the text props a translation overlay may hold (design 18.3).
	Translatable bool   `json:"translatable"`
	Label        string `json:"label"`
}
type blockDef struct {
	Label  string           `json:"label"`
	Max    int              `json:"max"`
	Shared bool             `json:"shared"` // lives on Home, drawn on every page
	Props  map[string]field `json:"props"`
}

var catalog struct {
	Theme  map[string]field    `json:"theme"`
	Blocks map[string]blockDef `json:"blocks"`
	Pages  struct {
		Max  int `json:"max"`
		Slug struct {
			Pattern string `json:"pattern"`
		} `json:"slug"`
		Content struct {
			Max int `json:"max"`
		} `json:"content"`
	} `json:"pages"`
}

func init() {
	b, _ := assets.ReadFile("catalog.json")
	if err := json.Unmarshal(b, &catalog); err != nil {
		panic(err)
	}
	slugPattern = regexp.MustCompile(catalog.Pages.Slug.Pattern)
}

var imageIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z]+-[0-9a-f]{8}$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)
var phonePattern = regexp.MustCompile(`^\+?[0-9 ()-]{3,30}$`)
var telPattern = regexp.MustCompile(`^[0-9+ -]+$`)

// slugPattern is a page address (design 17.10); internalLink is "/", "/<slug>" or either with "#<block id>".
var slugPattern *regexp.Regexp
var internalLink = regexp.MustCompile(`^/([a-z0-9-]{1,40})?(?:#([A-Za-z]+-[0-9a-f]{8}))?$`)

// reservedSlugs can never be a page: the web server answers them itself.
var reservedSlugs = map[string]bool{"blog": true, "api": true, "internal": true, "_next": true, "robots.txt": true, "sitemap.xml": true, "favicon.ico": true, "assets": true, "img": true, "published": true}

// maxDocBytes is below the column's 262144 because jsonb text is a little longer than compact JSON.
const maxDocBytes = 240000

func validateFields(path string, values map[string]any, defs map[string]field, slugs map[string]bool) error {
	for k, v := range values {
		f, ok := defs[k]
		if !ok {
			return fmt.Errorf("%s.%s: unknown property", path, k)
		}
		if err := validateFieldIn(path+"."+k, v, f, slugs); err != nil {
			return err
		}
	}
	for k, f := range defs {
		if f.Required {
			v, ok := values[k]
			if !ok || v == "" {
				return fmt.Errorf("%s.%s: required", path, k)
			}
		}
	}
	return nil
}
func validateField(path string, v any, f field) error { return validateFieldIn(path, v, f, nil) }

// validateFieldIn checks one value; slugs are the pages a link may point at.
func validateFieldIn(path string, v any, f field, slugs map[string]bool) error {
	if f.Kind == "list" {
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: expected list", path)
		}
		if len(a) > f.Max {
			return fmt.Errorf("%s: at most %d items", path, f.Max)
		}
		for _, x := range a {
			m, ok := x.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: expected object", path)
			}
			if err := validateFields(path, m, f.Props, slugs); err != nil {
				return err
			}
		}
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s: expected string", path)
	}
	if f.Max > 0 && utf8.RuneCountInString(s) > f.Max {
		return fmt.Errorf("%s: at most %d characters", path, f.Max)
	}
	switch f.Kind {
	case "text":
		if strings.ContainsAny(s, "\r\n") {
			return fmt.Errorf("%s: expected one line", path)
		}
	case "longtext":
	case "color":
		if !colorPattern.MatchString(s) {
			return fmt.Errorf("%s: invalid color", path)
		}
	case "enum":
		for _, x := range f.Values {
			if s == x {
				return nil
			}
		}
		return fmt.Errorf("%s: invalid choice", path)
	case "phone":
		if s != "" && !phonePattern.MatchString(s) {
			return fmt.Errorf("%s: invalid phone", path)
		}
	case "email":
		if s != "" {
			a, e := mail.ParseAddress(s)
			if e != nil || a.Address != s {
				return fmt.Errorf("%s: invalid email", path)
			}
		}
	case "image":
		if s != "" && !imageIDPattern.MatchString(s) {
			return fmt.Errorf("%s: invalid image id", path)
		}
	case "link":
		if len(s) > 300 {
			return fmt.Errorf("%s: link too long", path)
		}
		if s != "" {
			u, e := url.Parse(s)
			good := false
			if e == nil && !strings.ContainsFunc(s, unicode.IsControl) {
				switch u.Scheme {
				case "http", "https":
					decoded, decodeErr := url.PathUnescape(s)
					good = decodeErr == nil && u.Hostname() != "" && u.User == nil && !strings.ContainsFunc(decoded, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
				case "tel":
					good = u.Opaque != "" && telPattern.MatchString(u.Opaque) && strings.ContainsAny(u.Opaque, "0123456789")
				case "mailto":
					good = u.Opaque != "" && validEmail(u.Opaque) && !strings.ContainsFunc(s, unicode.IsSpace)
				case "":
					good = strings.HasPrefix(s, "#") && idPattern.MatchString(strings.TrimPrefix(s, "#"))
					if m := internalLink.FindStringSubmatch(s); m != nil {
						good = m[1] == "" || slugs[m[1]]
					}
				}
			}
			if !good {
				return fmt.Errorf("%s: invalid link", path)
			}
		}
	default:
		return fmt.Errorf("%s: unknown kind", path)
	}
	return nil
}
func validEmail(s string) bool { a, e := mail.ParseAddress(s); return e == nil && a.Address == s }
func luminance(s string) float64 {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	conv := func(n int) float64 {
		v := float64(n) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return .2126*conv(r) + .7152*conv(g) + .0722*conv(b)
}

type docBlock struct {
	Type  string         `json:"type"`
	Props map[string]any `json:"props"`
}
type docPage struct {
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	Content     []docBlock `json:"content"`
	I18n        overlays   `json:"i18n"`
}
type document struct {
	Root struct {
		Props map[string]any `json:"props"`
		I18n  overlays       `json:"i18n"`
	} `json:"root"`
	Content []docBlock `json:"content"`
	Pages   []docPage  `json:"pages"`
}

// Validate checks a whole document: Home, every extra page and the theme (design 2, 17.10).
func Validate(data json.RawMessage) error {
	var d document
	if len(data) >= maxDocBytes {
		return fmt.Errorf("page too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return err
	}
	if len(d.Pages) > catalog.Pages.Max {
		return fmt.Errorf("at most %d pages", catalog.Pages.Max)
	}
	slugs := map[string]bool{}
	for _, p := range d.Pages {
		if !slugPattern.MatchString(p.Slug) {
			return fmt.Errorf("page %q: the address may use only a-z, 0-9 and hyphens, up to 40 characters", p.Slug)
		}
		if reservedSlugs[p.Slug] && p.Slug != "blog" {
			return fmt.Errorf("page %q: that address is reserved", p.Slug)
		}
		if slugs[p.Slug] {
			return fmt.Errorf("page %q: duplicate address", p.Slug)
		}
		slugs[p.Slug] = true
	}
	for k := range d.Root.Props {
		if k != "title" && k != "description" && k != "theme" && k != "langs" && k != "langs_hidden" {
			return fmt.Errorf("root: unknown property %s", k)
		}
	}
	title, ok := d.Root.Props["title"].(string)
	if !ok {
		return fmt.Errorf("root.title: expected string")
	}
	if err := validateField("root.title", title, field{Kind: "text", Max: 120}); err != nil {
		return err
	}
	if v, exists := d.Root.Props["description"]; exists {
		if err := validateField("root.description", v, field{Kind: "longtext", Max: 160}); err != nil {
			return err
		}
	}
	theme, ok := d.Root.Props["theme"].(map[string]any)
	if !ok {
		return fmt.Errorf("theme: expected object")
	}
	if err := validateFields("theme", theme, catalog.Theme, nil); err != nil {
		return err
	}
	for k, f := range catalog.Theme {
		if _, ok := theme[k]; !ok && !f.Optional {
			return fmt.Errorf("theme.%s: required", k)
		}
	}
	a, b := luminance(theme["text"].(string)), luminance(theme["background"].(string))
	if a < b {
		a, b = b, a
	}
	if (a+.05)/(b+.05) < 4.5 {
		return fmt.Errorf("theme: text contrast must be at least 4.5")
	}
	ids := map[string]bool{}
	if err := validateBlocks(d.Content, true, slugs, ids); err != nil {
		return err
	}
	langs, err := validateLangs(d.Root.Props)
	if err != nil {
		return err
	}
	if err := validateOverlays("root", d.Root.I18n, langs, d.Content, field{Kind: "text", Max: 120}); err != nil {
		return err
	}
	for _, p := range d.Pages {
		if err := validateOverlays("page "+p.Slug, p.I18n, langs, p.Content, field{Kind: "text", Max: 80}); err != nil {
			return err
		}
	}
	for _, p := range d.Pages {
		if p.Title == "" {
			return fmt.Errorf("page %s: title required", p.Slug)
		}
		if err := validateField("page "+p.Slug+" title", p.Title, field{Kind: "text", Max: 80}); err != nil {
			return err
		}
		if p.Description != nil {
			if err := validateField("page "+p.Slug+" description", *p.Description, field{Kind: "longtext", Max: 160}); err != nil {
				return err
			}
		}
		if err := validateBlocks(p.Content, false, slugs, ids); err != nil {
			return err
		}
	}
	return nil
}

// validateBlocks checks one page's blocks; ids is shared by every page so a block id is unique in the document.
func validateBlocks(blocks []docBlock, home bool, slugs, ids map[string]bool) error {
	if len(blocks) > catalog.Pages.Content.Max {
		return fmt.Errorf("at most %d blocks", catalog.Pages.Content.Max)
	}
	counts := map[string]int{}
	for _, block := range blocks {
		def, ok := catalog.Blocks[block.Type]
		if !ok {
			return fmt.Errorf("unknown block %s", block.Type)
		}
		if def.Shared && !home {
			return fmt.Errorf("%s: only the Home page can have it; it shows on every page", block.Type)
		}
		counts[block.Type]++
		if def.Max > 0 && counts[block.Type] > def.Max {
			return fmt.Errorf("%s: too many blocks", block.Type)
		}
		id, ok := block.Props["id"].(string)
		if !ok || !idPattern.MatchString(id) || !strings.HasPrefix(id, block.Type+"-") || ids[id] {
			return fmt.Errorf("%s: invalid or duplicate id", block.Type)
		}
		ids[id] = true
		props := map[string]any{}
		for k, v := range block.Props {
			if k != "id" {
				props[k] = v
			}
		}
		if err := validateFields(block.Type, props, def.Props, slugs); err != nil {
			return err
		}
		if block.Type == "Embed" {
			provider, _ := props["provider"].(string)
			ref, _ := props["source"].(string)
			if EmbedSrc(provider, ref) == "" {
				return fmt.Errorf("Embed.source: not a valid link or id for %s", provider)
			}
		}
	}
	return nil
}

// allBlocks lists every block of every page, Home first. A document that does not parse has none.
func allBlocks(data json.RawMessage) []docBlock {
	var d document
	if json.Unmarshal(data, &d) != nil {
		return nil
	}
	out := d.Content
	for _, p := range d.Pages {
		out = append(out, p.Content...)
	}
	return out
}

// ImageIDs returns the distinct uploaded image IDs referenced by any page.
func ImageIDs(data json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(map[string]any, map[string]field)
	walk = func(values map[string]any, defs map[string]field) {
		for k, f := range defs {
			v := values[k]
			if f.Kind == "image" {
				if id, ok := v.(string); ok && imageIDPattern.MatchString(id) && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
			if f.Kind == "list" {
				if list, ok := v.([]any); ok {
					for _, item := range list {
						if obj, ok := item.(map[string]any); ok {
							walk(obj, f.Props)
						}
					}
				}
			}
		}
	}
	for _, block := range allBlocks(data) {
		walk(block.Props, catalog.Blocks[block.Type].Props)
	}
	return out
}

// HasBlock reports whether any page has a block of this type.
func HasBlock(data json.RawMessage, typ string) bool {
	for _, b := range allBlocks(data) {
		if b.Type == typ {
			return true
		}
	}
	return false
}

// WaDigits converts a phone number to the digits used by WhatsApp links.
func WaDigits(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if !strings.ContainsRune("+ -()", r) {
			return ""
		}
	}
	digits := b.String()
	if len(digits) == 10 {
		digits = "91" + digits
	}
	if len(digits) < 8 || len(digits) > 15 {
		return ""
	}
	return digits
}
