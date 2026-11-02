package nimbo

import (
	"encoding/json"
	"io"
	"net/http"
)

// Register mounts the chaos routes.
func (c *Chaos) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chaos", Handle(func(w http.ResponseWriter, r *http.Request) error {
		view, err := c.View(r.Context())
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, view)
		return nil
	}))
	mux.HandleFunc("POST /api/chaos/reset", Handle(func(w http.ResponseWriter, r *http.Request) error {
		view, err := c.ResetAll(r.Context())
		if err != nil {
			return err
		}
		c.record("chaos.reset", "all", "")
		WriteJSON(w, http.StatusOK, view)
		return nil
	}))
	mux.HandleFunc("POST /api/chaos/targets/{id}/{preset}", Handle(func(w http.ResponseWriter, r *http.Request) error {
		// The body is optional: a missing or unreadable one means "use the defaults".
		var body ApplyDto
		if raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)); err == nil {
			if json.Unmarshal(raw, &body) != nil {
				body = ApplyDto{}
			}
		}
		id, preset := Param(r, "id"), Param(r, "preset")
		result, err := c.Apply(r.Context(), id, preset, body.Params)
		if err != nil {
			return err
		}
		c.record("chaos.apply", id, preset)
		WriteJSON(w, http.StatusOK, result)
		return nil
	}))
	mux.HandleFunc("DELETE /api/chaos/targets/{id}/toxics", Handle(func(w http.ResponseWriter, r *http.Request) error {
		id := Param(r, "id")
		result, err := c.Clear(r.Context(), id)
		if err != nil {
			return err
		}
		c.record("chaos.clear", id, "")
		WriteJSON(w, http.StatusOK, result)
		return nil
	}))
	mux.HandleFunc("DELETE /api/chaos/targets/{id}/toxics/{name}", Handle(func(w http.ResponseWriter, r *http.Request) error {
		result, err := c.RemoveToxic(r.Context(), Param(r, "id"), Param(r, "name"))
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, result)
		return nil
	}))
}

func (c *Chaos) record(kind, subject, detail string) {
	if c.activity != nil {
		c.activity.Record(kind, subject, detail)
	}
}
