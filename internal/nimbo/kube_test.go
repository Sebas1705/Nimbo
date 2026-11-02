package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const obsCluster = `{"apiVersion":"v1","kind":"List","items":[
{"kind":"Node","metadata":{"name":"n1"},"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.34.1+k3s1"},"capacity":{"cpu":"32","memory":"32421584Ki"}}},
{"kind":"Namespace","metadata":{"name":"kube-system"}},
{"kind":"Namespace","metadata":{"name":"default"}},
{"kind":"Deployment","metadata":{"name":"hello","namespace":"default"},"spec":{"replicas":3},"status":{"readyReplicas":2}},
{"kind":"Deployment","metadata":{"name":"idle","namespace":"default"},"spec":{"replicas":0},"status":{}},
{"kind":"Pod","metadata":{"name":"hello-1","namespace":"default"},"spec":{"nodeName":"n1","containers":[{"name":"web"}]},"status":{"phase":"Running","startTime":"2026-11-01T18:43:17Z","containerStatuses":[{"ready":true,"restartCount":0,"state":{"running":{}}}]}},
{"kind":"Pod","metadata":{"name":"crashy-1","namespace":"default"},"spec":{"nodeName":"n1","containers":[{"name":"boom"}]},"status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
{"kind":"Pod","metadata":{"name":"going","namespace":"default","deletionTimestamp":"2026-11-01T19:00:00Z"},"spec":{"containers":[{"name":"a"},{"name":"b"}]},"status":{"phase":"Running"}},
{"kind":"Pod","metadata":{"name":"flap-1","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":2,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}},
{"kind":"Pod","metadata":{"name":"job-done","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Succeeded","containerStatuses":[{"ready":false,"restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}},
{"kind":"Pod","metadata":{"name":"healed-1","namespace":"default"},"spec":{"containers":[{"name":"x"}]},"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":3,"state":{"running":{}},"lastState":{"terminated":{"reason":"Error"}}}]}},
{"kind":"Service","metadata":{"name":"hello","namespace":"default"},"spec":{"type":"NodePort","clusterIP":"10.43.149.167","ports":[{"port":80,"nodePort":30080,"protocol":"TCP"}]}},
{"kind":"Service","metadata":{"name":"kubernetes","namespace":"default"},"spec":{"clusterIP":"10.43.0.1","ports":[{"port":443,"protocol":"TCP"}]}}
]}`

// obsScriptedShell records every invocation; respond decides the result. It never runs a process.
type obsScriptedShell struct {
	respond func(args []string, stdin *string) ExecResult
	calls   [][]string
	stdins  []*string
}

func (s *obsScriptedShell) Run(_ context.Context, args []string, stdin *string, _ int) ExecResult {
	s.calls = append(s.calls, args)
	s.stdins = append(s.stdins, stdin)
	return s.respond(args, stdin)
}

func obsShell(f func([]string, *string) ExecResult) *obsScriptedShell {
	return &obsScriptedShell{respond: f}
}

func obsHealthy(extra func([]string, *string) *ExecResult) *obsScriptedShell {
	return obsShell(func(args []string, stdin *string) ExecResult {
		if extra != nil {
			if r := extra(args, stdin); r != nil {
				return *r
			}
		}
		switch {
		case slices.Contains(args, "--raw=/readyz"):
			return ExecResult{0, "ok"}
		case slices.Contains(args, "get") && slices.Contains(args, "nodes,namespaces,deployments,pods,services"):
			return ExecResult{0, obsCluster}
		case slices.Contains(args, "cat"):
			return ExecResult{0, "apiVersion: v1\nclusters: []"}
		}
		return ExecResult{0, "done"}
	})
}

func obsWhen(word string, r ExecResult) func([]string, *string) *ExecResult {
	return func(args []string, _ *string) *ExecResult {
		if slices.Contains(args, word) {
			return &r
		}
		return nil
	}
}

func obsAlwaysExit(code int, out string) *obsScriptedShell {
	return obsShell(func([]string, *string) ExecResult { return ExecResult{code, out} })
}

