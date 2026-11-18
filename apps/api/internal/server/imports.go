package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/dataimport"
)

// POST /api/w/{ws}/imports?kind=contacts|items|opening_docs|tally[&space_id=<uuid>][&filename=<name>]  body = the file (<= 10 MB)
// Runs data_import.upload (Admin, finance gate, scopes as for any command) and returns the preview job: {id, kind, status, ...}.
func importRoute(r *http.Request) (ws string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/w/"), "/")
	if len(parts) != 2 || parts[1] != "imports" || !wsRe.MatchString(parts[0]) || r.Method != http.MethodPost {
		return "", false
	}
	return parts[0], true
}

func isImportRoute(r *http.Request) bool { _, ok := importRoute(r); return ok }

func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) {
	ws, _ := importRoute(r)
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
	q := r.URL.Query()
	in, _ := json.Marshal(map[string]string{"kind": q.Get("kind"), "space_id": q.Get("space_id"), "filename": q.Get("filename"), "data_base64": base64.StdEncoding.EncodeToString(data)})
	out, err := s.Registry.Execute(r.Context(), s.Pool, "data_import.upload", in, p, command.ExecOptions{})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
