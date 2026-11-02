package nimbo

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseStacksKeepsOrderAndDedupes(t *testing.T) {
	c := ParseStacks("stacks:\n  one:\n    description: first\n    providers: [redis, minio, redis]\n  two:\n    providers: [floci-aws]\n")
	coreEq(t, 0, len(c.Problems), strings.Join(c.Problems, ";"))
	coreEq(t, "one,two", c.Stacks[0].ID+","+c.Stacks[1].ID)
	coreTrue(t, reflect.DeepEqual([]string{"redis", "minio"}, c.Find("one").Providers), "duplicates are removed")
	coreEq(t, "", c.Find("two").Description)
}

func TestParseStacksSkipsInvalidAndReports(t *testing.T) {
	c := ParseStacks("stacks:\n  ok:\n    providers: [redis]\n  ghost:\n    providers: [redis, not-a-provider]\n  empty:\n    providers: []\n  Bad_Id:\n    providers: [redis]\n")
	coreEq(t, 1, len(c.Stacks))
	coreEq(t, "ok", c.Stacks[0].ID)
	coreEq(t, 3, len(c.Problems))
	all := strings.Join(c.Problems, "\n")
	coreContains(t, all, "not-a-provider")
	coreContains(t, all, "Bad_Id")
}

func TestParseStacksMalformedDoesNotPanic(t *testing.T) {
	coreTrue(t, len(ParseStacks("stacks: [unclosed").Problems) > 0)
	p := ParseStacks("other: 1").Problems
	coreTrue(t, len(p) == 1 && strings.Contains(p[0], "stacks"))
	coreEq(t, 0, len(ParseStacks("").Stacks))
	c := ParseStacks("stacks: !!java.lang.ProcessBuilder [[calc]]")
	coreTrue(t, len(c.Stacks) == 0 && len(c.Problems) > 0)
}

func TestShippedNimboYmlIsValid(t *testing.T) {
	b, err := os.ReadFile(coreRepoFile(t, "nimbo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	c := ParseStacks(string(b))
	coreEq(t, 0, len(c.Problems), strings.Join(c.Problems, ";"))
	coreTrue(t, len(c.Stacks) >= 3)
}

func TestBuiltInStacksReferenceKnownProviders(t *testing.T) {
	for _, s := range BuiltInStacks().Stacks {
		for _, p := range s.Providers {
			coreTrue(t, CatalogFind(p) != nil, s.ID, p)
		}
	}
}

func TestBundledStacks(t *testing.T) {
	fsys := fstest.MapFS{"nimbo.yml": {Data: []byte("stacks:\n  bundled:\n    providers: [redis]\n")}}
	c := LoadBundledStacks(fsys, "nimbo.yml")
	coreTrue(t, c != nil && len(c.Stacks) == 1 && c.Stacks[0].ID == "bundled")
	coreTrue(t, LoadBundledStacks(fsys, "nope.yml") == nil)
	coreTrue(t, LoadBundledStacks(nil, "nimbo.yml") == nil)
}

func TestLoadStacksFromFileThenBundledThenBuiltIn(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/custom.yml"
	_ = os.WriteFile(file, []byte("stacks:\n  mine:\n    providers: [redis]\n"), 0o644)
	c := LoadStacks(map[string]string{"NIMBO_STACKS_FILE": file}, nil)
	coreEq(t, "mine", c.Stacks[0].ID)

	// From the package folder neither ./nimbo.yml nor ../nimbo.yml exists, so the bundle (then the built-ins) apply.
	bundled := fstest.MapFS{"nimbo.yml": {Data: []byte("stacks:\n  b:\n    providers: [redis]\n")}}
	coreEq(t, "b", LoadStacks(map[string]string{}, bundled).Stacks[0].ID)
	coreEq(t, "aws-basic", LoadStacks(map[string]string{}, nil).Stacks[0].ID)
}

func TestExportEnvGroupsPerProvider(t *testing.T) {
	text := ExportEnv([]ProviderDef{*CatalogFind("floci-aws"), *CatalogFind("redis")}, false)
	coreContains(t, text, "# Floci AWS")
	coreContains(t, text, "AWS_ENDPOINT_URL=http://localhost:4566")
	coreContains(t, text, "REDIS_URL=redis://localhost:6379")
}

func TestExportTofu(t *testing.T) {
	tf := ExportTofuAwsOverride(false)
	coreContains(t, tf, `provider "aws"`)
	coreContains(t, tf, "skip_credentials_validation = true")
	coreContains(t, tf, "skip_requesting_account_id  = true")
	coreTrue(t, regexp.MustCompile(`s3\s+= "http://localhost:4566"`).MatchString(tf))
	coreEq(t, strings.Count(tf, "{"), strings.Count(tf, "}"), "balanced braces")
	coreContains(t, tf, `    cloudwatchlogs = "http://localhost:4566"`)
	coreContains(t, ExportTofuAwsOverride(true), "http://localhost:14566")
}

func TestExportAwsCliProfileAndChaos(t *testing.T) {
	ini := ExportAwsCliProfile(false)
	coreContains(t, ini, "[profile nimbo]")
	coreContains(t, ini, "endpoint_url = http://localhost:4566")
	coreTrue(t, !strings.Contains(ini, "AKIA"))
	env := ExportEnv([]ProviderDef{*CatalogFind("floci-aws"), *CatalogFind("floci-gcp")}, true)
	coreContains(t, env, "# Through Toxiproxy")
	coreContains(t, env, "AWS_ENDPOINT_URL=http://localhost:14566")
	coreContains(t, env, "PUBSUB_EMULATOR_HOST=localhost:14588")
	coreEq(t, "x localhost:1234", ExportViaChaos("x localhost:1234"))
}

const coreMixStacks = "stacks:\n  mix:\n    description: test\n    providers: [floci-aws, redis, minio]\n  azure-only:\n    providers: [azurite]\n"

func TestStackListAggregatesState(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: true, Status: "Up"}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt, Stacks: ParseStacks(coreMixStacks)})
	body := coreDo(h, "GET", "/api/stacks", "").Body
	coreContains(t, body, `"id":"mix"`)
	coreContains(t, body, `"state":"partial"`)
	coreContains(t, body, `"running":1`)
	coreContains(t, body, `"state":"stopped"`)
	coreContains(t, body, `"problems":[]`)
}

