package command

import (
	"testing"
	"time"
)

func TestTsMatchesChronoAutoSi(t *testing.T) {
	base := time.Date(2026, 11, 14, 7, 15, 43, 0, time.UTC)
	for ns, want := range map[int]string{
		0:           `"2026-11-14T07:15:43Z"`,
		398000000:   `"2026-11-14T07:15:43.398Z"`,
		398176000:   `"2026-11-14T07:15:43.398176Z"`,
		398100000:   `"2026-11-14T07:15:43.398100Z"`, // micros precision from Postgres keeps 6 digits
		398176123:   `"2026-11-14T07:15:43.398176123Z"`,
		1000:        `"2026-11-14T07:15:43.000001Z"`,
		500_000_000: `"2026-11-14T07:15:43.500Z"`,
	} {
		got, _ := Ts(base.Add(time.Duration(ns))).MarshalJSON()
		if string(got) != want {
			t.Errorf("ns=%d: got %s want %s", ns, got, want)
		}
	}
	// a non-UTC zone is converted
	loc := time.FixedZone("x", 5*3600)
	if got, _ := Ts(base.In(loc)).MarshalJSON(); string(got) != `"2026-11-14T07:15:43Z"` {
		t.Errorf("zone: %s", got)
	}
}

func TestNormSortsKeysAndKeepsNumbers(t *testing.T) {
	got, err := Norm([]byte(`{"b": 1.0, "a": {"z": [1, {"y":2,"x":"<&>"}], "c": null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":{"c":null,"z":[1,{"x":"<&>","y":2}]},"b":1.0}`; string(got) != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestStatuses(t *testing.T) {
	for c, want := range map[Code]int{Unauthenticated: 401, Forbidden: 403, NotFound: 404, Invalid: 400, Conflict: 409, RateLimited: 429, PlanLimit: 402, Internal: 500} {
		if c.Status() != want {
			t.Errorf("%s: %d", c, c.Status())
		}
	}
}

func TestRawLineSeparators(t *testing.T) {
	got, _ := Marshal(map[string]string{"a": "x\u2028y\u2029z", "b": `\u2028 literal`})
	if want := "{\"a\":\"x\u2028y\u2029z\",\"b\":\"\\\\u2028 literal\"}"; string(got) != want {
		t.Errorf("got %s want %s", got, want)
	}
}
