package nimbo

import (
	"strings"
	"sync"
)

// coreChaosTarget mirrors the ports of the Chaos area (Chaos.kt) that the catalog and the exports need: which
// emulator port Toxiproxy fronts and where. It is private to core so the two areas do not share a type.
type coreChaosTarget struct {
	id           string
	upstreamPort int
	proxyPort    int
}

const coreToxiproxyAPIPort = 8474

var coreChaosTargets = []coreChaosTarget{
	{"floci-aws", 4566, 14566},
	{"floci-azure", 4577, 14577},
	{"floci-gcp", 4588, 14588},
	{"floci-oci", 4599, 14599},
	{"azurite-blob", 10000, 20000},
	{"azurite-queue", 10001, 20001},
	{"azurite-table", 10002, 20002},
	{"minio", 9000, 19000},
	{"postgres", 5432, 15432},
	{"redis", 6379, 16379},
}

func coreChaosTargetByID(id string) *coreChaosTarget {
	for i := range coreChaosTargets {
		if coreChaosTargets[i].id == id {
			return &coreChaosTargets[i]
		}
	}
	return nil
}

// catalogPersistence is what each provider needs to keep its data between runs, taken from its own documentation.
// Providers that are absent have nothing to keep: Toxiproxy is stateless and Floci OCI documents no persistence.
func catalogPersistence() map[string]*PersistSpec {
	return map[string]*PersistSpec{
		"floci-aws":   {MountPath: "/app/data", Env: Vars{{"FLOCI_STORAGE_MODE", "hybrid"}, {"FLOCI_STORAGE_PERSISTENT_PATH", "/app/data"}}},
		"floci-azure": {MountPath: "/app/data", Env: Vars{{"FLOCI_AZ_STORAGE_MODE", "hybrid"}, {"FLOCI_AZ_STORAGE_PATH", "/app/data"}}},
		"floci-gcp":   {MountPath: "/app/data", Env: Vars{{"FLOCI_GCP_STORAGE_MODE", "hybrid"}, {"FLOCI_GCP_STORAGE_PERSISTENT_PATH", "/app/data"}}},
		"azurite":     {MountPath: "/data"},
		"minio":       {MountPath: "/data", User: "0:0"},
		"redpanda":    {MountPath: "/var/lib/redpanda/data"},
		// The official image declares this folder as a volume; naming it makes the data survive removing the container.
		"postgres": {MountPath: "/var/lib/postgresql/data"},
		"redis":    {MountPath: "/data", Command: []string{"sh", "-c", redisStartScript(true)}},
		"k3s":      {MountPath: "/var/lib/rancher/k3s"},
		"jaeger": {
			MountPath: "/badger",
			Env: Vars{
				{"SPAN_STORAGE_TYPE", "badger"}, {"BADGER_EPHEMERAL", "false"},
				{"BADGER_DIRECTORY_VALUE", "/badger/data"}, {"BADGER_DIRECTORY_KEY", "/badger/key"},
			},
			User: "0:0",
		},
	}
}

