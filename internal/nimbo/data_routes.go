package nimbo

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

type StatsViewDto struct {
	Stats    StatsData            `json:"stats"`
	Journal  []JournalEntry       `json:"journal"`
	Fidelity []FidelityRunSummary `json:"fidelity"`
}

type DataInfoDto struct {
	Location     *string          `json:"location"`
	Files        map[string]int64 `json:"files"`
	Distribution string           `json:"distribution"`
}

type ImportIdsDto struct {
	IDs []string `json:"ids"`
}

type SecretItemDto struct {
	Name     string   `json:"name"`
	Set      bool     `json:"set"`
	UsedBy   []string `json:"usedBy"`
	Password bool     `json:"password"`
}

type SecretsDto struct {
	Available bool            `json:"available"`
	Reason    *string         `json:"reason"`
	Items     []SecretItemDto `json:"items"`
}

type SecretValueDto struct {
	Value string `json:"value"`
}

type PersistDto struct {
	Enabled bool `json:"enabled"`
}

type RestoredDto struct {
	Restored []string `json:"restored"`
}

// DataRoutes: settings, lab progress, usage statistics, backups and secrets. Everything lives on this machine.
type DataRoutes struct {
	Data    *AppData
	Secrets SecretStore
	Config  Config
}

func (d *DataRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", Handle(d.getSettings))
	mux.HandleFunc("PUT /api/settings", Handle(d.putSettings))
	mux.HandleFunc("GET /api/progress", Handle(d.getProgress))
	mux.HandleFunc("POST /api/progress/import", Handle(d.importProgress))
	mux.HandleFunc("GET /api/stats", Handle(d.stats))
	mux.HandleFunc("GET /api/data", Handle(d.info))
	mux.HandleFunc("GET /api/data/export", Handle(d.export))
	mux.HandleFunc("POST /api/data/import", Handle(d.importData))
	mux.HandleFunc("POST /api/data/reset", Handle(d.reset))
	mux.HandleFunc("GET /api/secrets", Handle(d.listSecrets))
	mux.HandleFunc("PUT /api/secrets/{name}", Handle(d.setSecret))
	mux.HandleFunc("DELETE /api/secrets/{name}", Handle(d.deleteSecret))
	mux.HandleFunc("POST /api/secrets/{name}/generate", Handle(d.generateSecret))
}

func coreAtoiQuery(r *http.Request, name string) (int, bool) {
	n, err := strconv.Atoi(Query(r, name))
	return n, err == nil
}

func (d *DataRoutes) getSettings(w http.ResponseWriter, r *http.Request) error {
	WriteJSON(w, http.StatusOK, d.Data.Settings.Get())
	return nil
}

func (d *DataRoutes) putSettings(w http.ResponseWriter, r *http.Request) error {
	var patch SettingsPatch
	if err := ReadJSON(r, &patch); err != nil {
		return err
	}
	before := d.Data.Settings.Get()
	after, err := d.Data.Settings.Patch(patch)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(after, before) {
		d.Data.Stats.Record("settings.change", "settings")
	}
	WriteJSON(w, http.StatusOK, after)
	return nil
}

func (d *DataRoutes) getProgress(w http.ResponseWriter, r *http.Request) error {
	WriteJSON(w, http.StatusOK, d.Data.Progress.Get())
	return nil
}

func (d *DataRoutes) importProgress(w http.ResponseWriter, r *http.Request) error {
	var body ImportIdsDto
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	if body.IDs == nil {
		return BadBody{}
	}
	p, err := d.Data.Progress.ImportDone(body.IDs)
	if err != nil {
		return err
	}
	WriteJSON(w, http.StatusOK, p)
	return nil
}

func (d *DataRoutes) stats(w http.ResponseWriter, r *http.Request) error {
	limit := 40
	if n, ok := coreAtoiQuery(r, "limit"); ok {
		limit = n
	}
	fid := d.Data.Fidelity.List()
	if len(fid) > 30 {
		fid = fid[len(fid)-30:]
	}
	WriteJSON(w, http.StatusOK, StatsViewDto{Stats: d.Data.Stats.Stats(), Journal: d.Data.Stats.Journal(limit), Fidelity: fid})
	return nil
}

