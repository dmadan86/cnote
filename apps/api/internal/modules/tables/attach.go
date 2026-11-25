package tables

// Phase 1b attachments (design 18.1, revised by 18.8). Images and PDF only; no form uploads. The tables side checks the
// base role and the plan's storage cap; the files module (files/record.go) owns the rows and the ownership checks.
//
// Locking: every change of bases.attachment_bytes happens after `select ... from tables.bases where id=$1 for update`
// (changeBytes, files.ReconcileBase). Order is always base, then table, then record.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

const maxFileBytes = 10 << 20

const maxFilesPerCell = 10 // enforced by file.record_attach (files.MaxRecordFiles), which is the one that counts

// FileMeta is one element of a record's attachment cell: data[field id] = []FileMeta, at most 10, in upload order.
type FileMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"` // the sniffed content type
}

// FileOps is one attachment field's part of the `files` input of record.create and record.update.
type FileOps struct {
	Add    []string `json:"add"`    // upload handles staged by the same caller
	Remove []string `json:"remove"` // attached file ids of this record and field
}

// FilesIn maps an attachment field id to its operations.
type FilesIn map[string]FileOps

// lockBase takes the base row FOR UPDATE and returns its attachment counter. Every counter change starts here.
func lockBase(c *command.Ctx, baseID string) (used int64, err error) {
	err = c.Tx.QueryRow(c.Context, `select attachment_bytes from tables.bases where workspace_id=$1::uuid and id=$2::uuid for update`, c.WorkspaceID(), baseID).Scan(&used)
	return used, command.DB(err)
}

// changeBytes adds delta (negative to release) to the base's counter. The caller holds the base lock (lockBase).
// A reservation above the cap is a conflict, decided in the same UPDATE.
func changeBytes(c *command.Ctx, baseID string, delta, limit int64) error {
	tag, err := c.Tx.Exec(c.Context, `update tables.bases set attachment_bytes = greatest(attachment_bytes + $3, 0)
		where workspace_id=$1::uuid and id=$2::uuid and ($3 <= 0 or attachment_bytes + $3 <= $4)`, c.WorkspaceID(), baseID, delta, limit)
	if err != nil {
		return command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		plan, _ := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
		return command.ErrConflict(fmt.Sprintf("This base has reached its storage limit (%s on the %s plan)", bytesText(limit), planName(plan)))
	}
	return nil
}

func bytesText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%d GB", n>>30)
	}
	return fmt.Sprintf("%d MB", n>>20)
}

func attachmentCap(c *command.Ctx) (int64, error) {
	plan, err := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
	return limitsFor(plan).AttachmentBytesPerBase, err
}

// StageFile stores one upload for an attachment field and returns its handle (the file's id, pending). The caller runs
// it in its own transaction and commits before answering: the handle exists only once this commits. The owner of the
// handle is the calling principal. Order: role and field, base lock, plan cap, announce the object (own committed
// transaction), reserve the bytes, write the row and the object last.
func StageFile(c *command.Ctx, tableID, fieldID, filename string, data []byte) (FileMeta, error) {
	fieldID = strings.ToLower(fieldID)
	tc, err := openTable(c, tableID, RoleWrite)
	if err != nil {
		return FileMeta{}, err
	}
	f, ok := tc.byID[fieldID]
	if !ok || f.Type != Attachment {
		return FileMeta{}, command.ErrNotFound("field")
	}
	if f.Locked && !tc.Role.AtLeast(RoleManage) {
		return FileMeta{}, command.ErrForbidden(fmt.Sprintf("field %q is locked: ask a manager", f.Name))
	}
	if _, err := lockBase(c, tc.Base.ID); err != nil {
		return FileMeta{}, err
	}
	limit, err := attachmentCap(c)
	if err != nil {
		return FileMeta{}, err
	}
	// the type, size and name are judged by file.record_stage; refuse before announcing an object only what is cheap here
	if len(data) == 0 || len(data) > maxFileBytes {
		return FileMeta{}, command.ErrInvalid("file is empty or larger than 10 MB")
	}
	rawIntent, err := c.Call("file.record_intent", map[string]any{})
	if err != nil {
		return FileMeta{}, err
	}
	var intent struct {
		ObjectID string `json:"object_id"`
	}
	if err := json.Unmarshal(rawIntent, &intent); err != nil {
		return FileMeta{}, err
	}
	objectID := intent.ObjectID
	if err := changeBytes(c, tc.Base.ID, int64(len(data)), limit); err != nil {
		return FileMeta{}, err
	}
	raw, err := c.Call("file.record_stage", map[string]any{"base_id": tc.Base.ID, "table_id": tc.Table.ID, "field_id": fieldID,
		"filename": filename, "data_base64": base64.StdEncoding.EncodeToString(data), "object_id": objectID})
	if err != nil {
		return FileMeta{}, err
	}
	var m struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return FileMeta{}, err
	}
	return FileMeta{ID: m.ID, Name: m.Filename, Size: m.SizeBytes, Type: m.ContentType}, nil
}

