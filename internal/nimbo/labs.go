package nimbo

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---- DTOs ---------------------------------------------------------------------------------------------------------

type L10nDto struct {
	En string `json:"en"`
	Es string `json:"es"`
}

type SnippetDto struct {
	Title *L10nDto `json:"title"`
	Code  string   `json:"code"`
}

type LabCheckDto struct {
	Label L10nDto `json:"label"`
}

type LabDto struct {
	ID       string        `json:"id"`
	Level    int           `json:"level"`
	Stack    *string       `json:"stack"`
	Title    L10nDto       `json:"title"`
	Summary  L10nDto       `json:"summary"`
	Steps    []L10nDto     `json:"steps"`
	Hints    []L10nDto     `json:"hints"`
	Solution []SnippetDto  `json:"solution"`
	Checks   []LabCheckDto `json:"checks"`
}

type LabListDto struct {
	Labs     []LabDto `json:"labs"`
	Problems []string `json:"problems"`
}

type LabCheckResultDto struct {
	Index  int     `json:"index"`
	Ok     bool    `json:"ok"`
	Detail *string `json:"detail"`
}

type LabVerifyDto struct {
	ID      string              `json:"id"`
	Passed  bool                `json:"passed"`
	Results []LabCheckResultDto `json:"results"`
}

// ---- Catalog ------------------------------------------------------------------------------------------------------

// CheckSpec is one check of a lab with its raw parameters (never sent to the UI).
type CheckSpec struct {
	Type   string
	Params map[string]any
}

// Str is the parameter as text; ok is false when it is absent or null.
func (c CheckSpec) Str(key string) (string, bool) {
	v, present := c.Params[key]
	if !present || v == nil {
		return "", false
	}
	return labText(v), true
}

// Int is the numeric parameter, or def when it is absent or not a number.
func (c CheckSpec) Int(key string, def int) int {
	if labIsNumber(c.Params[key]) {
		f, _ := labNumber(c.Params[key])
		return int(f)
	}
	return def
}

type LabDef struct {
	Dto    LabDto
	Checks []CheckSpec
}

type LabCatalog struct {
	Labs     []LabDef
	Problems []string
}

func (c *LabCatalog) Find(id string) *LabDef {
	for i := range c.Labs {
		if c.Labs[i].Dto.ID == id {
			return &c.Labs[i]
		}
	}
	return nil
}

var (
	labIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	labNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
)

const labIDPattern = "[a-z0-9][a-z0-9-]{0,40}"

// labRequired maps a check type to its required string params (an `atLeast`-style number is always optional).
var labRequired = map[string][]string{
	"s3.bucket":           {"name"},
	"s3.object":           {"bucket", "key"},
	"sqs.queue":           {"name"},
	"sqs.messages":        {"queue"},
	"dynamodb.table":      {"name"},
	"dynamodb.items":      {"table"},
	"pubsub.topic":        {"name"},
	"pubsub.subscription": {"name"},
	"chaos.fault":         {"target", "preset"},
	"k8s.deployment":      {"name"},
	"k8s.service":         {"name"},
	"trace.service":       {"name"},
}

// labNamed are the parameters that end up in URLs and commands, so they must be plain names.
var labNamed = []string{"attribute", "bucket", "namespace", "name", "preset", "project", "queue", "serviceType", "table", "target", "topic"}

// labText renders a YAML scalar the way the lab text and checks expect it.
func labText(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case float64:
		s := strconv.FormatFloat(t, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return fmt.Sprint(v)
}

func labNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case interface{ Float64() (float64, error) }: // json.Number
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

func labIsNumber(v any) bool {
	switch v.(type) {
	case int, int64, uint64, float64, float32:
		return true
	}
	return false
}

// labMap reads a YAML mapping whatever the key type is.
func labMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[labText(k)] = val
		}
		return out, true
	}
	return nil, false
}

// labL10n: a plain string is used for both languages and a single language is mirrored.
func labL10n(v any) (L10nDto, bool) {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return L10nDto{}, false
		}
		return L10nDto{t, t}, true
	default:
		m, ok := labMap(v)
		if !ok {
			return L10nDto{}, false
		}
		pick := func(k string) string {
			if x, present := m[k]; present && x != nil {
				if s := labText(x); strings.TrimSpace(s) != "" {
					return s
				}
			}
			return ""
		}
		en, es := pick("en"), pick("es")
		if en == "" && es == "" {
			return L10nDto{}, false
		}
		if en == "" {
			en = es
		}
		if es == "" {
			es = en
		}
		return L10nDto{en, es}, true
	}
}

