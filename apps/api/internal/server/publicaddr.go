package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/cnote/api/internal/secretbox"
)

// publicIPKey is the keyed hash of a client address that every public endpoint counts requests under, so counters never
// hold the address itself. It needs the master key, which every replica shares; without one there is no stable key and the
// endpoint answers 503 rather than count per process. domain separates the endpoints ("table-form-ip", "page-ip"), so one
// address has a different key in each. The public table forms and the public page links (docs-parity-b 9.1) both use it.
func publicIPKey(domain, ip string) (string, error) {
	key, err := secretbox.MasterKey()
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(domain + "\x00" + ip))
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}
