package nimbo

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const regKeyName = "NIMBO_C_MY_API_API_KEY"

// imgRegServer is the whole API with an image registry mounted, over an in-memory data folder and the fake runtime. The
// registered images are process-wide, so each test clears them when it ends.
func imgRegServer(t *testing.T) (h http.Handler, rt *coreFakeRuntime, store *coreMemorySecrets, data *AppData, reg *ImageRegistry) {
	t.Helper()
	SetCustomProviders(nil)
	t.Cleanup(func() { SetCustomProviders(nil) })
	rt, store, data = newCoreFakeRuntime(), newCoreMemorySecrets(), NewAppData(nil, nil)
	reg = NewImageRegistry(data.Store, data, rt, store, map[string]string{})
	h = coreTestServer(Config{HostEnv: map[string]string{}}, Deps{Runtime: rt, Secrets: store, Data: data, Routes: []func(*http.ServeMux){reg.Register}})
	return
}

const regAPI = `{"name":"My API","image":"ghcr.io/me/api:1.2","ports":[8081],"env":[{"key":"MODE","value":"test"}],` +
	`"secretKeys":["API_KEY"],"command":["serve","--dev"],"volume":"/data","notes":"my service"}`

func regPut(h http.Handler, id, body string) coreResp {
	return coreDo(h, "PUT", "/api/registry/images/"+id, body)
}

