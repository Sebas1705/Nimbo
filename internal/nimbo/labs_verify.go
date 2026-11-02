package nimbo

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The verifier needs read-only views of the emulators. Each is the smallest interface the checks use; the
// integrator adapts the observe area's Inspector, Chaos, Kube and Traces to them. Gateway failures must come back as
// EmulatorUnreachable / EmulatorError (a missing object is an EmulatorError with Status 404).

type labTable struct {
	Name string
	Keys []string
}

type labSubscription struct {
	Name  string
	Topic *string // nil when the subscription has no topic
}

type labInspector interface {
	BucketNames(ctx context.Context) ([]string, error)
	ObjectText(ctx context.Context, bucket, key string) (string, error)
	QueueNames(ctx context.Context) ([]string, error)
	MessageBodies(ctx context.Context, queue string) ([]string, error)
	Tables(ctx context.Context) ([]labTable, error)
	// Items returns up to limit items; each is a JSON value (an object when it is a normal item).
	Items(ctx context.Context, table string, limit int) ([]any, error)
	PubSub(ctx context.Context, project string) (topics []string, subscriptions []labSubscription, err error)
}

type labToxic struct {
	Name       string
	Attributes map[string]any
}

type labChaosTarget struct {
	ID      string
	Name    string
	Enabled bool
	Toxics  []labToxic
}

type labChaosView struct {
	Available bool
	Targets   []labChaosTarget
}

type labChaos interface {
	View(ctx context.Context) (labChaosView, error)
}

type labDeployment struct {
	Namespace, Name string
	Ready, Desired  int
}

type labService struct {
	Namespace, Name, Type string
}

type labKubeOverview struct {
	Available   bool
	Reason      string // why the cluster is not available ("" when unknown)
	Deployments []labDeployment
	Services    []labService
}

type labKube interface {
	Overview(ctx context.Context) (labKubeOverview, error)
}

type labTraces interface {
	// Services lists the services that have sent traces.
	Services(ctx context.Context) ([]string, error)
}

// LabVerifier evaluates a lab's checks against the live emulators, using the same read-only views as the
// inspector. kube and traces may be nil (those checks then say they are not available).
type LabVerifier struct {
	inspector labInspector
	chaos     labChaos
	kube      labKube
	traces    labTraces
}

func NewLabVerifier(inspector labInspector, chaos labChaos, kube labKube, traces labTraces) *LabVerifier {
	return &LabVerifier{inspector: inspector, chaos: chaos, kube: kube, traces: traces}
}

func (v *LabVerifier) Verify(ctx context.Context, lab *LabDef) LabVerifyDto {
	results := make([]LabCheckResultDto, 0, len(lab.Checks))
	passed := true
	for i, spec := range lab.Checks {
		outcome := v.safely(ctx, spec)
		r := LabCheckResultDto{Index: i, Ok: outcome == ""}
		if outcome != "" {
			passed = false
			o := outcome
			r.Detail = &o
		}
		results = append(results, r)
	}
	return LabVerifyDto{ID: lab.Dto.ID, Passed: passed, Results: results}
}

// safely turns every failure into the reason shown next to the check instead of an error.
func (v *LabVerifier) safely(ctx context.Context, spec CheckSpec) (outcome string) {
	defer func() {
		if r := recover(); r != nil {
			outcome = fmt.Sprint(r)
		}
	}()
	outcome, err := v.evaluate(ctx, spec)
	if err == nil {
		return outcome
	}
	var eu EmulatorUnreachable
	var ee EmulatorError
	switch {
	case errors.As(err, &eu):
		return "the emulator is not reachable; start it first"
	case errors.As(err, &ee):
		if ee.Status == 404 {
			return "not found"
		}
		return orDefault(ee.Msg, "emulator error")
	}
	return orDefault(err.Error(), "error")
}

