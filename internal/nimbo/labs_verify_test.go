package nimbo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// labWorld answers from fixed documents so each check type can be driven to pass or fail.
type labWorld struct {
	buckets       []string
	objectText    *string
	queues        []string
	messages      []string
	tables        map[string][]string
	items         []any
	topics        []string
	subscriptions map[string]string
	down          bool
}

func (w *labWorld) check() error {
	if w.down {
		return EmulatorUnreachable{"down"}
	}
	return nil
}

func (w *labWorld) BucketNames(context.Context) ([]string, error) { return w.buckets, w.check() }
func (w *labWorld) ObjectText(context.Context, string, string) (string, error) {
	if err := w.check(); err != nil {
		return "", err
	}
	if w.objectText == nil {
		return "", EmulatorError{404, "NoSuchKey"}
	}
	return *w.objectText, nil
}
func (w *labWorld) QueueNames(context.Context) ([]string, error) { return w.queues, w.check() }
func (w *labWorld) MessageBodies(context.Context, string) ([]string, error) {
	return w.messages, w.check()
}
func (w *labWorld) Tables(context.Context) ([]labTable, error) {
	var out []labTable
	for name, keys := range w.tables {
		out = append(out, labTable{name, keys})
	}
	return out, w.check()
}
func (w *labWorld) Items(_ context.Context, _ string, limit int) ([]any, error) {
	if limit != 100 {
		return nil, errors.New("the verifier reads 100 items")
	}
	return w.items, w.check()
}
func (w *labWorld) PubSub(context.Context, string) ([]string, []labSubscription, error) {
	var subs []labSubscription
	for name, topic := range w.subscriptions {
		topic := topic
		subs = append(subs, labSubscription{name, &topic})
	}
	return w.topics, subs, w.check()
}

type labToxiFake struct {
	view labChaosView
	err  error
}

func (c *labToxiFake) View(context.Context) (labChaosView, error) { return c.view, c.err }

type labKubeFake struct {
	o   labKubeOverview
	err error
}

func (k *labKubeFake) Overview(context.Context) (labKubeOverview, error) { return k.o, k.err }

type labTracesFake struct {
	names []string
	err   error
}

func (t *labTracesFake) Services(context.Context) ([]string, error) { return t.names, t.err }

func labStr(s string) *string { return &s }

func labVerify(t *testing.T, w *labWorld, tox *labToxiFake, checks ...string) LabVerifyDto {
	t.Helper()
	if tox == nil {
		tox = &labToxiFake{view: labChaosView{Available: true, Targets: []labChaosTarget{{ID: "floci-aws", Name: "Floci AWS", Enabled: true}, {ID: "redis", Name: "Redis"}}}}
	}
	def, problem := labParse(labYAML("", checks...))
	if problem != "" {
		t.Fatal(problem)
	}
	return NewLabVerifier(w, tox, nil, nil).Verify(context.Background(), def)
}

func labDetail(t *testing.T, r LabVerifyDto) string {
	t.Helper()
	if len(r.Results) != 1 || r.Results[0].Detail == nil {
		t.Fatalf("expected one failed check, got %+v", r)
	}
	return *r.Results[0].Detail
}

func labContainsAll(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not contain %q", got, w)
		}
	}
}

func TestLabVerifyS3BucketAndObject(t *testing.T) {
	w := &labWorld{buckets: []string{"b1"}, objectText: labStr("say hello nimbo please")}
	if !labVerify(t, w, nil, "{type: s3.bucket, name: b1, label: L}", "{type: s3.object, bucket: b1, key: hello.txt, contains: hello nimbo, label: L}").Passed {
		t.Fatal("should pass")
	}
	missing := labVerify(t, w, nil, "{type: s3.bucket, name: nope, label: L}")
	if missing.Passed {
		t.Fatal("should fail")
	}
	labContainsAll(t, labDetail(t, missing), "found: b1")
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: s3.object, bucket: b1, key: hello.txt, contains: other, label: L}")), "does not contain 'other'")

	w.objectText = nil
	if got := labDetail(t, labVerify(t, w, nil, "{type: s3.object, bucket: b1, key: hello.txt, label: L}")); got != "not found" {
		t.Fatal(got)
	}
	w.buckets = nil
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: s3.bucket, name: b, label: L}")), "found: none")
}

func TestLabVerifyListsAtMostFiveNamesThatWereFound(t *testing.T) {
	w := &labWorld{queues: []string{"q1", "q2", "q3", "q4", "q5", "q6", "q7"}}
	got := labDetail(t, labVerify(t, w, nil, "{type: sqs.queue, name: ghost, label: L}"))
	if got != "no such queue; found: q1, q2, q3, q4, q5" {
		t.Fatal(got)
	}
}