func regJournal(data *AppData) string {
	var b strings.Builder
	for _, e := range data.Stats.Journal(200) {
		b.WriteString(e.Kind + " " + e.Subject + " ")
		if e.Detail != nil {
			b.WriteString(*e.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestRegistryImageValidation(t *testing.T) {
	h, _, _, _, _ := imgRegServer(t)
	long := strings.Repeat("x", 501)
	cases := []struct{ name, id, body, want string }{
		{"bad id", "Bad_ID", regAPI, "lowercase"},
		{"built-in id", "redis", regAPI, "comes with Nimbo"},
		{"no image", "a", `{"name":"A"}`, "image must look like"},
		{"image with a space", "a", `{"image":"x y"}`, "image must look like"},
		{"image starting with a dash", "a", `{"image":"-v /:/h"}`, "image must look like"},
		{"upper case image", "a", `{"image":"Foo/Bar"}`, "image must look like"},
		{"low port", "a", `{"image":"x","ports":[80]}`, "between 1024 and 65535"},
		{"high port", "a", `{"image":"x","ports":[70000]}`, "between 1024 and 65535"},
		{"repeated port", "a", `{"image":"x","ports":[40000,40000]}`, "twice"},
		{"too many ports", "a", `{"image":"x","ports":[20001,20002,20003,20004,20005,20006,20007,20008,20009,20010,20011]}`, "At most 10 ports"},
		{"built-in port", "a", `{"image":"x","ports":[6379]}`, "already used by Redis"},
		{"toxiproxy port", "a", `{"image":"x","ports":[14566]}`, "already used by Toxiproxy"},
		{"bad env key", "a", `{"image":"x","env":[{"key":"1BAD","value":"v"}]}`, "variable name"},
		{"env key with a dash", "a", `{"image":"x","env":[{"key":"A-B","value":"v"}]}`, "variable name"},
		{"env value too long", "a", `{"image":"x","env":[{"key":"A","value":"` + long + `"}]}`, "at most 500"},
		{"env value with a control character", "a", `{"image":"x","env":[{"key":"A","value":"a\nb"}]}`, "control characters"},
		{"repeated env key", "a", `{"image":"x","env":[{"key":"A","value":"1"},{"key":"A","value":"2"}]}`, "used twice"},
		{"key in both lists", "a", `{"image":"x","env":[{"key":"A","value":"1"}],"secretKeys":["A"]}`, "used twice"},
		{"bad secret key", "a", `{"image":"x","secretKeys":["bad key"]}`, "secret key"},
		{"command too long", "a", `{"image":"x","command":["` + strings.Repeat("c", 201) + `"]}`, "at most 200"},
		{"relative volume", "a", `{"image":"x","volume":"data"}`, "absolute folder"},
		{"volume with dots and spaces", "a", `{"image":"x","volume":"/da ta"}`, "absolute folder"},
		{"name too long", "a", `{"image":"x","name":"` + strings.Repeat("n", 61) + `"}`, "at most 60"},
		{"notes too long", "a", `{"image":"x","notes":"` + long + `"}`, "at most 500"},
		{"no body", "a", ``, "not valid JSON"},
	}
	for _, c := range cases {
		r := regPut(h, c.id, c.body)
		coreEq(t, 400, r.Code, c.name)
		coreContains(t, r.Body, c.want)
	}
	many := make([]string, 31)
	for i := range many {
		many[i] = `{"key":"K` + strings.Repeat("a", i) + `","value":"v"}`
	}
	r := regPut(h, "a", `{"image":"x","env":[`+strings.Join(many, ",")+`]}`)
	coreEq(t, 400, r.Code)
	coreContains(t, r.Body, "At most 30 variables")
	cmd := strings.TrimSuffix(strings.Repeat(`"c",`, 21), ",")
	coreContains(t, regPut(h, "a", `{"image":"x","command":[`+cmd+`]}`).Body, "at most 20 items")
	// Nothing invalid was kept.
	coreEq(t, "[]\n", coreDo(h, "GET", "/api/registry/images", "").Body)
}

func TestRegistryImageLifecycleOverHTTP(t *testing.T) {
	h, _, _, data, _ := imgRegServer(t)
	coreEq(t, "[]\n", coreDo(h, "GET", "/api/registry/images", "").Body, "lists are never null")

	r := regPut(h, "my-api", regAPI)
	coreEq(t, 200, r.Code)
	o := coreObject(t, r.Body)
	coreEq(t, "my-api", o["id"].(string))
	coreEq(t, "My API", o["name"].(string))
	coreEq(t, true, o["custom"].(bool))
	coreEq(t, "/data", o["volume"].(string))
	secrets := o["secrets"].([]any)
	coreEq(t, 1, len(secrets))
	s0 := secrets[0].(map[string]any)
	coreEq(t, "API_KEY", s0["key"].(string))
	coreEq(t, regKeyName, s0["name"].(string))
	coreEq(t, false, s0["set"].(bool))

	var list []ImageDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/registry/images", "").Body, &list) == nil)
	coreEq(t, 1, len(list))
	coreEq(t, "ghcr.io/me/api:1.2", list[0].Image)

	// It is one more provider, marked as your own, next to the built-in ones.
	var providers []ProviderDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/providers", "").Body, &providers) == nil)
	coreEq(t, len(CatalogProviders)+1, len(providers))
	last := providers[len(providers)-1]
	coreEq(t, "my-api", last.ID)
	coreTrue(t, last.Custom && last.Persistable && last.State == "not_created" && len(last.NeedsSetup) == 0)
	coreTrue(t, !providers[0].Custom)

	// Replace: other port, no volume, no secret keys, the volume null.
	r = regPut(h, "my-api", `{"name":"","image":"ghcr.io/me/api:2","ports":[8082]}`)
	coreEq(t, 200, r.Code)
	o = coreObject(t, r.Body)
	coreEq(t, "my-api", o["name"].(string), "an empty name falls back to the id")
	coreTrue(t, o["volume"] == nil, "volume is sent as null")
	coreEq(t, 0, len(o["secrets"].([]any)))
	coreEq(t, 0, len(o["env"].([]any)))
	coreEq(t, 0, len(o["command"].([]any)))
	coreTrue(t, FindProvider("my-api").Persist == nil)
	coreEq(t, 1, len(coreObjectList(t, coreDo(h, "GET", "/api/registry/images", "").Body)), "replacing does not duplicate")

	coreEq(t, 204, coreDo(h, "DELETE", "/api/registry/images/my-api", "").Code)
	coreEq(t, "[]\n", coreDo(h, "GET", "/api/registry/images", "").Body)
	coreEq(t, 404, coreDo(h, "DELETE", "/api/registry/images/my-api", "").Code)
	coreTrue(t, FindProvider("my-api") == nil, "gone from every provider list")
	coreEq(t, 404, coreDo(h, "POST", "/api/providers/my-api/start", "").Code)
	j := regJournal(data)
	coreContains(t, j, "registry.image.save my-api")
	coreContains(t, j, "registry.image.delete my-api")
}

// regExport asks for an export and returns the text of the file it carries.
func regExport(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	r := coreDo(h, "GET", path, "")
	coreEq(t, 200, r.Code, path)
	var dto ExportDto
	if err := jsonUnmarshal(r.Body, &dto); err != nil {
		t.Fatal(err)
	}
	return dto.Content
}

func coreObjectList(t *testing.T, body string) []any {
	t.Helper()
	var l []any
	if err := jsonUnmarshal(body, &l); err != nil {
		t.Fatalf("not a JSON list: %v: %s", err, body)
	}
	return l
}

func TestACustomImageStartsThroughTheRuntimeWithItsEnvAndSecret(t *testing.T) {
	h, rt, store, _, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)

	coreEq(t, 204, coreDo(h, "PUT", "/api/secrets/"+regKeyName, `{"value":"super-secret-key-1"}`).Code)
	coreEq(t, "super-secret-key-1", store.values[regKeyName])
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/start", "").Code)
	st := rt.started[len(rt.started)-1]
	coreEq(t, "my-api", st.id)
	coreEq(t, "super-secret-key-1", st.env[regKeyName], "the secret reaches the start")
	coreTrue(t, !st.persist, "data is kept only when asked")

	// What docker is asked to run: loopback port, fixed env, the secret under its own name inside the container, command.
	def := *FindProvider("my-api")
	args := BuildRunArgs(def, st.env, false)
	line := strings.Join(args, " ")
	coreContains(t, line, "--name nimbo-my-api")
	coreContains(t, line, "-p 127.0.0.1:8081:8081")
	coreContains(t, line, "-e MODE=test")
	coreContains(t, line, "-e API_KEY=super-secret-key-1")
	coreTrue(t, strings.HasSuffix(line, "ghcr.io/me/api:1.2 serve --dev"), line)
	coreTrue(t, !strings.Contains(line, "--privileged") && !strings.Contains(line, "-v "), "no privileged mode and no mounts")

	// Without a value the key is simply not handed over.
	delete(store.values, regKeyName)
	bare := strings.Join(BuildRunArgs(def, map[string]string{}, false), " ")
	coreTrue(t, !strings.Contains(bare, "API_KEY"), bare)

	// Stop and the logs work like for any provider.
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/stop", "").Code)
	coreContains(t, coreDo(h, "GET", "/api/providers/my-api/logs?tail=5", "").Body, "line-my-api-5")
}

func TestPersistedVolumeArgsOfACustomImage(t *testing.T) {
	h, rt, _, _, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	def := *FindProvider("my-api")
	on := strings.Join(BuildRunArgs(def, nil, true), " ")
	coreContains(t, on, "-v nimbo-my-api-data:/data")
	coreContains(t, on, "--label nimbo.persist=true")
	off := strings.Join(BuildRunArgs(def, nil, false), " ")
	coreTrue(t, !strings.Contains(off, "nimbo-my-api-data"), off)

	// The keep-data switch works for it, and the start carries the choice.
	coreEq(t, 200, coreDo(h, "POST", "/api/providers/my-api/persist", `{"enabled":true}`).Code)
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/start", "").Code)
	coreTrue(t, rt.started[len(rt.started)-1].persist)

	// An image without a volume has nothing to keep.
	coreEq(t, 200, regPut(h, "plain", `{"image":"x/plain","ports":[8090]}`).Code)
	coreEq(t, 400, coreDo(h, "POST", "/api/providers/plain/persist", `{"enabled":true}`).Code)
}

func TestSecretsOfACustomImageAreListedRevealedAndInitialized(t *testing.T) {
	h, _, store, data, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)

	// Settings > secrets lists the dynamic name, unset, with the image as its user.
	var secrets SecretsDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/secrets", "").Body, &secrets) == nil)
	var item *SecretItemDto
	for i := range secrets.Items {
		if secrets.Items[i].Name == regKeyName {
			item = &secrets.Items[i]
		}
	}
	coreTrue(t, item != nil && !item.Set && item.Password && strings.Join(item.UsedBy, ",") == "my-api")
	coreEq(t, len(SecretNames)+1, len(secrets.Items))

	// Variables screen: one optional secret that can be generated.
	var vars VariablesDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/variables", "").Body, &vars) == nil)
	coreEq(t, len(CatalogProviders)+1, len(vars.Providers))
	mine := vars.Providers[len(vars.Providers)-1]
	coreEq(t, "my-api", mine.Provider)
	coreEq(t, 1, len(mine.Vars))
	coreTrue(t, mine.Vars[0].Secret && !mine.Vars[0].Required && mine.Vars[0].CanGenerate && !mine.Vars[0].Set && mine.Vars[0].Container == "API_KEY")

	// Values: length and control characters only.
	coreEq(t, 400, coreDo(h, "PUT", "/api/secrets/"+regKeyName, `{"value":""}`).Code)
	coreEq(t, 400, coreDo(h, "PUT", "/api/secrets/"+regKeyName, `{"value":"a\nb"}`).Code)
	coreEq(t, 404, coreDo(h, "PUT", "/api/secrets/NIMBO_C_NOPE_KEY", `{"value":"x"}`).Code, "a name no image reads is unknown")
	coreEq(t, 404, coreDo(h, "POST", "/api/secrets/"+regKeyName+"/reveal", "").Code, "nothing to reveal yet")

	// Initialize: optional ones only when asked, and the value never travels in the answer.
	r := coreDo(h, "POST", "/api/providers/my-api/variables/init", "")
	coreEq(t, 200, r.Code)
	coreEq(t, 0, len(store.values), "an optional secret is left alone by default")
	r = coreDo(h, "POST", "/api/providers/my-api/variables/init?optional=true", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, regKeyName)
	value := store.values[regKeyName]
	coreTrue(t, value != "" && !strings.Contains(r.Body, value))

	// Reveal is deliberate and leaves the name, never the value, in the journal.
	r = coreDo(h, "POST", "/api/secrets/"+regKeyName+"/reveal", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, value)
	coreContains(t, regJournal(data), "secret.reveal "+regKeyName)
	coreTrue(t, !strings.Contains(regJournal(data), value))

	// The registry now says it is set; generating again replaces it and returns the new one.
	var list []ImageDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/registry/images", "").Body, &list) == nil)
	coreTrue(t, list[0].Secrets[0].Set)
	r = coreDo(h, "POST", "/api/secrets/"+regKeyName+"/generate", "")
	coreEq(t, 200, r.Code)
	coreTrue(t, store.values[regKeyName] != value)

	coreEq(t, 204, coreDo(h, "DELETE", "/api/secrets/"+regKeyName, "").Code)
	_, still := store.values[regKeyName]
	coreTrue(t, !still)
}

func TestDeleteIsRefusedWhileTheContainerExists(t *testing.T) {
	h, rt, _, _, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/start", "").Code)

	r := coreDo(h, "DELETE", "/api/registry/images/my-api", "")
	coreEq(t, 409, r.Code, "running")
	coreContains(t, r.Body, "remove it first")
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/stop", "").Code)
	coreEq(t, 409, coreDo(h, "DELETE", "/api/registry/images/my-api", "").Code, "a stopped container still counts")
	coreEq(t, 1, len(coreObjectList(t, coreDo(h, "GET", "/api/registry/images", "").Body)), "still registered")

	coreEq(t, 204, coreDo(h, "DELETE", "/api/providers/my-api", "").Code)
	rt.saved["my-api"] = true // its data volume is not deleted along with the registration
	coreEq(t, 204, coreDo(h, "DELETE", "/api/registry/images/my-api", "").Code)
	coreTrue(t, rt.saved["my-api"] && len(rt.deleted) == 0, "the data volume is left alone")
}

func TestPortsCannotCollideWithBuiltInsOrOtherImages(t *testing.T) {
	h, _, _, _, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "one", `{"image":"x/one","ports":[8081,8082]}`).Code)
	r := regPut(h, "two", `{"image":"x/two","ports":[8082]}`)
	coreEq(t, 400, r.Code)
	coreContains(t, r.Body, "already used by one")
	coreEq(t, 400, regPut(h, "two", `{"image":"x/two","ports":[4566]}`).Code)
	coreEq(t, 400, regPut(h, "two", `{"image":"x/two","ports":[30080]}`).Code, "k3s node ports")
	// Replacing an image keeps its own ports, and frees the ones it gives up.
	coreEq(t, 200, regPut(h, "one", `{"image":"x/one","ports":[8081,8082]}`).Code)
	coreEq(t, 200, regPut(h, "one", `{"image":"x/one","ports":[8081]}`).Code)
	coreEq(t, 200, regPut(h, "two", `{"image":"x/two","ports":[8082]}`).Code)
	coreEq(t, 400, regPut(h, "one", `{"image":"x/one","ports":[8082]}`).Code)
	// The same id twice is a replacement, never a second image.
	coreEq(t, 2, len(coreObjectList(t, coreDo(h, "GET", "/api/registry/images", "").Body)))
}

