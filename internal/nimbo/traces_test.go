package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const obsTraceID = "5b8efff798038103d269b633813fc60c"

func obsSpan(id, op string, start, dur int64, parent string, isErr bool, proc string) string {
	if proc == "" {
		proc = "p1"
	}
	refs := ""
	if parent != "" {
		refs = fmt.Sprintf(`{"refType":"CHILD_OF","traceID":"%s","spanID":"%s"}`, obsTraceID, parent)
	}
	errTag := ""
	status := "OK"
	if isErr {
		errTag = `,{"key":"error","type":"bool","value":true}`
		status = "ERROR"
	}
	return fmt.Sprintf(`{"traceID":"%s","spanID":"%s","operationName":"%s","startTime":%d,"duration":%d,"processID":"%s","references":[%s],"tags":[{"key":"otel.status_code","type":"string","value":"%s"}%s]}`,
		obsTraceID, id, op, start, dur, proc, refs, status, errTag)
}

func obsTrace(spans ...string) string {
	return fmt.Sprintf(`{"traceID":"%s","spans":[%s],"processes":{"p1":{"serviceName":"checkout","tags":[]},"p2":{"serviceName":"billing","tags":[]}}}`, obsTraceID, strings.Join(spans, ","))
}

func obsEnvelope(traces ...string) string {
	return fmt.Sprintf(`{"data":[%s],"total":0,"limit":0,"offset":0,"errors":null}`, strings.Join(traces, ","))
}

// obsFakeJaeger answers through a function and records every call.
type obsFakeJaeger struct {
	responses   func(method, url string) HTTPReply
	calls       []obsSent
	unreachable bool
}

func obsJaeger(f func(method, url string) HTTPReply) *obsFakeJaeger {
	return &obsFakeJaeger{responses: f}
}

func obsJaegerBody(status int, body string) *obsFakeJaeger {
	return obsJaeger(func(string, string) HTTPReply { return HTTPReply{status, body} })
}

func (f *obsFakeJaeger) Send(_ context.Context, method, url string, headers map[string]string, body *string) (HTTPReply, error) {
	if f.unreachable {
		return HTTPReply{}, EmulatorUnreachable{"down"}
	}
	f.calls = append(f.calls, obsSent{method, url, headers, body})
	if f.responses == nil {
		return HTTPReply{404, ""}, nil
	}
	return f.responses(method, url), nil
}

func obsTraces(gw HTTPGateway) *Traces { return NewTraces(gw, "", "") }

var obsRootSpan = obsSpan("aaaa000000000001", "POST /checkout", 1_000_000, 120_000, "", false, "")

func TestTracesServicesAreListedSorted(t *testing.T) {
	gw := obsJaegerBody(200, `{"data":["zeta","checkout","jaeger-all-in-one"],"total":3}`)
	got := obsMust[[]string](t)(obsTraces(gw).Services(context.Background()))
	if !slices.Equal(got, []string{"checkout", "jaeger-all-in-one", "zeta"}) {
		t.Fatalf("got %v", got)
	}
	if gw.calls[0].URL != "http://localhost:16686/api/services" {
		t.Fatalf("url: %s", gw.calls[0].URL)
	}
}

