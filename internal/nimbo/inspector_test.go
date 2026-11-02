package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// obsSent is one request a fake gateway saw.
type obsSent struct {
	Method, URL string
	Headers     map[string]string
	Body        *string
}

func (s obsSent) body() string {
	if s.Body == nil {
		return ""
	}
	return *s.Body
}

type obsRule struct {
	match func(obsSent) bool
	reply HTTPReply
}

// obsFakeGateway replies by matching on method + URL + body, and records every call. Nothing touches the network.
type obsFakeGateway struct {
	rules       []obsRule
	sent        []obsSent
	unreachable bool
}

func (g *obsFakeGateway) Send(_ context.Context, method, url string, headers map[string]string, body *string) (HTTPReply, error) {
	call := obsSent{method, url, headers, body}
	g.sent = append(g.sent, call)
	if g.unreachable {
		return HTTPReply{}, EmulatorUnreachable{"down"}
	}
	for _, r := range g.rules {
		if r.match(call) {
			return r.reply, nil
		}
	}
	return HTTPReply{404, "no rule"}, nil
}

func obsOn(match func(obsSent) bool, status int, body string) obsRule {
	return obsRule{match, HTTPReply{status, body}}
}

func obsAws(rules ...obsRule) *obsFakeGateway { return &obsFakeGateway{rules: rules} }

func obsAlways(body string) obsRule { return obsOn(func(obsSent) bool { return true }, 200, body) }

const (
	obsS3Buckets = `<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>f7b1</ID><DisplayName>floci</DisplayName></Owner><Buckets><Bucket><Name>probe-bkt</Name><CreationDate>2026-11-01T18:12:45Z</CreationDate></Bucket><Bucket><Name>second</Name><CreationDate>2026-11-02T10:00:00Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>`

	obsS3Objects = `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>probe-bkt</Name><Prefix></Prefix><MaxKeys>1000</MaxKeys><KeyCount>1</KeyCount><IsTruncated>true</IsTruncated><Contents><Key>dir/hello world.txt</Key><LastModified>2026-11-01T18:12:46Z</LastModified><ETag>&quot;fd33&quot;</ETag><Size>8</Size><StorageClass>STANDARD</StorageClass></Contents></ListBucketResult>`

	obsSQSList = `<ListQueuesResponse><ListQueuesResult><QueueUrl>http://localhost:4566/000000000000/probe-q</QueueUrl></ListQueuesResult></ListQueuesResponse>`

	obsSQSAttrs = `<GetQueueAttributesResponse><GetQueueAttributesResult><Attribute><Name>VisibilityTimeout</Name><Value>30</Value></Attribute><Attribute><Name>ApproximateNumberOfMessages</Name><Value>3</Value></Attribute><Attribute><Name>ApproximateNumberOfMessagesNotVisible</Name><Value>1</Value></Attribute><Attribute><Name>ApproximateNumberOfMessagesDelayed</Name><Value>2</Value></Attribute></GetQueueAttributesResult></GetQueueAttributesResponse>`

	obsSQSMessages = `<ReceiveMessageResponse><ReceiveMessageResult><Message><MessageId>m-1</MessageId><ReceiptHandle>abc</ReceiptHandle><Body>hello &amp; goodbye</Body><Attribute><Name>SentTimestamp</Name><Value>1793556777922</Value></Attribute><Attribute><Name>ApproximateReceiveCount</Name><Value>2</Value></Attribute></Message><Message><MessageId>m-2</MessageId><Body>second</Body></Message></ReceiveMessageResult></ReceiveMessageResponse>`
)

func obsBodyHas(s string) func(obsSent) bool {
	return func(c obsSent) bool { return strings.Contains(c.body(), s) }
}

func obsTarget(suffix string) func(obsSent) bool {
	return func(c obsSent) bool { return strings.HasSuffix(c.Headers["X-Amz-Target"], suffix) }
}

