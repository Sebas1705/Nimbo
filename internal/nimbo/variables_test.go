package nimbo

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"
)

var varsCtx = context.Background()

func TestEveryProviderStatesItsVariables(t *testing.T) {
	want := map[string][]string{
		"minio":    {"NIMBO_MINIO_USER", "NIMBO_MINIO_PASSWORD"},
		"postgres": {"NIMBO_POSTGRES_PASSWORD", "NIMBO_POSTGRES_USER", "NIMBO_POSTGRES_DB"},
		"redis":    {"NIMBO_REDIS_PASSWORD"},
		"k3s":      {"NIMBO_K3S_TOKEN"},
		"azurite":  {"NIMBO_AZURITE_ACCOUNTS"},
	}
	for _, def := range CatalogProviders {
		var names []string
		for _, v := range def.Vars {
			names = append(names, v.Name)
			coreTrue(t, strings.HasPrefix(v.Name, "NIMBO_"), def.ID, v.Name)
			coreTrue(t, v.Container != "", def.ID+" "+v.Name+" must reach the container")
			// What Initialize makes must be accepted by the same validation the editor uses.
			value, err := GenerateFor(v)
			if err != nil {
				t.Fatal(err)
			}
			if value == "" {
				value = v.Default
			}
			coreTrue(t, value != "", def.ID+" "+v.Name+" has neither a generator nor a default")
			coreTrue(t, ValidateSecret(v.Name, value) == nil, def.ID, v.Name, value)
		}
		coreEq(t, strings.Join(want[def.ID], ","), strings.Join(names, ","), def.ID)
	}
}

func TestSecretsAreMarkedAndUsersAreNot(t *testing.T) {
	for _, n := range []string{"NIMBO_MINIO_PASSWORD", "NIMBO_POSTGRES_PASSWORD", "NIMBO_REDIS_PASSWORD", "NIMBO_K3S_TOKEN", "NIMBO_AZURITE_ACCOUNTS"} {
		coreTrue(t, IsSecretPassword(n), n)
	}
	for _, n := range []string{"NIMBO_MINIO_USER", "NIMBO_POSTGRES_USER", "NIMBO_POSTGRES_DB"} {
		coreTrue(t, !IsSecretPassword(n), n)
	}
}

func TestNewValidations(t *testing.T) {
	bad := [][2]string{
		{"NIMBO_REDIS_PASSWORD", "short"}, {"NIMBO_REDIS_PASSWORD", "has a space in it"},
		{"NIMBO_POSTGRES_USER", "Has-Caps"}, {"NIMBO_POSTGRES_DB", "1starts_with_digit"}, {"NIMBO_POSTGRES_DB", "a;drop table"},
		{"NIMBO_AZURITE_ACCOUNTS", "nokey"}, {"NIMBO_AZURITE_ACCOUNTS", "ab:AAAAAAAAAAAAAAAAAAAA"}, {"NIMBO_AZURITE_ACCOUNTS", "good1:AAAAAAAAAAAAAAAAAAAA;bad"},
	}
	for _, c := range bad {
		_, isBad := ValidateSecret(c[0], c[1]).(InvalidParam)
		coreTrue(t, isBad, c[0], c[1])
	}
	good := [][2]string{
		{"NIMBO_REDIS_PASSWORD", "eight-chars"}, {"NIMBO_POSTGRES_USER", "app_user"}, {"NIMBO_POSTGRES_DB", "_db1"},
		{"NIMBO_AZURITE_ACCOUNTS", "acct1:AAAAAAAAAAAAAAAAAAAA"}, {"NIMBO_AZURITE_ACCOUNTS", "acct1:AAAAAAAAAAAAAAAAAAAA;acct2:BBBBBBBBBBBBBBBBBBBB:CCCCCCCCCCCCCCCCCCCC"},
	}
	for _, c := range good {
		coreTrue(t, ValidateSecret(c[0], c[1]) == nil, c[0], c[1])
	}
}

