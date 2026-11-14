package files

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// S3 is an S3-compatible object store reached with AWS Signature V4 (path-style URLs, standard library only).
// Objects are private; bytes only ever leave through the Go download handler after the usual access checks.
type S3 struct {
	Endpoint, Bucket, Region, KeyID, Secret string
	Client                                  *http.Client
}

// store is nil unless FILES_S3_* is configured: then Postgres bytea is used.
var store *S3

// UseS3 sets (or with nil clears) the object store; tests use it.
func UseS3(s *S3) { store = s }

// ConfigureFromEnv selects S3 when FILES_S3_BUCKET is set. Credentials come only from the environment.
func ConfigureFromEnv() (bool, error) {
	b := os.Getenv("FILES_S3_BUCKET")
	if b == "" {
		return false, nil
	}
	s := &S3{Endpoint: os.Getenv("FILES_S3_ENDPOINT"), Bucket: b, Region: os.Getenv("FILES_S3_REGION"),
		KeyID: os.Getenv("FILES_S3_ACCESS_KEY_ID"), Secret: os.Getenv("FILES_S3_SECRET_ACCESS_KEY")}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Endpoint == "" {
		s.Endpoint = "https://s3." + s.Region + ".amazonaws.com"
	}
	if s.KeyID == "" || s.Secret == "" {
		return false, fmt.Errorf("FILES_S3_BUCKET is set but FILES_S3_ACCESS_KEY_ID / FILES_S3_SECRET_ACCESS_KEY are not")
	}
	store = s
	return true, nil
}

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func (s *S3) do(ctx context.Context, method, key string, body []byte) ([]byte, int, error) {
	u, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil {
		return nil, 0, err
	}
	u.Path = "/" + s.Bucket + "/" + key
	now := time.Now().UTC()
	date, stamp := now.Format("20060102"), now.Format("20060102T150405Z")
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	canon := strings.Join([]string{method, u.EscapedPath(), "",
		"host:" + u.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + stamp + "\n",
		"host;x-amz-content-sha256;x-amz-date", payload}, "\n")
	scope := date + "/" + s.Region + "/s3/aws4_request"
	ch := sha256.Sum256([]byte(canon))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k := hmacSHA(hmacSHA(hmacSHA(hmacSHA([]byte("AWS4"+s.Secret), date), s.Region), "s3"), "aws4_request")
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.KeyID+"/"+scope+
		", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature="+hex.EncodeToString(hmacSHA(k, toSign)))
	cl := s.Client
	if cl == nil {
		cl = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	return out, res.StatusCode, err
}

func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	_, code, err := s.do(ctx, http.MethodPut, key, data)
	if err == nil && code/100 != 2 {
		err = fmt.Errorf("s3 put: status %d", code)
	}
	return err
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	b, code, err := s.do(ctx, http.MethodGet, key, nil)
	if err == nil && code/100 != 2 {
		err = fmt.Errorf("s3 get: status %d", code)
	}
	return b, err
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, code, err := s.do(ctx, http.MethodDelete, key, nil)
	if err == nil && code/100 != 2 && code != http.StatusNotFound {
		err = fmt.Errorf("s3 delete: status %d", code)
	}
	return err
}
