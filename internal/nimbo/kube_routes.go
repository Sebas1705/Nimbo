package nimbo

import "net/http"

// Register mounts the Kubernetes routes. Failures of kubectl itself (bad manifest, unknown resource) are KubeErrors,
// which Handle maps to 422.
func (k *Kube) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/k8s", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := k.Overview(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET /api/k8s/samples", Handle(func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, http.StatusOK, KubeSamples)
		return nil
	}))
	mux.HandleFunc("GET /api/k8s/kubeconfig", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := k.Kubeconfig(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("POST /api/k8s/apply", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Manifest *string `json:"manifest"`
		}
		if err := ReadJSON(r, &body); err != nil {
			return err
		}
		if body.Manifest == nil { // the field is required
			return BadBody{}
		}
		v, err := k.Apply(r.Context(), *body.Manifest)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("POST /api/k8s/namespaces/{ns}/deployments/{name}/scale", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Replicas *int `json:"replicas"`
		}
		if err := ReadJSON(r, &body); err != nil {
			return err
		}
		if body.Replicas == nil {
			return BadBody{}
		}
		v, err := k.Scale(r.Context(), Param(r, "ns"), Param(r, "name"), *body.Replicas)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET /api/k8s/namespaces/{ns}/pods/{name}/logs", Handle(func(w http.ResponseWriter, r *http.Request) error {
		tail := 200
		if n, ok := obsAtoi32(Query(r, "tail")); ok {
			tail = n
		}
		v, err := k.Logs(r.Context(), Param(r, "ns"), Param(r, "name"), tail)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("DELETE /api/k8s/namespaces/{ns}/{kind}/{name}", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := k.Delete(r.Context(), Param(r, "kind"), Param(r, "ns"), Param(r, "name"))
		return inspectReply(w, v, err)
	}))
}
