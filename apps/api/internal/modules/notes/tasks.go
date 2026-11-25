package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/notify"
)

// Tasks with an assignee and a due date (docs-parity-b 4.4, 13.2.6). A task is a `- [ ]` item; its assignee is the first
// mention in the item and its due date the first date chip. The index (core.note_tasks) is rebuilt per page by IndexTasks, from
// the worker (note.created and note.updated, API and gateway alike) and from the backfill sweep. "My tasks" and the digest read
// the index with the access predicate inside the query.

func init() { b1Handlers["task.mine"] = taskMine }

// Task is one parsed to-do item.
type Task struct {
	Ord      int
	Text     string // plain text, at most 300 characters, mention and date chips removed
	Assignee string // user id, lower case; empty when the item has no mention
	Due      string // YYYY-MM-DD; empty when the item has no date
	Done     bool
}

var (
	taskLineRe = regexp.MustCompile(`^\s*[-*+] \[([ xX])\] ?(.*)$`)
	taskDateRe = regexp.MustCompile(`<time datetime="(\d{4}-\d{2}-\d{2})"[^>]*>[^<]*</time>`)
	taskLinkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	taskWSRe   = regexp.MustCompile(`\s+`)
	// A mention chip as the editor writes it: [@Name](/w/<ws>/people/<user>).
	mentionLinkRe = regexp.MustCompile(`\[@[^\]]*\]\(/w/[0-9a-fA-F-]{36}/people/[0-9a-fA-F-]{36}\)`)
)

// Tasks parses the to-do items of a page body. ws is the workspace id: only mentions of this workspace's people count. Code
// fences are skipped; a bullet without a checkbox is not a task. Items are numbered in document order, nested ones included.
func Tasks(md, ws string) []Task {
	ws = strings.ToLower(ws)
	var out []Task
	fence := ""
	for _, line := range strings.Split(md, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			switch {
			case fence == "":
				fence = t[:3]
			case strings.HasPrefix(t, fence):
				fence = ""
			}
			continue
		}
		m := taskLineRe.FindStringSubmatch(line)
		if fence != "" || m == nil {
			continue
		}
		body := m[2]
		t := Task{Ord: len(out), Done: m[1] != " "}
		if us := mentions(body, ws); len(us) > 0 {
			// The first mention in reading order, not the first in a set.
			t.Assignee = us[0]
		}
		if d := taskDateRe.FindStringSubmatch(body); d != nil {
			if _, err := time.Parse("2006-01-02", d[1]); err == nil {
				t.Due = d[1]
			}
		}
		body = taskDateRe.ReplaceAllString(body, " ")
		body = mentionLinkRe.ReplaceAllString(body, " ")
		body = taskLinkRe.ReplaceAllString(body, "$1")
		t.Text = truncRunes(strings.TrimSpace(taskWSRe.ReplaceAllString(body, " ")), 300)
		out = append(out, t)
	}
	return out
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

// taskHash is the identity of a task's text for assignment notifications: lower case, spaces collapsed.
func taskHash(text string) string {
	h := sha256.Sum256([]byte(strings.ToLower(taskWSRe.ReplaceAllString(strings.TrimSpace(text), " "))))
	return hex.EncodeToString(h[:])
}

// IndexTasks rebuilds one page's rows in core.note_tasks from its current body, under a per-page advisory lock, and only when the
// page's version is newer than the one the index was built from (core.note_task_state), so a reordered, retried or racing
// rebuild (live event, retry, backfill) is harmless: whoever takes the lock second finds the version done. It reads the page row
// after taking the lock, so a late event for an old version indexes the latest body.
//
// A task whose identity (note, assignee, sha256 of the normalised text) is new is recorded in core.note_task_notified and, when
// notifyFirst allows (or the page already had an index), the assignee gets one `assigned` notification, unless they are the
// actor, cannot read the page, or were told about this same version by the mention path (13.2.10: one person, one edit, one
// notification). actor is the person who edited, or nil. A page that had no index yet (it predates this feature) is seeded
// without notifying unless notifyFirst is true, so the first edit of an old page does not announce its whole history.
func IndexTasks(ctx context.Context, tx pgx.Tx, ws, noteID string, actor *string, notifyFirst bool) error {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended('notes.tasks:' || $1::text, 0))", noteID); err != nil {
		return err
	}
	var title, md string
	var version int
	var plain bool
	err := tx.QueryRow(ctx, "select title, content_md, version, cardinality(types) = 0 from core.notes where id = $1::uuid and deleted_at is null", noteID).
		Scan(&title, &md, &version, &plain)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var had int
	err = tx.QueryRow(ctx, "select indexed_version from core.note_task_state where note_id = $1::uuid", noteID).Scan(&had)
	seen := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if seen && had >= version {
		return nil
	}
	var tasks []Task
	if plain {
		tasks = Tasks(md, ws)
	}
	if _, err := tx.Exec(ctx, "delete from core.note_tasks where note_id = $1::uuid", noteID); err != nil {
		return err
	}
	for _, t := range tasks {
		// An assignee who is not a member of the workspace is stored as null.
		if _, err := tx.Exec(ctx,
			`insert into core.note_tasks (workspace_id, note_id, ord, text, assignee_id, due, done, indexed_version)
			 values ($1::uuid, $2::uuid, $3, $4, (select user_id from core.memberships where workspace_id = $1::uuid and user_id = nullif($5, '')::uuid),
			         nullif($6, '')::date, $7, $8)`, ws, noteID, t.Ord, t.Text, t.Assignee, t.Due, t.Done, version); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`insert into core.note_task_state (workspace_id, note_id, indexed_version) values ($1::uuid, $2::uuid, $3)
		 on conflict (note_id) do update set indexed_version = excluded.indexed_version, updated_at = now()`, ws, noteID, version); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "select assignee_id::text, text from core.note_tasks where note_id = $1::uuid and assignee_id is not null order by ord", noteID)
	if err != nil {
		return err
	}
	type pair struct{ user, text string }
	var assigned []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.user, &p.text); err != nil {
			return err
		}
		assigned = append(assigned, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	loud := seen || notifyFirst
	key := fmt.Sprintf("note:%s:v%d", noteID, version)
	for _, p := range assigned {
		if loud {
			var ok bool
			if err := tx.QueryRow(ctx, "select "+notify.ReadableExpr("$1::uuid", "$2::uuid"), p.user, noteID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				continue // not recorded: they are told when they can read it and the task is next indexed as new
			}
		}
		tag, err := tx.Exec(ctx,
			`insert into core.note_task_notified (workspace_id, note_id, assignee_id, text_hash) values ($1::uuid, $2::uuid, $3::uuid, $4)
			 on conflict do nothing`, ws, noteID, p.user, taskHash(p.text))
		if err != nil {
			return err
		}
		if !loud || tag.RowsAffected() == 0 || (actor != nil && strings.EqualFold(*actor, p.user)) {
			continue
		}
		var told bool
		if err := tx.QueryRow(ctx, "select exists(select 1 from core.notifications where user_id = $1::uuid and note_id = $2::uuid and kind = 'mention' and event_key = $3)",
			p.user, noteID, key).Scan(&told); err != nil {
			return err
		}
		if told {
			continue
		}
		if _, err := notify.Send(ctx, tx, notify.Notification{WorkspaceID: ws, UserID: p.user, Kind: "assigned", Title: title, NoteID: &noteID, ActorUserID: actor,
			EventKey: &key}); err != nil {
			return err
		}
	}
	return nil
}

