package sites

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"
)

func themeOf(t *testing.T, key string) map[string]any {
	t.Helper()
	f, err := fillTemplate(map[string]string{}, key, fillOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(f.Data, &d); err != nil {
		t.Fatal(err)
	}
	return d.Root.Props["theme"].(map[string]any)
}

func TestTemplateList(t *testing.T) {
	if len(templateList) != 15 {
		t.Fatalf("want 15 templates, got %d", len(templateList))
	}
	for _, m := range templateList {
		if m.Name == "" || m.Description == "" || m.Vertical == "" || m.Style == "" || m.Order == 0 {
			t.Errorf("%s: incomplete metadata %+v", m.Key, m)
		}
	}
}

// Every pair the renderer draws is readable for every template, and so is the template with its own palette only.
func TestTemplatePaletteContrast(t *testing.T) {
	for _, m := range templateList {
		if issues := themeIssues(themeOf(t, m.Key)); len(issues) > 0 {
			t.Errorf("%s: %s", m.Key, strings.Join(issues, "; "))
		}
	}
}

func TestFocusRingOnPrimarySection(t *testing.T) {
	// A text-coloured outline would disappear against this black primary.
	theme := map[string]any{"primary": "#000000", "text": "#000000", "background": "#ffffff"}
	if issues := themeIssues(theme); len(issues) != 0 {
		t.Fatalf("two-tone focus ring should remain visible: %v", issues)
	}
	for _, surface := range []rgb{{0, 0, 0}, {255, 255, 255}, {128, 128, 128}} {
		if r := math.Max(ratio(rgb{0, 0, 0}, surface), ratio(rgb{255, 255, 255}, surface)); r < 3 {
			t.Errorf("focus ring lacks contrast against %v: %.2f", surface, r)
		}
	}
}

func TestBrandColourIsOptIn(t *testing.T) {
	slots := map[string]string{"name": "X", "accent": "#7c3aed"}
	for _, m := range templateList {
		own := themeOf(t, m.Key)["primary"]
		f, err := fillTemplate(slots, m.Key, fillOpts{})
		if err != nil || f.BrandUsed {
			t.Fatalf("%s: brand colour applied without opt-in (%v)", m.Key, err)
		}
		var d document
		json.Unmarshal(f.Data, &d)
		if d.Root.Props["theme"].(map[string]any)["primary"] != own {
			t.Errorf("%s: primary changed without opt-in", m.Key)
		}
	}
	// a near-white accent cannot be read on a light page: kept out, the template's own colour stays
	f, err := fillTemplate(map[string]string{"accent": "#f5f5f5"}, "services", fillOpts{Brand: true})
	if err != nil || f.BrandUsed {
		t.Fatalf("unreadable accent used: %v", err)
	}
	f, err = fillTemplate(slots, "services", fillOpts{Brand: true})
	if err != nil || !f.BrandUsed || !strings.Contains(string(f.Data), "#7c3aed") {
		t.Fatalf("readable accent not used: %v", err)
	}
}

func TestChosenTheme(t *testing.T) {
	f, err := fillTemplate(map[string]string{}, "clinic", fillOpts{Theme: map[string]any{"primary": "#9d174d", "background": "#fdf6f3", "text": "#3b1f2b"}})
	if err != nil || !strings.Contains(string(f.Data), "#9d174d") {
		t.Fatalf("chosen palette not applied: %v", err)
	}
	for name, th := range map[string]map[string]any{
		"low contrast": {"primary": "#aaaaaa", "background": "#ffffff", "text": "#999999"},
		"bad colour":   {"primary": "red"},
		"unknown key":  {"nope": "#000000"},
		"bad font":     {"font": "comic"},
	} {
		if _, err := fillTemplate(map[string]string{}, "clinic", fillOpts{Theme: th}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Every template, filled with missing, normal and over-long company data, is a valid document whose ids and links resolve.
func TestTemplatesFillAndValidate(t *testing.T) {
	long := strings.Repeat("x", 400)
	cases := map[string]map[string]string{
		"missing": {},
		"normal":  {"name": "Example Traders", "phone": "+91 9876543210", "email": "a@example.com", "address": "12 MG Road, Pune, 411001"},
		"long":    {"name": long, "phone": long, "email": long + "@example.com", "address": long},
	}
	id := regexp.MustCompile(`^([A-Za-z]+)-[0-9a-f]{8}$`)
	for _, m := range templateList {
		for label, slots := range cases {
			f, err := fillTemplate(slots, m.Key, fillOpts{})
			if err != nil {
				t.Fatalf("%s/%s: %v", m.Key, label, err)
			}
			if err := Validate(f.Data); err != nil {
				t.Fatalf("%s/%s: %v", m.Key, label, err)
			}
			if strings.Contains(string(f.Data), "{{") {
				t.Errorf("%s/%s: unfilled slot", m.Key, label)
			}
			if label == "long" && len(f.TooLong) == 0 {
				t.Errorf("%s: over-long data not reported", m.Key)
			}
			blocks := allBlocks(f.Data)
			ids := map[string]bool{}
			for _, b := range blocks {
				ids[b.Props["id"].(string)] = true
				if x := id.FindStringSubmatch(b.Props["id"].(string)); x == nil || x[1] != b.Type {
					t.Errorf("%s: bad id %v", m.Key, b.Props["id"])
				}
			}
			check := func(href any) {
				if s, _ := href.(string); strings.HasPrefix(s, "#") && !ids[s[1:]] {
					t.Errorf("%s/%s: link %s points at no block", m.Key, label, s)
				}
			}
			for _, b := range blocks {
				check(b.Props["cta_href"])
				links, _ := b.Props["links"].([]any)
				for _, l := range links {
					check(l.(map[string]any)["href"])
				}
			}
			if !HasBlock(f.Data, "WhatsAppButton") || !HasBlock(f.Data, "ContactForm") {
				t.Errorf("%s: missing WhatsApp or ContactForm", m.Key)
			}
			// no placeholder copy: nothing a visitor would read is an instruction or an invented claim
			if s := string(f.Data); strings.Contains(s, "Add your") || strings.Contains(s, "Your first") {
				t.Errorf("%s: placeholder copy stored", m.Key)
			}
		}
	}
}