func obsKube(s Shell) *Kube { return NewKube(s, "") }

func TestKubeOverviewParsesTheCluster(t *testing.T) {
	o := obsMust[KubeOverviewDto](t)(obsKube(obsHealthy(nil)).Overview(context.Background()))
	if !o.Available || o.Version == nil || *o.Version != "v1.34.1+k3s1" {
		t.Fatalf("overview: %+v", o)
	}
	if !slices.Equal(o.Namespaces, []string{"default", "kube-system"}) {
		t.Fatalf("namespaces: %v", o.Namespaces)
	}
	if len(o.Nodes) != 1 || !o.Nodes[0].Ready || *o.Nodes[0].CPU != "32" {
		t.Fatalf("nodes: %+v", o.Nodes)
	}
	if len(o.Deployments) != 2 || o.Deployments[0] != (KubeDeploymentDto{"default", "hello", 2, 3}) || o.Deployments[1] != (KubeDeploymentDto{"default", "idle", 0, 0}) {
		t.Fatalf("deployments: %+v", o.Deployments)
	}
	pods := map[string]KubePodDto{}
	for _, p := range o.Pods {
		pods[p.Name] = p
	}
	checks := []struct{ pod, status, ready, why string }{
		{"hello-1", "Running", "1/1", ""},
		{"crashy-1", "CrashLoopBackOff", "0/1", "a waiting reason is more useful than the phase"},
		{"going", "Terminating", "0/2", "no container statuses yet: use the spec"},
		{"flap-1", "Error", "0/1", "a restarting container that is momentarily running is an Error"},
		{"job-done", "Completed", "0/1", ""},
		{"healed-1", "Running", "1/1", "restarted in the past but ready now"},
	}
	for _, c := range checks {
		p := pods[c.pod]
		if p.Status != c.status || p.Ready != c.ready {
			t.Errorf("%s: %s %s, want %s %s (%s)", c.pod, p.Status, p.Ready, c.status, c.ready, c.why)
		}
	}
	if pods["crashy-1"].Restarts != 4 || pods["hello-1"].Node == nil || *pods["hello-1"].Node != "n1" || *pods["hello-1"].StartedAt != "2026-11-01T18:43:17Z" {
		t.Fatalf("pods: %+v", pods)
	}
	var hello, kubernetes KubeServiceDto
	for _, s := range o.Services {
		if s.Name == "hello" {
			hello = s
		} else if s.Name == "kubernetes" {
			kubernetes = s
		}
	}
	if hello.Type != "NodePort" || !slices.Equal(hello.Ports, []string{"80:30080/TCP"}) || !slices.Equal(hello.NodePorts, []int{30080}) {
		t.Fatalf("hello: %+v", hello)
	}
	if kubernetes.Type != "ClusterIP" || !slices.Equal(kubernetes.Ports, []string{"443/TCP"}) || len(kubernetes.NodePorts) != 0 {
		t.Fatalf("kubernetes: %+v", kubernetes)
	}
}

func TestKubeOverviewUsesDockerExecWithKubectlInsideTheContainerAsAnArgumentList(t *testing.T) {
	shell := obsHealthy(nil)
	obsMust[KubeOverviewDto](t)(obsKube(shell).Overview(context.Background()))
	if !slices.Equal(shell.calls[0], []string{"exec", "nimbo-k3s", "kubectl", "get", "--raw=/readyz"}) {
		t.Fatalf("first: %v", shell.calls[0])
	}
	if !slices.Equal(shell.calls[1], []string{"exec", "nimbo-k3s", "kubectl", "get", "nodes,namespaces,deployments,pods,services", "-A", "-o", "json"}) {
		t.Fatalf("second: %v", shell.calls[1])
	}
}

