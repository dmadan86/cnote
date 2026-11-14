// Package secretbox is the AES-256-GCM envelope for stored secrets (port of crates/cnote-ai/src/crypto.rs).
// Blob = nonce(12) || ciphertext; the workspace uuid (16 raw bytes) is the AAD, so a blob copied to
// another workspace's row does not decrypt.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
)

const nonceLen = 12

// MasterKey reads CNOTE_MASTER_KEY (base64, 32 bytes) at call time.
func MasterKey() ([]byte, error) {
	raw := os.Getenv("CNOTE_MASTER_KEY")
	if strings.TrimSpace(raw) == "" {
		return nil, command.ErrInvalid("CNOTE_MASTER_KEY is not set; refusing to store provider keys")
	}
	return ParseKey(raw)
}

func ParseKey(b64 string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != 32 {
		return nil, command.ErrInvalid("CNOTE_MASTER_KEY must be 32 bytes, base64 encoded")
	}
	return b, nil
}

func aad(ws string) ([]byte, error) {
	b, err := hex.DecodeString(strings.ReplaceAll(ws, "-", ""))
	if err != nil || len(b) != 16 {
		return nil, command.NewError(command.Internal, "invalid workspace id")
	}
	return b, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func Encrypt(key []byte, ws, plaintext string) ([]byte, error) {
	fail := command.NewError(command.Internal, "encryption failed")
	a, err := aad(ws)
	if err != nil {
		return nil, err
	}
	g, err := gcm(key)
	if err != nil {
		return nil, fail
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fail
	}
	return g.Seal(nonce, nonce, []byte(plaintext), a), nil
}

func Decrypt(key []byte, ws string, blob []byte) (string, error) {
	bad := command.NewError(command.Internal, "cannot decrypt the workspace provider key (wrong master key?)")
	a, err := aad(ws)
	if err != nil || len(blob) <= nonceLen {
		return "", bad
	}
	g, err := gcm(key)
	if err != nil {
		return "", bad
	}
	pt, err := g.Open(nil, blob[:nonceLen], blob[nonceLen:], a)
	if err != nil || !utf8.Valid(pt) {
		return "", bad
	}
	return string(pt), nil
}
