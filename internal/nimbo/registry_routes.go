package nimbo

import (
	"net/http"
	"strconv"
	"strings"
)

// RegistryRoutes is the HTTP side of the linked sources and the compose profiles.
type RegistryRoutes struct {
	sources *RegistrySources
	compose *RegistryCompose
}

func NewRegistryRoutes(s *RegistrySources, c *RegistryCompose) *RegistryRoutes {
	return &RegistryRoutes{sources: s, compose: c}
}

// regHandle is Handle plus the two answers this area adds: 409 for something not possible now and 422 for a failure
// of the command line tools that the person should read.
func regHandle(f HandlerFunc) http.HandlerFunc {
	return Handle(func(w http.ResponseWriter, r *http.Request) error {
		err := f(w, r)
		switch e := err.(type) {
		case RegistryConflict:
			WriteJSON(w, http.StatusConflict, ErrorDto{e.Msg})
			return nil
		case RegistryError:
			WriteJSON(w, http.StatusUnprocessableEntity, ErrorDto{e.Msg})
			return nil
		}
		return err
	})
}

func (x *RegistryRoutes) Register(mux *http.ServeMux) {
	// ---- sources
	mux.HandleFunc("GET /api/registry/sources", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, http.StatusOK, x.sources.List(r.Context()))
		return nil
	}))
	mux.HandleFunc("PUT /api/registry/sources/{id}", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		var def SourceDef
		if err := ReadJSON(r, &def); err != nil {
			return err
		}
		def.ID = Param(r, "id")
		v, err := x.sources.Put(r.Context(), def)
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))
	mux.HandleFunc("DELETE /api/registry/sources/{id}", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		id := Param(r, "id")
		if _, ok := x.sources.find(id); !ok {
			return UnknownTarget{"Unknown source '" + id + "'"}
		}
		if used := x.compose.UsingSource(id); len(used) > 0 {
			return RegistryConflict{"The source '" + id + "' is used by the compose profile(s): " + strings.Join(used, ", ")}
		}
		if err := x.sources.Delete(id); err != nil {
			return err
		}
		WriteJSON(w, http.StatusNoContent, nil)
		return nil
	}))
	mux.HandleFunc("POST /api/registry/sources/{id}/sync", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := x.sources.Sync(r.Context(), Param(r, "id"))
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))

	// ---- compose profiles
	mux.HandleFunc("GET /api/registry/compose", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, http.StatusOK, x.compose.List(r.Context()))
		return nil
	}))
	mux.HandleFunc("PUT /api/registry/compose/{id}", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		var def ComposeDef
		if err := ReadJSON(r, &def); err != nil {
			return err
		}
		def.ID = Param(r, "id")
		v, err := x.compose.Put(r.Context(), def)
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))
	mux.HandleFunc("DELETE /api/registry/compose/{id}", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		if err := x.compose.Delete(r.Context(), Param(r, "id")); err != nil {
			return err
		}
		WriteJSON(w, http.StatusNoContent, nil)
		return nil
	}))
	mux.HandleFunc("POST /api/registry/compose/{id}/validate", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := x.compose.Validate(r.Context(), Param(r, "id"))
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))
	mux.HandleFunc("POST /api/registry/compose/{id}/up", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := x.compose.Up(r.Context(), Param(r, "id"))
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusAccepted, v)
		return nil
	}))
	mux.HandleFunc("POST /api/registry/compose/{id}/down", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := x.compose.Down(r.Context(), Param(r, "id"), Query(r, "volumes") == "true")
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))
	mux.HandleFunc("GET /api/registry/compose/{id}/logs", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		tail := 200
		if t := Query(r, "tail"); t != "" {
			n, err := strconv.Atoi(t)
			if err != nil || n < 1 {
				return InvalidParam{"tail must be a positive number"}
			}
			tail = n
		}
		lines, err := x.compose.Logs(r.Context(), Param(r, "id"), tail, Query(r, "service"))
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, map[string]string{"lines": lines})
		return nil
	}))
	mux.HandleFunc("GET /api/registry/compose/{id}/file", regHandle(func(w http.ResponseWriter, r *http.Request) error {
		idx := 0
		if q := Query(r, "index"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil {
				return InvalidParam{"index must be a number"}
			}
			idx = n
		}
		v, err := x.compose.File(Param(r, "id"), idx)
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, v)
		return nil
	}))
}
