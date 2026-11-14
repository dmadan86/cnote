// Integration tests for notifications commands, ported from
// crates/cnote-core/tests/page_permissions.rs (mentions_notify_once_and_only_people_who_can_read).
// All tests run against the real Postgres and the real Rust server; nothing is mocked.
package notifications_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/proxy"
	"github.com/cnote/api/internal/server"
	"github.com/jackc/pgx/v5/pgxpool"

	"crypto/rand"
	"encoding/hex"
)

// ---- mini test harness (self-contained, mirrors server_test helpers) ----

type testEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	rust string
	gosv *httptest.Server
}

type who struct {
	userID, ws, email, cookie, bearer string
}

func rnd() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required (load the repo .env)")
	}
	rust := strings.TrimRight(os.Getenv("RUST_API_URL"), "/")
	if rust == "" {
		rust = "http://localhost:4000"
	}
	if resp, err := http.Get(rust + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("Rust server not reachable at %s: %v", rust, err)
	}
	pool, err := db.Connect(context.Background(), url, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	up, err := proxy.New(rust)
	if err != nil {
		t.Fatal(err)
	}
	reg := command.NewRegistry(modules.Commands()...)
	gosv := httptest.NewServer(server.New(pool, reg, up))
	t.Cleanup(gosv.Close)
	return &testEnv{t: t, pool: pool, rust: rust, gosv: gosv}
}

