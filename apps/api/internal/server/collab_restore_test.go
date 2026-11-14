package server_test

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
