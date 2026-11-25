package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/importers"
)

// POST /api/w/{ws}/import/docx?space_id=&parent_id=&filename=  body = the .docx (<= 10 MB)
// Runs the import.docx command, so roles, scopes, the space checks and every parser limit are the command's.
func docxRoute(r *http.Request) (ws string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/w/"), "/")
	if len(parts) != 3 || parts[1] != "import" || parts[2] != "docx" || !wsRe.MatchString(parts[0]) || r.Method != http.MethodPost {
		return "", false
	}
	return parts[0], true
}

func (s *Server) importDocx(w http.ResponseWriter, r *http.Request, ws string) {
	p, err := s.Auth.PrincipalFor(r.Context(), r.Header, ws)
	if err != nil {
		s.fileFail(w, err)
		return
	}
	if r.ContentLength > importers.DocxMaxInput {
		s.fileFail(w, command.ErrInvalid("the file is larger than 10 MB"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, importers.DocxMaxInput+1))
	if err != nil || len(data) > importers.DocxMaxInput {
		s.fileFail(w, command.ErrInvalid("the file is larger than 10 MB or could not be read"))
		return
	}
	q := r.URL.Query()
	m := map[string]string{"space_id": q.Get("space_id"), "filename": q.Get("filename"), "data_base64": base64.StdEncoding.EncodeToString(data)}
	if v := q.Get("parent_id"); v != "" {
		m["parent_id"] = v
	}
	in, _ := json.Marshal(m)
	out, err := s.Registry.Execute(r.Context(), s.Pool, "import.docx", in, p, command.ExecOptions{})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func isDocxRoute(r *http.Request) bool { _, ok := docxRoute(r); return ok }
