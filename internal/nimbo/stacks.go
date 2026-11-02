package nimbo

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// StackDef is a named group of providers that start and stop together.
type StackDef struct {
	ID          string
	Description string
	Providers   []string
}

// StackCatalog holds the stacks that loaded, plus human-readable reasons for entries that were skipped.
type StackCatalog struct {
	Stacks   []StackDef
	Problems []string
}

func (c *StackCatalog) Find(id string) *StackDef {
	for i := range c.Stacks {
		if c.Stacks[i].ID == id {
			return &c.Stacks[i]
		}
	}
	return nil
}

const stackIDPattern = `[a-z0-9][a-z0-9-]{0,40}`

var stackIDRe = regexp.MustCompile(`^` + stackIDPattern + `$`)

// BuiltInStacks is what Nimbo offers when it finds no nimbo.yml anywhere.
func BuiltInStacks() *StackCatalog {
	return &StackCatalog{Stacks: []StackDef{
		{"aws-basic", "AWS emulator plus S3-compatible storage", []string{"floci-aws", "minio"}},
		{"multicloud", "AWS, Azure and GCP emulators side by side", []string{"floci-aws", "floci-azure", "floci-gcp"}},
		{"event-driven", "Messaging and data stores for event-driven labs", []string{"floci-aws", "redpanda", "postgres", "redis"}},
	}, Problems: []string{}}
}

func stackDeref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// stackGet finds a key of a mapping node.
func stackGet(m *yaml.Node, key string) *yaml.Node {
	m = stackDeref(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := stackDeref(m.Content[i]); k.Kind == yaml.ScalarNode && k.Value == key {
			return stackDeref(m.Content[i+1])
		}
	}
	return nil
}

func stackScalar(n *yaml.Node) string {
	n = stackDeref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// ParseStacks reads a nimbo.yml. Anything wrong with it is reported in Problems; it never fails. The YAML is read as
// plain data, so a document cannot ask for anything to be instantiated.
func ParseStacks(text string) *StackCatalog {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		return &StackCatalog{Stacks: []StackDef{}, Problems: []string{"Invalid YAML: " + first}}
	}
	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	entries := stackGet(root, "stacks")
	if entries == nil || entries.Kind != yaml.MappingNode {
		return &StackCatalog{Stacks: []StackDef{}, Problems: []string{"Missing top-level 'stacks' map"}}
	}

	stacks := []StackDef{}
	problems := []string{}
	for i := 0; i+1 < len(entries.Content); i += 2 {
		id := stackScalar(entries.Content[i])
		body := stackDeref(entries.Content[i+1])
		var providers []string
		if seq := stackGet(body, "providers"); seq != nil && seq.Kind == yaml.SequenceNode {
			for _, p := range seq.Content {
				p = stackDeref(p)
				if p.Kind == yaml.ScalarNode {
					providers = append(providers, p.Value)
				} else {
					providers = append(providers, "")
				}
			}
		}
		var unknown []string
		for _, p := range providers {
			if FindProvider(p) == nil {
				unknown = append(unknown, p)
			}
		}
		switch {
		case !stackIDRe.MatchString(id):
			problems = append(problems, fmt.Sprintf("Stack '%s': id must match %s", id, stackIDPattern))
		case len(providers) == 0:
			problems = append(problems, fmt.Sprintf("Stack '%s': 'providers' is empty or missing", id))
		case len(unknown) > 0:
			problems = append(problems, fmt.Sprintf("Stack '%s': unknown providers %s", id, strings.Join(unknown, ", ")))
		default:
			var distinct []string
			seen := map[string]bool{}
			for _, p := range providers {
				if !seen[p] {
					seen[p] = true
					distinct = append(distinct, p)
				}
			}
			stacks = append(stacks, StackDef{ID: id, Description: stackScalar(stackGet(body, "description")), Providers: distinct})
		}
	}
	return &StackCatalog{Stacks: stacks, Problems: problems}
}

// LoadBundledStacks reads the nimbo.yml packed inside the program (an embedded file system), if there is one.
func LoadBundledStacks(fsys fs.FS, path string) *StackCatalog {
	if fsys == nil {
		return nil
	}
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	return ParseStacks(string(b))
}

// LoadStacks looks for NIMBO_STACKS_FILE, then nimbo.yml here and one level up, and falls back to the bundled one
// (file "nimbo.yml" in bundled, which may be nil) and then to the built-in stacks.
func LoadStacks(env map[string]string, bundled fs.FS) *StackCatalog {
	// NIMBO_STACKS_FILE first. Then the copy bundled in the app: the folder the app was started from must not decide
	// which stacks it shows. Only a build without a bundled copy (made for development) looks beside where it started.
	candidates := []string{env["NIMBO_STACKS_FILE"]}
	if bundled == nil {
		candidates = append(candidates, "nimbo.yml", "../nimbo.yml")
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if st, err := os.Stat(candidate); err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(candidate)
		if err != nil {
			return &StackCatalog{Stacks: []StackDef{}, Problems: []string{"Cannot read " + candidate + ": " + err.Error()}}
		}
		return ParseStacks(string(b))
	}
	if c := LoadBundledStacks(bundled, "nimbo.yml"); c != nil {
		return c
	}
	return BuiltInStacks()
}