func TestTwoImagesNeverShareASecretName(t *testing.T) {
	h, _, _, _, _ := imgRegServer(t)
	coreEq(t, "NIMBO_C_A_B_K", CustomSecretName("a-b", "K"))
	coreEq(t, "NIMBO_C_A_B_K", CustomSecretName("a", "B_k"))
	coreEq(t, 200, regPut(h, "a-b", `{"image":"x/a","secretKeys":["K"]}`).Code)
	r := regPut(h, "a", `{"image":"x/b","secretKeys":["B_K"]}`)
	coreEq(t, 400, r.Code)
	coreContains(t, r.Body, "NIMBO_C_A_B_K")
	coreEq(t, 200, regPut(h, "a", `{"image":"x/b","secretKeys":["OTHER"]}`).Code)
}

func TestStacksAndExportsIncludeACustomImage(t *testing.T) {
	h, rt, store, data, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	coreEq(t, 200, coreDo(h, "PUT", "/api/stacks/with-mine", `{"description":"x","providers":["my-api","redis"]}`).Code)
	coreEq(t, 400, coreDo(h, "PUT", "/api/stacks/bad", `{"providers":["ghost"]}`).Code)

	var stacks StackListDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/stacks", "").Body, &stacks) == nil)
	found := false
	for _, s := range stacks.Stacks {
		if s.ID == "with-mine" {
			found = true
			coreEq(t, 2, s.Total)
		}
	}
	coreTrue(t, found)

	// Up starts both; down stops both.
	r := coreDo(h, "POST", "/api/stacks/with-mine/up", "")
	coreEq(t, 200, r.Code)
	coreTrue(t, !strings.Contains(r.Body, `"ok":false`), r.Body)
	started := map[string]bool{}
	for _, s := range rt.started {
		started[s.id] = true
	}
	coreTrue(t, started["my-api"] && started["redis"])
	coreEq(t, 200, coreDo(h, "POST", "/api/stacks/with-mine/down", "").Code)
	coreEq(t, false, rt.live["my-api"].Running)

	// Compose: written like any provider; the secret is a reference, not a value, and only when it has one.
	compose := regExport(t, h, "/api/stacks/with-mine/export?format=compose&persist=true")
	for _, want := range []string{"  my-api:", "image: ghcr.io/me/api:1.2", `"127.0.0.1:8081:8081"`, `MODE: "test"`, `command: ["serve", "--dev"]`, "nimbo-my-api-data:/data"} {
		coreContains(t, compose, want)
	}
	coreTrue(t, !strings.Contains(compose, "API_KEY"), "no value, so no reference")
	store.values[regKeyName] = "export-secret-xyz"
	compose = regExport(t, h, "/api/stacks/with-mine/export?format=compose")
	coreContains(t, compose, `API_KEY: "${`+regKeyName+`:-}"`)
	coreTrue(t, !strings.Contains(compose, "export-secret-xyz"), "never in the compose file")
	coreTrue(t, !strings.Contains(compose, "nimbo-my-api-data"), "no volume unless persist is asked")

	// The .env next to it: a placeholder by default, the value only when the person asks for passwords.
	env := regExport(t, h, "/api/stacks/with-mine/export?format=composeenv")
	coreContains(t, env, "# My API")
	coreContains(t, env, "# "+regKeyName+"=")
	coreTrue(t, !strings.Contains(env, "export-secret-xyz"))
	env = regExport(t, h, "/api/stacks/with-mine/export?format=composeenv&secrets=true")
	coreContains(t, env, regKeyName+"=export-secret-xyz")
	coreContains(t, regJournal(data), "secret.reveal with-mine export composeenv")

	// The other formats treat it as an emulator without endpoints.
	for _, f := range []string{"env", "spring", "dotnet"} {
		r := coreDo(h, "GET", "/api/stacks/with-mine/export?format="+f, "")
		coreEq(t, 200, r.Code, f)
		coreTrue(t, !strings.Contains(r.Body, "export-secret-xyz") && !strings.Contains(r.Body, "ghcr.io/me/api"), f)
	}

	// A stack that names an image deleted afterwards does not take the app down.
	coreEq(t, 204, coreDo(h, "DELETE", "/api/providers/my-api", "").Code)
	coreEq(t, 204, coreDo(h, "DELETE", "/api/registry/images/my-api", "").Code)
	coreEq(t, 200, coreDo(h, "GET", "/api/stacks", "").Code)
	coreEq(t, 200, coreDo(h, "POST", "/api/stacks/with-mine/up", "").Code)
}

