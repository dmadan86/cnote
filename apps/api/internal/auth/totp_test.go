package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890": the 8-digit vectors end in the 6-digit codes below.
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037", 20000000000: "353130"} {
		if got := totpCode(secret, unix/30); got != want {
			t.Errorf("t=%d: %s, want %s", unix, got, want)
		}
	}
}

func TestTOTPWindowIsPlusMinusOneStep(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := now.Unix() / 30
	for _, c := range []struct {
		step int64
		ok   bool
	}{{cur - 2, false}, {cur - 1, true}, {cur, true}, {cur + 1, true}, {cur + 2, false}} {
		step, ok := totpMatch(secret, totpCode(secret, c.step), now)
		if ok != c.ok || (ok && step != c.step) {
			t.Errorf("step %+d: ok=%v step=%d", c.step-cur, ok, step)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok := totpMatch(secret, bad, now); ok && bad != "000000" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOTPAuthURIShape(t *testing.T) {
	u := OTPAuthURI([]byte("12345678901234567890"), "a+b@example.test")
	for _, want := range []string{"otpauth://totp/C%20Note:a+b@example.test?", "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "issuer=C+Note", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s missing %q", u, want)
		}
	}
}

func TestRecoveryCodeShapeAndNormalising(t *testing.T) {
	c := newRecoveryCode()
	if len(c) != 11 || c[5] != '-' || normCode(strings.ToUpper(c)) != strings.ReplaceAll(c, "-", "") || len(normCode(c)) != 10 {
		t.Fatalf("%q", c)
	}
}

// The unknown-email path must cost the same as a real check: the dummy hash carries the production argon2 parameters.
func TestDummyHashHasProductionCost(t *testing.T) {
	real, _ := HashPassword("x")
	params := func(h string) string { return strings.Split(h, "$")[3] }
	if params(dummyHash) != params(real) || !VerifyPassword("dummy password for the unknown-email path", dummyHash) || VerifyPassword("other", dummyHash) {
		t.Fatalf("dummy hash %q vs %q", dummyHash, real)
	}
}
