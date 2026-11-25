package files

// Phase 1b: files on a Tables record's attachment field (design 18.1, revised by 18.8). Internal commands only, called by
// the tables module with c.Call after it has checked the base role and the plan's storage cap. The ownership checks
// live here, because this is where the rows are: every command joins the whole chain workspace -> base -> table ->
// field / record, and a handle is attachable only by the principal who staged it. Nothing here trusts the caller's
// input for an owner, a size or a count: those are the principal and the constants below.
//
// Lock order is base, then record. A command that takes a base lock (every stage, detach, discard and sweep does) takes
// it before any record lock, and bases.attachment_bytes changes only while the base row is held FOR UPDATE.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	MaxRecordFiles     = 10 // files in one attachment cell
	maxPendingPerOwner = 20 // unattached uploads one owner may hold in one base
	gcBatch            = 200
	gcWarnTries        = 10 // from this many failures a stuck object is logged on every attempt
)

// recordTypes is the whole allow-list of record attachments (images and PDF). It is separate from the shared
// file.upload list, which does not change.
var recordTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "application/pdf": true}

// RecordPolicy judges and serves record attachments.
var RecordPolicy recordPolicy

type recordPolicy struct{}

// Judge returns the content type of data, judged from the bytes, or an error when it is empty, too large or not an image or PDF.
func (recordPolicy) Judge(data []byte) (string, error) {
	switch {
	case len(data) == 0:
		return "", command.ErrInvalid("file is empty")
	case len(data) > MaxBytes:
		return "", command.ErrInvalid("file is larger than 10 MB")
	}
	ct, _, _ := strings.Cut(http.DetectContentType(data), ";")
	if !recordTypes[ct] {
		return "", command.ErrInvalid("only images (JPEG, PNG, GIF, WebP) and PDF files are accepted")
	}
	return ct, nil
}

// Name cleans a filename (base name, no control characters, 200 characters). CR or LF is refused, not stripped.
func (recordPolicy) Name(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return "", command.ErrInvalid("the file name cannot contain a line break")
	}
	return cleanName(raw), nil
}

// Serve decides how a stored file is shown: an image is inline only when its bytes still sniff as one of the four image
// types (and then that sniffed type is the Content-Type); everything else, PDF included, is an attachment.
func (recordPolicy) Serve(stored string, data []byte) (ctype, disposition string) {
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	switch sniffed {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		if stored == sniffed {
			return sniffed, "inline"
		}
	case "application/pdf":
		if stored == sniffed {
			return sniffed, "attachment"
		}
	}
	return "application/octet-stream", "attachment"
}

// Headers writes the response headers of a record file.
func (p recordPolicy) Headers(h http.Header, name, stored string, data []byte) {
	ct, disp := p.Serve(stored, data)
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", ContentDisposition(disp, name))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
}

// ContentDisposition is `<disp>; filename="<ascii fallback>"; filename*=UTF-8”<RFC 5987 name>`.
func ContentDisposition(disp, name string) string {
	var fb strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r > 0x7e || r == '"' || r == '\\' || r == '%' {
			r = '_'
		}
		fb.WriteRune(r)
	}
	return disp + `; filename="` + fb.String() + `"; filename*=UTF-8''` + rfc5987(name)
}

func rfc5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// owner is the staging owner: it comes from the authenticated principal and from nowhere else.
func owner(p command.Principal) (string, error) {
	switch {
	case p.AgentID != nil:
		return "a:" + *p.AgentID, nil
	case p.UserID != nil:
		return "u:" + *p.UserID, nil
	}
	return "", command.ErrForbidden("file uploads need a signed-in person or an AI employee")
}

const (
	rID   = `{"type":"string","description":"UUID"}`
	rStr  = `{"type":"string"}`
	rInt  = `{"type":"integer"}`
	rMeta = `{"type":"object","properties":{"id":` + rID + `,"filename":` + rStr + `,"content_type":` + rStr + `,"size_bytes":` + rInt +
		`},"required":["id","filename","content_type","size_bytes"]}`
)

