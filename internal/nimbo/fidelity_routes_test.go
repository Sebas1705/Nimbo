package nimbo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fitHistoryFake records what the routes hand to the data layer.
type fitHistoryFake struct {
	recorded []FidelityReportDto
	changes  []FidelityChange
	list     any
}

func (h *fitHistoryFake) ChangesFor(FidelityReportDto) []FidelityChange { return h.changes }
func (h *fitHistoryFake) Record(r FidelityReportDto)                    { h.recorded = append(h.recorded, r) }
func (h *fitHistoryFake) List() any                                     { return h.list }

type fitActivity struct{ entries []string }

func (a *fitActivity) Record(kind, subject string, detail *string) {
	d := "<nil>"
	if detail != nil {
		d = *detail
	}
	a.entries = append(a.entries, kind+"|"+subject+"|"+d)
}

type fitServer struct {
	mux     *http.ServeMux
	store   *FidelityStore
	history *fitHistoryFake
	stats   *fitActivity
}

func newFitServer(gw HTTPGateway) *fitServer {
	s := &fitServer{mux: http.NewServeMux(), store: NewFidelityStore(), history: &fitHistoryFake{}, stats: &fitActivity{}}
	NewFidelityRoutes(NewConformance(gw), s.store, s.history, s.stats).Register(s.mux)
	return s
}

func (s *fitServer) do(method, path string) (int, map[string]any, string) {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

func TestFidelityStateStartsEmptyWithTheFullCatalog(t *testing.T) {
	s := newFitServer(newFitFakeS3())
	code, body, raw := s.do("GET", "/api/fidelity")
	if code != 200 {
		t.Fatal(code)
	}
	if len(body["results"].([]any)) != 0 || len(body["catalog"].([]any)) < 15 || body["running"] != false {
		t.Fatal(raw)
	}
	// every key is always present, nullable ones as null
	for _, k := range []string{"updatedAt", "summary", "changes"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing %s in %s", k, raw)
		}
	}
	if body["updatedAt"] != nil {
		t.Fatal(raw)
	}
}

func TestFidelityARunStoresResultsAndALaterRunOfAnotherSuiteKeepsThem(t *testing.T) {
	s := newFitServer(newFitFakeS3())
	s.do("POST", "/api/fidelity/run?cloud=aws&suite=s3")
	code, after, raw := s.do("POST", "/api/fidelity/run?cloud=gcp&suite=pubsub")
	if code != 200 {
		t.Fatal(code, raw)
	}
	var ids []string
	for _, r := range after["results"].([]any) {
		ids = append(ids, r.(map[string]any)["id"].(string))
	}
	fitEq(t, ids, []string{"s3", "pubsub"})
	if !strings.Contains(raw, `"full"`) {
		t.Fatal(raw)
	}
	if after["updatedAt"] == nil || after["running"] != false {
		t.Fatal(raw)
	}
}

func TestFidelityInvalidFiltersAreRejectedBeforeAnyTraffic(t *testing.T) {
	fake := newFitFakeS3()
	s := newFitServer(fake)
	for _, q := range []string{"cloud=oracle", "suite=nope", "cloud=gcp&suite=s3"} {
		if code, _, raw := s.do("POST", "/api/fidelity/run?"+q); code != 400 {
			t.Fatalf("%s -> %d %s", q, code, raw)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("traffic: %v", fake.calls)
	}
	if len(s.history.recorded) != 0 {
		t.Fatal("a rejected run must not be recorded")
	}
}

func TestFidelityOnlyOneRunAtATime(t *testing.T) {
	s := newFitServer(newFitFakeS3())
	if !s.store.TryLock() {
		t.Fatal("could not lock")
	}
	if code, body, _ := s.do("POST", "/api/fidelity/run"); code != 409 || body["error"] != "A run is already in progress" {
		t.Fatal(code, body)
	}
	if _, _, raw := s.do("GET", "/api/fidelity"); !strings.Contains(raw, `"running":true`) {
		t.Fatal(raw)
	}
	s.store.Unlock()
	if code, _, _ := s.do("POST", "/api/fidelity/run?suite=s3"); code != 200 {
		t.Fatal(code)
	}
	if s.store.Running() {
		t.Fatal("the slot must be released after a run")
	}
}

func TestFidelityARunIsRecordedWithItsChangesAndActivity(t *testing.T) {
	s := newFitServer(newFitFakeS3())
	s.history.changes = []FidelityChange{{Suite: "s3", From: "full", To: "partial", Regression: true}}
	code, body, raw := s.do("POST", "/api/fidelity/run?cloud=aws&suite=s3")
	if code != 200 {
		t.Fatal(code, raw)
	}
	changes := body["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["regression"] != true {
		t.Fatal(raw)
	}
	if len(s.history.recorded) != 1 || s.history.recorded[0].Suites[0].ID != "s3" {
		t.Fatalf("%+v", s.history.recorded)
	}
	fitEq(t, s.stats.entries, []string{"fidelity.run|aws|s3"})

	s.do("POST", "/api/fidelity/run?cloud=gcp")
	if got := s.stats.entries[1]; got != "fidelity.run|gcp|<nil>" {
		t.Fatal(got)
	}
	s.do("POST", "/api/fidelity/run?suite=s3")
	if got := s.stats.entries[2]; got != "fidelity.run|all|s3" {
		t.Fatal(got)
	}
	// the state of a plain GET carries no changes
	if _, state, _ := s.do("GET", "/api/fidelity"); len(state["changes"].([]any)) != 0 {
		t.Fatal("GET must not report changes")
	}
}

func TestFidelityHistoryIsSentAsAListEvenWhenEmpty(t *testing.T) {
	s := newFitServer(newFitFakeS3())
	if _, _, raw := s.do("GET", "/api/fidelity/history"); strings.TrimSpace(raw) != "[]" {
		t.Fatalf("nil: %s", raw)
	}
	s.history.list = []string(nil)
	if _, _, raw := s.do("GET", "/api/fidelity/history"); strings.TrimSpace(raw) != "[]" {
		t.Fatalf("nil slice: %s", raw)
	}
	s.history.list = []map[string]any{{"at": 1, "steps": 2}}
	if _, _, raw := s.do("GET", "/api/fidelity/history"); !strings.Contains(raw, `"steps":2`) {
		t.Fatal(raw)
	}
}

func TestFidelityStoreKeepsCatalogOrderAndUpdatedAt(t *testing.T) {
	st := NewFidelityStore()
	order := []SuiteRefDto{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	st.Merge(FidelityReportDto{StartedAt: 100, DurationMs: 5, Suites: []SuiteResultDto{{ID: "c", Status: "full"}, {ID: "a", Status: "missing"}}})
	st.Merge(FidelityReportDto{StartedAt: 200, DurationMs: 7, Suites: []SuiteResultDto{{ID: "a", Status: "full"}}})
	got := st.Snapshot(order)
	if len(got) != 2 || got[0].ID != "a" || got[0].Status != "full" || got[1].ID != "c" {
		t.Fatalf("%+v", got)
	}
	if u := st.UpdatedAt(); u == nil || *u != 207 {
		t.Fatalf("updatedAt %v", u)
	}
}

func TestFidelityRunWorksWithoutTheDataLayer(t *testing.T) {
	mux := http.NewServeMux()
	NewFidelityRoutes(NewConformance(newFitFakeS3()), nil, nil, nil).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/fidelity/run?suite=s3", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/fidelity/history", nil))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatal(rec.Body.String())
	}
}