func TestTracesAnEmptyJaegerAnswersWithNoServicesInsteadOfFailing(t *testing.T) {
	got := obsMust[[]string](t)(obsTraces(obsJaegerBody(200, `{"data":null,"total":0}`)).Services(context.Background()))
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestTracesSummaryFindsTheRootCountsErrorsAndMeasuresTheWholeTrace(t *testing.T) {
	body := obsEnvelope(obsTrace(
		obsSpan("aaaa000000000002", "SQS SendMessage", 1_010_000, 50_000, "aaaa000000000001", false, "p2"),
		obsSpan("aaaa000000000003", "DynamoDB PutItem", 1_065_000, 45_000, "aaaa000000000001", true, ""),
		obsRootSpan,
	))
	gw := obsJaegerBody(200, body)
	list := obsMust[[]TraceSummaryDto](t)(obsTraces(gw).List(context.Background(), "checkout", 20))
	if len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	s := list[0]
	if s.Service != "checkout" || s.Operation != "POST /checkout" || s.Spans != 3 || s.Errors != 1 || s.DurationMicros != 120_000 || s.StartedAt != 1_000 {
		t.Fatalf("summary: %+v", s)
	}
	url := gw.calls[0].URL
	if !strings.Contains(url, "service=checkout") || !strings.Contains(url, "limit=20") || !strings.Contains(url, "lookback=24h") {
		t.Fatalf("url: %s", url)
	}
}

func TestTracesComeNewestFirstAndTheLimitIsClamped(t *testing.T) {
	mk := func(id string, start int64) string {
		tid := strings.Repeat("ab", 8) + (id + strings.Repeat("0", 16))[:16]
		return strings.ReplaceAll(obsTrace(obsSpan(id, "op", start, 10, "", false, "")), obsTraceID, tid)
	}
	gw := obsJaegerBody(200, obsEnvelope(mk("0001", 1_000), mk("0002", 9_000), mk("0003", 5_000)))
	list := obsMust[[]TraceSummaryDto](t)(obsTraces(gw).List(context.Background(), "svc", 100_000))
	got := []int64{}
	for _, s := range list {
		got = append(got, s.StartedAt)
	}
	if !slices.Equal(got, []int64{9, 5, 1}) {
		t.Fatalf("order: %v", got)
	}
	if !strings.Contains(gw.calls[0].URL, "limit=100&") {
		t.Fatalf("url: %s", gw.calls[0].URL)
	}
	_, _ = obsTraces(gw).List(context.Background(), "svc", 0)
	if !strings.Contains(gw.calls[1].URL, "limit=1&") {
		t.Fatalf("url: %s", gw.calls[1].URL)
	}
}

func TestTracesDetailOrdersSpansDepthFirstAndComputesOffsets(t *testing.T) {
	body := obsEnvelope(obsTrace(
		obsSpan("aaaa000000000004", "grandchild", 1_020_000, 5_000, "aaaa000000000002", false, ""),
		obsSpan("aaaa000000000003", "second child", 1_070_000, 30_000, "aaaa000000000001", true, ""),
		obsSpan("aaaa000000000002", "first child", 1_010_000, 50_000, "aaaa000000000001", false, "p2"),
		obsRootSpan,
	))
	d := obsMust[TraceDetailDto](t)(obsTraces(obsJaegerBody(200, body)).Detail(context.Background(), obsTraceID))
	var ops []string
	var depths []int
	var offsets []int64
	var errs []bool
	for _, s := range d.Spans {
		ops, depths, offsets, errs = append(ops, s.Operation), append(depths, s.Depth), append(offsets, s.OffsetMicros), append(errs, s.Error)
	}
	if !slices.Equal(ops, []string{"POST /checkout", "first child", "grandchild", "second child"}) {
		t.Fatalf("ops: %v", ops)
	}
	if !slices.Equal(depths, []int{0, 1, 2, 1}) || !slices.Equal(offsets, []int64{0, 10_000, 20_000, 70_000}) || !slices.Equal(errs, []bool{false, false, false, true}) {
		t.Fatalf("depths %v offsets %v errors %v", depths, offsets, errs)
	}
	if d.Spans[1].Service != "billing" || d.DurationMicros != 120_000 || d.Spans[0].ParentID != nil || d.TraceID != obsTraceID {
		t.Fatalf("detail: %+v", d)
	}
}

func TestTracesOrphansAndCyclesDoNotHangOrDropSpans(t *testing.T) {
	orphan := obsSpan("bbbb000000000001", "orphan", 1_005_000, 5_000, "ffffffffffffffff", false, "")
	a := obsSpan("cccc000000000001", "a", 1_001_000, 1_000, "cccc000000000002", false, "")
	b := obsSpan("cccc000000000002", "b", 1_002_000, 1_000, "cccc000000000001", false, "")
	d := obsMust[TraceDetailDto](t)(obsTraces(obsJaegerBody(200, obsEnvelope(obsTrace(obsRootSpan, orphan, a, b)))).Detail(context.Background(), obsTraceID))
	if len(d.Spans) != 4 {
		t.Fatalf("spans: %+v", d.Spans)
	}
	ids := map[string]bool{}
	for _, s := range d.Spans {
		ids[s.ID] = true
		if s.Operation == "orphan" && (s.Depth != 0 || s.ParentID != nil) {
			t.Errorf("orphan: %+v (a parent we do not have is not shown)", s)
		}
	}
	if len(ids) != 4 {
		t.Fatalf("duplicate spans: %+v", d.Spans)
	}
}

func TestTracesUnknownTraceIsA404StyleError(t *testing.T) {
	_, err := obsTraces(obsJaegerBody(200, `{"data":[]}`)).Detail(context.Background(), obsTraceID)
	var ee EmulatorError
	if !errors.As(err, &ee) || ee.Status != 404 {
		t.Fatalf("got %v", err)
	}
}

func TestTracesInvalidInputNeverReachesJaeger(t *testing.T) {
	gw := obsJaegerBody(200, "{}")
	tr := obsTraces(gw)
	ctx := context.Background()
	for _, bad := range []string{"", "a b", "x/y", "../z", "a&b=c", strings.Repeat("s", 200)} {
		var in InvalidName
		if _, err := tr.List(ctx, bad, 20); !errors.As(err, &in) {
			t.Errorf("service %q: %v", bad, err)
		}
	}
	for _, bad := range []string{"", "xyz", "../1", "ABCDEF0123456789", strings.Repeat("1", 40), "abc"} {
		var in InvalidName
		if _, err := tr.Detail(ctx, bad); !errors.As(err, &in) {
			t.Errorf("trace id %q: %v", bad, err)
		}
	}
	if len(gw.calls) != 0 {
		t.Fatalf("calls: %+v", gw.calls)
	}
}

func TestTracesServiceNamesAreURLEncoded(t *testing.T) {
	gw := obsJaegerBody(200, obsEnvelope())
	obsMust[[]TraceSummaryDto](t)(obsTraces(gw).List(context.Background(), "my.service-1_a", 20))
	if !strings.Contains(gw.calls[0].URL, "service=my.service-1_a") {
		t.Fatalf("url: %s", gw.calls[0].URL)
	}
}

func TestTracesAnUnreachableJaegerAndAFailingOneAreDifferentErrors(t *testing.T) {
	down := &obsFakeJaeger{unreachable: true}
	var eu EmulatorUnreachable
	if _, err := obsTraces(down).Services(context.Background()); !errors.As(err, &eu) {
		t.Fatalf("down: %v", err)
	}
	var ee EmulatorError
	if _, err := obsTraces(obsJaegerBody(500, "x")).Services(context.Background()); !errors.As(err, &ee) {
		t.Fatalf("500: %v", err)
	}
}

func obsSampleSpans(t *testing.T, body string) []map[string]any {
	t.Helper()
	var doc struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the sample is not JSON: %v\n%s", err, body)
	}
	return doc.ResourceSpans[0].ScopeSpans[0].Spans
}