func TestGeneratedValuesHaveTheRightShape(t *testing.T) {
	token, _ := GenerateFor(*varSpecByName("NIMBO_K3S_TOKEN"))
	coreEq(t, 48, len(token))
	acct, _ := GenerateFor(*varSpecByName("NIMBO_AZURITE_ACCOUNTS"))
	name, key, ok := strings.Cut(acct, ":")
	coreTrue(t, ok && name == "nimbo")
	raw, err := base64.StdEncoding.DecodeString(key)
	coreTrue(t, err == nil && len(raw) == 64, "the key is 64 bytes of base64")
	a, _ := GenerateFor(*varSpecByName("NIMBO_REDIS_PASSWORD"))
	b, _ := GenerateFor(*varSpecByName("NIMBO_REDIS_PASSWORD"))
	coreTrue(t, a != b && len(a) == 24)
	none, _ := GenerateFor(*varSpecByName("NIMBO_MINIO_USER"))
	coreEq(t, "", none, "a user name is not generated")
}

func TestResolveVarsPrefersTheEnvironment(t *testing.T) {
	store := newCoreMemorySecrets()
	store.values["NIMBO_MINIO_USER"] = "stored-user"
	store.values["NIMBO_MINIO_PASSWORD"] = "stored-password"
	rv := ResolveVars(varsCtx, *CatalogFind("minio"), map[string]string{"NIMBO_MINIO_USER": "env-user"}, store)
	coreEq(t, "env-user", rv[0].Value)
	coreEq(t, string(SourceEnvironment), string(rv[0].Source))
	coreEq(t, "stored-password", rv[1].Value)
	coreEq(t, string(SourceStored), string(rv[1].Source))
	un := ResolveVars(varsCtx, *CatalogFind("redis"), nil, store)
	coreEq(t, string(SourceUnset), string(un[0].Source))
}

func TestInitializeFillsOnlyWhatIsMissing(t *testing.T) {
	store := newCoreMemorySecrets()
	store.values["NIMBO_POSTGRES_USER"] = "mine"
	pg := *CatalogFind("postgres")

	created, err := InitializeVars(varsCtx, pg, nil, store, false)
	coreTrue(t, err == nil)
	coreEq(t, "NIMBO_POSTGRES_PASSWORD", strings.Join(created, ","), "only the required one without optional=true")
	coreEq(t, "mine", store.values["NIMBO_POSTGRES_USER"], "an existing value is never overwritten")

	again, _ := InitializeVars(varsCtx, pg, nil, store, false)
	coreEq(t, 0, len(again), "a second run changes nothing")

	opt, _ := InitializeVars(varsCtx, pg, nil, store, true)
	coreEq(t, "NIMBO_POSTGRES_DB", strings.Join(opt, ","))
	coreEq(t, "nimbo", store.values["NIMBO_POSTGRES_DB"])

	// A value that comes from the environment is respected too.
	redis := *CatalogFind("redis")
	none, _ := InitializeVars(varsCtx, redis, map[string]string{"NIMBO_REDIS_PASSWORD": "from-the-env"}, store, true)
	coreEq(t, 0, len(none))
}

func TestInitializeSaysWhyItCannot(t *testing.T) {
	_, err := InitializeVars(varsCtx, *CatalogFind("minio"), nil, UnavailableSecretStore{Why: "no store here"}, false)
	_, bad := err.(InvalidParam)
	coreTrue(t, bad && strings.Contains(err.Error(), "no store here"))
}

func TestStartNeedsOnlyTheRequiredOnesAndSendsTheOptionalOnesWhenSet(t *testing.T) {
	pg := *CatalogFind("postgres")
	args := BuildRunArgs(pg, map[string]string{"NIMBO_POSTGRES_PASSWORD": "pw-123456"}, false)
	coreTrue(t, coreHas(args, "POSTGRES_PASSWORD=pw-123456"))
	for _, a := range args {
		coreTrue(t, !strings.HasPrefix(a, "POSTGRES_USER="), "an unset optional variable is not sent")
	}
	full := BuildRunArgs(pg, map[string]string{"NIMBO_POSTGRES_PASSWORD": "pw-123456", "NIMBO_POSTGRES_USER": "app", "NIMBO_POSTGRES_DB": "shop"}, false)
	coreTrue(t, coreHas(full, "POSTGRES_USER=app") && coreHas(full, "POSTGRES_DB=shop"))

	redis := BuildRunArgs(*CatalogFind("redis"), map[string]string{"NIMBO_REDIS_PASSWORD": "redis-pass-1"}, false)
	coreTrue(t, coreHas(redis, "REDIS_PASSWORD=redis-pass-1"))
	for _, a := range redis {
		if strings.HasPrefix(a, "exec redis-server") {
			coreTrue(t, !strings.Contains(a, "redis-pass-1"), "the password must not be in the command line")
		}
	}
}