func TestStackUpStartsInOrderAndSkipsRunning(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: true, Status: "Up"}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt, Stacks: ParseStacks(coreMixStacks)})
	coreEq(t, 200, coreDo(h, "POST", "/api/stacks/mix/up", "").Code)
	coreTrue(t, reflect.DeepEqual([]string{"start:floci-aws", "start:minio"}, rt.calls), strings.Join(rt.calls, ","))
	coreContains(t, coreDo(h, "GET", "/api/stacks", "").Body, `"state":"running"`)
}

func TestStackUpKeepsGoingWhenOneFails(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.failing = map[string]bool{"redis": true}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt, Stacks: ParseStacks(coreMixStacks)})
	body := coreDo(h, "POST", "/api/stacks/mix/up", "").Body
	coreTrue(t, reflect.DeepEqual([]string{"start:floci-aws", "start:redis", "start:minio"}, rt.calls))
	coreContains(t, body, `"ok":false`)
	coreContains(t, body, "port busy")
}

func TestStackDownStopsOnlyRunningInReverse(t *testing.T) {
	rt := newCoreFakeRuntime()
	rt.live["floci-aws"] = ContainerInfo{ProviderID: "floci-aws", Running: true}
	rt.live["redis"] = ContainerInfo{ProviderID: "redis", Running: false}
	rt.live["minio"] = ContainerInfo{ProviderID: "minio", Running: true}
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt, Stacks: ParseStacks(coreMixStacks)})
	coreDo(h, "POST", "/api/stacks/mix/down", "")
	coreTrue(t, reflect.DeepEqual([]string{"stop:minio", "stop:floci-aws"}, rt.calls), strings.Join(rt.calls, ","))
}

func TestUnknownStackIs404AndTouchesNothing(t *testing.T) {
	rt := newCoreFakeRuntime()
	h := coreTestServer(DefaultConfig(), Deps{Runtime: rt, Stacks: ParseStacks(coreMixStacks)})
	coreEq(t, 404, coreDo(h, "POST", "/api/stacks/nope/up", "").Code)
	coreEq(t, 404, coreDo(h, "GET", "/api/stacks/nope/export?format=env", "").Code)
	coreEq(t, 0, len(rt.calls))
}

func TestStackExportValidation(t *testing.T) {
	h := coreTestServer(DefaultConfig(), Deps{Stacks: ParseStacks(coreMixStacks)})
	coreEq(t, 400, coreDo(h, "GET", "/api/stacks/mix/export?format=xml", "").Code)
	coreEq(t, 400, coreDo(h, "GET", "/api/stacks/mix/export", "").Code)
	coreEq(t, 409, coreDo(h, "GET", "/api/stacks/azure-only/export?format=tofu", "").Code)
	coreEq(t, 200, coreDo(h, "GET", "/api/stacks/azure-only/export?format=env", "").Code)
	tofu := coreDo(h, "GET", "/api/stacks/mix/export?format=tofu", "").Body
	coreContains(t, tofu, "nimbo_override.tf")
	coreContains(t, tofu, "provider")
}

func TestStacksAreProtectedByTheToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIToken = "t0ken"
	h := coreTestServer(cfg, Deps{Stacks: ParseStacks(coreMixStacks)})
	coreEq(t, 401, coreDo(h, "GET", "/api/stacks", "").Code)
	coreEq(t, 401, coreDo(h, "POST", "/api/stacks/mix/up", "").Code)
}