func TestLabVerifySQSQueueAndMessageCounts(t *testing.T) {
	w := &labWorld{queues: []string{"q"}, messages: []string{`{"event":"created"}`, "other"}}
	if !labVerify(t, w, nil, "{type: sqs.queue, name: q, label: L}", "{type: sqs.messages, queue: q, atLeast: 2, label: L}").Passed {
		t.Fatal("should pass")
	}
	if !labVerify(t, w, nil, "{type: sqs.messages, queue: q, contains: created, label: L}").Passed {
		t.Fatal("contains should pass")
	}
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: sqs.messages, queue: q, atLeast: 3, label: L}")), "found 2 message(s), need 3")
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: sqs.messages, queue: q, contains: shipped, label: L}")), "containing 'shipped'")
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: sqs.queue, name: ghost, label: L}")), "found: q")
}

func TestLabVerifyDynamoDBTableKeyAndItems(t *testing.T) {
	w := &labWorld{tables: map[string][]string{"t": {"id"}}, items: []any{
		map[string]any{"id": map[string]any{"S": "1"}, "total": map[string]any{"N": "5"}},
		map[string]any{"id": map[string]any{"S": "2"}, "total": map[string]any{"N": "6"}},
		map[string]any{"id": map[string]any{"S": "3"}},
	}}
	if !labVerify(t, w, nil, "{type: dynamodb.table, name: t, key: id, label: L}").Passed {
		t.Fatal("table should pass")
	}
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: dynamodb.table, name: t, key: pk, label: L}")), "not 'pk'", "the key is id")
	if got := labDetail(t, labVerify(t, w, nil, "{type: dynamodb.table, name: x, label: L}")); got != "no such table" {
		t.Fatal(got)
	}
	if !labVerify(t, w, nil, "{type: dynamodb.items, table: t, atLeast: 3, label: L}").Passed {
		t.Fatal("items should pass")
	}
	if !labVerify(t, w, nil, "{type: dynamodb.items, table: t, atLeast: 2, attribute: total, label: L}").Passed {
		t.Fatal("attribute should pass")
	}
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: dynamodb.items, table: t, atLeast: 3, attribute: total, label: L}")), "found 2 item(s) with 'total'")
}

func TestLabVerifyPubSubTopicAndSubscriptionWiring(t *testing.T) {
	w := &labWorld{topics: []string{"events"}, subscriptions: map[string]string{"billing": "events", "stray": "other"}}
	if !labVerify(t, w, nil, "{type: pubsub.topic, name: events, label: L}", "{type: pubsub.subscription, name: billing, topic: events, label: L}").Passed {
		t.Fatal("should pass")
	}
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: pubsub.subscription, name: stray, topic: events, label: L}")), "listens to 'other'")
	if got := labDetail(t, labVerify(t, w, nil, "{type: pubsub.subscription, name: nope, label: L}")); got != "no such subscription" {
		t.Fatal(got)
	}
	labContainsAll(t, labDetail(t, labVerify(t, w, nil, "{type: pubsub.topic, name: nope, label: L}")), "found: events")
	// without a topic parameter any wiring is fine
	if !labVerify(t, w, nil, "{type: pubsub.subscription, name: stray, label: L}").Passed {
		t.Fatal("topic is optional")
	}
}

func TestLabVerifyChaosFaultMustBeActiveAndStrongEnough(t *testing.T) {
	check := "{type: chaos.fault, target: floci-aws, preset: latency, attribute: latency, atLeast: 1000, label: L}"
	tox := &labToxiFake{view: labChaosView{Available: true, Targets: []labChaosTarget{{ID: "floci-aws", Name: "Floci AWS", Enabled: true}}}}

	labContainsAll(t, labDetail(t, labVerify(t, &labWorld{}, tox, check)), "no 'latency' fault")

	tox.view.Targets[0].Toxics = []labToxic{{"latency", map[string]any{"latency": 400.0}}}
	labContainsAll(t, labDetail(t, labVerify(t, &labWorld{}, tox, check)), "need at least 1000", "latency is 400")

	tox.view.Targets[0].Toxics = []labToxic{{"latency", map[string]any{"latency": 1500.0}}}
	if !labVerify(t, &labWorld{}, tox, check).Passed {
		t.Fatal("strong enough fault should pass")
	}
	tox.view.Targets[0].Toxics = []labToxic{{"latency", map[string]any{"latency": "1500"}}}
	if !labVerify(t, &labWorld{}, tox, check).Passed {
		t.Fatal("a numeric string attribute counts")
	}
	tox.view.Targets[0].Toxics = []labToxic{{"latency", map[string]any{}}}
	labContainsAll(t, labDetail(t, labVerify(t, &labWorld{}, tox, check)), "the fault has no 'latency'")

	off := &labToxiFake{view: labChaosView{Available: false}}
	if got := labDetail(t, labVerify(t, &labWorld{}, off, check)); got != "Toxiproxy is not running" {
		t.Fatal(got)
	}
	if got := labDetail(t, labVerify(t, &labWorld{}, tox, "{type: chaos.fault, target: floci-gcp, preset: latency, label: L}")); got != "unknown target" {
		t.Fatal(got)
	}
}

