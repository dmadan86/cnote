// Package files: a minimal workspace-scoped file store on core.attachments. A file hangs on a page (a receipt photo on
// an expense, a scan on a bill). Go-only. Uploads and downloads travel as base64 in the command envelope so every gate
// (role, scopes, agent groups, rate limit) applies; the HTTP endpoints in the server wrap them for raw bytes.
//
// shortcut: bytes live in Postgres (bytea, 10 MB cap, whole file in memory); move to object storage when hosting is decided.
package files

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

const (
	MaxBytes     = 10 << 20
	maxPerNote   = 10
	maxNameRunes = 200
)

// allowed content types, judged from the bytes; the client's Content-Type and file extension are never trusted.
var allowed = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "application/pdf": true}

// Sniff returns the content type of data when it is on the allow-list.
func Sniff(data []byte) (string, bool) {
	ct, _, _ := strings.Cut(http.DetectContentType(data), ";")
	return ct, allowed[ct]
}

func Commands() []command.Command {
	nc := command.NewCommand
	return []command.Command{
		nc("file.upload", command.GroupWriteNotes, upload),
		nc("file.list", command.GroupRead, list),
		nc("file.download", command.GroupRead, download),
		nc("file.delete", command.GroupWriteNotes, remove),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type in struct {
	ID       string `json:"id"`
	NoteID   string `json:"note_id"`
	Filename string `json:"filename"`
	Data     string `json:"data_base64"`
}

func decode(raw json.RawMessage, needNote, needID bool) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, f := range []struct {
		s    string
		need bool
	}{{v.NoteID, needNote}, {v.ID, needID}} {
		if (f.need && f.s == "") || (f.s != "" && !uuidRe.MatchString(f.s)) {
			return v, command.ErrInvalid("note_id / id must be a UUID")
		}
	}
	v.NoteID, v.ID = strings.ToLower(v.NoteID), strings.ToLower(v.ID)
	return v, nil
}

type meta struct {
	ID          string     `json:"id"`
	NoteID      string     `json:"note_id"`
	Filename    string     `json:"filename"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	CreatedAt   command.Ts `json:"created_at"`
}

// cleanName keeps the base name, drops control characters and caps the length.
func cleanName(s string) string {
	s = path.Base(strings.ReplaceAll(strings.TrimSpace(s), `\`, "/"))
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' {
			return -1
		}
		return r
	}, s)
	if s == "." || s == "/" || s == "" {
		return "file"
	}
	if utf8.RuneCountInString(s) > maxNameRunes {
		s = string([]rune(s)[:maxNameRunes])
	}
	return s
}

// guard decides whether the caller may read or change files on a page. The page must be visible to them; a finance
// document (invoice, quote, bill, receipt voucher, recurring invoice) also needs finance access; an expense is
// visible to its claimant, to admins and, for a company-paid one, to people with finance access. Files on an expense
// claim change only while the claim is a draft or was rejected.
func guard(c *command.Ctx, noteID string, write bool) error {
	if _, err := access.AssertNoteAccess(c, noteID, write); err != nil {
		return err
	}
	var isExp, otherFin bool
	var kind, status *string
	var claimant *string
	err := c.Tx.QueryRow(c.Context,
		`select e.note_id is not null, e.kind, e.status, e.paid_by_user_id::text,
		   exists(select 1 from billing.invoices where note_id = $1::uuid) or exists(select 1 from billing.quotations where note_id = $1::uuid)
		   or exists(select 1 from billing.purchase_bills where note_id = $1::uuid) or exists(select 1 from billing.receipts where note_id = $1::uuid)
		   or exists(select 1 from billing.recurring_invoices where note_id = $1::uuid)
		 from (select 1) x left join billing.expenses e on e.note_id = $1::uuid`, noteID).Scan(&isExp, &kind, &status, &claimant, &otherFin)
	if err != nil {
		return command.DB(err)
	}
	if !isExp && !otherFin {
		return nil
	}
	fin, err := c.FinanceAllowed()
	if err != nil {
		return err
	}
	if !isExp {
		if !fin {
			return command.ErrForbidden("finance access is turned off for you; ask an admin")
		}
		return nil
	}
	own := claimant != nil && c.Principal.UserID != nil && *claimant == *c.Principal.UserID
	admin := c.Principal.Role >= command.Admin
	if !admin && !own && !(*kind == "direct" && fin) {
		return command.ErrNotFound("note")
	}
	if write && *kind == "claim" && *status != "draft" && *status != "rejected" {
		return command.ErrConflict("receipts can only change while the claim is a draft or rejected")
	}
	return nil
}

func upload(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input, true, false)
	if err != nil {
		return nil, err
	}
	if base64.StdEncoding.DecodedLen(len(v.Data)) > MaxBytes+3 {
		return nil, command.ErrInvalid("file is larger than 10 MB")
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	switch {
	case err != nil:
		return nil, command.ErrInvalid("data_base64 is not valid base64")
	case len(data) == 0:
		return nil, command.ErrInvalid("file is empty")
	case len(data) > MaxBytes:
		return nil, command.ErrInvalid("file is larger than 10 MB")
	}
	ct, ok := Sniff(data)
	if !ok {
		return nil, command.ErrInvalid("only images (JPEG, PNG, GIF, WebP) and PDF files are accepted")
	}
	if err := guard(c, v.NoteID, true); err != nil {
		return nil, err
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from core.attachments where note_id = $1::uuid and data is not null", v.NoteID).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if n >= maxPerNote {
		return nil, command.ErrConflict("a page can hold at most 10 files")
	}
	var m meta
	var created time.Time
	err = c.Tx.QueryRow(c.Context,
		`insert into core.attachments (workspace_id, note_id, filename, content_type, size_bytes, storage_key, data, created_by)
		 values ($1::uuid,$2::uuid,$3,$4,$5,'pg',$6,$7::uuid)
		 returning id::text, note_id::text, filename, content_type, size_bytes, created_at`,
		c.WorkspaceID(), v.NoteID, cleanName(v.Filename), ct, len(data), data, c.Principal.UserID).
		Scan(&m.ID, &m.NoteID, &m.Filename, &m.ContentType, &m.SizeBytes, &created)
	if err != nil {
		return nil, command.DB(err)
	}
	m.CreatedAt = command.Ts(created)
	if _, err := c.Emit("file.uploaded", &v.NoteID, map[string]any{"file_id": m.ID, "size_bytes": m.SizeBytes, "content_type": ct}); err != nil {
		return nil, err
	}
	return m, nil
}

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input, true, false)
	if err != nil {
		return nil, err
	}
	if err := guard(c, v.NoteID, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, note_id::text, filename, content_type, size_bytes, created_at from core.attachments
		 where note_id = $1::uuid and data is not null order by created_at, id`, v.NoteID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []meta{}
	for rows.Next() {
		var m meta
		var t time.Time
		if err := rows.Scan(&m.ID, &m.NoteID, &m.Filename, &m.ContentType, &m.SizeBytes, &t); err != nil {
			return nil, command.DB(err)
		}
		m.CreatedAt = command.Ts(t)
		out = append(out, m)
	}
	return out, command.DB(rows.Err())
}

func noteOf(c *command.Ctx, id string) (string, error) {
	var note string
	err := c.Tx.QueryRow(c.Context, "select note_id::text from core.attachments where id = $1::uuid and data is not null", id).Scan(&note)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("file")
	}
	return note, command.DB(err)
}

func download(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input, false, true)
	if err != nil || v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	note, err := noteOf(c, v.ID)
	if err != nil {
		return nil, err
	}
	if err := guard(c, note, false); err != nil {
		if e := (*command.Error)(nil); errors.As(err, &e) && e.Code == command.NotFound {
			return nil, command.ErrNotFound("file")
		}
		return nil, err
	}
	var name, ct string
	var data []byte
	if err := c.Tx.QueryRow(c.Context, "select filename, content_type, data from core.attachments where id = $1::uuid", v.ID).Scan(&name, &ct, &data); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"id": v.ID, "filename": name, "content_type": ct, "data_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func remove(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input, false, true)
	if err != nil || v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	note, err := noteOf(c, v.ID)
	if err != nil {
		return nil, err
	}
	if err := guard(c, note, true); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.attachments where id = $1::uuid", v.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("file.deleted", &note, map[string]any{"file_id": v.ID}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