func TestNoSecretValueInAnyListOrTheJournal(t *testing.T) {
	h, _, store, data, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	coreEq(t, 204, coreDo(h, "PUT", "/api/secrets/"+regKeyName, `{"value":"never-show-this-9"}`).Code)
	coreEq(t, 202, coreDo(h, "POST", "/api/providers/my-api/start", "").Code)
	coreEq(t, 200, coreDo(h, "PUT", "/api/stacks/s1", `{"providers":["my-api"]}`).Code)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code) // replace while the secret exists
	for _, path := range []string{
		"/api/registry/images", "/api/providers", "/api/variables", "/api/secrets", "/api/stacks", "/api/stats", "/api/data",
		"/api/providers/my-api/env", "/api/stacks/s1/export?format=env", "/api/stacks/s1/export?format=compose", "/api/stacks/s1/export?format=composeenv",
	} {
		r := coreDo(h, "GET", path, "")
		coreTrue(t, r.Code == 200, path+" "+itoaForTest(r.Code))
		coreTrue(t, !strings.Contains(r.Body, "never-show-this-9"), path)
	}
	// An invalid save echoes nothing of the request either.
	r := regPut(h, "my-api", `{"image":"BAD IMAGE never-show-this-9"}`)
	coreEq(t, 400, r.Code)
	coreTrue(t, !strings.Contains(r.Body, "never-show-this-9"))
	coreTrue(t, !strings.Contains(regJournal(data), "never-show-this-9"))
	// The saved file holds the key names, never their values.
	text, _, _ := data.Store.Read(FileRegistryImages)
	coreContains(t, text, `"API_KEY"`)
	coreTrue(t, !strings.Contains(text, "never-show-this-9"))
	coreEq(t, "never-show-this-9", store.values[regKeyName])
}

