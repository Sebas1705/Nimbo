package nimbo

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fitGW adapts a function to HTTPGateway.
type fitGW func(method, url string, headers map[string]string, body *string) (HTTPReply, error)

func (g fitGW) Send(_ context.Context, method, url string, headers map[string]string, body *string) (HTTPReply, error) {
	return g(method, url, headers, body)
}

// fitFakeS3 is a minimal in-memory S3, enough for the S3 suite. failOn makes matching requests return 500.
type fitFakeS3 struct {
	mu      sync.Mutex
	buckets map[string]map[string]string
	order   []string
	calls   []string
	failOn  func(method, path string) bool
}

func newFitFakeS3() *fitFakeS3 { return &fitFakeS3{buckets: map[string]map[string]string{}} }

func (f *fitFakeS3) Send(_ context.Context, method, rawURL string, _ map[string]string, body *string) (HTTPReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(rawURL, "http://localhost:4566")
	f.calls = append(f.calls, method+" "+path)
	if f.failOn != nil && f.failOn(method, path) {
		return HTTPReply{500, "boom"}, nil
	}
	clean, _, _ := strings.Cut(path, "?")
	parts := strings.SplitN(strings.Trim(clean, "/"), "/", 2)
	bucket := parts[0]
	key, hasKey := "", len(parts) > 1
	if hasKey {
		key = parts[1]
	}
	b, bucketExists := f.buckets[bucket]
	text := ""
	if body != nil {
		text = *body
	}
	switch {
	case method == "PUT" && !hasKey:
		f.buckets[bucket] = map[string]string{}
		return HTTPReply{200, ""}, nil
	case method == "PUT":
		if !bucketExists {
			return HTTPReply{404, "NoSuchBucket"}, nil
		}
		b[key] = text
		return HTTPReply{200, ""}, nil
	case method == "GET" && !hasKey:
		if !bucketExists {
			return HTTPReply{404, "NoSuchBucket"}, nil
		}
		keys := make([]string, 0, len(b))
		for k := range b {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteString("<Contents><Key>" + k + "</Key></Contents>")
		}
		return HTTPReply{200, sb.String()}, nil
	case method == "GET":
		if v, ok := b[key]; bucketExists && ok {
			return HTTPReply{200, v}, nil
		}
		return HTTPReply{404, "NoSuchKey"}, nil
	case method == "DELETE" && hasKey:
		delete(b, key)
		return HTTPReply{204, ""}, nil
	case method == "DELETE":
		delete(f.buckets, bucket)
		return HTTPReply{204, ""}, nil
	}
	return HTTPReply{400, "unsupported"}, nil
}

func (f *fitFakeS3) firstPut() string {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "PUT") {
			return c
		}
	}
	return ""
}

func fitStatuses(steps []FitStepDto) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Status
	}
	return out
}

func fitEq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func fitDetail(s FitStepDto) string {
	if s.Detail == nil {
		return ""
	}
	return *s.Detail
}

func TestScenarioPassingStepsAreRecordedWithTimings(t *testing.T) {
	s := &Scenario{}
	s.Step("a", false, func() {})
	s.Step("b", false, func() {})
	fitEq(t, fitStatuses(s.Steps), []string{"pass", "pass"})
	for _, st := range s.Steps {
		if st.Ms < 0 {
			t.Fatal("negative timing")
		}
	}
}

func TestScenarioAFailureSkipsTheDependentStepsThatFollow(t *testing.T) {
	s := &Scenario{}
	s.Step("create", false, func() {})
	s.Step("use", false, func() { panic(StepFailed{"nope"}) })
	s.Step("later", false, func() { t.Fatal("must not run") })
	fitEq(t, fitStatuses(s.Steps), []string{"pass", "fail", "skipped"})
	if fitDetail(s.Steps[1]) != "nope" {
		t.Fatalf("detail %q", fitDetail(s.Steps[1]))
	}
	if !strings.Contains(fitDetail(s.Steps[2]), "earlier step") {
		t.Fatalf("detail %q", fitDetail(s.Steps[2]))
	}
}

func TestScenarioIndependentStepsStillRunAfterAFailureAndDoNotPoisonTheRest(t *testing.T) {
	s := &Scenario{}
	s.Step("create", false, func() { panic(StepFailed{"x"}) })
	ran := false
	s.Step("optional", true, func() { ran = true })
	s.Step("dependent", false, func() {})
	if !ran {
		t.Fatal("independent step did not run")
	}
	fitEq(t, fitStatuses(s.Steps), []string{"fail", "pass", "skipped"})
}

func TestScenarioAFailingIndependentStepDoesNotSkipTheNextDependentStep(t *testing.T) {
	s := &Scenario{}
	s.Step("create", false, func() {})
	s.Step("optional", true, func() { panic(StepFailed{"meh"}) })
	s.Step("after", false, func() {})
	fitEq(t, fitStatuses(s.Steps), []string{"pass", "fail", "pass"})
}

