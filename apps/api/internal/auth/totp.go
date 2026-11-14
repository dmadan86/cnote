package auth

// TOTP (RFC 6238 over RFC 4226 HOTP, SHA-1, 30 s steps, 6 digits), the profile every authenticator app supports.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; authenticator apps ignore other algorithms
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const totpPeriod = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() ([]byte, error) {
	s := make([]byte, 20)
	_, err := rand.Read(s)
	return s, err
}

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[o:o+4])&0x7fffffff)%1_000_000)
}

// totpMatch returns the step whose code equals code, looking one step either side of now (clock drift). Every step is
// compared, in constant time, so timing does not say which one matched.
func totpMatch(secret []byte, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	cur, hit, ok := now.Unix()/totpPeriod, int64(0), false
	for step := cur - 1; step <= cur+1; step++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			hit, ok = step, true
		}
	}
	return hit, ok
}

// OTPAuthURI is the otpauth:// link authenticator apps read from the QR code.
func OTPAuthURI(secret []byte, email string) string {
	q := url.Values{"secret": {b32.EncodeToString(secret)}, "issuer": {"C Note"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape("C Note:"+email) + "?" + q.Encode()
}

func normCode(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(s))
}
