package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/server"
)

const testMasterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes, tests only

type tf struct {
	*env
	owner           who
	baseID, tableID string
	name, email     string
	seq             atomic.Int64
}

func newTF(t *testing.T) *tf {
	t.Setenv("CNOTE_MASTER_KEY", testMasterKey)
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "127.0.0.1")
	e := newEnv(t)
	o := e.signup("Fay", true)
	sp := e.must(o, "space.create", map[string]any{"key": "TF", "name": "Forms"})["id"].(string)
	b := e.must(o, "base.create", map[string]any{"space_id": sp, "name": "Secret Base Name"})["id"].(string)
	tb := e.must(o, "table.create", map[string]any{"base_id": b, "name": "Leads", "fields": []any{map[string]any{"name": "Name", "type": "text"}}})
	tid := tb["id"].(string)
	x := &tf{env: e, owner: o, baseID: b, tableID: tid}
	x.name = firstField(tb)
	x.email = e.must(o, "field.create", map[string]any{"table_id": tid, "name": "Email", "type": "email"})["id"].(string)
	return x
}

func firstField(tb map[string]any) string {
	fs, _ := tb["fields"].([]any)
	if len(fs) == 0 {
		panic(fmt.Sprintf("no fields in %v", tb))
	}
	return fs[0].(map[string]any)["id"].(string)
}

func (x *tf) mk() (id, token string) {
	r := x.must(x.owner, "table_form.create", map[string]any{"table_id": x.tableID, "name": "Contact", "config": map[string]any{
		"title": "Say hi", "thank_you": "Thanks!", "fields": []any{
			map[string]any{"field_id": x.name, "required": true}, map[string]any{"field_id": x.email}}}})
	return r["form"].(map[string]any)["id"].(string), strings.TrimPrefix(r["url"].(string), "/tf/")
}

