package nimbo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

// repoLabsDir is the labs/ folder of the repo, two levels above this package.
const repoLabsDir = "../../labs"

func labYAML(extra string, checks ...string) string {
	var sb strings.Builder
	sb.WriteString("id: t\ntitle: T\nsummary: S\nsteps: [do it]\n")
	if extra != "" {
		sb.WriteString(extra + "\n")
	}
	sb.WriteString("checks:\n")
	for _, c := range checks {
		sb.WriteString("  - " + c + "\n")
	}
	return sb.String()
}

func labParse(text string) (*LabDef, string) { return ParseLab(text, "t.yml") }

func labRepoCatalog(t *testing.T) *LabCatalog {
	t.Helper()
	return LoadLabs(map[string]string{"NIMBO_LABS_DIR": repoLabsDir}, nil)
}

func TestLabsEveryLabShippedInTheRepoIsValidAndBilingual(t *testing.T) {
	cat := labRepoCatalog(t)
	if len(cat.Problems) != 0 {
		t.Fatal(cat.Problems)
	}
	if len(cat.Labs) < 6 {
		t.Fatalf("only %d labs", len(cat.Labs))
	}
	ids := map[string]bool{}
	for _, l := range cat.Labs {
		d := l.Dto
		ids[d.ID] = true
		if d.Title.En == d.Title.Es {
			t.Errorf("%s: title not translated", d.ID)
		}
		if d.Summary.En == d.Summary.Es {
			t.Errorf("%s: summary not translated", d.ID)
		}
		for _, s := range d.Steps {
			if s.En == s.Es {
				t.Errorf("%s: a step is not translated", d.ID)
			}
		}
		for _, c := range d.Checks {
			if c.Label.En == c.Label.Es {
				t.Errorf("%s: a check label is not translated", d.ID)
			}
		}
		if d.Level < 1 || d.Level > 3 {
			t.Errorf("%s: level %d", d.ID, d.Level)
		}
		if len(d.Solution) == 0 {
			t.Errorf("%s: no solution", d.ID)
		}
	}
	if len(ids) != len(cat.Labs) {
		t.Fatal("duplicate ids")
	}
}

func TestLabsAreOrderedByLevelThenID(t *testing.T) {
	labs := labRepoCatalog(t).Labs
	for i := 1; i < len(labs); i++ {
		a, b := labs[i-1].Dto, labs[i].Dto
		if a.Level > b.Level || (a.Level == b.Level && a.ID > b.ID) {
			t.Fatalf("%s (level %d) before %s (level %d)", a.ID, a.Level, b.ID, b.Level)
		}
	}
}

func TestLabsTheSuggestedStackOfEveryLabExists(t *testing.T) {
	raw, err := os.ReadFile("../../nimbo.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Stacks map[string]any `yaml:"stacks"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, l := range labRepoCatalog(t).Labs {
		if l.Dto.Stack != nil {
			if _, ok := doc.Stacks[*l.Dto.Stack]; !ok {
				t.Errorf("%s: unknown stack %s", l.Dto.ID, *l.Dto.Stack)
			}
		}
	}
}

// labChaosTargetIDs mirrors ChaosTargets of the observe area (kept here so this area does not depend on it).
var labChaosTargetIDs = map[string]bool{
	"floci-aws": true, "floci-azure": true, "floci-gcp": true, "floci-oci": true,
	"azurite-blob": true, "azurite-queue": true, "azurite-table": true,
	"minio": true, "postgres": true, "redis": true,
}

func TestLabsEveryLabCheckUsesOnlyProvidersAndCloudsTheCatalogKnows(t *testing.T) {
	for _, l := range labRepoCatalog(t).Labs {
		for _, c := range l.Checks {
			if c.Type == "chaos.fault" {
				if target, _ := c.Str("target"); !labChaosTargetIDs[target] {
					t.Errorf("%s: unknown chaos target %s", l.Dto.ID, target)
				}
			}
		}
	}
}

func TestLabsRecipesPointAtLabsThatExist(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/guides.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	_, after, _ := strings.Cut(src, "export const RECIPES")
	recipes, _, _ := strings.Cut(after, "export interface TabHelp")
	known := map[string]bool{}
	for _, l := range labRepoCatalog(t).Labs {
		known[l.Dto.ID] = true
	}
	used := regexp.MustCompile(`lab: '([a-z0-9-]+)'`).FindAllStringSubmatch(recipes, -1)
	if len(used) == 0 {
		t.Fatal("no recipe links to a lab")
	}
	for _, m := range used {
		if !known[m[1]] {
			t.Errorf("a recipe links to the lab '%s', which does not exist", m[1])
		}
	}
}

