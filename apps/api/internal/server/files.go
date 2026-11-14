package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/files"
)

// Raw file endpoints (the command envelope carries base64, these carry bytes):
//
//	POST /api/w/{ws}/files?note_id=<uuid>&filename=<name>   body = the file; returns the file's metadata
//	GET  /api/w/{ws}/files/{id}                             the bytes, with the type judged at upload
//
// Both run the file.upload / file.download commands, so roles, scopes, agent groups and the finance gate are the same.
func fileRoute(r *http.Request) (ws, id string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/w/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "files" || !wsRe.MatchString(parts[0]) {
		return "", "", false
	}
	if len(parts) == 3 {
		return parts[0], parts[2], r.Method == http.MethodGet
	}
	return parts[0], "", r.Method == http.MethodPost
}

func (s *Server) fileFail(w http.ResponseWriter, err error) {
	var ce *command.Error
	if !errors.As(err, &ce) {
		slog.Error("files", "error", err)
		ce = command.NewError(command.Internal, "internal error")
	}
	body, _ := command.Marshal(map[string]any{"error": ce})
	writeJSON(w, ce.Code.Status(), body)
}

func (s *Server) files(w http.ResponseWriter, r *http.Request, ws, id string) {
	p, err := s.Auth.PrincipalFor(r.Context(), r.Header, ws)
	if err != nil {
		s.fileFail(w, err)
		return
	}
	if id != "" {
		s.fileGet(w, r, p, id)
		return
	}
	// A declared size over the cap is refused before reading; the +1 read catches a missing or false length.
	if r.ContentLength > files.MaxBytes {
		s.fileFail(w, command.ErrInvalid("file is larger than 10 MB"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, files.MaxBytes+1))
	if err != nil {
		s.fileFail(w, command.ErrInvalid("could not read the upload"))
		return
	}
	q := r.URL.Query()
	in, _ := json.Marshal(map[string]string{"note_id": q.Get("note_id"), "filename": q.Get("filename"), "data_base64": base64.StdEncoding.EncodeToString(data)})
	out, err := s.Registry.Execute(r.Context(), s.Pool, "file.upload", in, p, command.ExecOptions{})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fileGet(w http.ResponseWriter, r *http.Request, p command.Principal, id string) {
	in, _ := json.Marshal(map[string]string{"id": id})
	out, err := s.Registry.Execute(r.Context(), s.Pool, "file.download", in, p, command.ExecOptions{})
	if err != nil {
		s.fileFail(w, err)
		return
	}
	var f map[string]string
	if json.Unmarshal(out, &f) != nil {
		s.fileFail(w, errors.New("bad file result"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(f["data_base64"])
	if err != nil {
		s.fileFail(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f["content_type"])
	h.Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(f["filename"]))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

func isFileRoute(r *http.Request) bool { _, _, ok := fileRoute(r); return ok }