// req sends to the public endpoint from a given client address (the test server is a trusted proxy).
func (x *tf) req(method, token, body, ct, xff string) (int, http.Header, string) {
	x.t.Helper()
	r, _ := http.NewRequest(method, x.gosv.URL+"/api/public/table-form/"+token, strings.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		x.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

// Buckets live in the database for an hour, so addresses must not repeat between runs.
var addrN = func() *atomic.Int64 { v := &atomic.Int64{}; v.Store(time.Now().UnixNano() & 0xffffff); return v }()

func freshAddr() string {
	n := addrN.Add(1)
	return fmt.Sprintf("10.%d.%d.%d", n>>16&255, n>>8&255, n&255)
}

func (x *tf) post(token string, vals map[string]any, addr string) (int, string) {
	b, _ := jsonString(map[string]any{"values": vals})
	s, _, body := x.req("POST", token, b, "application/json", addr)
	return s, body
}

func jsonString(v any) (string, error) {
	var b bytes.Buffer
	err := encodeJSON(&b, v)
	return b.String(), err
}

func (x *tf) records() int {
	x.t.Helper()
	return len(x.must(x.owner, "record.list", map[string]any{"table_id": x.tableID})["records"].([]any))
}

func ipKey(ip string) string {
	h := hmac.New(sha256.New, []byte("0123456789abcdef0123456789abcdef"))
	h.Write([]byte("table-form-ip\x00" + ip))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func (x *tf) bucket(key string) int {
	n, err := db.Peek(context.Background(), x.pool, key, false)
	if err != nil {
		x.t.Fatal(err)
	}
	return n
}

const notFoundBody = `{"error":{"code":"not_found","message":"This form is not available."}}`

func TestFormPublicFlowAndHeaders(t *testing.T) {
	x := newTF(t)
	_, tok := x.mk()
	s, h, body := x.req("GET", tok, "", "", freshAddr())
	if s != 200 || h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", s, h, body)
	}
	for _, leak := range []string{"Leads", "Fay WS", x.tableID, x.baseID} { // the table, workspace and ids stay private
		if strings.Contains(body, leak) {
			t.Errorf("GET leaks %q: %s", leak, body)
		}
	}
	if strings.Contains(body, "base_name") || strings.Contains(body, "Secret Base Name") || !strings.Contains(body, `"field_id":"`+x.name+`"`) || !strings.Contains(body, `"thank_you":"Thanks!"`) {
		t.Errorf("GET %s", body)
	}
	if s, b := x.post(tok, map[string]any{x.name: "Asha", x.email: "a@b.in"}, freshAddr()); s != 200 || b != `{"ok":true}` {
		t.Fatalf("post %d %s", s, b)
	}
	if s, _, _ := x.req("POST", tok, "--x\r\n", "multipart/form-data; boundary=x", freshAddr()); s != 400 {
		t.Errorf("multipart: %d", s)
	}
	if s, b := x.post(tok, map[string]any{x.email: "a@b.in"}, freshAddr()); s != 400 || !strings.Contains(b, "name") && !strings.Contains(b, "required") {
		t.Errorf("missing required: %d %s", s, b)
	}
	if n := x.records(); n != 1 {
		t.Errorf("records %d", n)
	}
	if s, _, _ := x.req("PUT", tok, "", "", ""); s != 405 {
		t.Errorf("PUT %d", s)
	}
	// honeypot: 200 and nothing stored
	if s, _, b := x.req("POST", tok, `{"values":{"`+x.name+`":"bot"},"hp":"x"}`, "application/json", freshAddr()); s != 200 || b != `{"ok":true}` || x.records() != 1 {
		t.Errorf("honeypot %d %s", s, b)
	}
}

func TestFormOne404ForEveryDeadLink(t *testing.T) {
	x := newTF(t)
	admin, err := db.Connect(context.Background(), os.Getenv("DATABASE_ADMIN_URL"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	type snap struct {
		status int
		h      [4]string
		body   string
	}
	get := func(method, tok string) snap {
		s, h, b := x.req(method, tok, `{"values":{}}`, "application/json", freshAddr())
		return snap{s, [4]string{h.Get("Content-Type"), h.Get("Cache-Control"), h.Get("X-Content-Type-Options"), h.Get("Referrer-Policy")}, b}
	}
	want := get("GET", "short")
	if want.status != 404 || want.body != notFoundBody || want.h[3] != "no-referrer" {
		t.Fatalf("baseline %+v", want)
	}
	cases := map[string]string{"unknown": "AAAAAAAAAAAAAAAAAAAAAA", "malformed": "bad token!"}
	id, tok := x.mk()
	x.must(x.owner, "table_form.disable", map[string]any{"id": id})
	cases["disabled"] = tok
	id2, tok2 := x.mk()
	x.must(x.owner, "table_form.rotate", map[string]any{"id": id2})
	cases["rotated away"] = tok2
	for name, tk := range cases {
		for _, m := range []string{"GET", "POST"} {
			if got := get(m, tk); got != want {
				t.Errorf("%s %s: %+v", name, m, got)
			}
		}
	}
	_, live := x.mk()
	if s, _, _ := x.req("GET", live, "", "", freshAddr()); s != 200 {
		t.Fatal("live form not served")
	}
	kill := func(name, sql string, args ...any) {
		if _, err := admin.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
		for _, m := range []string{"GET", "POST"} {
			if got := get(m, live); got != want {
				t.Errorf("%s %s: %+v", name, m, got)
			}
		}
	}
	kill("table trashed", `update tables.tables set deleted_at=now() where id=$1`, x.tableID)
	admin.Exec(context.Background(), `update tables.tables set deleted_at=null where id=$1`, x.tableID)
	kill("base trashed", `update tables.bases set deleted_at=now() where id=$1`, x.baseID)
	admin.Exec(context.Background(), `update tables.bases set deleted_at=null where id=$1`, x.baseID)
	kill("creator gone", `delete from core.memberships where workspace_id=$1 and user_id=$2`, x.owner.ws, x.owner.userID)
}

func TestFormLimitsRunBeforeTheBodyIsRead(t *testing.T) {
	x := newTF(t)
	id, tok := x.mk()
	h := server.New(x.pool, command.NewRegistry(modules.Commands()...))
	addr := freshAddr()
	over := func(key string, day bool, n int) {
		for i := 0; i < n; i++ {
			if _, err := db.Bump(context.Background(), x.pool, key, day); err != nil {
				t.Fatal(err)
			}
		}
	}
	over("tf:tok:"+id, false, 120)
	over("tf:sub:"+id+":"+ipKey(addr), false, 10)
	over("tf:day:"+id, true, 50) // free plan
	for _, a := range []string{addr, freshAddr()} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/public/table-form/"+tok, failReader{t})
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("X-Forwarded-For", a)
		h.ServeHTTP(rec, req)
		if rec.Code != 429 {
			t.Errorf("%s: %d", a, rec.Code)
		}
	}
	if x.records() != 0 {
		t.Error("a limited POST stored a record")
	}
}

type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Error("the body was read before the limits")
	return 0, io.EOF
}

func TestFormEachLimit(t *testing.T) {
	x := newTF(t)
	id, tok := x.mk()
	vals := map[string]any{x.name: "n"}
	set := func(key string, day bool, n int) {
		if _, err := x.pool.Exec(context.Background(), `insert into core.rate_buckets (key, bucket, n) values ($1,$2,$3) on conflict (key, bucket) do update set n=$3`, key, db.BucketStart(timeNow(), day), n); err != nil {
			t.Fatal(err)
		}
	}
	a := freshAddr()
	set("tf:tok:"+id, false, 120)
	if s, _ := x.post(tok, vals, a); s != 429 {
		t.Errorf("per form: %d", s)
	}
	set("tf:tok:"+id, false, 0)
	set("tf:sub:"+id+":"+ipKey(a), false, 10)
	if s, _ := x.post(tok, vals, a); s != 429 {
		t.Errorf("per address per form: %d", s)
	}
	set("tf:day:"+id, true, 49)
	if s, _ := x.post(tok, vals, freshAddr()); s != 200 {
		t.Errorf("last of the day: %d", s)
	}
	if s, _ := x.post(tok, vals, freshAddr()); s != 429 {
		t.Errorf("daily cap: %d", s)
	}
	if x.records() != 1 {
		t.Errorf("records %d", x.records())
	}
	b := freshAddr()
	set("tf:ip:"+ipKey(b), false, 119)
	if s, _, _ := x.req("GET", tok, "", "", b); s != 200 {
		t.Errorf("120th: %d", s)
	}
	if s, _, _ := x.req("GET", tok, "", "", b); s != 429 {
		t.Errorf("per address: %d", s)
	}
	c := freshAddr()
	set("tf:miss:"+ipKey(c), false, 20)
	if s, _, _ := x.req("GET", tok, "", "", c); s != 429 { // a valid link too: the address has probed too much
		t.Errorf("miss limit: %d", s)
	}
	set("tf:all", false, 6000)
	s, _, _ := x.req("GET", tok, "", "", freshAddr())
	set("tf:all", false, 0)
	if s != 429 {
		t.Errorf("global: %d", s)
	}
}

func TestFormAddressLimitCountsMalformedTokens(t *testing.T) {
	x := newTF(t)
	a := freshAddr()
	for i := 0; i < 5; i++ {
		if s, _, _ := x.req("GET", "not a token", "", "", a); s != 404 {
			t.Fatal(s)
		}
	}
	if n := x.bucket("tf:ip:" + ipKey(a)); n != 5 {
		t.Errorf("address bucket %d, want 5", n)
	}
	if n := x.bucket("tf:miss:" + ipKey(a)); n != 5 {
		t.Errorf("miss bucket %d, want 5", n)
	}
}

func TestFormClientAddressBehindTrustedAndUntrustedProxy(t *testing.T) {
	x := newTF(t)
	_, tok := x.mk()
	right := freshAddr()
	for _, left := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3, 4.4.4.4"} { // forged entries to the left do not matter
		x.req("GET", tok, "", "", left+", "+right)
	}
	if n := x.bucket("tf:ip:" + ipKey(right)); n != 3 {
		t.Errorf("trusted proxy: bucket of the real address %d, want 3", n)
	}
	if n := x.bucket("tf:ip:" + ipKey("1.1.1.1")); n != 0 {
		t.Errorf("a forged address was counted: %d", n)
	}
	garbage := "9.9.9.9, not-an-ip"
	x.req("GET", tok, "", "", garbage)
	if x.bucket("tf:ip:"+ipKey("9.9.9.9")) != 0 {
		t.Error("garbage header trusted")
	}
	// An untrusted peer: the header is ignored whatever it says.
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "192.0.2.0/24")
	before := x.bucket("tf:ip:" + ipKey("127.0.0.1"))
	spoof := freshAddr()
	x.req("GET", tok, "", "", spoof)
	if x.bucket("tf:ip:"+ipKey(spoof)) != 0 || x.bucket("tf:ip:"+ipKey("127.0.0.1")) != before+1 {
		t.Error("untrusted peer: X-Forwarded-For was believed")
	}
}

