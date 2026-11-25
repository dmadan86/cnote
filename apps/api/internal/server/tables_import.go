package server

import (
	"io"
	"net/http"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/dataimport"
	"github.com/cnote/api/internal/modules/tables"
)

// POST /api/tables/import?ws=<workspace>&base_id=<uuid>&table_id=<uuid>&filename=<name>   body = the CSV (<= 10 MB)
// Needs write access to the table's base (the record.create gate plus the base role) and returns {"handle": "<id>", "rows", "headers"};
// table_import.preview and table_import.run take the handle (design 17.6).
func isTablesImportRoute(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/tables/import"
}

func (s *Server) tablesImportUpload(w http.ResponseWriter, r *http.Request) {
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
	if r.ContentLength > dataimport.MaxBytes {
		s.fileFail(w, command.ErrInvalid("file is larger than 10 MB"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, dataimport.MaxBytes+1))
	if err != nil || len(data) > dataimport.MaxBytes {
		s.fileFail(w, command.ErrInvalid("file is larger than 10 MB or could not be read"))
		return
	}
	var out map[string]any
	err = s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
		if err := c.Allowed("record.create"); err != nil {
			return err
		}
		var e error
		out, e = tables.StoreUpload(c, q.Get("table_id"), q.Get("base_id"), q.Get("filename"), data)
		return e
	})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	body, _ := command.Marshal(out)
	writeJSON(w, http.StatusOK, body)
}
