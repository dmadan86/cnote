package server_test

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startCollab runs the TypeScript collab service (apps/collab) on a free port of 4710-4719 with a long snapshot
// debounce, so edits typed through it stay in core.note_updates, and points the Go front at it the way
// COLLAB_URL / COLLAB_INTERNAL_SECRET do in production. Returns the port. A missing node or `pnpm install` fails
// the test: restore is only provable with the real service.
func startCollab(t *testing.T) int {
	t.Helper()
	dir := "../../../collab"
	if _, err := os.Stat(dir + "/node_modules/tsx"); err != nil {
		t.Fatalf("apps/collab dependencies missing (pnpm install): %v", err)
	}
	port := 0
	for p := 4710; p <= 4719 && port == 0; p++ {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			l.Close()
			port = p
		}
	}
	if port == 0 {
		t.Fatal("no free port in 4710-4719")
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	secret := hex.EncodeToString(b[:])
	cmd := exec.Command("node", "--import", "tsx", "src/main.ts")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "COLLAB_PORT="+strconv.Itoa(port), "COLLAB_INTERNAL_SECRET="+secret, "COLLAB_DEBOUNCE_MS=600000")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	for i := 0; ; i++ {
		if resp, err := http.Get(url + "/healthz"); err == nil {
			resp.Body.Close()
			break
		}
		if i > 150 {
			t.Fatal("collab service did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Setenv("COLLAB_URL", url)
	t.Setenv("COLLAB_INTERNAL_SECRET", secret)
	return port
}

// typeInto types into the note through the collab service, like a browser would.
func (r *runner) typeInto(port int, caller, note string, texts ...string) {
	r.e.t.Helper()
	args := append([]string{"--import", "tsx", "test/edit-cli.ts", strconv.Itoa(port), r.f.owner.ws, note, r.f.who(caller).cookie}, texts...)
	cmd := exec.Command("node", args...)
	cmd.Dir = "../../../collab"
	if out, err := cmd.CombinedOutput(); err != nil {
		r.e.t.Fatalf("edit-cli: %v\n%s", err, out)
	}
}

// note.restore renders the live document through the collab service: with edits still in the update log (never
// snapshotted) the before_restore version must hold the typed text, byte-identical to what Rust's yrs renders.
func TestDifferentialRestoreLiveDocument(t *testing.T) {
	port := startCollab(t)
	var e *env
	script := func(r *runner) {
		e = r.e
		sp := r.f.space
		r.id("p1", "owner", "note.create", map[string]any{"space_id": sp, "title": "Live", "content_md": "alpha **bold** and `code`\n\n- one\n- two"})
		r.call("owner", "note.update", map[string]any{"id": r.v("p1"), "content_md": "v1 ünï 🚀"})
		r.call("owner", "note.update", map[string]any{"id": r.v("p1"), "content_md": "v2\n\n# Heading\n\ntext"})
		var revs []map[string]any
		revs = r.list("owner", "note.revisions", map[string]any{"note_id": r.v("p1")})
		first := revs[len(revs)-1]["id"].(string)
		r.vars["first"] = first
		r.typeInto(port, "owner", r.v("p1"), " typed ünï ✓ 🚀", " more")
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": first, "expected_epoch": 99})
		r.call("member", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": first})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": first})
		// restored content is the new live document; type again and restore the version before
		r.typeInto(port, "owner", r.v("p1"), " again")
		revs = r.list("owner", "note.revisions", map[string]any{"note_id": r.v("p1")})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": revs[1]["id"].(string)})
		r.call("owner", "note.restore", map[string]any{"note_id": r.v("p1"), "revision_id": revs[0]["id"].(string), "expected_epoch": 2})
	}
	_, gov := runTwin(t, script, map[string]string{
		"revisions": `select (to_jsonb(x) - 'id')::text from core.note_revisions x order by x.created_at, x.number`,
		"notes":     `select (jsonb_build_object('t', title, 'md', content_md, 'v', version, 'e', collab_epoch, 'y', content_ydoc is null))::text from core.notes order by created_at`,
		"updates":   `select count(*)::text from core.note_updates`,
		"events":    `select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind <> 'command.denied' order by x.id`,
	}, 6, 1)
	if e.proxied.has("note.restore") {
		t.Fatal("note.restore went to Rust: it must be served natively once COLLAB_URL and the secret are set")
	}
	joined := strings.Join(gov.transcript, "\n")
	if !strings.Contains(joined, "typed ünï") || !strings.Contains(joined, "before_restore") {
		t.Fatalf("the live edits are missing from the before_restore version:\n%s", joined)
	}
}
