package nimbo

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---- DTOs (same JSON as the Kotlin ones) --------------------------------------------------------------------------

type FitStepDto struct {
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Detail *string `json:"detail"`
	Ms     int64   `json:"ms"`
}

type SuiteResultDto struct {
	ID     string       `json:"id"`
	Cloud  string       `json:"cloud"`
	Name   string       `json:"name"`
	Status string       `json:"status"`
	Steps  []FitStepDto `json:"steps"`
	Ms     int64        `json:"ms"`
}

type SuiteRefDto struct {
	Cloud string `json:"cloud"`
	ID    string `json:"id"`
	Name  string `json:"name"`
}

type FidelityStateDto struct {
	Running   bool             `json:"running"`
	UpdatedAt *int64           `json:"updatedAt"`
	Catalog   []SuiteRefDto    `json:"catalog"`
	Results   []SuiteResultDto `json:"results"`
	Summary   map[string]int   `json:"summary"`
	Changes   []FidelityChange `json:"changes"`
}

type FidelityReportDto struct {
	StartedAt  int64            `json:"startedAt"`
	DurationMs int64            `json:"durationMs"`
	Suites     []SuiteResultDto `json:"suites"`
	Summary    map[string]int   `json:"summary"`
}

// ---- Scenario -----------------------------------------------------------------------------------------------------

// StepFailed is a step's own verdict ("the body differs"). Step blocks signal failure by panicking with it (or
// with any error): the block is the Go equivalent of the Kotlin lambda that throws, and Step recovers it.
type StepFailed struct{ Msg string }

func (e StepFailed) Error() string { return e.Msg }

// Scenario runs one suite: records steps, and skips the ones that depend on an earlier failure.
type Scenario struct {
	Steps   []FitStepDto
	failed  bool
	aborted error
}

// Step runs block and records its outcome. A step that is not independent is skipped once an earlier dependent
// step failed. An EmulatorUnreachable aborts the whole scenario: it is returned and every later Step returns it
// without running (the caller reports the suite as offline).
func (s *Scenario) Step(name string, independent bool, block func()) error {
	if s.aborted != nil {
		return s.aborted
	}
	if s.failed && !independent {
		d := "an earlier step failed"
		s.Steps = append(s.Steps, FitStepDto{Name: name, Status: "skipped", Detail: &d})
		return nil
	}
	started := time.Now()
	elapsed := func() int64 { return time.Since(started).Milliseconds() }
	err := fitCatch(block)
	var eu EmulatorUnreachable
	if errors.As(err, &eu) {
		s.aborted = err
		return err
	}
	if err != nil {
		if !independent {
			s.failed = true
		}
		msg := err.Error()
		if msg == "" {
			msg = fmt.Sprintf("%T", err)
		}
		d := fitTake(msg, 220)
		s.Steps = append(s.Steps, FitStepDto{Name: name, Status: "fail", Detail: &d, Ms: elapsed()})
		return nil
	}
	s.Steps = append(s.Steps, FitStepDto{Name: name, Status: "pass", Ms: elapsed()})
	return nil
}

// fitCatch runs f and turns a panic (a StepFailed, a gateway error or a genuine bug) into an error.
func fitCatch(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	f()
	return nil
}