func recordCommands() []command.Command {
	type spec struct {
		name, desc, in, out string
		group               command.Group
		min                 command.Role
		h                   command.Handler
	}
	obj := func(req string, props string) string {
		return `{"type":"object","properties":{` + props + `},"required":[` + req + `]}`
	}
	ids := `{"type":"array","items":` + rID + `}`
	specs := []spec{
		{"file.record_intent", "Internal. Announce an object key for the next file.record_stage, committed in its own transaction before the bucket is written, so a write that is lost can be found and collected. Returns {object_id} (empty when bytes live in Postgres).",
			obj(``, ``), obj(`"object_id"`, `"object_id":`+rStr), command.GroupWriteItems, command.Member, recordIntent},
		{"file.record_stage", "Internal. Store an uploaded file as a pending row for an attachment field: judge the type from the bytes (images and PDF only), refuse above 10 MB, check the base, table and field agree, allow at most 20 unattached files per person and base, write the object, return the handle. The owner is the calling principal. The caller (tables) has checked the role and reserved the bytes. Returns {id, filename, content_type, size_bytes}.",
			obj(`"base_id","table_id","field_id","filename","data_base64"`,
				`"base_id":`+rID+`,"table_id":`+rID+`,"field_id":`+rID+`,"filename":`+rStr+`,"data_base64":`+rStr+`,"object_id":`+rID),
			rMeta, command.GroupWriteItems, command.Member, recordStage},
		{"file.record_attach", "Internal. Attach pending handles to a record's field: each must be pending, staged by the calling principal, staged for this record's table, field and base, and under 24 hours old, and the cell may hold at most 10 files after the call. Returns the attached files in order.",
			obj(`"record_id","field_id","handles"`, `"record_id":`+rID+`,"field_id":`+rID+`,"handles":`+ids),
			`{"type":"array","items":` + rMeta + `}`, command.GroupWriteItems, command.Member, recordAttach},
		{"file.record_detach", "Internal. Delete attached files of a record's field (their objects are removed by the sweep). Every id must belong to that record and field. Returns {freed_bytes}.",
			obj(`"record_id","field_id","ids"`, `"record_id":`+rID+`,"field_id":`+rID+`,"ids":`+ids),
			obj(`"freed_bytes"`, `"freed_bytes":`+rInt), command.GroupWriteItems, command.Member, recordDetach},
		{"file.record_get", "Internal. Read one attached file's bytes; the file must be attached to record_id, and the record, field, table and base must be live. Returns {id, filename, content_type, data_base64}.",
			obj(`"record_id","id"`, `"record_id":`+rID+`,"id":`+rID),
			obj(`"id","filename","content_type","data_base64"`, `"id":`+rID+`,"filename":`+rStr+`,"content_type":`+rStr+`,"data_base64":`+rStr),
			command.GroupRead, command.Guest, recordGet},
		{"file.record_discard", "Internal. Delete pending handles that the calling principal staged and never attached (a failed save, or a file removed before saving). Returns {freed_bytes}.",
			obj(`"ids"`, `"ids":`+ids),
			obj(`"freed_bytes"`, `"freed_bytes":`+rInt), command.GroupWriteItems, command.Member, recordDiscard},
	}
	out := make([]command.Command, len(specs))
	for i, s := range specs {
		c := command.NewCommand(s.name, s.group, s.h)
		c.Internal, c.MinRole = true, s.min
		c.Description, c.Schema = s.desc, json.RawMessage(s.in)
		out[i] = c
	}
	return out
}

func recDecode(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return command.ErrInvalid("invalid input")
	}
	return nil
}

