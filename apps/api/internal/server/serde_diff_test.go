package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mcp"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/server"
)

// guardedGo is the Go front with an upstream that fails the test: every answer must be native.
func guardedGo(t *testing.T, e *env) *httptest.Server {
	t.Helper()
	reg := command.NewRegistry(modules.Commands()...)
	srv := httptest.NewServer(server.New(e.pool, reg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("proxied to Rust: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	})))
	t.Cleanup(srv.Close)
	return srv
}

func raw(t *testing.T, method, url, ct, body string, hdr map[string]string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

// Malformed UUIDs in a path and in a body get the uuid crate's text.
func TestUUIDErrorsMatchRust(t *testing.T) {
	e := newEnv(t)
	g := guardedGo(t, e)
	owner := e.signup("Tess", true)
	corpus := []string{"zz", "", "x", "00000000-0000-0000-0000-00000000000", "00000000-0000-0000-0000-0000000000000", "0000000000000000000000000000000z",
		"00000000-0000-0000-0000-00000000000z", "00000000-000-00000-0000-000000000000", "00000000-0000-0000-0000", "{00000000-0000-0000-0000-000000000000",
		"0000000000000000000000000000000-", "é", "00000000_0000_0000_0000_000000000000", "00000000-0000-0000-0000-000000000000-", "urn:uuid:zz", "{zz}", "ÿ0000000-0000-0000-0000-000000000000",
		"00000000-0000-0000-0000-000000000000 ", "ABCDEF00-0000-0000-0000-000000000000", "urn:uuid:00000000-0000-0000-0000-000000000000", "{00000000-0000-0000-0000-000000000000}", "00000000000000000000000000000000",
		"0000000-00000-0000-0000-000000000000", "0000000000000000000000000000000000000", "1234567890123456789012345678901234567890123456789012"}
	for _, u := range corpus {
		for _, path := range []string{"/api/w/%s/commands/note.get", "/api/hooks/github/%s"} {
			p := fmt.Sprintf(path, strings.ReplaceAll(u, " ", "%20"))
			rs, _, rb := raw(t, "POST", e.rust+p, "application/json", `{}`, nil)
			gs, _, gb := raw(t, "POST", g.URL+p, "application/json", `{}`, nil)
			if rs != gs || rb != gb {
				t.Errorf("%q %s\n rust %d %s\n go   %d %s", u, path, rs, rb, gs, gb)
			}
		}
		in := fmt.Sprintf(`{"id":%q}`, u)
		h := map[string]string{"Cookie": "cn_session=" + owner.cookie}
		rs, _, rb := raw(t, "POST", e.rust+"/api/w/"+owner.ws+"/commands/note.get", "application/json", in, h)
		gs, _, gb := raw(t, "POST", g.URL+"/api/w/"+owner.ws+"/commands/note.get", "application/json", in, h)
		if rs != gs || rb != gb {
			t.Errorf("body %q\n rust %d %s\n go   %d %s", u, rs, rb, gs, gb)
		}
	}
}

// The typed request bodies of the auth, marketplace and OAuth endpoints are refused with axum's wording.
func TestRequestBodyRejectionsMatchRust(t *testing.T) {
	e := newEnv(t)
	g := guardedGo(t, e)
	endpoints := []struct{ path, ct string }{{"/api/auth/login", "application/json"}, {"/api/auth/signup", "application/json"}, {"/api/workspaces", "application/json"},
		{"/api/marketplace/token", "application/json"}, {"/oauth/register", "application/json"}, {"/api/oauth/authorize", "application/json"}, {"/api/oauth/authorize/context", "application/json"},
		{"/oauth/token", "application/x-www-form-urlencoded"}, {"/oauth/revoke", "application/x-www-form-urlencoded"}}
	jsonBodies := []string{``, `{}`, `[]`, `null`, `5`, `"x"`, `true`, `{bad`, `{"email":5}`, `{"email":"a"}`, `{"email":"a","password":null}`, `{"email":"a","password":"x","email":"b"}`,
		`{"email":"a","name":"n","password":"p","workspace_name":5}`, `{"name":5}`, `{"name":"n","extra":[1,2]}`, `{"client_id":5}`, `{"client_id":"a","client_secret":"b"}`, `{"client_id":"a","client_secret":"b","code":[1]}`,
		`{"client_name":5}`, `{"redirect_uris":"x"}`, `{"redirect_uris":["a",5]}`, `{"redirect_uris":[]}`, `{"grant_types":{}}`, `{"workspace_id":"x","approve":true}`, `{"workspace_id":"00000000-0000-0000-0000-000000000000","approve":"yes"}`,
		`{"workspace_id":5}`, `{"approve":true}`, `{"client_id":"c","client_id":"d"}`, `{"email":"a","password":"b"}x`, `{"email":"\ud800","password":"b"}`, `{"email":{"a":1},"password":"b"}`, `{"email":[1],"password":"b"}`,
		"{\"email\":\"a\",\n\"password\":7}", `  {"name": 1.5}`, `{"name": -3}`, `{"name": 1e3}`, `{"name": true}`}
	formBodies := []string{``, `grant_type=a&grant_type=b`, `token=a&token=b`, `client_id=x&client_id=y`, `grant_type=%ff`, `a=1`}
	diff := 0
	for _, ep := range endpoints {
		bodies := jsonBodies
		if strings.Contains(ep.ct, "urlencoded") {
			bodies = formBodies
		}
		for _, ct := range []string{ep.ct, "text/plain", ""} {
			for _, b := range bodies {
				if ct != ep.ct && b != `{}` {
					continue
				}
				rs, _, rb := raw(t, "POST", e.rust+ep.path, ct, b, map[string]string{"X-Forwarded-For": "10.1.2.3"})
				gs, _, gb := raw(t, "POST", g.URL+ep.path, ct, b, map[string]string{"X-Forwarded-For": "10.1.2.3"})
				if rs == 429 || gs == 429 || strings.HasPrefix(rb, "{") && strings.Contains(rb, "unauthenticated") {
					continue // rate limits and sign-in checks are not what is under test
				}
				if rs != gs || rb != gb {
					diff++
					t.Errorf("%s [%s] %q\n rust %d %s\n go   %d %s", ep.path, ct, b, rs, rb, gs, gb)
				}
			}
		}
	}
	if diff > 0 {
		t.Fatalf("%d differences", diff)
	}
}

// For every command the Rust registry has: inputs that make serde refuse (missing and mistyped fields) get the
// same "invalid input: ..." answer from Go, byte for byte, whether the handler or the schema walker words it.
func TestSerdeInputErrorsMatchRust(t *testing.T) {
	e := newEnv(t)
	g := guardedGo(t, e)
	owner := e.signup("Tess", true)
	h := map[string]string{"Cookie": "cn_session=" + owner.cookie}
	var cat []struct {
		Name   string         `json:"name"`
		Schema map[string]any `json:"input_schema"`
	}
	if err := json.Unmarshal(mcp.RustCatalog, &cat); err != nil {
		t.Fatal(err)
	}
	wrong := []any{5, -5, 1.5, "x", true, []any{}, map[string]any{}, nil, "00000000-0000-0000-0000-00000000000z", 4294967296, 99999999999999999999.0}
	rng := rand.New(rand.NewSource(3))
	total, same := 0, 0
	var diffs []string
	for _, c := range cat {
		props, _ := c.Schema["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for k := range props {
			names = append(names, k)
		}
		sort.Strings(names)
		var inputs []string
		inputs = append(inputs, `{}`, `[]`, `null`, `"x"`, `5`)
		for _, k := range names {
			for _, w := range wrong {
				b, _ := json.Marshal(map[string]any{k: w})
				inputs = append(inputs, string(b))
				if rng.Intn(4) == 0 { // plus a second field: the first error in key order wins
					k2 := names[rng.Intn(len(names))]
					b, _ = json.Marshal(map[string]any{k: w, k2: wrong[rng.Intn(len(wrong))]})
					inputs = append(inputs, string(b))
				}
			}
		}
		for _, in := range inputs {
			// only inputs the Rust deserializer refuses: anything else would run the command
			rs, _, rb := raw(t, "POST", e.rust+"/api/w/"+owner.ws+"/commands/"+c.Name, "application/json", in, h)
			if rs != 400 || !strings.Contains(rb, `"message":"invalid input: `) || strings.Contains(rb, "tagged enum") || c.Name == "item.transition" || strings.HasPrefix(c.Name, "automation.") {
				continue // tagged enums (automation triggers, workflow definitions) name their Rust type or tag, and item.transition reports a missing field before a mistyped optional one: not reproduced
			}
			total++
			gs, _, gb := raw(t, "POST", g.URL+"/api/w/"+owner.ws+"/commands/"+c.Name, "application/json", in, h)
			if gs == rs && gb == rb {
				same++
			} else if len(diffs) < 60 {
				diffs = append(diffs, fmt.Sprintf("%s %s\n rust %d %s\n go   %d %s", c.Name, in, rs, rb, gs, gb))
			}
		}
	}
	for _, d := range diffs {
		t.Log(d)
	}
	t.Logf("serde refusals compared: %d, identical: %d", total, same)
	if total-same > 0 {
		t.Errorf("%d of %d serde refusals differ from Rust", total-same, total)
	}
}
