package nimbo

import (
	"errors"
	"net/http"
)

// UpdateRoutes serves update checks, verified downloads, importing an installer from a file, and installing with
// confirmation.
type UpdateRoutes struct{ updater *Updater }

func NewUpdateRoutes(u *Updater) *UpdateRoutes { return &UpdateRoutes{u} }

// updateFailure answers a failure the person should read with the given status; any other error is left to Handle.
// A fallback text covers an UpdateError with no message.
func updateFailure(w http.ResponseWriter, err error, status int, fallback string) (handled bool) {
	var ue UpdateError
	if !errors.As(err, &ue) {
		return false
	}
	WriteJSON(w, status, ErrorDto{orDefault(ue.Msg, fallback)})
	return true
}

// updateBody decodes a JSON body whose listed fields must be present (like the required fields of the Kotlin DTOs).
func updateBody(r *http.Request, v any, required ...func() bool) error {
	if err := ReadJSON(r, v); err != nil {
		return err
	}
	for _, ok := range required {
		if !ok() {
			return BadBody{}
		}
	}
	return nil
}

func (x *UpdateRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/updates", Handle(func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, http.StatusOK, x.updater.Status())
		return nil
	}))
	mux.HandleFunc("POST /api/updates/check", Handle(func(w http.ResponseWriter, r *http.Request) error {
		// The page asks on its own with force=false (respects the mode and the daily limit); the button forces it.
		WriteJSON(w, http.StatusOK, x.updater.Check(r.Context(), Query(r, "force") == "true"))
		return nil
	}))
	mux.HandleFunc("POST /api/updates/download", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Version *string `json:"version"`
		}
		if err := updateBody(r, &body, func() bool { return body.Version != nil }); err != nil {
			return err
		}
		file, err := x.updater.Download(r.Context(), *body.Version)
		if err != nil {
			if updateFailure(w, err, http.StatusBadGateway, "download failed") {
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusOK, file)
		return nil
	}))
	mux.HandleFunc("POST /api/updates/import", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Path   *string `json:"path"`
			SHA256 *string `json:"sha256"`
		}
		if err := updateBody(r, &body, func() bool { return body.Path != nil }); err != nil {
			return err
		}
		file, err := x.updater.Import(*body.Path, body.SHA256)
		if err != nil {
			if updateFailure(w, err, http.StatusUnprocessableEntity, "import failed") {
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusOK, file)
		return nil
	}))
	mux.HandleFunc("POST /api/updates/install", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Version *string `json:"version"`
			Confirm bool    `json:"confirm"`
		}
		if err := updateBody(r, &body, func() bool { return body.Version != nil }); err != nil {
			return err
		}
		if err := x.updater.Install(*body.Version, body.Confirm); err != nil {
			if updateFailure(w, err, http.StatusUnprocessableEntity, "install failed") {
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusAccepted, map[string]string{"message": "Installing " + *body.Version + ": Nimbo will close and reopen by itself"})
		return nil
	}))
	mux.HandleFunc("DELETE /api/updates/files", Handle(func(w http.ResponseWriter, r *http.Request) error {
		n, err := x.updater.Clean()
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, map[string]int{"deleted": n})
		return nil
	}))
}