func catalogProviders() []ProviderDef {
	toxiPorts := []int{coreToxiproxyAPIPort}
	for _, t := range coreChaosTargets {
		toxiPorts = append(toxiPorts, t.proxyPort)
	}
	k3sPorts := []int{6443}
	for p := 30080; p <= 30084; p++ {
		k3sPorts = append(k3sPorts, p)
	}
	defs := []ProviderDef{
		{
			ID: "floci-aws", Name: "Floci AWS", Cloud: CloudAWS,
			Description: "AWS emulator (S3, SQS, SNS, DynamoDB, Lambda...). MIT.",
			Image:       "floci/floci",
			Ports:       []int{4566},
			Endpoints: Vars{
				{"AWS_ENDPOINT_URL", "http://localhost:4566"},
				{"AWS_REGION", "us-east-1"},
				// Floci accepts any credentials; these are dummy values, not secrets.
				{"AWS_ACCESS_KEY_ID", "test"},
				{"AWS_SECRET_ACCESS_KEY", "test"},
			},
		},
		{
			ID: "floci-azure", Name: "Floci Azure", Cloud: CloudAzure,
			Description: "Azure emulator. MIT.",
			Image:       "floci/floci-az",
			Ports:       []int{4577},
			// One port, path-style routing. The account key is Azurite's documented development key; set it in your client.
			Endpoints: Vars{
				{"NIMBO_AZURE_ENDPOINT", "http://localhost:4577"},
				{"AZURE_STORAGE_BLOB_ENDPOINT", "http://localhost:4577/devstoreaccount1"},
				{"AZURE_STORAGE_QUEUE_ENDPOINT", "http://localhost:4577/devstoreaccount1-queue"},
				{"AZURE_STORAGE_TABLE_ENDPOINT", "http://localhost:4577/devstoreaccount1-table"},
			},
		},
		{
			ID: "floci-gcp", Name: "Floci GCP", Cloud: CloudGCP,
			Description: "Google Cloud emulator. MIT.",
			Image:       "floci/floci-gcp",
			Ports:       []int{4588},
			Endpoints: Vars{
				{"NIMBO_GCP_ENDPOINT", "http://localhost:4588"},
				{"GOOGLE_CLOUD_PROJECT", "floci-local"},
				{"PUBSUB_EMULATOR_HOST", "localhost:4588"},
				{"STORAGE_EMULATOR_HOST", "http://localhost:4588"},
				{"SECRET_MANAGER_EMULATOR_HOST", "localhost:4588"},
			},
		},
		{
			ID: "floci-oci", Name: "Floci OCI", Cloud: CloudOCI,
			Description: "Oracle Cloud emulator. MIT.",
			Image:       "floci/floci-oci",
			Ports:       []int{4599},
			Endpoints:   Vars{{"NIMBO_OCI_ENDPOINT", "http://localhost:4599"}},
		},
		{
			ID: "azurite", Name: "Azurite", Cloud: CloudAzure,
			Description: "Official Azure Storage emulator (Blob, Queue, Table).",
			Image:       "mcr.microsoft.com/azure-storage/azurite",
			Ports:       []int{10000, 10001, 10002},
			// Without an account, Azurite keeps its well-known development account.
			Vars:      []VarSpec{{Name: "NIMBO_AZURITE_ACCOUNTS", Container: "AZURITE_ACCOUNTS", Secret: true, Generate: "azurite"}},
			Endpoints: Vars{{"AZURE_STORAGE_CONNECTION_STRING", "UseDevelopmentStorage=true"}},
			Exports: func(v map[string]string) Vars {
				name, key, ok := strings.Cut(strings.SplitN(v["NIMBO_AZURITE_ACCOUNTS"], ";", 2)[0], ":")
				if !ok || name == "" {
					return Vars{}
				}
				key = strings.SplitN(key, ":", 2)[0]
				return Vars{{"AZURE_STORAGE_CONNECTION_STRING", "DefaultEndpointsProtocol=http;AccountName=" + name + ";AccountKey=" + key +
					";BlobEndpoint=http://127.0.0.1:10000/" + name + ";QueueEndpoint=http://127.0.0.1:10001/" + name + ";TableEndpoint=http://127.0.0.1:10002/" + name + ";"}}
			},
		},
		{
			ID: "minio", Name: "MinIO", Cloud: CloudOther,
			Description: "S3-compatible object storage, handy as a Terraform state backend.",
			// MinIO stopped publishing community images (Docker Hub and quay.io no longer serve them); Chainguard rebuilds them from source.
			Image:   "cgr.dev/chainguard/minio",
			Ports:   []int{9000, 9001},
			Command: []string{"server", "/data", "--console-address", ":9001"},
			Vars: []VarSpec{
				{Name: "NIMBO_MINIO_USER", Container: "MINIO_ROOT_USER", Required: true, Default: "nimbo"},
				{Name: "NIMBO_MINIO_PASSWORD", Container: "MINIO_ROOT_PASSWORD", Secret: true, Required: true, Generate: "password"},
			},
			Endpoints: Vars{{"MINIO_ENDPOINT", "http://localhost:9000"}},
			Exports: func(v map[string]string) Vars {
				out := Vars{}
				if v["NIMBO_MINIO_USER"] != "" {
					// MinIO speaks S3: the root user and password are the access key and secret of any S3 client.
					out = append(out, KV{"MINIO_ROOT_USER", v["NIMBO_MINIO_USER"]}, KV{"AWS_ACCESS_KEY_ID", v["NIMBO_MINIO_USER"]})
				}
				if v["NIMBO_MINIO_PASSWORD"] != "" {
					out = append(out, KV{"MINIO_ROOT_PASSWORD", v["NIMBO_MINIO_PASSWORD"]}, KV{"AWS_SECRET_ACCESS_KEY", v["NIMBO_MINIO_PASSWORD"]})
				}
				return out
			},
		},
		{
			ID: "redpanda", Name: "Redpanda", Cloud: CloudOther,
			Description: "Kafka-compatible streaming, single node.",
			Image:       "redpandadata/redpanda",
			Ports:       []int{9092},
			Command: []string{
				"redpanda", "start", "--overprovisioned", "--smp", "1", "--memory", "1G",
				"--reserve-memory", "0M", "--node-id", "0", "--check=false",
				"--kafka-addr", "PLAIN://0.0.0.0:9092",
				"--advertise-kafka-addr", "PLAIN://localhost:9092",
			},
			Endpoints: Vars{{"KAFKA_BOOTSTRAP_SERVERS", "localhost:9092"}},
		},
		{
			ID: "postgres", Name: "PostgreSQL", Cloud: CloudOther,
			Description: "Relational database for RDS / Cloud SQL style practice.",
			Image:       "postgres:17-alpine",
			Ports:       []int{5432},
			Vars: []VarSpec{
				{Name: "NIMBO_POSTGRES_PASSWORD", Container: "POSTGRES_PASSWORD", Secret: true, Required: true, Generate: "password", FixedAtCreation: true},
				// User and database are only read when the data folder is first created; the image defaults are postgres.
				{Name: "NIMBO_POSTGRES_USER", Container: "POSTGRES_USER", Default: "nimbo", FixedAtCreation: true},
				{Name: "NIMBO_POSTGRES_DB", Container: "POSTGRES_DB", Default: "nimbo", FixedAtCreation: true},
			},
			Endpoints: Vars{{"DATABASE_HOST", "localhost"}, {"DATABASE_PORT", "5432"}},
			Exports: func(v map[string]string) Vars {
				user, db, pw := orDefault(v["NIMBO_POSTGRES_USER"], "postgres"), orDefault(v["NIMBO_POSTGRES_DB"], orDefault(v["NIMBO_POSTGRES_USER"], "postgres")), v["NIMBO_POSTGRES_PASSWORD"]
				out := Vars{{"DATABASE_USER", user}, {"DATABASE_NAME", db}}
				if pw != "" {
					out = append(out, KV{"DATABASE_PASSWORD", pw}, KV{"DATABASE_URL", "postgres://" + user + ":" + pw + "@localhost:5432/" + db})
				}
				return out
			},
		},
		{
			ID: "toxiproxy", Name: "Toxiproxy", Cloud: CloudOther,
			Description: "Chaos proxy: latency, outages and resets in front of the emulators (see the Chaos tab).",
			Image:       "ghcr.io/shopify/toxiproxy",
			Ports:       toxiPorts,
			// Lets the proxy reach emulators published on the host; Docker Desktop resolves it already, Linux needs the flag.
			RunArgs:   []string{"--add-host=host.docker.internal:host-gateway"},
			Endpoints: Vars{{"TOXIPROXY_URL", "http://localhost:8474"}},
		},
		{
			ID: "k3s", Name: "Kubernetes (k3s)", Cloud: CloudOther,
			Description:  "Single-node Kubernetes. Runs privileged, which k3s needs; kubectl lives inside the container.",
			Image:        "rancher/k3s",
			Ports:        k3sPorts,
			Command:      []string{"server", "--disable=traefik"},
			RunArgs:      []string{"--privileged", "--tmpfs", "/run", "--tmpfs", "/var/run"},
			ContainerEnv: Vars{{"K3S_KUBECONFIG_MODE", "644"}},
			Vars:         []VarSpec{{Name: "NIMBO_K3S_TOKEN", Container: "K3S_TOKEN", Secret: true, Generate: "token"}},
			Endpoints:    Vars{{"KUBERNETES_API", "https://127.0.0.1:6443"}},
			Exports: func(v map[string]string) Vars {
				if v["NIMBO_K3S_TOKEN"] == "" {
					return Vars{}
				}
				return Vars{{"K3S_TOKEN", v["NIMBO_K3S_TOKEN"]}}
			},
		},
		{
			ID: "jaeger", Name: "Jaeger", Cloud: CloudOther,
			Description: "Distributed tracing backend with OTLP intake; traces show up in the Traces tab.",
			Image:       "jaegertracing/all-in-one",
			Ports:       []int{16686, 4317, 4318},
			Endpoints: Vars{
				{"OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318"},
				{"JAEGER_UI", "http://localhost:16686"},
			},
		},
		{
			ID: "redis", Name: "Redis", Cloud: CloudOther,
			Description: "Cache / queue practice (ElastiCache, Memorystore).",
			Image:       "redis:7-alpine",
			Ports:       []int{6379},
			// Without a password Redis starts as before; with one, requirepass is added. The password reaches Redis as an
			// environment variable and is expanded by the shell, so it never shows up in the command line.
			Command: []string{"sh", "-c", redisStartScript(false)},
			Vars: []VarSpec{
				{Name: "NIMBO_REDIS_PASSWORD", Container: "REDIS_PASSWORD", Secret: true, Generate: "password"},
			},
			Endpoints: Vars{{"REDIS_URL", "redis://localhost:6379"}},
			Exports: func(v map[string]string) Vars {
				if v["NIMBO_REDIS_PASSWORD"] == "" {
					return Vars{}
				}
				return Vars{{"REDIS_PASSWORD", v["NIMBO_REDIS_PASSWORD"]}, {"REDIS_URL", "redis://:" + v["NIMBO_REDIS_PASSWORD"] + "@localhost:6379"}}
			},
		},
	}
	persistence := catalogPersistence()
	for i := range defs {
		defs[i].Persist = persistence[defs[i].ID]
	}
	return defs
}