func TestLabVerifyAnOutageSatisfiesAnOutageCheck(t *testing.T) {
	tox := &labToxiFake{view: labChaosView{Available: true, Targets: []labChaosTarget{{ID: "redis", Name: "Redis", Enabled: false}}}}
	if !labVerify(t, &labWorld{}, tox, "{type: chaos.fault, target: redis, preset: outage, label: L}").Passed {
		t.Fatal("a disabled proxy is an outage")
	}
	// but a disabled proxy is not a latency fault
	labContainsAll(t, labDetail(t, labVerify(t, &labWorld{}, tox, "{type: chaos.fault, target: redis, preset: latency, label: L}")), "no 'latency' fault")
}

func TestLabVerifyAnEmulatorThatIsDownFailsEveryCheckWithAUsefulReasonInsteadOfThrowing(t *testing.T) {
	r := labVerify(t, &labWorld{down: true}, nil,
		"{type: s3.bucket, name: b, label: L}", "{type: sqs.queue, name: q, label: L}", "{type: pubsub.topic, name: t, label: L}")
	if r.Passed {
		t.Fatal("should fail")
	}
	for _, res := range r.Results {
		if res.Ok || res.Detail == nil || !strings.Contains(*res.Detail, "not reachable") {
			t.Fatalf("%+v", res)
		}
	}
}

func TestLabVerifyOtherEmulatorErrorsAreReportedWithTheirMessage(t *testing.T) {
	bad := &labToxiFake{err: EmulatorError{502, "Toxiproxy said no"}}
	if got := labDetail(t, labVerify(t, &labWorld{}, bad, "{type: chaos.fault, target: redis, preset: outage, label: L}")); got != "Toxiproxy said no" {
		t.Fatal(got)
	}
	empty := &labToxiFake{err: EmulatorError{502, ""}}
	if got := labDetail(t, labVerify(t, &labWorld{}, empty, "{type: chaos.fault, target: redis, preset: outage, label: L}")); got != "emulator error" {
		t.Fatal(got)
	}
	plain := &labToxiFake{err: errors.New("boom")}
	if got := labDetail(t, labVerify(t, &labWorld{}, plain, "{type: chaos.fault, target: redis, preset: outage, label: L}")); got != "boom" {
		t.Fatal(got)
	}
}

func TestLabVerifyPassedNeedsEveryCheck(t *testing.T) {
	r := labVerify(t, &labWorld{buckets: []string{"b1"}}, nil, "{type: s3.bucket, name: b1, label: L}", "{type: s3.bucket, name: b2, label: L}")
	if r.Passed || !r.Results[0].Ok || r.Results[1].Ok || r.Results[0].Index != 0 || r.Results[1].Index != 1 || r.ID != "t" {
		t.Fatalf("%+v", r)
	}
	if r.Results[0].Detail != nil {
		t.Fatal("a passing check has no detail")
	}
}

// ---- cluster and tracing checks ----

func labHealthyCluster() *labKubeFake {
	return &labKubeFake{o: labKubeOverview{
		Available:   true,
		Deployments: []labDeployment{{"default", "hello", 2, 3}},
		Services:    []labService{{"default", "hello", "NodePort"}, {"default", "kubernetes", "ClusterIP"}},
	}}
}

func labClusterVerify(t *testing.T, kube labKube, traces labTraces, checks ...string) LabVerifyDto {
	t.Helper()
	def, problem := labParse(labYAML("", checks...))
	if problem != "" {
		t.Fatal(problem)
	}
	return NewLabVerifier(&labWorld{}, &labToxiFake{}, kube, traces).Verify(context.Background(), def)
}

