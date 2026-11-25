package worker

import (
	"testing"

	"github.com/cnote/api/internal/command"
)

// The purge sweep must be registered explicitly (docs C1 13.3.9); sweeps run once when the worker starts.
func TestTrashPurgeSweepIsRegistered(t *testing.T) {
	w := New(nil, command.NewRegistry())
	for _, s := range w.sweeps {
		if s.Name == "docs.trash_purge" {
			return
		}
	}
	t.Fatal("docs.trash_purge is not registered in wire.go")
}