func TestKubeAMissingContainerAndAStartingClusterAreToldApartAndNeitherThrows(t *testing.T) {
	cases := []struct{ out, want string }{
		{"Error response from daemon: No such container: nimbo-k3s", "not running"},
		{"container abc is not running", "not running"},
		{"the server is currently unable to handle the request", "starting"},
	}
	for _, c := range cases {
		o, err := obsKube(obsAlwaysExit(1, c.out)).Overview(context.Background())
		if err != nil || o.Available || o.Reason == nil || !strings.Contains(*o.Reason, c.want) || len(o.Pods) != 0 {
			t.Errorf("%q: %+v %v", c.out, o, err)
		}
	}
	o, _ := obsKube(obsAlwaysExit(1, "x")).Overview(context.Background())
	out, _ := json.Marshal(o)
	for _, key := range []string{`"namespaces":[]`, `"nodes":[]`, `"deployments":[]`, `"pods":[]`, `"services":[]`, `"version":null`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("%s missing in %s", key, out)
		}
	}
}

func TestKubeAFailingListingIsAnErrorNotAnEmptyCluster(t *testing.T) {
	shell := obsShell(func(args []string, _ *string) ExecResult {
		if slices.Contains(args, "--raw=/readyz") {
			return ExecResult{0, "ok"}
		}
		return ExecResult{1, "forbidden"}
	})
	var ke KubeError
	if _, err := obsKube(shell).Overview(context.Background()); !errors.As(err, &ke) {
		t.Fatalf("got %v", err)
	}
}

func TestKubeApplyPipesTheManifestThroughStdin(t *testing.T) {
	shell := obsHealthy(obsWhen("apply", ExecResult{0, "deployment.apps/hello created"}))
	out := obsMust[OutputDto](t)(obsKube(shell).Apply(context.Background(), "kind: Deployment\n"))
	if out.Output != "deployment.apps/hello created" {
		t.Fatalf("output: %s", out.Output)
	}
	if len(shell.calls) != 1 || !slices.Equal(shell.calls[0], []string{"exec", "-i", "nimbo-k3s", "kubectl", "apply", "-f", "-"}) {
		t.Fatalf("calls: %v", shell.calls)
	}
	if shell.stdins[0] == nil || *shell.stdins[0] != "kind: Deployment\n" {
		t.Fatalf("stdin: %v", shell.stdins[0])
	}
}

func TestKubeApplyRejectsEmptyAndOversizedManifestsAndReportsKubectlErrors(t *testing.T) {
	shell := obsHealthy(obsWhen("apply", ExecResult{1, "error: unable to recognize"}))
	kube := obsKube(shell)
	ctx := context.Background()
	var in InvalidName
	if _, err := kube.Apply(ctx, "   "); !errors.As(err, &in) {
		t.Errorf("blank: %v", err)
	}
	if _, err := kube.Apply(ctx, strings.Repeat("a", KubeMaxManifest+1)); !errors.As(err, &in) {
		t.Errorf("large: %v", err)
	}
	if len(shell.calls) != 0 {
		t.Fatalf("nothing is sent for invalid input: %v", shell.calls)
	}
	var ke KubeError
	_, err := kube.Apply(ctx, "nonsense: true")
	if !errors.As(err, &ke) || !strings.Contains(err.Error(), "unable to recognize") {
		t.Fatalf("got %v", err)
	}
}

func TestKubeScaleValidatesNamesAndBoundsBeforeCallingKubectl(t *testing.T) {
	shell := obsHealthy(nil)
	kube := obsKube(shell)
	ctx := context.Background()
	obsMust[OutputDto](t)(kube.Scale(ctx, "default", "hello", 5))
	if !slices.Equal(shell.calls[0], []string{"exec", "nimbo-k3s", "kubectl", "scale", "deployment/hello", "-n", "default", "--replicas=5"}) {
		t.Fatalf("call: %v", shell.calls[0])
	}
	shell.calls = nil
	var in InvalidName
	for _, n := range []int{-1, KubeMaxReplicas + 1} {
		if _, err := kube.Scale(ctx, "default", "hello", n); !errors.As(err, &in) {
			t.Errorf("replicas %d: %v", n, err)
		}
	}
	for _, bad := range []string{"Bad_Name", "a b", "-x", "../x", ""} {
		if _, err := kube.Scale(ctx, "default", bad, 1); !errors.As(err, &in) {
			t.Errorf("name %q: %v", bad, err)
		}
		if _, err := kube.Scale(ctx, bad, "hello", 1); !errors.As(err, &in) {
			t.Errorf("namespace %q: %v", bad, err)
		}
	}
	if len(shell.calls) != 0 {
		t.Fatalf("calls: %v", shell.calls)
	}
}

