package nimbo

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func connectDefs(ids ...string) []ProviderDef {
	var out []ProviderDef
	for _, id := range ids {
		out = append(out, *CatalogFind(id))
	}
	return out
}

func TestComposeIsValidYAMLAndHoldsNoSecret(t *testing.T) {
	defs := connectDefs("floci-aws", "redpanda", "postgres", "redis")
	values := ExportValues{"NIMBO_POSTGRES_PASSWORD": "<NIMBO_POSTGRES_PASSWORD>", "NIMBO_REDIS_PASSWORD": "<NIMBO_REDIS_PASSWORD>"}
	text := ExportCompose("event-driven", defs, values, false)

	var doc struct {
		Name     string                    `yaml:"name"`
		Services map[string]map[string]any `yaml:"services"`
		Volumes  map[string]any            `yaml:"volumes"`
	}
	coreTrue(t, yaml.Unmarshal([]byte(text), &doc) == nil, text)
	coreEq(t, "nimbo-event-driven", doc.Name)
	coreEq(t, 4, len(doc.Services))
	coreEq(t, 0, len(doc.Volumes), "no volumes unless data is kept")

	pg := doc.Services["postgres"]
	coreEq(t, "postgres:17-alpine", pg["image"].(string))
	env := pg["environment"].(map[string]any)
	coreContains(t, env["POSTGRES_PASSWORD"].(string), "${NIMBO_POSTGRES_PASSWORD:?")
	_, hasUser := env["POSTGRES_USER"]
	coreTrue(t, !hasUser, "an optional setting you did not configure is left out")
	coreContains(t, text, `"127.0.0.1:5432:5432"`)
	for _, svc := range doc.Services {
		for _, p := range svc["ports"].([]any) {
			coreTrue(t, strings.HasPrefix(p.(string), "127.0.0.1:"), "every published port is on loopback: "+p.(string))
		}
	}

	redis := doc.Services["redis"]
	coreContains(t, redis["environment"].(map[string]any)["REDIS_PASSWORD"].(string), "${NIMBO_REDIS_PASSWORD:-}")
	// The start script uses the shell's own $: in a compose file it has to be written $$ or Compose would substitute it.
	cmd := redis["command"].([]any)
	coreContains(t, cmd[2].(string), "$${REDIS_PASSWORD:+")
	coreTrue(t, !strings.Contains(strings.ReplaceAll(cmd[2].(string), "$$", ""), "$"), "every $ in the script is doubled")
}

func TestComposeNeverContainsAValueEvenWhenTheyAreKnown(t *testing.T) {
	values := ExportValues{"NIMBO_MINIO_USER": "admin-user", "NIMBO_MINIO_PASSWORD": "real-minio-secret"}
	text := ExportCompose("s", connectDefs("minio"), values, false)
	coreTrue(t, !strings.Contains(text, "real-minio-secret") && !strings.Contains(text, "admin-user"))
	coreContains(t, text, "MINIO_ROOT_PASSWORD")
}

func TestComposeKeepsDataInNamedVolumesWhenAsked(t *testing.T) {
	text := ExportCompose("s", connectDefs("minio", "jaeger", "toxiproxy"), ExportValues{}, true)
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
		Volumes  map[string]any            `yaml:"volumes"`
	}
	coreTrue(t, yaml.Unmarshal([]byte(text), &doc) == nil, text)
	_, a := doc.Volumes["nimbo-minio-data"]
	_, b := doc.Volumes["nimbo-jaeger-data"]
	coreTrue(t, a && b)
	coreEq(t, 2, len(doc.Volumes), "a provider with nothing to keep gets no volume")
	coreEq(t, "0:0", doc.Services["minio"]["user"].(string), "images that run unprivileged need a writable volume")
	coreContains(t, text, "nimbo-minio-data:/data")
}

func TestComposeTranslatesTheRunFlagsItKnows(t *testing.T) {
	text := ExportCompose("c", connectDefs("k3s", "toxiproxy"), ExportValues{}, false)
	coreContains(t, text, "privileged: true")
	coreContains(t, text, "tmpfs:\n      - /run\n      - /var/run")
	coreContains(t, text, `extra_hosts:`+"\n"+`      - "host.docker.internal:host-gateway"`)
	var doc map[string]any
	coreTrue(t, yaml.Unmarshal([]byte(text), &doc) == nil)
}

func TestComposeIncludesAnOptionalSettingOnceYouConfiguredIt(t *testing.T) {
	text := ExportCompose("c", connectDefs("k3s"), ExportValues{"NIMBO_K3S_TOKEN": "<NIMBO_K3S_TOKEN>"}, false)
	coreContains(t, text, "K3S_TOKEN")
	coreContains(t, text, "${NIMBO_K3S_TOKEN:-}")
}

