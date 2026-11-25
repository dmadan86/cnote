package importers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/importers"
	"github.com/cnote/api/internal/modules/notes"
)

func init() { b2Handlers["import.docx"] = importDocx }

// importDocx turns one Word file into one page (docs-parity-b 5.7). Parsing and every limit are in importers.ParseDocx,
// which the raw route reaches through this command; the file itself is dropped when the command returns.
func importDocx(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	space, err := o.ReqUUID("space_id")
	if err != nil {
		return nil, err
	}
	parent, err := o.OptUUID("parent_id")
	if err != nil {
		return nil, err
	}
	filename, err := o.ReqStr("filename")
	if err != nil {
		return nil, err
	}
	b64, err := o.ReqStr("data_base64")
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		return nil, err
	}
	if base64.StdEncoding.DecodedLen(len(b64)) > importers.DocxMaxInput+3 {
		return nil, command.ErrInvalid("the file is larger than 10 MB")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return nil, command.ErrInvalid("data_base64 is not valid base64")
	}
	release, ok := importers.AcquireDocx(c.WorkspaceID())
	if !ok {
		return nil, command.NewError(command.RateLimited, "two Word imports are already running in this workspace; try again in a moment")
	}
	defer release()
	res, err := importers.ParseDocx(c.Context, raw)
	var de *importers.DocxError
	switch {
	case errors.As(err, &de):
		return nil, command.ErrInvalid(de.Msg)
	case errors.Is(err, context.DeadlineExceeded):
		return nil, command.ErrInvalid("the document took too long to read")
	case err != nil:
		return nil, err
	}
	title := strings.TrimSpace(res.Title)
	if title == "" {
		title = importers.FileTitle(filename)
	}
	body := res.Markdown
	for _, img := range res.Images {
		body = strings.ReplaceAll(body, img.Ref, "")
	}
	create := map[string]any{"space_id": space, "title": title, "content_md": body}
	if parent != nil {
		create["parent_id"] = *parent
	}
	out, err := c.Call("note.create", create)
	if err != nil {
		return nil, err
	}
	var note struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &note); err != nil || note.ID == "" {
		return nil, command.ErrInvalid("the page could not be created")
	}
	warnings := append([]string{}, res.Warnings...)
	if len(res.Images) > 0 {
		body = res.Markdown
		for _, img := range res.Images {
			name := imageName(img.Name)
			up, err := c.Call("file.upload", map[string]any{"note_id": note.ID, "filename": img.Name, "data_base64": base64.StdEncoding.EncodeToString(img.Data)})
			var f struct {
				ID string `json:"id"`
			}
			var ce *command.Error
			switch {
			case err == nil && json.Unmarshal(up, &f) == nil && f.ID != "":
				body = strings.ReplaceAll(body, img.Ref, "!["+name+"](/api/w/"+c.WorkspaceID()+"/files/"+f.ID+")")
				continue
			case err != nil && !(errors.As(err, &ce) && (ce.Code == command.Invalid || ce.Code == command.Conflict)):
				return nil, err
			}
			body = strings.ReplaceAll(body, img.Ref, "")
			warnings = append(warnings, "the picture "+name+" could not be added: "+failure(err))
		}
		if _, err := c.Call("note.update", map[string]any{"id": note.ID, "content_md": body}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"id": note.ID, "title": title, "warnings": warnings}, nil
}

func failure(err error) string {
	var ce *command.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return "unknown error"
}

// imageName is safe inside Markdown image alt text.
func imageName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\[]()<>!*_`+"`\r\n", r) || r < 32 {
			return -1
		}
		return r
	}, s)
}