func TestKubeDeleteOnlyTouchesPodsDeploymentsAndServicesOutsideSystemNamespaces(t *testing.T) {
	shell := obsHealthy(nil)
	kube := obsKube(shell)
	ctx := context.Background()
	obsMust[OutputDto](t)(kube.Delete(ctx, "pod", "default", "hello-1"))
	if !slices.Equal(shell.calls[0], []string{"exec", "nimbo-k3s", "kubectl", "delete", "pod", "hello-1", "-n", "default"}) {
		t.Fatalf("call: %v", shell.calls[0])
	}
	shell.calls = nil
	var in InvalidName
	bad := [][3]string{
		{"namespace", "default", "default"}, {"secret", "default", "x"}, {"pod,secret", "default", "x"},
		{"deployment", "kube-system", "coredns"}, {"deployment", "kube-public", "coredns"}, {"deployment", "kube-node-lease", "coredns"},
		{"pod", "default", "--all"},
	}
	for _, b := range bad {
		if _, err := kube.Delete(ctx, b[0], b[1], b[2]); !errors.As(err, &in) {
			t.Errorf("%v: %v", b, err)
		}
	}
	if len(shell.calls) != 0 {
		t.Fatalf("calls: %v", shell.calls)
	}
}

func TestKubeLogsClampTheTailAndValidateNames(t *testing.T) {
	shell := obsHealthy(obsWhen("logs", ExecResult{0, "line"}))
	kube := obsKube(shell)
	ctx := context.Background()
	if out := obsMust[OutputDto](t)(kube.Logs(ctx, "default", "p", 999_999)); out.Output != "line" {
		t.Fatalf("output: %s", out.Output)
	}
	if !slices.Contains(shell.calls[0], "--tail=2000") {
		t.Fatalf("call: %v", shell.calls[0])
	}
	shell.calls = nil
	obsMust[OutputDto](t)(kube.Logs(ctx, "default", "p", 0))
	if !slices.Contains(shell.calls[0], "--tail=1") {
		t.Fatalf("call: %v", shell.calls[0])
	}
	var in InvalidName
	if _, err := kube.Logs(ctx, "default", "p;ls", 5); !errors.As(err, &in) {
		t.Errorf("injection: %v", err)
	}
	var ke KubeError
	if _, err := obsKube(obsAlwaysExit(1, "pod not found")).Logs(ctx, "default", "p", 200); !errors.As(err, &ke) {
		t.Errorf("failure: %v", err)
	}
}

func TestKubeKubeconfigIsReadFromTheContainer(t *testing.T) {
	shell := obsHealthy(nil)
	cfg := obsMust[KubeconfigDto](t)(obsKube(shell).Kubeconfig(context.Background()))
	if cfg.Filename != "nimbo-k3s.kubeconfig.yaml" || !strings.Contains(cfg.Content, "apiVersion") {
		t.Fatalf("cfg: %+v", cfg)
	}
	if len(shell.calls) != 1 || !slices.Equal(shell.calls[0], []string{"exec", "nimbo-k3s", "cat", "/etc/rancher/k3s/k3s.yaml"}) {
		t.Fatalf("calls: %v", shell.calls)
	}
	var ke KubeError
	if _, err := obsKube(obsAlwaysExit(1, "no such container")).Kubeconfig(context.Background()); !errors.As(err, &ke) {
		t.Fatalf("got %v", err)
	}
}