func TestScenarioUnexpectedPanicsBecomeFailuresInsteadOfCrashingTheRun(t *testing.T) {
	s := &Scenario{}
	s.Step("boom", false, func() { panic("kaput") })
	s.Step("nil", true, func() { var m map[string]int; m["x"] = 1 })
	if s.Steps[0].Status != "fail" || !strings.Contains(fitDetail(s.Steps[0]), "kaput") {
		t.Fatalf("%+v", s.Steps[0])
	}
	if s.Steps[1].Status != "fail" {
		t.Fatalf("%+v", s.Steps[1])
	}
}

func TestScenarioLongDetailsAreTruncated(t *testing.T) {
	s := &Scenario{}
	s.Step("long", false, func() { panic(StepFailed{strings.Repeat("é", 500)}) })
	if n := len([]rune(fitDetail(s.Steps[0]))); n != 220 {
		t.Fatalf("detail has %d characters", n)
	}
}

func TestScenarioAnUnreachableEmulatorAbortsTheSuite(t *testing.T) {
	s := &Scenario{}
	err := s.Step("x", false, func() { panic(EmulatorUnreachable{"down"}) })
	var eu EmulatorUnreachable
	if !errors.As(err, &eu) {
		t.Fatalf("expected EmulatorUnreachable, got %v", err)
	}
	// once aborted nothing else runs
	if s.Step("y", true, func() { t.Fatal("must not run") }) == nil {
		t.Fatal("later steps must report the abort")
	}
	if len(s.Steps) != 0 {
		t.Fatalf("an aborted step is not recorded: %+v", s.Steps)
	}
}

func TestCleanupsRunInReverseOrderAndSurviveTheirOwnFailures(t *testing.T) {
	var order []int
	c := &Cleanups{}
	c.Add(func() { order = append(order, 1) })
	c.Add(func() { panic("cleanup broke") })
	c.Add(func() { order = append(order, 3) })
	c.Run()
	if len(order) != 2 || order[0] != 3 || order[1] != 1 {
		t.Fatalf("order %v", order)
	}
}

func fitRunSuite(t *testing.T, c *Conformance, cloud, suite string) []SuiteResultDto {
	t.Helper()
	return c.Run(context.Background(), cloud, suite).Suites
}

func TestConformanceAFaithfulEmulatorGetsAFullStatus(t *testing.T) {
	fake := newFitFakeS3()
	res := fitRunSuite(t, NewConformance(fake), "", "s3")
	if len(res) != 1 || res[0].Status != "full" || len(res[0].Steps) != 6 {
		t.Fatalf("%+v", res)
	}
	if len(fake.buckets) != 0 {
		t.Fatal("the suite must leave nothing behind")
	}
}

func TestConformanceAFailureMidwayIsPartialLaterStepsAreSkippedAndCleanupStillRuns(t *testing.T) {
	fake := newFitFakeS3()
	fake.failOn = func(m, p string) bool { return m == "GET" && strings.Contains(p, "/hello.txt") }
	res := fitRunSuite(t, NewConformance(fake), "", "s3")[0]
	if res.Status != "partial" {
		t.Fatalf("status %s", res.Status)
	}
	fitEq(t, fitStatuses(res.Steps), []string{"pass", "pass", "fail", "skipped", "skipped", "skipped"})
	if !strings.Contains(fitDetail(res.Steps[2]), "HTTP 500") {
		t.Fatalf("detail %q", fitDetail(res.Steps[2]))
	}
	if len(fake.buckets) != 0 {
		t.Fatal("cleanup must remove the bucket even after a failure")
	}
	deleted := false
	for _, c := range fake.calls {
		deleted = deleted || strings.HasPrefix(c, "DELETE")
	}
	if !deleted {
		t.Fatal("no DELETE was sent")
	}
}

func TestConformanceAnEmulatorThatImplementsNothingIsMissing(t *testing.T) {
	gw := fitGW(func(string, string, map[string]string, *string) (HTTPReply, error) {
		return HTTPReply{404, "nope"}, nil
	})
	res := fitRunSuite(t, NewConformance(gw), "", "s3")[0]
	if res.Status != "missing" || res.Steps[0].Status != "fail" {
		t.Fatalf("%+v", res)
	}
	for _, s := range res.Steps[1:] {
		if s.Status != "skipped" {
			t.Fatalf("%+v", res.Steps)
		}
	}
}

func TestConformanceAnUnreachableEmulatorIsOfflineAndNothingIsAttemptedAfterwards(t *testing.T) {
	calls := 0
	gw := fitGW(func(string, string, map[string]string, *string) (HTTPReply, error) {
		calls++
		return HTTPReply{}, EmulatorUnreachable{"down"}
	})
	res := fitRunSuite(t, NewConformance(gw), "", "s3")[0]
	if res.Status != "offline" {
		t.Fatalf("status %s", res.Status)
	}
	if calls != 1 {
		t.Fatalf("%d calls: no cleanup traffic towards a dead emulator", calls)
	}
}

