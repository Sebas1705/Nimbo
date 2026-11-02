package nimbo

import (
	"context"
	"net/http"
	"strings"
)

// ProviderRoutes start, stop and inspect the providers. Starting uses the saved secrets and the data setting of each
// provider.
type ProviderRoutes struct {
	Config  Config
	Runtime ContainerRuntime
	Data    *AppData
	Secrets SecretStore
}

type corePersistBody struct {
	Enabled *bool `json:"enabled"`
}

func (p *ProviderRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docker", Handle(p.docker))
	mux.HandleFunc("GET /api/providers", Handle(p.list))
	mux.HandleFunc("POST /api/providers/{id}/start", Handle(p.start))
	mux.HandleFunc("POST /api/providers/{id}/stop", Handle(p.stop))
	mux.HandleFunc("DELETE /api/providers/{id}", Handle(p.remove))
	mux.HandleFunc("POST /api/providers/{id}/persist", Handle(p.persist))
	mux.HandleFunc("DELETE /api/providers/{id}/data", Handle(p.deleteData))
	mux.HandleFunc("GET /api/providers/{id}/logs", Handle(p.logs))
	mux.HandleFunc("GET /api/providers/{id}/env", Handle(p.env))
}

func coreProviderParam(r *http.Request) (ProviderDef, error) {
	id := Param(r, "id")
	def := CatalogFind(id)
	if def == nil {
		return ProviderDef{}, UnknownTarget{"Unknown provider '" + id + "'"}
	}
	return *def, nil
}

// coreDetached keeps an action going when the page that asked for it goes away: a half-finished `docker run` helps nobody.
func coreDetached(r *http.Request) context.Context { return context.WithoutCancel(r.Context()) }

func coreConflict(w http.ResponseWriter, err error, fallback string) {
	WriteJSON(w, http.StatusConflict, ErrorDto{orDefault(err.Error(), fallback)})
}

func coreProviderDto(def ProviderDef, state RunState, detail *string, persist, hasData bool) ProviderDto {
	return ProviderDto{
		ID: def.ID, Name: def.Name, Cloud: string(def.Cloud), Description: def.Description, Image: def.Image,
		Ports: def.Ports, State: string(state), Detail: detail,
		Persistable: def.Persist != nil, Persist: persist, HasData: hasData, NeedsSetup: []string{},
	}
}

func (p *ProviderRoutes) docker(w http.ResponseWriter, r *http.Request) error {
	WriteJSON(w, http.StatusOK, p.Runtime.Status(r.Context()))
	return nil
}

func (p *ProviderRoutes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	docker := p.Runtime.Status(ctx)
	live := map[string]ContainerInfo{}
	saved := map[string]bool{}
	if docker.Available {
		live = p.Runtime.Containers(ctx)
		saved = p.Runtime.SavedData(ctx)
	}
	persisting := map[string]bool{}
	for _, id := range p.Data.Settings.Get().Persist {
		persisting[id] = true
	}
	out := make([]ProviderDto, 0, len(CatalogProviders))
	for _, def := range CatalogProviders {
		state := StateNotCreated
		var detail *string
		if info, ok := live[def.ID]; ok {
			detail = coreStr(info.Status)
			if info.Running {
				state = StateRunning
			} else {
				state = StateStopped
			}
		}
		dto := coreProviderDto(def, state, detail, persisting[def.ID], saved[def.ID])
		dto.NeedsSetup = MissingRequired(def, VarSources(r.Context(), def, p.Config.HostEnv, p.Secrets))
		out = append(out, dto)
	}
	WriteJSON(w, http.StatusOK, out)
	return nil
}

func (p *ProviderRoutes) start(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	ctx := coreDetached(r)
	env := EnvForProvider(ctx, def, p.Config.HostEnv, p.Secrets)
	if err := p.Runtime.Start(ctx, def, env, p.Data.Settings.Persists(def.ID)); err != nil {
		coreConflict(w, err, "start failed")
		return nil
	}
	p.Data.Stats.Record("provider.start", def.ID)
	WriteJSON(w, http.StatusAccepted, coreProviderDto(def, StateRunning, nil, false, false))
	return nil
}

func (p *ProviderRoutes) stop(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	if err := p.Runtime.Stop(coreDetached(r), def); err != nil {
		coreConflict(w, err, "stop failed")
		return nil
	}
	p.Data.Stats.Record("provider.stop", def.ID)
	WriteJSON(w, http.StatusAccepted, coreProviderDto(def, StateStopped, nil, false, false))
	return nil
}

