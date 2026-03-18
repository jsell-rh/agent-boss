//go:build !watercooler

package coordinator

import "net/http"

func (s *Server) handleWatercoolerGet(w http.ResponseWriter, r *http.Request, spaceName string) {
	http.NotFound(w, r)
}
