package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// CN-QA-001 section 3.4 / 5.1: RV-001, RV-006, RV-008, DB-007, DB-008, DB-011.

func (e *env) revisions(t *testing.T, ws, note string) []int {
	t.Helper()
	var nums []int
	inTx(t, e, ws, func(tx pgx.Tx) {
		rows, err := tx.Query(context.Background(), "select number from core.note_revisions where note_id = $1::uuid order by number", note)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var n int
			_ = rows.Scan(&n)
			nums = append(nums, n)
		}
	})
	return nums
}

// DB-007, DB-008, RV-001: versions are history. The app role can add them, never change or remove them.
func TestQARevisionsAreImmutableAndNumbersUnique(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	o := e.signup("Rev", true)
	note := e.note(o, "rev page")
	e.must(o, "note.update", map[string]any{"id": note, "content_md": "v1"})
	if got := e.revisions(t, o.ws, note); len(got) != 1 || got[0] != 1 {
		t.Fatalf("expected revision 1 after the first edit, got %v", got)
	}
	exec := func(sql string, args ...any) (err error) {
		inTx(t, e, o.ws, func(tx pgx.Tx) { _, err = tx.Exec(ctx, sql, args...) })
		return err
	}
	for name, sql := range map[string]string{
		"DB-007 UPDATE content":   "update core.note_revisions set content_md = 'tampered' where note_id = '" + note + "'",
		"DB-007 UPDATE number":    "update core.note_revisions set number = number + 10 where note_id = '" + note + "'",
		"DB-007 UPDATE every row": "update core.note_revisions set title = title",
		"DB-007 DELETE":           "delete from core.note_revisions where note_id = '" + note + "'",
		"DB-007 DELETE every row": "delete from core.note_revisions",
		"DB-007 TRUNCATE":         "truncate core.note_revisions",
	} {
		t.Run(name, func(t *testing.T) {
			err := exec(sql)
			if sqlState(err) != "42501" {
				t.Fatalf("must be refused with insufficient_privilege (42501), got: %v", err)
			}
		})
	}
	t.Run("DB-008 duplicate number for one note", func(t *testing.T) {
		err := exec(`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason) values ($1::uuid,$2::uuid,1,'t','c','edit')`, o.ws, note)
		if sqlState(err) != "23505" {
			t.Fatalf("want unique_violation, got %v", err)
		}
	})
	t.Run("DB-008 same number on another note is fine", func(t *testing.T) {
		other := e.note(o, "other")
		if err := exec(`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason) values ($1::uuid,$2::uuid,1,'t','c','edit')`, o.ws, other); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reason is constrained", func(t *testing.T) {
		err := exec(`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason) values ($1::uuid,$2::uuid,77,'t','c','publish')`, o.ws, note)
		if sqlState(err) != "23514" {
			t.Fatalf("want check_violation, got %v", err)
		}
	})
	if got := e.revisions(t, o.ws, note); len(got) != 1 {
		t.Fatalf("history changed: %v", got)
	}
}

