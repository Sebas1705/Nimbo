package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// obsToxiproxy is just enough of Toxiproxy's HTTP API to exercise Chaos; it keeps state and records every call.
type obsToxiproxy struct {
	proxies     map[string]*obsProxy
	order       []string
	calls       []string
	unreachable bool
}

type obsProxy struct {
	name, listen, upstream string
	enabled                bool
	toxics                 []map[string]any
}

func newObsToxiproxy() *obsToxiproxy { return &obsToxiproxy{proxies: map[string]*obsProxy{}} }

func (f *obsToxiproxy) Send(_ context.Context, method, url string, _ map[string]string, body *string) (HTTPReply, error) {
	if f.unreachable {
		return HTTPReply{}, EmulatorUnreachable{"down"}
	}
	path := strings.TrimPrefix(url, "http://localhost:8474")
	f.calls = append(f.calls, method+" "+path)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var payload map[string]any
	if body != nil {
		if err := json.Unmarshal([]byte(*body), &payload); err != nil {
			return HTTPReply{400, "bad json"}, nil
		}
	}
	switch {
	case method == "GET" && path == "/proxies":
		out := map[string]any{}
		for _, name := range f.order {
			p := f.proxies[name]
			toxics := []any{}
			for _, t := range p.toxics {
				toxics = append(toxics, t)
			}
			out[name] = map[string]any{"name": p.name, "listen": p.listen, "upstream": p.upstream, "enabled": p.enabled, "toxics": toxics}
		}
		b, _ := json.Marshal(out)
		return HTTPReply{200, string(b)}, nil
	case method == "POST" && path == "/reset":
		for _, p := range f.proxies {
			p.enabled, p.toxics = true, nil
		}
		return HTTPReply{204, ""}, nil
	case method == "POST" && path == "/proxies":
		name := payload["name"].(string)
		f.proxies[name] = &obsProxy{name, payload["listen"].(string), payload["upstream"].(string), true, nil}
		f.order = append(f.order, name)
		return HTTPReply{201, "{}"}, nil
	case method == "POST" && len(parts) == 2:
		p := f.proxies[parts[1]]
		if p == nil {
			return HTTPReply{404, "no proxy"}, nil
		}
		if e, ok := payload["enabled"].(bool); ok {
			p.enabled = e
		}
		return HTTPReply{200, "{}"}, nil
	case method == "POST" && len(parts) == 3 && parts[2] == "toxics":
		p := f.proxies[parts[1]]
		if p == nil {
			return HTTPReply{404, "no proxy"}, nil
		}
		p.toxics = append(p.toxics, payload)
		return HTTPReply{200, *body}, nil
	case method == "DELETE" && len(parts) == 4:
		p := f.proxies[parts[1]]
		if p == nil {
			return HTTPReply{404, "no proxy"}, nil
		}
		before := len(p.toxics)
		p.toxics = slices.DeleteFunc(p.toxics, func(t map[string]any) bool { return t["name"] == parts[3] })
		if len(p.toxics) == before {
			return HTTPReply{404, "no toxic"}, nil
		}
		return HTTPReply{204, ""}, nil
	}
	return HTTPReply{400, "unsupported " + method + " " + path}, nil
}

func (f *obsToxiproxy) writes() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET") {
			out = append(out, c)
		}
	}
	return out
}

func (f *obsToxiproxy) toxic(proxy string, i int) map[string]any { return f.proxies[proxy].toxics[i] }

func obsNewChaos(f *obsToxiproxy) *Chaos { return NewChaos(f, "") }