// TaskRow is one row of "My tasks".
type TaskRow struct {
	NoteID    string  `json:"note_id"`
	NoteTitle string  `json:"note_title"`
	Icon      *string `json:"icon"`
	SpaceID   string  `json:"space_id"`
	Ord       int     `json:"ord"`
	Text      string  `json:"text"`
	Due       *string `json:"due"`
	Done      bool    `json:"done"`
}

// TaskFilter picks which of a person's tasks to list.
type TaskFilter string

const (
	TasksOpen    TaskFilter = "open"    // not done
	TasksOverdue TaskFilter = "overdue" // not done, due before today
	TasksWeek    TaskFilter = "week"    // not done, due from today to six days later
	tasksDueBy   TaskFilter = "dueby"   // not done, due today or earlier (the digest)
	TasksDone    TaskFilter = "done"    // done only
	TasksAll     TaskFilter = "all"     // open and done
)

// UserTasks lists a person's tasks, judged with their own access, inside the query and before the limit: a page they cannot
// read (restricted, in the trash, archived freshness) is not listed and not counted. today is YYYY-MM-DD in the person's day.
// Overdue first, then by due date, undated last, done ones after open ones.
func UserTasks(ctx context.Context, tx pgx.Tx, user string, f TaskFilter, today string, limit int) ([]TaskRow, error) {
	rows, err := tx.Query(ctx,
		`select t.note_id::text, n.title, n.icon, n.space_id::text, t.ord, t.text, t.due::text, t.done
		 from core.note_tasks t join core.notes n on n.id = t.note_id and n.workspace_id = t.workspace_id
		 where t.assignee_id = $1::uuid and n.deleted_at is null and n.freshness <> 'archived'
		   and case $2::text when 'done' then t.done when 'all' then true else not t.done end
		   and ($2::text <> 'overdue' or t.due < $3::date)
		   and ($2::text <> 'week' or t.due between $3::date and $3::date + 6)
		   and ($2::text <> 'dueby' or t.due <= $3::date)
		   and `+notify.ReadableExpr("$1::uuid", "t.note_id")+`
		 order by t.done, t.due nulls last, n.title, t.note_id, t.ord limit $4::int`, user, string(f), today, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskRow{}
	for rows.Next() {
		var r TaskRow
		if err := rows.Scan(&r.NoteID, &r.NoteTitle, &r.Icon, &r.SpaceID, &r.Ord, &r.Text, &r.Due, &r.Done); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TasksDueBy is the digest's list (P3): a person's open tasks due today or earlier, overdue first, with the same access predicate
// as task.mine. today is YYYY-MM-DD in the person's day.
func TasksDueBy(ctx context.Context, tx pgx.Tx, user, today string, limit int) ([]TaskRow, error) {
	return UserTasks(ctx, tx, user, tasksDueBy, today, limit)
}

func taskMine(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Done  bool `json:"done"`
		Limit *int `json:"limit"`
	}
	if len(input) > 0 && json.Unmarshal(input, &in) != nil {
		return nil, command.ErrInvalid("done must be true or false and limit a number")
	}
	uid, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	limit := 100
	if in.Limit != nil {
		limit = min(max(*in.Limit, 1), 200)
	}
	f := TasksOpen
	if in.Done {
		f = TasksAll
	}
	return UserTasks(c.Context, c.Tx, uid, f, time.Now().UTC().Format("2006-01-02"), limit)
}
