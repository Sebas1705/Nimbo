package nimbo

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// AssembleOptions is what the application shell hands over: where the UI and the bundled labs and stacks are, the
// environment, and how to quit when an update has been launched.
type AssembleOptions struct {
	UI      fs.FS             // the built React app (index.html at its root)
	Bundled fs.FS             // labs/*.yml and nimbo.yml, used when no folder on disk has them
	Env     map[string]string // the process environment
	Home    string            // the user's home folder
	Quit    func()            // closes the app; called after the installer of an update has been launched
}

// Assemble builds the whole API with every area wired to the real services. The returned closer stops the
// background sampler.
func Assemble(cfg Config, opts AssembleOptions) (http.Handler, io.Closer) {
	env := opts.Env
	if env == nil {
		env = map[string]string{}
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = DataDir(env, opts.Home)
	}
	data := AppDataOnDisk(dataDir)
	secrets := DefaultSecretStore(data)
	runtime := NewCliContainerRuntime(ProcessExec{Bin: orDefault(cfg.DockerBin, "docker")})
	shell := ProcessShell{Bin: orDefault(cfg.DockerBin, "docker")}
	// The registry of own images comes first: it publishes them as emulators, and a stack in nimbo.yml may name one.
	registryImages := NewImageRegistry(data.Store, data, runtime, secrets, env)
	stacks := LoadStacks(env, opts.Bundled)
	labs := LoadLabs(env, opts.Bundled)

	// The emulators answer on the host: localhost natively, host.docker.internal when Nimbo itself is in Docker.
	h := orDefault(cfg.EmulatorHost, "localhost")
	gw := DirectHTTP{}
	inspector := NewInspector(gw, "http://"+h+":4566", "http://"+h+":4588")
	chaos := NewChaos(gw, "http://"+h+":8474")
	chaos.SetActivity(statsActivity{data.Stats})
	traces := NewTraces(gw, "http://"+h+":16686", "http://"+h+":4318")
	kube := NewKube(shell, "")
	conformance := NewConformanceAt(gw, "http://"+h+":4566", "http://"+h+":4577", "http://"+h+":4588")

	fidelityStore := NewFidelityStore()
	verifier := NewLabVerifier(labInspector2{inspector}, labChaos2{chaos}, labKube2{kube}, traces)

	exe, _ := os.Executable()
	appDir := ""
	if cfg.Distribution == "desktop" && exe != "" {
		appDir = filepath.Dir(exe)
	}
	updOpts := UpdaterOptions{Quit: opts.Quit}
	if cfg.Distribution == "desktop" {
		updOpts.Installer = NewWindowsInstaller(dataDir)
	}
	updater := NewUpdater(UpdateConfig{Version: cfg.Version, Distribution: cfg.Distribution},
		updateSettings2{data.Settings}, updateActivity2{data.Stats}, updateStore2{data.Store}, NewHTTPDownloader(), dataDir, updOpts)
	images := NewImages(runtime2{ProcessExec{Bin: orDefault(cfg.DockerBin, "docker")}}, func() []string {
		return ImagesSearchFolders(env, dataDir, appDir)
	}, updateActivity2{data.Stats}, ImageRefsOfAllProviders(), nil)
	// Asked every time, so images registered later are listed and can be pulled.
	images.UseProviders(ImageRefsOfAllProviders)

	// Compose profiles and the repositories their files live in.
	regRun := ExecRegistryRunner{DockerBin: orDefault(cfg.DockerBin, "docker")}
	regSources := NewRegistrySources(data.Store, data.Stats, dataDir, regRun)
	regCompose := NewRegistryCompose(data.Store, data.Stats, regSources, regRun)

	ctx, cancel := context.WithCancel(context.Background())
	routes := []func(*http.ServeMux){
		NewUpdateRoutes(updater).Register,
		NewImageRoutes(images).Register,
		registryImages.Register,
		NewRegistryRoutes(regSources, regCompose).Register,
		inspector.Register,
		chaos.Register,
		traces.Register,
		kube.Register,
		NewFidelityRoutes(conformance, fidelityStore, fidelityHistory2{data.Fidelity}, statsLog{data.Stats}).Register,
		NewLabRoutes(labs, verifier, labProgress2{data.Progress}, statsLog{data.Stats}).Register,
	}
	handler := NewServer(cfg, Deps{Runtime: runtime, Stacks: stacks, Data: data, Secrets: secrets, UI: opts.UI, Routes: routes, Lifetime: ctx})
	return handler, closerFunc(cancel)
}

type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

// runtime2 is only here so Images takes the plain Exec it needs.
type runtime2 struct{ ProcessExec }

// ---- Adapters: each area asked for the smallest interface it needs; these are the real services behind them ------------

type statsActivity struct{ s *StatsService }

func (a statsActivity) Record(kind, subject, detail string) {
	if detail == "" {
		a.s.Record(kind, subject)
		return
	}
	a.s.Record(kind, subject, detail)
}

type updateActivity2 = statsActivity

type statsLog struct{ s *StatsService }

func (a statsLog) Record(kind, subject string, detail *string) {
	if detail == nil {
		a.s.Record(kind, subject)
		return
	}
	a.s.Record(kind, subject, *detail)
}

