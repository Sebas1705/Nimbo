package nimbo

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type KubeNodeDto struct {
	Name    string  `json:"name"`
	Ready   bool    `json:"ready"`
	Version *string `json:"version"`
	CPU     *string `json:"cpu"`
	Memory  *string `json:"memory"`
}
type KubePodDto struct {
	Namespace string  `json:"namespace"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Ready     string  `json:"ready"`
	Restarts  int     `json:"restarts"`
	Node      *string `json:"node"`
	StartedAt *string `json:"startedAt"`
}
type KubeDeploymentDto struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     int    `json:"ready"`
	Desired   int    `json:"desired"`
}
type KubeServiceDto struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	ClusterIP *string  `json:"clusterIp"`
	Ports     []string `json:"ports"`
	NodePorts []int    `json:"nodePorts"`
}
type KubeOverviewDto struct {
	Available   bool                `json:"available"`
	Reason      *string             `json:"reason"`
	Version     *string             `json:"version"`
	Namespaces  []string            `json:"namespaces"`
	Nodes       []KubeNodeDto       `json:"nodes"`
	Deployments []KubeDeploymentDto `json:"deployments"`
	Pods        []KubePodDto        `json:"pods"`
	Services    []KubeServiceDto    `json:"services"`
}
type ApplyManifestDto struct {
	Manifest string `json:"manifest"`
}
type OutputDto struct {
	Output string `json:"output"`
}
type ScaleDto struct {
	Replicas int `json:"replicas"`
}
type KubeSampleDto struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Manifest    string `json:"manifest"`
}
type KubeconfigDto struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

const (
	KubeMaxManifest = 256 * 1024
	KubeMaxReplicas = 20
	// KubeContainer is the name of the k3s container; kubectl lives inside it.
	KubeContainer = "nimbo-k3s"
)

var (
	kubeName      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	kubeNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	kubeDeletable = []string{"pod", "deployment", "service"}
	kubeProtected = map[string]bool{"kube-system": true, "kube-public": true, "kube-node-lease": true}
)

// Kube drives a single-node k3s cluster running as the `nimbo-k3s` container. `kubectl` lives inside that
// container, so nothing needs to be installed on the host and every command is an argument list, never a shell string.
type Kube struct {
	shell     Shell
	container string
}

// NewKube runs kubectl inside container (empty means nimbo-k3s) through shell.
func NewKube(shell Shell, container string) *Kube {
	return &Kube{shell: shell, container: orDefault(container, KubeContainer)}
}

func (k *Kube) kubectl(ctx context.Context, timeout int, stdin *string, args ...string) ExecResult {
	command := []string{"exec"}
	if stdin != nil {
		command = append(command, "-i")
	}
	command = append(command, k.container, "kubectl")
	command = append(command, args...)
	return k.shell.Run(ctx, command, stdin, timeout)
}

// Overview answers "is the cluster up" and, when it is, what runs in it. A stopped or starting cluster is a normal
// answer (Available false with a reason), not an error.
func (k *Kube) Overview(ctx context.Context) (KubeOverviewDto, error) {
	ready := k.kubectl(ctx, 15, nil, "get", "--raw=/readyz")
	if !ready.OK() {
		reason := "The cluster is still starting"
		if strings.Contains(ready.Output, "No such container") || strings.Contains(ready.Output, "is not running") {
			reason = "The k3s container is not running"
		}
		return KubeOverviewDto{Available: false, Reason: &reason, Namespaces: []string{}, Nodes: []KubeNodeDto{}, Deployments: []KubeDeploymentDto{}, Pods: []KubePodDto{}, Services: []KubeServiceDto{}}, nil
	}
	all := k.kubectl(ctx, 30, nil, "get", "nodes,namespaces,deployments,pods,services", "-A", "-o", "json")
	if !all.OK() {
		return KubeOverviewDto{}, KubeError{"kubectl get failed: " + obsTake(all.Output, 300)}
	}
	root, err := obsParseObject(all.Output)
	if err != nil {
		return KubeOverviewDto{}, KubeError{"kubectl get returned something that is not JSON"}
	}
	var items []*obsObject
	for _, it := range obsArr(root.Get("items")) {
		if o := obsObj(it); o != nil {
			items = append(items, o)
		}
	}
	of := func(kind string) []*obsObject {
		var out []*obsObject
		for _, it := range items {
			if kk, _ := obsText(it.Get("kind")); kk == kind {
				out = append(out, it)
			}
		}
		return out
	}

	dto := KubeOverviewDto{Available: true, Namespaces: []string{}, Nodes: []KubeNodeDto{}, Deployments: []KubeDeploymentDto{}, Pods: []KubePodDto{}, Services: []KubeServiceDto{}}
	for _, n := range of("Node") {
		status := obsObj(n.Get("status"))
		node := KubeNodeDto{Name: kubeObjName(n)}
		for _, c := range obsArr(status.Get("conditions")) {
			co := obsObj(c)
			if obsStrOr(co, "type", "") == "Ready" && obsStrOr(co, "status", "") == "True" {
				node.Ready = true
			}
		}
		node.Version = kubeStr(obsObj(status.Get("nodeInfo")), "kubeletVersion")
		node.CPU = kubeStr(obsObj(status.Get("capacity")), "cpu")
		node.Memory = kubeStr(obsObj(status.Get("capacity")), "memory")
		dto.Nodes = append(dto.Nodes, node)
	}
	if len(dto.Nodes) > 0 {
		dto.Version = dto.Nodes[0].Version
	}
	for _, ns := range of("Namespace") {
		dto.Namespaces = append(dto.Namespaces, kubeObjName(ns))
	}
	sort.Strings(dto.Namespaces)
	for _, d := range of("Deployment") {
		readyN, _ := obsInt32(obsObj(d.Get("status")).Get("readyReplicas"))
		desired := 1
		if n, ok := obsInt32(obsObj(d.Get("spec")).Get("replicas")); ok {
			desired = n
		}
		dto.Deployments = append(dto.Deployments, KubeDeploymentDto{kubeObjNamespace(d), kubeObjName(d), readyN, desired})
	}
	for _, p := range of("Pod") {
		dto.Pods = append(dto.Pods, kubePod(p))
	}
	for _, s := range of("Service") {
		dto.Services = append(dto.Services, kubeService(s))
	}
	return dto, nil
}

// Apply pipes a manifest to `kubectl apply -f -`.
func (k *Kube) Apply(ctx context.Context, manifest string) (OutputDto, error) {
	if strings.TrimSpace(manifest) == "" {
		return OutputDto{}, InvalidName{"The manifest is empty"}
	}
	if utf8.RuneCountInString(manifest) > KubeMaxManifest {
		return OutputDto{}, InvalidName{fmt.Sprintf("The manifest is larger than %d KB", KubeMaxManifest/1024)}
	}
	result := k.kubectl(ctx, 60, &manifest, "apply", "-f", "-")
	if !result.OK() {
		return OutputDto{}, KubeError{obsTake(result.Output, 800)}
	}
	return OutputDto{result.Output}, nil
}

func (k *Kube) Scale(ctx context.Context, namespace, deployment string, replicas int) (OutputDto, error) {
	if err := kubeValidate(namespace, deployment); err != nil {
		return OutputDto{}, err
	}
	if replicas < 0 || replicas > KubeMaxReplicas {
		return OutputDto{}, InvalidName{fmt.Sprintf("replicas must be between 0 and %d", KubeMaxReplicas)}
	}
	return k.run(ctx, "scale", "deployment/"+deployment, "-n", namespace, "--replicas="+strconv.Itoa(replicas))
}

// Delete removes a pod, deployment or service outside the system namespaces.
func (k *Kube) Delete(ctx context.Context, kind, namespace, name string) (OutputDto, error) {
	allowed := false
	for _, d := range kubeDeletable {
		allowed = allowed || d == kind
	}
	if !allowed {
		return OutputDto{}, InvalidName{"kind must be one of " + strings.Join(kubeDeletable, ", ")}
	}
	if err := kubeValidate(namespace, name); err != nil {
		return OutputDto{}, err
	}
	if kubeProtected[namespace] {
		return OutputDto{}, InvalidName{"Resources in " + namespace + " cannot be deleted from here"}
	}
	return k.run(ctx, "delete", kind, name, "-n", namespace)
}

// Logs returns the last tail lines (1..2000) of every container of a pod.
func (k *Kube) Logs(ctx context.Context, namespace, pod string, tail int) (OutputDto, error) {
	if err := kubeValidate(namespace, pod); err != nil {
		return OutputDto{}, err
	}
	result := k.kubectl(ctx, 30, nil, "logs", "--tail="+strconv.Itoa(obsClamp(tail, 1, 2000)), pod, "-n", namespace, "--all-containers=true")
	if !result.OK() {
		return OutputDto{}, KubeError{obsTake(result.Output, 500)}
	}
	return OutputDto{result.Output}, nil
}

func (k *Kube) Kubeconfig(ctx context.Context) (KubeconfigDto, error) {
	result := k.shell.Run(ctx, []string{"exec", k.container, "cat", "/etc/rancher/k3s/k3s.yaml"}, nil, 15)
	if !result.OK() {
		return KubeconfigDto{}, KubeError{"The k3s container is not running"}
	}
	return KubeconfigDto{"nimbo-k3s.kubeconfig.yaml", result.Output}, nil
}

func (k *Kube) run(ctx context.Context, args ...string) (OutputDto, error) {
	result := k.kubectl(ctx, 30, nil, args...)
	if !result.OK() {
		return OutputDto{}, KubeError{obsTake(result.Output, 500)}
	}
	return OutputDto{result.Output}, nil
}

func kubeValidate(namespace, name string) error {
	if !kubeNamespace.MatchString(namespace) {
		return InvalidName{"Invalid namespace"}
	}
	if !kubeName.MatchString(name) {
		return InvalidName{"Invalid name"}
	}
	return nil
}

// ---- JSON helpers ----

func kubeStr(o *obsObject, key string) *string {
	if s, ok := obsText(o.Get(key)); ok {
		return &s
	}
	return nil
}

func kubeMeta(o *obsObject) *obsObject { return obsObj(o.Get("metadata")) }
func kubeObjName(o *obsObject) string {
	if s := kubeStr(kubeMeta(o), "name"); s != nil {
		return *s
	}
	return ""
}
func kubeObjNamespace(o *obsObject) string {
	if s := kubeStr(kubeMeta(o), "namespace"); s != nil {
		return *s
	}
	return ""
}

func kubePod(p *obsObject) KubePodDto {
	status := obsObj(p.Get("status"))
	var containers []*obsObject
	for _, c := range obsArr(status.Get("containerStatuses")) {
		if o := obsObj(c); o != nil {
			containers = append(containers, o)
		}
	}
	total := len(containers)
	if total == 0 {
		total = len(obsArr(obsObj(p.Get("spec")).Get("containers")))
	}
	state := func(c *obsObject, which string) *string {
		return kubeStr(obsObj(obsObj(c.Get("state")).Get(which)), "reason")
	}
	var waiting, terminated *string
	for _, c := range containers {
		if waiting == nil {
			waiting = state(c, "waiting")
		}
		if terminated == nil {
			terminated = state(c, "terminated")
		}
	}
	// Between two restarts a crashing container is briefly "running"; kubectl shows that as Error, so do we.
	flapping := false
	readyCount, restarts := 0, 0
	for _, c := range containers {
		isReady := false
		if s := kubeStr(c, "ready"); s != nil && *s == "true" {
			isReady = true
			readyCount++
		}
		n, _ := obsInt32(c.Get("restartCount"))
		restarts += n
		if !isReady && n > 0 && obsObj(c.Get("lastState")).Get("terminated") != nil {
			flapping = true
		}
	}
	phase := "Unknown"
	if s := kubeStr(status, "phase"); s != nil {
		phase = *s
	}
	switch {
	case kubeMeta(p).Get("deletionTimestamp") != nil:
		phase = "Terminating"
	case waiting != nil:
		phase = *waiting
	case terminated != nil:
		phase = *terminated
	case flapping:
		phase = "Error"
	}
	return KubePodDto{
		Namespace: kubeObjNamespace(p), Name: kubeObjName(p), Status: phase,
		Ready: fmt.Sprintf("%d/%d", readyCount, total), Restarts: restarts,
		Node: kubeStr(obsObj(p.Get("spec")), "nodeName"), StartedAt: kubeStr(status, "startTime"),
	}
}

func kubeService(s *obsObject) KubeServiceDto {
	spec := obsObj(s.Get("spec"))
	dto := KubeServiceDto{Namespace: kubeObjNamespace(s), Name: kubeObjName(s), Type: "ClusterIP", ClusterIP: kubeStr(spec, "clusterIP"), Ports: []string{}, NodePorts: []int{}}
	if t := kubeStr(spec, "type"); t != nil {
		dto.Type = *t
	}
	for _, item := range obsArr(spec.Get("ports")) {
		p := obsObj(item)
		port := "null"
		if s := kubeStr(p, "port"); s != nil {
			port = *s
		}
		proto := "TCP"
		if s := kubeStr(p, "protocol"); s != nil {
			proto = *s
		}
		label := port
		if node, ok := obsInt32(p.Get("nodePort")); ok {
			label += ":" + strconv.Itoa(node)
			dto.NodePorts = append(dto.NodePorts, node)
		}
		dto.Ports = append(dto.Ports, label+"/"+proto)
	}
	return dto
}

// KubeSamples are the manifests the UI offers to apply.
var KubeSamples = []KubeSampleDto{
	{
		"hello", "Hello (nginx)", "Two nginx replicas behind a NodePort service, reachable at http://localhost:30080.",
		`apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello
  labels: {app: hello}
spec:
  replicas: 2
  selector:
    matchLabels: {app: hello}
  template:
    metadata:
      labels: {app: hello}
    spec:
      containers:
        - name: web
          image: nginx:alpine
          ports:
            - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: hello
spec:
  type: NodePort
  selector: {app: hello}
  ports:
    - port: 80
      targetPort: 80
      nodePort: 30080
`,
	},
	{
		"crashloop", "Crash loop", "A container that always exits: watch the status go to CrashLoopBackOff and read its logs.",
		`apiVersion: apps/v1
kind: Deployment
metadata:
  name: crashy
spec:
  replicas: 1
  selector:
    matchLabels: {app: crashy}
  template:
    metadata:
      labels: {app: crashy}
    spec:
      containers:
        - name: boom
          image: busybox:stable
          command: ["sh", "-c", "echo starting; echo 'fatal: config missing' >&2; exit 1"]
`,
	},
}
