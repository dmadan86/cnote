package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"testing"
)

const ws = "00112233-4455-6677-8899-aabbccddeeff"

func randWS() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func TestRoundTripAndBinding(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	w, secret := randWS(), randWS()
	blob, err := Encrypt(key, w, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte(secret)) {
		t.Fatal("plaintext in blob")
	}
	if got, err := Decrypt(key, w, blob); err != nil || got != secret {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := Decrypt(key, randWS(), blob); err == nil {
		t.Error("AAD must bind the workspace")
	}
	if _, err := Decrypt(bytes.Repeat([]byte{8}, 32), w, blob); err == nil {
		t.Error("wrong key")
	}
	blob[len(blob)-1] ^= 1
	if _, err := Decrypt(key, w, blob); err == nil {
		t.Error("tampered blob")
	}
	if _, err := Decrypt(key, w, make([]byte, 12)); err == nil || err.Error() == "" {
		t.Error("short blob")
	}
}

// Known answer: nonce 00..0b, key 07*32, AAD = the 16 raw workspace bytes, as crates/cnote-ai/src/crypto.rs lays it out
// (nonce || ciphertext||tag). The hex was produced by this package and checked against crypto.rs by reading.
const katHex = "000102030405060708090a0b70e4851c7229ae2605de8186cb53b57811f1b5c913353e4ab6775b"

func TestKnownAnswer(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	blob, _ := hex.DecodeString(katHex)
	got, err := Decrypt(key, ws, blob)
	if err != nil || got != "hello world" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestKeyParsing(t *testing.T) {
	if _, err := ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"c2hvcnQ=", "not base64!"} {
		if _, err := ParseKey(bad); err == nil || err.Error() != "invalid: CNOTE_MASTER_KEY must be 32 bytes, base64 encoded" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	t.Setenv("CNOTE_MASTER_KEY", "  ")
	if _, err := MasterKey(); err == nil {
		t.Error("unset key must fail")
	}
}