func TestLabsAPlainStringIsUsedForBothLanguagesAndASingleLanguageIsMirrored(t *testing.T) {
	lab, problem := labParse(`
id: t
title: Only
summary: {en: English only}
steps: [Step]
checks:
  - {type: s3.bucket, name: b, label: {es: Solo español}}
`)
	if problem != "" {
		t.Fatal(problem)
	}
	if lab.Dto.Title != (L10nDto{"Only", "Only"}) || lab.Dto.Summary != (L10nDto{"English only", "English only"}) ||
		lab.Dto.Checks[0].Label != (L10nDto{"Solo español", "Solo español"}) {
		t.Fatalf("%+v", lab.Dto)
	}
}

func TestLabsInvalidLabsAreRejectedWithAReasonNamingTheFile(t *testing.T) {
	good := "{type: s3.bucket, name: b, label: L}"
	cases := []struct{ name, text, want string }{
		{"broken yaml", "not: [valid", "t.yml"},
		{"not a mapping", "- a list", "mapping"},
		{"empty document", "", "mapping"},
		{"bad id", strings.Replace(labYAML("", good), "id: t", "id: Bad_Id", 1), "id must match"},
		{"missing id", strings.Replace(labYAML("", good), "id: t\n", "", 1), "id must match"},
		{"missing title", strings.Replace(labYAML("", good), "title: T\n", "", 1), "title"},
		{"missing summary", strings.Replace(labYAML("", good), "summary: S\n", "", 1), "summary"},
		{"no steps", strings.Replace(labYAML("", good), "steps: [do it]", "steps: []", 1), "step"},
		{"blank step", strings.Replace(labYAML("", good), "steps: [do it]", "steps: ['']", 1), "a step has no text"},
		{"blank hint", labYAML("hints: ['']", good), "a hint has no text"},
		{"solution without code", labYAML("solution:\n  - {title: x}", good), "solution entry without code"},
		{"solution not a mapping", labYAML("solution: [x]", good), "solution entries need a code block"},
		{"unknown type", labYAML("", "{type: ec2.instance, name: b, label: L}"), "unknown type"},
		{"missing param", labYAML("", "{type: s3.object, bucket: b, label: L}"), "missing 'key'"},
		{"blank param", labYAML("", "{type: s3.bucket, name: '', label: L}"), "missing 'name'"},
		{"no label", labYAML("", "{type: s3.bucket, name: b}"), "no label"},
		{"path in a name", labYAML("", "{type: s3.bucket, name: ../x, label: L}"), "invalid value"},
		{"space in a name", labYAML("", "{type: sqs.messages, queue: 'a b', label: L}"), "invalid value"},
		{"shell in a name", labYAML("", "{type: s3.bucket, name: 'a;rm', label: L}"), "invalid value"},
		{"dollar in a name", labYAML("", "{type: s3.bucket, name: '$x', label: L}"), "invalid value"},
		{"angle in a name", labYAML("", "{type: s3.bucket, name: '<x>', label: L}"), "invalid value"},
		{"check not a mapping", labYAML("", "just text"), "check 1 is not a mapping"},
		{"no checks", strings.Replace(labYAML("", good), "checks:", "checks: []\nignored:", 1), "at least one check"},
		{"checks missing", "id: t\ntitle: T\nsummary: S\nsteps: [a]\n", "missing checks"},
	}
	for _, c := range cases {
		lab, problem := labParse(c.text)
		if lab != nil || !strings.Contains(problem, c.want) || !strings.HasPrefix(problem, "t.yml: ") {
			t.Errorf("%s: got %q, want it to contain %q", c.name, problem, c.want)
		}
	}
}

func TestLabsYAMLCannotInstantiateClasses(t *testing.T) {
	if lab, problem := labParse("id: !!java.lang.ProcessBuilder [[calc]]"); lab != nil || problem == "" {
		t.Fatalf("%v %q", lab, problem)
	}
}

