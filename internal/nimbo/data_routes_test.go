package nimbo

import (
	"net/http"
	"strings"
	"testing"
)

type coreApp struct {
	h       http.Handler
	data    *AppData
	rt      *coreFakeRuntime
	secrets *coreMemorySecrets
}

func newCoreApp(cfg Config) *coreApp {
	a := &coreApp{data: NewAppData(nil, nil), rt: newCoreFakeRuntime(), secrets: newCoreMemorySecrets()}
	a.h = NewServer(cfg, Deps{Runtime: a.rt, Data: a.data, Secrets: a.secrets})
	return a
}

func (a *coreApp) do(method, target, body string, extra ...string) coreResp {
	return coreDo(a.h, method, target, body, extra...)
}

func TestSettingsReadChangedValidated(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, "auto", coreObject(t, a.do("GET", "/api/settings", "").Body)["theme"].(string))
	coreEq(t, 200, a.do("PUT", "/api/settings", `{"lang":"en","theme":"dark"}`).Code)
	now := coreObject(t, a.do("GET", "/api/settings", "").Body)
	coreEq(t, "en", now["lang"].(string))
	coreEq(t, "dark", now["theme"].(string))
	coreEq(t, 400, a.do("PUT", "/api/settings", `{"lang":"xx"}`).Code)
	coreEq(t, 400, a.do("PUT", "/api/settings", `nonsense`).Code)
	coreContains(t, a.do("GET", "/api/stats", "").Body, "settings.change")
}

func TestProgressImportedFromTheBrowser(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, 200, a.do("POST", "/api/progress/import", `{"ids":["lab-a","lab-b"]}`).Code)
	coreContains(t, a.do("GET", "/api/progress", "").Body, "lab-b")
	coreEq(t, 400, a.do("POST", "/api/progress/import", `{}`).Code)
}

func TestStartingAndStoppingFeedsStatistics(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.do("POST", "/api/providers/redis/start", "")
	a.do("POST", "/api/providers/redis/stop", "")
	a.do("POST", "/api/stacks/"+BuiltInStacks().Stacks[0].ID+"/up", "")
	view := coreObject(t, a.do("GET", "/api/stats", "").Body)
	redis := view["stats"].(map[string]any)["providers"].(map[string]any)["redis"].(map[string]any)
	coreTrue(t, redis["starts"].(float64) >= 1)
	coreEq(t, 1.0, redis["stops"].(float64))
	coreTrue(t, len(view["journal"].([]any)) >= 3)
}

func TestPersistSwitchIsRememberedAndUsed(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, 200, a.do("POST", "/api/providers/postgres/persist", `{"enabled":true}`).Code)
	a.do("POST", "/api/providers/redis/persist", `{"enabled":true}`)
	a.do("POST", "/api/providers/redis/start", "")
	coreTrue(t, a.rt.started[len(a.rt.started)-1].persist)
	var redis map[string]any
	var list []any
	if err := jsonUnmarshal(a.do("GET", "/api/providers", "").Body, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.(map[string]any)["id"] == "redis" {
			redis = p.(map[string]any)
		}
	}
	coreTrue(t, redis["persistable"] == true && redis["persist"] == true && redis["hasData"] == true)
	coreEq(t, 400, a.do("POST", "/api/providers/redis/persist", `{}`).Code)
}

func TestPersistOfUnknownOrStatelessIsRefused(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, 404, a.do("POST", "/api/providers/nope/persist", `{"enabled":true}`).Code)
	for _, d := range CatalogProviders {
		if d.Persist == nil {
			coreEq(t, 400, a.do("POST", "/api/providers/"+d.ID+"/persist", `{"enabled":true}`).Code)
			return
		}
	}
}

func TestDeletingDataNeedsConfirmationAndAStoppedProvider(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.do("POST", "/api/providers/redis/persist", `{"enabled":true}`)
	a.do("POST", "/api/providers/redis/start", "")
	coreEq(t, 400, a.do("DELETE", "/api/providers/redis/data", "").Code)
	coreEq(t, 409, a.do("DELETE", "/api/providers/redis/data?confirm=yes", "").Code, "running")
	a.do("POST", "/api/providers/redis/stop", "")
	coreEq(t, 204, a.do("DELETE", "/api/providers/redis/data?confirm=yes", "").Code)
	coreEq(t, "redis", strings.Join(a.rt.deleted, ","))
	coreEq(t, 404, a.do("DELETE", "/api/providers/nope/data?confirm=yes", "").Code)
}

func TestStoredSecretsReachTheProviderAndEnvironmentWins(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.secrets.values["NIMBO_POSTGRES_PASSWORD"] = "stored-pw"
	a.do("POST", "/api/providers/postgres/start", "")
	coreEq(t, "stored-pw", a.rt.started[0].env["NIMBO_POSTGRES_PASSWORD"])

	cfg := DefaultConfig()
	cfg.HostEnv = map[string]string{"NIMBO_POSTGRES_PASSWORD": "env-pw"}
	b := newCoreApp(cfg)
	b.secrets.values["NIMBO_POSTGRES_PASSWORD"] = "stored-pw"
	b.do("POST", "/api/providers/postgres/start", "")
	coreEq(t, "env-pw", b.rt.started[0].env["NIMBO_POSTGRES_PASSWORD"])
}

func TestSecretsListNamesAndFlagsNeverValues(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.secrets.values["NIMBO_MINIO_PASSWORD"] = "very-secret-pw"
	body := a.do("GET", "/api/secrets", "").Body
	coreTrue(t, !strings.Contains(body, "very-secret-pw"))
	set := map[string]bool{}
	for _, it := range coreObject(t, body)["items"].([]any) {
		m := it.(map[string]any)
		set[m["name"].(string)] = m["set"].(bool)
	}
	coreTrue(t, set["NIMBO_MINIO_PASSWORD"] && !set["NIMBO_POSTGRES_PASSWORD"])
	coreContains(t, body, `"reason":null`)
}

