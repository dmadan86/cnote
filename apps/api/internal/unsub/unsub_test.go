package unsub

import (
	"strings"
	"testing"
)

const (
	ws = "00112233-4455-6677-8899-aabbccddeeff"
	u1 = "11111111-2222-3333-4444-555555555555"
	u2 = "11111111-2222-3333-4444-555555555556"
)

func TestTokenRoundTripAndTamper(t *testing.T) {
	t.Setenv("CNOTE_MASTER_KEY", "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=")
	tok, err := Token(ws, u1, Digest)
	if err != nil {
		t.Fatal(err)
	}
	w, u, k, err := Parse(tok)
	if err != nil || w != ws || u != u1 || k != Digest {
		t.Fatalf("round trip: %v %v %v %v", w, u, k, err)
	}
	other, _ := Token(ws, u2, Digest)
	if tok == other {
		t.Fatal("tokens for two people must differ")
	}
	// swap the person bytes but keep the first tag: must fail
	bad := other[:44] + tok[44:]
	if _, _, _, err := Parse(bad); err == nil {
		t.Error("an edited token must not parse")
	}
	if _, _, _, err := Parse(tok[:len(tok)-2] + "AA"); err == nil {
		t.Error("a bad tag must not parse")
	}
	if _, _, _, err := Parse(strings.Repeat("A", 66)); err == nil {
		t.Error("garbage must not parse")
	}
	if _, err := Token(ws, u1, "everything"); err == nil {
		t.Error("unknown kind")
	}
	t.Setenv("CNOTE_MASTER_KEY", "")
	if _, err := Token(ws, u1, Direct); err == nil {
		t.Error("no master key, no link")
	}
}