func TestLabsOptionalFieldsDefaultAndSolutionsAreTrimmed(t *testing.T) {
	lab, problem := labParse(labYAML("level: 2\nstack: aws-basic\nhints: [h]\nsolution:\n  - title: {en: CLI, es: CLI}\n    code: |\n      aws s3 mb\n\n\n  - code: plain", "{type: s3.bucket, name: b, label: L}"))
	if problem != "" {
		t.Fatal(problem)
	}
	d := lab.Dto
	if d.Level != 2 || d.Stack == nil || *d.Stack != "aws-basic" || len(d.Hints) != 1 {
		t.Fatalf("%+v", d)
	}
	if d.Solution[0].Code != "aws s3 mb" || d.Solution[0].Title == nil || d.Solution[1].Title != nil || d.Solution[1].Code != "plain" {
		t.Fatalf("%+v", d.Solution)
	}
	plain, _ := labParse(labYAML("", "{type: s3.bucket, name: b, label: L}"))
	if plain.Dto.Level != 1 || plain.Dto.Stack != nil || len(plain.Dto.Hints) != 0 || len(plain.Dto.Solution) != 0 {
		t.Fatalf("%+v", plain.Dto)
	}
}

func TestLabsACheckSpecReadsTypedParameters(t *testing.T) {
	lab, _ := labParse(labYAML("", "{type: sqs.messages, queue: q, atLeast: 3, contains: x, label: L}"))
	c := lab.Checks[0]
	if c.Int("atLeast", 1) != 3 || c.Int("missing", 7) != 7 {
		t.Fatal("Int")
	}
	if s, ok := c.Str("contains"); !ok || s != "x" {
		t.Fatal("Str")
	}
	if _, ok := c.Str("nope"); ok {
		t.Fatal("an absent parameter is not ok")
	}
}

func TestLabsABrokenFileIsReportedButDoesNotHideTheGoodOnesDuplicatesAreFlagged(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.yml", labYAML("", "{type: s3.bucket, name: b, label: L}"))
	write("b.yml", labYAML("", "{type: s3.bucket, name: b, label: L}")) // same id "t"
	write("c.yml", "id: [broken")
	write("ignored.txt", "x")
	cat := LoadLabs(map[string]string{"NIMBO_LABS_DIR": dir}, nil)
	if len(cat.Labs) != 1 {
		t.Fatalf("%d labs", len(cat.Labs))
	}
	var broken, dup bool
	for _, p := range cat.Problems {
		broken = broken || strings.Contains(p, "c.yml")
		dup = dup || strings.Contains(p, "duplicate")
	}
	if !broken || !dup {
		t.Fatal(cat.Problems)
	}
}

func TestLabsAMissingDirectoryMeansNoLabsAndNoErrors(t *testing.T) {
	cat := LabsFromDir(filepath.Join(t.TempDir(), "definitely", "not", "here"))
	if len(cat.Labs) != 0 || len(cat.Problems) != 0 || cat.Labs == nil || cat.Problems == nil {
		t.Fatalf("%+v", cat)
	}
	// like Kotlin, an unknown NIMBO_LABS_DIR falls back to ./labs, ../labs and then the bundled copy
	if got := LoadLabs(map[string]string{"NIMBO_LABS_DIR": "/definitely/not/here"}, nil); len(got.Problems) != 0 {
		t.Fatal(got.Problems)
	}
}

func TestLabsCanBeLoadedFromAnFS(t *testing.T) {
	good := labYAML("", "{type: s3.bucket, name: b, label: L}")
	fsys := fstest.MapFS{
		"b.yml":     {Data: []byte(strings.Replace(good, "id: t", "id: second", 1))},
		"a.yml":     {Data: []byte(strings.Replace(strings.Replace(good, "id: t", "id: first", 1), "steps:", "level: 2\nsteps:", 1))},
		"notes.txt": {Data: []byte("x")},
		"sub/c.yml": {Data: []byte(good)},
	}
	cat := LabsFromFS(fsys)
	var ids []string
	for _, l := range cat.Labs {
		ids = append(ids, l.Dto.ID)
	}
	// level 1 before level 2; the subfolder is not read
	fitEq(t, ids, []string{"second", "first"})
	// a bundled FS is used when no directory exists
	if got := LoadLabs(map[string]string{"NIMBO_LABS_DIR": filepath.Join(t.TempDir(), "none")}, fsys); len(got.Labs) == 0 {
		t.Skip("a ./labs folder exists next to the tests; the bundled copy is not needed")
	}
}

func TestLabsFindReturnsNilForUnknownIDs(t *testing.T) {
	cat := labRepoCatalog(t)
	if cat.Find("first-bucket") == nil || cat.Find("ghost") != nil {
		t.Fatal("Find")
	}
}