// DiscardFile deletes a pending upload of the caller (removed before saving) and releases its bytes.
func DiscardFile(c *command.Ctx, handle string) error {
	var baseID string
	err := c.Tx.QueryRow(c.Context, `select base_id::text from core.attachments where workspace_id=$1::uuid and id=$2::uuid and status='pending' and field_id is not null`,
		c.WorkspaceID(), handle).Scan(&baseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("file")
	}
	if err != nil {
		return command.DB(err)
	}
	if _, err := openBaseWrite(c, baseID); err != nil {
		return err
	}
	if _, err := lockBase(c, baseID); err != nil {
		return err
	}
	raw, err := c.Call("file.record_discard", map[string]any{"ids": []string{handle}})
	if err != nil {
		return err
	}
	var out struct {
		Freed int64 `json:"freed_bytes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.Freed == 0 {
		return command.ErrNotFound("file")
	}
	return changeBytes(c, baseID, -out.Freed, 0)
}

func openBaseWrite(c *command.Ctx, baseID string) (Role, error) {
	r, err := baseRole(c, baseID)
	if err != nil {
		return RoleNone, err
	}
	if r == RoleNone {
		return r, command.ErrNotFound("file")
	}
	if !r.AtLeast(RoleWrite) {
		return r, command.ErrForbidden("you do not have permission for this change in this base")
	}
	return r, nil
}

// FileData is one file's bytes for the download endpoint.
type FileData struct {
	Name, Type string
	Data       []byte
}

// GetFile reads an attached file through its record. The caller needs read access to the base; the record, field,
// table and base must be live (a record in the trash is not found), and the files layer checks the file is attached
// to exactly that record. Every failure is the same not found.
func GetFile(c *command.Ctx, recordID, fileID string) (*FileData, error) {
	nf := command.ErrNotFound("file")
	if !uuidRe.MatchString(recordID) || !uuidRe.MatchString(fileID) {
		return nil, nf
	}
	if _, err := openRecordTable(c, recordID, RoleRead); err != nil {
		if e := (*command.Error)(nil); errors.As(err, &e) && e.Code == command.NotFound {
			return nil, nf
		}
		return nil, err
	}
	raw, err := c.Call("file.record_get", map[string]any{"record_id": recordID, "id": fileID})
	if err != nil {
		if e := (*command.Error)(nil); errors.As(err, &e) && (e.Code == command.NotFound || e.Code == command.Invalid) {
			return nil, nf
		}
		return nil, err
	}
	var out struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Data        string `json:"data_base64"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return nil, err
	}
	return &FileData{Name: out.Filename, Type: out.ContentType, Data: b}, nil
}

// cellMeta is the record field's files as FileMeta, in upload order, read from the rows.
func cellMeta(c *command.Ctx, recordID, fieldID string) ([]FileMeta, error) {
	rows, err := c.Tx.Query(c.Context, `select id::text, filename, size_bytes, content_type from core.attachments
		where workspace_id=$1::uuid and record_id=$2::uuid and field_id=$3::uuid and status='attached' order by created_at, id`, c.WorkspaceID(), recordID, fieldID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []FileMeta{}
	for rows.Next() {
		var m FileMeta
		if err := rows.Scan(&m.ID, &m.Name, &m.Size, &m.Type); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, m)
	}
	return out, command.DB(rows.Err())
}

// lockBaseOf is the first lock of a record write that takes files (and of every insert): the base row.
func lockBaseOf(c *command.Ctx, tc *openedTable) error {
	_, err := lockBase(c, tc.Base.ID)
	return err
}

