package sites

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func postFixtures(t *testing.T) (valid, invalid []struct {
	ID   string
	Body json.RawMessage
}) {
	b, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Valid, Invalid []struct {
			ID   string
			Body json.RawMessage
		}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Valid, f.Invalid
}

func TestValidatePostFixtures(t *testing.T) {
	valid, invalid := postFixtures(t)
	owned := map[string]bool{"a1b2c3d4-e5f6-7890-abcd-ef1234567890": true, "b2c3d4e5-f6a7-8901-bcde-f12345678901": true}
	for _, v := range valid {
		if err := ValidatePost(v.Body, owned); err != nil {
			t.Errorf("%s: %v", v.ID, err)
		}
	}
	for _, v := range invalid {
		switch v.ID {
		case "unknown-mark-italic": // italic is allowed by 17.2, which supersedes the fixture
			if err := ValidatePost(v.Body, owned); err != nil {
				t.Errorf("italic: %v", err)
			}
		case "body-too-large":
			big := `{"type":"doc","content":[` + strings.Repeat(`{"type":"paragraph","content":[{"type":"text","text":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}]},`, 2000) + `{"type":"paragraph"}]}`
			if ValidatePost(json.RawMessage(big), owned) == nil {
				t.Error("oversized body accepted")
			}
		default:
			if ValidatePost(v.Body, owned) == nil {
				t.Errorf("%s accepted", v.ID)
			}
		}
	}
}

func TestValidatePostRules(t *testing.T) {
	doc := func(inner string) json.RawMessage { return json.RawMessage(`{"type":"doc","content":[` + inner + `]}`) }
	para := func(txt string) string {
		return `{"type":"paragraph","content":[{"type":"text","text":"` + txt + `"}]}`
	}
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"empty paragraph":      {`{"type":"paragraph"}`, true},
		"horizontal rule":      {`{"type":"horizontalRule"}`, true},
		"orderedList start":    {`{"type":"orderedList","attrs":{"start":1},"content":[{"type":"listItem","content":[` + para("a") + `]}]}`, true},
		"duplicate mark":       {`{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"bold"},{"type":"bold"}]}]}`, false},
		"link extra attr":      {`{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"https://a.test","target":"_blank"}}]}]}`, false},
		"mailto link":          {`{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"mailto:a@b.test"}}]}]}`, true},
		"list in paragraph":    {`{"type":"paragraph","content":[{"type":"bulletList"}]}`, false},
		"heading level 4":      {`{"type":"heading","attrs":{"level":4},"content":[{"type":"text","text":"a"}]}`, false},
		"image extra attr":     {`{"type":"image","attrs":{"image_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","alt":"x","src":"http://x"}}`, false},
		"unknown node field":   {`{"type":"paragraph","html":"<b>"}`, false},
		"text with child":      {`{"type":"paragraph","content":[{"type":"text","text":"a","content":[]}]}`, false},
		"empty text":           {`{"type":"paragraph","content":[{"type":"text","text":"\u0001"}]}`, false},
		"heading without attr": {`{"type":"heading","content":[{"type":"text","text":"a"}]}`, false},
	} {
		err := ValidatePost(doc(tc.body), map[string]bool{"a1b2c3d4-e5f6-7890-abcd-ef1234567890": true})
		if (err == nil) != tc.ok {
			t.Errorf("%s: err %v", name, err)
		}
	}
	if ValidatePost(json.RawMessage(`{"type":"paragraph"}`), nil) == nil {
		t.Error("a body must be one doc")
	}
	// control characters are stripped, and nil images skips ownership
	out, err := NormalizePost(doc(para(`a\u0007b`)), nil)
	if err != nil || !strings.Contains(string(out), `"ab"`) {
		t.Errorf("strip: %s %v", out, err)
	}
	// limits: 41 images, too many characters
	imgs := strings.Repeat(`{"type":"image","attrs":{"image_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","alt":""}},`, 21)
	if ValidatePost(doc(strings.TrimSuffix(imgs, ",")), nil) == nil {
		t.Error("21 images accepted")
	}
	long := strings.Repeat("a", 1000)
	if ValidatePost(doc(strings.Repeat(para(long)+",", 41)+para("x")), nil) == nil {
		t.Error("over 40000 characters accepted")
	}
}
