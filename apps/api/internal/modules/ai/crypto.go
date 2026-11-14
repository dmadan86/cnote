package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"

	"github.com/cnote/api/internal/command"
)

// AES-256-GCM envelope for BYO provider and hook secrets: nonce(12) || ciphertext, workspace id as AAD
// (crates/cnote-ai/src/crypto.rs), so Rust and Go read each other's rows.

const nonceLen = 12

// MasterKey reads CNOTE_MASTER_KEY (base64, 32 bytes).
func MasterKey() ([]byte, error) {
	raw := os.Getenv("CNOTE_MASTER_KEY")
	if strings.TrimSpace(raw) == "" {
		return nil, command.ErrInvalid("CNOTE_MASTER_KEY is not set; refusing to store provider keys")
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(k) != 32 {
		return nil, command.ErrInvalid("CNOTE_MASTER_KEY must be 32 bytes, base64 encoded")
	}
	return k, nil
}

func aad(ws string) []byte {
	b, _ := hex.DecodeString(strings.ReplaceAll(ws, "-", ""))
	return b
}

func gcm(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func Encrypt(key []byte, ws, plaintext string) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, command.NewError(command.Internal, "encryption failed")
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, command.NewError(command.Internal, "encryption failed")
	}
	return g.Seal(nonce, nonce, []byte(plaintext), aad(ws)), nil
}

func Decrypt(key []byte, ws string, blob []byte) (string, error) {
	bad := command.NewError(command.Internal, "cannot decrypt the workspace provider key (wrong master key?)")
	if len(blob) <= nonceLen {
		return "", bad
	}
	g, err := gcm(key)
	if err != nil {
		return "", bad
	}
	pt, err := g.Open(nil, blob[:nonceLen], blob[nonceLen:], aad(ws))
	if err != nil {
		return "", bad
	}
	return string(pt), nil // Rust refuses non-UTF-8 plaintext; only this code writes these blobs
}