// DB-011 / RV-008: concurrent checkpoints and restores of one page leave unique, gap-free numbers and no failed request.
func TestQAConcurrentCheckpointsAndRestoresKeepNumbersConsistent(t *testing.T) {
	e := newEnv(t)
	o := e.signup("RevRace", true)

	t.Run("concurrent edits of a fresh page checkpoint exactly once", func(t *testing.T) {
		note := e.note(o, "race edit")
		var wg sync.WaitGroup
		codes := make(chan int, 12)
		for i := range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes <- e.cmd(e.gosv.URL, o, o.ws, "note.update", map[string]any{"id": note, "content_md": fmt.Sprintf("body %d", i)}, nil).status
			}()
		}
		wg.Wait()
		close(codes)
		for c := range codes {
			if c != 200 {
				t.Errorf("a concurrent edit failed with %d", c)
			}
		}
		if got := e.revisions(t, o.ws, note); len(got) != 1 || got[0] != 1 {
			t.Fatalf("one checkpoint expected, got numbers %v", got)
		}
	})

	t.Run("concurrent restores and edits", func(t *testing.T) {
		note := e.note(o, "race restore")
		e.must(o, "note.update", map[string]any{"id": note, "content_md": "first"})
		r := e.cmd(e.rust, o, o.ws, "note.revisions", map[string]any{"note_id": note}, nil)
		var list []map[string]any
		if err := jsonUnmarshal(r.body, &list); err != nil || len(list) != 1 {
			t.Fatalf("revisions: %d %s", r.status, r.body)
		}
		rev := list[0]["id"].(string)
		const restores = 8
		var wg sync.WaitGroup
		type result struct {
			what   string
			status int
			body   []byte
		}
		codes := make(chan result, restores*2)
		for i := range restores {
			wg.Add(2)
			go func() {
				defer wg.Done()
				x := e.cmd(e.rust, o, o.ws, "note.restore", map[string]any{"note_id": note, "revision_id": rev}, nil)
				codes <- result{"restore", x.status, x.body}
			}()
			go func() {
				defer wg.Done()
				x := e.cmd(e.gosv.URL, o, o.ws, "note.update", map[string]any{"id": note, "content_md": fmt.Sprintf("edit %d", i)}, nil)
				codes <- result{"update", x.status, x.body}
			}()
		}
		wg.Wait()
		close(codes)
		for c := range codes {
			if c.status != 200 {
				t.Errorf("%s failed: %d %.200s", c.what, c.status, c.body)
			}
		}
		got := e.revisions(t, o.ws, note)
		if len(got) < 1+2*restores {
			t.Fatalf("each restore keeps before_restore and restore versions (>=%d), got %v", 1+2*restores, got)
		}
		for i, n := range got {
			if n != i+1 {
				t.Fatalf("revision numbers must be 1..n without gaps or repeats, got %v", got)
			}
		}
	})
}

// RV-006: restoring needs edit access to the page.
func TestQARestoreNeedsEditAccess(t *testing.T) {
	e := newEnv(t)
	o := e.signup("RevAuth", true)
	other := e.signup("RevOther", true)
	viewer := e.addMember(o, e.signup("Viewer", false), "member")
	guest := e.addMember(o, e.signup("Guest", false), "guest")
	note := e.note(o, "guarded")
	e.must(o, "note.update", map[string]any{"id": note, "content_md": "original"})
	e.must(o, "note.restrict", map[string]any{"note_id": note, "restriction": "edit"})
	e.must(o, "note.share", map[string]any{"note_id": note, "email": guest.email, "level": "view"})
	var list []map[string]any
	_ = jsonUnmarshal(e.cmd(e.rust, o, o.ws, "note.revisions", map[string]any{"note_id": note}, nil).body, &list)
	rev := list[0]["id"].(string)
	in := map[string]any{"note_id": note, "revision_id": rev}
	for name, c := range map[string]struct {
		w      who
		ws     string
		status []int
	}{
		"member on an edit-restricted page": {viewer, o.ws, []int{403, 404}},
		"guest with a view share":           {guest, o.ws, []int{403, 404}},
		"other tenant":                      {other, o.ws, []int{403}},
		"other tenant in its own workspace": {other, other.ws, []int{404}},
		"no credentials":                    {who{}, o.ws, []int{401}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, base := range []string{e.gosv.URL, e.rust} {
				r := e.cmd(base, c.w, c.ws, "note.restore", in, nil)
				ok := false
				for _, s := range c.status {
					ok = ok || r.status == s
				}
				if !ok {
					t.Fatalf("%s: want %v, got %d %s", base, c.status, r.status, r.body)
				}
			}
		})
	}
	if got := e.revisions(t, o.ws, note); len(got) != 1 {
		t.Fatalf("a refused restore left versions behind: %v", got)
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
