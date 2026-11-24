package sites

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

type abuseFixture struct {
	ID               string           `json:"id"`
	Business         string           `json:"business_name"`
	Age              int              `json:"workspace_age_days"`
	Site             map[string]any   `json:"site"`
	Posts            []map[string]any `json:"posts"`
	Min, Max         *int
	ScoreMin         int      `json:"expected_score_min"`
	ScoreMax         int      `json:"expected_score_max"`
	ExpectedTriggers []string `json:"expected_rules_triggered"`
}

func loadAbuseFixtures(t *testing.T) (benign, abusive []abuseFixture) {
	t.Helper()
	b, err := os.ReadFile("testdata/abuse_sites.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Benign, Abusive []abuseFixture }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Benign, f.Abusive
}

func scanFixture(f abuseFixture) AbuseResult {
	title := abStr(asMap(asMap(f.Site["root"])["props"])["title"])
	return abScanData(f.Site, nil, nil, AbuseSubject{SiteName: title, RootTitle: title, BusinessName: f.Business, AgeDays: f.Age})
}

func triggered(r AbuseResult) []string {
	var out []string
	for k, v := range r.Rules {
		if v > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Forty sites, none of which may be flagged by accident or missed: the engine gives every benign site fewer points than
// the threshold and no rule at all, and every abusive one the threshold and the rules the fixture names.
func TestAbuseFixtures(t *testing.T) {
	benign, abusive := loadAbuseFixtures(t)
	th := abRules().Threshold
	for _, f := range benign {
		r := scanFixture(f)
		if r.Score >= th || strings.Join(triggered(r), ",") != strings.Join(f.ExpectedTriggers, ",") {
			t.Errorf("benign %s: score %d rules %v signals %v", f.ID, r.Score, triggered(r), r.Signals)
		}
	}
	for _, f := range abusive {
		r := scanFixture(f)
		if r.Score < th {
			t.Errorf("abusive %s: score %d < %d rules %v", f.ID, r.Score, th, r.Rules)
		}
		for _, want := range f.ExpectedTriggers {
			if r.Rules[want] == 0 {
				t.Errorf("abusive %s: rule %s not triggered (%v)", f.ID, want, r.Rules)
			}
		}
	}
}

func TestAbuseNormalisation(t *testing.T) {
	site := func(sub string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "About", "props": map[string]any{"id": "About-001", "body": sub}}}}
	}
	for name, tc := range map[string]struct {
		text string
		rule string
		want bool
	}{
		"full-width letters":     {"ＬＯＧＩＮ with your ＰＡＳＳＷＯＲＤ", "R1", true},
		"mixed case":             {"Enter Your PaSsWoRd", "R1", true},
		"whole words only":       {"We love pinecones and logins-free tea", "R1", false},
		"zero width inside":      {"pass\u200bword", "R1", true}, // the joiner is removed: it is the word password
		"telugu with joiner":     {"దయచేసి పాస్‌వర్డ్ ఇవ్వండి", "R1", true},
		"hindi":                  {"अपना पासवर्ड बताएं", "R1", true},
		"punctuation between":    {"100% returns, guaranteed!", "R5", true},
		"transliteration":        {"paisa doguna karein", "R5", true},
		"clean text":             {"We repair watches since 1990.", "R5", false},
		"punycode link in text":  {"see https://xn--pypal-4ve.com/x", "R7", true},
		"shortener link in text": {"see https://bit.ly/abc", "R3", true},
	} {
		r := abScanData(site(tc.text), nil, nil, AbuseSubject{})
		got := r.Rules[tc.rule] > 0
		if got != tc.want {
			t.Errorf("%s: %s = %v want %v (%v)", name, tc.rule, got, tc.want, r.Rules)
		}
	}
}

func TestAbuseScopeCoversEverything(t *testing.T) {
	d := map[string]any{
		"root":    map[string]any{"props": map[string]any{"title": "Shop"}},
		"content": []any{map[string]any{"type": "WhatsAppButton", "props": map[string]any{"id": "W-1", "phone": "9876543210", "label": "x", "message": "hi"}}},
		"pages": []any{map[string]any{"slug": "about", "title": "About", "i18n": map[string]any{"hi": map[string]any{"Text-1": map[string]any{"body": map[string]any{"text": "अपना पासवर्ड बताएं https://bit.ly/zz", "fp": "ab"}}}},
			"content": []any{map[string]any{"type": "Embed", "props": map[string]any{"id": "Embed-1", "provider": "youtube", "source": "dQw4w9WgXcQ"}}}}},
	}
	posts := []abPost{{Slug: "p1", Title: "Post", Body: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"guaranteed returns on usdt","marks":[{"type":"link","attrs":{"href":"https://cutt.ly/q"}}]}]}]}`)}}
	cat := json.RawMessage(`[{"name":"Double your money","id":"x"}]`)
	r := abScanData(d, posts, cat, AbuseSubject{})
	joined := strings.Join(r.Links, " ")
	for _, want := range []string{"https://wa.me/919876543210", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", "https://bit.ly/zz", "https://cutt.ly/q"} {
		if !strings.Contains(joined, want) {
			t.Errorf("link %s missing from %v", want, r.Links)
		}
	}
	if r.Rules["R1"] == 0 || r.Rules["R3"] != 50 || r.Rules["R5"] < 50 {
		t.Errorf("translation overlay, post and catalogue are scanned: %v", r.Rules)
	}
	locs := map[string]bool{}
	for _, s := range r.Signals {
		locs[strings.SplitN(s.Location, "/", 2)[0]] = true
		if n := len([]rune(s.Snippet)); n > 200 {
			t.Errorf("snippet %d runes", n)
		}
	}
	for _, want := range []string{"page:about", "post:p1", "catalogue"} {
		if !locs[want] {
			t.Errorf("no signal located in %s: %v", want, locs)
		}
	}
}

func TestAbuseReportPointsCapped(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 10, 2: 20, 3: 20, 50: 20} {
		if got := abReportPts(n); got != want {
			t.Errorf("%d reports = %d points want %d", n, got, want)
		}
	}
}

func TestAbuseRulesFile(t *testing.T) {
	e := abRules()
	if e.Threshold != 50 || e.r["R9"] == nil || e.r["R8"].MaxPoints != 20 {
		t.Fatalf("rules: threshold %d", e.Threshold)
	}
	if strings.Contains(strings.ToLower(string(abuseRulesJSON)), "auto-takedown") {
		t.Error("the rules must not promise an automatic takedown")
	}
}