func obsBuckets(t *testing.T, gw HTTPGateway) []BucketDto {
	t.Helper()
	b, err := NewInspector(gw, "", "").Buckets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- S3 ----

func TestInspectorListsBucketsWithCreationDates(t *testing.T) {
	gw := obsAws(obsOn(func(c obsSent) bool { return c.Method == "GET" && c.URL == "http://localhost:4566/" }, 200, obsS3Buckets))
	buckets := obsBuckets(t, gw)
	if len(buckets) != 2 || buckets[0].Name != "probe-bkt" || buckets[1].Name != "second" {
		t.Fatalf("buckets: %+v", buckets)
	}
	if buckets[0].Created == nil || *buckets[0].Created != "2026-11-01T18:12:45Z" {
		t.Fatalf("created: %v", buckets[0].Created)
	}
	if len(gw.sent) != 1 || !strings.Contains(gw.sent[0].Headers["Authorization"], "/s3/aws4_request") {
		t.Fatalf("sent: %+v", gw.sent)
	}
}

func TestInspectorListsObjectsEncodesThePrefixAndReportsTruncation(t *testing.T) {
	gw := obsAws(obsOn(func(c obsSent) bool { return strings.Contains(c.URL, "/probe-bkt?") }, 200, obsS3Objects))
	result, err := NewInspector(gw, "", "").Objects(context.Background(), "probe-bkt", "dir/a b&c", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) != 1 || result.Objects[0].Key != "dir/hello world.txt" || result.Objects[0].Size != 8 {
		t.Fatalf("objects: %+v", result.Objects)
	}
	if !result.Truncated {
		t.Fatal("expected truncated")
	}
	url := gw.sent[0].URL
	for _, want := range []string{"prefix=dir%2Fa%20b%26c", "list-type=2", "max-keys=200"} {
		if !strings.Contains(url, want) {
			t.Fatalf("%s not in %s", want, url)
		}
	}
}

func TestInspectorObjectsClampsMaxKeys(t *testing.T) {
	gw := obsAws(obsAlways(`<R/>`))
	in := NewInspector(gw, "", "")
	_, _ = in.Objects(context.Background(), "b", "", 99999)
	_, _ = in.Objects(context.Background(), "b", "", 0)
	if !strings.Contains(gw.sent[0].URL, "max-keys=1000") || !strings.Contains(gw.sent[1].URL, "max-keys=1") {
		t.Fatalf("urls: %s %s", gw.sent[0].URL, gw.sent[1].URL)
	}
}

func TestInspectorPreviewAsksForAByteRangeAndEncodesEachKeySegment(t *testing.T) {
	gw := obsAws(obsAlways("hello"))
	preview, err := NewInspector(gw, "", "").Preview(context.Background(), "probe-bkt", "dir/hello world.txt", 100)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Text != "hello" || preview.Truncated {
		t.Fatalf("preview: %+v", preview)
	}
	call := gw.sent[0]
	if call.URL != "http://localhost:4566/probe-bkt/dir/hello%20world.txt" {
		t.Fatalf("url: %s", call.URL)
	}
	if call.Headers["Range"] != "bytes=0-99" {
		t.Fatalf("range: %s", call.Headers["Range"])
	}
}

func TestInspectorPreviewFlagsTruncatedTextWhenTheLimitIsReached(t *testing.T) {
	gw := obsAws(obsAlways(strings.Repeat("x", 10)))
	p, err := NewInspector(gw, "", "").Preview(context.Background(), "b", "k", 10)
	if err != nil || !p.Truncated {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestInspectorPreviewNeverReturnsMoreThanTheLimit(t *testing.T) {
	gw := obsAws(obsAlways(strings.Repeat("é", 50)))
	p, _ := NewInspector(gw, "", "").Preview(context.Background(), "b", "k", 10)
	if len([]rune(p.Text)) != 10 || !p.Truncated {
		t.Fatalf("%q %v", p.Text, p.Truncated)
	}
}

func TestInspectorHostileNamesNeverReachTheEmulator(t *testing.T) {
	gw := obsAws(obsAlways("<x/>"))
	in := NewInspector(gw, "", "")
	ctx := context.Background()
	isInvalid := func(err error) bool { var e InvalidName; return errors.As(err, &e) }
	for _, bad := range []string{"../etc", "a/b", "a b", "", strings.Repeat("x", 300), "a?b=c", "a%2fb", ".", ".."} {
		if _, err := in.Objects(ctx, bad, "", 200); !isInvalid(err) {
			t.Errorf("bucket %q: %v", bad, err)
		}
	}
	for _, badKey := range []string{"../secret", "a/../b", "./x", ""} {
		if _, err := in.Preview(ctx, "b", badKey, 4096); !isInvalid(err) {
			t.Errorf("key %q: %v", badKey, err)
		}
	}
	if _, err := in.Messages(ctx, "a/b", ""); !isInvalid(err) {
		t.Errorf("queue: %v", err)
	}
	if _, err := in.Items(ctx, "a b", 25); !isInvalid(err) {
		t.Errorf("table: %v", err)
	}
	if _, err := in.Pubsub(ctx, "Bad Project"); !isInvalid(err) {
		t.Errorf("project: %v", err)
	}
	if len(gw.sent) != 0 {
		t.Fatalf("requests leaked: %+v", gw.sent)
	}
}

func TestInspectorXMLWithADoctypeIsRejectedInsteadOfExpanded(t *testing.T) {
	evil := `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><ListAllMyBucketsResult>&e;</ListAllMyBucketsResult>`
	_, err := NewInspector(obsAws(obsAlways(evil)), "", "").Buckets(context.Background())
	var ee EmulatorError
	if !errors.As(err, &ee) || ee.Status != 502 {
		t.Fatalf("got %v", err)
	}
}

func TestInspectorGarbageXMLIsABadGateway(t *testing.T) {
	for _, body := range []string{"", "not xml", "<a><b></a>", "<a>", "text<a/>", "<a/><b/>"} {
		_, err := NewInspector(obsAws(obsAlways(body)), "", "").Buckets(context.Background())
		var ee EmulatorError
		if !errors.As(err, &ee) || ee.Status != 502 {
			t.Errorf("%q: got %v", body, err)
		}
	}
}

func TestInspectorEmulatorErrorsAndUnreachableEmulatorAreDistinct(t *testing.T) {
	_, err := NewInspector(obsAws(obsOn(func(obsSent) bool { return true }, 500, "boom")), "", "").Buckets(context.Background())
	var ee EmulatorError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v", err)
	}
	down := &obsFakeGateway{unreachable: true}
	_, err = NewInspector(down, "", "").Buckets(context.Background())
	var eu EmulatorUnreachable
	if !errors.As(err, &eu) {
		t.Fatalf("got %v", err)
	}
}

// ---- SQS ----

func obsSQSGateway() *obsFakeGateway {
	return obsAws(
		obsOn(obsBodyHas("Action=ListQueues"), 200, obsSQSList),
		obsOn(obsBodyHas("Action=GetQueueAttributes"), 200, obsSQSAttrs),
		obsOn(obsBodyHas("Action=ReceiveMessage"), 200, obsSQSMessages),
	)
}

func TestInspectorQueuesCarryMessageCounters(t *testing.T) {
	qs, err := NewInspector(obsSQSGateway(), "", "").Queues(context.Background())
	if err != nil || len(qs) != 1 {
		t.Fatalf("%+v %v", qs, err)
	}
	q := qs[0]
	if q.Name != "probe-q" || q.Messages == nil || *q.Messages != 3 || *q.InFlight != 1 || *q.Delayed != 2 {
		t.Fatalf("queue: %+v", q)
	}
	if q.URL != "http://localhost:4566/000000000000/probe-q" {
		t.Fatalf("url: %s", q.URL)
	}
}

func TestInspectorQueueURLsFromTheEmulatorNeverRedirectOurRequestsToAnotherHost(t *testing.T) {
	evil := obsAws(
		obsOn(obsBodyHas("Action=ListQueues"), 200, "<R><QueueUrl>http://evil.example:9999/000000000000/q1</QueueUrl></R>"),
		obsAlways(obsSQSAttrs),
	)
	qs, err := NewInspector(evil, "", "").Queues(context.Background())
	if err != nil || len(qs) != 1 {
		t.Fatalf("%+v %v", qs, err)
	}
	if qs[0].URL != "http://localhost:4566/000000000000/q1" {
		t.Fatalf("url: %s", qs[0].URL)
	}
	for _, s := range evil.sent {
		if !strings.HasPrefix(s.URL, "http://localhost:4566/") {
			t.Fatalf("request to %s", s.URL)
		}
	}
}

func TestInspectorAMalformedQueueURLIsABadGatewayNotACrash(t *testing.T) {
	bad := obsAws(obsAlways("<R><QueueUrl>http://localhost:4566/a/b/c</QueueUrl></R>"))
	_, err := NewInspector(bad, "", "").Queues(context.Background())
	var ee EmulatorError
	if !errors.As(err, &ee) || ee.Status != 502 {
		t.Fatalf("got %v", err)
	}
}

func TestInspectorAQueueWhoseAttributesFailStillShowsUp(t *testing.T) {
	partial := obsAws(
		obsOn(obsBodyHas("Action=ListQueues"), 200, obsSQSList),
		obsOn(func(obsSent) bool { return true }, 500, "nope"),
	)
	qs, err := NewInspector(partial, "", "").Queues(context.Background())
	if err != nil || len(qs) != 1 {
		t.Fatalf("%+v %v", qs, err)
	}
	if qs[0].Name != "probe-q" || qs[0].Messages != nil {
		t.Fatalf("queue: %+v", qs[0])
	}
}

func TestInspectorPeekUsesVisibilityTimeoutZeroAndDecodesBodies(t *testing.T) {
	gw := obsSQSGateway()
	result, err := NewInspector(gw, "", "").Messages(context.Background(), "probe-q", "")
	if err != nil || len(result.Messages) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	m := result.Messages
	if m[0].Body != "hello & goodbye" || m[1].Body != "second" {
		t.Fatalf("bodies: %q %q", m[0].Body, m[1].Body)
	}
	if m[0].ReceiveCount == nil || *m[0].ReceiveCount != 2 || m[0].SentAt == nil || *m[0].SentAt != 1793556777922 {
		t.Fatalf("first: %+v", m[0])
	}
	if m[1].ReceiveCount != nil {
		t.Fatalf("second: %+v", m[1])
	}
	call := gw.sent[len(gw.sent)-1]
	if !strings.Contains(call.body(), "VisibilityTimeout=0") || call.Method != "POST" || !strings.Contains(call.URL, "/000000000000/probe-q") {
		t.Fatalf("call: %+v", call)
	}
	if call.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("content type: %s", call.Headers["Content-Type"])
	}
}

// ---- DynamoDB ----

const obsDescribe = `{"Table":{"TableName":"probe-t","TableStatus":"ACTIVE","ItemCount":7,"KeySchema":[{"AttributeName":"id","KeyType":"HASH"},{"AttributeName":"ts","KeyType":"RANGE"}]}}`

func TestInspectorTablesAreDescribedWithKeysAndItemCounts(t *testing.T) {
	gw := obsAws(
		obsOn(obsTarget("ListTables"), 200, `{"TableNames":["probe-t"]}`),
		obsOn(obsTarget("DescribeTable"), 200, obsDescribe),
	)
	tables, err := NewInspector(gw, "", "").Tables(context.Background())
	if err != nil || len(tables) != 1 {
		t.Fatalf("%+v %v", tables, err)
	}
	tb := tables[0]
	if tb.Name != "probe-t" || tb.Status == nil || *tb.Status != "ACTIVE" || tb.Items == nil || *tb.Items != 7 {
		t.Fatalf("table: %+v", tb)
	}
	if strings.Join(tb.Keys, ",") != "id,ts" {
		t.Fatalf("keys: %v", tb.Keys)
	}
	for _, s := range gw.sent {
		if s.Headers["Content-Type"] != "application/x-amz-json-1.0" {
			t.Fatalf("content type: %s", s.Headers["Content-Type"])
		}
	}
}

func TestInspectorATableThatCannotBeDescribedStillShowsUp(t *testing.T) {
	gw := obsAws(
		obsOn(obsTarget("ListTables"), 200, `{"TableNames":["probe-t"]}`),
		obsOn(obsTarget("DescribeTable"), 500, `{}`),
	)
	tables, err := NewInspector(gw, "", "").Tables(context.Background())
	if err != nil || len(tables) != 1 || tables[0].Status != nil || tables[0].Items != nil || len(tables[0].Keys) != 0 {
		t.Fatalf("%+v %v", tables, err)
	}
}

func TestInspectorScanResultIsFlattenedFromDynamoDBTypedValues(t *testing.T) {
	body := `{"Items":[{"id":{"S":"1"},"n":{"N":"42"},"f":{"N":"1.5"},"tags":{"L":[{"S":"a"},{"N":"2"}]},"ok":{"BOOL":true},"gone":{"NULL":true},"nested":{"M":{"x":{"S":"y"}}},"set":{"SS":["p","q"]},"nums":{"NS":["1","2"]}}],"Count":1}`
	gw := obsAws(obsAlways(body))
	items, err := NewInspector(gw, "", "").Items(context.Background(), "probe-t", 25)
	if err != nil || len(items.Items) != 1 || items.Count != 1 || items.Table != "probe-t" {
		t.Fatalf("%+v %v", items, err)
	}
	out, _ := json.Marshal(items.Items[0])
	// Keys keep the order the emulator sent them in.
	want := `{"id":"1","n":42,"f":1.5,"tags":["a",2],"ok":true,"gone":null,"nested":{"x":"y"},"set":["p","q"],"nums":[1,2]}`
	if string(out) != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
	if !strings.Contains(gw.sent[0].body(), `"Limit":25`) {
		t.Fatalf("body: %s", gw.sent[0].body())
	}
}

func TestInspectorLimitIsCapped(t *testing.T) {
	gw := obsAws(obsAlways(`{"Items":[]}`))
	items, err := NewInspector(gw, "", "").Items(context.Background(), "t", 100_000)
	if err != nil || !strings.Contains(gw.sent[0].body(), `"Limit":100`) {
		t.Fatalf("%v %v", err, gw.sent)
	}
	if out, _ := json.Marshal(items); !strings.Contains(string(out), `"items":[]`) {
		t.Fatalf("empty items must be [] not null: %s", out)
	}
}

func TestInspectorAFailingDynamoDBCallIsABadGateway(t *testing.T) {
	_, err := NewInspector(obsAws(obsOn(func(obsSent) bool { return true }, 400, "{}")), "", "").Items(context.Background(), "t", 25)
	var ee EmulatorError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v", err)
	}
	if status, _ := StatusFor(err); status != http.StatusBadGateway {
		t.Fatalf("status %d", status)
	}
}

