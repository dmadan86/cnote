package server_test

import (
	"io"
	"math/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/cnote/api/internal/serdejson"
)

// Rust's body rejection text for malformed JSON is the contract; the Go parser must reproduce it for every input.
func TestSerdeJSONErrorsMatchRust(t *testing.T) {
	e := newEnv(t)
	url := e.rust + "/api/w/00000000-0000-4000-8000-000000000001/commands/note.get"
	ask := func(body string) string {
		req, _ := http.NewRequest("POST", url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode == 400 && strings.HasPrefix(string(b), "Failed to parse the request body as JSON: ") {
			return strings.TrimPrefix(string(b), "Failed to parse the request body as JSON: ")
		}
		return ""
	}
	seeds := []string{`{"a":[1,2.5,-3e2,"xé\n",true,false,null,{"b":{}}],"c":"d"}`, `[{"k":"v"},[],[[1]],"😀"]`, `{"id":"x","list":[0,10,-0.5E+3]}`}
	alphabet := []byte(`{}[]",:\ -+.eE0123456789ntrufalse` + "\n\t\x01\x7f")
	corpus := []string{"", " ", "{", "[", "\"", "{bad", "[1,]", `{"a":1,}`, `"\ud800"`, `"\udc00"`, `"\ud800A"`, `"\ud800\ud800"`, `"\u00zz"`, `"\u12"`, `"\x"`,
		"1e999", "-1e999", "123456789012345678901234567890", "-", "01", "1.", "1.e3", "1e", "1e+", "nul", "nulx", "tru", "falsy", "+1", ".5", "[1 2]", `{"a" 1}`, `{"a":1 "b":2}`, `{"a"}`, `{"a":}`, `{1:2}`,
		"1 2", "{}x", "\"abc", "\"a\nb\"", "\"a\x01b\"", `{"a":{"b":[1,{"c":nul}]}}`, `{"a":[1,2,]}`, `{"k.x":[{"z":tru}]}`, `{"é":[x]}`, "\xff", "\"\xff\"", `["\xff"]`,
		strings.Repeat("[", 127) + "1" + strings.Repeat("]", 127), strings.Repeat("[", 128) + "1", strings.Repeat("[", 200), strings.Repeat(`{"a":`, 127) + "1" + strings.Repeat("}", 127), strings.Repeat(`{"a":`, 128) + "1"}
	rng := rand.New(rand.NewSource(7))
	for len(corpus) < 5000 {
		b := []byte(seeds[rng.Intn(len(seeds))])
		for n := rng.Intn(3) + 1; n > 0; n-- {
			switch i := rng.Intn(len(b) + 1); rng.Intn(4) {
			case 0:
				b = b[:i]
			case 1:
				if i < len(b) {
					b = append(b[:i], b[i+1:]...)
				}
			case 2:
				b = append(b[:i], append([]byte{alphabet[rng.Intn(len(alphabet))]}, b[i:]...)...)
			default:
				if i < len(b) {
					b[i] = alphabet[rng.Intn(len(alphabet))]
				}
			}
		}
		corpus = append(corpus, string(b))
	}
	bad := 0
	for _, in := range corpus {
		want := ask(in)
		got := ""
		if err := serdejson.Check([]byte(in), true); err != nil {
			got = err.Error()
		}
		if got != want {
			bad++
			if bad <= 25 {
				t.Errorf("input %q\n  rust: %q\n  go:   %q", in, want, got)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d differ", bad, len(corpus))
	}
}