// applyFiles runs inside the record write, after the base is locked (lockBaseOf) and the record row exists. For each
// attachment field in `in` it removes, then attaches (file.record_detach, file.record_attach), releases the bytes of
// removed files, and rewrites data[field id] from the rows. It returns the field ids whose cell changed. For an
// update (isNew false) it also writes the history row holding [old list, new list] of the metadata.
func applyFiles(c *command.Ctx, tc *openedTable, recordID string, in FilesIn, isNew bool) ([]string, error) {
	var changed []string
	change := map[string][2]any{}
	var data map[string]json.RawMessage
	for _, fid := range sortedKeys(in) {
		ops := in[fid]
		if len(ops.Add) == 0 && len(ops.Remove) == 0 {
			continue
		}
		f, ok := tc.byID[fid]
		if !ok || f.Type != Attachment {
			return nil, command.ErrInvalid("files: " + fid + " is not an attachment field of this table")
		}
		if f.Locked && !tc.Role.AtLeast(RoleManage) {
			return nil, command.ErrForbidden(fmt.Sprintf("field %q is locked: ask a manager", f.Name))
		}
		if data == nil {
			var raw []byte
			if err := c.Tx.QueryRow(c.Context, `select data from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and id=$3::uuid and deleted_at is null for update`,
				c.WorkspaceID(), tc.Table.ID, recordID).Scan(&raw); err != nil {
				return nil, command.DB(err)
			}
			data = map[string]json.RawMessage{}
			_ = json.Unmarshal(raw, &data)
		}
		before, err := cellMeta(c, recordID, fid)
		if err != nil {
			return nil, err
		}
		if len(ops.Remove) > 0 {
			raw, err := c.Call("file.record_detach", map[string]any{"record_id": recordID, "field_id": fid, "ids": ops.Remove})
			if err != nil {
				return nil, err
			}
			var out struct {
				Freed int64 `json:"freed_bytes"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
			if err := changeBytes(c, tc.Base.ID, -out.Freed, 0); err != nil {
				return nil, err
			}
		}
		if len(ops.Add) > 0 {
			if _, err := c.Call("file.record_attach", map[string]any{"record_id": recordID, "field_id": fid, "handles": ops.Add}); err != nil {
				return nil, err
			}
		}
		after, err := cellMeta(c, recordID, fid)
		if err != nil {
			return nil, err
		}
		if len(after) == 0 {
			delete(data, fid)
		} else {
			data[fid], _ = json.Marshal(after)
		}
		change[fid] = [2]any{before, after}
		changed = append(changed, fid)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(data)
	if len(b) > maxRecordBytes {
		return nil, command.ErrInvalid("a record can hold at most 64 KB of data")
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.records set data=$4::jsonb, updated_at=now(), updated_by=$5::uuid where workspace_id=$1::uuid and table_id=$2::uuid and id=$3::uuid`,
		c.WorkspaceID(), tc.Table.ID, recordID, string(b), c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if !isNew {
		atype, aid, _ := c.Principal.Actor()
		batch := &pgx.Batch{}
		queueHistory(batch, c, tc.Table.ID, recordID, atype, aid, change)
		if err := sendBatch(c, batch); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// ReconcileAttachmentBytes sets bases.attachment_bytes of every base in the workspace that has files or a counter to
// the sum of its core.attachments rows, one base at a time, each after its row lock. PurgeSweep calls it after it
// purges, so cascaded deletes give their bytes back.
func ReconcileAttachmentBytes(ctx context.Context, tx pgx.Tx, ws string) error {
	ids, err := attachmentBases(ctx, tx, false)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := reconcileBase(ctx, tx, ws, id); err != nil {
			return err
		}
	}
	return nil
}

// attachmentBases lists the bases that hold record files or a non-zero counter, in id order, optionally locking them.
func attachmentBases(ctx context.Context, tx pgx.Tx, lock bool) ([]string, error) {
	q := `select b.id::text from tables.bases b where b.attachment_bytes > 0 or exists (select 1 from core.attachments a where a.workspace_id = b.workspace_id and a.base_id = b.id) order by b.id`
	if lock {
		q += ` for update of b`
	}
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// reconcileBase recomputes one base's counter from its rows, after the base row lock (the same rule as files.ReconcileBase,
// which the hourly sweep uses; the modules cannot import each other).
func reconcileBase(ctx context.Context, tx pgx.Tx, ws, baseID string) error {
	var one int
	err := tx.QueryRow(ctx, `select 1 from tables.bases where workspace_id=$1::uuid and id=$2::uuid for update`, ws, baseID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update tables.bases set attachment_bytes = (select coalesce(sum(size_bytes), 0) from core.attachments
		where workspace_id=$1::uuid and base_id=$2::uuid and field_id is not null) where workspace_id=$1::uuid and id=$2::uuid`, ws, baseID)
	return err
}