func labFound(names []string) string {
	if len(names) > 5 {
		names = names[:5]
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func labContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// evaluate returns "" when the check passes, otherwise why it does not.
func (v *LabVerifier) evaluate(ctx context.Context, c CheckSpec) (string, error) {
	str := func(k string) string { s, _ := c.Str(k); return s }
	project, ok := c.Str("project")
	if !ok {
		project = "floci-local"
	}
	switch c.Type {
	case "s3.bucket":
		names, err := v.inspector.BucketNames(ctx)
		if err != nil {
			return "", err
		}
		if _, ok := c.Str("name"); ok && labContains(names, str("name")) {
			return "", nil
		}
		return "no such bucket; found: " + labFound(names), nil

	case "s3.object":
		text, err := v.inspector.ObjectText(ctx, str("bucket"), str("key"))
		if err != nil {
			return "", err
		}
		if needle, ok := c.Str("contains"); ok && !strings.Contains(text, needle) {
			return fmt.Sprintf("the object does not contain '%s'", needle), nil
		}
		return "", nil

	case "sqs.queue":
		names, err := v.inspector.QueueNames(ctx)
		if err != nil {
			return "", err
		}
		if labContains(names, str("name")) {
			return "", nil
		}
		return "no such queue; found: " + labFound(names), nil

	case "sqs.messages":
		bodies, err := v.inspector.MessageBodies(ctx, str("queue"))
		if err != nil {
			return "", err
		}
		atLeast := c.Int("atLeast", 1)
		needle, hasNeedle := c.Str("contains")
		matching := 0
		for _, b := range bodies {
			if !hasNeedle || strings.Contains(b, needle) {
				matching++
			}
		}
		if matching >= atLeast {
			return "", nil
		}
		containing := ""
		if hasNeedle {
			containing = fmt.Sprintf(" containing '%s'", needle)
		}
		return fmt.Sprintf("found %d message(s)%s, need %d", matching, containing, atLeast), nil

	case "dynamodb.table":
		tables, err := v.inspector.Tables(ctx)
		if err != nil {
			return "", err
		}
		for _, t := range tables {
			if t.Name != str("name") {
				continue
			}
			if key, ok := c.Str("key"); ok && !labContains(t.Keys, key) {
				return fmt.Sprintf("the key is %s, not '%s'", strings.Join(t.Keys, ", "), key), nil
			}
			return "", nil
		}
		return "no such table", nil

	case "dynamodb.items":
		items, err := v.inspector.Items(ctx, str("table"), 100)
		if err != nil {
			return "", err
		}
		atLeast := c.Int("atLeast", 1)
		attr, hasAttr := c.Str("attribute")
		count := len(items)
		if hasAttr {
			count = 0
			for _, it := range items {
				if m, ok := it.(map[string]any); ok {
					if _, has := m[attr]; has {
						count++
					}
				}
			}
		}
		if count >= atLeast {
			return "", nil
		}
		with := ""
		if hasAttr {
			with = fmt.Sprintf(" with '%s'", attr)
		}
		return fmt.Sprintf("found %d item(s)%s, need %d", count, with, atLeast), nil

	case "pubsub.topic":
		topics, _, err := v.inspector.PubSub(ctx, project)
		if err != nil {
			return "", err
		}
		if labContains(topics, str("name")) {
			return "", nil
		}
		return "no such topic; found: " + labFound(topics), nil

	case "pubsub.subscription":
		_, subs, err := v.inspector.PubSub(ctx, project)
		if err != nil {
			return "", err
		}
		for _, s := range subs {
			if s.Name != str("name") {
				continue
			}
			if want, ok := c.Str("topic"); ok {
				got := ""
				if s.Topic != nil {
					got = *s.Topic
				}
				if s.Topic == nil || got != want {
					return fmt.Sprintf("it listens to '%s', not '%s'", labText(labTopicOrNull(s.Topic)), want), nil
				}
			}
			return "", nil
		}
		return "no such subscription", nil

	case "chaos.fault":
		view, err := v.chaos.View(ctx)
		if err != nil {
			return "", err
		}
		if !view.Available {
			return "Toxiproxy is not running", nil
		}
		var target *labChaosTarget
		for i := range view.Targets {
			if view.Targets[i].ID == str("target") {
				target = &view.Targets[i]
				break
			}
		}
		if target == nil {
			return "unknown target", nil
		}
		preset := str("preset")
		if !target.Enabled && preset == "outage" {
			return "", nil
		}
		var toxic *labToxic
		for i := range target.Toxics {
			if target.Toxics[i].Name == preset {
				toxic = &target.Toxics[i]
				break
			}
		}
		if toxic == nil {
			return fmt.Sprintf("no '%s' fault is active on %s", preset, target.Name), nil
		}
		attr, hasAttr := c.Str("attribute")
		if hasAttr && labIsNumber(c.Params["atLeast"]) {
			min, _ := labNumber(c.Params["atLeast"])
			value, ok := labNumber(toxic.Attributes[attr])
			if !ok {
				return fmt.Sprintf("the fault has no '%s'", attr), nil
			}
			if value < min {
				return fmt.Sprintf("%s is %d, need at least %d", attr, int64(value), int64(min)), nil
			}
		}
		return "", nil

	case "k8s.deployment":
		if v.kube == nil {
			return "Kubernetes is not available", nil
		}
		cluster, err := v.kube.Overview(ctx)
		if err != nil {
			return "", err
		}
		if !cluster.Available {
			return orDefault(cluster.Reason, "the cluster is not running"), nil
		}
		ns, ok := c.Str("namespace")
		if !ok {
			ns = "default"
		}
		for _, d := range cluster.Deployments {
			if d.Namespace != ns || d.Name != str("name") {
				continue
			}
			atLeast := c.Int("atLeast", 1)
			if d.Ready >= atLeast {
				return "", nil
			}
			return fmt.Sprintf("%d of %d replicas ready, need %d", d.Ready, d.Desired, atLeast), nil
		}
		return fmt.Sprintf("no deployment %s in namespace %s", str("name"), ns), nil

	case "k8s.service":
		if v.kube == nil {
			return "Kubernetes is not available", nil
		}
		cluster, err := v.kube.Overview(ctx)
		if err != nil {
			return "", err
		}
		if !cluster.Available {
			return orDefault(cluster.Reason, "the cluster is not running"), nil
		}
		ns, ok := c.Str("namespace")
		if !ok {
			ns = "default"
		}
		for _, s := range cluster.Services {
			if s.Namespace != ns || s.Name != str("name") {
				continue
			}
			if want, ok := c.Str("serviceType"); ok && s.Type != want {
				return fmt.Sprintf("the service is %s, not %s", s.Type, want), nil
			}
			return "", nil
		}
		return fmt.Sprintf("no service %s in namespace %s", str("name"), ns), nil

	case "trace.service":
		if v.traces == nil {
			return "Tracing is not available", nil
		}
		names, err := v.traces.Services(ctx)
		if err != nil {
			return "", err
		}
		if labContains(names, str("name")) {
			return "", nil
		}
		return "no traces from that service yet; found: " + labFound(names), nil
	}
	return "unsupported check type", nil
}

func labTopicOrNull(t *string) any {
	if t == nil {
		return nil
	}
	return *t
}
