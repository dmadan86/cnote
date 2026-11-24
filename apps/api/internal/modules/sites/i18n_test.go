package sites

import (
	"encoding/json"
	"strings"
	"testing"
)

// hindi returns the fixture with langs [en, hi, ta] (ta hidden) and a Hindi overlay on the Hero title.
func hindi(t *testing.T) (map[string]any, json.RawMessage) {
	t.Helper()
	d := docOf(t, fixture(t))
	rp := rootProps(d)
	rp["langs"] = []any{"en", "hi", "ta"}
	rp["langs_hidden"] = []any{"ta"}
	u, ok := unitByKey(d, "", "Hero-00000002.title")
	if !ok {
		t.Fatal("no Hero title txUnit")
	}
	setEntry(d, "", "hi", u, "नमस्ते", 0)
	b, _ := json.Marshal(d)
	return d, b
}

func TestOverlayValidate(t *testing.T) {
	_, data := hindi(t)
	if err := Validate(data); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(d map[string]any){
		"unknown block": func(d map[string]any) {
			ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi")["Hero-0000ffff"] = map[string]any{}
		},
		"not translatable": func(d map[string]any) {
			ensureSubMap(ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi"), "Hero-00000002")["cta_href"] = map[string]any{"text": "/x", "fp": Fingerprint("a"), "r": 0.0}
		},
		"main language": func(d map[string]any) {
			ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "en")["@meta"] = map[string]any{}
		},
		"language not on site": func(d map[string]any) {
			ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "kn")["@meta"] = map[string]any{}
		},
		"over the limit": func(d map[string]any) {
			ensureSubMap(ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi"), "Hero-00000002")["title"] = map[string]any{"text": strings.Repeat("अ", 81), "fp": Fingerprint("a"), "r": 0.0}
		},
		"bad fingerprint": func(d map[string]any) {
			ensureSubMap(ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi"), "Hero-00000002")["title"] = map[string]any{"text": "x", "fp": "zz", "r": 0.0}
		},
		"extra key": func(d map[string]any) {
			ensureSubMap(ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi"), "Hero-00000002")["title"] = map[string]any{"text": "x", "fp": Fingerprint("a"), "r": 0.0, "z": 1.0}
		},
		"list too long": func(d map[string]any) {
			ensureSubMap(ensureSubMap(ensureSubMap(d["root"].(map[string]any), "i18n"), "hi"), "Services-00000003")["items"] = []any{map[string]any{}, map[string]any{}}
		},
		"hidden main":    func(d map[string]any) { rootProps(d)["langs_hidden"] = []any{"en"} },
		"unknown lang":   func(d map[string]any) { rootProps(d)["langs"] = []any{"en", "fr"} },
		"too many langs": func(d map[string]any) { rootProps(d)["langs"] = []any{"en", "hi", "ta", "te", "kn"} },
	}
	for name, mutate := range bad {
		d, _ := hindi(t)
		mutate(d)
		b, _ := json.Marshal(d)
		if err := Validate(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOverlayStaleAndPrune(t *testing.T) {
	_, data := hindi(t)
	// editing the source leaves the entry in place but stale; removing the block drops it
	edited := mustApply(t, data, "update_block_props", `{"id":"Hero-00000002","props":{"title":"New"}}`)
	d := docOf(t, edited)
	u, _ := unitByKey(d, "", "Hero-00000002.title")
	if st, text, _ := entryState(d, "", "hi", u); st != "needs-update" || text != "नमस्ते" {
		t.Fatalf("got %s %q", st, text)
	}
	gone := docOf(t, mustApply(t, data, "remove_block", `{"id":"Hero-00000002"}`))
	if _, has := rootI18n(gone)["hi"]; has {
		t.Fatalf("overlay of a removed block stayed: %v", gone["root"])
	}
}

func rootI18n(d map[string]any) map[string]any { return asMap(asMap(d["root"])["i18n"]) }

func TestListOverlayAndMeta(t *testing.T) {
	d := docOf(t, fixture(t))
	rootProps(d)["langs"] = []any{"en", "hi"}
	var names []string
	for _, u := range pageUnits(d, "") {
		names = append(names, u.key())
	}
	for _, want := range []string{"@meta.title", "Services-00000003.title", "Services-00000003.items.0.name"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Fatalf("missing txUnit %s in %v", want, names)
		}
	}
	u, _ := unitByKey(d, "", "Services-00000003.items.0.name")
	setEntry(d, "", "hi", u, "पहली सेवा", 1)
	b, _ := json.Marshal(d)
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	// the list shrinks: the entry past the end goes
	d2 := docOf(t, mustApply(t, b, "update_block_props", `{"id":"Services-00000003","props":{"items":[]}}`))
	if _, has := rootI18n(d2)["hi"]; has {
		t.Fatalf("list overlay stayed: %v", rootI18n(d2))
	}
}

func TestPublicDocHidesHiddenAndOffSwitch(t *testing.T) {
	d, data := hindi(t)
	u, _ := unitByKey(d, "", "Hero-00000002.title")
	setEntry(d, "", "ta", u, "வணக்கம்", 0)
	data, _ = json.Marshal(d)
	t.Setenv("SITES_LANGUAGES", "1")
	pub := docOf(t, PublicDoc(data))
	ov := rootI18n(pub)
	if _, ok := ov["hi"]; !ok || ov["ta"] != nil {
		t.Fatalf("overlays: %v", ov)
	}
	if rp := rootProps(pub); rp["langs_hidden"] != nil || len(rp["langs"].([]any)) != 2 {
		t.Fatalf("root: %v", rp)
	}
	if got := PublicLanguages(data)["shown"].([]string); strings.Join(got, ",") != "en,hi" {
		t.Fatalf("shown %v", got)
	}
	t.Setenv("SITES_LANGUAGES", "")
	pub = docOf(t, PublicDoc(data))
	if rootI18n(pub) != nil || rootProps(pub)["langs"] != nil {
		t.Fatalf("switch off still served translations: %v", pub["root"])
	}
}

func TestConsentForLang(t *testing.T) {
	text, ver, ok := ConsentForLang("enquiry-v1", "hi", "Sharma Dental")
	if !ok || ver != "v1-hi" || !strings.Contains(text, "Sharma Dental") || strings.Contains(text, "{site_name}") || strings.HasPrefix(text, "I agree") {
		t.Fatalf("%q %q %v", text, ver, ok)
	}
	if text, ver, _ := ConsentForLang("", "xx", "A"); ver != "v1-en" || text != "I agree that A may use these details to reply to my enquiry." {
		t.Fatalf("fallback: %q %q", text, ver)
	}
	if _, _, ok := ConsentForLang("nope", "en", "A"); ok {
		t.Fatal("unknown consent id accepted")
	}
}
