package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// coreFakeRuntime stands in for Docker: it remembers what it was asked and which environment it got. It merges the
// three fakes of the Kotlin tests (FakeRuntime, RecordingRuntime, PersistingRuntime).
type coreFakeRuntime struct {
	mu        sync.Mutex
	docker    DockerStatusDto
	live      map[string]ContainerInfo
	failStart string          // every start fails with this message
	failing   map[string]bool // these providers fail to start with "port busy"
	calls     []string
	started   []coreStart
	saved     map[string]bool
	deleted   []string
}

type coreStart struct {
	id      string
	persist bool
	env     map[string]string
}

func newCoreFakeRuntime() *coreFakeRuntime {
	return &coreFakeRuntime{
		docker: DockerStatusDto{Available: true, Version: coreStr("29.0")},
		live:   map[string]ContainerInfo{},
		saved:  map[string]bool{},
	}
}

func (f *coreFakeRuntime) Status(context.Context) DockerStatusDto { return f.docker }

func (f *coreFakeRuntime) Containers(context.Context) map[string]ContainerInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]ContainerInfo{}
	for k, v := range f.live {
		out[k] = v
	}
	return out
}

func (f *coreFakeRuntime) Start(_ context.Context, def ProviderDef, env map[string]string, persist bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "start:"+def.ID)
	f.started = append(f.started, coreStart{def.ID, persist, env})
	if f.failStart != "" {
		return errors.New(f.failStart)
	}
	if f.failing[def.ID] {
		return errors.New("port busy")
	}
	f.live[def.ID] = ContainerInfo{ProviderID: def.ID, Running: true, Status: "Up", Persist: persist}
	if persist {
		f.saved[def.ID] = true
	}
	return nil
}

func (f *coreFakeRuntime) Stop(_ context.Context, def ProviderDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop:"+def.ID)
	f.live[def.ID] = ContainerInfo{ProviderID: def.ID, Running: false, Status: "Exited"}
	return nil
}

func (f *coreFakeRuntime) Remove(_ context.Context, def ProviderDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, def.ID)
	return nil
}

func (f *coreFakeRuntime) Logs(_ context.Context, def ProviderDef, tail int) (string, error) {
	return "line-" + def.ID + "-" + itoaForTest(tail), nil
}

func (f *coreFakeRuntime) SavedData(context.Context) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for k := range f.saved {
		out[k] = true
	}
	return out
}

func (f *coreFakeRuntime) DeleteData(_ context.Context, def ProviderDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live[def.ID].Running {
		return errors.New("Stop " + def.Name + " before deleting its data")
	}
	delete(f.saved, def.ID)
	f.deleted = append(f.deleted, def.ID)
	return nil
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func itoaForTest(n int) string { b, _ := json.Marshal(n); return string(b) }

// coreMemorySecrets is a SecretStore that keeps clear text, for tests only.
type coreMemorySecrets struct {
	avail  bool
	values map[string]string
}

func newCoreMemorySecrets() *coreMemorySecrets {
	return &coreMemorySecrets{avail: true, values: map[string]string{}}
}

func (m *coreMemorySecrets) Available() bool { return m.avail }
func (m *coreMemorySecrets) Reason() string {
	if m.avail {
		return ""
	}
	return "not here"
}
func (m *coreMemorySecrets) Names(context.Context) []string {
	var out []string
	for k := range m.values {
		out = append(out, k)
	}
	return out
}
func (m *coreMemorySecrets) Get(_ context.Context, n string) (string, bool, error) {
	v, ok := m.values[n]
	return v, ok, nil
}
func (m *coreMemorySecrets) Set(_ context.Context, n, v string) error {
	if err := ValidateSecret(n, v); err != nil {
		return err
	}
	m.values[n] = v
	return nil
}
func (m *coreMemorySecrets) Delete(_ context.Context, n string) error {
	delete(m.values, n)
	return nil
}

// coreResp is a finished request.
type coreResp struct {
	Code int
	Body string
}

// coreDo sends one request through the handler. extra holds header name/value pairs; "Host" sets the host.
func coreDo(h http.Handler, method, target, body string, extra ...string) coreResp {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Host = "localhost:8080" // httptest defaults to example.com, which the host guard rightly refuses
	for i := 0; i+1 < len(extra); i += 2 {
		if strings.EqualFold(extra[i], "Host") {
			req.Host = extra[i+1]
		} else {
			req.Header.Set(extra[i], extra[i+1])
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return coreResp{rec.Code, rec.Body.String()}
}

func coreObject(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not a JSON object: %v: %s", err, body)
	}
	return m
}

func coreTestServer(cfg Config, deps Deps) http.Handler {
	if deps.Runtime == nil {
		deps.Runtime = newCoreFakeRuntime()
	}
	return NewServer(cfg, deps)
}

func coreContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected %q to contain %q", s, sub)
	}
}

func coreEq[T comparable](t *testing.T, want, got T, what ...string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %v, got %v", strings.Join(what, " "), want, got)
	}
}

func coreTrue(t *testing.T, ok bool, what ...string) {
	t.Helper()
	if !ok {
		t.Errorf("expected true: %s", strings.Join(what, " "))
	}
}

// coreRepoFile finds a file of the repository from the package folder (internal/nimbo).
func coreRepoFile(t *testing.T, name string) string {
	t.Helper()
	for _, base := range []string{".", "..", "../..", "../../.."} {
		p := filepath.Join(base, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	t.Skipf("%s not found from the package folder", name)
	return ""
}