func TestComposeEnvIsPlaceholdersUntilAskedForTheRealOnes(t *testing.T) {
	defs := connectDefs("postgres", "redis")
	hidden := ExportComposeEnv(defs, ExportValues{"NIMBO_POSTGRES_PASSWORD": "<NIMBO_POSTGRES_PASSWORD>", "NIMBO_POSTGRES_USER": "app"})
	coreContains(t, hidden, "NIMBO_POSTGRES_PASSWORD=\n")
	coreContains(t, hidden, "NIMBO_POSTGRES_USER=app")
	coreContains(t, hidden, "# NIMBO_REDIS_PASSWORD=")
	coreTrue(t, !strings.Contains(hidden, "<NIMBO"), "an empty value, not a placeholder that would be taken literally")

	shown := ExportComposeEnv(defs, ExportValues{"NIMBO_POSTGRES_PASSWORD": "pa ss#word$1", "NIMBO_REDIS_PASSWORD": "plain-value"})
	coreContains(t, shown, "NIMBO_POSTGRES_PASSWORD='pa ss#word$1'")
	coreContains(t, shown, "NIMBO_REDIS_PASSWORD=plain-value")
}

func TestSpringYamlHasOnlyWhatTheStackContains(t *testing.T) {
	values := ExportValues{"NIMBO_POSTGRES_PASSWORD": "<NIMBO_POSTGRES_PASSWORD>", "NIMBO_POSTGRES_USER": "app", "NIMBO_POSTGRES_DB": "shop", "NIMBO_REDIS_PASSWORD": "redis-pw-1234"}
	text := ExportSpring(connectDefs("floci-aws", "redpanda", "postgres", "redis"), values)
	var doc map[string]any
	coreTrue(t, yaml.Unmarshal([]byte(text), &doc) == nil, text)
	coreContains(t, text, `endpoint: "http://localhost:4566"`)
	coreContains(t, text, `url: "jdbc:postgresql://localhost:5432/shop"`)
	coreContains(t, text, `username: "app"`)
	coreContains(t, text, `password: "<NIMBO_POSTGRES_PASSWORD>"`)
	coreContains(t, text, `password: "redis-pw-1234"`)
	coreContains(t, text, `bootstrap-servers: "localhost:9092"`)
	coreTrue(t, !strings.Contains(text, "azure") && !strings.Contains(text, "gcp"), "nothing for emulators that are not in the stack")
	spring := doc["spring"].(map[string]any)
	coreTrue(t, spring["cloud"] != nil && spring["datasource"] != nil && spring["kafka"] != nil)
}

func TestSpringExplainsAnEmptyStack(t *testing.T) {
	coreContains(t, ExportSpring(connectDefs("toxiproxy"), ExportValues{}), "nothing to configure")
}

func TestDotnetIsValidJSONWithTheRightKeys(t *testing.T) {
	values := ExportValues{"NIMBO_POSTGRES_PASSWORD": "<NIMBO_POSTGRES_PASSWORD>", "NIMBO_REDIS_PASSWORD": "redis-pw-1234",
		"NIMBO_MINIO_USER": "minio-admin", "NIMBO_MINIO_PASSWORD": "<NIMBO_MINIO_PASSWORD>"}
	text := ExportDotnet(connectDefs("floci-aws", "minio", "postgres", "redis", "redpanda", "jaeger"), values)
	coreTrue(t, json.Valid([]byte(text)), text)
	var doc map[string]map[string]any
	coreTrue(t, json.Unmarshal([]byte(text), &doc) == nil)
	conns := doc["ConnectionStrings"]
	coreEq(t, "Host=localhost;Port=5432;Database=postgres;Username=postgres;Password=<NIMBO_POSTGRES_PASSWORD>", conns["Postgres"].(string))
	coreEq(t, "localhost:6379,password=redis-pw-1234", conns["Redis"].(string))
	coreEq(t, "http://localhost:4566", doc["AWS"]["ServiceURL"].(string))
	coreEq(t, "minio-admin", doc["MinIO"]["AccessKey"].(string))
	coreEq(t, "localhost:9092", doc["Kafka"]["BootstrapServers"].(string))
	coreContains(t, text, "<NIMBO_POSTGRES_PASSWORD>")
	coreTrue(t, !strings.Contains(text, string(rune(92))+"u003c"), "placeholders stay readable")
	first := strings.Index(text, "ConnectionStrings")
	coreTrue(t, first > 0 && first < strings.Index(text, "AWS"), "keys keep the order they were written in")
}

func TestAzuriteConnectionStringFollowsTheAccount(t *testing.T) {
	coreEq(t, "UseDevelopmentStorage=true", azuriteConnectionString(ExportValues{}))
	cs := azuriteConnectionString(ExportValues{"NIMBO_AZURITE_ACCOUNTS": "acct1:AAAAAAAAAAAAAAAAAAAA;other:BBBBBBBBBBBBBBBBBBBB"})
	coreContains(t, cs, "AccountName=acct1;AccountKey=AAAAAAAAAAAAAAAAAAAA;")
	coreTrue(t, !strings.Contains(cs, "other"), "the first account is the one clients use")
}

