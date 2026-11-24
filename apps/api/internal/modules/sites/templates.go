package sites

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	"regexp"
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

// fillTemplate fills the slots, never truncating: a value that does not validate in its place is left empty and
// reported in tooLong (the name falls back to a placeholder because the page needs one).
func fillTemplate(slots map[string]string, selected ...string) (json.RawMessage, []string, error) {
	key := templateKey
	if len(selected) > 0 {
		key = selected[0]
	}
	tpl, err := assets.ReadFile("templates/" + key + ".json")
	if err == nil {
		var v struct {
			Data json.RawMessage `json:"data"`
		}
		err = json.Unmarshal(tpl, &v)
		tpl = v.Data
	}
	if err != nil {
		return nil, nil, err
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
	tooLong := []string{}
	if Validate(build(use)) != nil {
		for _, k := range keys {
			if !use[k] {
				continue
			}
			if Validate(build(map[string]bool{k: true})) != nil {
				use[k] = false
				tooLong = append(tooLong, k)
			}
		}
	}
	doc := build(use)
	if accent := slots["accent"]; accentRe.MatchString(accent) {
		var d map[string]any
		if json.Unmarshal(doc, &d) == nil {
			d["root"].(map[string]any)["props"].(map[string]any)["theme"].(map[string]any)["primary"] = strings.ToLower(accent)
			if b, err := json.Marshal(d); err == nil && Validate(b) == nil {
				doc = b
			}
		}
	}
	if err := Validate(doc); err != nil {
		return nil, nil, command.ErrInvalid("the template could not be filled: " + err.Error())
	}
	return doc, tooLong, nil
}

func hasTemplate(key string) bool {
	for _, k := range templateKeys {
		if k == key {
			return true
		}
	}
	return false
}

var templateKeys = []string{"services", "shop", "clinic", "restaurant", "trader"}

func templates(c *command.Ctx, raw json.RawMessage) (any, error) {
	b, err := business(c)
	if err != nil {
		return nil, err
	}
	out := []props{}
	for _, key := range templateKeys {
		var meta struct{ Key, Name, Description, Vertical string }
		bytes, err := assets.ReadFile("templates/" + key + ".json")
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(bytes, &meta); err != nil {
			return nil, err
		}
		data, tooLong, err := fillTemplate(b, key)
		if err != nil {
			return nil, err
		}
		out = append(out, props{"key": meta.Key, "name": meta.Name, "description": meta.Description, "vertical": meta.Vertical, "data": data, "too_long": tooLong})
	}
	return out, nil
}
