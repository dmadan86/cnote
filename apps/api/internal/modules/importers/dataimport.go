package importers

import (
	"encoding/base64"
	"encoding/json"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/importers"
)

// The data importers (docs/design/importers.md): contacts, items, opening documents, Tally masters, and the opening
// balances page. Admin only; the data_import. and opening. prefixes are finance commands (modules.go financePrefixes).
// The upload is POST /api/w/{ws}/imports?kind=..., which runs data_import.upload with the file as base64.

func dataCommands() []command.Command {
	adm := func(name string, g command.Group, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = command.Admin
		return c
	}
	undo := adm("data_import.undo", command.GroupAdmin, func(c *command.Ctx, in json.RawMessage) (any, error) {
		id, err := idOf(in)
		if err != nil {
			return nil, err
		}
		return importers.DataUndo(c, id)
	})
	undo.Confirm = true
	post := adm("opening.post", command.GroupAdmin, func(c *command.Ctx, _ json.RawMessage) (any, error) { return importers.OpeningPost(c) })
	post.Confirm = true
	reverse := adm("opening.reverse", command.GroupAdmin, func(c *command.Ctx, _ json.RawMessage) (any, error) { return importers.OpeningReverse(c) })
	reverse.Confirm = true
	return []command.Command{
		adm("data_import.upload", command.GroupAdmin, upload),
		adm("data_import.template", command.GroupRead, func(c *command.Ctx, in json.RawMessage) (any, error) {
			var v struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(in, &v) != nil {
				return nil, command.ErrInvalid("kind is required")
			}
			return importers.DataTemplate(v.Kind)
		}),
		adm("data_import.preview", command.GroupAdmin, func(c *command.Ctx, in json.RawMessage) (any, error) {
			var v struct {
				ID      string            `json:"id"`
				Mapping map[string]string `json:"mapping"`
				Options map[string]string `json:"options"`
				Status  string            `json:"status"`
			}
			if json.Unmarshal(in, &v) != nil || v.ID == "" {
				return nil, command.ErrInvalid("id is required")
			}
			var probe map[string]json.RawMessage
			_ = json.Unmarshal(in, &probe)
			_, has := probe["mapping"]
			return importers.DataPreview(c, v.ID, v.Mapping, has && string(probe["mapping"]) != "null", v.Options, v.Status)
		}),
		adm("data_import.run", command.GroupAdmin, func(c *command.Ctx, in json.RawMessage) (any, error) {
			var v struct {
				ID      string          `json:"id"`
				Include map[string]bool `json:"include"`
			}
			if json.Unmarshal(in, &v) != nil || v.ID == "" {
				return nil, command.ErrInvalid("id is required")
			}
			return importers.DataRun(c, v.ID, v.Include)
		}),
		adm("data_import.status", command.GroupRead, func(c *command.Ctx, in json.RawMessage) (any, error) {
			id, err := idOf(in)
			if err != nil {
				return nil, err
			}
			return importers.DataStatus(c, id)
		}),
		adm("data_import.list", command.GroupRead, func(c *command.Ctx, _ json.RawMessage) (any, error) { return importers.DataList(c) }),
		adm("data_import.errors", command.GroupRead, func(c *command.Ctx, in json.RawMessage) (any, error) {
			id, err := idOf(in)
			if err != nil {
				return nil, err
			}
			return importers.DataErrors(c, id)
		}),
		undo,
		adm("opening.get", command.GroupRead, func(c *command.Ctx, _ json.RawMessage) (any, error) { return importers.OpeningGet(c) }),
		adm("opening.save", command.GroupAdmin, func(c *command.Ctx, in json.RawMessage) (any, error) {
			var v struct {
				GoLive string `json:"go_live"`
				Lines  []struct {
					AccountCode string `json:"account_code"`
					DrMinor     int64  `json:"dr_minor"`
					CrMinor     int64  `json:"cr_minor"`
				} `json:"lines"`
			}
			if json.Unmarshal(in, &v) != nil {
				return nil, command.ErrInvalid("go_live and lines are required")
			}
			lines := make([]importers.OpenLine, 0, len(v.Lines))
			for _, l := range v.Lines {
				lines = append(lines, importers.OpenLine{AccountCode: l.AccountCode, DrMinor: l.DrMinor, CrMinor: l.CrMinor})
			}
			return importers.OpeningSave(c, v.GoLive, lines)
		}),
		post,
		reverse,
	}
}

func idOf(in json.RawMessage) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(in, &v) != nil || v.ID == "" {
		return "", command.ErrInvalid("id is required")
	}
	return v.ID, nil
}

// data_import.upload {kind, filename, space_id?, data_base64}: the HTTP upload route calls this.
func upload(c *command.Ctx, in json.RawMessage) (any, error) {
	var v struct {
		Kind     string `json:"kind"`
		Filename string `json:"filename"`
		SpaceID  string `json:"space_id"`
		Data     string `json:"data_base64"`
	}
	if json.Unmarshal(in, &v) != nil {
		return nil, command.ErrInvalid("kind and data_base64 are required")
	}
	b, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return nil, command.ErrInvalid("data_base64 is not valid base64")
	}
	return importers.DataUpload(c, v.Kind, v.Filename, v.SpaceID, b)
}