func TestRegisteredImagesSurviveARestartAndBadEntriesAreSkipped(t *testing.T) {
	h, rt, store, data, _ := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	coreEq(t, 200, regPut(h, "other", `{"image":"x/other","ports":[8090]}`).Code)
	coreTrue(t, FindProvider("my-api") != nil)

	// A new process: nothing registered until the registry loads its file.
	SetCustomProviders(nil)
	coreTrue(t, FindProvider("my-api") == nil)
	NewImageRegistry(data.Store, data, rt, store, nil)
	p := FindProvider("my-api")
	coreTrue(t, p != nil && p.Custom && p.Image == "ghcr.io/me/api:1.2" && p.Persist != nil && p.Persist.MountPath == "/data")
	coreEq(t, len(CatalogProviders)+2, len(AllProviders()))
	coreEq(t, "floci-aws", AllProviders()[0].ID)
	coreTrue(t, CatalogFind("my-api") == nil, "CatalogFind stays built-in only")

	// A hand-edited file: an id that is a built-in, a port that collides, a malformed entry. Good ones stay.
	bad := `{"items":[` +
		`{"id":"redis","image":"x/r"},` +
		`{"id":"good","name":"Good","image":"x/good","ports":[8100]},` +
		`{"id":"clash","image":"x/clash","ports":[8100]},` +
		`{"id":"BAD","image":"x/y"},` +
		`{"id":"nullish","image":"x/n","ports":null,"env":null}]}`
	coreTrue(t, data.Store.Write(FileRegistryImages, bad) == nil)
	NewImageRegistry(data.Store, data, rt, store, nil)
	ids := []string{}
	for _, d := range AllProviders()[len(CatalogProviders):] {
		ids = append(ids, d.ID)
	}
	coreEq(t, "good,nullish", strings.Join(ids, ","))
	coreEq(t, 0, len(FindProvider("nullish").Ports))

	// A damaged file starts empty instead of failing.
	coreTrue(t, data.Store.Write(FileRegistryImages, "{not json") == nil)
	NewImageRegistry(data.Store, data, rt, store, nil)
	coreEq(t, len(CatalogProviders), len(AllProviders()))
}

