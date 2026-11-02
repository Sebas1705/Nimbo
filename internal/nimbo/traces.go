package nimbo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type TraceSummaryDto struct {
	TraceID string `json:"traceId"`
	Service string `json:"service"`
	// Operation is the root span's name.
	Operation string `json:"operation"`
	// StartedAt is epoch milliseconds of the first span.
	StartedAt      int64 `json:"startedAt"`
	DurationMicros int64 `json:"durationMicros"`
	Spans          int   `json:"spans"`
	Errors         int   `json:"errors"`
}

type SpanDto struct {
	ID             string  `json:"id"`
	ParentID       *string `json:"parentId"`
	Service        string  `json:"service"`
	Operation      string  `json:"operation"`
	Depth          int     `json:"depth"`
	OffsetMicros   int64   `json:"offsetMicros"`
	DurationMicros int64   `json:"durationMicros"`
	Error          bool    `json:"error"`
}

type TraceDetailDto struct {
	TraceID        string    `json:"traceId"`
	StartedAt      int64     `json:"startedAt"`
	DurationMicros int64     `json:"durationMicros"`
	Spans          []SpanDto `json:"spans"`
}

type SampleTraceDto struct {
	TraceID string `json:"traceId"`
	Service string `json:"service"`
}

// TraceSampleService is the service name of the synthetic trace.
const TraceSampleService = "nimbo-demo"

var (
	traceService = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	traceIDRe    = regexp.MustCompile(`^[0-9a-f]{16,32}$`)
)

// Traces reads traces from Jaeger's query API and can push a synthetic one through its OTLP/HTTP intake.
type Traces struct {
	gw    HTTPGateway
	query string
	otlp  string
}

// NewTraces talks to Jaeger's query API at query and its OTLP intake at otlp; empty means the localhost defaults
// (16686 and 4318).
func NewTraces(gw HTTPGateway, query, otlp string) *Traces {
	return &Traces{gw: gw, query: orDefault(query, "http://localhost:16686"), otlp: orDefault(otlp, "http://localhost:4318")}
}

