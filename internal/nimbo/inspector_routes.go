package nimbo

import "net/http"

// Register mounts the read-only inspection routes. Errors from the emulators are mapped to HTTP statuses by Handle.
func (i *Inspector) Register(mux *http.ServeMux) {
	const base = "/api/inspect"
	mux.HandleFunc("GET "+base+"/aws/s3/buckets", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Buckets(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/s3/buckets/{bucket}/objects", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Objects(r.Context(), Param(r, "bucket"), Query(r, "prefix"), InspectObjectsMax)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/s3/buckets/{bucket}/object", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Preview(r.Context(), Param(r, "bucket"), Query(r, "key"), InspectPreviewBytes)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/sqs/queues", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Queues(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/sqs/queues/{queue}/messages", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Messages(r.Context(), Param(r, "queue"), InspectDefaultAccount)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/dynamodb/tables", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Tables(r.Context())
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/aws/dynamodb/tables/{table}/items", Handle(func(w http.ResponseWriter, r *http.Request) error {
		v, err := i.Items(r.Context(), Param(r, "table"), InspectItemsLimit)
		return inspectReply(w, v, err)
	}))
	mux.HandleFunc("GET "+base+"/gcp/pubsub", Handle(func(w http.ResponseWriter, r *http.Request) error {
		project := InspectDefaultProject
		if r.URL.Query().Has("project") {
			project = Query(r, "project")
		}
		v, err := i.Pubsub(r.Context(), project)
		return inspectReply(w, v, err)
	}))
}

// inspectReply answers 200 with the value, or hands the failure to Handle.
func inspectReply(w http.ResponseWriter, v any, err error) error {
	if err != nil {
		return err
	}
	WriteJSON(w, http.StatusOK, v)
	return nil
}
