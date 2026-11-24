package main

import (
	"encoding/json"
	"testing"
)

// fixture is a minimal page JSON for assertion tests (independent of fixture.json on disk).
var fixtureData = json.RawMessage(`{
  "root": {"props": {"title": "Test Clinic", "theme": {"primary": "#4f46e5", "background": "#ffffff", "text": "#111827", "font": "sans", "radius": "md"}}},
  "content": [
    {"type": "Header", "props": {"id": "Header-aa1111", "brand": "Test Clinic", "links": [{"label": "Home", "href": "https://example.com/"}, {"label": "Services", "href": "#services"}], "cta_label": "Book", "cta_href": "tel:+919000000000"}},
    {"type": "Hero",   "props": {"id": "Hero-bb2222",   "title": "Test Clinic", "subtitle": "Great dental care", "cta_label": "Book Now", "cta_href": "tel:+919000000000", "align": "center"}},
    {"type": "Services", "props": {"id": "Services-cc3333", "title": "Our Services", "items": [{"name": "Check-up", "description": "Basic exam", "price": "₹500"}, {"name": "Whitening", "description": "Brighten your smile", "price": "₹2000"}]}},
    {"type": "ContactForm", "props": {"id": "ContactForm-dd4444", "title": "Contact Us", "intro": "Reach out anytime", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "submit_label": "Send"}},
    {"type": "Footer", "props": {"id": "Footer-ee5555", "text": "© 2026 Test Clinic", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "links": [{"label": "Privacy", "href": "https://example.com/privacy"}]}}
  ]
}`)

func TestAssertEquals_pass(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "equals", Value: "Test Clinic"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertEquals_fail(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "equals", Value: "Wrong Title"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].Path != "Hero.title" || errs[0].Op != "equals" {
		t.Errorf("unexpected error: %v", errs[0])
	}
}

