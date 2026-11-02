package nimbo

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCatalogIdsAndPorts(t *testing.T) {
	ids, ports := map[string]bool{}, map[int]bool{}
	for _, d := range CatalogProviders {
		if ids[d.ID] {
			t.Errorf("duplicate id %s", d.ID)
		}
		ids[d.ID] = true
		for _, p := range d.Ports {
			if ports[p] {
				t.Errorf("two providers publish host port %d", p)
			}
			ports[p] = true
		}
	}
}

func TestCatalogSecretsByNameOnly(t *testing.T) {
	for _, d := range CatalogProviders {
		for _, kv := range d.SecretEnv {
			coreTrue(t, strings.HasPrefix(kv.Value, "NIMBO_"), d.ID, kv.Value)
		}
		for _, kv := range d.ContainerEnv {
			coreTrue(t, !strings.Contains(strings.ToLower(kv.Value), "password"), d.ID)
		}
	}
	coreEq(t, "cgr.dev/chainguard/minio", CatalogFind("minio").Image)
}

func TestListsEveryProviderWithLiveState(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: true, Status: "Up 2 minutes"}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt})
	r := coreDo(h, "GET", "/api/providers", "")
	for _, d := range CatalogProviders {
		coreContains(t, r.Body, `"id":"`+d.ID+`"`)
	}
	coreContains(t, r.Body, `"state":"running"`)
	coreContains(t, r.Body, `"state":"not_created"`)
}

func TestStartThenStop(t *testing.T) {
	rt := newCoreFakeRuntime()
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt})
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/redis/start", "").Code)
	coreTrue(t, rt.live["redis"].Running)
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/redis/stop", "").Code)
	coreTrue(t, !rt.live["redis"].Running)
}

func TestStartFailureIs409(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.failStart = "port is already allocated"
	r := coreDo(coreTestServer(DefaultConfig(), Deps{Runtime: rt}), "POST", "/api/providers/redis/start", "")
	coreEq(t, 409, r.Code)
	coreContains(t, r.Body, "port is already allocated")
}

func TestUnknownProviderIs404AndNothingReachesDocker(t *testing.T) {
	rt := newCoreFakeRuntime()
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt})
	coreEq(t, 404, coreDo(h, "POST", "/api/providers/..%2Fevil/start", "").Code)
	coreEq(t, 404, coreDo(h, "GET", "/api/providers/nope/logs", "").Code)
	coreEq(t, 0, len(rt.calls))
}

func TestEnvEndpointRendersDotenv(t *testing.T) {
	body := coreDo(coreTestServer(DefaultConfig(), Deps{}), "GET", "/api/providers/floci-aws/env", "").Body
	coreContains(t, body, "AWS_ENDPOINT_URL")
	coreContains(t, body, "http://localhost:4566")
	coreContains(t, body, `"dotenv":"AWS_ENDPOINT_URL=http://localhost:4566\nAWS_REGION`)
}

func TestDockerDownYieldsEmptyLiveState(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.docker = DockerStatusDto{Available: false, Error: coreStr("down")}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt})
	coreEq(t, 200, coreDo(h, "GET", "/api/providers", "").Code)
	coreContains(t, coreDo(h, "GET", "/api/docker", "").Body, `"available":false`)
}

func TestTokenProtectsTheApiButNotHealth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIToken = "s3cret"
	h := coreTestServer(cfg, Deps{})
	health := coreDo(h, "GET", "/api/health", "")
	coreEq(t, 200, health.Code)
	coreContains(t, health.Body, `"authRequired":true`)
	coreEq(t, 401, coreDo(h, "GET", "/api/providers", "").Code)
	coreEq(t, 401, coreDo(h, "POST", "/api/providers/redis/start", "").Code)
	coreEq(t, 401, coreDo(h, "GET", "/api/nothing-here", "").Code)
	coreEq(t, 401, coreDo(h, "GET", "/api/providers", "", "Authorization", "Bearer nope").Code)
	coreEq(t, 200, coreDo(h, "GET", "/api/providers", "", "Authorization", "Bearer s3cret").Code)
}

func TestLoopbackHostsAreServed(t *testing.T) {
	h := coreTestServer(DefaultConfig(), Deps{})
	for _, host := range []string{"localhost", "localhost:8080", "127.0.0.1:8766", "LOCALHOST"} {
		coreEq(t, 200, coreDo(h, "GET", "/api/health", "", "Host", host).Code, host)
	}
}