func TestLabClusterDeploymentReadinessIsComparedWithWhatTheLabAsksFor(t *testing.T) {
	up := labHealthyCluster()
	if !labClusterVerify(t, up, nil, "{type: k8s.deployment, name: hello, atLeast: 2, label: L}").Passed {
		t.Fatal("atLeast 2")
	}
	if !labClusterVerify(t, up, nil, "{type: k8s.deployment, name: hello, label: L}").Passed {
		t.Fatal("atLeast defaults to 1")
	}
	labContainsAll(t, labDetail(t, labClusterVerify(t, up, nil, "{type: k8s.deployment, name: hello, atLeast: 3, label: L}")), "2 of 3 replicas ready, need 3")
	labContainsAll(t, labDetail(t, labClusterVerify(t, up, nil, "{type: k8s.deployment, name: ghost, label: L}")), "no deployment ghost in namespace default")
	labContainsAll(t, labDetail(t, labClusterVerify(t, up, nil, "{type: k8s.deployment, name: hello, namespace: staging, label: L}")), "namespace staging")
}

func TestLabClusterServiceExistenceAndType(t *testing.T) {
	up := labHealthyCluster()
	if !labClusterVerify(t, up, nil, "{type: k8s.service, name: hello, serviceType: NodePort, label: L}").Passed {
		t.Fatal("NodePort")
	}
	if !labClusterVerify(t, up, nil, "{type: k8s.service, name: hello, label: L}").Passed {
		t.Fatal("any type")
	}
	labContainsAll(t, labDetail(t, labClusterVerify(t, up, nil, "{type: k8s.service, name: kubernetes, serviceType: NodePort, label: L}")), "ClusterIP, not NodePort")
	labContainsAll(t, labDetail(t, labClusterVerify(t, up, nil, "{type: k8s.service, name: nope, label: L}")), "no service nope")
}

func TestLabClusterAStoppedClusterFailsWithTheReasonAnUnconfiguredOneSaysSo(t *testing.T) {
	stopped := &labKubeFake{o: labKubeOverview{Available: false, Reason: "the k3s container is not running"}}
	labContainsAll(t, labDetail(t, labClusterVerify(t, stopped, nil, "{type: k8s.deployment, name: hello, label: L}")), "not running")
	noReason := &labKubeFake{o: labKubeOverview{Available: false}}
	if got := labDetail(t, labClusterVerify(t, noReason, nil, "{type: k8s.service, name: hello, label: L}")); got != "the cluster is not running" {
		t.Fatal(got)
	}
	if got := labDetail(t, labClusterVerify(t, nil, nil, "{type: k8s.service, name: hello, label: L}")); got != "Kubernetes is not available" {
		t.Fatal(got)
	}
	if got := labDetail(t, labClusterVerify(t, nil, nil, "{type: trace.service, name: s, label: L}")); got != "Tracing is not available" {
		t.Fatal(got)
	}
	failing := &labKubeFake{err: KubeError{"kubectl failed: boom"}}
	labContainsAll(t, labDetail(t, labClusterVerify(t, failing, nil, "{type: k8s.deployment, name: hello, label: L}")), "kubectl failed: boom")
}

func TestLabClusterAServiceShowsUpOnceItHasSentTraces(t *testing.T) {
	jaeger := &labTracesFake{names: []string{"lab-orders-api", "other"}}
	if !labClusterVerify(t, nil, jaeger, "{type: trace.service, name: lab-orders-api, label: L}").Passed {
		t.Fatal("should pass")
	}
	detail := labDetail(t, labClusterVerify(t, nil, jaeger, "{type: trace.service, name: missing, label: L}"))
	labContainsAll(t, detail, "no traces from that service yet", "lab-orders-api")
	down := &labTracesFake{err: EmulatorUnreachable{"down"}}
	labContainsAll(t, labDetail(t, labClusterVerify(t, nil, down, "{type: trace.service, name: x, label: L}")), "not reachable")
}

func TestLabClusterTheNewCheckTypesValidateTheirParameters(t *testing.T) {
	for check, want := range map[string]string{
		"{type: k8s.deployment, label: L}":                         "missing 'name'",
		"{type: k8s.service, name: a, namespace: 'x y', label: L}": "invalid value",
		"{type: trace.service, name: '../x', label: L}":            "invalid value",
	} {
		_, problem := labParse(labYAML("", check))
		if !strings.Contains(problem, want) {
			t.Errorf("%s: %q", check, problem)
		}
	}
}

func TestLabVerifyAnUnsupportedTypeIsReported(t *testing.T) {
	def := &LabDef{Dto: LabDto{ID: "x"}, Checks: []CheckSpec{{Type: "weird", Params: map[string]any{}}}}
	r := NewLabVerifier(&labWorld{}, &labToxiFake{}, nil, nil).Verify(context.Background(), def)
	if got := labDetail(t, r); got != "unsupported check type" {
		t.Fatal(got)
	}
}
