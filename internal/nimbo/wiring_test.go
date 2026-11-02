package nimbo

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The catalog (core) and the chaos area each keep their own table of proxied ports so they do not share a type. They
// must say the same thing, or the exports and the Toxiproxy ports would disagree.
func TestChaosTargetTablesAgree(t *testing.T) {
	coreEq(t, len(ChaosTargets), len(coreChaosTargets))
	for i, want := range ChaosTargets {
		got := coreChaosTargets[i]
		coreEq(t, want.ID, got.id)
		coreEq(t, want.UpstreamPort, got.upstreamPort, want.ID)
		coreEq(t, want.ProxyPort, got.proxyPort, want.ID)
	}
}

func TestEveryChaosTargetPointsAtAPublishedEmulatorPort(t *testing.T) {
	published := map[int]bool{}
	for _, def := range CatalogProviders {
		for _, p := range def.Ports {
			published[p] = true
		}
	}
	for _, target := range ChaosTargets {
		coreTrue(t, published[target.UpstreamPort], target.ID+": no provider publishes port "+itoaForTest(target.UpstreamPort))
		coreEq(t, target.UpstreamPort+10000, target.ProxyPort, target.ID+": proxy ports are the normal one plus 10000")
	}
}

func TestToxiproxyPublishesEveryProxyPort(t *testing.T) {
	ports := map[int]bool{}
	for _, p := range CatalogFind("toxiproxy").Ports {
		ports[p] = true
	}
	coreTrue(t, ports[ChaosAPIPort], "its own API")
	for _, target := range ChaosTargets {
		coreTrue(t, ports[target.ProxyPort], target.ID+" proxy port is not published")
	}
}

func TestChaosExportRewritesThePortsOfTheCatalog(t *testing.T) {
	for _, target := range ChaosTargets {
		in := "http://localhost:" + itoaForTest(target.UpstreamPort)
		coreEq(t, "http://localhost:"+itoaForTest(target.ProxyPort), ExportViaChaos(in), target.ID)
	}
	coreEq(t, "redpanda:9092", ExportViaChaos("redpanda:9092"), "an emulator without a proxy is left alone")
}

func TestK3sRunsTheWayKubernetesNeeds(t *testing.T) {
	k3s := CatalogFind("k3s")
	coreTrue(t, k3s != nil)
	ports := map[int]bool{}
	for _, p := range k3s.Ports {
		ports[p] = true
	}
	coreTrue(t, ports[6443], "the API server")
	for p := 30080; p <= 30084; p++ {
		coreTrue(t, ports[p], "NodePort "+itoaForTest(p))
	}
	args := strings.Join(BuildRunArgs(*k3s, map[string]string{}, false), " ")
	coreContains(t, args, "--privileged")
	coreContains(t, args, "--tmpfs /run")
	coreContains(t, args, "K3S_KUBECONFIG_MODE=644")
	coreContains(t, args, "server --disable=traefik")
	coreEq(t, KubeContainer, ContainerName(*k3s), "the Kubernetes screen talks to the container Nimbo starts")
	coreEq(t, "/var/lib/rancher/k3s", k3s.Persist.MountPath)
}

// Every route of every area is behind the API token when there is one, and only the health check is not.
func TestTheAPITokenProtectsEveryArea(t *testing.T) {
	dir := t.TempDir()
	h, closer := Assemble(Config{APIToken: "test-token", HostEnv: map[string]string{}, DataDir: dir, Distribution: "binary"},
		AssembleOptions{Env: map[string]string{}, Home: dir})
	defer closer.Close()

	routes := []string{
		"/api/providers", "/api/stacks", "/api/docker", "/api/settings", "/api/stats", "/api/data", "/api/data/export", "/api/progress",
		"/api/secrets", "/api/variables", "/api/updates", "/api/images", "/api/inspect/aws/s3/buckets", "/api/chaos", "/api/traces/services",
		"/api/k8s", "/api/fidelity", "/api/labs",
	}
	for _, path := range routes {
		coreEq(t, 401, coreDo(h, "GET", path, "").Code, path+" without the token")
		coreEq(t, 401, coreDo(h, "GET", path, "", "Authorization", "Bearer wrong").Code, path+" with a wrong token")
	}
	for _, c := range [][2]string{{"POST", "/api/providers/redis/start"}, {"POST", "/api/secrets/NIMBO_REDIS_PASSWORD/reveal"}, {"DELETE", "/api/providers/redis/data?confirm=yes"}, {"POST", "/api/updates/check"}} {
		coreEq(t, 401, coreDo(h, c[0], c[1], "").Code, c[0]+" "+c[1])
	}
	coreEq(t, 200, coreDo(h, "GET", "/api/health", "").Code, "the health check stays open")
	for _, path := range []string{"/api/settings", "/api/labs", "/api/variables", "/api/updates"} {
		code := coreDo(h, "GET", path, "", "Authorization", "Bearer test-token").Code
		coreTrue(t, code == http.StatusOK, path+" with the token: "+itoaForTest(code))
	}
}

// Nothing the real wiring writes goes outside the data folder it was given.
func TestAssembleKeepsItsFilesInTheDataFolder(t *testing.T) {
	dir := t.TempDir()
	h, closer := Assemble(Config{HostEnv: map[string]string{}, DataDir: dir}, AssembleOptions{Env: map[string]string{}, Home: dir})
	defer closer.Close()
	coreEq(t, 200, coreDo(h, "PUT", "/api/settings", `{"theme":"dark"}`).Code)
	coreEq(t, 200, coreDo(h, "PUT", "/api/stacks/mine", `{"providers":["redis"]}`).Code)
	entries, err := os.ReadDir(dir)
	coreTrue(t, err == nil)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	coreTrue(t, names[FileSettings] && names[FileCustomStacks], "settings and stacks are files in the data folder")
	if _, err := os.Stat(filepath.Join(dir, FileSecrets)); err == nil {
		t.Error("nothing wrote secrets: the file must not exist yet")
	}
}
