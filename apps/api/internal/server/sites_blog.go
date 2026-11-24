package server

import "net/http"

// siteBlogStub reserves the server-to-server public blog reads for Phase 3a.
func (s *Server) siteBlogStub(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