// Services lists the services Jaeger has seen, sorted.
func (t *Traces) Services(ctx context.Context) ([]string, error) {
	body, err := t.get(ctx, "/api/services")
	if err != nil {
		return nil, err
	}
	root, err := traceParse(body)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, s := range obsArr(root.Get("data")) {
		if name, ok := obsText(s); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// List returns the newest traces of a service (limit 1..100) from the last day.
func (t *Traces) List(ctx context.Context, service string, limit int) ([]TraceSummaryDto, error) {
	if !traceService.MatchString(service) {
		return nil, InvalidName{"Invalid service name"}
	}
	body, err := t.get(ctx, fmt.Sprintf("/api/traces?service=%s&limit=%d&lookback=24h", url.QueryEscape(service), obsClamp(limit, 1, 100)))
	if err != nil {
		return nil, err
	}
	root, err := traceParse(body)
	if err != nil {
		return nil, err
	}
	out := []TraceSummaryDto{}
	for _, tr := range obsArr(root.Get("data")) {
		if o := obsObj(tr); o != nil {
			out = append(out, traceSummary(o))
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].StartedAt > out[b].StartedAt })
	return out, nil
}

// Detail returns one trace with its spans in tree order and their offsets from the start (the waterfall).
func (t *Traces) Detail(ctx context.Context, traceID string) (TraceDetailDto, error) {
	if !traceIDRe.MatchString(traceID) {
		return TraceDetailDto{}, InvalidName{"Invalid trace id"}
	}
	body, err := t.get(ctx, "/api/traces/"+traceID)
	if err != nil {
		return TraceDetailDto{}, err
	}
	root, err := traceParse(body)
	if err != nil {
		return TraceDetailDto{}, err
	}
	var first *obsObject
	if data := obsArr(root.Get("data")); len(data) > 0 {
		first = obsObj(data[0])
	}
	if first == nil {
		return TraceDetailDto{}, EmulatorError{404, "Trace not found"}
	}
	return traceDetail(first), nil
}

// Sample sends a 3-span trace (an API call that writes to a queue and a table); optionally with a failing span.
func (t *Traces) Sample(ctx context.Context, failing bool) (SampleTraceDto, error) {
	traceID := traceHex(16)
	root := traceHex(8)
	now := time.Now().UnixMilli() * 1_000_000
	span := func(id, parent, name string, kind int, startMs, endMs int64, isErr bool) string {
		var b strings.Builder
		fmt.Fprintf(&b, `{"traceId":"%s","spanId":"%s",`, traceID, id)
		if parent != "" {
			fmt.Fprintf(&b, `"parentSpanId":"%s",`, parent)
		}
		fmt.Fprintf(&b, `"name":"%s","kind":%d,"startTimeUnixNano":"%d","endTimeUnixNano":"%d",`, name, kind, now+startMs*1_000_000, now+endMs*1_000_000)
		if isErr {
			b.WriteString(`"status":{"code":2,"message":"simulated failure"}}`)
		} else {
			b.WriteString(`"status":{"code":1}}`)
		}
		return b.String()
	}
	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"` + TraceSampleService + `"}}]},"scopeSpans":[{"scope":{"name":"nimbo"},"spans":[` +
		strings.Join([]string{
			span(root, "", "GET /orders", 2, 0, 120, false),
			span(traceHex(8), root, "sqs.SendMessage", 3, 10, 55, false),
			span(traceHex(8), root, "dynamodb.PutItem", 3, 60, 110, failing),
		}, ",") + "]}]}]}"
	reply, err := t.gw.Send(ctx, http.MethodPost, t.otlp+"/v1/traces", map[string]string{"Content-Type": "application/json"}, &body)
	if err != nil {
		return SampleTraceDto{}, err
	}
	if reply.Status < 200 || reply.Status > 299 {
		return SampleTraceDto{}, EmulatorError{reply.Status, fmt.Sprintf("OTLP intake answered %d: %s", reply.Status, obsTake(reply.Body, 120))}
	}
	return SampleTraceDto{TraceID: traceID, Service: TraceSampleService}, nil
}

// ---- parsing ----

type traceRaw struct {
	id       string
	parent   *string
	service  string
	op       string
	start    int64
	duration int64
	err      bool
}

func traceParse(body string) (*obsObject, error) {
	o, err := obsParseObject(body)
	if err != nil {
		return nil, EmulatorError{502, "Unreadable JSON from Jaeger"}
	}
	return o, nil
}

func traceSpans(trace *obsObject) []traceRaw {
	processes := obsObj(trace.Get("processes"))
	var out []traceRaw
	for _, item := range obsArr(trace.Get("spans")) {
		o := obsObj(item)
		if o == nil {
			continue
		}
		r := traceRaw{id: obsStrOr(o, "spanID", ""), service: "unknown", op: obsStrOr(o, "operationName", "")}
		for _, ref := range obsArr(o.Get("references")) {
			if ro := obsObj(ref); ro != nil && obsStrOr(ro, "refType", "") == "CHILD_OF" {
				if p, ok := obsText(ro.Get("spanID")); ok {
					r.parent = &p
				}
				break
			}
		}
		if pid, ok := obsText(o.Get("processID")); ok {
			if name, ok := obsText(obsObj(processes.Get(pid)).Get("serviceName")); ok {
				r.service = name
			}
		}
		r.start, _ = obsInt64(o.Get("startTime"))
		r.duration, _ = obsInt64(o.Get("duration"))
		for _, tag := range obsArr(o.Get("tags")) {
			if to := obsObj(tag); to != nil && obsStrOr(to, "key", "") == "error" && obsStrOr(to, "value", "") == "true" {
				r.err = true
			}
		}
		out = append(out, r)
	}
	return out
}

// traceBounds is the earliest start and the latest end of the spans.
func traceBounds(spans []traceRaw) (start, end int64) {
	for i, s := range spans {
		if i == 0 || s.start < start {
			start = s.start
		}
		if i == 0 || s.start+s.duration > end {
			end = s.start + s.duration
		}
	}
	return
}

func traceSummary(trace *obsObject) TraceSummaryDto {
	spans := traceSpans(trace)
	ids := map[string]bool{}
	for _, s := range spans {
		ids[s.id] = true
	}
	// The root is the earliest span whose parent is missing or not part of the trace.
	var root *traceRaw
	for i := range spans {
		s := &spans[i]
		if (s.parent == nil || !ids[*s.parent]) && (root == nil || s.start < root.start) {
			root = s
		}
	}
	if root == nil {
		for i := range spans {
			if root == nil || spans[i].start < root.start {
				root = &spans[i]
			}
		}
	}
	start, end := traceBounds(spans)
	dto := TraceSummaryDto{
		TraceID: obsStrOr(trace, "traceID", ""), Service: "unknown",
		StartedAt: start / 1000, DurationMicros: end - start, Spans: len(spans),
	}
	if root != nil {
		dto.Service, dto.Operation = root.service, root.op
	}
	for _, s := range spans {
		if s.err {
			dto.Errors++
		}
	}
	return dto
}

func traceDetail(trace *obsObject) TraceDetailDto {
	spans := traceSpans(trace)
	start, end := traceBounds(spans)
	ids := map[string]bool{}
	for _, s := range spans {
		ids[s.id] = true
	}
	// Spans grouped by the parent we actually have; the others are roots.
	children := map[string][]traceRaw{}
	var roots []traceRaw
	for _, s := range spans {
		if s.parent != nil && ids[*s.parent] {
			children[*s.parent] = append(children[*s.parent], s)
		} else {
			roots = append(roots, s)
		}
	}
	byStart := func(l []traceRaw) []traceRaw {
		l = append([]traceRaw{}, l...)
		sort.SliceStable(l, func(a, b int) bool { return l[a].start < l[b].start })
		return l
	}
	ordered := []SpanDto{}
	seen := map[string]bool{}
	var visit func(s traceRaw, depth int)
	visit = func(s traceRaw, depth int) {
		if seen[s.id] { // a malformed trace with a cycle must not loop forever
			return
		}
		seen[s.id] = true
		var parent *string
		if s.parent != nil && ids[*s.parent] {
			parent = s.parent
		}
		ordered = append(ordered, SpanDto{s.id, parent, s.service, s.op, depth, s.start - start, s.duration, s.err})
		for _, c := range byStart(children[s.id]) {
			visit(c, depth+1)
		}
	}
	for _, r := range byStart(roots) {
		visit(r, 0)
	}
	var left []traceRaw
	for _, s := range spans {
		if !seen[s.id] {
			left = append(left, s)
		}
	}
	for _, s := range byStart(left) {
		visit(s, 0)
	}
	return TraceDetailDto{TraceID: obsStrOr(trace, "traceID", ""), StartedAt: start / 1000, DurationMicros: end - start, Spans: ordered}
}

func (t *Traces) get(ctx context.Context, path string) (string, error) {
	reply, err := t.gw.Send(ctx, http.MethodGet, t.query+path, map[string]string{}, nil)
	if err != nil {
		return "", err
	}
	if reply.Status < 200 || reply.Status > 299 {
		return "", EmulatorError{reply.Status, fmt.Sprintf("Jaeger answered %d", reply.Status)}
	}
	return reply.Body, nil
}

func traceHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
