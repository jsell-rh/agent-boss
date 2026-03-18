//go:build watercooler

package coordinator

import (
	_ "embed"
	"net/http"
)

//go:embed watercooler/index.html
var watercoolerHTML []byte

func (s *Server) handleWatercoolerGet(w http.ResponseWriter, r *http.Request, spaceName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(watercoolerHTML) //nolint:errcheck
}