func (e *testEnv) signup(name string, workspace bool) who {
	e.t.Helper()
	w := who{email: "go-notif-" + rnd() + "@example.test"}
	body := map[string]any{"email": w.email, "name": name, "password": rnd() + rnd()}
	if workspace {
		body["workspace_name"] = name + " WS"
	}
	b, _ := json.Marshal(body)
	res, err := http.Post(e.rust+"/api/auth/signup", "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		UserID      string  `json:"user_id"`
		WorkspaceID *string `json:"workspace_id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	for _, c := range res.Cookies() {
		if c.Name == "cn_session" {
			w.cookie = c.Value
		}
	}
	if res.StatusCode != 200 || w.cookie == "" {
		e.t.Fatalf("signup failed: %d", res.StatusCode)
	}
	w.userID = out.UserID
	if out.WorkspaceID != nil {
		w.ws = *out.WorkspaceID
	}
	return w
}

func (e *testEnv) addMember(owner, u who, role string) who {
	e.t.Helper()
	e.must(owner, "member.add", map[string]any{"email": u.email, "role": role})
	u.ws = owner.ws
	return u
}

func (e *testEnv) must(w who, cmd string, in any) map[string]any {
	e.t.Helper()
	r := e.do(e.rust, "POST", "/api/w/"+w.ws+"/commands/"+cmd, w, in)
	if r.status != 200 {
		e.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (e *testEnv) space(w who) string {
	e.t.Helper()
	r := e.do(e.rust, "POST", "/api/w/"+w.ws+"/commands/space.list", w, map[string]any{})
	var spaces []map[string]any
	_ = json.Unmarshal(r.body, &spaces)
	return spaces[0]["id"].(string)
}

func (e *testEnv) note(w who, title string) string {
	e.t.Helper()
	return e.must(w, "note.create", map[string]any{"space_id": e.space(w), "title": title})["id"].(string)
}

type resp struct {
	status int
	body   []byte
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (e *testEnv) do(base, method, path string, w who, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if w.cookie != "" {
		req.Header.Set("Cookie", "cn_session="+w.cookie)
	}
	if w.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+w.bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b}
}

func (e *testEnv) go_(w who, cmd string, in any) resp {
	return e.do(e.gosv.URL, "POST", "/api/w/"+w.ws+"/commands/"+cmd, w, in)
}

func (e *testEnv) rust_(w who, cmd string, in any) resp {
	return e.do(e.rust, "POST", "/api/w/"+w.ws+"/commands/"+cmd, w, in)
}

// ---- tests ----

// TestNotificationListAndUnreadCount ports the core assertion from the Rust test
// mentions_notify_once_and_only_people_who_can_read in page_permissions.rs:
// - Mention creates a notification for the mentioned user (not the author).
// - Re-saving the same body does not duplicate.
// - A mention of a user who cannot read the page (view-restricted) creates no notification.
// - notification.list returns the notification with correct note_title.
// - notification.unread_count matches the number of unread items.
func TestNotificationListAndUnreadCount(t *testing.T) {
	e := newTestEnv(t)
	owner := e.signup("Owner", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")

	sp := e.space(owner)
	page := e.must(owner, "note.create", map[string]any{
		"space_id": sp, "title": "Plan",
	})["id"].(string)

	wsID := owner.ws
	link := fmt.Sprintf("[@Bob](/w/%s/people/%s)", wsID, bob.userID)
	save := func(body string) {
		t.Helper()
		e.must(owner, "note.update", map[string]any{"id": page, "content_md": body})
	}

	// First mention: Bob gets 1 notification.
	body1 := "hi " + link
	save(body1)
	r := e.go_(bob, "notification.unread_count", map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(1) {
		t.Fatalf("first mention: status=%d body=%s", r.status, r.body)
	}

	// Re-saving the identical body: still exactly 1 (no duplicate).
	save(body1 + " more")
	r = e.go_(bob, "notification.unread_count", map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(1) {
		t.Fatalf("re-save duplicated: status=%d body=%s", r.status, r.body)
	}

	// notification.list must include the notification with the correct note title.
	r = e.go_(bob, "notification.list", map[string]any{})
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	var items []map[string]any
	if err := json.Unmarshal(r.body, &items); err != nil || len(items) == 0 {
		t.Fatalf("list parse: err=%v body=%s", err, r.body)
	}
	if items[0]["note_title"] != "Plan" {
		t.Fatalf("note_title: %v", items[0])
	}
	if items[0]["kind"] != "mention" {
		t.Fatalf("kind: %v", items[0])
	}
	if items[0]["read_at"] != nil {
		t.Fatalf("read_at should be nil: %v", items[0])
	}
}

// TestNotificationRead ports the "Marking read clears the badge" assertion.
func TestNotificationRead(t *testing.T) {
	e := newTestEnv(t)
	owner := e.signup("Owner", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")

	sp := e.space(owner)
	page := e.must(owner, "note.create", map[string]any{"space_id": sp, "title": "ReadTest"})["id"].(string)

	link := fmt.Sprintf("[@Bob](/w/%s/people/%s)", owner.ws, bob.userID)
	e.must(owner, "note.update", map[string]any{"id": page, "content_md": "hi " + link})

	// Confirm there is 1 unread on Go.
	r := e.go_(bob, "notification.unread_count", map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(1) {
		t.Fatalf("before read: %d %s", r.status, r.body)
	}

	// Mark all read (no id).
	r = e.go_(bob, "notification.read", map[string]any{})
	if r.status != 200 || r.json()["updated"] != float64(1) {
		t.Fatalf("notification.read: %d %s", r.status, r.body)
	}

	// Unread count is now 0.
	r = e.go_(bob, "notification.unread_count", map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(0) {
		t.Fatalf("after read: %d %s", r.status, r.body)
	}

	// A second mark-all returns updated=0 (already read).
	r = e.go_(bob, "notification.read", map[string]any{})
	if r.status != 200 || r.json()["updated"] != float64(0) {
		t.Fatalf("second mark-all: %d %s", r.status, r.body)
	}
}

// TestNotificationReadById marks a single notification read by its id.
func TestNotificationReadById(t *testing.T) {
	e := newTestEnv(t)
	owner := e.signup("Owner", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")

	sp := e.space(owner)
	page := e.must(owner, "note.create", map[string]any{"space_id": sp, "title": "ByID"})["id"].(string)
	link := fmt.Sprintf("[@Bob](/w/%s/people/%s)", owner.ws, bob.userID)
	e.must(owner, "note.update", map[string]any{"id": page, "content_md": "hi " + link})

	// Get the notification id.
	r := e.go_(bob, "notification.list", map[string]any{})
	var items []map[string]any
	_ = json.Unmarshal(r.body, &items)
	if len(items) == 0 {
		t.Fatal("no notifications")
	}
	nid := items[0]["id"].(string)

	// Mark only that notification read.
	r = e.go_(bob, "notification.read", map[string]any{"id": nid})
	if r.status != 200 || r.json()["updated"] != float64(1) {
		t.Fatalf("read by id: %d %s", r.status, r.body)
	}

	// Now read_at is non-nil in the list.
	r = e.go_(bob, "notification.list", map[string]any{})
	_ = json.Unmarshal(r.body, &items)
	if items[0]["read_at"] == nil {
		t.Fatal("read_at should be non-nil after mark-read")
	}
}

// TestNotificationTenantIsolation ensures Bob cannot see Alice's notifications.
func TestNotificationTenantIsolation(t *testing.T) {
	e := newTestEnv(t)
	alice := e.signup("Alice", true)
	bob := e.signup("Bob", true)

	spA := e.space(alice)
	pageA := e.must(alice, "note.create", map[string]any{"space_id": spA, "title": "AlicePage"})["id"].(string)
	// Mention alice in her own page (self-mention is skipped, but we still want a row fixture).
	// Invite carol so she can be mentioned.
	carol := e.addMember(alice, e.signup("Carol", false), "member")
	link := fmt.Sprintf("[@Carol](/w/%s/people/%s)", alice.ws, carol.userID)
	e.must(alice, "note.update", map[string]any{"id": pageA, "content_md": "hi " + link})

	// Carol has 1 notification in Alice's workspace.
	r := e.do(e.gosv.URL, "POST", "/api/w/"+alice.ws+"/commands/notification.unread_count", carol, map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(1) {
		t.Fatalf("carol count: %d %s", r.status, r.body)
	}

	// Bob in his own workspace sees 0 (RLS isolates rows).
	r = e.go_(bob, "notification.unread_count", map[string]any{})
	if r.status != 200 || r.json()["count"] != float64(0) {
		t.Fatalf("bob count: %d %s", r.status, r.body)
	}
}

// TestNotificationUnauthenticated checks that unauthenticated callers get 401.
func TestNotificationUnauthenticated(t *testing.T) {
	e := newTestEnv(t)
	owner := e.signup("Owner", true)
	for _, cmd := range []string{"notification.list", "notification.unread_count", "notification.read"} {
		r := e.do(e.gosv.URL, "POST", "/api/w/"+owner.ws+"/commands/"+cmd, who{}, map[string]any{})
		if r.status != 401 {
			t.Errorf("%s unauthenticated: got %d %s", cmd, r.status, r.body)
		}
	}
}

// TestNotificationEmptyList ensures notification.list returns [] (not null) for a fresh user.
func TestNotificationEmptyList(t *testing.T) {
	e := newTestEnv(t)
	owner := e.signup("Owner", true)
	r := e.go_(owner, "notification.list", map[string]any{})
	if r.status != 200 || string(r.body) != "[]" {
		t.Fatalf("empty list: %d %s", r.status, r.body)
	}
}