// ---- over HTTP ----------------------------------------------------------------------------------------------------

func exportBody(t *testing.T, body string) ExportDto {
	t.Helper()
	var dto ExportDto
	coreTrue(t, jsonUnmarshal(body, &dto) == nil, body)
	return dto
}

func TestExportRouteWritesEveryFormatForAStack(t *testing.T) {
	h, _, store, _ := varsServer(t)
	store.values["NIMBO_POSTGRES_PASSWORD"] = "pg-secret-value-1"
	store.values["NIMBO_POSTGRES_USER"] = "app"
	for _, c := range []struct{ format, file, has string }{
		{"compose", "docker-compose.yml", "services:"},
		{"composeenv", ".env", "NIMBO_POSTGRES_USER=app"},
		{"spring", "application-nimbo.yml", "jdbc:postgresql"},
		{"dotnet", "appsettings.Nimbo.json", "ConnectionStrings"},
		{"env", "nimbo.env", "DATABASE_HOST"},
		{"tofu", "nimbo_override.tf", "provider \"aws\""},
		{"awscli", "aws-config-nimbo.ini", "[profile nimbo]"},
	} {
		r := coreDo(h, "GET", "/api/stacks/event-driven/export?format="+c.format, "")
		coreEq(t, 200, r.Code, c.format)
		dto := exportBody(t, r.Body)
		coreEq(t, c.file, dto.Filename, c.format)
		coreContains(t, dto.Content, c.has)
		coreTrue(t, !strings.Contains(r.Body, "pg-secret-value-1"), c.format+" must not hold the password unless asked")
	}
}

func TestExportWithPasswordsIsDeliberateAndJournaled(t *testing.T) {
	h, _, store, data := varsServer(t)
	store.values["NIMBO_POSTGRES_PASSWORD"] = "pg-secret-value-1"
	r := coreDo(h, "GET", "/api/stacks/event-driven/export?format=composeenv&secrets=true", "")
	coreEq(t, 200, r.Code)
	coreContains(t, exportBody(t, r.Body).Content, "NIMBO_POSTGRES_PASSWORD=pg-secret-value-1")
	r = coreDo(h, "GET", "/api/stacks/event-driven/export?format=spring&secrets=true", "")
	coreContains(t, exportBody(t, r.Body).Content, `password: "pg-secret-value-1"`)
	var kinds []string
	for _, e := range data.Stats.Journal(10) {
		kinds = append(kinds, e.Kind)
		if e.Detail != nil {
			coreTrue(t, !strings.Contains(*e.Detail, "pg-secret-value-1"))
		}
	}
	coreContains(t, strings.Join(kinds, ","), "secret.reveal")
}

func TestExportRouteRefusesWhatDoesNotApply(t *testing.T) {
	_, rt, store, data := varsServer(t)
	h := coreTestServer(Config{HostEnv: map[string]string{}}, Deps{Runtime: rt, Secrets: store, Data: data, Stacks: &StackCatalog{Stacks: []StackDef{
		{"event-driven", "", []string{"floci-aws", "postgres"}}, {"storage-lab", "", []string{"azurite", "minio"}},
	}}})
	r := coreDo(h, "GET", "/api/stacks/event-driven/export?format=nope", "")
	coreEq(t, 400, r.Code)
	coreContains(t, r.Body, "compose")
	r = coreDo(h, "GET", "/api/stacks/storage-lab/export?format=tofu", "")
	coreEq(t, 409, r.Code)
	coreContains(t, r.Body, "no AWS emulator")
	coreEq(t, 200, coreDo(h, "GET", "/api/stacks/storage-lab/export?format=compose", "").Code, "compose needs no AWS")
	coreEq(t, 404, coreDo(h, "GET", "/api/stacks/nope/export?format=compose", "").Code)
}

func TestExportedComposeAndEnvWorkTogether(t *testing.T) {
	h, _, store, _ := varsServer(t)
	store.values["NIMBO_MINIO_USER"] = "minio-admin"
	store.values["NIMBO_MINIO_PASSWORD"] = "minio-secret-pass"
	compose := exportBody(t, coreDo(h, "GET", "/api/stacks/aws-basic/export?format=compose", "").Body).Content
	env := exportBody(t, coreDo(h, "GET", "/api/stacks/aws-basic/export?format=composeenv&secrets=true", "").Body).Content
	// Every ${NIMBO_*} the compose file asks for is defined in the .env that goes with it.
	for _, m := range regexp.MustCompile(`\$\{(NIMBO_[A-Z0-9_]+)[:?-]`).FindAllStringSubmatch(compose, -1) {
		coreContains(t, env, m[1]+"=")
	}
	coreTrue(t, !strings.Contains(compose, "minio-secret-pass"))
	coreContains(t, env, "NIMBO_MINIO_PASSWORD=minio-secret-pass")
}
