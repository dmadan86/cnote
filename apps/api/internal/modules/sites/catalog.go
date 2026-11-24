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

//go:embed catalog.json template_services.json
var assets embed.FS

type field struct {
	Kind     string           `json:"kind"`
	Max      int              `json:"max"`
	Required bool             `json:"required"`
	Values   []string         `json:"values"`
	Props    map[string]field `json:"props"`
}
type blockDef struct {
	Max   int              `json:"max"`
	Props map[string]field `json:"props"`
}

var catalog struct {
	Theme  map[string]field    `json:"theme"`
	Blocks map[string]blockDef `json:"blocks"`
}

func init() {
	b, _ := assets.ReadFile("catalog.json")
	if err := json.Unmarshal(b, &catalog); err != nil {
		panic(err)
	}
}

var idPattern = regexp.MustCompile(`^[A-Za-z]+-[0-9a-f]{8}$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)
var phonePattern = regexp.MustCompile(`^\+?[0-9 ()-]{3,30}$`)
var telPattern = regexp.MustCompile(`^[0-9+ -]+$`)

func validateFields(path string, values map[string]any, defs map[string]field) error {
	for k, v := range values {
		f, ok := defs[k]
		if !ok {
			return fmt.Errorf("%s.%s: unknown property", path, k)
		}
		if err := validateField(path+"."+k, v, f); err != nil {
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
func validateField(path string, v any, f field) error {
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
			if err := validateFields(path, m, f.Props); err != nil {
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
		if s != "" {
			return fmt.Errorf("%s: images are not supported", path)
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
func Validate(data json.RawMessage) error {
	var d struct {
		Root struct {
			Props map[string]any `json:"props"`
		} `json:"root"`
		Content []struct {
			Type  string         `json:"type"`
			Props map[string]any `json:"props"`
		} `json:"content"`
	}
	if len(data) >= 65536 {
		return fmt.Errorf("page too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return err
	}
	if len(d.Content) > 30 {
		return fmt.Errorf("at most 30 blocks")
	}
	for k := range d.Root.Props {
		if k != "title" && k != "theme" {
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
	theme, ok := d.Root.Props["theme"].(map[string]any)
	if !ok {
		return fmt.Errorf("theme: expected object")
	}
	if err := validateFields("theme", theme, catalog.Theme); err != nil {
		return err
	}
	for k := range catalog.Theme {
		if _, ok := theme[k]; !ok {
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
	counts := map[string]int{}
	ids := map[string]bool{}
	for _, block := range d.Content {
		def, ok := catalog.Blocks[block.Type]
		if !ok {
			return fmt.Errorf("unknown block %s", block.Type)
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
		if err := validateFields(block.Type, props, def.Props); err != nil {
			return err
		}
	}
	return nil
}