func allUUID(ss ...string) bool {
	for _, s := range ss {
		if !uuidRe.MatchString(s) {
			return false
		}
	}
	return true
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// distinctUUIDs lowercases ids, refuses a non-UUID or a repeat, and caps the count.
func distinctUUIDs(ids []string, max int) ([]string, error) {
	ids = lowerAll(ids)
	if len(ids) == 0 || len(ids) > max || !allUUID(ids...) {
		return nil, command.ErrInvalid(fmt.Sprintf("send between 1 and %d ids", max))
	}
	s := slices.Clone(ids)
	slices.Sort(s)
	if len(slices.Compact(s)) != len(ids) {
		return nil, command.ErrInvalid("an id appears twice")
	}
	return ids, nil
}

// lockBase takes the base row FOR UPDATE (the first lock of every command here). A missing or trashed base is not found.
func lockBase(ctx context.Context, tx pgx.Tx, ws, baseID string, live bool) error {
	q := `select 1 from tables.bases where workspace_id=$1::uuid and id=$2::uuid`
	if live {
		q += ` and deleted_at is null`
	}
	var one int
	err := tx.QueryRow(ctx, q+` for update`, ws, baseID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("base")
	}
	return command.DB(err)
}

// ReconcileBase sets bases.attachment_bytes to the sum of the base's record files (pending and attached). It takes the
// base row lock first, like every change of that counter. A base that is gone is skipped.
func ReconcileBase(ctx context.Context, tx pgx.Tx, ws, baseID string) error {
	if err := lockBase(ctx, tx, ws, baseID, false); err != nil {
		if e := (*command.Error)(nil); errors.As(err, &e) && e.Code == command.NotFound {
			return nil
		}
		return err
	}
	_, err := tx.Exec(ctx, `update tables.bases set attachment_bytes = (select coalesce(sum(size_bytes), 0) from core.attachments
		where workspace_id=$1::uuid and base_id=$2::uuid and field_id is not null) where workspace_id=$1::uuid and id=$2::uuid`, ws, baseID)
	return command.DB(err)
}

// recordIntent announces a new object key in its own committed transaction (c.Pool), before anything is written to the
// bucket, and returns the future row id. With no bucket (bytes in Postgres) there is nothing to announce: it returns "".
// The caller (tables) has checked the role; the intent holds no data and is collected after 24 hours if never used.
func recordIntent(c *command.Ctx, _ json.RawMessage) (any, error) {
	if store == nil {
		return map[string]any{"object_id": ""}, nil
	}
	ws := c.WorkspaceID()
	tx, err := db.BeginTenant(c.Context, c.Pool, ws)
	if err != nil {
		return nil, command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(c.Context)) }()
	var id string
	if err := tx.QueryRow(c.Context, `with n as (select gen_random_uuid() as id) insert into core.file_objects (key, workspace_id, state)
		select $1::uuid::text || '/' || n.id::text, $1::uuid, 'intent' from n returning split_part(key, '/', 2)`, ws).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"object_id": id}, command.DB(tx.Commit(c.Context))
}

type stageIn struct {
	BaseID   string `json:"base_id"`
	TableID  string `json:"table_id"`
	FieldID  string `json:"field_id"`
	Filename string `json:"filename"`
	Data     string `json:"data_base64"`
	ObjectID string `json:"object_id"`
}

