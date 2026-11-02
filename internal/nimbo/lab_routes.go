package nimbo

import "net/http"

// labProgress is the part of the progress store (ProgressService) the lab routes use.
type labProgress interface {
	// Completed is the ids of the labs that were ever completed.
	Completed() []string
	// Attempt records a verification. Completion is sticky: failing later does not take the badge away.
	Attempt(labID string, passed bool)
}

// LabRoutes serves /api/labs. progress and stats may be nil (nothing is recorded then).
type LabRoutes struct {
	labs     *LabCatalog
	verifier *LabVerifier
	progress labProgress
	stats    activityLog
}

func NewLabRoutes(labs *LabCatalog, verifier *LabVerifier, progress labProgress, stats activityLog) *LabRoutes {
	return &LabRoutes{labs: labs, verifier: verifier, progress: progress, stats: stats}
}

func (x *LabRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/labs", Handle(x.list))
	mux.HandleFunc("POST /api/labs/{id}/verify", Handle(x.verify))
}

func (x *LabRoutes) list(w http.ResponseWriter, r *http.Request) error {
	dtos := make([]LabDto, 0, len(x.labs.Labs))
	for _, l := range x.labs.Labs {
		dtos = append(dtos, l.Dto)
	}
	problems := x.labs.Problems
	if problems == nil {
		problems = []string{}
	}
	WriteJSON(w, http.StatusOK, LabListDto{Labs: dtos, Problems: problems})
	return nil
}

func (x *LabRoutes) verify(w http.ResponseWriter, r *http.Request) error {
	id := Param(r, "id")
	lab := x.labs.Find(id)
	if lab == nil {
		return UnknownTarget{"Unknown lab '" + id + "'"}
	}
	firstTime := true
	if x.progress != nil {
		for _, done := range x.progress.Completed() {
			if done == id {
				firstTime = false
			}
		}
	}
	result := x.verifier.Verify(r.Context(), lab)
	if x.progress != nil {
		x.progress.Attempt(id, result.Passed)
	}
	if x.stats != nil {
		outcome := "not yet"
		if result.Passed {
			outcome = "passed"
		}
		x.stats.Record("lab.verify", id, &outcome)
		if result.Passed && firstTime {
			x.stats.Record("lab.complete", id, nil)
		}
	}
	WriteJSON(w, http.StatusOK, result)
	return nil
}
