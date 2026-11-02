package nimbo

import (
	"bytes"
	"encoding/json"
)

// Cloud is the lowercase name the UI expects ("aws", "azure", "gcp", "oci", "other").
type Cloud string

const (
	CloudAWS   Cloud = "aws"
	CloudAzure Cloud = "azure"
	CloudGCP   Cloud = "gcp"
	CloudOCI   Cloud = "oci"
	CloudOther Cloud = "other"
)

// KV is one entry of an ordered map. Go maps have no order, and the order of a provider's endpoints decides how its
// .env file reads (and, in Kotlin, how the JSON object was written), so those maps are ordered lists here.
type KV struct{ Key, Value string }

// Vars is an ordered map of strings. It is written to JSON as an object, in order.
type Vars []KV

func (v Vars) Get(key string) (string, bool) {
	for _, kv := range v {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

func (v Vars) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.Key)
		val, _ := json.Marshal(kv.Value)
		b.Write(k)
		b.WriteByte(':')
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// PersistSpec is how a provider keeps its data between runs: a named Docker volume mounted at MountPath.
type PersistSpec struct {
	MountPath string
	// Env only makes sense when persisting (an emulator storage mode, say).
	Env Vars
	// Command replaces the container command when persisting; nil keeps the image default.
	Command []string
	// User: images that run as an unprivileged user cannot write into a fresh volume (owned by root); this runs them
	// as that user. Empty means the image default.
	User string
}

// ProviderDef is a container Nimbo knows how to run. Secrets are never stored here, only the name of the env var that
// holds them.
type ProviderDef struct {
	ID          string
	Name        string
	Cloud       Cloud
	Description string
	Image       string
	Ports       []int
	Command     []string
	// RunArgs are extra `docker run` flags placed before the image (e.g. --add-host).
	RunArgs []string
	// ContainerEnv is the container env with fixed, non-secret values.
	ContainerEnv Vars
	// Vars are the settings an emulator can be given when it starts: passwords, users, keys, tokens. Each is kept in
	// Nimbo's secret store (or read from its environment) and handed to the container; none is ever in this file.
	Vars []VarSpec
	// Exports are the client-side variables that depend on those settings (a database URL with its password). The
	// values map holds what is set, by VarSpec.Name; secrets are placeholders unless the person asked to see them.
	Exports func(values map[string]string) Vars
	// Endpoints are the variables a client should set to talk to this provider.
	Endpoints Vars
	// Persist is nil when the provider has nothing worth keeping (a proxy, an emulator without persistence).
	Persist *PersistSpec
}

// VarSpec describes one setting of an emulator.
type VarSpec struct {
	// Name is its key in the secret store and in Nimbo's environment, always NIMBO_*.
	Name string
	// Container is the variable it becomes inside the container.
	Container string
	// Secret values are masked in lists and need an explicit "reveal" to be read back.
	Secret bool
	// Required settings stop the emulator from starting when missing; optional ones only change it when set.
	Required bool
	// Generate says how Initialize makes a value: "password", "token" or "azurite" (an account:key pair).
	Generate string
	// Default is what Initialize stores when nothing is generated (a user name, a database name).
	Default string
	// FixedAtCreation: the emulator only reads it when its data is first created (PostgreSQL user, database, password).
	FixedAtCreation bool
}

type RunState string

const (
	StateNotCreated RunState = "not_created"
	StateRunning    RunState = "running"
	StateStopped    RunState = "stopped"
	StateError      RunState = "error"
)

type ProviderDto struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Cloud       string  `json:"cloud"`
	Description string  `json:"description"`
	Image       string  `json:"image"`
	Ports       []int   `json:"ports"`
	State       string  `json:"state"`
	Detail      *string `json:"detail"`
	// Persistable: can this provider keep its data in a volume?
	Persistable bool `json:"persistable"`
	// Persist: is that switched on?
	Persist bool `json:"persist"`
	// HasData: is there saved data in a volume right now?
	HasData bool `json:"hasData"`
	// NeedsSetup lists the required settings that have no value yet; the emulator cannot start until they do.
	NeedsSetup []string `json:"needsSetup"`
}

type DockerStatusDto struct {
	Available bool    `json:"available"`
	Version   *string `json:"version"`
	Error     *string `json:"error"`
}

type HealthDto struct {
	Status       string  `json:"status"`
	AuthRequired bool    `json:"authRequired"`
	Version      *string `json:"version"`
}

type LogsDto struct {
	ID    string `json:"id"`
	Lines string `json:"lines"`
}

type EnvDto struct {
	ID        string `json:"id"`
	Variables Vars   `json:"variables"`
	Dotenv    string `json:"dotenv"`
}

type StackDto struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Providers   []string `json:"providers"`
	// State is running (all up), partial, or stopped (none up).
	State   string `json:"state"`
	Running int    `json:"running"`
	Total   int    `json:"total"`
	// Custom stacks were made in the app and can be edited and deleted.
	Custom bool `json:"custom"`
}

type StackListDto struct {
	Stacks   []StackDto `json:"stacks"`
	Problems []string   `json:"problems"`
}

type StepResultDto struct {
	ID    string  `json:"id"`
	OK    bool    `json:"ok"`
	Error *string `json:"error"`
}

type StackActionDto struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Results []StepResultDto `json:"results"`
}

type ExportDto struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// coreStr is a pointer to a copy of s, for the nullable string fields of the DTOs.
func coreStr(s string) *string { return &s }