func recordStage(c *command.Ctx, raw json.RawMessage) (any, error) {
	var v stageIn
	if err := recDecode(raw, &v); err != nil {
		return nil, err
	}
	v.BaseID, v.TableID, v.FieldID, v.ObjectID = strings.ToLower(v.BaseID), strings.ToLower(v.TableID), strings.ToLower(v.FieldID), strings.ToLower(v.ObjectID)
	if !allUUID(v.BaseID, v.TableID, v.FieldID) || v.ObjectID != "" && !allUUID(v.ObjectID) {
		return nil, command.ErrInvalid("base_id, table_id, field_id and object_id must be UUIDs")
	}
	if base64.StdEncoding.DecodedLen(len(v.Data)) > MaxBytes+3 {
		return nil, command.ErrInvalid("file is larger than 10 MB")
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return nil, command.ErrInvalid("data_base64 is not valid base64")
	}
	ct, err := RecordPolicy.Judge(data)
	if err != nil {
		return nil, err
	}
	name, err := RecordPolicy.Name(v.Filename)
	if err != nil {
		return nil, err
	}
	who, err := owner(c.Principal)
	if err != nil {
		return nil, err
	}
	ws := c.WorkspaceID()
	if err := lockBase(c.Context, c.Tx, ws, v.BaseID, true); err != nil {
		return nil, err
	}
	var ok bool
	if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from tables.fields f
		join tables.tables t on t.workspace_id = f.workspace_id and t.id = f.table_id
		join tables.bases b on b.workspace_id = t.workspace_id and b.id = t.base_id
		where f.workspace_id=$1::uuid and f.id=$4::uuid and f.table_id=$3::uuid and t.base_id=$2::uuid and f.type='attachment'
		  and f.deleted_at is null and t.deleted_at is null and b.deleted_at is null)`, ws, v.BaseID, v.TableID, v.FieldID).Scan(&ok); err != nil {
		return nil, command.DB(err)
	}
	if !ok {
		return nil, command.ErrNotFound("field")
	}
	var pending int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from core.attachments where workspace_id=$1::uuid and base_id=$2::uuid and staged_by=$3 and status='pending'`,
		ws, v.BaseID, who).Scan(&pending); err != nil {
		return nil, command.DB(err)
	}
	if pending >= maxPendingPerOwner {
		return nil, command.ErrConflict("you have 20 uploads waiting to be attached in this base; save or remove some first")
	}
	key, stored := "pg", data
	if store != nil {
		key, stored = "s3", nil
		var announced bool
		if v.ObjectID != "" {
			// FOR UPDATE: the sweep claims intents with SKIP LOCKED, so an intent held by a staging transaction is never swept.
			if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from (select 1 from core.file_objects where key = $1::uuid::text || '/' || $2::uuid::text and workspace_id=$1::uuid and state='intent' for update) l)`,
				ws, v.ObjectID).Scan(&announced); err != nil {
				return nil, command.DB(err)
			}
		}
		if !announced {
			return nil, command.ErrInvalid("object_id was not announced")
		}
	}
	var id string
	err = c.Tx.QueryRow(c.Context, `insert into core.attachments (id, workspace_id, base_id, table_id, field_id, status, staged_by, filename, content_type, size_bytes, storage_key, data, created_by)
		values (coalesce(nullif($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'pending', $6, $7, $8, $9, $10, $11, $12::uuid) returning id::text`,
		v.ObjectID, ws, v.BaseID, v.TableID, v.FieldID, who, name, ct, len(data), key, stored, c.Principal.UserID).Scan(&id)
	if err != nil {
		return nil, command.DB(err)
	}
	if store != nil { // last: a failed write rolls the row (and the caller's reservation) back
		if err := store.Put(c.Context, ws+"/"+id, data); err != nil {
			return nil, fmt.Errorf("file storage: %w", err)
		}
	}
	return map[string]any{"id": id, "filename": name, "content_type": ct, "size_bytes": len(data)}, nil
}

// recordRef finds a live record's table and base through the whole chain and takes base then record locks.
func recordRef(c *command.Ctx, recordID, fieldID string) (baseID, tableID string, err error) {
	q := `select t.base_id::text, t.id::text from tables.records r
		join tables.tables t on t.workspace_id = r.workspace_id and t.id = r.table_id
		join tables.bases b on b.workspace_id = t.workspace_id and b.id = t.base_id
		join tables.fields f on f.workspace_id = r.workspace_id and f.table_id = r.table_id and f.id = $3::uuid and f.type = 'attachment'
		where r.workspace_id=$1::uuid and r.id=$2::uuid and r.deleted_at is null and t.deleted_at is null and b.deleted_at is null and f.deleted_at is null`
	if err = c.Tx.QueryRow(c.Context, q, c.WorkspaceID(), recordID, fieldID).Scan(&baseID, &tableID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", command.ErrNotFound("file")
		}
		return "", "", command.DB(err)
	}
	return baseID, tableID, nil
}

func lockBaseAndRecord(c *command.Ctx, baseID, tableID, recordID string) error {
	if err := lockBase(c.Context, c.Tx, c.WorkspaceID(), baseID, true); err != nil {
		return err
	}
	var one int
	err := c.Tx.QueryRow(c.Context, `select 1 from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and id=$3::uuid and deleted_at is null for update`,
		c.WorkspaceID(), tableID, recordID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("record")
	}
	return command.DB(err)
}

func recordAttach(c *command.Ctx, raw json.RawMessage) (any, error) {
	var v struct {
		RecordID string   `json:"record_id"`
		FieldID  string   `json:"field_id"`
		Handles  []string `json:"handles"`
	}
	if err := recDecode(raw, &v); err != nil {
		return nil, err
	}
	v.RecordID, v.FieldID = strings.ToLower(v.RecordID), strings.ToLower(v.FieldID)
	handles, err := distinctUUIDs(v.Handles, MaxRecordFiles)
	if err != nil || !allUUID(v.RecordID, v.FieldID) {
		return nil, command.ErrInvalid("record_id, field_id and handles must be UUIDs, at most 10 handles")
	}
	who, err := owner(c.Principal)
	if err != nil {
		return nil, err
	}
	baseID, tableID, err := recordRef(c, v.RecordID, v.FieldID)
	if err != nil {
		return nil, err
	}
	if err := lockBaseAndRecord(c, baseID, tableID, v.RecordID); err != nil {
		return nil, err
	}
	ws := c.WorkspaceID()
	var have int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from core.attachments where workspace_id=$1::uuid and record_id=$2::uuid and field_id=$3::uuid and status='attached'`,
		ws, v.RecordID, v.FieldID).Scan(&have); err != nil {
		return nil, command.DB(err)
	}
	if have+len(handles) > MaxRecordFiles {
		return nil, command.ErrConflict("a file field can hold at most 10 files")
	}
	// pending rows are locked as they are consumed; the whole chain and the owner must match, or the call fails and nothing changes
	rows, err := c.Tx.Query(c.Context, `update core.attachments set status='attached', record_id=$2::uuid
		where workspace_id=$1::uuid and id = any($6::uuid[]) and status='pending' and staged_by=$3 and base_id=$4::uuid and table_id=$5::uuid
		  and field_id=$7::uuid and created_at > now() - interval '24 hours'
		returning id::text, filename, content_type, size_bytes`,
		ws, v.RecordID, who, baseID, tableID, handles, v.FieldID)
	if err != nil {
		return nil, command.DB(err)
	}
	type meta struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	got := map[string]meta{}
	for rows.Next() {
		var m meta
		if err := rows.Scan(&m.ID, &m.Filename, &m.ContentType, &m.SizeBytes); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		got[m.ID] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(got) != len(handles) {
		return nil, command.ErrInvalid("an upload is missing, was already used, belongs to someone else or has expired (24 hours); upload it again")
	}
	out := make([]meta, 0, len(handles))
	for _, h := range handles {
		out = append(out, got[h])
	}
	if _, err := c.Tx.Exec(c.Context, `update core.file_objects set state='live' where workspace_id=$1::uuid and key = any($2::text[])`, ws,
		mapKeys(handles, func(h string) string { return ws + "/" + h })); err != nil {
		return nil, command.DB(err)
	}
	return out, nil
}

func mapKeys(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func recordDetach(c *command.Ctx, raw json.RawMessage) (any, error) {
	var v struct {
		RecordID string   `json:"record_id"`
		FieldID  string   `json:"field_id"`
		IDs      []string `json:"ids"`
	}
	if err := recDecode(raw, &v); err != nil {
		return nil, err
	}
	v.RecordID, v.FieldID = strings.ToLower(v.RecordID), strings.ToLower(v.FieldID)
	ids, err := distinctUUIDs(v.IDs, MaxRecordFiles)
	if err != nil || !allUUID(v.RecordID, v.FieldID) {
		return nil, command.ErrInvalid("record_id, field_id and ids must be UUIDs, at most 10 ids")
	}
	baseID, tableID, err := recordRef(c, v.RecordID, v.FieldID)
	if err != nil {
		return nil, err
	}
	if err := lockBaseAndRecord(c, baseID, tableID, v.RecordID); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `delete from core.attachments where workspace_id=$1::uuid and id = any($2::uuid[]) and record_id=$3::uuid and field_id=$4::uuid
		and base_id=$5::uuid and table_id=$6::uuid and status='attached' returning size_bytes`, c.WorkspaceID(), ids, v.RecordID, v.FieldID, baseID, tableID)
	if err != nil {
		return nil, command.DB(err)
	}
	n, freed := 0, int64(0)
	for rows.Next() {
		var sz int64
		if err := rows.Scan(&sz); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		n, freed = n+1, freed+sz
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if n != len(ids) {
		return nil, command.ErrInvalid("a file does not belong to this record field")
	}
	return map[string]any{"freed_bytes": freed}, nil
}

func recordGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var v struct {
		RecordID string `json:"record_id"`
		ID       string `json:"id"`
	}
	if err := recDecode(raw, &v); err != nil {
		return nil, err
	}
	v.RecordID, v.ID = strings.ToLower(v.RecordID), strings.ToLower(v.ID)
	if !allUUID(v.RecordID, v.ID) {
		return nil, command.ErrNotFound("file")
	}
	var name, ct, key string
	var data []byte
	err := c.Tx.QueryRow(c.Context, `select a.filename, a.content_type, a.storage_key, a.data from core.attachments a
		join tables.records r on r.workspace_id = a.workspace_id and r.table_id = a.table_id and r.id = a.record_id
		join tables.fields f on f.workspace_id = a.workspace_id and f.table_id = a.table_id and f.id = a.field_id
		join tables.tables t on t.workspace_id = a.workspace_id and t.base_id = a.base_id and t.id = a.table_id
		join tables.bases b on b.workspace_id = t.workspace_id and b.id = t.base_id
		where a.workspace_id=$1::uuid and a.id=$2::uuid and a.record_id=$3::uuid and a.status='attached' and f.type='attachment'
		  and r.deleted_at is null and f.deleted_at is null and t.deleted_at is null and b.deleted_at is null`,
		c.WorkspaceID(), v.ID, v.RecordID).Scan(&name, &ct, &key, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("file")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if key == "s3" {
		if store == nil {
			return nil, fmt.Errorf("file is in object storage but FILES_S3_* is not configured")
		}
		if data, err = store.Get(c.Context, c.WorkspaceID()+"/"+v.ID); err != nil {
			return nil, fmt.Errorf("file storage: %w", err)
		}
	}
	return map[string]any{"id": v.ID, "filename": name, "content_type": ct, "data_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func recordDiscard(c *command.Ctx, raw json.RawMessage) (any, error) {
	var v struct {
		IDs []string `json:"ids"`
	}
	if err := recDecode(raw, &v); err != nil {
		return nil, err
	}
	ids, err := distinctUUIDs(v.IDs, maxPendingPerOwner)
	if err != nil {
		return nil, err
	}
	who, err := owner(c.Principal)
	if err != nil {
		return nil, err
	}
	// base first: find the bases of the caller's own pending rows, lock them in id order, then delete
	rows, err := c.Tx.Query(c.Context, `select distinct base_id::text from core.attachments where workspace_id=$1::uuid and id = any($2::uuid[]) and status='pending' and staged_by=$3 and field_id is not null order by 1`,
		c.WorkspaceID(), ids, who)
	if err != nil {
		return nil, command.DB(err)
	}
	var bases []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		bases = append(bases, b)
	}
	rows.Close()
	for _, b := range bases {
		if err := lockBase(c.Context, c.Tx, c.WorkspaceID(), b, false); err != nil {
			return nil, err
		}
	}
	var freed int64
	if err := c.Tx.QueryRow(c.Context, `with d as (delete from core.attachments where workspace_id=$1::uuid and id = any($2::uuid[]) and status='pending' and staged_by=$3 and field_id is not null returning size_bytes)
		select coalesce(sum(size_bytes), 0)::bigint from d`, c.WorkspaceID(), ids, who).Scan(&freed); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"freed_bytes": freed}, nil
}

// RecordSweep runs hourly (files.record_sweep), for every workspace that has work, including workspaces that no longer
// exist. Per workspace: delete pending record files older than 24 hours (base lock first, then the counter is
// reconciled), turn announced objects older than 24 hours that never got a row into queue entries, then claim queued
// objects with FOR UPDATE SKIP LOCKED and delete each from the bucket when no attachment row has its id. A failure
// keeps the entry with its attempt count and error; nothing is dropped.
func RecordSweep(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "select core.file_sweep_workspaces()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var errs []error
	for _, ws := range wss {
		if err := sweepWorkspace(ctx, pool, ws); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", ws, err))
		}
	}
	return errors.Join(errs...)
}

func sweepWorkspace(ctx context.Context, pool *pgxpool.Pool, ws string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, `select distinct base_id::text from core.attachments where workspace_id=$1::uuid and status='pending' and field_id is not null
		and created_at < now() - interval '24 hours' order by 1`, ws)
	if err != nil {
		return err
	}
	bases, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, b := range bases {
		if err := lockBase(ctx, tx, ws, b, false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from core.attachments where workspace_id=$1::uuid and base_id=$2::uuid and status='pending' and created_at < now() - interval '24 hours'`, ws, b); err != nil {
			return err
		}
		if err := ReconcileBase(ctx, tx, ws, b); err != nil {
			return err
		}
	}
	// Claim old intents with SKIP LOCKED (a staging transaction holds its intent FOR UPDATE), then re-check in a fresh
	// statement: a stage that committed in between has an attachment row now.
	crows, err := tx.Query(ctx, `select key from core.file_objects where workspace_id=$1::uuid and state='intent' and created_at < now() - interval '24 hours' for update skip locked`, ws)
	if err != nil {
		return err
	}
	claimed, err := pgx.CollectRows(crows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `with d as (delete from core.file_objects o where o.workspace_id=$1::uuid and o.key = any($2::text[]) and o.state='intent'
		and not exists (select 1 from core.attachments a where a.workspace_id = o.workspace_id and a.id = split_part(o.key, '/', 2)::uuid) returning o.key, o.workspace_id)
		insert into core.file_gc (workspace_id, object_key) select workspace_id, key from d on conflict (object_key) do nothing`, ws, claimed); err != nil {
		return err
	}
	type item struct {
		id    int64
		key   string
		tries int
	}
	grows, err := tx.Query(ctx, `select id, object_key, tries from core.file_gc where workspace_id=$1::uuid and next_try_at <= now() order by id limit $2 for update skip locked`, ws, gcBatch)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(grows, func(r pgx.CollectableRow) (item, error) {
		var it item
		return it, r.Scan(&it.id, &it.key, &it.tries)
	})
	if err != nil {
		return err
	}
	for _, it := range items {
		_, idPart, _ := strings.Cut(it.key, "/")
		var referenced bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from core.attachments where workspace_id=$1::uuid and id=$2::uuid)`, ws, idPart).Scan(&referenced); err != nil {
			return err
		}
		if !referenced {
			var derr error
			if store == nil {
				derr = errors.New("object storage is not configured")
			} else {
				derr = store.Delete(ctx, it.key)
			}
			if derr != nil {
				if it.tries+1 >= gcWarnTries {
					slog.Error("file gc: object not deleted", "key", it.key, "tries", it.tries+1, "error", derr)
				}
				if _, err := tx.Exec(ctx, `update core.file_gc set tries = tries + 1, last_error = $2, next_try_at = now() + least(tries + 1, 24) * interval '10 minutes' where id=$1`, it.id, derr.Error()); err != nil {
					return err
				}
				continue
			}
		}
		if _, err := tx.Exec(ctx, `delete from core.file_gc where id=$1`, it.id); err != nil {
			return err
		}
		if !referenced {
			if _, err := tx.Exec(ctx, `delete from core.file_objects where key=$1 and workspace_id=$2::uuid`, it.key, ws); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
