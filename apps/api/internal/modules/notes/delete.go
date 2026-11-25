package notes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.delete (docs-parity 7.9 steps 1 to 6 with the coordinated fold of 7.10.4). Order of work, which matters:
//  1. page access, the tree lock, the subtree, the edit rule on every page (no row locks yet);
//  2. every page of the subtree is folded by the collab gateway with a fence (its own transactions, which commit),
//     so the room accepts no more writes. The gateway needs the page rows, so this runs before this transaction locks them;
//  3. rows FOR UPDATE, then a second look: still unfolded edits (a frame logged in between) refuse the delete, never strand them;
//  4. one UPDATE with the deletion id, the actor, the parent and the restricted ancestors (restore evidence), a checkpoint
//     revision from the folded content and one note.deleted event per page.
func noteDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	space, err := access.AssertNoteAccess(c, id, true)
	if err != nil {
		return nil, err
	}
	if err := lockTree(c, space); err != nil {
		return nil, err
	}
	sub, _, err := subtree(c, id)
	if err != nil {
		return nil, err
	}
	for _, p := range sub[1:] {
		if len(p.Types) > 0 {
			return nil, command.ErrInvalid("This page has items that cannot be deleted with it")
		}
	}
	ids := subIDs(sub)
	if ok, err := canEdit(c, space, ids, false); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRestrictedIn(c, space)
	}
	for _, p := range ids { // every page is fenced, pending edits or not: the fence is what keeps a racing edit out
		if err := foldNote(c, p, true); err != nil {
			return nil, err
		}
	}
	if err := lockRows(c, ids); err != nil {
		return nil, err
	}
	if again, err := pendingPages(c, ids); err != nil {
		return nil, err
	} else if len(again) > 0 {
		return nil, command.ErrConflict(CouldntSaveMessage)
	}
	var did string
	if err := c.Tx.QueryRow(c.Context, "select gen_random_uuid()::text").Scan(&did); err != nil {
		return nil, command.DB(err)
	}
	if err := checkpointMany(c, ids); err != nil { // before the pages leave the live set; content is the folded content
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`with recursive up as (
		   select n.id origin, p.id node, p.parent_id nxt, p.restriction r, 1 d
		     from core.notes n join core.notes p on p.id = n.parent_id where n.id = any($1::text[]::uuid[])
		   union all
		   select up.origin, p.id, p.parent_id, p.restriction, up.d + 1 from up join core.notes p on p.id = up.nxt where up.d < 51),
		 ev as (select origin, coalesce(array_agg(node order by d) filter (where r <> 'none'), '{}'::uuid[]) arr from up group by origin)
		 update core.notes n set deleted_at = now(), deletion_id = $2::uuid, deleted_by = $3::uuid, updated_at = now(),
		   fenced_until = null, deleted_parent_id = n.parent_id, deleted_restricted_by = coalesce(ev.arr, '{}'::uuid[])
		 from (select id from core.notes where id = any($1::text[]::uuid[])) s left join ev on ev.origin = s.id
		 where n.id = s.id`, ids, did, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	for _, p := range ids {
		p := p
		if _, err := c.Emit("note.deleted", &p, map[string]any{"deletion_id": did, "root_id": id, "count": len(ids)}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"deleted": true, "deletion_id": did, "count": len(ids)}, nil
}

// checkpointMany is checkpoint for many pages in one statement, with no minimum gap: the content at deletion is in history.
func checkpointMany(c *command.Ctx, ids []string) error {
	_, err := c.Tx.Exec(c.Context,
		`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, created_by)
		 select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions r where r.note_id = n.id), 0) + 1,
		        n.title, n.content_md, 'edit', $2::uuid
		 from core.notes n
		 where n.id = any($1::text[]::uuid[]) and n.content_md <> ''
		   and n.content_md is distinct from (select content_md from core.note_revisions r where r.note_id = n.id order by number desc limit 1)`,
		ids, c.Principal.UserID)
	return command.DB(err)
}

// pendingPages are the pages with edits in the live-editing log that no snapshot has absorbed yet (current epoch).
func pendingPages(c *command.Ctx, ids []string) ([]string, error) {
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text from core.notes n where n.id = any($1::text[]::uuid[])
		   and exists (select 1 from core.note_updates u where u.note_id = n.id and u.epoch = n.collab_epoch) order by n.id`, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

var foldClient = &http.Client{Timeout: 20 * time.Second}

// foldNote asks the collab gateway (POST {COLLAB_URL}/internal/fold, secret COLLAB_INTERNAL_SECRET) to fold the page's snapshot
// plus its logged edits into content_md and the snapshot. With fence the room also stops accepting writes first and stays closed
// to them until the page is seen deleted (or a minute passes). It works with no open room. Any failure is the one user-facing
// refusal: nothing is deleted or copied from a page whose latest edits could not be saved.
func foldNote(c *command.Ctx, note string, fence bool) error {
	base, secret := os.Getenv("COLLAB_URL"), os.Getenv("COLLAB_INTERNAL_SECRET")
	u, err := url.Parse(base)
	if base == "" || secret == "" || err != nil {
		return command.ErrConflict(CouldntSaveMessage)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	body, _ := json.Marshal(map[string]any{"note_id": note, "workspace_id": c.WorkspaceID(), "fence": fence})
	req, err := http.NewRequestWithContext(c.Context, http.MethodPost, strings.TrimRight(u.String(), "/")+"/internal/fold", bytes.NewReader(body))
	if err != nil {
		return command.ErrConflict(CouldntSaveMessage)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Collab-Secret", secret)
	resp, err := foldClient.Do(req)
	if err != nil {
		return command.ErrConflict(CouldntSaveMessage)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return command.ErrConflict(CouldntSaveMessage)
	}
	return nil
}