// ParseLab parses one lab file. The problem names the file, so a broken lab is reported and never fatal.
func ParseLab(text, source string) (*LabDef, string) {
	var parsed any
	if err := yaml.Unmarshal([]byte(text), &parsed); err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		return nil, fmt.Sprintf("%s: invalid YAML (%s)", source, first)
	}
	root, ok := labMap(parsed)
	if !ok {
		return nil, source + ": expected a mapping at the top level"
	}
	fail := func(msg string) (*LabDef, string) { return nil, source + ": " + msg }

	id := ""
	if v, present := root["id"]; present && v != nil {
		id = labText(v)
	}
	if !labIDRe.MatchString(id) {
		return fail("id must match " + labIDPattern)
	}
	level := 1
	if f, ok := labNumber(root["level"]); ok && labIsNumber(root["level"]) {
		level = int(f)
	}
	title, ok := labL10n(root["title"])
	if !ok {
		return fail("missing title")
	}
	summary, ok := labL10n(root["summary"])
	if !ok {
		return fail("missing summary")
	}

	steps := []L10nDto{}
	if list, ok := root["steps"].([]any); ok {
		for _, s := range list {
			t, ok := labL10n(s)
			if !ok {
				return fail("a step has no text")
			}
			steps = append(steps, t)
		}
	}
	if len(steps) == 0 {
		return fail("needs at least one step")
	}
	hints := []L10nDto{}
	if list, ok := root["hints"].([]any); ok {
		for _, h := range list {
			t, ok := labL10n(h)
			if !ok {
				return fail("a hint has no text")
			}
			hints = append(hints, t)
		}
	}
	solution := []SnippetDto{}
	if list, ok := root["solution"].([]any); ok {
		for _, s := range list {
			m, ok := labMap(s)
			if !ok {
				return fail("solution entries need a code block")
			}
			code, present := m["code"]
			if !present || code == nil {
				return fail("solution entry without code")
			}
			snippet := SnippetDto{Code: strings.TrimRight(labText(code), " \t\r\n")}
			if t, ok := labL10n(m["title"]); ok {
				snippet.Title = &t
			}
			solution = append(solution, snippet)
		}
	}

	rawChecks, ok := root["checks"].([]any)
	if !ok {
		return fail("missing checks")
	}
	if len(rawChecks) == 0 {
		return fail("needs at least one check")
	}
	checks := []CheckSpec{}
	checkDtos := []LabCheckDto{}
	for i, raw := range rawChecks {
		n := i + 1
		m, ok := labMap(raw)
		if !ok {
			return fail(fmt.Sprintf("check %d is not a mapping", n))
		}
		typ := ""
		if v, present := m["type"]; present && v != nil {
			typ = labText(v)
		}
		required, known := labRequired[typ]
		if !known {
			return fail(fmt.Sprintf("check %d: unknown type '%s'", n, typ))
		}
		for _, key := range required {
			if v, present := m[key]; !present || v == nil || strings.TrimSpace(labText(v)) == "" {
				return fail(fmt.Sprintf("check %d (%s): missing '%s'", n, typ, key))
			}
		}
		for _, key := range labNamed {
			if v, present := m[key]; present && !labNameRe.MatchString(labText(v)) {
				return fail(fmt.Sprintf("check %d (%s): '%s' has an invalid value", n, typ, key))
			}
		}
		label, ok := labL10n(m["label"])
		if !ok {
			return fail(fmt.Sprintf("check %d has no label", n))
		}
		checkDtos = append(checkDtos, LabCheckDto{label})
		checks = append(checks, CheckSpec{typ, m})
	}
	var stack *string
	if v, present := root["stack"]; present && v != nil {
		s := labText(v)
		stack = &s
	}
	return &LabDef{LabDto{id, level, stack, title, summary, steps, hints, solution, checkDtos}, checks}, ""
}

// labSource is one lab file's name and text.
type labSource struct{ name, text string }

// LabsFromDir reads every *.yml of the directory, ordered by level then id.
func LabsFromDir(dir string) *LabCatalog {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &LabCatalog{Labs: []LabDef{}, Problems: []string{}}
	}
	var sources []labSource
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".yml" {
			continue
		}
		b, err := os.ReadFile(dir + string(os.PathSeparator) + e.Name())
		if err != nil {
			continue
		}
		sources = append(sources, labSource{e.Name(), string(b)})
	}
	return labAssemble(sources)
}

// LabsFromFS reads every *.yml at the root of fsys (an embedded copy of labs/, for instance).
func LabsFromFS(fsys fs.FS) *LabCatalog {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return &LabCatalog{Labs: []LabDef{}, Problems: []string{}}
	}
	var sources []labSource
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".yml" {
			continue
		}
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			continue
		}
		sources = append(sources, labSource{e.Name(), string(b)})
	}
	return labAssemble(sources)
}

// LoadLabs finds the labs the way the Kotlin app did: NIMBO_LABS_DIR, then ./labs, then ../labs, and finally the
// bundled copy (may be nil, which means no labs).
func LoadLabs(env map[string]string, bundled fs.FS) *LabCatalog {
	for _, dir := range []string{env["NIMBO_LABS_DIR"], "labs", "../labs"} {
		if dir == "" {
			continue
		}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return LabsFromDir(dir)
		}
	}
	if bundled == nil {
		return &LabCatalog{Labs: []LabDef{}, Problems: []string{}}
	}
	return LabsFromFS(bundled)
}

func labAssemble(sources []labSource) *LabCatalog {
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	labs := []LabDef{}
	problems := []string{}
	count := map[string]int{}
	var duplicated []string
	for _, s := range sources {
		lab, problem := ParseLab(s.text, s.name)
		if lab != nil {
			labs = append(labs, *lab)
			count[lab.Dto.ID]++
			if count[lab.Dto.ID] == 2 {
				duplicated = append(duplicated, lab.Dto.ID)
			}
		}
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	for _, id := range duplicated {
		problems = append(problems, fmt.Sprintf("duplicate lab id '%s'", id))
	}
	seen := map[string]bool{}
	distinct := labs[:0:0]
	for _, l := range labs {
		if !seen[l.Dto.ID] {
			seen[l.Dto.ID] = true
			distinct = append(distinct, l)
		}
	}
	sort.SliceStable(distinct, func(i, j int) bool {
		a, b := distinct[i].Dto, distinct[j].Dto
		if a.Level != b.Level {
			return a.Level < b.Level
		}
		return a.ID < b.ID
	})
	return &LabCatalog{Labs: distinct, Problems: problems}
}