func TestKubeSamplesAreWellFormed(t *testing.T) {
	ids := map[string]bool{}
	for _, s := range KubeSamples {
		ids[s.ID] = true
		if !strings.Contains(s.Manifest, "apiVersion:") || !strings.Contains(s.Manifest, "kind: Deployment") || len(s.Manifest) >= KubeMaxManifest {
			t.Errorf("sample %s is malformed", s.ID)
		}
	}
	if len(ids) != len(KubeSamples) {
		t.Fatal("duplicate sample ids")
	}
	for _, s := range KubeSamples {
		if s.ID == "hello" && !strings.Contains(s.Manifest, "nodePort: 30080") {
			t.Fatal("hello must use nodePort 30080 (published by the k3s provider)")
		}
	}
}

// ---- HTTP API ----

func obsKubeAPI(shell Shell, method, path, body string) *httptest.ResponseRecorder {
	return obsServe(obsKube(shell).Register, method, path, body)
}

func TestKubeAPIOverviewSamplesAndKubeconfig(t *testing.T) {
	shell := obsHealthy(nil)
	var overview map[string]any
	rec := obsKubeAPI(shell, "GET", "/api/k8s", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil || overview["available"] != true {
		t.Fatalf("overview: %s %v", rec.Body, err)
	}
	if rec := obsKubeAPI(shell, "GET", "/api/k8s/samples", ""); !strings.Contains(rec.Body.String(), `"id":"hello"`) {
		t.Fatalf("samples: %s", rec.Body)
	}
	if rec := obsKubeAPI(shell, "GET", "/api/k8s/kubeconfig", ""); !strings.Contains(rec.Body.String(), "apiVersion") {
		t.Fatalf("kubeconfig: %s", rec.Body)
	}
}

func TestKubeAPIAStoppedClusterIsANormalAnswerNotAnError(t *testing.T) {
	rec := obsKubeAPI(obsAlwaysExit(1, "No such container"), "GET", "/api/k8s", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestKubeAPIApplyReturnsTheOutputAndMapsKubectlFailuresTo422(t *testing.T) {
	ok := obsHealthy(obsWhen("apply", ExecResult{0, "created"}))
	rec := obsKubeAPI(ok, "POST", "/api/k8s/apply", `{"manifest":"kind: Pod"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "created") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	bad := obsHealthy(obsWhen("apply", ExecResult{1, "error: bad yaml"}))
	rec = obsKubeAPI(bad, "POST", "/api/k8s/apply", `{"manifest":"x"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "bad yaml") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := obsKubeAPI(bad, "POST", "/api/k8s/apply", `{"manifest":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: %d", rec.Code)
	}
	for _, body := range []string{`{}`, `nope`} {
		if rec := obsKubeAPI(bad, "POST", "/api/k8s/apply", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: %d", body, rec.Code)
		}
	}
}

func TestKubeAPIScaleDeleteAndLogsGoThroughValidation(t *testing.T) {
	shell := obsHealthy(nil)
	reg := obsKube(shell).Register
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/k8s/namespaces/default/deployments/hello/scale", `{"replicas":3}`, 200},
		{"POST", "/api/k8s/namespaces/default/deployments/hello/scale", `{"replicas":999}`, 400},
		{"POST", "/api/k8s/namespaces/default/deployments/hello/scale", `{}`, 400},
		{"DELETE", "/api/k8s/namespaces/default/pod/hello-1", "", 200},
		{"DELETE", "/api/k8s/namespaces/kube-system/deployment/coredns", "", 400},
		{"DELETE", "/api/k8s/namespaces/default/secret/x", "", 400},
		{"GET", "/api/k8s/namespaces/default/pods/hello-1/logs?tail=5", "", 200},
		{"GET", "/api/k8s/namespaces/default/pods/BAD/logs", "", 400},
	}
	for _, c := range cases {
		if rec := obsServe(reg, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	for _, call := range shell.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "secret") || strings.Contains(joined, "kube-system") || strings.Contains(joined, "999") {
			t.Errorf("a rejected request reached kubectl: %s", joined)
		}
	}
}
