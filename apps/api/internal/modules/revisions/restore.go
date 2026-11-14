package revisions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// note.restore renders the live Yjs document through the collab service: POST {COLLAB_URL}/internal/render with
// the rows read in this transaction (snapshot, log) and the shared secret COLLAB_INTERNAL_SECRET. The endpoint is
// stateless and never reached through the public front (only /collab/ is forwarded). Without both variables the
// command answers an internal error naming them.
func restoreCommand() command.Command {
	c := command.NewCommand("note.restore", command.GroupWriteNotes, restore)
	c.MinRole = command.Guest // edit access to the page is checked per note
	return c
}

var renderClient = &http.Client{Timeout: 30 * time.Second}

func renderURL() (string, string, bool) {
	base, secret := os.Getenv("COLLAB_URL"), os.Getenv("COLLAB_INTERNAL_SECRET")
	if base == "" || secret == "" {
		return "", "", false
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", "", false
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	return strings.TrimRight(u.String(), "/") + "/internal/render", secret, true
}

func renderLive(c *command.Ctx, note, mdText string, ydoc []byte, updates [][]byte) (string, error) {
	endpoint, secret, ok := renderURL()
	if !ok {
		return "", command.NewError(command.Internal, "note.restore needs the collab service: set COLLAB_URL and COLLAB_INTERNAL_SECRET")
	}
	req := struct {
		NoteID  string   `json:"note_id"`
		MD      string   `json:"content_md"`
		Ydoc    *string  `json:"content_ydoc"`
		Updates []string `json:"updates"`
	}{NoteID: note, MD: mdText, Updates: []string{}}
	if len(ydoc) > 0 {
		s := base64.StdEncoding.EncodeToString(ydoc)
		req.Ydoc = &s
	}
	for _, u := range updates {
		req.Updates = append(req.Updates, base64.StdEncoding.EncodeToString(u))
	}
	body, _ := json.Marshal(req)
	hr, err := http.NewRequestWithContext(c.Context, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("X-Collab-Secret", secret)
	resp, err := renderClient.Do(hr)
	if err != nil {
		return "", command.NewError(command.Internal, "collab service unreachable")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var out struct {
		Markdown *string `json:"markdown"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(b, &out) != nil || out.Markdown == nil {
		return "", command.NewError(command.Internal, fmt.Sprintf("collab render failed (%d)", resp.StatusCode))
	}
	return *out.Markdown, nil
}

func insertRev(c *command.Ctx, note, content, reason string, from *string) (string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context,
		`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, restored_from, created_by)
		 select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions r where r.note_id = n.id), 0) + 1,
		        n.title, $2, $3, $4::uuid, $5::uuid
		 from core.notes n where n.id = $1::uuid
		 returning id::text`, note, content, reason, from, c.Principal.UserID).Scan(&id)
	return id, err
}

func restore(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	revID, err := o.ReqUUID("revision_id")
	if err != nil {
		return nil, err
	}
	expected, err := o.OptInt32("expected_epoch")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, noteID, true); err != nil {
		return nil, err
	}
	src, err := load(c, revID)
	if err != nil {
		return nil, err
	}
	if src.NoteID != noteID {
		return nil, command.ErrNotFound("version")
	}
	// Lock the page: concurrent restores and collab snapshots of this page serialise here.
	var epoch int32
	if err := c.Tx.QueryRow(c.Context, "select collab_epoch from core.notes where id = $1::uuid for update", noteID).Scan(&epoch); err != nil {
		return nil, err
	}
	if expected != nil && *expected != int64(epoch) {
		return nil, command.ErrConflict("the page was restored by someone else since you opened it")
	}
	// The live content: last snapshot plus every logged edit not yet folded in.
	var mdText string
	var ydoc []byte
	if err := c.Tx.QueryRow(c.Context, "select content_md, content_ydoc, collab_epoch from core.notes where id = $1::uuid and deleted_at is null", noteID).
		Scan(&mdText, &ydoc, &epoch); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("note")
	} else if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select update from core.note_updates where note_id = $1::uuid and epoch = $2 order by id", noteID, epoch)
	if err != nil {
		return nil, err
	}
	var updates [][]byte
	for rows.Next() {
		var u []byte
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return nil, err
		}
		updates = append(updates, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	live, err := renderLive(c, noteID, mdText, ydoc, updates)
	if err != nil {
		return nil, err
	}
	if _, err := insertRev(c, noteID, live, "before_restore", nil); err != nil {
		return nil, err
	}
	content := ""
	if src.ContentMD != nil {
		content = *src.ContentMD
	}
	var version int32
	if err := c.Tx.QueryRow(c.Context,
		`update core.notes set content_md = $2, content_ydoc = null, collab_epoch = collab_epoch + 1,
		   version = version + 1, updated_at = now()
		 where id = $1::uuid returning version, collab_epoch`, noteID, content).Scan(&version, &epoch); err != nil {
		return nil, err
	}
	// Absorbed into the before_restore version above; the new epoch starts with an empty log.
	if _, err := c.Tx.Exec(c.Context, "delete from core.note_updates where note_id = $1::uuid", noteID); err != nil {
		return nil, err
	}
	rid, err := insertRev(c, noteID, content, "restore", &src.ID)
	if err != nil {
		return nil, err
	}
	if _, err := c.Emit("note.restored", &noteID, map[string]any{"from_revision": src.Number, "version": version, "collab_epoch": epoch}); err != nil {
		return nil, err
	}
	rev, err := load(c, rid)
	if err != nil {
		return nil, err
	}
	rev.ContentMD = nil
	return struct {
		NoteID      string    `json:"note_id"`
		CollabEpoch int32     `json:"collab_epoch"`
		Version     int32     `json:"version"`
		Revision    *revision `json:"revision"`
	}{noteID, epoch, version, rev}, nil
}
