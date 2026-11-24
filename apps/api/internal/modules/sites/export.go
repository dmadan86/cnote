package sites

// "Download your site's content" (design 17.9): site.json and the photos of the published version, as one zip.

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

const (
	maxExportImages = 40
	maxExportBytes  = 20 << 20
)

// ExportCommands is empty: the export is a download, served by GET /api/w/{ws}/sites/{id}/export through BuildExport.
func ExportCommands() []command.Command { return []command.Command{} }

// docImageIDs is ImageIDs over Home and every extra page.
func docImageIDs(data json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw json.RawMessage) {
		for _, id := range ImageIDs(raw) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(data)
	var d struct {
		Pages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"pages"`
	}
	if json.Unmarshal(data, &d) == nil {
		for _, p := range d.Pages {
			add(json.RawMessage(`{"content":` + string(p.Content) + `}`))
		}
	}
	return out
}

// BuildExport returns the zip of the site's published version. It needs a signed-in admin (not a key or an agent),
// allows one export per site at a time (a second answers 409) and holds the site row while it reads.
func BuildExport(c *command.Ctx, siteID string) ([]byte, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("only an admin signed in as a person can download the site")
	}
	if err := uid("id", siteID); err != nil {
		return nil, err
	}
	var free bool
	if err := c.Tx.QueryRow(c.Context, "select pg_try_advisory_xact_lock(hashtextextended($1, 9))", "siteexport:"+siteID).Scan(&free); err != nil {
		return nil, command.DB(err)
	}
	if !free {
		return nil, command.ErrConflict("A download of this site is already being prepared. Try again in a moment.")
	}
	var name string
	var vid *string
	var pubAt *time.Time
	err := c.Tx.QueryRow(c.Context, "select name, published_version_id::text, published_at from sites.sites where workspace_id = $1::uuid and id = $2::uuid for share",
		c.WorkspaceID(), siteID).Scan(&name, &vid, &pubAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("site")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if vid == nil {
		return nil, command.ErrConflict("This website is not published yet. Publish it first.")
	}
	data, err := versionData(c, siteID, *vid)
	if err != nil {
		return nil, err
	}
	ids := docImageIDs(data)
	if len(ids) > maxExportImages {
		return nil, command.ErrInvalid("the site uses too many photos to download")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	total := len(data)
	type imgOut struct {
		ID          string `json:"id"`
		File        string `json:"file"`
		ContentType string `json:"content_type"`
	}
	images := []imgOut{}
	for _, id := range ids {
		out, err := c.Call("file.image_get", props{"site_id": siteID, "id": id})
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.NotFound {
			continue // a photo that was deleted after publishing is simply not in the zip
		}
		if err != nil {
			return nil, err
		}
		var img struct {
			ContentType string `json:"content_type"`
			Data        string `json:"data_base64"`
		}
		if err := json.Unmarshal(out, &img); err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return nil, err
		}
		if total += len(b); total > maxExportBytes {
			return nil, command.ErrInvalid("the site's photos are too large to download (20 MB at most)")
		}
		ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"}[img.ContentType]
		file := "images/" + id + ext
		w, err := zw.CreateHeader(&zip.FileHeader{Name: file, Method: zip.Store})
		if err == nil {
			_, err = w.Write(b)
		}
		if err != nil {
			return nil, err
		}
		images = append(images, imgOut{id, file, img.ContentType})
	}
	doc, err := json.MarshalIndent(props{"site_id": siteID, "name": name, "published_version_id": *vid, "published_at": pubAt,
		"exported_at": time.Now().UTC(), "data": data, "images": images}, "", "  ")
	if err != nil {
		return nil, err
	}
	w, err := zw.Create("site.json")
	if err == nil {
		_, err = w.Write(doc)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