func TestClientVariablesFollowTheSettings(t *testing.T) {
	store := newCoreMemorySecrets()
	store.values["NIMBO_POSTGRES_PASSWORD"] = "s3cret-pw"
	store.values["NIMBO_POSTGRES_USER"] = "app"
	store.values["NIMBO_POSTGRES_DB"] = "shop"
	pg := *CatalogFind("postgres")
	rvs := ResolveVars(varsCtx, pg, nil, store)

	hidden := clientVars(pg, rvs, false)
	text := ""
	for _, kv := range hidden {
		text += kv.Key + "=" + kv.Value + "\n"
	}
	coreTrue(t, !strings.Contains(text, "s3cret-pw"), "secrets are placeholders until revealed")
	coreContains(t, text, "DATABASE_URL=postgres://app:<NIMBO_POSTGRES_PASSWORD>@localhost:5432/shop")

	shown := clientVars(pg, rvs, true)
	got := map[string]string{}
	for _, kv := range shown {
		got[kv.Key] = kv.Value
	}
	coreEq(t, "postgres://app:s3cret-pw@localhost:5432/shop", got["DATABASE_URL"])
	coreEq(t, "5432", got["DATABASE_PORT"], "the fixed endpoints stay")

	redis := *CatalogFind("redis")
	coreEq(t, "redis://localhost:6379", func() string {
		return clientVarsMap(clientVars(redis, ResolveVars(varsCtx, redis, nil, store), true))["REDIS_URL"]
	}())
	store.values["NIMBO_REDIS_PASSWORD"] = "redis-pass-1"
	coreEq(t, "redis://:redis-pass-1@localhost:6379", clientVarsMap(clientVars(redis, ResolveVars(varsCtx, redis, nil, store), true))["REDIS_URL"])

	az := *CatalogFind("azurite")
	store.values["NIMBO_AZURITE_ACCOUNTS"] = "acct1:AAAAAAAAAAAAAAAAAAAA"
	cs := clientVarsMap(clientVars(az, ResolveVars(varsCtx, az, nil, store), true))["AZURE_STORAGE_CONNECTION_STRING"]
	coreContains(t, cs, "AccountName=acct1;AccountKey=AAAAAAAAAAAAAAAAAAAA;")
	coreContains(t, cs, "BlobEndpoint=http://127.0.0.1:10000/acct1")
}

func clientVarsMap(v Vars) map[string]string {
	m := map[string]string{}
	for _, kv := range v {
		m[kv.Key] = kv.Value
	}
	return m
}

func varsServer(t *testing.T) (h http.Handler, rt *coreFakeRuntime, store *coreMemorySecrets, data *AppData) {
	t.Helper()
	rt, store, data = newCoreFakeRuntime(), newCoreMemorySecrets(), NewAppData(nil, nil)
	h = coreTestServer(Config{HostEnv: map[string]string{}}, Deps{Runtime: rt, Secrets: store, Data: data})
	return
}

func TestVariablesListNeverCarriesValues(t *testing.T) {
	h, _, store, _ := varsServer(t)
	store.values["NIMBO_REDIS_PASSWORD"] = "very-secret-redis"
	r := coreDo(h, "GET", "/api/variables", "")
	coreEq(t, 200, r.Code)
	coreTrue(t, !strings.Contains(r.Body, "very-secret-redis"))
	var dto VariablesDto
	coreTrue(t, jsonUnmarshal(r.Body, &dto) == nil)
	coreEq(t, len(CatalogProviders), len(dto.Providers), "every emulator is listed, also the ones with nothing to set")
	byID := map[string]ProviderVarsDto{}
	for _, p := range dto.Providers {
		byID[p.Provider] = p
	}
	coreEq(t, 0, len(byID["floci-aws"].Vars))
	coreEq(t, 1, len(byID["redis"].Vars))
	coreEq(t, "stored", byID["redis"].Vars[0].Source)
	coreTrue(t, byID["redis"].Vars[0].Set && byID["redis"].Vars[0].Secret && byID["redis"].Vars[0].CanGenerate)
	coreTrue(t, byID["postgres"].Vars[0].FixedAtCreation)
	coreTrue(t, byID["minio"].Vars[0].Default != nil && *byID["minio"].Vars[0].Default == "nimbo", "a user default is shown, a secret one never")
}