func (p *ProviderRoutes) remove(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	if err := p.Runtime.Remove(coreDetached(r), def); err != nil {
		coreConflict(w, err, "remove failed")
		return nil
	}
	p.Data.Stats.Record("provider.remove", def.ID)
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}

func (p *ProviderRoutes) persist(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	var body corePersistBody
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	if body.Enabled == nil {
		return BadBody{}
	}
	settings, err := p.Data.Settings.SetPersist(def.ID, *body.Enabled)
	if err != nil {
		return err
	}
	detail := "off"
	if *body.Enabled {
		detail = "on"
	}
	p.Data.Stats.Record("provider.persist", def.ID, detail)
	WriteJSON(w, http.StatusOK, settings)
	return nil
}

func (p *ProviderRoutes) deleteData(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	if Query(r, "confirm") != "yes" {
		return InvalidParam{"Add confirm=yes: this deletes the saved data of " + def.Name + " and cannot be undone"}
	}
	if err := p.Runtime.DeleteData(coreDetached(r), def); err != nil {
		coreConflict(w, err, "could not delete the data")
		return nil
	}
	p.Data.Stats.Record("provider.data.delete", def.ID)
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}

func (p *ProviderRoutes) logs(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	tail := 200
	if n, ok := coreAtoiQuery(r, "tail"); ok {
		tail = n
	}
	lines, err := p.Runtime.Logs(r.Context(), def, tail)
	if err != nil {
		coreConflict(w, err, "no logs")
		return nil
	}
	WriteJSON(w, http.StatusOK, LogsDto{ID: def.ID, Lines: lines})
	return nil
}

func (p *ProviderRoutes) env(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	// With ?secrets=true the real values are included (the page asks when you press the button); otherwise the secrets
	// show as <NAME> placeholders so the text is safe to paste anywhere.
	reveal := Query(r, "secrets") == "true"
	vars := clientVars(def, ResolveVars(r.Context(), def, p.Config.HostEnv, p.Secrets), reveal)
	if reveal {
		p.Data.Stats.Record("secret.reveal", def.ID, "environment variables")
	}
	var dotenv strings.Builder
	for i, kv := range vars {
		if i > 0 {
			dotenv.WriteString("\n")
		}
		dotenv.WriteString(kv.Key + "=" + kv.Value)
	}
	WriteJSON(w, http.StatusOK, EnvDto{ID: def.ID, Variables: vars, Dotenv: dotenv.String()})
	return nil
}

// StackRoutes start, stop and export groups of providers.
type StackRoutes struct {
	Config  Config
	Runtime ContainerRuntime
	Stacks  *StackCatalog
	Data    *AppData
	Secrets SecretStore
}

func (s *StackRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/stacks", Handle(s.list))
	mux.HandleFunc("POST /api/stacks/{id}/up", Handle(s.up))
	mux.HandleFunc("POST /api/stacks/{id}/down", Handle(s.down))
	mux.HandleFunc("GET /api/stacks/{id}/export", Handle(s.export))
}

func coreStackDto(st StackDef, live map[string]ContainerInfo) StackDto {
	running := 0
	for _, id := range st.Providers {
		if live[id].Running {
			running++
		}
	}
	state := "partial"
	switch running {
	case 0:
		state = "stopped"
	case len(st.Providers):
		state = "running"
	}
	return StackDto{ID: st.ID, Description: st.Description, Providers: st.Providers, State: state, Running: running, Total: len(st.Providers)}
}

func (s *StackRoutes) stack(r *http.Request) (StackDef, error) {
	id := Param(r, "id")
	st := s.Stacks.Find(id)
	if st == nil {
		return StackDef{}, UnknownTarget{"Unknown stack '" + id + "'"}
	}
	return *st, nil
}

func coreStepResult(id string, err error) StepResultDto {
	if err == nil {
		return StepResultDto{ID: id, OK: true}
	}
	return StepResultDto{ID: id, OK: false, Error: coreStr(err.Error())}
}

