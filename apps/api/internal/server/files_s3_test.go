package server_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cnote/api/internal/modules/files"
)

// fakeS3 stores objects in memory and rejects any request whose SigV4 signature does not verify.
type fakeS3 struct {
	mu      sync.Mutex
	objs    map[string][]byte
	rejects int
}

var authRe = regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=([^/]+)/(\d{8})/([^/]+)/s3/aws4_request, SignedHeaders=([^,]+), Signature=([0-9a-f]{64})$`)

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !f.verify(r, body) {
		f.mu.Lock()
		f.rejects++
		f.mu.Unlock()
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		f.objs[r.URL.Path] = body
	case http.MethodGet:
		if o, ok := f.objs[r.URL.Path]; ok {
			_, _ = w.Write(o)
		} else {
			http.NotFound(w, r)
		}
	case http.MethodDelete:
		delete(f.objs, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeS3) verify(r *http.Request, body []byte) bool {
	m := authRe.FindStringSubmatch(r.Header.Get("Authorization"))
	sum := sha256.Sum256(body)
	if m == nil || m[1] != "test-key-id" || m[3] != "eu-test-1" || r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
		return false
	}
	stamp := r.Header.Get("x-amz-date")
	if !strings.HasPrefix(stamp, m[2]) {
		return false
	}
	var canon strings.Builder
	canon.WriteString(r.Method + "\n" + r.URL.EscapedPath() + "\n" + r.URL.RawQuery + "\n")
	for _, h := range strings.Split(m[4], ";") {
		v := r.Host
		if h != "host" {
			v = r.Header.Get(h)
		}
		canon.WriteString(h + ":" + v + "\n")
	}
	canon.WriteString("\n" + m[4] + "\n" + r.Header.Get("x-amz-content-sha256"))
	ch := sha256.Sum256([]byte(canon.String()))
	sts := "AWS4-HMAC-SHA256\n" + stamp + "\n" + m[2] + "/" + m[3] + "/s3/aws4_request\n" + hex.EncodeToString(ch[:])
	mac := func(k []byte, s string) []byte { h := hmac.New(sha256.New, k); h.Write([]byte(s)); return h.Sum(nil) }
	k := mac(mac(mac(mac([]byte("AWS4"+"test-secret"), m[2]), m[3]), "s3"), "aws4_request")
	return hmac.Equal([]byte(hex.EncodeToString(mac(k, sts))), []byte(m[5]))
}

func TestFilesS3BackendSignsAndKeepsAccessChecks(t *testing.T) {
	fake := &fakeS3{objs: map[string][]byte{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	files.UseS3(&files.S3{Endpoint: srv.URL, Bucket: "bkt", Region: "eu-test-1", KeyID: "test-key-id", Secret: "test-secret"})
	defer files.UseS3(nil)

	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	out := b.addMember(b.owner, b.signup("Out", false), "guest")
	id := b.claim(m, 10_000)
	ws := b.owner.ws
	r := b.upload(m, ws, id, "a.png", png, "image/png")
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fid := r.json()["id"].(string)
	eq(t, b.dbStrings(ws, "select storage_key || ':' || coalesce(length(data)::text,'null') from core.attachments where id = $1::uuid", fid), []string{"s3:null"})
	if !bytes.Equal(fake.objs["/bkt/"+ws+"/"+fid], png) || fake.rejects != 0 {
		t.Fatalf("object not stored with a valid signature: %v rejects=%d", len(fake.objs), fake.rejects)
	}
	got, h := b.fetch(m, ws, fid)
	if got.status != 200 || !bytes.Equal(got.body, png) || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download %d %v", got.status, h)
	}
	eq(t, len(b.call(m, "file.list", map[string]any{"note_id": id}).jsonList()), 1)
	if g, _ := b.fetch(out, ws, fid); g.status == 200 { // same access checks as before
		t.Fatal("outsider downloaded")
	}
	b.okAs(m, "file.delete", map[string]any{"id": fid})
	eq(t, len(fake.objs), 0)

	// a wrong secret is refused by the bucket: the upload fails and leaves no row
	files.UseS3(&files.S3{Endpoint: srv.URL, Bucket: "bkt", Region: "eu-test-1", KeyID: "test-key-id", Secret: "wrong"})
	if r := b.upload(m, ws, id, "a.png", png, "image/png"); r.status < 500 {
		t.Fatalf("bad signature accepted: %d", r.status)
	}
	eq(t, b.dbStrings(ws, "select count(*)::text from core.attachments where note_id = $1::uuid", id), []string{"0"})
}