func TestInitializeOverHTTPThenStartWorks(t *testing.T) {
	h, rt, store, data := varsServer(t)
	r := coreDo(h, "POST", "/api/providers/postgres/variables/init", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, "NIMBO_POSTGRES_PASSWORD")
	coreTrue(t, !strings.Contains(r.Body, store.values["NIMBO_POSTGRES_PASSWORD"]), "the values never travel in the answer")

	r = coreDo(h, "POST", "/api/providers/postgres/start", "")
	coreEq(t, 202, r.Code)
	coreEq(t, store.values["NIMBO_POSTGRES_PASSWORD"], rt.started[len(rt.started)-1].env["NIMBO_POSTGRES_PASSWORD"])
	coreEq(t, 1, len(store.values), "optional ones are left alone by default")

	r = coreDo(h, "POST", "/api/providers/postgres/variables/init?optional=true", "")
	coreEq(t, 200, r.Code)
	coreEq(t, 3, len(store.values))
	coreTrue(t, len(data.Stats.Journal(10)) > 0)
	coreEq(t, 404, coreDo(h, "POST", "/api/providers/nope/variables/init", "").Code)
}

func TestRevealIsDeliberateAndLeavesATrace(t *testing.T) {
	h, _, store, data := varsServer(t)
	store.values["NIMBO_REDIS_PASSWORD"] = "reveal-me-please"
	coreEq(t, 404, coreDo(h, "POST", "/api/secrets/NIMBO_K3S_TOKEN/reveal", "").Code, "nothing to reveal yet")
	coreEq(t, 404, coreDo(h, "POST", "/api/secrets/HOME/reveal", "").Code)
	coreEq(t, 404, coreDo(h, "GET", "/api/secrets/NIMBO_REDIS_PASSWORD/reveal", "").Code, "a GET never reveals")

	r := coreDo(h, "POST", "/api/secrets/NIMBO_REDIS_PASSWORD/reveal", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, "reveal-me-please")
	coreContains(t, r.Body, `"source":"stored"`)
	journal := ""
	for _, e := range data.Stats.Journal(10) {
		journal += e.Kind + " " + e.Subject + " " + func() string {
			if e.Detail != nil {
				return *e.Detail
			}
			return ""
		}() + "\n"
	}
	coreContains(t, journal, "secret.reveal NIMBO_REDIS_PASSWORD")
	coreTrue(t, !strings.Contains(journal, "reveal-me-please"), "the journal holds the name, never the value")
}

func TestEnvRouteHidesSecretsUnlessAsked(t *testing.T) {
	h, _, store, _ := varsServer(t)
	store.values["NIMBO_POSTGRES_PASSWORD"] = "pg-secret-value"
	r := coreDo(h, "GET", "/api/providers/postgres/env", "")
	coreEq(t, 200, r.Code)
	coreTrue(t, !strings.Contains(r.Body, "pg-secret-value"))
	coreContains(t, r.Body, "<NIMBO_POSTGRES_PASSWORD>")
	r = coreDo(h, "GET", "/api/providers/postgres/env?secrets=true", "")
	coreContains(t, r.Body, "pg-secret-value")
	coreContains(t, r.Body, "DATABASE_URL")
}

