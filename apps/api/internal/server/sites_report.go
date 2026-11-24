package server

import "net/http"

func (s *Server) siteReport(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