func TestImageStatusListsAndPullsCustomImages(t *testing.T) {
	h, _, _, data, reg := imgRegServer(t)
	coreEq(t, 200, regPut(h, "my-api", regAPI).Code)
	docker := newImgDocker()
	docker.images = "ghcr.io/me/api:1.2"
	log := &updFakeLog{}
	svc := NewImages(docker, func() []string { return nil }, log, ImageRefsOfAllProviders(), nil)
	svc.UseProviders(ImageRefsOfAllProviders)
	_ = reg
	_ = data

	st := svc.Status(context.Background())
	mine := imgProvider(st, "my-api")
	coreEq(t, "ghcr.io/me/api:1.2", mine.Image)
	coreTrue(t, mine.Present && mine.Pack == nil, "present locally, in no offline pack")
	coreEq(t, len(CatalogProviders)+1, len(st.Images))

	docker.calls = nil
	coreTrue(t, svc.Pull(context.Background(), "my-api") == nil)
	coreEq(t, "pull ghcr.io/me/api:1.2", strings.Join(docker.calls[len(docker.calls)-1], " "))

	// Registered after the service was built: still found, because the list is asked for every time.
	coreEq(t, 200, regPut(h, "later", `{"image":"x/later","ports":[8095]}`).Code)
	coreTrue(t, svc.Pull(context.Background(), "later") == nil)
	coreEq(t, 204, coreDo(h, "DELETE", "/api/registry/images/later", "").Code)
	_, isUnknown := svc.Pull(context.Background(), "later").(UnknownTarget)
	coreTrue(t, isUnknown)

	// And over HTTP, through the real image routes.
	mux := http.NewServeMux()
	NewImageRoutes(svc).Register(mux)
	r := coreDo(mux, "GET", "/api/images", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, `"provider":"my-api"`)
}
