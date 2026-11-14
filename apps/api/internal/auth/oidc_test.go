package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

const (
	tIss   = "https://idp.example.test"
	tAud   = "client-1"
	tNonce = "nonce-1"
)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func jwkOf(k *rsa.PrivateKey, kid string) jwk {
	return jwk{Kty: "RSA", Kid: kid, Use: "sig", N: b64u(k.N.Bytes()), E: b64u(big.NewInt(int64(k.E)).Bytes())}
}

func sign(t *testing.T, k *rsa.PrivateKey, hdr, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(hdr)
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func good(now time.Time) map[string]any {
	return map[string]any{"iss": tIss, "aud": tAud, "sub": "u1", "exp": now.Add(time.Hour).Unix(), "nonce": tNonce, "email": "a@corp.test", "email_verified": true}
}

func TestVerifyIDToken(t *testing.T) {
	now := time.Now()
	k, other := newKey(t), newKey(t)
	keys := []jwk{jwkOf(k, "k1")}
	hdr := map[string]any{"alg": "RS256", "kid": "k1"}
	with := func(over map[string]any) map[string]any {
		c := good(now)
		for key, v := range over {
			if v == nil {
				delete(c, key)
			} else {
				c[key] = v
			}
		}
		return c
	}
	if c, err := verifyIDToken(sign(t, k, hdr, good(now)), keys, tIss, tAud, tNonce, now); err != nil || c.Email != "a@corp.test" || !c.verified() {
		t.Fatalf("valid token refused: %v", err)
	}
	for name, tc := range map[string]struct {
		tok  string
		keys []jwk
	}{
		"bad signature (other key)": {sign(t, other, hdr, good(now)), keys},
		"unknown kid":               {sign(t, k, map[string]any{"alg": "RS256", "kid": "zz"}, good(now)), keys},
		"no keys":                   {sign(t, k, hdr, good(now)), nil},
		"alg none":                  {sign(t, k, map[string]any{"alg": "none"}, good(now)), keys},
		"alg HS256":                 {sign(t, k, map[string]any{"alg": "HS256", "kid": "k1"}, good(now)), keys},
		"wrong issuer":              {sign(t, k, hdr, with(map[string]any{"iss": "https://evil.example.test"})), keys},
		"wrong audience":            {sign(t, k, hdr, with(map[string]any{"aud": "other-client"})), keys},
		"audience list without azp": {sign(t, k, hdr, with(map[string]any{"aud": []string{tAud, "x"}})), keys},
		"expired":                   {sign(t, k, hdr, with(map[string]any{"exp": now.Add(-time.Hour).Unix()})), keys},
		"no exp":                    {sign(t, k, hdr, with(map[string]any{"exp": nil})), keys},
		"not yet valid":             {sign(t, k, hdr, with(map[string]any{"nbf": now.Add(time.Hour).Unix()})), keys},
		"wrong nonce":               {sign(t, k, hdr, with(map[string]any{"nonce": "replayed"})), keys},
		"missing nonce":             {sign(t, k, hdr, with(map[string]any{"nonce": nil})), keys},
		"tampered payload":          {tamper(t, sign(t, k, hdr, good(now))), keys},
		"garbage":                   {"a.b", keys},
	} {
		if _, err := verifyIDToken(tc.tok, tc.keys, tIss, tAud, tNonce, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a list audience naming us with a matching azp is fine; email_verified as the string "true" counts, "false" and absent do not
	if c, err := verifyIDToken(sign(t, k, hdr, with(map[string]any{"aud": []string{tAud, "x"}, "azp": tAud})), keys, tIss, tAud, tNonce, now); err != nil || !c.verified() {
		t.Errorf("aud list with azp: %v", err)
	}
	for v, want := range map[any]bool{"true": true, "false": false, nil: false, true: true, false: false} {
		c, err := verifyIDToken(sign(t, k, hdr, with(map[string]any{"email_verified": v})), keys, tIss, tAud, tNonce, now)
		if err != nil || c.verified() != want {
			t.Errorf("email_verified=%v: verified=%v err=%v", v, c.verified(), err)
		}
	}
}

func tamper(t *testing.T, tok string) string {
	t.Helper()
	b := []byte(tok)
	i := len(tok) / 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestValidDomainRefusesPublicMailboxProviders(t *testing.T) {
	for d, want := range map[string]bool{"corp.test": true, "Corp.Test": true, "gmail.com": false, "outlook.com": false, "nodot": false, "a@b.test": false, "": false} {
		if ValidDomain(d) != want {
			t.Errorf("%q: want %v", d, want)
		}
	}
}
