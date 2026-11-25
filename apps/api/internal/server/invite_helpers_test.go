package server_test

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

func b64url(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func sleep100() { time.Sleep(100 * time.Millisecond) }

func jsonArr(b []byte) []any {
	var out []any
	_ = json.Unmarshal(b, &out)
	return out
}