func TestInspectorSimplifyDynamoLeavesPlainValuesAlone(t *testing.T) {
	v, _ := obsParse(`{"a":1,"b":{"S":"x","N":"1"},"c":{"X":"y"}}`)
	out, _ := json.Marshal(inspectSimplifyDynamo(v))
	if string(out) != `{"a":1,"b":{"S":"x","N":"1"},"c":{"X":"y"}}` {
		t.Fatalf("%s", out)
	}
}

// ---- Pub/Sub ----

func TestInspectorTopicsAndSubscriptionsAreListedWithShortNames(t *testing.T) {
	gw := obsAws(
		obsOn(func(c obsSent) bool { return strings.HasSuffix(c.URL, "/topics") }, 200, `{"topics":[{"name":"projects/p1/topics/orders"}]}`),
		obsOn(func(c obsSent) bool { return strings.HasSuffix(c.URL, "/subscriptions") }, 200, `{"subscriptions":[{"name":"projects/p1/subscriptions/billing","topic":"projects/p1/topics/orders"}]}`),
	)
	result, err := NewInspector(gw, "", "").Pubsub(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Topics) != 1 || result.Topics[0].Name != "orders" {
		t.Fatalf("topics: %+v", result.Topics)
	}
	if len(result.Subscriptions) != 1 || result.Subscriptions[0].Name != "billing" || *result.Subscriptions[0].Topic != "orders" {
		t.Fatalf("subs: %+v", result.Subscriptions)
	}
	if gw.sent[0].URL != "http://localhost:4588/v1/projects/p1/topics" {
		t.Fatalf("url: %s", gw.sent[0].URL)
	}
}

