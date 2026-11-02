package nimbo

import "net/http"

// ImageRoutes tells which emulator images are on this machine, and how to bring the missing ones in: pull them or
// load a pack.
type ImageRoutes struct{ images *Images }

func NewImageRoutes(i *Images) *ImageRoutes { return &ImageRoutes{i} }

func (x *ImageRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/images", Handle(func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, http.StatusOK, x.images.Status(r.Context()))
		return nil
	}))
	mux.HandleFunc("POST /api/images/load", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Path *string `json:"path"`
		}
		if err := updateBody(r, &body, func() bool { return body.Path != nil }); err != nil {
			return err
		}
		loaded, err := x.images.Load(r.Context(), *body.Path)
		if err != nil {
			if updateFailure(w, err, http.StatusUnprocessableEntity, "load failed") {
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusOK, loaded)
		return nil
	}))
	mux.HandleFunc("POST /api/images/pull", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Provider *string `json:"provider"`
		}
		if err := updateBody(r, &body, func() bool { return body.Provider != nil }); err != nil {
			return err
		}
		if err := x.images.Pull(r.Context(), *body.Provider); err != nil {
			if updateFailure(w, err, http.StatusBadGateway, "pull failed") {
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusNoContent, nil)
		return nil
	}))
}
