package nimbo

import (
	"net/http"
	"strings"
)

// VariableRoutes manage the settings of every emulator: list them, initialize the missing ones, read one back on
// request, and apply the current values to a running emulator.
type VariableRoutes struct {
	Config  Config
	Runtime ContainerRuntime
	Data    *AppData
	Secrets SecretStore
}

// VarDto is one setting of an emulator. It never carries the value: that needs an explicit reveal.
type VarDto struct {
	Name        string  `json:"name"`
	Container   string  `json:"container"`
	Secret      bool    `json:"secret"`
	Required    bool    `json:"required"`
	CanGenerate bool    `json:"canGenerate"`
	Default     *string `json:"default"`
	// FixedAtCreation settings only take effect when the emulator's data is first created (PostgreSQL's user, database
	// and password): with kept data, changing them does not change what is already there.
	FixedAtCreation bool `json:"fixedAtCreation"`
	// Source is "environment", "stored" or "unset". The environment wins over the store.
	Source string `json:"source"`
	Set    bool   `json:"set"`
}

type ProviderVarsDto struct {
	Provider string   `json:"provider"`
	Name     string   `json:"name"`
	Vars     []VarDto `json:"vars"`
}

type VariablesDto struct {
	Available bool              `json:"available"`
	Reason    *string           `json:"reason"`
	Providers []ProviderVarsDto `json:"providers"`
}

type RevealDto struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

type InitDto struct {
	Created []string `json:"created"`
}

type VarsApplyDto struct {
	// Applied is false when there was nothing running to apply the values to.
	Applied bool    `json:"applied"`
	Note    *string `json:"note"`
}

func (v *VariableRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/variables", Handle(v.list))
	mux.HandleFunc("POST /api/secrets/{name}/reveal", Handle(v.reveal))
	mux.HandleFunc("POST /api/providers/{id}/variables/init", Handle(v.initialize))
	mux.HandleFunc("POST /api/providers/{id}/apply", Handle(v.apply))
}

func (v *VariableRoutes) list(w http.ResponseWriter, r *http.Request) error {
	out := make([]ProviderVarsDto, 0, len(CatalogProviders))
	for _, def := range CatalogProviders {
		dto := ProviderVarsDto{Provider: def.ID, Name: def.Name, Vars: []VarDto{}}
		sources := VarSources(r.Context(), def, v.Config.HostEnv, v.Secrets)
		for _, spec := range def.Vars {
			d := VarDto{
				Name: spec.Name, Container: spec.Container, Secret: spec.Secret, Required: spec.Required,
				CanGenerate: spec.Generate != "", FixedAtCreation: spec.FixedAtCreation,
				Source: string(sources[spec.Name]), Set: sources[spec.Name] != SourceUnset,
			}
			if !spec.Secret && spec.Default != "" {
				d.Default = coreStr(spec.Default)
			}
			dto.Vars = append(dto.Vars, d)
		}
		out = append(out, dto)
	}
	var reason *string
	if why := v.Secrets.Reason(); why != "" {
		reason = coreStr(why)
	}
	WriteJSON(w, http.StatusOK, VariablesDto{Available: v.Secrets.Available(), Reason: reason, Providers: out})
	return nil
}

// reveal reads one setting back. It is the only way a secret leaves the server, it needs a deliberate POST from the
// page, and it leaves a line in the journal (the name, never the value).
func (v *VariableRoutes) reveal(w http.ResponseWriter, r *http.Request) error {
	name := Param(r, "name")
	if !isSecretName(name) {
		return UnknownTarget{"Unknown secret '" + name + "'"}
	}
	for _, def := range CatalogProviders {
		for _, rv := range ResolveVars(r.Context(), def, v.Config.HostEnv, v.Secrets) {
			if rv.Spec.Name == name && rv.Source != SourceUnset {
				v.Data.Stats.Record("secret.reveal", name)
				WriteJSON(w, http.StatusOK, RevealDto{Value: rv.Value, Source: string(rv.Source)})
				return nil
			}
		}
	}
	return UnknownTarget{name + " has no value yet"}
}

// initialize fills in every setting of an emulator that has none: required ones always, optional ones when asked.
func (v *VariableRoutes) initialize(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	created, err := InitializeVars(coreDetached(r), def, v.Config.HostEnv, v.Secrets, Query(r, "optional") == "true")
	if err != nil {
		return err
	}
	if len(created) > 0 {
		v.Data.Stats.Record("variables.init", def.ID, strings.Join(created, ", "))
	}
	WriteJSON(w, http.StatusOK, InitDto{Created: created})
	return nil
}

// apply makes a running emulator use the values it has now: it removes the container (the data volume stays) and
// starts it again. A stopped emulator is only removed, so its next start uses the new values.
func (v *VariableRoutes) apply(w http.ResponseWriter, r *http.Request) error {
	def, err := coreProviderParam(r)
	if err != nil {
		return err
	}
	ctx := coreDetached(r)
	info, exists := v.Runtime.Containers(ctx)[def.ID]
	if !exists {
		WriteJSON(w, http.StatusOK, VarsApplyDto{Applied: false})
		return nil
	}
	if err := v.Runtime.Remove(ctx, def); err != nil {
		coreConflict(w, err, "could not remove the old container")
		return nil
	}
	if info.Running {
		env := EnvForProvider(ctx, def, v.Config.HostEnv, v.Secrets)
		if err := v.Runtime.Start(ctx, def, env, v.Data.Settings.Persists(def.ID)); err != nil {
			coreConflict(w, err, "start failed")
			return nil
		}
	}
	v.Data.Stats.Record("variables.apply", def.ID)
	var note *string
	if v.Data.Settings.Persists(def.ID) && v.hasFixedAtCreation(def) {
		note = coreStr("This emulator keeps its data, and some of its settings only apply when that data is first created: the ones already inside are unchanged.")
	}
	WriteJSON(w, http.StatusOK, VarsApplyDto{Applied: info.Running, Note: note})
	return nil
}

func (v *VariableRoutes) hasFixedAtCreation(def ProviderDef) bool {
	for _, s := range def.Vars {
		if s.FixedAtCreation {
			return true
		}
	}
	return false
}

// clientVars is what a client should set to talk to a provider: its fixed endpoints plus the ones that depend on its
// settings. Secrets are placeholders unless reveal is true.
func clientVars(def ProviderDef, rvs []ResolvedVar, reveal bool) Vars {
	out := append(Vars{}, def.Endpoints...)
	if def.Exports == nil {
		return out
	}
	values := map[string]string{}
	for _, rv := range rvs {
		switch {
		case rv.Source == SourceUnset:
		case rv.Spec.Secret && !reveal:
			values[rv.Spec.Name] = "<" + rv.Spec.Name + ">"
		default:
			values[rv.Spec.Name] = rv.Value
		}
	}
	for _, kv := range def.Exports(values) {
		replaced := false
		for i := range out {
			if out[i].Key == kv.Key {
				out[i].Value, replaced = kv.Value, true
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	return out
}
