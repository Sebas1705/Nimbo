package nimbo

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
)

// fidelityHistory is the part of the data layer the fidelity routes use (FidelityHistory in Kotlin). The
// integrator adapts the real store to it.
type fidelityHistory interface {
	// ChangesFor says what changed against the last time each suite ran. Offline suites never count as regressions.
	ChangesFor(report FidelityReportDto) []FidelityChange
	Record(report FidelityReportDto)
	// List is the stored run summaries, oldest first, as they are sent to the UI (a slice; nil is sent as []).
	List() any
}

// activityLog is the part of the stats journal (StatsService.record) used by the fidelity and lab routes.
type activityLog interface {
	Record(kind, subject string, detail *string)
}

// FidelityStore keeps the latest result of every suite so a partial run (one cloud) does not discard the others,
// and guards against two runs at once.
type FidelityStore struct {
	mu        sync.Mutex
	results   map[string]SuiteResultDto
	updatedAt *int64
	running   atomic.Bool
}

func NewFidelityStore() *FidelityStore {
	return &FidelityStore{results: map[string]SuiteResultDto{}}
}

// TryLock claims the single run slot; it answers false when a run is already in progress.
func (s *FidelityStore) TryLock() bool { return s.running.CompareAndSwap(false, true) }
func (s *FidelityStore) Unlock()       { s.running.Store(false) }
func (s *FidelityStore) Running() bool { return s.running.Load() }

func (s *FidelityStore) Merge(report FidelityReportDto) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range report.Suites {
		s.results[r.ID] = r
	}
	at := report.StartedAt + report.DurationMs
	s.updatedAt = &at
}

// Snapshot returns the stored results in the catalog's order.
func (s *FidelityStore) Snapshot(order []SuiteRefDto) []SuiteResultDto {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []SuiteResultDto{}
	for _, ref := range order {
		if r, ok := s.results[ref.ID]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (s *FidelityStore) UpdatedAt() *int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updatedAt
}

// FidelityRoutes serves /api/fidelity. history and stats may be nil (nothing is recorded then).
type FidelityRoutes struct {
	conformance *Conformance
	store       *FidelityStore
	history     fidelityHistory
	stats       activityLog
}

func NewFidelityRoutes(c *Conformance, store *FidelityStore, history fidelityHistory, stats activityLog) *FidelityRoutes {
	if store == nil {
		store = NewFidelityStore()
	}
	return &FidelityRoutes{conformance: c, store: store, history: history, stats: stats}
}

func (x *FidelityRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fidelity", Handle(x.state))
	mux.HandleFunc("POST /api/fidelity/run", Handle(x.run))
	mux.HandleFunc("GET /api/fidelity/history", Handle(x.list))
}

func (x *FidelityRoutes) snapshot(changes []FidelityChange) FidelityStateDto {
	catalog := x.conformance.Catalog()
	results := x.store.Snapshot(catalog)
	summary := map[string]int{}
	for _, r := range results {
		summary[r.Status]++
	}
	if changes == nil {
		changes = []FidelityChange{}
	}
	return FidelityStateDto{Running: x.store.Running(), UpdatedAt: x.store.UpdatedAt(), Catalog: catalog, Results: results, Summary: summary, Changes: changes}
}

func (x *FidelityRoutes) state(w http.ResponseWriter, r *http.Request) error {
	WriteJSON(w, http.StatusOK, x.snapshot(nil))
	return nil
}

func (x *FidelityRoutes) list(w http.ResponseWriter, r *http.Request) error {
	var v any = []any{}
	if x.history != nil {
		if l := x.history.List(); l != nil && !(reflect.ValueOf(l).Kind() == reflect.Slice && reflect.ValueOf(l).IsNil()) {
			v = l
		}
	}
	WriteJSON(w, http.StatusOK, v)
	return nil
}

func (x *FidelityRoutes) run(w http.ResponseWriter, r *http.Request) error {
	cloud, suite := Query(r, "cloud"), Query(r, "suite")
	if cloud != "" && cloud != "aws" && cloud != "azure" && cloud != "gcp" {
		return InvalidParam{"cloud must be aws, azure or gcp"}
	}
	if suite != "" {
		known := false
		for _, s := range x.conformance.Catalog() {
			if s.ID == suite && (cloud == "" || s.Cloud == cloud) {
				known = true
			}
		}
		if !known {
			return InvalidParam{"Unknown suite '" + suite + "'"}
		}
	}
	if !x.store.TryLock() {
		WriteJSON(w, http.StatusConflict, ErrorDto{"A run is already in progress"})
		return nil
	}
	changes := x.execute(r.Context(), cloud, suite)
	WriteJSON(w, http.StatusOK, x.snapshot(changes))
	return nil
}

// execute runs while holding the slot and always releases it. The run is not tied to the request: closing the
// tab must not leave half-created resources behind, so cleanup always gets to finish.
func (x *FidelityRoutes) execute(ctx context.Context, cloud, suite string) []FidelityChange {
	defer x.store.Unlock()
	report := x.conformance.Run(context.WithoutCancel(ctx), cloud, suite)
	var changes []FidelityChange
	if x.history != nil {
		changes = x.history.ChangesFor(report)
		x.history.Record(report)
	}
	if x.stats != nil {
		scope := cloud
		if scope == "" {
			scope = "all"
		}
		var detail *string
		if suite != "" {
			detail = &suite
		}
		x.stats.Record("fidelity.run", scope, detail)
	}
	x.store.Merge(report)
	return changes
}