func (s *StackRoutes) list(w http.ResponseWriter, r *http.Request) error {
	live := map[string]ContainerInfo{}
	if s.Runtime.Status(r.Context()).Available {
		live = s.Runtime.Containers(r.Context())
	}
	stacks := make([]StackDto, 0, len(s.Stacks.Stacks))
	for _, st := range s.Stacks.Stacks {
		stacks = append(stacks, coreStackDto(st, live))
	}
	problems := s.Stacks.Problems
	if problems == nil {
		problems = []string{}
	}
	WriteJSON(w, http.StatusOK, StackListDto{Stacks: stacks, Problems: problems})
	return nil
}

func (s *StackRoutes) up(w http.ResponseWriter, r *http.Request) error {
	stack, err := s.stack(r)
	if err != nil {
		return err
	}
	ctx := coreDetached(r)
	live := s.Runtime.Containers(ctx)
	// Sequential on purpose: parallel `docker run` of several big images is hard to read when one fails.
	results := make([]StepResultDto, 0, len(stack.Providers))
	for _, id := range stack.Providers {
		if live[id].Running {
			results = append(results, StepResultDto{ID: id, OK: true})
			continue
		}
		def := *CatalogFind(id)
		err := s.Runtime.Start(ctx, def, EnvForProvider(ctx, def, s.Config.HostEnv, s.Secrets), s.Data.Settings.Persists(id))
		if err == nil {
			s.Data.Stats.Record("provider.start", id)
		}
		results = append(results, coreStepResult(id, err))
	}
	s.Data.Stats.Record("stack.up", stack.ID)
	WriteJSON(w, http.StatusOK, StackActionDto{ID: stack.ID, Action: "up", Results: results})
	return nil
}

func (s *StackRoutes) down(w http.ResponseWriter, r *http.Request) error {
	stack, err := s.stack(r)
	if err != nil {
		return err
	}
	ctx := coreDetached(r)
	live := s.Runtime.Containers(ctx)
	results := make([]StepResultDto, 0, len(stack.Providers))
	for i := len(stack.Providers) - 1; i >= 0; i-- {
		id := stack.Providers[i]
		if !live[id].Running {
			results = append(results, StepResultDto{ID: id, OK: true})
			continue
		}
		err := s.Runtime.Stop(ctx, *CatalogFind(id))
		if err == nil {
			s.Data.Stats.Record("provider.stop", id)
		}
		results = append(results, coreStepResult(id, err))
	}
	s.Data.Stats.Record("stack.down", stack.ID)
	WriteJSON(w, http.StatusOK, StackActionDto{ID: stack.ID, Action: "down", Results: results})
	return nil
}

func (s *StackRoutes) export(w http.ResponseWriter, r *http.Request) error {
	stack, err := s.stack(r)
	if err != nil {
		return err
	}
	format, ok := ExportFormatOf(Query(r, "format"))
	if !ok {
		return InvalidParam{"format must be one of: " + strings.Join(sortedFormatIDs(), ", ")}
	}
	var defs []ProviderDef
	hasAWS := false
	for _, id := range stack.Providers {
		if def := CatalogFind(id); def != nil {
			defs = append(defs, *def)
			hasAWS = hasAWS || id == "floci-aws"
		}
	}
	if format.NeedsAWS && !hasAWS {
		WriteJSON(w, http.StatusConflict, ErrorDto{"Stack '" + stack.ID + "' has no AWS emulator (floci-aws)"})
		return nil
	}
	chaos := Query(r, "chaos") == "true"
	// The real passwords are in a file only when the page asks for them (a deliberate button), and that is journaled.
	reveal := Query(r, "secrets") == "true"
	values := exportValues(r.Context(), defs, s.Config.HostEnv, s.Secrets, reveal)
	var content string
	switch format {
	case ExportFormatEnv:
		content = ExportEnv(defs, chaos)
	case ExportFormatCompose:
		content = ExportCompose(stack.ID, defs, values, Query(r, "persist") == "true")
	case ExportFormatComposeEnv:
		content = ExportComposeEnv(defs, values)
	case ExportFormatSpring:
		content = ExportSpring(defs, values)
	case ExportFormatDotnet:
		content = ExportDotnet(defs, values)
	case ExportFormatTofu:
		content = ExportTofuAwsOverride(chaos)
	default:
		content = ExportAwsCliProfile(chaos)
	}
	if reveal {
		s.Data.Stats.Record("secret.reveal", stack.ID, "export "+format.ID)
	}
	s.Data.Stats.Record("export", stack.ID, format.ID)
	WriteJSON(w, http.StatusOK, ExportDto{Format: format.ID, Filename: format.Filename, Content: content})
	return nil
}
