package server

import (
	"io"
	"net/http"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/files"
	"github.com/cnote/api/internal/modules/tables"
)

// Tables attachments over HTTP (design 18.1, revised by 18.8). Images and PDF only.
//
//	POST   /api/tables/files?ws=<workspace>&table_id=<uuid>&field_id=<uuid>&filename=<name>   body = the file (<= 10 MB)
//	       -> {"handle","filename","size","content_type"}; committed before the answer, valid 24 h, for the caller only
//	GET    /api/tables/files/<file id>?ws=<workspace>&record_id=<uuid>                         the bytes (read access to the base)
//	DELETE /api/tables/files/<handle>?ws=<workspace>                                           drop an unattached upload of the caller
const tablesFilesPrefix = "/api/tables/files/"

func isTablesFilesRoute(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost:
		return r.URL.Path == "/api/tables/files"
	case http.MethodGet, http.MethodDelete:
		return len(r.URL.Path) > len(tablesFilesPrefix) && strings.HasPrefix(r.URL.Path, tablesFilesPrefix)
	}
	return false
}

func (s *Server) tablesFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ws := q.Get("ws")
	if !wsRe.MatchString(ws) {
		s.fileFail(w, command.ErrInvalid("ws is required"))
		return
	}
	p, err := s.Auth.PrincipalFor(r.Context(), r.Header, ws)
	if err != nil {
		s.fileFail(w, err)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, tablesFilesPrefix)
	switch {
	case r.Method == http.MethodPost:
		s.tablesFileStage(w, r, p)
	case r.Method == http.MethodGet:
		var f *tables.FileData
		err := s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
			if err := c.Allowed("record.get"); err != nil {
				return err
			}
			var e error
			f, e = tables.GetFile(c, q.Get("record_id"), id)
			return e
		})
		if err != nil {
			s.fileFail(w, err)
			return
		}
		files.RecordPolicy.Headers(w.Header(), f.Name, f.Type, f.Data)
		_, _ = w.Write(f.Data)
	default:
		err := s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
			if err := c.Allowed("record.update"); err != nil {
				return err
			}
			return tables.DiscardFile(c, id)
		})
		if err != nil {
			s.fileFail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, []byte(`{"deleted":true}`))
	}
}

func (s *Server) tablesFileStage(w http.ResponseWriter, r *http.Request, p command.Principal) {
	q := r.URL.Query()
	// A declared size over the cap is refused before reading; MaxBytesReader catches a missing or false length.
	if r.ContentLength > files.MaxBytes {
		s.fileFail(w, command.ErrInvalid("file is larger than 10 MB"))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, files.MaxBytes))
	if err != nil {
		s.fileFail(w, command.ErrInvalid("file is larger than 10 MB or could not be read"))
		return
	}
	var m tables.FileMeta
	err = s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
		if err := c.Allowed("record.update"); err != nil {
			return err
		}
		var e error
		m, e = tables.StageFile(c, q.Get("table_id"), q.Get("field_id"), q.Get("filename"), data)
		return e
	})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	body, _ := command.Marshal(map[string]any{"handle": m.ID, "filename": m.Name, "size": m.Size, "content_type": m.Type})
	writeJSON(w, http.StatusOK, body)
}
