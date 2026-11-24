package sites

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	"regexp"
	"sort"
	"strings"
)

// ---------- templates ----------

// business reads the company profile into the four template slots (plus the accent colour).
func business(c *command.Ctx) (map[string]string, error) {
	raw, err := c.Call("company.get", props{})
	if err != nil {
		return nil, err
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, command.ErrInvalid("company profile unreadable")
	}
	s := func(k string) string { v, _ := p[k].(string); return strings.TrimSpace(v) }
	name := s("trade_name")
	if name == "" {
		name = s("legal_name")
	}
	if name == "" {
		name = s("name")
	}
	address := s("address")
	if pin := s("pincode"); pin != "" {
		if address != "" {
			address += ", "
		}
		address += pin
	}
	return map[string]string{"name": name, "phone": s("phone"), "email": s("email"), "address": address, "accent": s("email_accent")}, nil
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// fillOpts: Theme is the palette the owner picked (any theme field); Brand asks for the company accent as the primary.
type fillOpts struct {
	Brand bool
	Theme map[string]any
}

type filled struct {
	Data      json.RawMessage
	TooLong   []string
	BrandUsed bool
}

// fillTemplate fills the slots, never truncating: a value that does not validate in its place is left empty and
// reported in TooLong (the name falls back to a placeholder because the page needs one). The template keeps its own
// primary unless Brand is set and the accent is readable on that template (themeIssues empty).
func fillTemplate(slots map[string]string, key string, o fillOpts) (filled, error) {
	var out filled
	tpl, err := assets.ReadFile("templates/" + key + ".json")
	if err == nil {
		var v struct {
			Data json.RawMessage `json:"data"`
		}
		err = json.Unmarshal(tpl, &v)
		tpl = v.Data
	}
	if err != nil {
		return out, err
	}
	keys := []string{"name", "phone", "email", "address"}
	build := func(use map[string]bool) json.RawMessage {
		s := string(tpl)
		for _, k := range keys {
			v := ""
			if use[k] {
				v = slots[k]
			} else if k == "name" {
				v = fallbackName
			}
			b, _ := json.Marshal(v)
			s = strings.ReplaceAll(s, "{{"+k+"}}", string(b[1:len(b)-1]))
		}
		return json.RawMessage(s)
	}
	use := map[string]bool{}
	for _, k := range keys {
		use[k] = slots[k] != ""
	}
	out.TooLong = []string{}
	if Validate(build(use)) != nil {
		for _, k := range keys {
			if !use[k] {
				continue
			}
			if Validate(build(map[string]bool{k: true})) != nil {
				use[k] = false
				out.TooLong = append(out.TooLong, k)
			}
		}
	}
	var d map[string]any
	if err := json.Unmarshal(build(use), &d); err != nil {
		return out, err
	}
	theme := d["root"].(map[string]any)["props"].(map[string]any)["theme"].(map[string]any)
	if len(o.Theme) > 0 {
		if err := validateFields("theme", o.Theme, catalog.Theme, nil); err != nil {
			return out, command.ErrInvalid(err.Error())
		}
		for k, v := range o.Theme {
			theme[k] = v
		}
		if issues := themeIssues(theme); len(issues) > 0 {
			return out, command.ErrInvalid("that palette is not readable enough: " + strings.Join(issues, ", "))
		}
	}
	if accent := strings.ToLower(slots["accent"]); o.Brand && accentRe.MatchString(accent) {
		was := theme["primary"]
		theme["primary"] = accent
		if len(themeIssues(theme)) == 0 {
			out.BrandUsed = true
		} else {
			theme["primary"] = was
		}
	}
	doc, err := json.Marshal(d)
	if err != nil {
		return out, err
	}
	if err := Validate(doc); err != nil {
		return out, command.ErrInvalid("the template could not be filled: " + err.Error())
	}
	out.Data = doc
	return out, nil
}

type tplMeta struct {
	Key, Name, Description, Vertical, Style string
	Order                                   int
}

// templateList is read from the embedded templates directory (legal_* are page templates, not site designs), in
// the files' own "order". Adding a design is adding a file.
var templateList = func() []tplMeta {
	entries, err := assets.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	var out []tplMeta
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "legal_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, _ := assets.ReadFile("templates/" + e.Name())
		var m tplMeta
		if json.Unmarshal(b, &m) != nil || m.Key+".json" != e.Name() {
			panic("bad template " + e.Name())
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}()

func hasTemplate(key string) bool {
	for _, m := range templateList {
		if m.Key == key {
			return true
		}
	}
	return false
}

func templates(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		Brand bool
		Theme map[string]any
		Key   string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	b, err := business(c)
	if err != nil {
		return nil, err
	}
	out := []props{}
	for _, m := range templateList {
		if in.Key != "" && in.Key != m.Key {
			continue
		}
		f, err := fillTemplate(b, m.Key, fillOpts{Brand: in.Brand, Theme: in.Theme})
		if err != nil {
			return nil, err
		}
		out = append(out, props{"palette": palette(m.Key), "key": m.Key, "name": m.Name, "description": m.Description, "vertical": m.Vertical, "style": m.Style,
			"data": f.Data, "too_long": f.TooLong, "brand_available": accentRe.MatchString(b["accent"]), "brand_used": f.BrandUsed})
	}
	return out, nil
}

// palette is a template's own colours, as shipped (before the brand colour or a chosen palette).
func palette(key string) map[string]any {
	var v struct{ Data document }
	b, _ := assets.ReadFile("templates/" + key + ".json")
	json.Unmarshal(b, &v)
	th, _ := v.Data.Root.Props["theme"].(map[string]any)
	return map[string]any{"primary": th["primary"], "background": th["background"], "text": th["text"]}
}