// CatalogProviders is the built-in list of providers, in the order the UI shows them. Treat it as read-only.
var CatalogProviders = catalogProviders()

// customProviders are the images a person registered (see registry_images.go). The registry replaces the whole list
// whenever it changes, so readers get a consistent snapshot without holding any lock while they work.
var (
	customMu        sync.RWMutex
	customProviders []ProviderDef
)

// SetCustomProviders replaces the registered images. Only the registry calls it.
func SetCustomProviders(defs []ProviderDef) {
	customMu.Lock()
	customProviders = append([]ProviderDef(nil), defs...)
	customMu.Unlock()
}

// AllProviders is every provider Nimbo can run: the built-in ones first, in catalog order, then the registered images.
// Runtime code uses this; CatalogProviders stays "built-in only" for the catalog's own checks.
func AllProviders() []ProviderDef {
	customMu.RLock()
	defer customMu.RUnlock()
	out := make([]ProviderDef, 0, len(CatalogProviders)+len(customProviders))
	out = append(out, CatalogProviders...)
	return append(out, customProviders...)
}

// FindProvider returns the built-in or registered provider with that id, or nil.
func FindProvider(id string) *ProviderDef {
	if d := CatalogFind(id); d != nil {
		return d
	}
	customMu.RLock()
	defer customMu.RUnlock()
	for i := range customProviders {
		if customProviders[i].ID == id {
			d := customProviders[i]
			return &d
		}
	}
	return nil
}

// CatalogFind returns the built-in provider with that id, or nil.
func CatalogFind(id string) *ProviderDef {
	for i := range CatalogProviders {
		if CatalogProviders[i].ID == id {
			return &CatalogProviders[i]
		}
	}
	return nil
}

// redisStartScript starts Redis, adding requirepass only when REDIS_PASSWORD is set and, when the data is kept,
// the append-only file. The script is a fixed text: the password is read from the environment by the shell.
func redisStartScript(appendOnly bool) string {
	flags := ""
	if appendOnly {
		flags = " --appendonly yes"
	}
	return "exec redis-server" + flags + ` ${REDIS_PASSWORD:+--requirepass "$REDIS_PASSWORD"}`
}