func fitTake(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Cleanups remembers how to undo what a suite created and runs it in reverse, ignoring failures.
type Cleanups struct{ actions []func() }

func (c *Cleanups) Add(action func()) { c.actions = append(c.actions, action) }

func (c *Cleanups) Run() {
	for i := len(c.actions) - 1; i >= 0; i-- {
		_ = fitCatch(c.actions[i])
	}
}

// ---- Conformance --------------------------------------------------------------------------------------------------

type fitSuite struct {
	id, cloud, name string
	run             func(f *fitCtx)
}

// fitCtx is what a suite sees: its scenario, its cleanups, a per-run tag and the wire helpers.
type fitCtx struct {
	c       *Conformance
	ctx     context.Context
	s       *Scenario
	cleanup *Cleanups
	tag     string
}

func (f *fitCtx) step(name string, block func())  { _ = f.s.Step(name, false, block) }
func (f *fitCtx) indep(name string, block func()) { _ = f.s.Step(name, true, block) }

// Conformance exercises real operations on each emulator and reports what works, what only partly works and what
// is missing. It is the honest answer to "can I trust this emulator for X?", and it also surfaces emulator bugs.
type Conformance struct {
	http            HTTPGateway
	aws, azure, gcp string
	suites          []fitSuite
}

// NewConformance talks to the emulators on localhost at their default ports.
func NewConformance(gw HTTPGateway) *Conformance {
	return NewConformanceAt(gw, "http://localhost:4566", "http://localhost:4577", "http://localhost:4588")
}

// NewConformanceAt takes the base URL of each emulator (Nimbo reaches them at NIMBO_EMULATOR_HOST).
func NewConformanceAt(gw HTTPGateway, aws, azure, gcp string) *Conformance {
	c := &Conformance{http: gw, aws: aws, azure: azure, gcp: gcp}
	c.suites = fitSuites()
	return c
}

func (c *Conformance) Catalog() []SuiteRefDto {
	out := make([]SuiteRefDto, 0, len(c.suites))
	for _, s := range c.suites {
		out = append(out, SuiteRefDto{Cloud: s.cloud, ID: s.id, Name: s.name})
	}
	return out
}

// Run runs the suites matching the filters ("" matches everything).
func (c *Conformance) Run(ctx context.Context, cloud, suite string) FidelityReportDto {
	started := time.Now().UnixMilli()
	results := []SuiteResultDto{}
	for _, s := range c.suites {
		if (cloud == "" || s.cloud == cloud) && (suite == "" || s.id == suite) {
			results = append(results, c.runSuite(ctx, s))
		}
	}
	summary := map[string]int{}
	for _, r := range results {
		summary[r.Status]++
	}
	return FidelityReportDto{StartedAt: started, DurationMs: time.Now().UnixMilli() - started, Suites: results, Summary: summary}
}

func (c *Conformance) runSuite(ctx context.Context, suite fitSuite) SuiteResultDto {
	scenario := &Scenario{}
	cleanup := &Cleanups{}
	started := time.Now()
	f := &fitCtx{c: c, ctx: ctx, s: scenario, cleanup: cleanup, tag: fitTag()}
	suite.run(f)
	offline := scenario.aborted != nil
	// Nothing is sent towards an emulator that is already known to be down.
	if !offline {
		cleanup.Run()
	}
	steps := scenario.Steps
	if steps == nil {
		steps = []FitStepDto{}
	}
	passed := 0
	for _, s := range steps {
		if s.Status == "pass" {
			passed++
		}
	}
	status := "partial"
	switch {
	case offline:
		status = "offline"
	case len(steps) == 0 || passed == 0:
		status = "missing"
	case passed == len(steps):
		status = "full"
	}
	return SuiteResultDto{ID: suite.id, Cloud: suite.cloud, Name: suite.name, Status: status, Steps: steps, Ms: time.Since(started).Milliseconds()}
}

// fitTag is the first 8 characters of a random id, so two runs never share resource names.
func fitTag() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- wire helpers -------------------------------------------------------------------------------------------------

type fitReply struct{ HTTPReply }

var fitSpaces = regexp.MustCompile(`\s+`)

// ok fails the step unless the status is in codes (any 2xx when none are given).
func (r fitReply) ok(what string, codes ...int) fitReply {
	good := r.Status >= 200 && r.Status <= 299
	if len(codes) > 0 {
		good = false
		for _, c := range codes {
			if c == r.Status {
				good = true
			}
		}
	}
	if !good {
		panic(StepFailed{fmt.Sprintf("%s -> HTTP %d: %s", what, r.Status, fitTake(fitSpaces.ReplaceAllString(r.Body, " "), 120))})
	}
	return r
}

func fitNeed(condition bool, message string) {
	if !condition {
		panic(StepFailed{message})
	}
}

func fitXMLTag(xml, name string) (string, bool) {
	m := regexp.MustCompile("<" + regexp.QuoteMeta(name) + ">([^<]*)</" + regexp.QuoteMeta(name) + ">").FindStringSubmatch(xml)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// fitTagOr is fitXMLTag that fails the step with msg when the tag is missing.
func fitTagOr(xml, name, msg string) string {
	v, ok := fitXMLTag(xml, name)
	if !ok {
		panic(StepFailed{msg})
	}
	return v
}

func fitB64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func fitHas(s, sub string) bool { return strings.Contains(s, sub) }

func fitURLPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(StepFailed{err.Error()})
	}
	return u.Path
}

