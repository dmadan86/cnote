package auth

import (
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestPasswordHashRoundTripAndParsing(t *testing.T) {
	pw := RandomToken(12)
	h, err := HashPassword(pw)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("%q %v", h, err)
	}
	h2, _ := HashPassword(pw)
	if h == h2 {
		t.Fatal("salt is not random")
	}
	if !VerifyPassword(pw, h) || VerifyPassword(pw+"x", h) || VerifyPassword("", h) {
		t.Fatal("verify")
	}
	// hashes made with other parameters (or argon2i) verify with the parameters stored in them
	salt := []byte("0123456789abcdef")
	enc := base64.RawStdEncoding.EncodeToString
	for alg, key := range map[string][]byte{
		"argon2id": argon2.IDKey([]byte(pw), salt, 3, 64, 2, 24),
		"argon2i":  argon2.Key([]byte(pw), salt, 3, 64, 2, 24),
	} {
		stored := "$" + alg + "$v=19$m=64,t=3,p=2$" + enc(salt) + "$" + enc(key)
		if !VerifyPassword(pw, stored) || VerifyPassword(pw+"x", stored) {
			t.Errorf("%s", alg)
		}
		if noVersion := strings.Replace(stored, "v=19$", "", 1); !VerifyPassword(pw, noVersion) {
			t.Errorf("%s without version", alg)
		}
	}
	for _, bad := range []string{"", "x", "$argon2d$v=19$m=64,t=3,p=2$" + enc(salt) + "$" + enc(salt), "$argon2id$v=16$m=64,t=3,p=2$" + enc(salt) + "$" + enc(salt),
		"$argon2id$v=19$m=64,t=3$" + enc(salt) + "$" + enc(salt), "$argon2id$v=19$m=64,t=0,p=1$" + enc(salt) + "$" + enc(salt), "$argon2id$v=19$m=0,t=1,p=1$" + enc(salt) + "$" + enc(salt),
		"$argon2id$v=19$m=64,t=3,p=300$" + enc(salt) + "$" + enc(salt), "$argon2id$v=19$m=64,t=3,p=1$!!$" + enc(salt), "$argon2id$v=19$m=64,t=3,p=1$" + enc(salt) + "$", h[:len(h)-3]} {
		if VerifyPassword(pw, bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRustLower(t *testing.T) {
	for in, want := range map[string]string{"ΑΣ": "ας", "ΑΣΑ": "ασα", "Σ": "σ", "İ": "i̇", "ΑΣ-b": "ας-b", "ΌΣΟΣ": "όσος", "Ab@C.D": "ab@c.d"} {
		if got := rustLower(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}
