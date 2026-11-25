package server_test

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"

	"github.com/cnote/api/internal/modules/files"
)

// C1 step 0 (design 13.3.1): file.delete only queues the object; the committed sweep removes it.

func (d dg) pageFile(w who, page string) string {
	d.t.Helper()
	up := d.ok(w, "file.upload", map[string]any{"note_id": page, "filename": "a.png", "data_base64": base64.StdEncoding.EncodeToString(png)})
	return up["id"].(string)
}

func (d dg) sweep() {
	d.t.Helper()
	if err := files.RecordSweep(context.Background(), d.e.pool); err != nil {
		d.t.Fatal(err)
	}
}

func TestC1FileDeleteQueuesAndSweepRemovesTheObject(t *testing.T) {
	m := useMemS3(t)
	d, owner := newDG(t)
	sp := d.space(owner, "FIL", "open")
	page := d.page(owner, sp, "P", nil, "x")
	id := d.pageFile(owner, page)
	if !m.has(d.ws, id) {
		t.Fatal("object not written")
	}
	// a rolled-back delete leaves the row, the object and an empty queue
	tx, err := dbTenant(d.e, d.ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "delete from core.attachments where id = $1::uuid", id); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(context.Background())
	d.sweep()
	if !m.has(d.ws, id) || d.scalar("select count(*)::text from core.file_gc") != "0" {
		t.Fatal("rollback lost the object or left a queue entry")
	}
	// the command deletes the row and queues the key, but never touches the bucket itself
	d.ok(owner, "file.delete", map[string]any{"id": id})
	if !m.has(d.ws, id) || d.scalar("select count(*)::text from core.file_gc where object_key = $1", d.ws+"/"+id) != "1" {
		t.Fatal("file.delete must only queue the object")
	}
	d.sweep()
	if m.has(d.ws, id) || d.scalar("select count(*)::text from core.file_gc") != "0" {
		t.Fatal("sweep did not remove the object")
	}
}

func TestC1FileDeleteConcurrentIsSafe(t *testing.T) {
	m := useMemS3(t)
	d, owner := newDG(t)
	sp := d.space(owner, "FIC", "open")
	page := d.page(owner, sp, "P", nil, "x")
	const n = 8
	ids := make([]string, n)
	for i := range ids {
		ids[i] = d.pageFile(owner, page)
	}
	// every file is deleted by two racing commands: one wins, the other sees not_found, none errors out
	var wg sync.WaitGroup
	for _, id := range ids {
		for r := 0; r < 2; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp := d.run(owner, "file.delete", map[string]any{"id": id})
				if resp.status != 200 && resp.status != 404 {
					t.Errorf("delete %s: %d %s", id, resp.status, resp.body)
				}
			}()
		}
	}
	wg.Wait()
	for _, id := range ids {
		if !m.has(d.ws, id) {
			t.Errorf("object %s deleted inside the command", id)
		}
	}
	// racing sweeps remove each object once
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d.sweep() }()
	}
	wg.Wait()
	for _, id := range ids {
		if m.has(d.ws, id) {
			t.Errorf("object %s kept", id)
		}
	}
	if d.scalar("select count(*)::text from core.file_gc") != "0" {
		t.Fatal("queue not drained")
	}
}

func TestC1WorkspaceCreationStartsTheRetentionClock(t *testing.T) {
	d, owner := newDG(t)
	if d.scalar("select (trash_purge_from is not null and trash_purge_from > now() - interval '1 minute')::text from core.workspaces where id = $1::uuid", d.ws) != "true" {
		t.Fatal("a new workspace must have trash_purge_from set")
	}
	// the retention setting changes only through its own command, which resets the clock
	if code, _ := d.errOf(owner, "workspace.update", map[string]any{"settings": map[string]any{"trash_retention_days": 0}}); code != "invalid" {
		t.Fatalf("workspace.update must refuse trash_retention_days, got %s", code)
	}
}

// A purge deletes pages that have history: the foreign key nulls events.note_id, which the append-only trigger must allow, and nothing else.
func TestC1EventsStayAppendOnlyExceptTheForeignKeyNulling(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "EVT", "open")
	page := d.page(owner, sp, "P", nil, "x")
	if d.scalar("select count(*)::text from core.events where note_id = $1::uuid", page) == "0" {
		t.Fatal("expected history for the page")
	}
	d.sql("delete from core.notes where id = $1::uuid", page)
	if d.scalar("select count(*)::text from core.events where kind = 'note.created' and note_id is null and workspace_id = $1::uuid", d.ws) == "0" {
		t.Fatal("events of the deleted page must stay, with note_id null")
	}
	for _, q := range []string{
		"update core.events set kind = 'x' where workspace_id = $1::uuid",
		"update core.events set payload = '{}' where workspace_id = $1::uuid",
		"delete from core.events where workspace_id = $1::uuid",
	} {
		tx, err := dbTenant(d.e, d.ws)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(context.Background(), q, d.ws); err == nil {
			t.Errorf("%q must fail", q)
		}
		_ = tx.Rollback(context.Background())
	}
}
