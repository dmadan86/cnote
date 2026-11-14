package crm

import "testing"

// Assertions ported from crm.rs::tests.
func TestStageRules(t *testing.T) {
	for _, s := range stages {
		if err := checkStage(s); err != nil {
			t.Fatal(err)
		}
	}
	if checkStage("closed") == nil {
		t.Fatal("accepted closed")
	}
	for _, v := range []struct {
		s      string
		p      *int64
		want   int64
		closed bool
	}{{"won", ptr(int64(5)), 100, true}, {"lost", nil, 0, true}, {"proposal", ptr(int64(40)), 40, false}} {
		p, c := stageDefaults(v.s, v.p)
		if p != v.want || c != v.closed {
			t.Fatalf("%s: %d %v", v.s, p, c)
		}
	}
}
func ptr[T any](v T) *T { return &v }
func TestTaxIDs(t *testing.T) {
	g := "29ABCDE1234F1Z"
	g += string(checkChar(g))
	got, state, err := checkTaxIDs(&g, nil)
	if err != nil || got == nil || *got != g || state == nil || *state != "29" {
		t.Fatalf("%v %v %v", got, state, err)
	}
	for _, v := range []struct{ g, s *string }{{&g, ptr("27")}, {nil, ptr("00")}, {ptr("bad"), nil}} {
		if _, _, err := checkTaxIDs(v.g, v.s); err == nil {
			t.Fatal("accepted invalid tax ids")
		}
	}
	a, b, err := checkTaxIDs(nil, nil)
	if a != nil || b != nil || err != nil {
		t.Fatal("expected None,None")
	}
}