// ---- The hand-written in-app guides must agree with the catalog (GuidesConsistencyTest) ----------------------------

func guidesSource(t *testing.T) (providers, recipes, blurbs string) {
	b, err := os.ReadFile(coreRepoFile(t, "web/src/guides.ts"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	providers, _, _ = strings.Cut(src, "export const RECIPES")
	_, afterRecipes, _ := strings.Cut(src, "export const RECIPES")
	recipes, _, _ = strings.Cut(afterRecipes, "export interface TabHelp")
	_, afterBlurbs, _ := strings.Cut(src, "export const STACK_BLURBS")
	blurbs, _, _ = strings.Cut(afterBlurbs, "export function stackBlurb")
	return
}

func guideBlocks(providerPart string) map[string]string {
	out := map[string]string{}
	parts := strings.Split(providerPart, "\n  {\n    id: '")
	for _, p := range parts[1:] {
		id, _, _ := strings.Cut(p, "'")
		out[id] = p
	}
	return out
}

func TestEveryProviderHasAGuideAndViceVersa(t *testing.T) {
	p, _, _ := guidesSource(t)
	blocks := guideBlocks(p)
	for _, d := range CatalogProviders {
		_, ok := blocks[d.ID]
		coreTrue(t, ok, "no guide for "+d.ID)
	}
	coreEq(t, len(CatalogProviders), len(blocks), "guides.ts and the catalog list different providers")
}

func TestGuidePortsAreRealPorts(t *testing.T) {
	p, _, _ := guidesSource(t)
	re := regexp.MustCompile(`localhost:(\d+)`)
	for id, block := range guideBlocks(p) {
		allowed := map[string]bool{}
		for _, port := range CatalogFind(id).Ports {
			allowed[itoaForTest(port)] = true
		}
		for _, c := range coreChaosTargets {
			allowed[itoaForTest(c.proxyPort)] = true
		}
		for _, m := range re.FindAllStringSubmatch(block, -1) {
			coreTrue(t, allowed[m[1]], id+" mentions localhost:"+m[1])
		}
	}
}

func TestFirstPublishedPorts(t *testing.T) {
	want := map[string]int{"floci-aws": 4566, "floci-azure": 4577, "floci-gcp": 4588, "floci-oci": 4599, "redpanda": 9092,
		"postgres": 5432, "redis": 6379, "toxiproxy": 8474, "k3s": 6443, "jaeger": 16686, "minio": 9000, "azurite": 10000}
	for id, port := range want {
		coreEq(t, port, CatalogFind(id).Ports[0], id)
	}
}

func TestRecipesUseRealProvidersWithoutSecrets(t *testing.T) {
	_, recipes, _ := guidesSource(t)
	lists := regexp.MustCompile(`providers: \[([^\]]*)\]`).FindAllStringSubmatch(recipes, -1)
	coreTrue(t, len(lists) >= 4, "could not find the recipes in guides.ts")
	for _, l := range lists {
		for _, m := range regexp.MustCompile(`'([a-z0-9-]+)'`).FindAllStringSubmatch(l[1], -1) {
			def := CatalogFind(m[1])
			coreTrue(t, def != nil, "recipe starts unknown provider "+m[1])
			if def != nil {
				for _, v := range def.Vars {
					coreTrue(t, !v.Required, m[1]+" needs "+v.Name+": a recipe must work on a fresh install")
				}
			}
		}
	}
}

func TestStackBlurbsMatchShippedStacks(t *testing.T) {
	_, _, blurbs := guidesSource(t)
	b, _ := os.ReadFile(coreRepoFile(t, "nimbo.yml"))
	shipped := map[string]bool{}
	for _, s := range ParseStacks(string(b)).Stacks {
		shipped[s.ID] = true
	}
	blurbed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s{2}'?([a-z-]+)'?: t\(`).FindAllStringSubmatch(blurbs, -1) {
		blurbed[m[1]] = true
	}
	coreTrue(t, reflect.DeepEqual(shipped, blurbed), "nimbo.yml and the translated stack descriptions are out of sync")
}

func TestGuideSecretsAreTheOnesTheCatalogReads(t *testing.T) {
	b, err := os.ReadFile(coreRepoFile(t, "web/src/guides.ts"))
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{}
	for _, n := range SecretNames {
		read[n] = true
	}
	found := regexp.MustCompile(`NIMBO_(?:MINIO|POSTGRES)_[A-Z_]+`).FindAllString(string(b), -1)
	coreTrue(t, len(found) > 0)
	for _, n := range found {
		coreTrue(t, read[n], "a guide names a secret the backend does not read: "+n)
	}
}
