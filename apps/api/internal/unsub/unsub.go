// Package unsub signs and checks the one-click unsubscribe links of notification mail (docs-parity-b 13.2.5, RFC 8058).
// A token names one workspace, one person and one kind of mail, and carries a 16 byte HMAC-SHA256 tag keyed by
// CNOTE_MASTER_KEY, so it needs no table, cannot be forged or edited to name someone else, and works with no login.
package unsub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/cnote/api/internal/secretbox"
)

// The kinds of mail a person can switch off on their own: direct mail (mentions, tasks, shares) and the daily digest.
const (
	Direct = "direct"
	Digest = "digest"
)

var kinds = []string{Direct, Digest}

var ErrInvalid = errors.New("invalid unsubscribe token")

func uuidBytes(s string) ([]byte, bool) {
	h := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(h)
	return b, err == nil && len(b) == 16
}

func uuidString(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func tag(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("cnote-unsubscribe-v1\x00"))
	m.Write(msg)
	return m.Sum(nil)[:16]
}

// Token returns the link token for (workspace, user, kind). It fails without CNOTE_MASTER_KEY: mail that is on by default
// must not go out without a working unsubscribe link.
func Token(ws, user, kind string) (string, error) {
	w, ok1 := uuidBytes(ws)
	u, ok2 := uuidBytes(user)
	k := -1
	for i, x := range kinds {
		if x == kind {
			k = i
		}
	}
	if !ok1 || !ok2 || k < 0 {
		return "", ErrInvalid
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return "", err
	}
	msg := append(append(append([]byte{}, w...), u...), byte(k))
	return base64.RawURLEncoding.EncodeToString(append(msg, tag(key, msg)...)), nil
}

// Parse checks the tag in constant time and returns what the token names.
func Parse(tok string) (ws, user, kind string, err error) {
	raw, derr := base64.RawURLEncoding.DecodeString(tok)
	if derr != nil || len(raw) != 33+16 || int(raw[32]) >= len(kinds) {
		return "", "", "", ErrInvalid
	}
	key, kerr := secretbox.MasterKey()
	if kerr != nil {
		return "", "", "", kerr
	}
	if !hmac.Equal(raw[33:], tag(key, raw[:33])) {
		return "", "", "", ErrInvalid
	}
	return uuidString(raw[:16]), uuidString(raw[16:32]), kinds[raw[32]], nil
}