func TestConformanceCloudAndSuiteFiltersNarrowTheRun(t *testing.T) {
	c := NewConformance(fitGW(func(string, string, map[string]string, *string) (HTTPReply, error) { return HTTPReply{404, ""}, nil }))
	aws := fitRunSuite(t, c, "aws", "")
	if len(aws) == 0 {
		t.Fatal("no aws suites")
	}
	for _, s := range aws {
		if s.Cloud != "aws" {
			t.Fatalf("%+v", s)
		}
	}
	gcp := fitRunSuite(t, c, "gcp", "pubsub")
	if len(gcp) != 1 || gcp[0].ID != "pubsub" {
		t.Fatalf("%+v", gcp)
	}
	if got := fitRunSuite(t, c, "aws", "pubsub"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestConformanceTheCatalogCoversThreeCloudsWithUniqueSuiteIds(t *testing.T) {
	catalog := NewConformance(fitGW(func(string, string, map[string]string, *string) (HTTPReply, error) { return HTTPReply{200, ""}, nil })).Catalog()
	clouds, ids := map[string]bool{}, map[string]bool{}
	for _, s := range catalog {
		clouds[s.Cloud], ids[s.ID] = true, true
	}
	if len(clouds) != 3 || !clouds["aws"] || !clouds["azure"] || !clouds["gcp"] {
		t.Fatalf("clouds %v", clouds)
	}
	if len(ids) != len(catalog) {
		t.Fatal("suite ids are not unique")
	}
	if len(catalog) < 15 {
		t.Fatalf("only %d suites", len(catalog))
	}
}

func TestConformanceResourcesAreNamedPerRunSoTwoRunsNeverCollide(t *testing.T) {
	a, b := newFitFakeS3(), newFitFakeS3()
	fitRunSuite(t, NewConformance(a), "", "s3")
	fitRunSuite(t, NewConformance(b), "", "s3")
	if !strings.Contains(a.firstPut(), "nimbo-fit-") {
		t.Fatalf("%q", a.firstPut())
	}
	if a.firstPut() == b.firstPut() {
		t.Fatal("two runs used the same names")
	}
}

func TestConformanceTheLambdaSuiteUploadsARealZipWithTheHandler(t *testing.T) {
	var zipB64 string
	gw := fitGW(func(_ string, u string, _ map[string]string, body *string) (HTTPReply, error) {
		if strings.HasSuffix(u, "/2015-03-31/functions") && body != nil && strings.Contains(*body, "ZipFile") {
			var v struct{ Code struct{ ZipFile string } }
			if err := json.Unmarshal([]byte(*body), &v); err != nil {
				t.Fatal(err)
			}
			zipB64 = v.Code.ZipFile
		}
		return HTTPReply{200, `{"ok":true}`}, nil
	})
	res := fitRunSuite(t, NewConformance(gw), "", "lambda")[0]
	if res.Status != "full" {
		t.Fatalf("%+v", res)
	}
	raw, err := base64.StdEncoding.DecodeString(zipB64)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		entries[f.Name] = string(b)
	}
	if !strings.Contains(entries["index.js"], "exports.handler") {
		t.Fatalf("%v", entries)
	}
}

func TestConformanceEndpointsFollowTheConfiguredHost(t *testing.T) {
	var seen []string
	gw := fitGW(func(_ string, u string, _ map[string]string, _ *string) (HTTPReply, error) {
		seen = append(seen, u)
		return HTTPReply{404, ""}, nil
	})
	c := NewConformanceAt(gw, "http://emu:4566", "http://emu:4577", "http://emu:4588")
	fitRunSuite(t, c, "", "")
	for _, u := range seen {
		if !strings.HasPrefix(u, "http://emu:") {
			t.Fatalf("request to %s", u)
		}
	}
}

// fitRichGateway answers like a generous emulator for every other suite, so each one reaches its last step.
// It only checks that each suite can run to the end against well-formed answers without panicking.
func TestConformanceEverySuiteSurvivesGarbageAnswers(t *testing.T) {
	for _, body := range []string{"", "not json", "[]", "{}", "<R></R>"} {
		body := body
		gw := fitGW(func(string, string, map[string]string, *string) (HTTPReply, error) { return HTTPReply{200, body}, nil })
		rep := NewConformance(gw).Run(context.Background(), "", "")
		if len(rep.Suites) != 19 {
			t.Fatalf("%d suites for %q", len(rep.Suites), body)
		}
		total := 0
		for _, n := range rep.Summary {
			total += n
		}
		if total != 19 {
			t.Fatalf("summary %v", rep.Summary)
		}
	}
}

func TestConformanceSendsFormEncodedQueryCallsInOrder(t *testing.T) {
	var form string
	gw := fitGW(func(_ string, u string, _ map[string]string, body *string) (HTTPReply, error) {
		if body != nil && strings.Contains(*body, "Action=CreateQueue") {
			form = *body
		}
		return HTTPReply{404, ""}, nil
	})
	fitRunSuite(t, NewConformance(gw), "", "sqs")
	vals, err := url.ParseQuery(form)
	if err != nil || vals.Get("Action") != "CreateQueue" || vals.Get("Version") != "2012-11-05" || !strings.HasPrefix(vals.Get("QueueName"), "nimbo-fit-") {
		t.Fatalf("%q %v", form, err)
	}
	if !strings.HasPrefix(form, "Action=CreateQueue&") {
		t.Fatalf("Action must come first: %q", form)
	}
}