func TestSecretsSetValidatedGeneratedDeleted(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, 204, a.do("PUT", "/api/secrets/NIMBO_POSTGRES_PASSWORD", `{"value":"hunter2-hunter2"}`).Code)
	coreEq(t, "hunter2-hunter2", a.secrets.values["NIMBO_POSTGRES_PASSWORD"])
	coreEq(t, 400, a.do("PUT", "/api/secrets/NIMBO_MINIO_PASSWORD", `{"value":"short"}`).Code)
	coreEq(t, 404, a.do("PUT", "/api/secrets/HOME", `{"value":"x"}`).Code)
	coreEq(t, 400, a.do("PUT", "/api/secrets/NIMBO_MINIO_PASSWORD", `{}`).Code)

	generated := coreObject(t, a.do("POST", "/api/secrets/NIMBO_MINIO_PASSWORD/generate", "").Body)["value"].(string)
	coreEq(t, generated, a.secrets.values["NIMBO_MINIO_PASSWORD"])
	coreEq(t, 400, a.do("POST", "/api/secrets/NIMBO_MINIO_USER/generate", "").Code, "users are chosen, not generated")
	coreEq(t, 404, a.do("POST", "/api/secrets/HOME/generate", "").Code)

	coreEq(t, 204, a.do("DELETE", "/api/secrets/NIMBO_POSTGRES_PASSWORD", "").Code)
	_, still := a.secrets.values["NIMBO_POSTGRES_PASSWORD"]
	coreTrue(t, !still)
	coreEq(t, 404, a.do("DELETE", "/api/secrets/HOME", "").Code)

	journal := a.do("GET", "/api/stats", "").Body
	coreContains(t, journal, "secret.set")
	coreTrue(t, !strings.Contains(journal, generated) && !strings.Contains(journal, "hunter2-hunter2"))
}

func TestUnavailableStoreSaysWhy(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.secrets.avail = false
	// The test store validates; the real unavailable one refuses with its reason (below).
	b := NewServer(DefaultConfig(), Deps{Runtime: a.rt, Secrets: UnavailableSecretStore{Why: "not here"}})
	coreEq(t, 400, coreDo(b, "PUT", "/api/secrets/NIMBO_MINIO_USER", `{"value":"admin"}`).Code)
	body := coreObject(t, coreDo(b, "GET", "/api/secrets", "").Body)
	coreEq(t, false, body["available"].(bool))
	coreEq(t, "not here", body["reason"].(string))
}

func TestBackupRoundTripThroughTheApi(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.secrets.values["NIMBO_MINIO_USER"] = "admin"
	a.do("PUT", "/api/settings", `{"lang":"en"}`)
	a.do("POST", "/api/progress/import", `{"ids":["lab-a"]}`)
	backup := a.do("GET", "/api/data/export", "").Body
	coreTrue(t, !strings.Contains(backup, "NIMBO_MINIO_USER"))

	coreEq(t, 204, a.do("POST", "/api/data/reset?what=all&confirm=yes", "").Code)
	coreEq(t, 0, len(a.data.Progress.Completed()))
	coreEq(t, 200, a.do("POST", "/api/data/import", backup).Code)
	coreEq(t, "en", *a.data.Settings.Get().Lang)
	coreTrue(t, a.data.Progress.Completed()["lab-a"])

	coreEq(t, 400, a.do("POST", "/api/data/import", `{"format":1,"exportedAt":1,"files":{"settings.json":"nope"}}`).Code)
	coreEq(t, 400, a.do("POST", "/api/data/import", `{"format":1}`).Code)
	coreEq(t, "en", *a.data.Settings.Get().Lang)
}

func TestResetNeedsConfirmAndKnownTarget(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	a.do("POST", "/api/providers/redis/start", "")
	a.do("POST", "/api/progress/import", `{"ids":["lab-a"]}`)
	coreEq(t, 400, a.do("POST", "/api/data/reset?what=stats", "").Code)
	coreEq(t, 400, a.do("POST", "/api/data/reset?what=everything&confirm=yes", "").Code)
	coreEq(t, 204, a.do("POST", "/api/data/reset?what=stats&confirm=yes", "").Code)
	coreEq(t, 0, len(a.data.Stats.Stats().Providers))
	coreTrue(t, a.data.Progress.Completed()["lab-a"], "resetting statistics keeps the progress")
	coreEq(t, 204, a.do("POST", "/api/data/reset?what=all&confirm=yes", "").Code)
	coreEq(t, 0, len(a.data.Progress.Completed()))
}

func TestDataInfo(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Distribution = "docker"
	a := newCoreApp(cfg)
	info := coreObject(t, a.do("GET", "/api/data", "").Body)
	coreEq(t, "docker", info["distribution"].(string))
	coreTrue(t, info["location"] == nil)
}

func TestTokenProtectsDataRoutes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIToken = "tok"
	a := newCoreApp(cfg)
	for _, p := range []string{"/api/settings", "/api/stats", "/api/secrets", "/api/data", "/api/data/export", "/api/progress"} {
		coreEq(t, 401, a.do("GET", p, "").Code, p)
	}
	coreEq(t, 200, a.do("GET", "/api/secrets", "", "Authorization", "Bearer tok").Code)
}

func TestForeignHostRefusedBeforeAnythingIsRead(t *testing.T) {
	a := newCoreApp(DefaultConfig())
	coreEq(t, 403, a.do("GET", "/api/secrets", "", "Host", "evil.example").Code)
}