func obsMust[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func obsToxicNames(r ChaosTargetDto) []string {
	names := []string{}
	for _, t := range r.Toxics {
		names = append(names, t.Name)
	}
	return names
}

func TestChaosViewWithoutToxiproxyReportsUnavailableButStillListsTargetsAndPresets(t *testing.T) {
	f := newObsToxiproxy()
	f.unreachable = true
	view := obsMust[ChaosDto](t)(obsNewChaos(f).View(context.Background()))
	if view.Available {
		t.Fatal("should be unavailable")
	}
	if len(view.Targets) != len(ChaosTargets) {
		t.Fatalf("targets: %d", len(view.Targets))
	}
	for i, tg := range view.Targets {
		if tg.ID != ChaosTargets[i].ID || tg.Active || !tg.Enabled || tg.Toxics == nil {
			t.Fatalf("target %d: %+v", i, tg)
		}
	}
	ids := []string{}
	for _, p := range view.Presets {
		ids = append(ids, p.ID)
	}
	for _, want := range []string{"latency", "bandwidth", "timeout", "reset", "slow_close", "limit_data", "outage"} {
		if !slices.Contains(ids, want) {
			t.Errorf("missing preset %s in %v", want, ids)
		}
	}
}

func TestChaosFirstUseCreatesTheProxyTowardsTheHostAndAddsADownstreamToxicWithDefaults(t *testing.T) {
	f := newObsToxiproxy()
	result := obsMust[ChaosTargetDto](t)(obsNewChaos(f).Apply(context.Background(), "floci-aws", "latency", nil))
	p := f.proxies["floci-aws"]
	if p.listen != "0.0.0.0:14566" || p.upstream != "host.docker.internal:4566" {
		t.Fatalf("proxy: %+v", p)
	}
	if len(p.toxics) != 1 {
		t.Fatalf("toxics: %v", p.toxics)
	}
	toxic := p.toxics[0]
	attrs := toxic["attributes"].(map[string]any)
	if toxic["type"] != "latency" || toxic["stream"] != "downstream" || toxic["toxicity"] != 1.0 || attrs["latency"] != 500.0 || attrs["jitter"] != 100.0 {
		t.Fatalf("toxic: %v", toxic)
	}
	if _, has := attrs["toxicity"]; has {
		t.Fatal("toxicity is not an attribute")
	}
	if !result.Active || !result.Enabled || result.Endpoint != "http://localhost:14566" || len(result.Toxics) != 1 || result.Toxics[0].Name != "latency" {
		t.Fatalf("result: %+v", result)
	}
}

func TestChaosCustomValuesAndToxicityPercentageAreSent(t *testing.T) {
	f := newObsToxiproxy()
	obsMust[ChaosTargetDto](t)(obsNewChaos(f).Apply(context.Background(), "redis", "latency", map[string]float64{"latency": 1200, "jitter": 0, "toxicity": 30}))
	toxic := f.toxic("redis", 0)
	attrs := toxic["attributes"].(map[string]any)
	if attrs["latency"] != 1200.0 || toxic["toxicity"] != 0.3 || f.proxies["redis"].upstream != "host.docker.internal:6379" {
		t.Fatalf("toxic: %v upstream %s", toxic, f.proxies["redis"].upstream)
	}
}

func TestChaosApplyingTheSamePresetTwiceReplacesTheToxicInsteadOfStackingIt(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "minio", "latency", map[string]float64{"latency": 100}))
	result := obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "minio", "latency", map[string]float64{"latency": 900}))
	if len(f.proxies["minio"].toxics) != 1 {
		t.Fatalf("toxics: %v", f.proxies["minio"].toxics)
	}
	if v, _ := result.Toxics[0].AttributeFloat("latency"); v != 900 {
		t.Fatalf("attributes: %v", result.Toxics[0].Attributes)
	}
}

func TestChaosDifferentPresetsStack(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "postgres", "latency", nil))
	result := obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "postgres", "bandwidth", map[string]float64{"rate": 5}))
	if got := obsToxicNames(result); !slices.Equal(got, []string{"latency", "bandwidth"}) {
		t.Fatalf("toxics: %v", got)
	}
	if f.toxic("postgres", 1)["type"] != "bandwidth" {
		t.Fatalf("toxic: %v", f.toxic("postgres", 1))
	}
}

func TestChaosResetPresetMapsToResetPeer(t *testing.T) {
	f := newObsToxiproxy()
	obsMust[ChaosTargetDto](t)(obsNewChaos(f).Apply(context.Background(), "floci-gcp", "reset", nil))
	if f.toxic("floci-gcp", 0)["type"] != "reset_peer" {
		t.Fatalf("toxic: %v", f.toxic("floci-gcp", 0))
	}
}

func TestChaosOutageDisablesTheProxyWithoutAToxicAndClearBringsItBack(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	down := obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "floci-aws", "outage", nil))
	if down.Enabled || len(f.proxies["floci-aws"].toxics) != 0 {
		t.Fatalf("down: %+v", down)
	}
	back := obsMust[ChaosTargetDto](t)(c.Clear(context.Background(), "floci-aws"))
	if !back.Enabled {
		t.Fatal("clear must re-enable")
	}
}

func TestChaosAToxicPresetReEnablesAProxyThatWasInOutage(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "floci-aws", "outage", nil))
	result := obsMust[ChaosTargetDto](t)(c.Apply(context.Background(), "floci-aws", "latency", nil))
	if !result.Enabled {
		t.Fatal("expected enabled")
	}
}

func TestChaosClearRemovesEveryToxicAndIsANoOpForATargetThatWasNeverProxied(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	ctx := context.Background()
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "redis", "latency", nil))
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "redis", "bandwidth", nil))
	cleared := obsMust[ChaosTargetDto](t)(c.Clear(ctx, "redis"))
	if len(cleared.Toxics) != 0 || !cleared.Enabled {
		t.Fatalf("cleared: %+v", cleared)
	}

	before := len(f.writes())
	untouched := obsMust[ChaosTargetDto](t)(c.Clear(ctx, "floci-oci"))
	if untouched.Active || len(f.writes()) != before {
		t.Fatalf("no proxy must be created just to clear it: %v", f.writes())
	}
}

