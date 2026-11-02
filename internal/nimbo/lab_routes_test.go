package nimbo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type labProgressFake struct {
	done     map[string]bool
	attempts []string
}

func (p *labProgressFake) Completed() []string {
	var out []string
	for id := range p.done {
		out = append(out, id)
	}
	return out
}

func (p *labProgressFake) Attempt(id string, passed bool) {
	p.attempts = append(p.attempts, id+"="+map[bool]string{true: "pass", false: "fail"}[passed])
	if passed {
		p.done[id] = true
	}
}

type labRouteServer struct {
	mux      *http.ServeMux
	world    *labWorld
	progress *labProgressFake
	stats    *fitActivity
}

func newLabRouteServer(t *testing.T) *labRouteServer {
	t.Helper()
	s := &labRouteServer{
		mux:      http.NewServeMux(),
		world:    &labWorld{buckets: []string{"lab-first-bucket"}, objectText: labStr("hello nimbo")},
		progress: &labProgressFake{done: map[string]bool{}},
		stats:    &fitActivity{},
	}
	verifier := NewLabVerifier(s.world, &labToxiFake{}, nil, nil)
	NewLabRoutes(labRepoCatalog(t), verifier, s.progress, s.stats).Register(s.mux)
	return s
}

func (s *labRouteServer) do(method, target string) (int, string) {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Code, rec.Body.String()
}

func TestLabAPIListReturnsEveryLabWithBilingualTextAndNoCheckParameters(t *testing.T) {
	s := newLabRouteServer(t)
	code, body := s.do("GET", "/api/labs")
	if code != 200 {
		t.Fatal(code)
	}
	var list struct {
		Labs     []map[string]any `json:"labs"`
		Problems []any            `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Labs) < 6 || list.Problems == nil {
		t.Fatalf("%d labs, problems %v", len(list.Labs), list.Problems)
	}
	first := list.Labs[0]
	if first["title"].(map[string]any)["es"] == nil {
		t.Fatal("no Spanish title")
	}
	check := first["checks"].([]any)[0].(map[string]any)
	if len(check) != 1 || check["label"] == nil {
		t.Fatalf("checks must not leak parameters: %v", check)
	}
	if strings.Contains(body, `"params"`) {
		t.Fatal("params leaked")
	}
	// every key the UI reads is present, nullable ones as null
	for _, k := range []string{"id", "level", "stack", "title", "summary", "steps", "hints", "solution", "checks"} {
		if _, ok := first[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestLabAPIVerifyRunsTheChecksAgainstTheEmulatorsAndRecordsProgress(t *testing.T) {
	s := newLabRouteServer(t)
	code, body := s.do("POST", "/api/labs/first-bucket/verify")
	var ok map[string]any
	_ = json.Unmarshal([]byte(body), &ok)
	if code != 200 || ok["passed"] != true || ok["id"] != "first-bucket" {
		t.Fatal(code, body)
	}
	fitEq(t, s.stats.entries, []string{"lab.verify|first-bucket|passed", "lab.complete|first-bucket|<nil>"})

	// a second pass is not a new completion
	s.do("POST", "/api/labs/first-bucket/verify")
	fitEq(t, s.stats.entries[2:], []string{"lab.verify|first-bucket|passed"})

	s.world.buckets = nil
	_, body = s.do("POST", "/api/labs/first-bucket/verify")
	var bad map[string]any
	_ = json.Unmarshal([]byte(body), &bad)
	if bad["passed"] != false {
		t.Fatal(body)
	}
	fitEq(t, s.progress.attempts, []string{"first-bucket=pass", "first-bucket=pass", "first-bucket=fail"})
	if got := s.stats.entries[len(s.stats.entries)-1]; got != "lab.verify|first-bucket|not yet" {
		t.Fatal(got)
	}
}

func TestLabAPIUnknownLabIs404(t *testing.T) {
	s := newLabRouteServer(t)
	for _, target := range []string{"/api/labs/ghost/verify", "/api/labs/..%2Fx/verify"} {
		code, body := s.do("POST", target)
		if code != http.StatusNotFound {
			t.Errorf("%s -> %d %s", target, code, body)
		}
	}
	if len(s.progress.attempts) != 0 || len(s.stats.entries) != 0 {
		t.Fatal("an unknown lab must record nothing")
	}
}

func TestLabAPIWorksWithoutTheDataLayer(t *testing.T) {
	mux := http.NewServeMux()
	world := &labWorld{buckets: []string{"lab-first-bucket"}, objectText: labStr("hello nimbo")}
	NewLabRoutes(labRepoCatalog(t), NewLabVerifier(world, &labToxiFake{}, nil, nil), nil, nil).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/labs/first-bucket/verify", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"passed":true`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestLabAPIProblemsAreListedForInvalidLabs(t *testing.T) {
	mux := http.NewServeMux()
	cat := &LabCatalog{Problems: []string{"x.yml: missing title"}}
	NewLabRoutes(cat, NewLabVerifier(&labWorld{}, &labToxiFake{}, nil, nil), nil, nil).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/labs", nil))
	if !strings.Contains(rec.Body.String(), `"labs":[]`) || !strings.Contains(rec.Body.String(), "x.yml: missing title") {
		t.Fatal(rec.Body.String())
	}
}
