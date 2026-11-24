package files

// Website images (design 17.11): the web app shrinks a photo before upload, but the server trusts nothing. An image is
// decoded and re-encoded (EXIF, GPS and any appended payload are dropped, the orientation is applied to the pixels),
// at most 1 MB in and out, 16 megapixels, two decodes at a time. Images hang on a site (core.attachments.site_id).
// The sites module locks the site row before calling these internal commands, so the 40-per-site count is exact.
//
// shortcut: the object store has no listing call, so there is no sweep for objects whose row never committed. The object
// is written last, after the row, and deleted again on any later error; only a failed COMMIT can leave one behind.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	maxImageBytes  = 1 << 20
	maxImagePixels = 16_000_000
	maxSiteImages  = 40
	userImagesHour = 30
)

var decodeSlots = make(chan struct{}, 2)

// ProcessImage validates and re-encodes a JPEG or PNG. It returns the clean bytes and their content type.
func ProcessImage(ctx context.Context, data []byte) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", command.ErrInvalid("file is empty")
	}
	if len(data) > maxImageBytes {
		return nil, "", command.ErrInvalid("the photo is larger than 1 MB")
	}
	ct, ok := Sniff(data)
	if !ok || (ct != "image/jpeg" && ct != "image/png") {
		return nil, "", command.ErrInvalid("only JPG and PNG photos are accepted")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return nil, "", command.ErrInvalid("that file is not a valid photo")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return nil, "", command.ErrInvalid("the photo is too large (over 16 megapixels)")
	}
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", command.ErrInvalid("that file is not a valid photo")
	}
	if ct == "image/jpeg" {
		img = orient(img, exifOrientation(data))
	}
	var out bytes.Buffer
	if ct == "image/png" {
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, img); err != nil {
			return nil, "", err
		}
	} else {
		for _, q := range []int{85, 70, 55, 40} {
			out.Reset()
			if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil {
				return nil, "", err
			}
			if out.Len() <= maxImageBytes {
				break
			}
		}
	}
	if out.Len() > maxImageBytes {
		return nil, "", command.ErrInvalid("the photo is still over 1 MB after resizing; use a smaller one")
	}
	return out.Bytes(), ct, nil
}

// exifOrientation reads the EXIF orientation tag (1 to 8) of a JPEG; anything unreadable is 1.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		m := b[i+1]
		if m == 0xFF {
			i++
			continue
		}
		if m == 0xD9 || m == 0xDA {
			return 1
		}
		l := int(b[i+2])<<8 | int(b[i+3])
		if l < 2 || i+2+l > len(b) {
			return 1
		}
		if seg := b[i+4 : i+2+l]; m == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + l
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	for k, n := 0, int(bo.Uint16(t[off:])); k < n; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			break
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient applies an EXIF orientation to the pixels.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			default:
				sx, sy = w-1-y, x
			}
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func imageCommands() []command.Command {
	put := command.NewCommand("file.image_put", command.GroupWriteItems, imagePut)
	get := command.NewCommand("file.image_get", command.GroupRead, imageGet)
	list := command.NewCommand("file.image_list", command.GroupRead, imageList)
	put.Internal, get.Internal, list.Internal = true, true, true
	get.MinRole, list.MinRole = command.Member, command.Member
	return []command.Command{put, get, list}
}

type imageIn struct {
	SiteID   string `json:"site_id"`
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Data     string `json:"data_base64"`
}

func imageDecode(raw json.RawMessage, needID bool) (imageIn, error) {
	var v imageIn
	if err := json.Unmarshal(raw, &v); err != nil || !uuidRe.MatchString(v.SiteID) || (needID && !uuidRe.MatchString(v.ID)) {
		return v, command.ErrInvalid("site_id and id must be ids")
	}
	return v, nil
}

func imagePut(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := imageDecode(raw, false)
	if err != nil {
		return nil, err
	}
	if base64.StdEncoding.DecodedLen(len(v.Data)) > maxImageBytes+3 {
		return nil, command.ErrInvalid("the photo is larger than 1 MB")
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return nil, command.ErrInvalid("data_base64 is not valid base64")
	}
	if c.Principal.UserID != nil {
		n, err := db.Bump(c.Context, c.Tx, "img:"+*c.Principal.UserID, false)
		if err != nil {
			return nil, command.DB(err)
		}
		if n > userImagesHour {
			return nil, command.NewError(command.RateLimited, "Too many photos this hour. Try again later.")
		}
	}
	clean, ct, err := ProcessImage(c.Context, data)
	if err != nil {
		return nil, err
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from core.attachments where site_id = $1::uuid and (data is not null or storage_key = 's3')", v.SiteID).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if n >= maxSiteImages {
		return nil, command.ErrConflict(fmt.Sprintf("a website can hold at most %d photos", maxSiteImages))
	}
	key, stored := "pg", clean
	if store != nil {
		key, stored = "s3", nil
	}
	name := cleanName(v.Filename)
	var id string
	err = c.Tx.QueryRow(c.Context, `insert into core.attachments (workspace_id, site_id, filename, content_type, size_bytes, storage_key, data, created_by)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid) returning id::text`,
		c.WorkspaceID(), v.SiteID, name, ct, len(clean), key, stored, c.Principal.UserID).Scan(&id)
	if err != nil {
		return nil, command.DB(err)
	}
	if store != nil {
		if err := store.Put(c.Context, c.WorkspaceID()+"/"+id, clean); err != nil {
			return nil, fmt.Errorf("file storage: %w", err)
		}
	}
	return map[string]any{"id": id, "filename": name, "size_bytes": len(clean)}, nil
}

func imageGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := imageDecode(raw, true)
	if err != nil {
		return nil, err
	}
	var ct, key string
	var data []byte
	err = c.Tx.QueryRow(c.Context, "select content_type, storage_key, data from core.attachments where site_id = $1::uuid and id = $2::uuid", v.SiteID, v.ID).Scan(&ct, &key, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("image")
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
	return map[string]any{"content_type": ct, "data_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func imageList(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := imageDecode(raw, false)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, filename from core.attachments where site_id = $1::uuid and (data is not null or storage_key = 's3') order by created_at, id`, v.SiteID)
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (m map[string]string, err error) {
		var id, name string
		err = r.Scan(&id, &name)
		return map[string]string{"id": id, "filename": name}, err
	})
	if out == nil {
		out = []map[string]string{}
	}
	return out, command.DB(err)
}
