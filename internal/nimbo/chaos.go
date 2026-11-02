package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// ChaosAPIPort is where the Toxiproxy control API listens.
const ChaosAPIPort = 8474

// ChaosTarget is an emulator endpoint that can be reached through Toxiproxy on ProxyPort instead of UpstreamPort.
type ChaosTarget struct {
	ID           string
	Name         string
	UpstreamPort int
	ProxyPort    int
}

// ChaosTargets lists every proxiable endpoint. Proxy ports are the normal ones plus 10000.
var ChaosTargets = []ChaosTarget{
	{"floci-aws", "Floci AWS", 4566, 14566},
	{"floci-azure", "Floci Azure", 4577, 14577},
	{"floci-gcp", "Floci GCP", 4588, 14588},
	{"floci-oci", "Floci OCI", 4599, 14599},
	{"azurite-blob", "Azurite Blob", 10000, 20000},
	{"azurite-queue", "Azurite Queue", 10001, 20001},
	{"azurite-table", "Azurite Table", 10002, 20002},
	{"minio", "MinIO", 9000, 19000},
	{"postgres", "PostgreSQL", 5432, 15432},
	{"redis", "Redis", 6379, 16379},
}

// FindChaosTarget returns the target with that id.
func FindChaosTarget(id string) (ChaosTarget, bool) {
	for _, t := range ChaosTargets {
		if t.ID == id {
			return t, true
		}
	}
	return ChaosTarget{}, false
}