func TestOtherHostsAreRefusedForApiAndUi(t *testing.T) {
	h := coreTestServer(DefaultConfig(), Deps{UI: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	for _, host := range []string{"evil.example", "evil.example:8080", "localhost.evil.example", "127.0.0.1.evil.example", "10.0.0.5:8080", "0.0.0.0:8080"} {
		for _, p := range []string{"/api/health", "/api/providers", "/"} {
			coreEq(t, 403, coreDo(h, "GET", p, "", "Host", host).Code, host, p)
		}
	}
}

func TestExtraHostsCanBeAllowed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowedHosts = []string{"nimbo.lan"}
	h := coreTestServer(cfg, Deps{})
	coreEq(t, 200, coreDo(h, "GET", "/api/health", "", "Host", "Nimbo.lan:443").Code)
	coreEq(t, 403, coreDo(h, "GET", "/api/health", "", "Host", "other.lan").Code)
}

func TestAllowedHostsAndConfigFromEnv(t *testing.T) {
	c := ConfigFromEnv(map[string]string{"NIMBO_ALLOWED_HOSTS": " Nimbo.lan , ,box.local,nimbo.lan"}, "/home/x")
	coreEq(t, "nimbo.lan,box.local", strings.Join(c.AllowedHosts, ","))
	coreEq(t, 0, len(ConfigFromEnv(map[string]string{}, "/h").AllowedHosts))
}

func TestConfigDefaultsAndOverrides(t *testing.T) {
	c := ConfigFromEnv(map[string]string{}, "/h")
	coreEq(t, "127.0.0.1", c.Host)
	coreEq(t, "localhost", c.EmulatorHost)
	coreEq(t, 8080, c.Port)
	coreEq(t, "desktop", c.Distribution)
	c = ConfigFromEnv(map[string]string{"NIMBO_HOST": "0.0.0.0", "NIMBO_EMULATOR_HOST": "host.docker.internal", "NIMBO_PORT": "9000", "NIMBO_DISTRIBUTION": "docker", "NIMBO_API_TOKEN": "t"}, "/h")
	coreEq(t, "0.0.0.0", c.Host)
	coreEq(t, "host.docker.internal", c.EmulatorHost)
	coreEq(t, 9000, c.Port)
	coreEq(t, "docker", c.Distribution)
	coreEq(t, "t", c.APIToken)
	c = ConfigFromEnv(map[string]string{"NIMBO_HOST": "  ", "NIMBO_EMULATOR_HOST": "", "NIMBO_DISTRIBUTION": "weird", "NIMBO_PORT": "x"}, "/h")
	coreEq(t, "127.0.0.1", c.Host)
	coreEq(t, "localhost", c.EmulatorHost)
	coreEq(t, "desktop", c.Distribution)
	coreEq(t, 8080, c.Port)
}

func TestHealthVersion(t *testing.T) {
	cfg := DefaultConfig()
	coreContains(t, coreDo(coreTestServer(cfg, Deps{}), "GET", "/api/health", "").Body, `"version":null`)
	cfg.Version = "9.9.9"
	coreContains(t, coreDo(coreTestServer(cfg, Deps{}), "GET", "/api/health", "").Body, `"version":"9.9.9"`)
}

func TestBundledUI(t *testing.T) {
	ui := fstest.MapFS{"index.html": {Data: []byte("<title>fixture ui</title>")}, "assets/app.js": {Data: []byte("1")}}
	h := coreTestServer(DefaultConfig(), Deps{UI: ui})
	r := coreDo(h, "GET", "/", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, "fixture ui")
	coreContains(t, coreDo(h, "GET", "/index.html", "").Body, "fixture ui")
	coreContains(t, coreDo(h, "GET", "/some/route", "").Body, "fixture ui")
	coreEq(t, 200, coreDo(h, "GET", "/assets/app.js", "").Code)
	coreEq(t, 404, coreDo(h, "GET", "/assets/missing.js", "").Code)
	coreEq(t, 404, coreDo(h, "GET", "/api/unknown", "").Code)
}

func TestWithoutUITheRootIs404(t *testing.T) {
	h := coreTestServer(DefaultConfig(), Deps{})
	coreEq(t, 404, coreDo(h, "GET", "/", "").Code)
	coreEq(t, 200, coreDo(h, "GET", "/api/health", "").Code)
}

func TestExtraRoutesAreMountedBehindTheGuards(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIToken = "tok"
	h := coreTestServer(cfg, Deps{Routes: []func(*http.ServeMux){func(m *http.ServeMux) {
		m.HandleFunc("GET /api/extra", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	}}})
	coreEq(t, 401, coreDo(h, "GET", "/api/extra", "").Code)
	coreEq(t, 204, coreDo(h, "GET", "/api/extra", "", "Authorization", "Bearer tok").Code)
	coreEq(t, 403, coreDo(h, "GET", "/api/extra", "", "Authorization", "Bearer tok", "Host", "evil.example").Code)
}

func TestSamplerCountsUptime(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: true}
	data := NewAppData(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunStatsSampler(ctx, rt, data.Stats, time.Second); close(done) }()
	deadline := time.Now().Add(4 * time.Second)
	for data.Stats.Stats().Providers["redis"].UpSeconds == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	coreTrue(t, data.Stats.Stats().Providers["redis"].UpSeconds >= 1, "sampler added uptime")
	<-done
}