func (d *DataRoutes) info(w http.ResponseWriter, r *http.Request) error {
	var location *string
	if loc := d.Data.Store.Location(); loc != "" {
		location = coreStr(loc)
	}
	WriteJSON(w, http.StatusOK, DataInfoDto{Location: location, Files: d.Data.Store.Sizes(), Distribution: d.Config.Distribution})
	return nil
}

func (d *DataRoutes) export(w http.ResponseWriter, r *http.Request) error {
	WriteJSON(w, http.StatusOK, d.Data.Export())
	return nil
}

func (d *DataRoutes) importData(w http.ResponseWriter, r *http.Request) error {
	// exportedAt and files have no defaults: a body without them is not a backup.
	var raw struct {
		Format     *int               `json:"format"`
		ExportedAt *int64             `json:"exportedAt"`
		Files      *map[string]string `json:"files"`
	}
	if err := ReadJSON(r, &raw); err != nil {
		return err
	}
	if raw.ExportedAt == nil || raw.Files == nil {
		return BadBody{}
	}
	bundle := DataBundle{Format: 1, ExportedAt: *raw.ExportedAt, Files: *raw.Files}
	if raw.Format != nil {
		bundle.Format = *raw.Format
	}
	restored, err := d.Data.Restore(bundle)
	if err != nil {
		return err
	}
	d.Data.Stats.Record("data.import", "backup", strings.Join(restored, ", "))
	WriteJSON(w, http.StatusOK, RestoredDto{Restored: restored})
	return nil
}

func (d *DataRoutes) reset(w http.ResponseWriter, r *http.Request) error {
	if Query(r, "confirm") != "yes" {
		return InvalidParam{"Add confirm=yes: this deletes data and cannot be undone"}
	}
	switch what := Query(r, "what"); what {
	case "stats":
		d.Data.Stats.Reset()
	case "progress":
		_ = d.Data.Store.Delete(FileProgress)
	case "history":
		d.Data.Fidelity.Clear()
	case "all":
		d.Data.Stats.Reset()
		d.Data.Fidelity.Clear()
		for _, n := range BackupFiles {
			_ = d.Data.Store.Delete(n)
		}
	default:
		return InvalidParam{"what must be stats, progress, history or all (got '" + what + "')"}
	}
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}

func (d *DataRoutes) listSecrets(w http.ResponseWriter, r *http.Request) error {
	set := map[string]bool{}
	for _, n := range d.Secrets.Names(r.Context()) {
		set[n] = true
	}
	items := make([]SecretItemDto, 0, len(SecretNames))
	for _, n := range SecretNames {
		items = append(items, SecretItemDto{Name: n, Set: set[n], UsedBy: SecretUsedBy(n), Password: IsSecretPassword(n)})
	}
	var reason *string
	if why := d.Secrets.Reason(); why != "" {
		reason = coreStr(why)
	}
	WriteJSON(w, http.StatusOK, SecretsDto{Available: d.Secrets.Available(), Reason: reason, Items: items})
	return nil
}

func (d *DataRoutes) setSecret(w http.ResponseWriter, r *http.Request) error {
	name := Param(r, "name")
	var body struct {
		Value *string `json:"value"`
	}
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	if body.Value == nil {
		return BadBody{}
	}
	if err := d.Secrets.Set(coreDetached(r), name, *body.Value); err != nil {
		return err
	}
	d.Data.Stats.Record("secret.set", name)
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}

func (d *DataRoutes) deleteSecret(w http.ResponseWriter, r *http.Request) error {
	name := Param(r, "name")
	if !isSecretName(name) {
		return UnknownTarget{"Unknown secret '" + name + "'"}
	}
	if err := d.Secrets.Delete(coreDetached(r), name); err != nil {
		return err
	}
	d.Data.Stats.Record("secret.delete", name)
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}

func (d *DataRoutes) generateSecret(w http.ResponseWriter, r *http.Request) error {
	name := Param(r, "name")
	if !isSecretName(name) {
		return UnknownTarget{"Unknown secret '" + name + "'"}
	}
	value, err := GenerateFor(*varSpecByName(name))
	if err != nil {
		return err
	}
	if value == "" {
		return InvalidParam{"This one is not generated: choose its value yourself"}
	}
	if err := d.Secrets.Set(coreDetached(r), name, value); err != nil {
		return err
	}
	d.Data.Stats.Record("secret.set", name, "generated")
	// The only time the value leaves the store: the person has to be able to connect with it.
	WriteJSON(w, http.StatusOK, SecretValueDto{Value: value})
	return nil
}
