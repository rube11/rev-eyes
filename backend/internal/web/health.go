package web

import "net/http"

// Health reports that the backend process is accepting requests.
func Health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