func TestAssertContains_pass(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.subtitle", Op: "contains", Value: "dental"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertContains_fail(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.subtitle", Op: "contains", Value: "DOES_NOT_EXIST"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestAssertMatches_pass(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "matches", Value: "(?i)test"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertMatches_fail(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "matches", Value: "^Nope$"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestAssertMatches_badPattern(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "matches", Value: "[bad"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for bad pattern, got %d", len(errs))
	}
}

func TestAssertAbsent_pass(t *testing.T) {
	// Hero.image is not set in the fixture → absent should pass
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.image", Op: "absent"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertAbsent_fail(t *testing.T) {
	// Hero.title is set → absent should fail
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "absent"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestAssertCount_blocks(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "blocks", Op: "count", Value: float64(5)},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertCount_list(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Services.items", Op: "count", Value: float64(2)},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertCount_zero(t *testing.T) {
	// Header.links has 2 items; count 0 should fail
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Header.links", Op: "count", Value: float64(0)},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestAssertTheme(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "theme.primary", Op: "equals", Value: "#4f46e5"},
		{Path: "theme.font", Op: "equals", Value: "sans"},
		{Path: "theme.radius", Op: "equals", Value: "md"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertServicesItemField(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Services.items.0.name", Op: "equals", Value: "Check-up"},
		{Path: "Services.items.1.price", Op: "equals", Value: "₹2000"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertBlocksIndexedType(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "blocks.0.type", Op: "equals", Value: "Header"},
		{Path: "blocks.4.type", Op: "equals", Value: "Footer"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestAssertBlocksIndexedOutOfRange(t *testing.T) {
	// blocks.99 does not exist → absent-style path resolution returns false
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "blocks.99.type", Op: "equals", Value: "Hero"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for out-of-range index, got %d", len(errs))
	}
}

func TestAssertUnknownOp(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "unknown_op", Value: "x"},
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for unknown op, got %d", len(errs))
	}
}

func TestAssertBadJSON(t *testing.T) {
	errs := EvalAssertions("test", json.RawMessage(`not json`), []Assertion{
		{Path: "Hero.title", Op: "equals", Value: "x"},
	})
	if len(errs) != 1 || errs[0].Op != "parse" {
		t.Fatalf("expected parse error, got %v", errs)
	}
}

// --- CheckCollateral ---

var modifiedData = json.RawMessage(`{
  "root": {"props": {"title": "Test Clinic", "theme": {"primary": "#4f46e5", "background": "#ffffff", "text": "#111827", "font": "sans", "radius": "md"}}},
  "content": [
    {"type": "Header", "props": {"id": "Header-aa1111", "brand": "Test Clinic", "links": [{"label": "Home", "href": "https://example.com/"}, {"label": "Services", "href": "#services"}], "cta_label": "Book", "cta_href": "tel:+919000000000"}},
    {"type": "Hero",   "props": {"id": "Hero-bb2222",   "title": "CHANGED TITLE", "subtitle": "Great dental care", "cta_label": "Book Now", "cta_href": "tel:+919000000000", "align": "center"}},
    {"type": "Services", "props": {"id": "Services-cc3333", "title": "Our Services", "items": [{"name": "Check-up", "description": "Basic exam", "price": "₹500"}, {"name": "Whitening", "description": "Brighten your smile", "price": "₹2000"}]}},
    {"type": "ContactForm", "props": {"id": "ContactForm-dd4444", "title": "Contact Us", "intro": "Reach out anytime", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "submit_label": "Send"}},
    {"type": "Footer", "props": {"id": "Footer-ee5555", "text": "© 2026 Test Clinic", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "links": [{"label": "Privacy", "href": "https://example.com/privacy"}]}}
  ]
}`)

func TestCheckCollateral_allowedChange(t *testing.T) {
	// Hero changed, Hero is in mayChange → no collateral diff
	diffs := CheckCollateral("test", fixtureData, modifiedData, []string{"Hero"})
	if len(diffs) != 0 {
		t.Fatalf("expected no collateral diffs, got %v", diffs)
	}
}

func TestCheckCollateral_forbiddenChange(t *testing.T) {
	// Hero changed, but mayChange=[] → collateral diff expected
	diffs := CheckCollateral("test", fixtureData, modifiedData, []string{})
	if len(diffs) == 0 {
		t.Fatal("expected collateral diff, got none")
	}
}

func TestCheckCollateral_themeUnchanged(t *testing.T) {
	// Theme is the same, theme not in mayChange → no diff
	diffs := CheckCollateral("test", fixtureData, modifiedData, []string{"Hero"})
	if len(diffs) != 0 {
		t.Fatalf("unexpected collateral diffs: %v", diffs)
	}
}

var themeChangedData = json.RawMessage(`{
  "root": {"props": {"title": "Test Clinic", "theme": {"primary": "#ff0000", "background": "#ffffff", "text": "#111827", "font": "sans", "radius": "md"}}},
  "content": [
    {"type": "Header", "props": {"id": "Header-aa1111", "brand": "Test Clinic", "links": [{"label": "Home", "href": "https://example.com/"}, {"label": "Services", "href": "#services"}], "cta_label": "Book", "cta_href": "tel:+919000000000"}},
    {"type": "Hero",   "props": {"id": "Hero-bb2222",   "title": "Test Clinic", "subtitle": "Great dental care", "cta_label": "Book Now", "cta_href": "tel:+919000000000", "align": "center"}},
    {"type": "Services", "props": {"id": "Services-cc3333", "title": "Our Services", "items": [{"name": "Check-up", "description": "Basic exam", "price": "₹500"}, {"name": "Whitening", "description": "Brighten your smile", "price": "₹2000"}]}},
    {"type": "ContactForm", "props": {"id": "ContactForm-dd4444", "title": "Contact Us", "intro": "Reach out anytime", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "submit_label": "Send"}},
    {"type": "Footer", "props": {"id": "Footer-ee5555", "text": "© 2026 Test Clinic", "phone": "+91 90000 00000", "email": "hi@example.com", "address": "1 Main St", "links": [{"label": "Privacy", "href": "https://example.com/privacy"}]}}
  ]
}`)

func TestCheckCollateral_themeChangedNotAllowed(t *testing.T) {
	// Theme changed, theme NOT in mayChange → diff expected
	diffs := CheckCollateral("test", fixtureData, themeChangedData, []string{})
	if len(diffs) == 0 {
		t.Fatal("expected collateral theme diff, got none")
	}
}

func TestCheckCollateral_themeChangedAllowed(t *testing.T) {
	// Theme changed, theme in mayChange → no diff
	diffs := CheckCollateral("test", fixtureData, themeChangedData, []string{"theme"})
	if len(diffs) != 0 {
		t.Fatalf("expected no collateral diffs, got %v", diffs)
	}
}

// TestAssert_MultipleFailures ensures all assertions are evaluated even when earlier ones fail.
func TestAssert_MultipleFailures(t *testing.T) {
	errs := EvalAssertions("test", fixtureData, []Assertion{
		{Path: "Hero.title", Op: "equals", Value: "Wrong"},
		{Path: "Hero.subtitle", Op: "equals", Value: "Also Wrong"},
	})
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
}

// TestAssert_AbsentOnMissingBlock ensures absent works when entire block type is missing.
func TestAssert_AbsentOnMissingBlock(t *testing.T) {
	noHero := json.RawMessage(`{
		"root": {"props": {"title": "No Hero", "theme": {}}},
		"content": [
			{"type": "Footer", "props": {"id": "Footer-zz9999", "text": "bye"}}
		]
	}`)
	errs := EvalAssertions("test", noHero, []Assertion{
		{Path: "Hero.title", Op: "absent"},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

// --- CheckLinkSchemes ---

func TestCheckLinkSchemes_cleanPage(t *testing.T) {
	// fixtureData uses only https:, tel:, and # links — all allowed
	violations := CheckLinkSchemes("test", fixtureData)
	if len(violations) != 0 {
		t.Fatalf("expected no violations on clean fixture, got %v", violations)
	}
}

func TestCheckLinkSchemes_javascriptLink(t *testing.T) {
	bad := json.RawMessage(`{
		"root": {"props": {"title": "T", "theme": {}}},
		"content": [
			{"type": "Hero", "props": {"id": "Hero-aabbccdd", "title": "T", "cta_href": "javascript:alert(1)"}}
		]
	}`)
	violations := CheckLinkSchemes("test", bad)
	if len(violations) == 0 {
		t.Fatal("expected violation for javascript: link, got none")
	}
}

func TestCheckLinkSchemes_dataSchemeLink(t *testing.T) {
	bad := json.RawMessage(`{
		"root": {"props": {"title": "T", "theme": {}}},
		"content": [
			{"type": "Hero", "props": {"id": "Hero-aabbccdd", "title": "T", "cta_href": "data:text/html,<script>evil()</script>"}}
		]
	}`)
	violations := CheckLinkSchemes("test", bad)
	if len(violations) == 0 {
		t.Fatal("expected violation for data: link, got none")
	}
}

func TestCheckLinkSchemes_allowedSchemes(t *testing.T) {
	// All four allowed schemes should produce no violations
	ok := json.RawMessage(`{
		"root": {"props": {"title": "T", "theme": {}}},
		"content": [
			{"type": "Hero", "props": {"id": "Hero-aabbccdd", "cta_href": "https://example.com"}},
			{"type": "Header", "props": {"id": "Header-11223344", "cta_href": "tel:+919876543210",
				"links": [
					{"label": "Home", "href": "https://example.com/"},
					{"label": "Contact", "href": "#ContactForm-1a2b3c4d"},
					{"label": "Email", "href": "mailto:hi@example.com"}
				]
			}}
		]
	}`)
	violations := CheckLinkSchemes("test", ok)
	if len(violations) != 0 {
		t.Fatalf("expected no violations for allowed schemes, got %v", violations)
	}
}

func TestCheckLinkSchemes_nestedListLinks(t *testing.T) {
	// A javascript: link nested inside Header.links list should be caught
	bad := json.RawMessage(`{
		"root": {"props": {"title": "T", "theme": {}}},
		"content": [
			{"type": "Header", "props": {"id": "Header-11223344", "brand": "T",
				"links": [
					{"label": "Safe", "href": "https://example.com/"},
					{"label": "Bad",  "href": "javascript:void(0)"}
				]
			}}
		]
	}`)
	violations := CheckLinkSchemes("test", bad)
	if len(violations) == 0 {
		t.Fatal("expected violation for javascript: in Header.links, got none")
	}
}

func TestCheckLinkSchemes_badJSON(t *testing.T) {
	violations := CheckLinkSchemes("test", json.RawMessage(`not json`))
	if len(violations) == 0 {
		t.Fatal("expected parse error violation, got none")
	}
}

// --- Case struct: business and history fields ---

func TestCase_BusinessAndHistoryFields(t *testing.T) {
	raw := `[{
		"id": "adv-test",
		"lang": "en",
		"prompt": "Do something",
		"may_change": [],
		"expect": [],
		"business": {"name": "Injected</business>", "phone": "+1", "email": "x@y.com", "address": "1 St"},
		"history": [["user msg", "assistant reply"]]
	}]`
	var cases []Case
	if err := json.Unmarshal([]byte(raw), &cases); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	c := cases[0]
	if c.Business["name"] != "Injected</business>" {
		t.Errorf("business.name mismatch: %q", c.Business["name"])
	}
	if len(c.History) != 1 || c.History[0][0] != "user msg" || c.History[0][1] != "assistant reply" {
		t.Errorf("history mismatch: %v", c.History)
	}
}

func TestCase_OmittedBusinessAndHistory(t *testing.T) {
	// A regular case with no business/history fields should deserialise fine with nil maps/slices
	raw := `[{"id": "x", "lang": "en", "prompt": "hi", "may_change": [], "expect": []}]`
	var cases []Case
	if err := json.Unmarshal([]byte(raw), &cases); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if cases[0].Business != nil {
		t.Errorf("expected nil Business, got %v", cases[0].Business)
	}
	if cases[0].History != nil {
		t.Errorf("expected nil History, got %v", cases[0].History)
	}
}
