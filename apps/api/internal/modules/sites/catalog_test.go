package sites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(strings.NewReplacer("{{name}}", "Business", "{{phone}}", "", "{{email}}", "", "{{address}}", "").Replace(`{
  "root": {
    "props": {
      "title": "{{name}}",
      "theme": {
        "primary": "#4f46e5",
        "background": "#ffffff",
        "text": "#111827",
        "font": "sans",
        "radius": "md"
      }
    }
  },
  "content": [
    {
      "type": "Header",
      "props": {
        "id": "Header-00000001",
        "brand": "{{name}}",
        "links": []
      }
    },
    {
      "type": "Hero",
      "props": {
        "id": "Hero-00000002",
        "title": "{{name}}",
        "align": "center"
      }
    },
    {
      "type": "Services",
      "props": {
        "id": "Services-00000003",
        "title": "Our services",
        "items": [
          {
            "name": "Your first service",
            "description": ""
          }
        ]
      }
    },
    {
      "type": "ContactForm",
      "props": {
        "id": "ContactForm-00000004",
        "title": "Contact us",
        "phone": "{{phone}}",
        "email": "{{email}}",
        "address": "{{address}}",
        "submit_label": "Send"
      }
    },
    {
      "type": "Footer",
      "props": {
        "id": "Footer-00000005",
        "text": "{{name}}",
        "phone": "{{phone}}",
        "email": "{{email}}",
        "address": "{{address}}",
        "links": []
      }
    }
  ]
}
`))
}
func TestWebCatalogInSync(t *testing.T) {
	a, err := assets.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../../../web/src/components/sites/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("web catalog differs from Go catalog")
	}
}
func fmtID(i int) string { return fmt.Sprintf("Services-%08x", i+100) }
func TestValidate(t *testing.T) {
	base := fixture(t)
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, tool, input string }{
		{"unknown block", "add_block", `{"type":"Unknown","props":{}}`},
		{"unknown prop", "update_block_props", `{"id":"Hero-00000002","props":{"unknown":"x"}}`},
		{"wrong kind", "update_block_props", `{"id":"Hero-00000002","props":{"title":42}}`},
		{"title limit", "update_block_props", `{"id":"Hero-00000002","props":{"title":"` + strings.Repeat("x", 81) + `"}}`},
		{"duplicate singleton", "add_block", `{"type":"Hero","props":{"title":"x"}}`},
		{"bad contrast", "set_theme", `{"text":"#ffffff"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Apply(base, tc.tool, json.RawMessage(tc.input)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	for _, tc := range []struct {
		link string
		ok   bool
	}{
		{"https://example.com", true}, {"http://example.com/path", true}, {"tel:+123-456", true}, {"mailto:a@example.com", true}, {"#Hero-00000002", true},
		{"javascript:alert(1)", false}, {"data:text/html,x", false}, {"//example.com", false}, {"https://user@example.com", false}, {"https://example.com/a b", false},
	} {
		t.Run(tc.link, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"id": "Hero-00000002", "props": map[string]any{"cta_href": tc.link}})
			_, err := Apply(base, "update_block_props", b)
			if (err == nil) != tc.ok {
				t.Fatalf("error: %v", err)
			}
		})
	}
	var d map[string]any
	if err := json.Unmarshal(base, &d); err != nil {
		t.Fatal(err)
	}
	content := d["content"].([]any)
	for i := 0; i < 26; i++ {
		content = append(content, map[string]any{"type": "Services", "props": map[string]any{"id": fmtID(i)}})
	}
	d["content"] = content
	b, _ := json.Marshal(d)
	if err := Validate(b); err == nil {
		t.Fatal("expected block limit")
	}
}

func TestStrictPageAndValues(t *testing.T) {
	var d map[string]any
	if err := json.Unmarshal(fixture(t), &d); err != nil {
		t.Fatal(err)
	}
	d["unexpected"] = true
	b, _ := json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("unknown top-level field accepted")
	}
	delete(d, "unexpected")
	blocks := d["content"].([]any)
	d["content"] = append(blocks, blocks[0])
	b, _ = json.Marshal(d)
	if Validate(b) == nil {
		t.Fatal("duplicate ID accepted")
	}
	for _, s := range []string{"#ABCDEF", "#12345g"} {
		if validateField("color", s, field{Kind: "color"}) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{"https://example.com/\u00a0x", "https://example.com/\u0001", "mailto:a@example.com,b@example.com", "tel:abc", "#Hero-000002"} {
		if validateField("link", s, field{Kind: "link"}) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestImageIDsAndWhatsApp(t *testing.T) {
	data := json.RawMessage(`{"content":[{"type":"Gallery","props":{"items":[{"image":"12345678-1234-1234-1234-123456789abc"}]}}]}`)
	if len(ImageIDs(data)) != 1 || !HasBlock(data, "Gallery") || HasBlock(data, "Hero") {
		t.Fatal("image or block lookup failed")
	}
	if WaDigits("98765 43210") != "919876543210" || WaDigits("123") != "" {
		t.Fatal("invalid WhatsApp digits")
	}
}