type ParamDto struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Unit    string  `json:"unit"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
}
type PresetDto struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Params      []ParamDto `json:"params"`
}
type ToxicDto struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Toxicity float64 `json:"toxicity"`
	// Attributes are Toxiproxy's own; numbers are float64.
	Attributes map[string]any `json:"attributes"`
}

// AttributeFloat reads a numeric attribute the way the lab checks do.
func (t ToxicDto) AttributeFloat(key string) (float64, bool) {
	switch v := t.Attributes[key].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

type ChaosTargetDto struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	UpstreamPort int    `json:"upstreamPort"`
	ProxyPort    int    `json:"proxyPort"`
	Endpoint     string `json:"endpoint"`
	// Active is true when the proxy exists in Toxiproxy (it is created on first use).
	Active  bool       `json:"active"`
	Enabled bool       `json:"enabled"`
	Toxics  []ToxicDto `json:"toxics"`
}
type ChaosDto struct {
	Available bool             `json:"available"`
	Presets   []PresetDto      `json:"presets"`
	Targets   []ChaosTargetDto `json:"targets"`
}

// ApplyDto is the optional body of an apply call.
type ApplyDto struct {
	Params map[string]float64 `json:"params"`
}

type chaosPreset struct {
	id, label, description string
	toxic                  string // empty for the outage, which disables the proxy instead
	params                 []ParamDto
}

var chaosToxicity = ParamDto{"toxicity", "Affected connections", "%", 1, 100, 100}

func (p chaosPreset) dto() PresetDto {
	params := append([]ParamDto{}, p.params...)
	if p.toxic != "" {
		params = append(params, chaosToxicity)
	}
	return PresetDto{p.id, p.label, p.description, params}
}

var chaosPresets = []chaosPreset{
	{"latency", "Latency", "Delay every response", "latency", []ParamDto{
		{"latency", "Latency", "ms", 0, 30_000, 500},
		{"jitter", "Jitter", "ms", 0, 10_000, 100},
	}},
	{"bandwidth", "Slow link", "Limit throughput", "bandwidth", []ParamDto{{"rate", "Rate", "KB/s", 1, 100_000, 10}}},
	{"timeout", "Hang", "Stop all data, then close after the timeout (0 = never)", "timeout", []ParamDto{{"timeout", "Timeout", "ms", 0, 60_000, 5_000}}},
	{"reset", "Connection reset", "Reset the TCP connection (RST)", "reset_peer", []ParamDto{{"timeout", "After", "ms", 0, 60_000, 0}}},
	{"slow_close", "Slow close", "Delay closing the connection", "slow_close", []ParamDto{{"delay", "Delay", "ms", 0, 60_000, 2_000}}},
	{"limit_data", "Cut after N bytes", "Close the connection after N bytes", "limit_data", []ParamDto{{"bytes", "Bytes", "B", 1, 100_000_000, 1_000}}},
	{"outage", "Outage", "Refuse every connection (disable the proxy)", "", nil},
}

var chaosToxicName = regexp.MustCompile(`^[a-z_]{1,32}$`)

// ChaosActivity is where the chaos routes note what the person did (the activity journal). Optional.
type ChaosActivity interface {
	Record(kind, subject, detail string)
}

// Chaos drives a Toxiproxy server. A proxy per target is created lazily, and presets map onto Toxiproxy toxics
// (always on the downstream, i.e. the emulator's responses).
type Chaos struct {
	gw       HTTPGateway
	base     string
	activity ChaosActivity
}

// NewChaos talks to Toxiproxy at base; empty means http://localhost:8474.
func NewChaos(gw HTTPGateway, base string) *Chaos {
	return &Chaos{gw: gw, base: orDefault(base, "http://localhost:"+strconv.Itoa(ChaosAPIPort))}
}

// SetActivity makes the routes record chaos.apply, chaos.clear and chaos.reset.
func (c *Chaos) SetActivity(a ChaosActivity) { c.activity = a }

func chaosPresetDtos() []PresetDto {
	out := make([]PresetDto, len(chaosPresets))
	for i, p := range chaosPresets {
		out[i] = p.dto()
	}
	return out
}

// View lists the presets and every target. When Toxiproxy is not running it still answers, with Available false.
func (c *Chaos) View(ctx context.Context) (ChaosDto, error) {
	live, err := c.proxies(ctx)
	if err != nil {
		var down EmulatorUnreachable
		if !errors.As(err, &down) {
			return ChaosDto{}, err
		}
		targets := make([]ChaosTargetDto, len(ChaosTargets))
		for i, t := range ChaosTargets {
			targets[i] = chaosRender(t, nil)
		}
		return ChaosDto{Available: false, Presets: chaosPresetDtos(), Targets: targets}, nil
	}
	targets := make([]ChaosTargetDto, len(ChaosTargets))
	for i, t := range ChaosTargets {
		targets[i] = chaosRender(t, live[t.ID])
	}
	return ChaosDto{Available: true, Presets: chaosPresetDtos(), Targets: targets}, nil
}

// Apply puts a preset on a target. Values are checked before anything is written.
func (c *Chaos) Apply(ctx context.Context, targetID, presetID string, params map[string]float64) (ChaosTargetDto, error) {
	target, ok := FindChaosTarget(targetID)
	if !ok {
		return ChaosTargetDto{}, UnknownTarget{"Unknown target '" + targetID + "'"}
	}
	var preset *chaosPreset
	for i := range chaosPresets {
		if chaosPresets[i].id == presetID {
			preset = &chaosPresets[i]
		}
	}
	if preset == nil {
		return ChaosTargetDto{}, InvalidParam{"Unknown preset '" + presetID + "'"}
	}
	attributes := map[string]float64{}
	for _, p := range preset.dto().Params {
		value, given := params[p.Key]
		if !given {
			value = p.Default
		}
		if math.IsNaN(value) || value < p.Min || value > p.Max {
			return ChaosTargetDto{}, InvalidParam{fmt.Sprintf("%s must be between %d and %d", p.Key, int64(p.Min), int64(p.Max))}
		}
		attributes[p.Key] = value
	}

	if err := c.ensureProxy(ctx, target); err != nil {
		return ChaosTargetDto{}, err
	}
	proxyPath := "/proxies/" + target.ID
	if preset.toxic == "" {
		if err := c.ok(c.send(ctx, "POST", proxyPath, `{"enabled":false}`)); err != nil {
			return ChaosTargetDto{}, err
		}
	} else {
		if err := c.ok(c.send(ctx, "POST", proxyPath, `{"enabled":true}`)); err != nil {
			return ChaosTargetDto{}, err
		}
		// Replacing is simpler than updating in place: delete (a 404 is fine) and create again.
		if _, err := c.send(ctx, "DELETE", proxyPath+"/toxics/"+preset.id, ""); err != nil {
			return ChaosTargetDto{}, err
		}
		toxicAttrs := map[string]int64{}
		for k, v := range attributes {
			if k != "toxicity" {
				toxicAttrs[k] = int64(math.Round(v))
			}
		}
		body, _ := json.Marshal(struct {
			Name       string           `json:"name"`
			Type       string           `json:"type"`
			Stream     string           `json:"stream"`
			Toxicity   float64          `json:"toxicity"`
			Attributes map[string]int64 `json:"attributes"`
		}{preset.id, preset.toxic, "downstream", attributes["toxicity"] / 100.0, toxicAttrs})
		if err := c.ok(c.send(ctx, "POST", proxyPath+"/toxics", string(body))); err != nil {
			return ChaosTargetDto{}, err
		}
	}
	return c.current(ctx, target)
}

// Clear removes every toxic of the target and turns the proxy back on. A target that was never proxied is left alone.
func (c *Chaos) Clear(ctx context.Context, targetID string) (ChaosTargetDto, error) {
	target, ok := FindChaosTarget(targetID)
	if !ok {
		return ChaosTargetDto{}, UnknownTarget{"Unknown target '" + targetID + "'"}
	}
	live, err := c.proxies(ctx)
	if err != nil {
		return ChaosTargetDto{}, err
	}
	if proxy, found := live[target.ID]; found {
		for _, t := range chaosToxicsOf(proxy) {
			if _, err := c.send(ctx, "DELETE", "/proxies/"+target.ID+"/toxics/"+t.Name, ""); err != nil {
				return ChaosTargetDto{}, err
			}
		}
		if err := c.ok(c.send(ctx, "POST", "/proxies/"+target.ID, `{"enabled":true}`)); err != nil {
			return ChaosTargetDto{}, err
		}
	}
	return c.current(ctx, target)
}

func (c *Chaos) RemoveToxic(ctx context.Context, targetID, name string) (ChaosTargetDto, error) {
	target, ok := FindChaosTarget(targetID)
	if !ok {
		return ChaosTargetDto{}, UnknownTarget{"Unknown target '" + targetID + "'"}
	}
	if !chaosToxicName.MatchString(name) {
		return ChaosTargetDto{}, InvalidParam{"Invalid toxic name"}
	}
	if err := c.ok(c.send(ctx, "DELETE", "/proxies/"+target.ID+"/toxics/"+name, "")); err != nil {
		return ChaosTargetDto{}, err
	}
	return c.current(ctx, target)
}

func (c *Chaos) ResetAll(ctx context.Context) (ChaosDto, error) {
	if err := c.ok(c.send(ctx, "POST", "/reset", "")); err != nil {
		return ChaosDto{}, err
	}
	return c.View(ctx)
}

// ---- plumbing ----

func (c *Chaos) current(ctx context.Context, target ChaosTarget) (ChaosTargetDto, error) {
	live, err := c.proxies(ctx)
	if err != nil {
		return ChaosTargetDto{}, err
	}
	return chaosRender(target, live[target.ID]), nil
}

func (c *Chaos) ensureProxy(ctx context.Context, target ChaosTarget) error {
	live, err := c.proxies(ctx)
	if err != nil {
		return err
	}
	if _, found := live[target.ID]; found {
		return nil
	}
	body, _ := json.Marshal(struct {
		Name     string `json:"name"`
		Listen   string `json:"listen"`
		Upstream string `json:"upstream"`
		Enabled  bool   `json:"enabled"`
	}{target.ID, fmt.Sprintf("0.0.0.0:%d", target.ProxyPort), fmt.Sprintf("host.docker.internal:%d", target.UpstreamPort), true})
	return c.ok(c.send(ctx, "POST", "/proxies", string(body)))
}

func (c *Chaos) proxies(ctx context.Context) (map[string]*obsObject, error) {
	reply, err := c.send(ctx, "GET", "/proxies", "")
	if err := c.ok(reply, err); err != nil {
		return nil, err
	}
	root, perr := obsParseObject(reply.Body)
	if perr != nil {
		return nil, EmulatorError{502, "Unreadable reply from Toxiproxy"}
	}
	out := map[string]*obsObject{}
	for _, k := range root.keys {
		o := obsObj(root.vals[k])
		if o == nil {
			return nil, EmulatorError{502, "Unreadable reply from Toxiproxy"}
		}
		out[k] = o
	}
	return out, nil
}

func chaosToxicsOf(proxy *obsObject) []ToxicDto {
	out := []ToxicDto{}
	for _, item := range obsArr(proxy.Get("toxics")) {
		t := obsObj(item)
		if t == nil {
			continue
		}
		toxicity := 1.0
		if s, ok := obsText(t.Get("toxicity")); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				toxicity = f
			}
		}
		attrs := map[string]any{}
		if a := obsObj(t.Get("attributes")); a != nil {
			for _, k := range a.keys {
				switch v := a.vals[k].(type) {
				case json.Number:
					f, _ := v.Float64()
					attrs[k] = f
				default:
					attrs[k] = v
				}
			}
		}
		out = append(out, ToxicDto{Name: obsStrOr(t, "name", ""), Type: obsStrOr(t, "type", ""), Toxicity: toxicity, Attributes: attrs})
	}
	return out
}

func chaosRender(target ChaosTarget, proxy *obsObject) ChaosTargetDto {
	dto := ChaosTargetDto{
		ID: target.ID, Name: target.Name, UpstreamPort: target.UpstreamPort, ProxyPort: target.ProxyPort,
		Endpoint: fmt.Sprintf("http://localhost:%d", target.ProxyPort),
		Active:   proxy != nil,
		Enabled:  true,
		Toxics:   []ToxicDto{},
	}
	if proxy != nil {
		if s, ok := obsText(proxy.Get("enabled")); ok && s == "false" {
			dto.Enabled = false
		}
		dto.Toxics = chaosToxicsOf(proxy)
	}
	return dto
}

func (c *Chaos) send(ctx context.Context, method, path, body string) (HTTPReply, error) {
	if body == "" {
		return c.gw.Send(ctx, method, c.base+path, map[string]string{}, nil)
	}
	return c.gw.Send(ctx, method, c.base+path, map[string]string{"Content-Type": "application/json"}, &body)
}

// ok turns a transport failure or a non-2xx answer into an error.
func (c *Chaos) ok(reply HTTPReply, err error) error {
	if err != nil {
		return err
	}
	if reply.Status < 200 || reply.Status > 299 {
		return EmulatorError{reply.Status, fmt.Sprintf("Toxiproxy %d: %s", reply.Status, obsTake(reply.Body, 120))}
	}
	return nil
}