func TestFormAnswers503WithoutAStableKey(t *testing.T) {
	x := newTF(t)
	_, tok := x.mk()
	t.Setenv("CNOTE_MASTER_KEY", "")
	for _, p := range []string{tok, "garbage"} {
		for _, m := range []string{"GET", "POST"} {
			if s, _, _ := x.req(m, p, "{}", "application/json", freshAddr()); s != 503 {
				t.Errorf("%s %s: %d", m, p, s)
			}
		}
	}
}

func TestFormTokenNeverInLogsOrResponses(t *testing.T) {
	x := newTF(t)
	_, tok := x.mk()
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	var all strings.Builder
	for _, b := range []string{`{"values":{"` + x.name + `":"` + strings.Repeat("x", 300000) + `"}}`, `{`, `{"values":{"zzz":"1"}}`} {
		_, h, body := x.req("POST", tok, b, "application/json", freshAddr())
		all.WriteString(body + fmt.Sprint(h))
	}
	_, h, body := x.req("GET", tok, "", "", freshAddr())
	all.WriteString(body + fmt.Sprint(h))
	if strings.Contains(all.String(), tok) || strings.Contains(logs.String(), tok) {
		t.Error("token leaked")
	}
}

func encodeJSON(b *bytes.Buffer, v any) error { return json.NewEncoder(b).Encode(v) }

func timeNow() time.Time { return time.Now() }