func TestTracesTheSampleTraceIsValidOTLPWithAParentChildStructure(t *testing.T) {
	gw := obsJaegerBody(200, "{}")
	sample := obsMust[SampleTraceDto](t)(obsTraces(gw).Sample(context.Background(), false))
	if sample.Service != "nimbo-demo" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(sample.TraceID) {
		t.Fatalf("sample: %+v", sample)
	}
	call := gw.calls[0]
	if call.Method != "POST" || call.URL != "http://localhost:4318/v1/traces" || call.Headers["Content-Type"] != "application/json" {
		t.Fatalf("call: %+v", call)
	}
	spans := obsSampleSpans(t, call.body())
	if len(spans) != 3 {
		t.Fatalf("spans: %d", len(spans))
	}
	rootID, roots := "", 0
	for _, s := range spans {
		if s["traceId"] != sample.TraceID || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(s["spanId"].(string)) {
			t.Errorf("span ids: %v", s)
		}
		if s["status"].(map[string]any)["code"].(float64) != 1 {
			t.Errorf("status: %v", s["status"])
		}
		start, _ := strconv.ParseInt(s["startTimeUnixNano"].(string), 10, 64)
		end, _ := strconv.ParseInt(s["endTimeUnixNano"].(string), 10, 64)
		if end <= start {
			t.Errorf("end before start: %v", s)
		}
		if _, has := s["parentSpanId"]; !has {
			roots++
			rootID = s["spanId"].(string)
		}
	}
	if roots != 1 {
		t.Fatalf("roots: %d", roots)
	}
	for _, s := range spans {
		if p, has := s["parentSpanId"]; has && p != rootID {
			t.Errorf("not a child of the root: %v", s)
		}
	}
}