// fitMerge returns a copy of base with extra added.
func fitMerge(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (f *fitCtx) send(base, method, path string, headers map[string]string, body []string) fitReply {
	var b *string
	if len(body) > 0 {
		b = &body[0]
	}
	reply, err := f.c.http.Send(f.ctx, method, base+path, headers, b)
	if err != nil {
		panic(err)
	}
	return fitReply{reply}
}

func fitAWSAuth(service string) map[string]string {
	return map[string]string{"Authorization": "AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/" + service + "/aws4_request, SignedHeaders=host, Signature=0"}
}

func (f *fitCtx) s3(method, path string, body ...string) fitReply {
	return f.send(f.c.aws, method, path, fitAWSAuth("s3"), body)
}

// query is an AWS Query-protocol call: kv are alternating form keys and values, sent in that order.
func (f *fitCtx) query(service, path string, kv ...string) fitReply {
	var form []string
	for i := 0; i+1 < len(kv); i += 2 {
		form = append(form, QueryEscape(kv[i])+"="+QueryEscape(kv[i+1]))
	}
	return f.send(f.c.aws, "POST", path, fitMerge(fitAWSAuth(service), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), []string{strings.Join(form, "&")})
}

// jsonCall is an AWS JSON-protocol call. body is a raw JSON string, a map, or nil for {}.
func (f *fitCtx) jsonCall(service, target, version string, body any) fitReply {
	var text string
	switch b := body.(type) {
	case nil:
		text = "{}"
	case string:
		text = b
	default:
		text = fitJSON(b)
	}
	return f.send(f.c.aws, "POST", "/", fitMerge(fitAWSAuth(service), map[string]string{"X-Amz-Target": target, "Content-Type": "application/x-amz-json-" + version}), []string{text})
}

// fitJSON encodes without HTML escaping so what the emulator receives is what was written.
func fitJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

type fitM = map[string]any

// fitObj parses a reply body as a JSON object (an empty body is {}). Numbers keep their text.
func fitObj(r fitReply) fitM {
	text := strings.TrimSpace(r.Body)
	if text == "" {
		text = "{}"
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		panic(StepFailed{"invalid JSON: " + err.Error()})
	}
	m, ok := v.(map[string]any)
	if !ok {
		panic(StepFailed{"expected a JSON object"})
	}
	return m
}

// fitStr walks path through nested objects and returns the primitive found there as text.
func fitStr(m fitM, path ...string) (string, bool) {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		if cur, ok = mm[k]; !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	case nil:
		return "null", true
	}
	return "", false
}

// fitStrOr is fitStr that fails the step with msg when the value is missing.
func fitStrOr(m fitM, msg string, path ...string) string {
	v, ok := fitStr(m, path...)
	if !ok {
		panic(StepFailed{msg})
	}
	return v
}

func fitFirst(m fitM, key string) (fitM, bool) {
	arr, ok := m[key].([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	first, ok := arr[0].(map[string]any)
	return first, ok
}

func (f *fitCtx) azHeaders(extra map[string]string) map[string]string {
	return fitMerge(map[string]string{
		"x-ms-version":  "2021-08-06",
		"x-ms-date":     time.Now().UTC().Format(http.TimeFormat),
		"Authorization": "SharedKey devstoreaccount1:AAAA",
	}, extra)
}

func (f *fitCtx) az(method, path string, body *string, extra map[string]string) fitReply {
	var b []string
	if body != nil {
		b = []string{*body}
	}
	return f.send(f.c.azure, method, path, f.azHeaders(extra), b)
}

func (f *fitCtx) g(method, path string, body ...string) fitReply {
	return f.gct(method, path, "application/json", body...)
}

func (f *fitCtx) gct(method, path, contentType string, body ...string) fitReply {
	return f.send(f.c.gcp, method, path, map[string]string{"Content-Type": contentType}, body)
}

func (f *fitCtx) lambda(method, path string, body ...string) fitReply {
	return f.send(f.c.aws, method, path, fitMerge(fitAWSAuth("lambda"), map[string]string{"Content-Type": "application/json"}), body)
}

func fitLambdaZip() string {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create("index.js")
	if err != nil {
		panic(err)
	}
	_, _ = w.Write([]byte("exports.handler = async () => ({ ok: true });"))
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func fitSP(s string) *string { return &s }