func TestInspectorAnEmptyProjectReturnsEmptyLists(t *testing.T) {
	result, err := NewInspector(obsAws(obsAlways("{}")), "", "").Pubsub(context.Background(), "empty-project")
	if err != nil || len(result.Topics) != 0 || len(result.Subscriptions) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if out, _ := json.Marshal(result); !strings.Contains(string(out), `"topics":[]`) || !strings.Contains(string(out), `"subscriptions":[]`) {
		t.Fatalf("%s", out)
	}
}

func TestInspectorUsesTheConfiguredEndpoints(t *testing.T) {
	gw := obsAws(obsAlways(`{}`))
	in := NewInspector(gw, "http://host.docker.internal:4566", "http://host.docker.internal:4588")
	_, _ = in.Pubsub(context.Background(), "p1")
	_, _ = in.Items(context.Background(), "t", 1)
	if gw.sent[0].URL != "http://host.docker.internal:4588/v1/projects/p1/topics" || gw.sent[2].URL != "http://host.docker.internal:4566/" {
		t.Fatalf("urls: %s %s", gw.sent[0].URL, gw.sent[2].URL)
	}
}

// ---- HTTP API ----

func obsServe(register func(*http.ServeMux), method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	register(mux)
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInspectAPIBucketsEndpointSerializes(t *testing.T) {
	gw := obsAws(obsOn(func(c obsSent) bool { return c.Method == "GET" && c.URL == "http://localhost:4566/" }, 200, obsS3Buckets))
	rec := obsServe(NewInspector(gw, "", "").Register, "GET", "/api/inspect/aws/s3/buckets", "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 || got[0]["name"] != "probe-bkt" || got[1]["name"] != "second" {
		t.Fatalf("%s %v", rec.Body, err)
	}
}

func TestInspectAPIStatusCodesMapTheFailureKinds(t *testing.T) {
	down := &obsFakeGateway{unreachable: true}
	reg := NewInspector(down, "", "").Register
	cases := []struct {
		path string
		want int
	}{
		{"/api/inspect/aws/s3/buckets", http.StatusServiceUnavailable},
		{"/api/inspect/aws/s3/buckets/a%20b/objects", http.StatusBadRequest},
		{"/api/inspect/gcp/pubsub?project=NO", http.StatusBadRequest},
		{"/api/inspect/gcp/pubsub?project=", http.StatusBadRequest},
		{"/api/inspect/aws/s3/buckets/b/object", http.StatusBadRequest}, // no key
	}
	for _, c := range cases {
		if rec := obsServe(reg, "GET", c.path, ""); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.path, rec.Code, c.want)
		}
	}
}