func TestChaosRemovingOneToxicLeavesTheOthers(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	ctx := context.Background()
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "redis", "latency", nil))
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "redis", "bandwidth", nil))
	result := obsMust[ChaosTargetDto](t)(c.RemoveToxic(ctx, "redis", "latency"))
	if got := obsToxicNames(result); !slices.Equal(got, []string{"bandwidth"}) {
		t.Fatalf("toxics: %v", got)
	}
	var ip InvalidParam
	if _, err := c.RemoveToxic(ctx, "redis", "../x"); !errors.As(err, &ip) {
		t.Fatalf("bad name: %v", err)
	}
	var ee EmulatorError
	if _, err := c.RemoveToxic(ctx, "redis", "ghost"); !errors.As(err, &ee) {
		t.Fatalf("ghost: %v", err)
	}
	var ut UnknownTarget
	if _, err := c.RemoveToxic(ctx, "ghost", "latency"); !errors.As(err, &ut) {
		t.Fatalf("ghost target: %v", err)
	}
}

func TestChaosOutOfRangeOrNonNumericValuesAreRejectedBeforeAnyWrite(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	ctx := context.Background()
	invalid := []struct {
		preset string
		params map[string]float64
	}{
		{"latency", map[string]float64{"latency": -1}},
		{"latency", map[string]float64{"latency": 99_999_999}},
		{"latency", map[string]float64{"toxicity": 0}},
		{"latency", map[string]float64{"toxicity": 101}},
		{"latency", map[string]float64{"latency": math.NaN()}},
		{"nope", nil},
	}
	for _, in := range invalid {
		var ip InvalidParam
		if _, err := c.Apply(ctx, "redis", in.preset, in.params); !errors.As(err, &ip) {
			t.Errorf("%s %v: got %v", in.preset, in.params, err)
		}
	}
	var ut UnknownTarget
	if _, err := c.Apply(ctx, "../admin", "latency", nil); !errors.As(err, &ut) {
		t.Errorf("unknown target: %v", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("writes: %v", w)
	}
}

func TestChaosRangeErrorsNameTheParameterAndItsBounds(t *testing.T) {
	_, err := obsNewChaos(newObsToxiproxy()).Apply(context.Background(), "redis", "latency", map[string]float64{"latency": 31_000})
	if err == nil || err.Error() != "latency must be between 0 and 30000" {
		t.Fatalf("got %v", err)
	}
}

func TestChaosResetAllClearsEverything(t *testing.T) {
	f := newObsToxiproxy()
	c := obsNewChaos(f)
	ctx := context.Background()
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "redis", "latency", nil))
	obsMust[ChaosTargetDto](t)(c.Apply(ctx, "minio", "outage", nil))
	view := obsMust[ChaosDto](t)(c.ResetAll(ctx))
	for _, tg := range view.Targets {
		if tg.Active && (!tg.Enabled || len(tg.Toxics) != 0) {
			t.Errorf("not reset: %+v", tg)
		}
	}
	if !slices.Contains(f.calls, "POST /reset") {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestChaosProxyPortsAreUniqueAndNeverCollideWithEmulatorPorts(t *testing.T) {
	proxy, upstream := map[int]bool{}, map[int]bool{}
	for _, tg := range ChaosTargets {
		if proxy[tg.ProxyPort] {
			t.Errorf("duplicate proxy port %d", tg.ProxyPort)
		}
		proxy[tg.ProxyPort] = true
		upstream[tg.UpstreamPort] = true
		if tg.ProxyPort != tg.UpstreamPort+10000 {
			t.Errorf("%s: proxy port is not the normal one plus 10000", tg.ID)
		}
	}
	for p := range proxy {
		if upstream[p] {
			t.Errorf("proxy port %d is also an emulator port", p)
		}
	}
	if proxy[ChaosAPIPort] {
		t.Error("the API port is a proxy port")
	}
}

func TestChaosTargetsAreFoundByID(t *testing.T) {
	if tg, ok := FindChaosTarget("floci-aws"); !ok || tg.ProxyPort != 14566 {
		t.Fatalf("%+v %v", tg, ok)
	}
	if _, ok := FindChaosTarget("../admin"); ok {
		t.Fatal("found a made-up target")
	}
}

func TestChaosViewSerializesPresetParamsWithToxicity(t *testing.T) {
	view := obsMust[ChaosDto](t)(obsNewChaos(newObsToxiproxy()).View(context.Background()))
	out, _ := json.Marshal(view)
	var got struct {
		Presets []struct {
			ID     string
			Params []map[string]any
		}
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Presets {
		last := ""
		if n := len(p.Params); n > 0 {
			last, _ = p.Params[n-1]["key"].(string)
		}
		if p.ID == "outage" && len(p.Params) != 0 {
			t.Errorf("outage has params: %v", p.Params)
		}
		if p.ID != "outage" && last != "toxicity" {
			t.Errorf("%s: last param %q", p.ID, last)
		}
	}
	if !strings.Contains(string(out), `"default":500`) {
		t.Fatalf("param defaults missing: %s", out)
	}
}

// ---- HTTP API ----

type obsActivity struct{ entries []string }

func (a *obsActivity) Record(kind, subject, detail string) {
	a.entries = append(a.entries, kind+"|"+subject+"|"+detail)
}

func TestChaosAPIGetReturnsAvailabilityPresetsAndTargets(t *testing.T) {
	rec := obsServe(obsNewChaos(newObsToxiproxy()).Register, "GET", "/api/chaos", "")
	var body struct {
		Available bool
		Presets   []any
		Targets   []any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Available || len(body.Presets) < 7 || len(body.Targets) != len(ChaosTargets) {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
}

func TestChaosAPIGetStillAnswers200WhenToxiproxyIsDown(t *testing.T) {
	f := newObsToxiproxy()
	f.unreachable = true
	rec := obsServe(obsNewChaos(f).Register, "GET", "/api/chaos", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestChaosAPIApplyWithAJSONBodyThenDelete(t *testing.T) {
	f := newObsToxiproxy()
	reg := obsNewChaos(f).Register
	applied := obsServe(reg, "POST", "/api/chaos/targets/redis/latency", `{"params":{"latency":250,"jitter":10}}`)
	if applied.Code != 200 || !strings.Contains(applied.Body.String(), `"name":"latency"`) {
		t.Fatalf("%d %s", applied.Code, applied.Body)
	}
	if f.toxic("redis", 0)["attributes"].(map[string]any)["latency"] != 250.0 {
		t.Fatalf("toxic: %v", f.toxic("redis", 0))
	}
	if rec := obsServe(reg, "DELETE", "/api/chaos/targets/redis/toxics/latency", ""); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if len(f.proxies["redis"].toxics) != 0 {
		t.Fatal("toxic not removed")
	}
}

func TestChaosAPIApplyWithoutABodyUsesDefaults(t *testing.T) {
	f := newObsToxiproxy()
	if rec := obsServe(obsNewChaos(f).Register, "POST", "/api/chaos/targets/redis/outage", ""); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.proxies["redis"].enabled {
		t.Fatal("outage must disable the proxy")
	}
}

func TestChaosAPIAnUnreadableBodyAlsoMeansDefaults(t *testing.T) {
	f := newObsToxiproxy()
	if rec := obsServe(obsNewChaos(f).Register, "POST", "/api/chaos/targets/redis/latency", `{not json`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.toxic("redis", 0)["attributes"].(map[string]any)["latency"] != 500.0 {
		t.Fatalf("toxic: %v", f.toxic("redis", 0))
	}
}

func TestChaosAPIBadInputIs400AndUnknownTargetIs404(t *testing.T) {
	reg := obsNewChaos(newObsToxiproxy()).Register
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/chaos/targets/redis/nope", "", http.StatusBadRequest},
		{"POST", "/api/chaos/targets/redis/latency", `{"params":{"latency":9999999}}`, http.StatusBadRequest},
		{"POST", "/api/chaos/targets/ghost/latency", "", http.StatusNotFound},
		{"DELETE", "/api/chaos/targets/ghost/toxics", "", http.StatusNotFound},
	}
	for _, c := range cases {
		if rec := obsServe(reg, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestChaosAPIWritingWhileToxiproxyIsDownIsA503(t *testing.T) {
	f := newObsToxiproxy()
	f.unreachable = true
	reg := obsNewChaos(f).Register
	for _, path := range []string{"/api/chaos/targets/redis/latency", "/api/chaos/reset"} {
		if rec := obsServe(reg, "POST", path, ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestChaosAPIRecordsWhatThePersonDid(t *testing.T) {
	log := &obsActivity{}
	c := obsNewChaos(newObsToxiproxy())
	c.SetActivity(log)
	reg := c.Register
	obsServe(reg, "POST", "/api/chaos/targets/redis/latency", "")
	obsServe(reg, "DELETE", "/api/chaos/targets/redis/toxics", "")
	obsServe(reg, "POST", "/api/chaos/reset", "")
	obsServe(reg, "POST", "/api/chaos/targets/redis/nope", "") // failures are not recorded
	want := []string{"chaos.apply|redis|latency", "chaos.clear|redis|", "chaos.reset|all|"}
	if !slices.Equal(log.entries, want) {
		t.Fatalf("entries: %v", log.entries)
	}
}