type updateSettings2 struct{ s *SettingsService }

func (a updateSettings2) UpdateSettings() (string, *int64) {
	cur := a.s.Get()
	return cur.UpdateMode, cur.LastUpdateCheck
}
func (a updateSettings2) MarkUpdateCheck(at int64) { _ = a.s.MarkUpdateCheck(at) }

type updateStore2 struct{ s DataStore }

func (a updateStore2) Read(name string) (string, bool) {
	text, ok, err := a.s.Read(name)
	return text, ok && err == nil
}
func (a updateStore2) Write(name, text string) error { return a.s.Write(name, text) }

type fidelityHistory2 struct{ h *FidelityHistory }

func fidelitySummary(r FidelityReportDto) FidelityReportSummary {
	out := FidelityReportSummary{DurationMs: r.DurationMs}
	for _, s := range r.Suites {
		passed := 0
		for _, st := range s.Steps {
			if st.Status == "pass" {
				passed++
			}
		}
		out.Suites = append(out.Suites, FidelitySuiteSummary{ID: s.ID, Status: s.Status, Steps: len(s.Steps), Passed: passed})
	}
	return out
}

func (a fidelityHistory2) ChangesFor(r FidelityReportDto) []FidelityChange {
	return a.h.ChangesFor(fidelitySummary(r))
}
func (a fidelityHistory2) Record(r FidelityReportDto) { _, _ = a.h.Record(fidelitySummary(r)) }
func (a fidelityHistory2) List() any                  { return a.h.List() }

type labProgress2 struct{ p *ProgressService }

func (a labProgress2) Completed() []string {
	var ids []string
	for id := range a.p.Completed() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (a labProgress2) Attempt(id string, passed bool) { _, _ = a.p.Attempt(id, passed) }

// labInspector2 answers the lab checks from the inspector's richer views.
type labInspector2 struct{ i *Inspector }

func (a labInspector2) BucketNames(ctx context.Context) ([]string, error) {
	list, err := a.i.Buckets(ctx)
	names := make([]string, 0, len(list))
	for _, b := range list {
		names = append(names, b.Name)
	}
	return names, err
}
func (a labInspector2) ObjectText(ctx context.Context, bucket, key string) (string, error) {
	p, err := a.i.Preview(ctx, bucket, key, InspectPreviewBytes)
	return p.Text, err
}
func (a labInspector2) QueueNames(ctx context.Context) ([]string, error) {
	list, err := a.i.Queues(ctx)
	names := make([]string, 0, len(list))
	for _, q := range list {
		names = append(names, q.Name)
	}
	return names, err
}
func (a labInspector2) MessageBodies(ctx context.Context, queue string) ([]string, error) {
	m, err := a.i.Messages(ctx, queue, "")
	bodies := make([]string, 0, len(m.Messages))
	for _, x := range m.Messages {
		bodies = append(bodies, x.Body)
	}
	return bodies, err
}
func (a labInspector2) Tables(ctx context.Context) ([]labTable, error) {
	list, err := a.i.Tables(ctx)
	out := make([]labTable, 0, len(list))
	for _, t := range list {
		out = append(out, labTable{Name: t.Name, Keys: t.Keys})
	}
	return out, err
}
func (a labInspector2) Items(ctx context.Context, table string, limit int) ([]any, error) {
	it, err := a.i.Items(ctx, table, limit)
	return it.Items, err
}
func (a labInspector2) PubSub(ctx context.Context, project string) ([]string, []labSubscription, error) {
	p, err := a.i.Pubsub(ctx, project)
	var topics []string
	for _, t := range p.Topics {
		topics = append(topics, t.Name)
	}
	var subs []labSubscription
	for _, s := range p.Subscriptions {
		subs = append(subs, labSubscription{Name: s.Name, Topic: s.Topic})
	}
	return topics, subs, err
}

type labChaos2 struct{ c *Chaos }

func (a labChaos2) View(ctx context.Context) (labChaosView, error) {
	v, err := a.c.View(ctx)
	out := labChaosView{Available: v.Available}
	for _, t := range v.Targets {
		lt := labChaosTarget{ID: t.ID, Name: t.Name, Enabled: t.Enabled}
		for _, x := range t.Toxics {
			lt.Toxics = append(lt.Toxics, labToxic{Name: x.Name, Attributes: x.Attributes})
		}
		out.Targets = append(out.Targets, lt)
	}
	return out, err
}

type labKube2 struct{ k *Kube }

func (a labKube2) Overview(ctx context.Context) (labKubeOverview, error) {
	o, err := a.k.Overview(ctx)
	out := labKubeOverview{Available: o.Available}
	if o.Reason != nil {
		out.Reason = *o.Reason
	}
	for _, d := range o.Deployments {
		out.Deployments = append(out.Deployments, labDeployment{Namespace: d.Namespace, Name: d.Name, Ready: d.Ready, Desired: d.Desired})
	}
	for _, s := range o.Services {
		out.Services = append(out.Services, labService{Namespace: s.Namespace, Name: s.Name, Type: s.Type})
	}
	return out, err
}
