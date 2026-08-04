package collector

import "net/http"

// Handler routes the collector's two operations.
//
// One path, two methods: POST puts events in, GET streams them out. Go's
// ServeMux has matched on method since 1.22, so this needs no router and no
// dependency — which matters for a module that has none.
//
// A GET on a path with no handler, or a method nobody registered, gets
// ServeMux's own 404 or 405. That is the right answer and not worth improving:
// this is demo infrastructure, and its error responses are for whoever typed
// the wrong URL, not for a protocol participant.
func Handler(h *Hub) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+EventsPath, Ingest(h))
	mux.Handle("GET "+EventsPath, Stream(h))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}