func TestApplyRecreatesARunningEmulatorWithTheNewValues(t *testing.T) {
	h, rt, store, _ := varsServer(t)
	store.values["NIMBO_REDIS_PASSWORD"] = "first-password"
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/redis/start", "").Code)

	var out VarsApplyDto
	coreEq(t, 200, coreDo(h, "POST", "/api/providers/redis/apply", "").Code)
	store.values["NIMBO_REDIS_PASSWORD"] = "second-password"
	r := coreDo(h, "POST", "/api/providers/redis/apply", "")
	coreEq(t, 200, r.Code)
	coreTrue(t, jsonUnmarshal(r.Body, &out) == nil && out.Applied)
	last := rt.started[len(rt.started)-1]
	coreEq(t, "second-password", last.env["NIMBO_REDIS_PASSWORD"])
	coreEq(t, "start:redis", rt.calls[len(rt.calls)-1])
	coreEq(t, 3, len(rt.started), "start, and one recreation per apply")
}

func TestApplyWithNothingRunningOnlyRemovesAndDoesNotStart(t *testing.T) {
	h, rt, _, _ := varsServer(t)
	r := coreDo(h, "POST", "/api/providers/redis/apply", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, `"applied":false`)
	coreEq(t, 0, len(rt.started))

	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: false, Status: "Exited"}
	r = coreDo(h, "POST", "/api/providers/redis/apply", "")
	coreEq(t, 200, r.Code)
	coreEq(t, 0, len(rt.started), "a stopped emulator is not started by apply")
	_, still := rt.live["redis"]
	coreTrue(t, !still, "but its old container is gone, so the next start uses the new values")
}

func TestApplyWarnsWhenKeptDataFixesTheSettings(t *testing.T) {
	h, _, store, data := varsServer(t)
	store.values["NIMBO_POSTGRES_PASSWORD"] = "pg-password-1"
	_, _ = data.Settings.SetPersist("postgres", true)
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/postgres/start", "").Code)
	r := coreDo(h, "POST", "/api/providers/postgres/apply", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, "only apply when that data is first created")
}

func TestSecretsRoutesAcceptTheNewSettings(t *testing.T) {
	h, _, store, _ := varsServer(t)
	coreEq(t, 204, coreDo(h, "PUT", "/api/secrets/NIMBO_POSTGRES_USER", `{"value":"app_user"}`).Code)
	coreEq(t, "app_user", store.values["NIMBO_POSTGRES_USER"])
	coreEq(t, 400, coreDo(h, "PUT", "/api/secrets/NIMBO_POSTGRES_USER", `{"value":"Bad User"}`).Code)
	coreEq(t, 200, coreDo(h, "POST", "/api/secrets/NIMBO_K3S_TOKEN/generate", "").Code)
	coreEq(t, 48, len(store.values["NIMBO_K3S_TOKEN"]))
	coreEq(t, 200, coreDo(h, "POST", "/api/secrets/NIMBO_AZURITE_ACCOUNTS/generate", "").Code)
	coreTrue(t, ValidateSecret("NIMBO_AZURITE_ACCOUNTS", store.values["NIMBO_AZURITE_ACCOUNTS"]) == nil)
	coreEq(t, 400, coreDo(h, "POST", "/api/secrets/NIMBO_MINIO_USER/generate", "").Code, "a user name is chosen, not generated")
}

func TestProviderListSaysWhatStillNeedsSetupWithoutDecrypting(t *testing.T) {
	h, _, store, _ := varsServer(t)
	listed := func() map[string][]string {
		var out []ProviderDto
		coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/providers", "").Body, &out) == nil)
		m := map[string][]string{}
		for _, p := range out {
			m[p.ID] = p.NeedsSetup
		}
		return m
	}
	before := listed()
	coreEq(t, "NIMBO_POSTGRES_PASSWORD", strings.Join(before["postgres"], ","))
	coreEq(t, "NIMBO_MINIO_USER,NIMBO_MINIO_PASSWORD", strings.Join(before["minio"], ","))
	coreEq(t, 0, len(before["redis"]), "an optional password is never required")
	coreEq(t, 0, len(before["floci-aws"]))
	store.values["NIMBO_POSTGRES_PASSWORD"] = "pg-password-1"
	coreEq(t, 0, len(listed()["postgres"]))
}

func TestVariableNamesAreAllExplainedInTheGuides(t *testing.T) {
	b, err := os.ReadFile(coreRepoFile(t, "web/src/guides.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range SecretNames {
		coreTrue(t, strings.Contains(string(b), n), "guides.ts does not explain "+n)
	}
}