func TestInspectAPIPubsubDefaultsToTheFlociProject(t *testing.T) {
	gw := obsAws(obsAlways(`{}`))
	rec := obsServe(NewInspector(gw, "", "").Register, "GET", "/api/inspect/gcp/pubsub", "")
	if rec.Code != 200 || !strings.Contains(gw.sent[0].URL, "/projects/floci-local/") {
		t.Fatalf("%d %s", rec.Code, gw.sent[0].URL)
	}
}

func TestInspectAPIAMissingBucketIsAShort404WithoutTheRawXML(t *testing.T) {
	xml := `<?xml version="1.0"?><Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist.</Message></Error>`
	gw := obsAws(obsOn(func(obsSent) bool { return true }, 404, xml))
	rec := obsServe(NewInspector(gw, "", "").Register, "GET", "/api/inspect/aws/s3/buckets/nope/objects", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	text := rec.Body.String()
	if !strings.Contains(text, "NoSuchBucket") || strings.Contains(text, "<?xml") {
		t.Fatalf("body: %s", text)
	}
}

func TestInspectAPIEmulatorFailureIs502(t *testing.T) {
	gw := obsAws(obsOn(func(obsSent) bool { return true }, 500, "x"))
	if rec := obsServe(NewInspector(gw, "", "").Register, "GET", "/api/inspect/aws/sqs/queues", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestInspectAPIAllRoutesAnswer(t *testing.T) {
	gw := obsAws(
		obsOn(obsBodyHas("Action=ListQueues"), 200, obsSQSList),
		obsOn(obsBodyHas("Action=GetQueueAttributes"), 200, obsSQSAttrs),
		obsOn(obsBodyHas("Action=ReceiveMessage"), 200, obsSQSMessages),
		obsOn(obsTarget("ListTables"), 200, `{"TableNames":["t"]}`),
		obsOn(obsTarget("DescribeTable"), 200, obsDescribe),
		obsOn(obsTarget("Scan"), 200, `{"Items":[{"id":{"S":"1"}}]}`),
		obsOn(func(c obsSent) bool { return strings.Contains(c.URL, "list-type=2") }, 200, obsS3Objects),
		obsOn(func(c obsSent) bool { return strings.Contains(c.URL, "/b/k") }, 200, "data"),
	)
	reg := NewInspector(gw, "", "").Register
	for _, path := range []string{
		"/api/inspect/aws/s3/buckets/b/objects?prefix=x",
		"/api/inspect/aws/s3/buckets/b/object?key=k",
		"/api/inspect/aws/sqs/queues",
		"/api/inspect/aws/sqs/queues/probe-q/messages",
		"/api/inspect/aws/dynamodb/tables",
		"/api/inspect/aws/dynamodb/tables/t/items",
	} {
		if rec := obsServe(reg, "GET", path, ""); rec.Code != 200 {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}