func TestTracesTheFailingSampleMarksExactlyOneSpanAsAnErrorAndIDsDifferBetweenRuns(t *testing.T) {
	gw := obsJaegerBody(200, "{}")
	tr := obsTraces(gw)
	first := obsMust[SampleTraceDto](t)(tr.Sample(context.Background(), true))
	second := obsMust[SampleTraceDto](t)(tr.Sample(context.Background(), false))
	if first.TraceID == second.TraceID {
		t.Fatal("trace ids repeat")
	}
	failing := 0
	for _, s := range obsSampleSpans(t, gw.calls[0].body()) {
		if s["status"].(map[string]any)["code"].(float64) == 2 {
			failing++
		}
	}
	if failing != 1 {
		t.Fatalf("failing spans: %d", failing)
	}
}

func TestTracesARejectedOTLPIntakeIsReported(t *testing.T) {
	var ee EmulatorError
	if _, err := obsTraces(obsJaegerBody(400, "bad")).Sample(context.Background(), false); !errors.As(err, &ee) {
		t.Fatalf("got %v", err)
	}
}

func TestTracesUseTheConfiguredEndpoints(t *testing.T) {
	gw := obsJaegerBody(200, `{"data":[]}`)
	tr := NewTraces(gw, "http://h:16686", "http://h:4318")
	_, _ = tr.Services(context.Background())
	_, _ = tr.Sample(context.Background(), false)
	if gw.calls[0].URL != "http://h:16686/api/services" || gw.calls[1].URL != "http://h:4318/v1/traces" {
		t.Fatalf("calls: %+v", gw.calls)
	}
}

// ---- HTTP API ----

func obsTraceGateway() *obsFakeJaeger {
	body := obsEnvelope(obsTrace(
		obsSpan("aaaa000000000001", "GET /", 1_000_000, 100_000, "", false, ""),
		obsSpan("aaaa000000000002", "child", 1_010_000, 10_000, "aaaa000000000001", false, ""),
	))
	return obsJaeger(func(m, url string) HTTPReply {
		switch {
		case strings.HasSuffix(url, "/api/services"):
			return HTTPReply{200, `{"data":["checkout"]}`}
		case strings.Contains(url, "/api/traces?service"):
			return HTTPReply{200, body}
		case strings.HasSuffix(url, "/api/traces/"+obsTraceID):
			return HTTPReply{200, body}
		case m == "POST":
			return HTTPReply{200, "{}"}
		}
		return HTTPReply{404, ""}
	})
}

func TestTraceAPIListDetailAndSampleOverHTTP(t *testing.T) {
	reg := obsTraces(obsTraceGateway()).Register
	if rec := obsServe(reg, "GET", "/api/traces/services", ""); !strings.Contains(rec.Body.String(), "checkout") {
		t.Fatalf("services: %s", rec.Body)
	}
	if rec := obsServe(reg, "GET", "/api/traces?service=checkout", ""); !strings.Contains(rec.Body.String(), `"spans":2`) {
		t.Fatalf("list: %s", rec.Body)
	}
	if rec := obsServe(reg, "GET", "/api/traces/"+obsTraceID, ""); !strings.Contains(rec.Body.String(), `"depth":1`) {
		t.Fatalf("detail: %s", rec.Body)
	}
	sample := obsServe(reg, "POST", "/api/traces/sample?error=true", "")
	if sample.Code != 200 || !strings.Contains(sample.Body.String(), "nimbo-demo") {
		t.Fatalf("sample: %d %s", sample.Code, sample.Body)
	}
}

func TestTraceAPIValidationAndEmulatorErrorsMapToHTTPStatuses(t *testing.T) {
	down := &obsFakeJaeger{unreachable: true}
	reg := obsTraces(down).Register
	cases := []struct {
		path string
		want int
	}{
		{"/api/traces/services", http.StatusServiceUnavailable},
		{"/api/traces", http.StatusBadRequest},
		{"/api/traces?service=a%20b", http.StatusBadRequest},
		{"/api/traces/nothex", http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec := obsServe(reg, "GET", c.path, ""); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.path, rec.Code, c.want)
		}
	}
	if rec := obsServe(reg, "GET", "/api/traces", ""); !strings.Contains(rec.Body.String(), "service is required") {
		t.Errorf("message: %s", rec.Body)
	}
}

func TestTraceAPIAnUnknownTraceIsA404(t *testing.T) {
	reg := obsTraces(obsJaegerBody(200, `{"data":[]}`)).Register
	if rec := obsServe(reg, "GET", "/api/traces/"+obsTraceID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}
