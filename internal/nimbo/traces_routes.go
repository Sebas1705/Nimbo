package nimbo

import "net/http"

// Register mounts the tracing routes.
func (t *Traces) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traces/services", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := t.Services(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET /api/traces", Handle(func(w http.ResponseWriter, r *http.Request) error {
		if !r.URL.Query().Has("service") {
			WriteJSON(w, http.StatusBadRequest, ErrorDto{"service is required"})
			return nil
		}
		limit := 20
		if n, ok := obsAtoi32(Query(r, "limit")); ok {
			limit = n
		}
		v, err := t.List(r.Context(), Query(r, "service"), limit)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("POST /api/traces/sample", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := t.Sample(r.Context(), Query(r, "error") == "true")
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET /api/traces/{id}", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := t.Detail(r.Context(), Param(r, "id"))
		return inspectReply(w, v, err)
	}))
}
